package index

import (
	"regexp"
	"strings"
)

// SymKind is the kind of a declared symbol.
type SymKind uint8

const (
	Class SymKind = iota
	Interface
	Trait
	Enum
	Function
	Method
	Const
	Property
)

func (k SymKind) String() string {
	return [...]string{"class", "interface", "trait", "enum", "function", "method", "const", "property", "variable", "keyword"}[k]
}

// staticType marks a return type of self/static/$this, i.e. the receiver class.
const staticType = "static"

// Symbol is a declaration found in a file.
type Symbol struct {
	Kind  SymKind
	Name  string // short name; properties without "$"
	FQN   string // classes/functions: fully qualified name; members: Class::Name
	Class string // owner class of a member
	Type  string // property type or return type (resolved class name, staticType, or "")
	File  string
	Line  int
	Col   int

	Visibility string // public, protected or private (members)
	Static     bool
	Params     string // "(Type $a, $b = …)" for functions and methods
}

// Builtin reports whether the symbol comes from PHP itself (no source file).
func (s *Symbol) Builtin() bool { return s.File == "" }

// ClassInfo is a class-like declaration with its members.
type ClassInfo struct {
	*Symbol
	Extends    []string
	Implements []string
	Traits     []string
	Members    []*Symbol
	BodyStart  [2]int // line, col of "{"
	BodyEnd    [2]int // line, col of "}"
}

// FileInfo holds the declarations of one file.
type FileInfo struct {
	Path      string
	Namespace string
	Uses      map[string]string // lower alias -> class FQN
	FuncUses  map[string]string // lower alias -> function FQN
	Classes   []*ClassInfo
	Functions []*Symbol

	NamespaceLine int // line of the namespace statement, -1 when none
	LastUseLine   int // line of the last top-level use statement, -1 when none
}

var scalarTypes = toSet(`int float bool string array void never mixed object iterable callable null false true
	resource double integer boolean scalar numeric number list`)

func toSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

var modifiers = toSet(`public protected private static readonly var final abstract`)

// ClassAt returns the class whose body contains the position.
func (f *FileInfo) ClassAt(line, col int) *ClassInfo {
	for _, c := range f.Classes {
		if before(c.BodyStart, [2]int{line, col}) && (c.BodyEnd == [2]int{} || before([2]int{line, col}, c.BodyEnd)) {
			return c
		}
	}
	return nil
}

func before(a, b [2]int) bool { return a[0] < b[0] || a[0] == b[0] && a[1] <= b[1] }

// ResolveClass turns a class reference into a fully qualified name.
func (f *FileInfo) ResolveClass(name string, cur *ClassInfo) string {
	name = strings.TrimPrefix(name, "?")
	if name == "" {
		return ""
	}
	lower := strings.ToLower(name)
	switch lower {
	case "self", "static", "$this":
		if cur != nil {
			return cur.FQN
		}
		return ""
	case "parent":
		if cur != nil && len(cur.Extends) > 0 {
			return cur.Extends[0]
		}
		return ""
	}
	if scalarTypes[lower] {
		return ""
	}
	if name[0] == '\\' {
		return name[1:]
	}
	if i := strings.IndexByte(name, '\\'); i > 0 {
		first := strings.ToLower(name[:i])
		if first == "namespace" {
			return join(f.Namespace, name[i+1:])
		}
		if fq, ok := f.Uses[first]; ok {
			return fq + name[i:]
		}
	} else if fq, ok := f.Uses[lower]; ok {
		return fq
	}
	return join(f.Namespace, name)
}

// typeOf resolves a type declaration or docblock type to a class name.
func (f *FileInfo) typeOf(raw string, cur *ClassInfo) string {
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == '|' || r == '&' || r == '(' || r == ')' }) {
		part = strings.TrimPrefix(part, "?")
		if i := strings.IndexAny(part, "<{"); i >= 0 {
			part = part[:i]
		}
		if part == "" || strings.HasSuffix(part, "[]") {
			continue
		}
		switch strings.ToLower(part) {
		case "self", "static", "$this":
			return staticType
		}
		if t := f.ResolveClass(part, cur); t != "" {
			return t
		}
	}
	return ""
}

func join(ns, name string) string {
	if ns == "" {
		return name
	}
	return ns + "\\" + name
}

var (
	docReturn = regexp.MustCompile(`@return\s+(\S+)`)
	docVar    = regexp.MustCompile(`@var\s+(\S+)`)
)

// Parse extracts declarations from tokens of a file.
func Parse(path string, toks []Token) *FileInfo {
	f := &FileInfo{Path: path, Uses: map[string]string{}, FuncUses: map[string]string{}, NamespaceLine: -1, LastUseLine: -1}
	tok := func(i int) Token {
		if i >= 0 && i < len(toks) {
			return toks[i]
		}
		return Token{Kind: TOp}
	}
	depth, topDepth := 0, 0
	var cls, pending *ClassInfo
	clsDepth := -1
	doc := ""

	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch {
		case t.Kind == TDoc:
			doc = t.Text
			continue
		case t.is("{"):
			depth++
			if pending != nil {
				cls, pending = pending, nil
				clsDepth = depth
				cls.BodyStart = [2]int{t.Line, t.Col}
			}
			doc = ""
		case t.is("}"):
			if cls != nil && depth == clsDepth {
				cls.BodyEnd = [2]int{t.Line, t.Col}
				cls, clsDepth = nil, -1
			}
			depth--
			doc = ""
		case t.is(";"):
			doc = ""
		case t.isWord("namespace") && cls == nil:
			n := tok(i + 1)
			if n.Kind == TIdent {
				f.Namespace = strings.TrimPrefix(n.Text, "\\")
				i++
			} else if !n.is("{") {
				continue
			} else {
				f.Namespace = ""
			}
			f.Uses, f.FuncUses = map[string]string{}, map[string]string{}
			f.NamespaceLine = t.Line
			if tok(i + 1).is("{") {
				topDepth = depth + 1
			}
		case t.isWord("use") && cls == nil && depth == topDepth && !tok(i+1).is("("):
			i = parseUse(f, toks, i+1)
			if i < len(toks) {
				f.LastUseLine = toks[i].Line
			}
		case t.isWord("use") && cls != nil && depth == clsDepth:
			for i++; i < len(toks) && !toks[i].is(";") && !toks[i].is("{"); i++ {
				if toks[i].Kind == TIdent {
					cls.Traits = append(cls.Traits, f.ResolveClass(toks[i].Text, cls))
				}
			}
			i--
		case (t.isWord("class") || t.isWord("interface") || t.isWord("trait") || t.isWord("enum")) &&
			cls == nil && pending == nil && tok(i+1).Kind == TIdent && !tok(i-1).is("::") && !tok(i-1).isWord("new"):
			if t.isWord("enum") && (tok(i+2).is("(") || tok(i+2).is("=")) {
				continue // a function or constant called enum
			}
			kind := map[string]SymKind{"class": Class, "interface": Interface, "trait": Trait, "enum": Enum}[strings.ToLower(t.Text)]
			n := tok(i + 1)
			c := &ClassInfo{Symbol: &Symbol{Kind: kind, Name: n.Text, FQN: join(f.Namespace, n.Text), File: path, Line: n.Line, Col: n.Col}}
			list := ""
			for i += 2; i < len(toks) && !toks[i].is("{") && !toks[i].is(";"); i++ {
				switch x := toks[i]; {
				case x.isWord("extends"), x.isWord("implements"):
					list = strings.ToLower(x.Text)
				case x.Kind == TIdent && list == "extends":
					c.Extends = append(c.Extends, f.ResolveClass(x.Text, nil))
				case x.Kind == TIdent && list == "implements":
					c.Implements = append(c.Implements, f.ResolveClass(x.Text, nil))
				case x.is(":"):
					list = ""
				}
			}
			i--
			f.Classes = append(f.Classes, c)
			pending = c
		case t.isWord("function"):
			j := i + 1
			if tok(j).is("&") {
				j++
			}
			n := tok(j)
			if n.Kind != TIdent || !tok(j+1).is("(") {
				continue
			}
			inClass := cls != nil && depth == clsDepth
			if !inClass && (cls != nil || depth != topDepth) {
				continue
			}
			sym := &Symbol{Kind: Function, Name: n.Text, File: path, Line: n.Line, Col: n.Col}
			// Parameters and return type.
			k, pd := j+1, 0
			paramsStart := k
			for ; k < len(toks); k++ {
				if toks[k].is("(") {
					pd++
				} else if toks[k].is(")") {
					pd--
					if pd == 0 {
						break
					}
				}
			}
			if tok(k + 1).is(":") {
				var sb strings.Builder
				for m := k + 2; m < len(toks) && (toks[m].Kind == TIdent || toks[m].is("?") || toks[m].is("|") || toks[m].is("&") || toks[m].is("(") || toks[m].is(")")); m++ {
					sb.WriteString(toks[m].Text)
				}
				sym.Type = f.typeOf(sb.String(), cls)
			}
			if sym.Type == "" && doc != "" {
				if m := docReturn.FindStringSubmatch(doc); m != nil {
					sym.Type = f.typeOf(m[1], cls)
				}
			}
			sym.Params = formatParams(toks[paramsStart+1 : min(k, len(toks))])
			if inClass {
				sym.Kind, sym.Class, sym.FQN = Method, cls.FQN, cls.FQN+"::"+n.Text
				sym.Visibility, sym.Static = modsBefore(toks, i)
				cls.Members = append(cls.Members, sym)
				if strings.EqualFold(n.Text, "__construct") {
					promotedProperties(f, cls, toks[paramsStart:k])
				}
			} else {
				sym.FQN = join(f.Namespace, n.Text)
				f.Functions = append(f.Functions, sym)
			}
			doc = ""
			i = k // skip the parameters: they are not properties
		case t.isWord("const") && cls != nil && depth == clsDepth:
			j := i + 1
			for tok(j).Kind == TIdent && tok(j+1).Kind == TIdent {
				j++ // typed constant: const string NAME
			}
			if n := tok(j); n.Kind == TIdent {
				vis, _ := modsBefore(toks, i)
				cls.Members = append(cls.Members, &Symbol{Kind: Const, Name: n.Text, Class: cls.FQN, FQN: cls.FQN + "::" + n.Text, File: path, Line: n.Line, Col: n.Col, Visibility: vis, Static: true})
				i = j
			}
		case t.isWord("case") && cls != nil && cls.Kind == Enum && depth == clsDepth:
			if n := tok(i + 1); n.Kind == TIdent {
				cls.Members = append(cls.Members, &Symbol{Kind: Const, Name: n.Text, Class: cls.FQN, FQN: cls.FQN + "::" + n.Text, Type: cls.FQN, File: path, Line: n.Line, Col: n.Col, Visibility: "public", Static: true})
				i++
			}
		case t.Kind == TVar && cls != nil && depth == clsDepth:
			cls.Members = append(cls.Members, property(f, cls, toks, i, doc))
		}
	}
	return f
}

// property builds a property symbol for the variable token at i.
func property(f *FileInfo, cls *ClassInfo, toks []Token, i int, doc string) *Symbol {
	t := toks[i]
	sym := &Symbol{Kind: Property, Name: t.Text[1:], Class: cls.FQN, FQN: cls.FQN + "::$" + t.Text[1:], File: f.Path, Line: t.Line, Col: t.Col}
	sym.Visibility, sym.Static = modsBefore(toks, i)
	var parts []string // collected backwards
	for j := i - 1; j >= 0; j-- {
		p := toks[j]
		switch {
		case p.Kind == TIdent && modifiers[strings.ToLower(p.Text)]:
		case p.Kind == TIdent || p.is("?") || p.is("|") || p.is("&"):
			parts = append(parts, p.Text)
		default:
			j = -1
		}
	}
	var sb strings.Builder
	for j := len(parts) - 1; j >= 0; j-- {
		sb.WriteString(parts[j])
	}
	sym.Type = f.typeOf(sb.String(), cls)
	if sym.Type == "" && doc != "" {
		if m := docVar.FindStringSubmatch(doc); m != nil {
			sym.Type = f.typeOf(m[1], cls)
		}
	}
	return sym
}

// modsBefore reads the modifiers (skipping a type) written before token i.
func modsBefore(toks []Token, i int) (visibility string, static bool) {
	visibility = "public"
	for j := i - 1; j >= 0; j-- {
		t := toks[j]
		switch {
		case t.Kind == TIdent && modifiers[strings.ToLower(t.Text)]:
			switch w := strings.ToLower(t.Text); w {
			case "private", "protected", "public":
				visibility = w
			case "static":
				static = true
			}
		case t.Kind == TIdent || t.is("?") || t.is("|") || t.is("&"):
		default:
			return
		}
	}
	return
}

// formatParams renders a parameter list like "(?Foo $a, int ...$rest, $b = …)".
func formatParams(toks []Token) string {
	var params []string
	var cur []Token
	depth := 0
	flush := func() {
		if len(cur) == 0 {
			return
		}
		var typ []string
		name, prefix, def := "", "", false
		for i, t := range cur {
			switch {
			case name == "" && t.Kind == TVar:
				name = t.Text
			case name == "" && t.is("&"):
				prefix += "&"
			case name == "" && t.is(".") && i+2 < len(cur) && cur[i+1].is(".") && cur[i+2].is("."):
				prefix += "..."
			case name == "" && t.is("."):
			case name == "" && t.Kind == TIdent && modifiers[strings.ToLower(t.Text)]:
			case name == "" && (t.Kind == TIdent || t.is("?") || t.is("|")):
				typ = append(typ, t.Text)
			case name != "" && t.is("="):
				def = true
			}
		}
		if name == "" {
			cur = cur[:0]
			return
		}
		s := prefix + name
		if len(typ) > 0 {
			s = strings.Join(typ, "") + " " + s
		}
		if def {
			s += " = …"
		}
		params = append(params, s)
		cur = cur[:0]
	}
	for _, t := range toks {
		switch {
		case t.is("(") || t.is("["):
			depth++
		case t.is(")") || t.is("]"):
			depth--
		case t.is(",") && depth == 0:
			flush()
			continue
		}
		cur = append(cur, t)
	}
	flush()
	return "(" + strings.Join(params, ", ") + ")"
}

// promotedProperties adds constructor-promoted parameters as properties.
func promotedProperties(f *FileInfo, cls *ClassInfo, params []Token) {
	for i, p := range params {
		if p.Kind != TVar {
			continue
		}
		promoted := false
		for j := i - 1; j >= 0 && !params[j].is(",") && !params[j].is("("); j-- {
			if params[j].Kind == TIdent && modifiers[strings.ToLower(params[j].Text)] {
				promoted = true
			}
		}
		if promoted {
			cls.Members = append(cls.Members, property(f, cls, params, i, ""))
		}
	}
}

// parseUse reads a top-level use statement starting at token i; returns the index of ";".
func parseUse(f *FileInfo, toks []Token, i int) int {
	target := f.Uses
	if i < len(toks) && (toks[i].isWord("function") || toks[i].isWord("const")) {
		if toks[i].isWord("function") {
			target = f.FuncUses
		} else {
			target = map[string]string{}
		}
		i++
	}
	prefix := ""
	name, alias := "", ""
	flush := func() {
		if name == "" {
			return
		}
		full := strings.TrimPrefix(prefix+name, "\\")
		if alias == "" {
			alias = full[strings.LastIndexByte(full, '\\')+1:]
		}
		target[strings.ToLower(alias)] = full
		name, alias = "", ""
	}
	for ; i < len(toks); i++ {
		t := toks[i]
		switch {
		case t.is(";"):
			flush()
			return i
		case t.is("{"):
			prefix = name
			if !strings.HasSuffix(prefix, "\\") {
				prefix += "\\"
			}
			name = ""
		case t.is("}"):
			flush()
		case t.is(","):
			flush()
		case t.isWord("as"):
			if i+1 < len(toks) {
				alias = toks[i+1].Text
				i++
			}
		case t.Kind == TIdent:
			name = t.Text
		case t.is("\\"):
			name += "\\"
		}
	}
	flush()
	return i
}
