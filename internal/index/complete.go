package index

import (
	"sort"
	"strings"

	"github.com/jannovy/iderm/internal/syntax"
)

// Extra kinds used only by completion.
const (
	Variable SymKind = iota + Property + 1
	Keyword
)

// CompletionItem is one suggestion.
type CompletionItem struct {
	Label  string
	Kind   SymKind
	Detail string // signature or type
	Owner  string // declaring class (short name) of a member
	Insert string
	Call   bool   // insert parentheses
	Params bool   // the call takes parameters
	Import string // use statement to add, e.g. "App\Model\User" or "function App\helper"
	score  int
	boost  int // ranking bonus, e.g. own class members before inherited ones
}

// Completion is the result of Complete.
type Completion struct {
	Line, Col   int // start of the text that the item replaces
	Prefix      string
	Member      bool // after -> or ::
	Items       []CompletionItem
	ImportLine  int  // line where a use statement goes
	ImportBlank bool // separate the new use statement by an empty line
}

const maxCompletions = 150

var keywordList = strings.Fields(`abstract array as break case catch class clone const continue declare default do echo
	else elseif empty enum extends false final finally fn for foreach function global if implements include
	include_once instanceof interface isset list match namespace new null parent print private protected public
	readonly require require_once return self static switch throw trait true try unset use while yield`)

var superglobals = strings.Fields(`$_GET $_POST $_SERVER $_SESSION $_COOKIE $_FILES $_ENV $_REQUEST $GLOBALS`)

// noCompletionAfter lists words after which a new name is being declared.
var noCompletionAfter = toSet(`function class interface trait enum const namespace as goto`)

// classContext lists words after which only class names make sense.
var classContext = toSet(`new extends implements instanceof insteadof`)

// Complete returns suggestions for the cursor at (line, col). Without
// manual, nothing is offered unless the context is clear enough.
func (ix *Index) Complete(path string, src Lines, line, col int, manual bool) *Completion {
	if inStringOrComment(src, line, col) {
		return nil
	}
	toks := Tokenize(src)
	f := Parse(path, toks)
	if path != "" {
		ix.UpdateInfo(f)
	}
	ix.mu.RLock()
	defer ix.mu.RUnlock()

	k := -1
	for i, t := range toks {
		if t.Kind == TDoc {
			continue
		}
		if t.Line > line || t.Line == line && t.Col >= col {
			break
		}
		k = i
	}
	r := &resolver{ix: ix, toks: toks, f: f, cur: f.ClassAt(line, col)}
	c := &Completion{Line: line, Col: col}
	c.ImportLine, c.ImportBlank = f.importPosition()

	atCursor := k >= 0 && toks[k].Line == line && toks[k].End == col
	t := r.tok(k)
	switch {
	case atCursor && t.Kind == TIdent && (r.tok(k-1).is("->") || r.tok(k-1).is("::")):
		c.Prefix = t.Text
		c.Member = true
		ix.memberCompletion(r, c, k-1)
	case atCursor && (t.is("->") || t.is("::")):
		c.Member = true
		ix.memberCompletion(r, c, k)
	case atCursor && (t.Kind == TVar || t.is("$")):
		c.Prefix = t.Text
		r.variableCompletion(c, k)
	case atCursor && t.Kind == TIdent:
		c.Prefix = t.Text
		if !manual && len([]rune(t.Text)) < 2 {
			return nil
		}
		ix.globalCompletion(r, c, k-1)
	case manual:
		ix.globalCompletion(r, c, k)
	default:
		return nil
	}
	c.Col = col - len([]rune(c.Prefix))
	c.finish()
	if len(c.Items) == 0 {
		return nil
	}
	return c
}

func inStringOrComment(src Lines, line, col int) bool {
	if col == 0 || line >= src.LineCount() {
		return false
	}
	st := syntax.Initial
	var kinds []syntax.Kind
	for i := 0; i <= line; i++ {
		kinds, st = syntax.Lex(src.Line(i), st)
	}
	if col-1 >= len(kinds) {
		return false
	}
	switch kinds[col-1] {
	case syntax.String, syntax.Comment, syntax.HTML:
		return true
	}
	return false
}

// importPosition returns where a new use statement belongs.
func (f *FileInfo) importPosition() (int, bool) {
	switch {
	case f.LastUseLine >= 0:
		return f.LastUseLine + 1, false
	case f.NamespaceLine >= 0:
		return f.NamespaceLine + 1, true
	}
	return 1, true
}

func shortName(fqn string) string { return fqn[strings.LastIndexByte(fqn, '\\')+1:] }

func namespaceOf(fqn string) string {
	if i := strings.LastIndexByte(fqn, '\\'); i >= 0 {
		return fqn[:i]
	}
	return ""
}

func memberItem(s *Symbol, staticAccess bool) CompletionItem {
	it := CompletionItem{Label: s.Name, Kind: s.Kind, Owner: shortName(s.Class), Insert: s.Name}
	typ := ""
	if s.Type != "" && s.Type != staticType {
		typ = shortName(s.Type)
	} else if s.Type == staticType {
		typ = "static"
	}
	switch s.Kind {
	case Method:
		it.Detail = s.Params
		if typ != "" {
			it.Detail += ": " + typ
		}
		it.Call, it.Params = true, s.Params != "" && s.Params != "()"
	case Property:
		if staticAccess {
			it.Label, it.Insert = "$"+s.Name, "$"+s.Name
		}
		it.Detail = typ
	case Const:
		it.Detail = "const"
	}
	if s.Static && s.Kind == Method {
		it.Detail = "static " + it.Detail
	}
	if s.Visibility != "" && s.Visibility != "public" {
		it.Detail = s.Visibility + " " + it.Detail
	}
	return it
}

// memberCompletion lists members of the receiver before the operator at op.
func (ix *Index) memberCompletion(r *resolver, c *Completion, op int) {
	staticAccess := r.tok(op).is("::")
	recv := r.tok(op - 1)
	cls := r.exprType(op-1, 0)
	inside := recv.Text == "$this" || recv.Kind == TIdent && (strings.EqualFold(recv.Text, "self") || strings.EqualFold(recv.Text, "static") || strings.EqualFold(recv.Text, "parent")) ||
		r.cur != nil && strings.EqualFold(cls, r.cur.FQN)
	if cls == "" {
		// Unknown receiver: offer members of every class that match the prefix.
		if c.Prefix == "" {
			return
		}
		lp := strings.ToLower(c.Prefix)
		for name, syms := range ix.members {
			if !strings.HasPrefix(name, lp) {
				continue
			}
			for _, s := range syms {
				if s.Visibility == "public" && (s.Kind == Method || s.Kind == Property && !staticAccess || s.Kind == Const && staticAccess) {
					c.Items = append(c.Items, memberItem(s, staticAccess))
				}
			}
		}
		return
	}
	if staticAccess {
		c.Items = append(c.Items, CompletionItem{Label: "class", Kind: Keyword, Insert: "class", Detail: "class name"})
	}
	seen := map[string]bool{}
	visited := map[string]bool{}
	queue := []string{cls}
	magic := strings.HasPrefix(c.Prefix, "_")
	for level := 0; len(queue) > 0 && len(visited) < 64; level++ {
		name := queue[0]
		queue = queue[1:]
		if name == "" || visited[strings.ToLower(name)] {
			continue
		}
		visited[strings.ToLower(name)] = true
		for _, ci := range ix.classesByFQN(name) {
			for _, s := range ci.Members {
				key := s.Kind.String() + strings.ToLower(s.Name)
				if seen[key] {
					continue
				}
				switch {
				case s.Visibility == "private" && (!inside || !strings.EqualFold(s.Class, cls)):
					continue
				case s.Visibility == "protected" && !inside:
					continue
				case staticAccess && s.Kind == Property && !s.Static:
					continue
				case staticAccess && s.Kind == Method && !s.Static && !inside:
					continue
				case !staticAccess && (s.Kind == Const || s.Static):
					continue // static members are reached with ::
				case s.Kind == Method && strings.HasPrefix(s.Name, "__") && !magic:
					continue // __construct, __get … only when asked for
				}
				seen[key] = true
				it := memberItem(s, staticAccess)
				it.boost = -min(level, 5) * 20
				c.Items = append(c.Items, it)
			}
			queue = append(queue, ci.Traits...)
			queue = append(queue, ci.Extends...)
			queue = append(queue, ci.Implements...)
		}
	}
}

// variableCompletion lists variables of the enclosing function.
func (r *resolver) variableCompletion(c *Completion, k int) {
	seen := map[string]bool{}
	add := func(name, detail string) {
		if !seen[name] {
			seen[name] = true
			c.Items = append(c.Items, CompletionItem{Label: name, Kind: Variable, Insert: name, Detail: detail})
		}
	}
	if r.cur != nil {
		add("$this", shortName(r.cur.FQN))
	}
	for i := k - 1; i >= 0; i-- {
		t := r.toks[i]
		if t.Kind == TVar && t.Text != "$this" {
			add(t.Text, "")
		}
		if t.isWord("function") || t.isWord("fn") {
			// Parameters come after the keyword.
			for j := i + 1; j < k && !r.toks[j].is("{"); j++ {
				if r.toks[j].Kind == TVar {
					add(r.toks[j].Text, "parameter")
				}
			}
			break
		}
	}
	for _, g := range superglobals {
		add(g, "superglobal")
	}
}

// globalCompletion lists functions, classes and keywords; prev is the token before the prefix.
func (ix *Index) globalCompletion(r *resolver, c *Completion, prev int) {
	p := r.tok(prev)
	if p.Kind == TIdent && noCompletionAfter[strings.ToLower(p.Text)] {
		return
	}
	sameLine := p.Line == c.Line
	if sameLine && (p.Kind == TVar || p.Kind == TIdent && !classContext[strings.ToLower(p.Text)] && !syntax.IsKeyword(p.Text)) {
		return // "$a foo" or "Foo bar": a name follows a value, nothing sensible to offer
	}
	f := r.f
	inUse := r.inUseStatement(prev + 1)
	classesOnly := inUse || p.Kind == TIdent && classContext[strings.ToLower(p.Text)] ||
		p.is("(") && r.tok(prev-1).isWord("catch") || p.is(":") && r.tok(prev-1).is(")") || p.is("?") || p.is("|")
	qualified := strings.Contains(c.Prefix, "\\")

	imported := map[string]bool{}
	for _, fq := range f.Uses {
		imported[strings.ToLower(fq)] = true
	}
	instantiable := p.isWord("new")
	addClass := func(ci *ClassInfo) {
		if instantiable && ci.Kind != Class {
			return // interfaces, traits and enums cannot be instantiated
		}
		it := CompletionItem{Label: ci.Name, Kind: ci.Kind, Insert: ci.Name, Detail: namespaceOf(ci.FQN)}
		switch {
		case inUse || qualified:
			it.Label, it.Insert = ci.FQN, ci.FQN
			if strings.HasPrefix(c.Prefix, "\\") {
				it.Label, it.Insert = "\\"+ci.FQN, "\\"+ci.FQN
			}
		case !strings.EqualFold(namespaceOf(ci.FQN), f.Namespace) && !imported[strings.ToLower(ci.FQN)]:
			if fq, taken := f.Uses[strings.ToLower(ci.Name)]; taken && !strings.EqualFold(fq, ci.FQN) {
				it.Insert = "\\" + ci.FQN // the short name is taken by another import
			} else if namespaceOf(ci.FQN) != "" || f.Namespace != "" {
				it.Import = ci.FQN
			}
		}
		if ci.Builtin() {
			it.Detail = "PHP"
		}
		c.Items = append(c.Items, it)
	}
	for _, list := range ix.classes {
		for _, ci := range list {
			addClass(ci)
		}
	}
	builtins.RLock()
	for _, ci := range builtins.classes {
		addClass(ci)
	}
	if !classesOnly {
		for _, fn := range builtins.funcs {
			c.Items = append(c.Items, CompletionItem{Label: fn.Name, Kind: Function, Insert: fn.Name, Detail: fn.Params + typeSuffix(fn.Type),
				Call: true, Params: fn.Params != "()"})
		}
	}
	builtins.RUnlock()
	if classesOnly {
		return
	}
	for _, list := range ix.functions {
		for _, fn := range list {
			it := CompletionItem{Label: fn.Name, Kind: Function, Insert: fn.Name, Detail: fn.Params + typeSuffix(fn.Type),
				Call: true, Params: fn.Params != "()", Owner: namespaceOf(fn.FQN)}
			if ns := namespaceOf(fn.FQN); ns != "" && !strings.EqualFold(ns, f.Namespace) {
				if _, ok := f.FuncUses[strings.ToLower(fn.Name)]; !ok {
					it.Import = "function " + fn.FQN
				}
			}
			c.Items = append(c.Items, it)
		}
	}
	for _, kw := range keywordList {
		c.Items = append(c.Items, CompletionItem{Label: kw, Kind: Keyword, Insert: kw})
	}
}

func typeSuffix(t string) string {
	switch t {
	case "":
		return ""
	case staticType:
		return ": static"
	}
	return ": " + shortName(t)
}

// matchScore rates how well label matches prefix; 0 means no match.
func matchScore(label, prefix string) int {
	if prefix == "" {
		return 1
	}
	l, p := strings.ToLower(label), strings.ToLower(prefix)
	switch {
	case strings.HasPrefix(label, prefix):
		return 1100
	case strings.HasPrefix(l, p):
		return 1000
	case strings.HasPrefix(strings.TrimPrefix(l, "$"), strings.TrimPrefix(p, "$")) && p != "$":
		return 900
	case len(p) >= 2 && strings.Contains(l, strings.TrimPrefix(p, "$")):
		return 500
	case len(p) >= 2 && subsequence(l, strings.TrimPrefix(p, "$")):
		return 300
	}
	return 0
}

// finish filters, ranks and deduplicates the items.
func (c *Completion) finish() {
	out := c.Items[:0]
	seen := map[string]bool{}
	for _, it := range c.Items {
		s := matchScore(it.Label, c.Prefix)
		if s == 0 || it.Label == c.Prefix && it.Kind == Variable {
			continue
		}
		key := it.Label + "\x00" + it.Import + "\x00" + it.Kind.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		if it.Kind == Keyword {
			s -= 50
		}
		if it.Detail == "PHP" {
			s -= 10
		}
		if c.Prefix != "" {
			s -= min(len(it.Label), 40) // shorter matches first
		}
		it.score = s + it.boost
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return strings.ToLower(out[i].Label) < strings.ToLower(out[j].Label)
	})
	if len(out) > maxCompletions {
		out = out[:maxCompletions]
	}
	c.Items = out
}
