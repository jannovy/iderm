package ui

import (
	"path/filepath"
	"strings"

	"github.com/jannovy/iderm/internal/buffer"
	"github.com/jannovy/iderm/internal/git"
	"github.com/jannovy/iderm/internal/syntax"
)

type Pos = buffer.Pos

const indentUnit = "    "

// Tab is one open document together with its view state.
type Tab struct {
	buf    *buffer.Buffer
	cur    Pos
	anchor Pos
	sel    bool
	wantV  int // preferred visual column for vertical movement
	follow bool

	scrollY, scrollX int

	hl      *syntax.Highlighter // nil for plain text
	lang    syntax.Lang
	lint    []lintError
	lintVer int // buffer version the lint result belongs to

	head     []string // file content at HEAD, for change markers
	hasHead  bool
	marks    []git.LineMark
	hunks    []git.Hunk
	marksVer int
}

func newTab(b *buffer.Buffer) *Tab {
	t := &Tab{buf: b, follow: true, lintVer: -1, marksVer: -1}
	t.updateLanguage()
	return t
}

func (t *Tab) updateLanguage() {
	lang := syntax.LangFor(t.buf.Path)
	switch {
	case lang == syntax.LangText:
		t.hl = nil
	case t.hl == nil || lang != t.lang:
		t.hl = syntax.NewHighlighter(lang)
	}
	t.lang = lang
	if !t.isPHP() {
		t.lint = nil
	}
}

// isPHP reports whether the tab holds PHP code (lint, completion, definitions).
func (t *Tab) isPHP() bool { return t.lang == syntax.LangPHP }

func (t *Tab) title() string {
	if t.buf.Path == "" {
		return "untitled"
	}
	return filepath.Base(t.buf.Path)
}

func (t *Tab) pristine() bool {
	return t.buf.Path == "" && !t.buf.Dirty() && t.buf.LineCount() == 1 && len(t.buf.Line(0)) == 0
}

// selection returns the ordered selection, ok is false when nothing is selected.
func (t *Tab) selection() (Pos, Pos, bool) {
	if !t.sel || t.anchor == t.cur {
		return t.cur, t.cur, false
	}
	s, e := buffer.Order(t.anchor, t.cur)
	return s, e, true
}

// selectedLines returns the range of lines touched by the selection or the cursor.
func (t *Tab) selectedLines() (int, int) {
	s, e, ok := t.selection()
	if !ok {
		return t.cur.Line, t.cur.Line
	}
	last := e.Line
	if e.Col == 0 && e.Line > s.Line {
		last--
	}
	return s.Line, last
}

// place moves the cursor after an edit without touching the selection anchor.
func (t *Tab) place(p Pos) {
	t.cur = t.buf.Clamp(p)
	t.wantV = visualCol(t.buf.Line(t.cur.Line), t.cur.Col)
	t.follow = true
}

// moveTo is cursor navigation; with extend it grows the selection.
func (t *Tab) moveTo(p Pos, extend bool) {
	t.buf.Seal()
	if extend {
		if !t.sel {
			t.anchor = t.cur
			t.sel = true
		}
	} else {
		t.sel = false
	}
	t.place(p)
}

func (t *Tab) moveVert(dy int, extend bool) {
	t.buf.Seal()
	if extend {
		if !t.sel {
			t.anchor = t.cur
			t.sel = true
		}
	} else {
		t.sel = false
	}
	line := max(0, min(t.cur.Line+dy, t.buf.LineCount()-1))
	if line == t.cur.Line && dy != 0 {
		if dy < 0 {
			t.place(Pos{line, 0})
		} else {
			t.place(Pos{line, len(t.buf.Line(line))})
		}
		return
	}
	t.cur = Pos{line, colForVisual(t.buf.Line(line), t.wantV)}
	t.follow = true
}

func (t *Tab) left(p Pos) Pos {
	if p.Col > 0 {
		return Pos{p.Line, p.Col - 1}
	}
	if p.Line > 0 {
		return Pos{p.Line - 1, len(t.buf.Line(p.Line - 1))}
	}
	return p
}

func (t *Tab) right(p Pos) Pos {
	if p.Col < len(t.buf.Line(p.Line)) {
		return Pos{p.Line, p.Col + 1}
	}
	if p.Line+1 < t.buf.LineCount() {
		return Pos{p.Line + 1, 0}
	}
	return p
}

func (t *Tab) wordLeft(p Pos) Pos {
	if p.Col == 0 {
		return t.left(p)
	}
	line := t.buf.Line(p.Line)
	i := p.Col
	for i > 0 && charClass(line[i-1]) == 0 {
		i--
	}
	if i > 0 {
		c := charClass(line[i-1])
		for i > 0 && charClass(line[i-1]) == c {
			i--
		}
	}
	return Pos{p.Line, i}
}

func (t *Tab) wordRight(p Pos) Pos {
	line := t.buf.Line(p.Line)
	if p.Col >= len(line) {
		return t.right(p)
	}
	i := p.Col
	for i < len(line) && charClass(line[i]) == 0 {
		i++
	}
	if i < len(line) {
		c := charClass(line[i])
		for i < len(line) && charClass(line[i]) == c {
			i++
		}
	}
	return Pos{p.Line, i}
}

// smartHome toggles between the first non-blank character and column 0.
func (t *Tab) smartHome() Pos {
	ind := len(leadingWhitespace(t.buf.Line(t.cur.Line)))
	if t.cur.Col == ind {
		return Pos{t.cur.Line, 0}
	}
	return Pos{t.cur.Line, ind}
}

func (t *Tab) selectWord(p Pos) {
	line := t.buf.Line(p.Line)
	if len(line) == 0 {
		t.moveTo(p, false)
		return
	}
	i := min(p.Col, len(line)-1)
	c := charClass(line[i])
	s, e := i, i
	for s > 0 && charClass(line[s-1]) == c {
		s--
	}
	for e < len(line) && charClass(line[e]) == c {
		e++
	}
	t.moveTo(Pos{p.Line, s}, false)
	t.moveTo(Pos{p.Line, e}, true)
}

func (t *Tab) selectAll() {
	t.moveTo(Pos{}, false)
	t.moveTo(t.buf.End(), true)
}

func (t *Tab) deleteSelection() bool {
	s, e, ok := t.selection()
	t.sel = false
	if !ok {
		return false
	}
	t.buf.Delete(s, e)
	t.place(s)
	return true
}

// insert replaces the selection (if any) with text.
func (t *Tab) insert(text string) {
	if _, _, ok := t.selection(); ok {
		t.buf.BeginGroup()
		defer t.buf.EndGroup()
		t.deleteSelection()
	}
	t.sel = false
	t.place(t.buf.Insert(t.cur, text))
}

func (t *Tab) typeRune(r rune) {
	if _, _, ok := t.selection(); !ok && (r == '}' || r == ')' || r == ']') {
		line := t.buf.Line(t.cur.Line)
		before := line[:t.cur.Col]
		if len(before) > 0 && isBlank(before) {
			n := 1
			if before[len(before)-1] == ' ' {
				n = min(len(before), len(indentUnit))
				for k := 1; k <= n; k++ {
					if before[len(before)-k] != ' ' {
						n = k - 1
						break
					}
				}
			}
			t.buf.BeginGroup()
			t.buf.Delete(Pos{t.cur.Line, t.cur.Col - n}, t.cur)
			t.place(t.buf.Insert(Pos{t.cur.Line, t.cur.Col - n}, string(r)))
			t.buf.EndGroup()
			return
		}
	}
	t.insert(string(r))
}

// newline inserts a line break with automatic indentation.
func (t *Tab) newline() {
	t.buf.BeginGroup()
	defer t.buf.EndGroup()
	t.deleteSelection()
	line := t.buf.Line(t.cur.Line)
	indent := string(leadingWhitespace(line[:t.cur.Col]))
	before := strings.TrimRight(string(line[:t.cur.Col]), " \t")
	after := strings.TrimLeft(string(line[t.cur.Col:]), " \t")
	extra := ""
	if before != "" && strings.ContainsAny(before[len(before)-1:], "{[(") {
		extra = indentUnit
	}
	// Drop trailing whitespace before the cursor.
	trimmed := len([]rune(before))
	if trimmed < t.cur.Col && trimmed >= len([]rune(indent)) {
		t.buf.Delete(Pos{t.cur.Line, trimmed}, t.cur)
		t.cur.Col = trimmed
	}
	text := "\n" + indent + extra
	end := t.buf.Insert(t.cur, text)
	if extra != "" && after != "" && strings.ContainsAny(after[:1], "}])") {
		t.buf.Insert(end, "\n"+indent)
	}
	t.place(end)
}

func (t *Tab) backspace() {
	if t.deleteSelection() {
		return
	}
	if t.cur.Col == 0 {
		if t.cur.Line > 0 {
			p := t.left(t.cur)
			t.buf.Delete(p, t.cur)
			t.place(p)
		}
		return
	}
	line := t.buf.Line(t.cur.Line)
	n := 1
	if before := line[:t.cur.Col]; strings.Trim(string(before), " ") == "" {
		n = t.cur.Col - ((t.cur.Col-1)/tabWidth)*tabWidth
	}
	start := Pos{t.cur.Line, t.cur.Col - n}
	t.buf.Delete(start, t.cur)
	t.place(start)
}

func (t *Tab) deleteForward() {
	if t.deleteSelection() {
		return
	}
	t.buf.Delete(t.cur, t.right(t.cur))
	t.place(t.cur)
}

func (t *Tab) deleteWordBack() {
	if t.deleteSelection() {
		return
	}
	p := t.wordLeft(t.cur)
	t.buf.Delete(p, t.cur)
	t.place(p)
}

func (t *Tab) deleteWordForward() {
	if t.deleteSelection() {
		return
	}
	t.buf.Delete(t.cur, t.wordRight(t.cur))
	t.place(t.cur)
}

func (t *Tab) tabKey() {
	if s, e, ok := t.selection(); ok && s.Line != e.Line {
		t.indentLines(1)
		return
	}
	v := visualCol(t.buf.Line(t.cur.Line), t.cur.Col)
	t.insert(strings.Repeat(" ", tabWidth-v%tabWidth))
}

// indentLines indents (dir > 0) or dedents the selected lines.
func (t *Tab) indentLines(dir int) {
	first, last := t.selectedLines()
	delta := map[int]int{}
	t.buf.BeginGroup()
	for l := first; l <= last; l++ {
		line := t.buf.Line(l)
		if dir > 0 {
			if len(line) > 0 {
				t.buf.Insert(Pos{l, 0}, indentUnit)
				delta[l] = len(indentUnit)
			}
			continue
		}
		k := 0
		for k < len(indentUnit) && k < len(line) && line[k] == ' ' {
			k++
		}
		if k == 0 && len(line) > 0 && line[0] == '\t' {
			k = 1
		}
		if k > 0 {
			t.buf.Delete(Pos{l, 0}, Pos{l, k})
			delta[l] = -k
		}
	}
	t.buf.EndGroup()
	t.shiftCols(delta)
}

func (t *Tab) shiftCols(delta map[int]int) {
	if t.sel {
		t.anchor = t.buf.Clamp(Pos{t.anchor.Line, max(0, t.anchor.Col+delta[t.anchor.Line])})
	}
	t.place(Pos{t.cur.Line, max(0, t.cur.Col+delta[t.cur.Line])})
}

// toggleComment adds or removes "// " on the selected lines.
func (t *Tab) toggleComment() {
	first, last := t.selectedLines()
	minIndent := -1
	commented := true
	for l := first; l <= last; l++ {
		line := t.buf.Line(l)
		if isBlank(line) {
			continue
		}
		ind := len(leadingWhitespace(line))
		if minIndent < 0 || ind < minIndent {
			minIndent = ind
		}
		if !strings.HasPrefix(string(line[ind:]), "//") {
			commented = false
		}
	}
	if minIndent < 0 {
		return
	}
	delta := map[int]int{}
	t.buf.BeginGroup()
	for l := first; l <= last; l++ {
		line := t.buf.Line(l)
		if isBlank(line) {
			continue
		}
		if commented {
			ind := len(leadingWhitespace(line))
			n := 2
			if ind+2 < len(line) && line[ind+2] == ' ' {
				n = 3
			}
			t.buf.Delete(Pos{l, ind}, Pos{l, ind + n})
			delta[l] = -n
		} else {
			t.buf.Insert(Pos{l, minIndent}, "// ")
			delta[l] = 3
		}
	}
	t.buf.EndGroup()
	t.shiftCols(delta)
}

func (t *Tab) duplicateLine() {
	first, last := t.selectedLines()
	text := t.buf.TextRange(Pos{first, 0}, Pos{last, len(t.buf.Line(last))})
	t.buf.Insert(Pos{last, len(t.buf.Line(last))}, "\n"+text)
	n := last - first + 1
	if t.sel {
		t.anchor.Line += n
	}
	t.place(Pos{t.cur.Line + n, t.cur.Col})
}

func (t *Tab) deleteLines() {
	first, last := t.selectedLines()
	t.sel = false
	switch {
	case last+1 < t.buf.LineCount():
		t.buf.Delete(Pos{first, 0}, Pos{last + 1, 0})
	case first > 0:
		t.buf.Delete(Pos{first - 1, len(t.buf.Line(first - 1))}, Pos{last, len(t.buf.Line(last))})
		first--
	default:
		t.buf.Delete(Pos{0, 0}, t.buf.End())
	}
	line := t.buf.Line(first)
	t.cur = Pos{first, colForVisual(line, t.wantV)}
	t.follow = true
}

// moveLines moves the selected lines one line up (dir < 0) or down.
func (t *Tab) moveLines(dir int) {
	first, last := t.selectedLines()
	if dir < 0 && first == 0 || dir > 0 && last+1 >= t.buf.LineCount() {
		return
	}
	t.buf.BeginGroup()
	if dir < 0 {
		prev := string(t.buf.Line(first - 1))
		t.buf.Delete(Pos{first - 1, 0}, Pos{first, 0})
		t.buf.Insert(Pos{last - 1, len(t.buf.Line(last - 1))}, "\n"+prev)
	} else {
		next := string(t.buf.Line(last + 1))
		t.buf.Delete(Pos{last, len(t.buf.Line(last))}, Pos{last + 1, len(t.buf.Line(last + 1))})
		t.buf.Insert(Pos{first, 0}, next+"\n")
	}
	t.buf.EndGroup()
	if t.sel {
		t.anchor.Line += dir
	}
	t.cur.Line += dir
	t.follow = true
}
