package syntax

import (
	"strings"
	"unicode"
)

var jsKeywords = toSet(`break case catch class const continue debugger default delete do else
	export extends finally for function if import in instanceof let new return super switch
	this throw try typeof var void while with yield async await of static get set from as
	interface type enum implements namespace module declare abstract private protected public
	readonly keyof infer is satisfies override`)

var jsTypes = toSet(`string number boolean any unknown never object symbol bigint`)

var jsConstants = toSet(`true false null undefined NaN Infinity`)

// closesEmbedded reports whether </tag ends a script or style block of an HTML host.
func (l *lexer) closesEmbedded(tag string) bool {
	return l.st.Lang != LangJS && l.st.Lang != LangCSS && l.at(l.i) == '<' && l.at(l.i+1) == '/' && l.hasPrefixFold(l.i+2, tag)
}

func (l *lexer) js() {
	if l.embedded() {
		return
	}
	if l.closesEmbedded("script") {
		l.st.Mode = ModeHTML
		return
	}
	i, c := l.i, l.s[l.i]
	wasMember := l.member
	l.member = false
	switch {
	case c == ' ' || c == '\t' || c == '\r':
		l.i++
		l.member = wasMember
	case c == '/' && l.at(i+1) == '/':
		j := i
		for j < len(l.s) && !l.mayEmbed(j) {
			j++
		}
		l.mark(i, j, Comment)
		l.i = j
	case c == '/' && l.at(i+1) == '*':
		l.mark(i, i+2, Comment)
		l.i += 2
		l.st.Mode = ModeJSComment
	case c == '\'' || c == '"':
		l.mark(i, i+1, String)
		l.i++
		l.st.Mode = ModeJSSingle
		if c == '"' {
			l.st.Mode = ModeJSDouble
		}
	case c == '`':
		l.mark(i, i+1, String)
		l.i++
		l.st.Mode = ModeJSTemplate
	case isDigit(c) || c == '.' && isDigit(l.at(i+1)):
		l.number()
	case isIdentStart(c) || c == '$' || c == '#' && isIdentStart(l.at(i+1)):
		j := l.word(i+1, "$")
		word := string(l.s[i:j])
		call := l.nextNonSpace(j) == '('
		k := Plain
		switch {
		case wasMember && call:
			k = Function
		case wasMember:
		case jsKeywords[word]:
			k = Keyword
		case jsConstants[word]:
			k = Constant
		case jsTypes[word]:
			k = Type
		case call:
			k = Function
		case unicode.IsUpper(c):
			k = Type
		}
		l.mark(i, j, k)
		l.i = j
	case c == '.' || c == '?' && l.at(i+1) == '.':
		if c == '?' {
			l.i++
		}
		l.i++
		l.member = true
	case strings.ContainsRune("+-*/%=<>!&|^~?:", c):
		l.mark(i, i+1, Operator)
		l.i++
	default:
		l.i++
	}
}

func (l *lexer) jsComment() {
	for j := l.i; j+1 < len(l.s); j++ {
		if l.s[j] == '*' && l.s[j+1] == '/' {
			l.mark(l.i, j+2, Comment)
			l.i = j + 2
			l.st.Mode = ModeJS
			return
		}
	}
	l.mark(l.i, len(l.s), Comment)
	l.i = len(l.s)
}

func (l *lexer) jsString(q rune) {
	if l.embedded() {
		return
	}
	j := l.i
	for j < len(l.s) && !l.mayEmbed(j) {
		if l.s[j] == '\\' {
			j += 2
			continue
		}
		if l.s[j] == q {
			l.mark(l.i, j+1, String)
			l.i = j + 1
			l.st.Mode = ModeJS
			return
		}
		j++
	}
	j = min(j, len(l.s))
	if j == l.i {
		j++
	}
	l.mark(l.i, j, String)
	l.i = j
}

func (l *lexer) jsTemplate() {
	if l.embedded() {
		return
	}
	j := l.i
	for j < len(l.s) && !l.mayEmbed(j) {
		switch {
		case l.s[j] == '\\':
			j += 2
			continue
		case l.s[j] == '`':
			l.mark(l.i, j+1, String)
			l.i = j + 1
			l.st.Mode = ModeJS
			return
		case l.s[j] == '$' && l.at(j+1) == '{':
			l.mark(l.i, j, String)
			k := j
			for k < len(l.s) && l.s[k] != '}' {
				k++
			}
			k = min(k+1, len(l.s))
			l.mark(j, k, Variable)
			l.i, j = k, k
			continue
		}
		j++
	}
	j = min(j, len(l.s))
	if j == l.i {
		j++
	}
	l.mark(l.i, j, String)
	l.i = j
}

func isCSSIdent(r rune) bool { return isIdent(r) || r == '-' }

func (l *lexer) css() {
	if l.embedded() {
		return
	}
	if l.closesEmbedded("style") {
		l.st.Mode = ModeHTML
		return
	}
	i, c := l.i, l.s[l.i]
	inBlock := l.st.Depth > 0
	switch {
	case c == ' ' || c == '\t' || c == '\r':
		l.i++
	case c == '/' && l.at(i+1) == '*':
		l.mark(i, i+2, Comment)
		l.i += 2
		l.st.Mode = ModeCSSComment
	case c == '/' && l.at(i+1) == '/' && l.st.Lang == LangCSS && (i == 0 || strings.ContainsRune(" \t;{}", l.s[i-1])): // SCSS and Less
		l.mark(i, len(l.s), Comment)
		l.i = len(l.s)
	case c == '\'' || c == '"':
		l.quoted(String)
	case c == '{':
		l.st.Depth = min(l.st.Depth+1, 250)
		l.i++
	case c == '}':
		if l.st.Depth > 0 {
			l.st.Depth--
		}
		l.i++
	case c == '@' || c == '!' && isIdentStart(l.at(i+1)):
		j := l.word(i+1, "-")
		l.mark(i, j, Keyword)
		l.i = j
	case c == '$' && isIdentStart(l.at(i+1)) || c == '-' && l.at(i+1) == '-' && isIdentStart(l.at(i+2)):
		j := l.word(i+1, "-")
		l.mark(i, j, Variable)
		l.i = j
	case c == '#' && inBlock && isHexDigit(l.at(i+1)):
		j := l.word(i+1, "")
		l.mark(i, j, Number)
		l.i = j
	case isDigit(c) || c == '.' && isDigit(l.at(i+1)) || c == '-' && isDigit(l.at(i+1)):
		j := i + 1
		for j < len(l.s) && (isDigit(l.s[j]) || l.s[j] == '.') {
			j++
		}
		for j < len(l.s) && (unicode.IsLetter(l.s[j]) || l.s[j] == '%') {
			j++
		}
		l.mark(i, j, Number)
		l.i = j
	case (c == '.' || c == '#' || c == ':' || c == '&') && (isCSSIdent(l.at(i+1)) || c == ':' && l.at(i+1) == ':') && (!inBlock || l.selectorAhead()):
		j := i + 1
		for l.at(j) == ':' {
			j++
		}
		j = l.word(j, "-")
		k := Type
		switch c {
		case '#':
			k = Constant
		case ':':
			k = Function
		}
		l.mark(i, j, k)
		l.i = j
	case isCSSIdent(c):
		j := l.word(i, "-")
		k := Tag
		switch {
		case l.prevNonSpace(i) == '(' && l.nextNonSpace(j) == ':': // @media (max-width: …)
			k = Key
		case !inBlock || l.selectorAhead():
		case l.nextNonSpace(j) == ':':
			k = Key
		case l.at(j) == '(':
			k = Function
		default:
			k = Constant
		}
		l.mark(i, j, k)
		l.i = j
	case strings.ContainsRune(":;,>+~*=", c):
		l.mark(i, i+1, Operator)
		l.i++
	default:
		l.i++
	}
}

// selectorAhead reports whether the rest of the line opens a nested block,
// so that words inside a block are a selector rather than a declaration.
func (l *lexer) selectorAhead() bool {
	for j := l.i; j < len(l.s); j++ {
		switch l.s[j] {
		case ';', '}':
			return false
		case '{':
			return true
		}
	}
	return false
}

func isHexDigit(r rune) bool { return isDigit(r) || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F' }

func (l *lexer) cssComment() {
	for j := l.i; j+1 < len(l.s); j++ {
		if l.s[j] == '*' && l.s[j+1] == '/' {
			l.mark(l.i, j+2, Comment)
			l.i = j + 2
			l.st.Mode = ModeCSS
			return
		}
	}
	l.mark(l.i, len(l.s), Comment)
	l.i = len(l.s)
}
