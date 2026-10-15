package index

import (
	"regexp"
	"strings"

	"github.com/jannovy/iderm/internal/syntax"
)

// Location is a resolved declaration.
type Location struct {
	File      string
	Line, Col int
	Name      string // e.g. App\Models\User::greet
	Kind      SymKind
}

func loc(s *Symbol) Location {
	return Location{File: s.File, Line: s.Line, Col: s.Col, Name: s.FQN, Kind: s.Kind}
}

func locs(syms []*Symbol) []Location {
	out := make([]Location, len(syms))
	for i, s := range syms {
		out[i] = loc(s)
	}
	return out
}

type resolver struct {
	ix   *Index
	toks []Token
	f    *FileInfo
	cur  *ClassInfo
}

func (r *resolver) tok(i int) Token {
	if i >= 0 && i < len(r.toks) {
		return r.toks[i]
	}
	return Token{Kind: TOp}
}

// Definition finds the declaration of the symbol at (line, col) in src.
// The file's own declarations are refreshed from src first, so unsaved
// changes are taken into account. msg explains why nothing was found.
func (ix *Index) Definition(path string, src Lines, line, col int) ([]Location, string) {
	res, msg := ix.definition(path, src, line, col)
	var out []Location
	for _, l := range res {
		if l.File != "" {
			out = append(out, l)
		}
	}
	if len(out) == 0 && len(res) > 0 {
		return nil, res[0].Name + " is built into PHP"
	}
	return out, msg
}

func (ix *Index) definition(path string, src Lines, line, col int) (res []Location, msg string) {
	toks := Tokenize(src)
	f := Parse(path, toks)
	ix.UpdateInfo(f)

	k := -1
	for i, t := range toks {
		if t.Line == line && t.Kind != TOp && t.Kind != TDoc && col >= t.Col && col <= t.End {
			k = i
			if col < t.End {
				break
			}
		}
	}
	if k < 0 {
		return nil, "No symbol under cursor"
	}
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	r := &resolver{ix: ix, toks: toks, f: f, cur: f.ClassAt(line, col)}
	t, prev, next := toks[k], r.tok(k-1), r.tok(k+1)
	call := next.is("(")

	switch {
	case t.Kind == TVar && prev.is("::"):
		cls := r.exprType(k-2, 0)
		if m := ix.findMember(cls, t.Text[1:], Property); len(m) > 0 {
			return locs(m), ""
		}
		return locs(ix.membersNamed(t.Text[1:], Property)), "Property " + t.Text + " not found"
	case t.Kind == TVar && t.Text == "$this":
		if r.cur != nil {
			return []Location{loc(r.cur.Symbol)}, ""
		}
		return nil, "$this outside of a class"
	case t.Kind == TVar:
		if d := r.varDecl(k); d >= 0 {
			v := toks[d]
			return []Location{{File: path, Line: v.Line, Col: v.Col, Name: v.Text, Kind: Property}}, ""
		}
		return nil, "Declaration of " + t.Text + " not found"
	case prev.is("->"):
		kind := Property
		if call {
			kind = Method
		}
		if cls := r.exprType(k-2, 0); cls != "" {
			if m := ix.findMember(cls, t.Text, kind); len(m) > 0 {
				return locs(m), ""
			}
		}
		if m := ix.membersNamed(t.Text, kind); len(m) > 0 {
			return locs(m), ""
		}
		return nil, kind.String() + " " + t.Text + " not found"
	case prev.is("::"):
		cls := r.exprType(k-2, 0)
		if strings.EqualFold(t.Text, "class") {
			return r.classLocs(cls)
		}
		kind := Const
		if call {
			kind = Method
		}
		if m := ix.findMember(cls, t.Text, kind); len(m) > 0 {
			return locs(m), ""
		}
		if cls == "" {
			return locs(ix.membersNamed(t.Text, kind)), kind.String() + " " + t.Text + " not found"
		}
		return nil, kind.String() + " " + cls + "::" + t.Text + " not found"
	case prev.isWord("function"), prev.is("&") && r.tok(k-2).isWord("function"):
		return nil, "This is the declaration"
	case call && !prev.isWord("new"):
		if fn := ix.lookupFunction(f, t.Text); len(fn) > 0 {
			return locs(fn), ""
		}
		return nil, "Function " + t.Text + " not found (built-in?)"
	}
	if syntax.IsKeyword(t.Text) {
		return nil, "No symbol under cursor"
	}
	if r.inUseStatement(k) {
		name := strings.TrimPrefix(t.Text, "\\")
		if prev.isWord("as") {
			return r.classLocs(f.Uses[strings.ToLower(name)])
		}
		// Inside a group use ("use A\{B, C}") the token is only the tail of the name.
		for _, fq := range f.Uses {
			if strings.EqualFold(fq, name) || strings.HasSuffix(strings.ToLower(fq), "\\"+strings.ToLower(name)) {
				return r.classLocs(fq)
			}
		}
		return r.classLocs(name)
	}
	return r.classLocs(f.ResolveClass(t.Text, r.cur))
}

func (r *resolver) classLocs(fqn string) ([]Location, string) {
	if fqn == "" {
		return nil, "Not a class"
	}
	var out []Location
	for _, c := range r.ix.classesByFQN(fqn) {
		out = append(out, loc(c.Symbol))
	}
	if len(out) == 0 {
		return nil, "Class " + fqn + " not found"
	}
	return out, ""
}

// inUseStatement reports whether token k is part of a top-level use statement.
func (r *resolver) inUseStatement(k int) bool {
	if r.cur != nil {
		return false
	}
	for i := k - 1; i >= 0; i-- {
		t := r.toks[i]
		if t.is(";") || t.is("}") && !r.tok(i+1).is(",") || t.is("(") {
			return false
		}
		if t.isWord("use") {
			return true
		}
	}
	return false
}

// exprType returns the class of the expression ending at token j.
func (r *resolver) exprType(j, depth int) string {
	if depth > 12 || j < 0 {
		return ""
	}
	t := r.toks[j]
	switch {
	case t.Kind == TVar && t.Text == "$this":
		if r.cur != nil {
			return r.cur.FQN
		}
	case t.Kind == TVar && r.tok(j-1).is("::"):
		owner := r.exprType(j-2, depth+1)
		return r.memberType(owner, t.Text[1:], Property)
	case t.Kind == TVar:
		return r.varType(j, depth)
	case t.Kind == TIdent && r.tok(j-1).is("->"):
		owner := r.exprType(j-2, depth+1)
		return r.memberType(owner, t.Text, Property)
	case t.Kind == TIdent && r.tok(j-1).is("::"):
		owner := r.exprType(j-2, depth+1)
		return r.memberType(owner, t.Text, Const)
	case t.Kind == TIdent:
		return r.f.ResolveClass(t.Text, r.cur)
	case t.is(")"):
		o := r.matchOpen(j)
		if o < 0 {
			return ""
		}
		name := r.tok(o - 1)
		switch {
		case name.Kind == TIdent && r.tok(o-2).is("->"):
			return r.memberType(r.exprType(o-3, depth+1), name.Text, Method)
		case name.Kind == TIdent && r.tok(o-2).is("::"):
			return r.memberType(r.exprType(o-3, depth+1), name.Text, Method)
		case name.Kind == TIdent && r.tok(o-2).isWord("new"):
			return r.f.ResolveClass(name.Text, r.cur)
		case name.Kind == TIdent:
			if fn := r.ix.lookupFunction(r.f, name.Text); len(fn) > 0 {
				return fn[0].Type
			}
		case r.tok(o + 1).isWord("new"):
			return r.f.ResolveClass(r.tok(o+2).Text, r.cur)
		default:
			return r.exprType(j-1, depth+1) // (expression)
		}
	}
	return ""
}

func (r *resolver) memberType(owner, name string, kind SymKind) string {
	if owner == "" {
		return ""
	}
	for _, m := range r.ix.findMember(owner, name, kind) {
		if m.Type == staticType {
			return owner
		}
		if m.Type != "" {
			return m.Type
		}
	}
	return ""
}

func (r *resolver) matchOpen(j int) int {
	d := 0
	for i := j; i >= 0; i-- {
		switch {
		case r.toks[i].is(")"):
			d++
		case r.toks[i].is("("):
			d--
			if d == 0 {
				return i
			}
		}
	}
	return -1
}

// varType infers the class of variable token j from the code before it.
func (r *resolver) varType(j, depth int) string {
	name := r.toks[j].Text
	docRe := regexp.MustCompile(`@var\s+(\S+)\s+` + regexp.QuoteMeta(name) + `\b`)
	for i := j - 1; i >= 0; i-- {
		t := r.toks[i]
		if t.Kind == TDoc {
			if m := docRe.FindStringSubmatch(t.Text); m != nil {
				return r.f.typeOf(m[1], r.cur)
			}
			continue
		}
		if t.Kind != TVar || t.Text != name {
			if t.isWord("function") || t.isWord("fn") {
				return ""
			}
			continue
		}
		// Typed parameter or catch: Foo $name
		if p := r.tok(i - 1); p.Kind == TIdent && !modifiers[strings.ToLower(p.Text)] && !p.isWord("as") && !p.isWord("global") {
			if ty := r.f.typeOf(p.Text, r.cur); ty != "" {
				return r.fixStatic(ty)
			}
		}
		// Assignment: $name = expr;
		if r.tok(i + 1).is("=") {
			if end := r.statementEnd(i + 2); end > i+1 {
				if ty := r.exprType(end, depth+1); ty != "" {
					return ty
				}
			}
		}
	}
	return ""
}

func (r *resolver) fixStatic(t string) string {
	if t == staticType && r.cur != nil {
		return r.cur.FQN
	}
	return t
}

// statementEnd returns the last token of the expression starting at i.
func (r *resolver) statementEnd(i int) int {
	d := 0
	for k := i; k < len(r.toks); k++ {
		t := r.toks[k]
		switch {
		case t.is("(") || t.is("["):
			d++
		case t.is(")") || t.is("]"):
			if d == 0 {
				return k - 1
			}
			d--
		case d == 0 && (t.is(";") || t.is(",")):
			return k - 1
		}
	}
	return -1
}

// varDecl finds the first occurrence of variable token k in its function.
func (r *resolver) varDecl(k int) int {
	name := r.toks[k].Text
	first := -1
	for i := k; i >= 0; i-- {
		t := r.toks[i]
		if t.Kind == TVar && t.Text == name {
			first = i
		}
		if t.isWord("function") || t.isWord("fn") {
			break
		}
	}
	if first == k {
		return -1
	}
	return first
}
