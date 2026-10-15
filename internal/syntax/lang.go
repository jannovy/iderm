package syntax

import (
	"path/filepath"
	"strings"
)

// Lang is the language of a document.
type Lang uint8

const (
	LangPHP Lang = iota
	LangLatte
	LangHTML
	LangCSS
	LangJS
	LangJSON
	LangNEON
	LangText // no highlighting
)

var langNames = [...]string{"PHP", "Latte", "HTML", "CSS", "JavaScript", "JSON", "NEON", "Text"}

func (l Lang) String() string { return langNames[l] }

var langByExt = map[string]Lang{
	".php": LangPHP, ".phtml": LangPHP, ".inc": LangPHP, ".phpt": LangPHP, ".module": LangPHP,
	".latte": LangLatte,
	".html":  LangHTML, ".htm": LangHTML, ".xhtml": LangHTML, ".vue": LangHTML, ".svelte": LangHTML, ".svg": LangHTML, ".xml": LangHTML,
	".css": LangCSS, ".scss": LangCSS, ".less": LangCSS,
	".js": LangJS, ".mjs": LangJS, ".cjs": LangJS, ".jsx": LangJS, ".ts": LangJS, ".tsx": LangJS, ".mts": LangJS, ".cts": LangJS,
	".json": LangJSON, ".jsonc": LangJSON, ".lock": LangJSON, ".map": LangJSON, ".webmanifest": LangJSON,
	".neon": LangNEON,
}

// LangFor picks the language from a file name; an empty path is PHP.
func LangFor(path string) Lang {
	if path == "" {
		return LangPHP
	}
	name := strings.ToLower(filepath.Base(path))
	switch name {
	case ".babelrc", ".eslintrc", ".prettierrc", "composer.lock":
		return LangJSON
	}
	if strings.HasSuffix(name, ".neon.dist") {
		return LangNEON
	}
	if l, ok := langByExt[filepath.Ext(name)]; ok {
		return l
	}
	return LangText
}

// LangName is the display name of a language, with TypeScript told apart.
func LangName(lang Lang, path string) string {
	if lang == LangJS {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".ts", ".tsx", ".mts", ".cts":
			return "TypeScript"
		}
	}
	return lang.String()
}

func initialState(lang Lang) State {
	st := State{Lang: lang}
	switch lang {
	case LangCSS:
		st.Mode = ModeCSS
	case LangJS:
		st.Mode = ModeJS
	case LangJSON:
		st.Mode = ModeJSON
	case LangNEON:
		st.Mode = ModeNEON
	}
	return st
}

// markupMode lexes one token in any non-PHP mode.
func (l *lexer) markupMode() {
	switch l.st.Mode {
	case ModeHTML:
		l.htmlText()
	case ModeTag:
		l.tag()
	case ModeAttrSingle:
		l.attrValue('\'')
	case ModeAttrDouble:
		l.attrValue('"')
	case ModeHTMLComment:
		l.htmlComment()
	case ModeJS:
		l.js()
	case ModeJSComment:
		l.jsComment()
	case ModeJSSingle:
		l.jsString('\'')
	case ModeJSDouble:
		l.jsString('"')
	case ModeJSTemplate:
		l.jsTemplate()
	case ModeCSS:
		l.css()
	case ModeCSSComment:
		l.cssComment()
	case ModeJSON:
		l.json()
	case ModeNEON:
		l.neon()
	case ModeLatte:
		l.latte('}')
	case ModeLatteAttr:
		l.latte('"')
	case ModeLatteComment:
		l.latteComment()
	default:
		l.i = len(l.s)
	}
}

// endOfLine fixes up modes that cannot continue on the next line.
func (l *lexer) endOfLine() {
	switch l.st.Mode {
	case ModeJSSingle, ModeJSDouble:
		if len(l.s) == 0 || l.s[len(l.s)-1] != '\\' {
			l.st.Mode = ModeJS
		}
	}
}

// embedded enters PHP (in PHP files) or a Latte tag (in Latte files)
// starting at l.i, returning whether it did.
func (l *lexer) embedded() bool {
	i := l.i
	switch l.st.Lang {
	case LangPHP:
		if l.at(i) != '<' || l.at(i+1) != '?' {
			return false
		}
		n := 0
		if l.hasPrefixFold(i+2, "php") && !isIdent(l.at(i+5)) {
			n = 5
		} else if l.at(i+2) == '=' {
			n = 3
		}
		if n == 0 {
			return false
		}
		l.mark(i, i+n, Tag)
		l.i += n
		l.st.Ret, l.st.Mode = l.st.Mode, ModePHP
		return true
	case LangLatte:
		if !l.latteOpen(i) {
			return false
		}
		l.st.Ret = l.st.Mode
		if l.at(i+1) == '*' {
			l.mark(i, i+2, Comment)
			l.i += 2
			l.st.Mode = ModeLatteComment
			return true
		}
		l.mark(i, i+1, Tag)
		j := i + 1
		if l.at(j) == '/' {
			l.mark(j, j+1, Tag)
			j++
		}
		k := j
		for k < len(l.s) && (isIdent(l.s[k]) || l.s[k] == ':' || l.s[k] == '.') && l.s[k] != '$' {
			k++
		}
		l.mark(j, k, Keyword)
		l.i = k
		l.st.Mode = ModeLatte
		return true
	}
	return false
}

// latteOpen reports whether a Latte tag starts at i: a brace followed by
// something other than whitespace, a quote or another brace.
func (l *lexer) latteOpen(i int) bool {
	if l.at(i) != '{' || i+1 >= len(l.s) {
		return false
	}
	switch l.s[i+1] {
	case ' ', '\t', '\r', '\'', '"', '{', '}':
		return false
	}
	return true
}

// skipTo marks runes from l.i up to the first stop rune (or a possible
// embedded start) with k and moves there.
func (l *lexer) skipTo(k Kind, stop string) {
	j := l.i
	for j < len(l.s) && !strings.ContainsRune(stop, l.s[j]) && !l.mayEmbed(j) {
		j++
	}
	if j == l.i {
		j++
	}
	l.mark(l.i, j, k)
	l.i = j
}

// mayEmbed reports whether PHP or Latte could start at j.
func (l *lexer) mayEmbed(j int) bool {
	switch l.st.Lang {
	case LangPHP:
		return l.at(j) == '<' && l.at(j+1) == '?'
	case LangLatte:
		return l.at(j) == '{'
	}
	return false
}

func (l *lexer) hasPrefix(i int, p string) bool {
	for j, r := range []rune(p) {
		if l.at(i+j) != r {
			return false
		}
	}
	return true
}

func (l *lexer) word(i int, extra string) int {
	j := i
	for j < len(l.s) && (isIdent(l.s[j]) || strings.ContainsRune(extra, l.s[j])) {
		j++
	}
	return j
}

func (l *lexer) prevNonSpace(j int) rune {
	j--
	for l.at(j) == ' ' || l.at(j) == '\t' {
		j--
	}
	return l.at(j)
}

func (l *lexer) nextNonSpace(j int) rune {
	for l.at(j) == ' ' || l.at(j) == '\t' {
		j++
	}
	return l.at(j)
}

// quoted marks a string starting at l.i that ends on this line.
func (l *lexer) quoted(k Kind) {
	q := l.s[l.i]
	j := l.i + 1
	for j < len(l.s) && l.s[j] != q {
		if l.s[j] == '\\' {
			j++
		}
		j++
	}
	j = min(j+1, len(l.s))
	l.mark(l.i, j, k)
	l.i = j
}
