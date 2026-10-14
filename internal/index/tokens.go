// Package index builds a symbol index of a PHP project and resolves
// definitions of classes, functions and class members.
package index

import (
	"strings"

	"iderm/internal/syntax"
)

// TokKind is the kind of a code token.
type TokKind uint8

const (
	TIdent TokKind = iota // identifier, possibly namespaced (Foo\Bar)
	TVar                  // $variable
	TOp                   // punctuation and operators
	TDoc                  // /** doc comment */
)

// Token is a piece of PHP code; strings, comments and inline HTML are skipped.
type Token struct {
	Kind      TokKind
	Text      string
	Line, Col int // start position (rune column)
	End       int // end column on Line (exclusive)
}

// Lines is the source of text.
type Lines = syntax.Lines

func (t Token) is(op string) bool { return t.Kind == TOp && t.Text == op }

func (t Token) isWord(w string) bool { return t.Kind == TIdent && strings.EqualFold(t.Text, w) }

// Tokenize splits PHP source into code tokens.
func Tokenize(src Lines) []Token {
	var toks []Token
	st := syntax.Initial
	var doc strings.Builder
	docActive := false
	docLine, docCol := 0, 0
	for li := 0; li < src.LineCount(); li++ {
		line := src.Line(li)
		kinds, nst := syntax.Lex(line, st)
		st = nst
		if docActive {
			doc.WriteByte('\n')
		}
		for i := 0; i < len(line); {
			k := kinds[i]
			r := line[i]
			if k == syntax.Comment {
				if !docActive && r == '/' && hasAt(line, i, "/**") {
					docActive = true
					docLine, docCol = li, i
					doc.Reset()
				}
				if docActive {
					doc.WriteRune(r)
					if r == '/' && i > 0 && line[i-1] == '*' && doc.Len() > 3 {
						toks = append(toks, Token{Kind: TDoc, Text: doc.String(), Line: docLine, Col: docCol, End: docCol})
						docActive = false
					}
				}
				i++
				continue
			}
			if k == syntax.String || k == syntax.HTML || k == syntax.Tag {
				i++
				continue
			}
			switch {
			case r == ' ' || r == '\t' || r == '\r':
				i++
			case r == '$' && i+1 < len(line) && isIdentStart(line[i+1]):
				j := i + 1
				for j < len(line) && isIdent(line[j]) {
					j++
				}
				toks = append(toks, Token{Kind: TVar, Text: string(line[i:j]), Line: li, Col: i, End: j})
				i = j
			case isIdentStart(r) || r == '\\' && i+1 < len(line) && isIdentStart(line[i+1]):
				j := i
				for j < len(line) && (isIdent(line[j]) || line[j] == '\\' && j+1 < len(line) && isIdentStart(line[j+1])) {
					j++
				}
				toks = append(toks, Token{Kind: TIdent, Text: string(line[i:j]), Line: li, Col: i, End: j})
				i = j
			case r >= '0' && r <= '9':
				j := i
				for j < len(line) && (isIdent(line[j]) || line[j] == '.') {
					j++
				}
				i = j
			default:
				op, n := string(r), 1
				switch {
				case hasAt(line, i, "?->"):
					op, n = "->", 3
				case hasAt(line, i, "->"), hasAt(line, i, "::"), hasAt(line, i, "=>"):
					op, n = string(line[i:i+2]), 2
				}
				toks = append(toks, Token{Kind: TOp, Text: op, Line: li, Col: i, End: i + n})
				i += n
			}
		}
	}
	return toks
}

func hasAt(line []rune, i int, s string) bool {
	for j, r := range []rune(s) {
		if i+j >= len(line) || line[i+j] != r {
			return false
		}
	}
	return true
}

func isIdentStart(r rune) bool {
	return r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= 0x80
}

func isIdent(r rune) bool { return isIdentStart(r) || r >= '0' && r <= '9' }

// runeLines adapts a string to Lines.
type runeLines [][]rune

func (l runeLines) LineCount() int    { return len(l) }
func (l runeLines) Line(i int) []rune { return l[i] }

// SplitLines converts text to Lines.
func SplitLines(text string) Lines {
	parts := strings.Split(text, "\n")
	l := make(runeLines, len(parts))
	for i, p := range parts {
		l[i] = []rune(strings.TrimSuffix(p, "\r"))
	}
	return l
}
