package syntax

import "strings"

var neonConstants = toSet(`true false null yes no on off TRUE FALSE NULL YES NO ON OFF True False Null Yes No On Off`)

func (l *lexer) json() {
	i, c := l.i, l.s[l.i]
	switch {
	case c == '"':
		l.quoted(String)
		if l.nextNonSpace(l.i) == ':' {
			l.mark(i, l.i, Key)
		}
	case c == '/' && l.at(i+1) == '/': // JSONC
		l.mark(i, len(l.s), Comment)
		l.i = len(l.s)
	case isDigit(c) || c == '-' && isDigit(l.at(i+1)):
		l.i++
		l.number()
		l.mark(i, l.i, Number)
	case isIdentStart(c):
		j := l.word(i, "")
		if w := string(l.s[i:j]); w == "true" || w == "false" || w == "null" {
			l.mark(i, j, Constant)
		}
		l.i = j
	default:
		l.i++
	}
}

func isNeonKey(r rune) bool { return isIdent(r) || strings.ContainsRune(`-.\/`, r) }

func (l *lexer) neon() {
	i, c := l.i, l.s[l.i]
	switch {
	case c == ' ' || c == '\t' || c == '\r':
		l.i++
	case c == '#' && (i == 0 || l.s[i-1] == ' ' || l.s[i-1] == '\t'):
		l.mark(i, len(l.s), Comment)
		l.i = len(l.s)
	case c == '\'' || c == '"':
		l.quoted(String)
		if l.at(l.i) == ':' {
			l.mark(i, l.i, Key)
		}
	case c == '-' && (l.at(i+1) == ' ' || i+1 == len(l.s)):
		l.mark(i, i+1, Operator)
		l.i++
	case c == '%':
		j := i + 1
		for j < len(l.s) && l.s[j] != '%' && l.s[j] != ' ' {
			j++
		}
		j = min(j+1, len(l.s))
		l.mark(i, j, Variable)
		l.i = j
	case c == '@' && (isIdentStart(l.at(i+1)) || l.at(i+1) == '\\'):
		j := l.word(i+1, `\.`)
		l.mark(i, j, Variable)
		l.i = j
	case isDigit(c) || c == '-' && isDigit(l.at(i+1)):
		j := i + 1
		for j < len(l.s) && (isIdent(l.s[j]) || l.s[j] == '.') {
			j++
		}
		k := Number
		if l.at(j) == ':' && (l.at(j+1) == ' ' || j+1 == len(l.s)) {
			k = Key
		}
		l.mark(i, j, k)
		l.i = j
	case isNeonKey(c):
		j := i
		for j < len(l.s) && isNeonKey(l.s[j]) {
			j++
		}
		word := string(l.s[i:j])
		k := Plain
		switch {
		case l.at(j) == ':' && (l.at(j+1) == ' ' || l.at(j+1) == '\t' || j+1 == len(l.s)) || l.at(j) == '=' || l.nextNonSpace(j) == ':' && l.at(j) == ' ':
			k = Key
		case neonConstants[word]:
			k = Constant
		case l.at(j) == '(':
			k = Function
		case strings.Contains(word, `\`) || word[0] >= 'A' && word[0] <= 'Z':
			k = Type
		case l.at(j) == ':' && l.at(j+1) == ':':
			k = Type
		}
		l.mark(i, j, k)
		l.i = j
	case strings.ContainsRune(":=,()[]{}", c):
		l.mark(i, i+1, Operator)
		l.i++
	default:
		l.i++
	}
}
