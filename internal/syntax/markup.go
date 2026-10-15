package syntax

import "strings"

const (
	tagScript = 1
	tagStyle  = 2
)

func isTagName(r rune) bool { return isIdent(r) || r == '-' || r == ':' || r == '.' }

func (l *lexer) htmlText() {
	if l.embedded() {
		return
	}
	i, c := l.i, l.s[l.i]
	switch {
	case l.hasPrefix(i, "<!--"):
		l.mark(i, i+4, Comment)
		l.i += 4
		l.st.Mode = ModeHTMLComment
	case c == '<' && l.at(i+1) == '!':
		j := i
		for j < len(l.s) && l.s[j] != '>' {
			j++
		}
		l.mark(i, min(j+1, len(l.s)), Tag)
		l.i = min(j+1, len(l.s))
	case c == '<' && (isIdentStart(l.at(i+1)) || l.at(i+1) == '/' && isIdentStart(l.at(i+2))):
		j := i + 1
		closing := l.at(j) == '/'
		if closing {
			j++
		}
		k := j
		for k < len(l.s) && isTagName(l.s[k]) {
			k++
		}
		l.mark(i, k, Tag)
		l.i = k
		l.st.Mode, l.st.Tag = ModeTag, 0
		if !closing {
			switch strings.ToLower(string(l.s[j:k])) {
			case "script":
				l.st.Tag = tagScript
			case "style":
				l.st.Tag = tagStyle
			}
		}
	case c == '&':
		j := i + 1
		for j < len(l.s) && (isIdent(l.s[j]) || l.s[j] == '#') {
			j++
		}
		if l.at(j) == ';' && j > i+1 {
			l.mark(i, j+1, Constant)
			l.i = j + 1
		} else {
			l.i++
		}
	default:
		l.skipTo(Plain, "<&")
	}
}

func (l *lexer) tag() {
	if l.embedded() {
		return
	}
	i, c := l.i, l.s[l.i]
	nattr := l.nattr
	l.nattr = false
	switch {
	case c == ' ' || c == '\t' || c == '\r':
		l.i++
		l.nattr = nattr
	case c == '/' && l.at(i+1) == '>':
		l.mark(i, i+2, Tag)
		l.i += 2
		l.st.Mode, l.st.Tag = ModeHTML, 0
	case c == '>':
		l.mark(i, i+1, Tag)
		l.i++
		switch l.st.Tag {
		case tagScript:
			l.st.Mode = ModeJS
		case tagStyle:
			l.st.Mode, l.st.Depth = ModeCSS, 0
		default:
			l.st.Mode = ModeHTML
		}
		l.st.Tag = 0
	case c == '=':
		l.mark(i, i+1, Operator)
		l.i++
		l.nattr = nattr
	case c == '"':
		l.mark(i, i+1, String)
		l.i++
		l.st.Mode = ModeAttrDouble
		if nattr {
			l.st.Mode = ModeLatteAttr
		}
	case c == '\'':
		l.mark(i, i+1, String)
		l.i++
		l.st.Mode = ModeAttrSingle
	default:
		j := i
		for j < len(l.s) && !strings.ContainsRune(" \t\r=>/\"'", l.s[j]) && !l.mayEmbed(j) {
			j++
		}
		if j == i {
			l.i++
			return
		}
		k := Attr
		if l.st.Lang == LangLatte && l.hasPrefix(i, "n:") {
			k, l.nattr = Keyword, true
		}
		l.mark(i, j, k)
		l.i = j
	}
}

func (l *lexer) attrValue(q rune) {
	if l.embedded() {
		return
	}
	if l.s[l.i] == q {
		l.mark(l.i, l.i+1, String)
		l.i++
		l.st.Mode = ModeTag
		return
	}
	l.skipTo(String, string(q))
}

func (l *lexer) htmlComment() {
	for j := l.i; j < len(l.s); j++ {
		if l.hasPrefix(j, "-->") {
			l.mark(l.i, j+3, Comment)
			l.i = j + 3
			l.st.Mode = ModeHTML
			return
		}
	}
	l.mark(l.i, len(l.s), Comment)
	l.i = len(l.s)
}

// latte lexes the expression of a Latte tag, which ends at end ('}' or the
// '"' of an n:attribute).
func (l *lexer) latte(end rune) {
	i, c := l.i, l.s[l.i]
	l.member = l.member && (c == ' ' || c == '\t')
	switch {
	case c == end:
		l.mark(i, i+1, Tag)
		if end == '"' {
			l.mark(i, i+1, String)
			l.st.Mode = ModeTag
		} else {
			l.st.Mode = l.st.Ret
		}
		l.i++
	case c == '\'' || c == '"':
		l.quoted(String)
	case c == '|' && isIdentStart(l.at(i+1)):
		j := l.word(i+1, "")
		l.mark(i, i+1, Operator)
		l.mark(i+1, j, Function)
		l.i = j
	case c == '#' || c == '/' || c == '?' && l.at(i+1) == '>' || c == '<' && l.at(i+1) == '<':
		l.i++
	default:
		l.php()
	}
}

func (l *lexer) latteComment() {
	for j := l.i; j+1 < len(l.s); j++ {
		if l.s[j] == '*' && l.s[j+1] == '}' {
			l.mark(l.i, j+2, Comment)
			l.i = j + 2
			l.st.Mode = l.st.Ret
			return
		}
	}
	l.mark(l.i, len(l.s), Comment)
	l.i = len(l.s)
}
