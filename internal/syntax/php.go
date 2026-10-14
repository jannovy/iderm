// Package syntax provides a line-oriented PHP lexer for syntax highlighting.
package syntax

import (
	"strings"
	"unicode"
)

// Kind classifies a character for highlighting.
type Kind uint8

const (
	Plain Kind = iota
	Keyword
	Type
	Variable
	String
	Number
	Comment
	Tag
	HTML
	Constant
	Function
	Operator
)

// Mode is the lexer mode carried from one line to the next.
type Mode uint8

const (
	ModeHTML Mode = iota
	ModePHP
	ModeComment
	ModeSingle
	ModeDouble
	ModeBacktick
	ModeHeredoc
	ModeNowdoc
)

// State is the lexer state at the start of a line.
type State struct {
	Mode  Mode
	Label string // heredoc/nowdoc terminator
}

// Initial is the state at the start of a PHP file (inline HTML).
var Initial = State{Mode: ModeHTML}

var keywords = toSet(`abstract and array as break case catch class clone const continue
	declare default die do echo else elseif empty enddeclare endfor endforeach endif
	endswitch endwhile enum eval exit extends final finally fn for foreach function
	global goto if implements include include_once instanceof insteadof interface
	isset list match namespace new or parent print private protected public readonly
	require require_once return self static switch throw trait try unset use var
	while xor yield __halt_compiler`)

var types = toSet(`int float bool string void never mixed object iterable callable`)

var constants = toSet(`true false null`)

func toSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

type lexer struct {
	s      []rune
	k      []Kind
	i      int
	st     State
	member bool // previous token was -> ?-> or ::
}

// Lex classifies every rune of line, starting in state st, and returns
// the state at the end of the line.
func Lex(line []rune, st State) ([]Kind, State) {
	l := &lexer{s: line, k: make([]Kind, len(line)), st: st}
	if st.Mode == ModeHeredoc || st.Mode == ModeNowdoc {
		l.heredocEnd()
	}
	for l.i < len(l.s) {
		switch l.st.Mode {
		case ModeHTML:
			l.html()
		case ModePHP:
			l.php()
		case ModeComment:
			l.blockComment()
		case ModeSingle:
			l.single()
		case ModeDouble:
			l.interpolated('"')
		case ModeBacktick:
			l.interpolated('`')
		case ModeHeredoc:
			l.interpolated(-1)
		case ModeNowdoc:
			l.mark(l.i, len(l.s), String)
			l.i = len(l.s)
		}
	}
	return l.k, l.st
}

func (l *lexer) mark(from, to int, k Kind) {
	for j := from; j < to && j < len(l.k); j++ {
		l.k[j] = k
	}
}

func (l *lexer) at(i int) rune {
	if i >= 0 && i < len(l.s) {
		return l.s[i]
	}
	return 0
}

func (l *lexer) hasPrefixFold(i int, p string) bool {
	for j, r := range []rune(p) {
		if unicode.ToLower(l.at(i+j)) != r {
			return false
		}
	}
	return true
}

func (l *lexer) html() {
	for j := l.i; j+1 < len(l.s); j++ {
		if l.s[j] != '<' || l.s[j+1] != '?' {
			continue
		}
		n := 0
		if l.hasPrefixFold(j+2, "php") && !isIdent(l.at(j+5)) {
			n = 5
		} else if l.at(j+2) == '=' {
			n = 3
		}
		if n > 0 {
			l.mark(l.i, j, HTML)
			l.mark(j, j+n, Tag)
			l.i = j + n
			l.st.Mode = ModePHP
			return
		}
	}
	l.mark(l.i, len(l.s), HTML)
	l.i = len(l.s)
}

func (l *lexer) php() {
	i, c := l.i, l.s[l.i]
	wasMember := l.member
	l.member = false
	switch {
	case c == ' ' || c == '\t':
		l.i++
		l.member = wasMember
	case c == '?' && l.at(i+1) == '>':
		l.mark(i, i+2, Tag)
		l.i += 2
		l.st.Mode = ModeHTML
	case c == '/' && l.at(i+1) == '*':
		l.mark(i, i+2, Comment)
		l.i += 2
		l.st.Mode = ModeComment
	case c == '/' && l.at(i+1) == '/', c == '#' && l.at(i+1) != '[':
		j := i
		for j < len(l.s) && !(l.s[j] == '?' && l.at(j+1) == '>') {
			j++
		}
		l.mark(i, j, Comment)
		l.i = j
	case c == '\'':
		l.mark(i, i+1, String)
		l.i++
		l.st.Mode = ModeSingle
	case c == '"':
		l.mark(i, i+1, String)
		l.i++
		l.st.Mode = ModeDouble
	case c == '`':
		l.mark(i, i+1, String)
		l.i++
		l.st.Mode = ModeBacktick
	case c == '<' && l.at(i+1) == '<' && l.at(i+2) == '<' && l.heredocStart():
	case c == '$' && isIdentStart(l.at(i+1)):
		j := i + 1
		for j < len(l.s) && isIdent(l.s[j]) {
			j++
		}
		l.mark(i, j, Variable)
		l.i = j
	case isDigit(c) || c == '.' && isDigit(l.at(i+1)):
		l.number()
	case isIdentStart(c) || c == '\\' && isIdentStart(l.at(i+1)):
		l.ident(wasMember)
	case c == '-' && l.at(i+1) == '>', c == ':' && l.at(i+1) == ':':
		l.mark(i, i+2, Operator)
		l.i += 2
		l.member = true
	case c == '?' && l.at(i+1) == '-' && l.at(i+2) == '>':
		l.mark(i, i+3, Operator)
		l.i += 3
		l.member = true
	case strings.ContainsRune("+-*/%=<>!&|^~?:.@", c):
		l.mark(i, i+1, Operator)
		l.i++
	default:
		l.i++
	}
}

func (l *lexer) blockComment() {
	for j := l.i; j+1 < len(l.s); j++ {
		if l.s[j] == '*' && l.s[j+1] == '/' {
			l.mark(l.i, j+2, Comment)
			l.i = j + 2
			l.st.Mode = ModePHP
			return
		}
	}
	l.mark(l.i, len(l.s), Comment)
	l.i = len(l.s)
}

func (l *lexer) single() {
	j := l.i
	for j < len(l.s) {
		switch l.s[j] {
		case '\\':
			j += 2
			continue
		case '\'':
			l.mark(l.i, j+1, String)
			l.i = j + 1
			l.st.Mode = ModePHP
			return
		}
		j++
	}
	l.mark(l.i, len(l.s), String)
	l.i = len(l.s)
}

// interpolated lexes a double-quoted, backtick or heredoc string body
// (quote < 0 means the string does not end on this line).
func (l *lexer) interpolated(quote rune) {
	j := l.i
	for j < len(l.s) {
		c := l.s[j]
		switch {
		case c == '\\':
			j += 2
		case c == quote:
			l.mark(l.i, j+1, String)
			l.i = j + 1
			l.st.Mode = ModePHP
			return
		case c == '$' && isIdentStart(l.at(j+1)):
			l.mark(l.i, j, String)
			k := j + 1
			for k < len(l.s) && isIdent(l.s[k]) {
				k++
			}
			l.mark(j, k, Variable)
			l.i, j = k, k
		case c == '{' && l.at(j+1) == '$':
			l.mark(l.i, j, String)
			k := j + 1
			for k < len(l.s) && l.s[k] != '}' {
				k++
			}
			k = min(k+1, len(l.s))
			l.mark(j, k, Variable)
			l.i, j = k, k
		default:
			j++
		}
	}
	l.mark(l.i, len(l.s), String)
	l.i = len(l.s)
}

func (l *lexer) heredocStart() bool {
	j := l.i + 3
	for l.at(j) == ' ' || l.at(j) == '\t' {
		j++
	}
	quote := l.at(j)
	if quote == '\'' || quote == '"' {
		j++
	} else {
		quote = 0
	}
	if !isIdentStart(l.at(j)) {
		return false
	}
	k := j
	for k < len(l.s) && isIdent(l.s[k]) {
		k++
	}
	label := string(l.s[j:k])
	if quote != 0 {
		if l.at(k) != quote {
			return false
		}
		k++
	}
	l.mark(l.i, k, String)
	l.i = k
	l.st = State{Mode: ModeHeredoc, Label: label}
	if quote == '\'' {
		l.st.Mode = ModeNowdoc
	}
	return true
}

// heredocEnd checks whether the line closes the current heredoc.
func (l *lexer) heredocEnd() {
	j := 0
	for l.at(j) == ' ' || l.at(j) == '\t' {
		j++
	}
	label := []rune(l.st.Label)
	for n, r := range label {
		if l.at(j+n) != r {
			return
		}
	}
	end := j + len(label)
	if isIdent(l.at(end)) {
		return
	}
	l.mark(j, end, String)
	l.i = end
	l.st = State{Mode: ModePHP}
}

func (l *lexer) number() {
	j := l.i
	hex := l.s[j] == '0' && (l.at(j+1) == 'x' || l.at(j+1) == 'X')
	for j < len(l.s) {
		c := l.s[j]
		if c < 0x80 && isIdent(c) || c == '.' && isDigit(l.at(j+1)) {
			j++
			continue
		}
		if (c == '+' || c == '-') && !hex && j > l.i && (l.s[j-1] == 'e' || l.s[j-1] == 'E') {
			j++
			continue
		}
		break
	}
	l.mark(l.i, j, Number)
	l.i = j
}

func (l *lexer) ident(member bool) {
	i, j := l.i, l.i
	for j < len(l.s) && (isIdent(l.s[j]) || l.s[j] == '\\' && isIdentStart(l.at(j+1))) {
		j++
	}
	word := string(l.s[i:j])
	lower := strings.ToLower(word)
	k := j
	for l.at(k) == ' ' || l.at(k) == '\t' {
		k++
	}
	call := l.at(k) == '('
	last := word[strings.LastIndexByte(word, '\\')+1:]

	kind := Plain
	switch {
	case member && call:
		kind = Function
	case member && lower == "class":
		kind = Keyword
	case member && isUpperConst(last):
		kind = Constant
	case member:
		kind = Plain
	case keywords[lower]:
		kind = Keyword
	case constants[lower]:
		kind = Constant
	case types[lower]:
		kind = Type
	case isUpperConst(last):
		kind = Constant
	case last != "" && unicode.IsUpper([]rune(last)[0]):
		kind = Type
	case call:
		kind = Function
	}
	l.mark(i, j, kind)
	l.i = j
}

func isUpperConst(w string) bool {
	letters := 0
	for _, r := range w {
		if unicode.IsLower(r) {
			return false
		}
		if unicode.IsLetter(r) {
			letters++
		}
	}
	return letters > 0 && len(w) > 1
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

func isIdentStart(r rune) bool {
	return r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= 0x80
}

func isIdent(r rune) bool { return isIdentStart(r) || isDigit(r) }

// Lines is the source of text for a Highlighter.
type Lines interface {
	LineCount() int
	Line(i int) []rune
}

// Highlighter caches lexer states at line starts so that only changed
// parts of a document need to be re-lexed.
type Highlighter struct {
	states []State
	valid  int // states[:valid] are up to date
}

// NewHighlighter creates a highlighter for a PHP document.
func NewHighlighter() *Highlighter {
	return &Highlighter{states: []State{Initial}, valid: 1}
}

// Invalidate marks lines after line as needing re-lexing. Negative values are ignored.
func (h *Highlighter) Invalidate(line int) {
	if line >= 0 && line+1 < h.valid {
		h.valid = line + 1
	}
}

// Line returns the highlighting of line i.
func (h *Highlighter) Line(src Lines, i int) []Kind {
	for h.valid <= i {
		j := h.valid - 1
		_, st := Lex(src.Line(j), h.states[j])
		if h.valid < len(h.states) {
			h.states[h.valid] = st
		} else {
			h.states = append(h.states, st)
		}
		h.valid++
	}
	k, _ := Lex(src.Line(i), h.states[i])
	return k
}

// IsKeyword reports whether word is a PHP keyword or built-in type.
func IsKeyword(word string) bool {
	w := strings.ToLower(word)
	return keywords[w] && w != "self" && w != "parent" && w != "static" || types[w] || constants[w]
}
