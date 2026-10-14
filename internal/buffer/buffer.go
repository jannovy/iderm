// Package buffer implements an editable text buffer with undo/redo history.
package buffer

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Pos is a position in the buffer; Col is an index of a rune within the line.
type Pos struct{ Line, Col int }

// Less reports whether p is before q.
func (p Pos) Less(q Pos) bool {
	return p.Line < q.Line || (p.Line == q.Line && p.Col < q.Col)
}

// Order returns both positions sorted.
func Order(a, b Pos) (Pos, Pos) {
	if b.Less(a) {
		return b, a
	}
	return a, b
}

// EndOf returns the position right after text inserted at start.
func EndOf(start Pos, text string) Pos {
	nl := strings.Count(text, "\n")
	if nl == 0 {
		return Pos{start.Line, start.Col + utf8.RuneCountInString(text)}
	}
	last := text[strings.LastIndexByte(text, '\n')+1:]
	return Pos{start.Line + nl, utf8.RuneCountInString(last)}
}

// ErrBinary is returned when loading a file that does not look like text.
var ErrBinary = errors.New("binary file")

type opKind uint8

const (
	opInsert opKind = iota
	opDelete
)

type edit struct {
	kind  opKind
	start Pos
	text  string
	group int
	id    int
}

func (e edit) end() Pos { return EndOf(e.start, e.text) }

// Buffer holds lines of text together with the edit history.
type Buffer struct {
	Path string
	CRLF bool

	lines      [][]rune
	undo, redo []edit
	nextID     int
	nextGroup  int
	groupDepth int
	curGroup   int
	savedID    int
	version    int
	dirtyFrom  int
	merge      bool
}

// New creates an empty buffer.
func New() *Buffer {
	return &Buffer{lines: [][]rune{{}}}
}

// Load reads a file into a new buffer.
func Load(path string) (*Buffer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
		return nil, ErrBinary
	}
	b := New()
	b.Path = path
	s := string(data)
	if strings.Contains(s, "\r\n") {
		b.CRLF = true
		s = strings.ReplaceAll(s, "\r\n", "\n")
	}
	parts := strings.Split(s, "\n")
	b.lines = make([][]rune, len(parts))
	for i, p := range parts {
		b.lines[i] = []rune(p)
	}
	return b, nil
}

// Save writes the buffer to its Path.
func (b *Buffer) Save() error {
	s := b.Text()
	if b.CRLF {
		s = strings.ReplaceAll(s, "\n", "\r\n")
	}
	perm := os.FileMode(0o644)
	if fi, err := os.Stat(b.Path); err == nil {
		perm = fi.Mode().Perm()
	}
	if err := os.WriteFile(b.Path, []byte(s), perm); err != nil {
		return err
	}
	b.savedID = b.topID()
	b.merge = false
	return nil
}

// Text returns the whole content joined by "\n".
func (b *Buffer) Text() string {
	var sb strings.Builder
	for i, l := range b.lines {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(string(l))
	}
	return sb.String()
}

// LineCount returns the number of lines (always at least 1).
func (b *Buffer) LineCount() int { return len(b.lines) }

// Line returns the runes of line i. The slice must not be modified.
func (b *Buffer) Line(i int) []rune { return b.lines[i] }

// Version increases with every modification.
func (b *Buffer) Version() int { return b.version }

// Dirty reports whether the buffer differs from the last saved state.
func (b *Buffer) Dirty() bool { return b.topID() != b.savedID }

// TakeDirtyFrom returns the first line modified since the previous call, or -1.
func (b *Buffer) TakeDirtyFrom() int {
	d := b.dirtyFrom
	b.dirtyFrom = -1
	return d
}

// Clamp moves p to the nearest valid position.
func (b *Buffer) Clamp(p Pos) Pos {
	p.Line = max(0, min(p.Line, len(b.lines)-1))
	p.Col = max(0, min(p.Col, len(b.lines[p.Line])))
	return p
}

// End returns the position after the last character.
func (b *Buffer) End() Pos {
	l := len(b.lines) - 1
	return Pos{l, len(b.lines[l])}
}

// TextRange returns the text between two positions.
func (b *Buffer) TextRange(start, end Pos) string {
	start, end = Order(b.Clamp(start), b.Clamp(end))
	if start.Line == end.Line {
		return string(b.lines[start.Line][start.Col:end.Col])
	}
	var sb strings.Builder
	sb.WriteString(string(b.lines[start.Line][start.Col:]))
	for i := start.Line + 1; i < end.Line; i++ {
		sb.WriteByte('\n')
		sb.WriteString(string(b.lines[i]))
	}
	sb.WriteByte('\n')
	sb.WriteString(string(b.lines[end.Line][:end.Col]))
	return sb.String()
}

// BeginGroup starts a compound edit that is undone as a single step.
func (b *Buffer) BeginGroup() {
	if b.groupDepth == 0 {
		b.nextGroup++
		b.curGroup = b.nextGroup
		b.merge = false
	}
	b.groupDepth++
}

// EndGroup finishes a compound edit.
func (b *Buffer) EndGroup() {
	b.groupDepth--
	if b.groupDepth == 0 {
		b.merge = false
	}
}

// Seal prevents the next edit from being merged with the previous one.
func (b *Buffer) Seal() { b.merge = false }

// Insert inserts text at p and returns the position after it.
func (b *Buffer) Insert(p Pos, text string) Pos {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	p = b.Clamp(p)
	if text == "" {
		return p
	}
	end := b.rawInsert(p, text)
	single := utf8.RuneCountInString(text) == 1 && text != "\n"
	if single && b.canMerge() {
		top := &b.undo[len(b.undo)-1]
		if top.kind == opInsert && top.end() == p && !(text == " " && !strings.HasSuffix(top.text, " ")) {
			top.text += text
			top.id = b.newID()
			b.redo = nil
			return end
		}
	}
	b.record(edit{kind: opInsert, start: p, text: text})
	b.merge = single
	return end
}

// Delete removes the text between start and end and returns it.
func (b *Buffer) Delete(start, end Pos) string {
	start, end = Order(b.Clamp(start), b.Clamp(end))
	if start == end {
		return ""
	}
	text := b.rawDelete(start, end)
	single := utf8.RuneCountInString(text) == 1 && text != "\n"
	if single && b.canMerge() {
		top := &b.undo[len(b.undo)-1]
		if top.kind == opDelete && (end == top.start || start == top.start) {
			if end == top.start {
				top.start = start
				top.text = text + top.text
			} else {
				top.text += text
			}
			top.id = b.newID()
			b.redo = nil
			return text
		}
	}
	b.record(edit{kind: opDelete, start: start, text: text})
	b.merge = single
	return text
}

// Undo reverts the last edit group and returns a suitable cursor position.
func (b *Buffer) Undo() (Pos, bool) {
	if len(b.undo) == 0 {
		return Pos{}, false
	}
	g := b.undo[len(b.undo)-1].group
	var cur Pos
	for len(b.undo) > 0 && b.undo[len(b.undo)-1].group == g {
		e := b.undo[len(b.undo)-1]
		b.undo = b.undo[:len(b.undo)-1]
		if e.kind == opInsert {
			b.rawDelete(e.start, e.end())
			cur = e.start
		} else {
			cur = b.rawInsert(e.start, e.text)
		}
		b.redo = append(b.redo, e)
	}
	b.merge = false
	return cur, true
}

// Redo re-applies the last undone edit group.
func (b *Buffer) Redo() (Pos, bool) {
	if len(b.redo) == 0 {
		return Pos{}, false
	}
	g := b.redo[len(b.redo)-1].group
	var cur Pos
	for len(b.redo) > 0 && b.redo[len(b.redo)-1].group == g {
		e := b.redo[len(b.redo)-1]
		b.redo = b.redo[:len(b.redo)-1]
		if e.kind == opInsert {
			cur = b.rawInsert(e.start, e.text)
		} else {
			b.rawDelete(e.start, e.end())
			cur = e.start
		}
		b.undo = append(b.undo, e)
	}
	b.merge = false
	return cur, true
}

// Find searches for query from the given position. Matches never span lines.
// With wrap, the search continues from the other end of the buffer.
func (b *Buffer) Find(query string, from Pos, backward, fold, wrap bool) (Pos, bool) {
	q := []rune(query)
	if len(q) == 0 {
		return Pos{}, false
	}
	from = b.Clamp(from)
	n := len(b.lines)
	for k := 0; k <= n; k++ {
		var li int
		if backward {
			li = from.Line - k
			if li < 0 {
				if !wrap {
					break
				}
				li += n
			}
		} else {
			li = from.Line + k
			if li >= n {
				if !wrap {
					break
				}
				li -= n
			}
		}
		line := b.lines[li]
		if backward {
			before := len(line) + 1
			if k == 0 {
				before = from.Col
			}
			if i := LastIndex(line, q, before, fold); i >= 0 {
				return Pos{li, i}, true
			}
		} else {
			start := 0
			if k == 0 {
				start = from.Col
			}
			if i := Index(line, q, start, fold); i >= 0 {
				return Pos{li, i}, true
			}
		}
	}
	return Pos{}, false
}

// Index returns the first index >= from where q occurs in line, or -1.
func Index(line, q []rune, from int, fold bool) int {
	for i := max(from, 0); i+len(q) <= len(line); i++ {
		if matchAt(line, q, i, fold) {
			return i
		}
	}
	return -1
}

// LastIndex returns the last index < before where q occurs in line, or -1.
func LastIndex(line, q []rune, before int, fold bool) int {
	for i := min(before-1, len(line)-len(q)); i >= 0; i-- {
		if matchAt(line, q, i, fold) {
			return i
		}
	}
	return -1
}

func matchAt(line, q []rune, i int, fold bool) bool {
	for j, r := range q {
		c := line[i+j]
		if c != r && (!fold || unicode.ToLower(c) != unicode.ToLower(r)) {
			return false
		}
	}
	return true
}

func (b *Buffer) canMerge() bool {
	return b.merge && b.groupDepth == 0 && len(b.undo) > 0
}

func (b *Buffer) newID() int {
	b.nextID++
	return b.nextID
}

func (b *Buffer) topID() int {
	if len(b.undo) == 0 {
		return 0
	}
	return b.undo[len(b.undo)-1].id
}

func (b *Buffer) record(e edit) {
	e.id = b.newID()
	if b.groupDepth > 0 {
		e.group = b.curGroup
	} else {
		b.nextGroup++
		e.group = b.nextGroup
	}
	b.undo = append(b.undo, e)
	b.redo = nil
}

func (b *Buffer) touch(line int) {
	b.version++
	if b.dirtyFrom < 0 || line < b.dirtyFrom {
		b.dirtyFrom = line
	}
}

func (b *Buffer) rawInsert(p Pos, text string) Pos {
	b.touch(p.Line)
	parts := strings.Split(text, "\n")
	line := b.lines[p.Line]
	head, tail := line[:p.Col], line[p.Col:]
	if len(parts) == 1 {
		ins := []rune(parts[0])
		nl := make([]rune, 0, len(line)+len(ins))
		nl = append(append(append(nl, head...), ins...), tail...)
		b.lines[p.Line] = nl
		return Pos{p.Line, p.Col + len(ins)}
	}
	newLines := make([][]rune, len(parts))
	first := []rune(parts[0])
	newLines[0] = append(append(make([]rune, 0, len(head)+len(first)), head...), first...)
	for i := 1; i < len(parts)-1; i++ {
		newLines[i] = []rune(parts[i])
	}
	last := []rune(parts[len(parts)-1])
	newLines[len(parts)-1] = append(append(make([]rune, 0, len(last)+len(tail)), last...), tail...)
	b.lines = append(b.lines[:p.Line], append(newLines, b.lines[p.Line+1:]...)...)
	return Pos{p.Line + len(parts) - 1, len(last)}
}

func (b *Buffer) rawDelete(start, end Pos) string {
	text := b.TextRange(start, end)
	b.touch(start.Line)
	head := b.lines[start.Line][:start.Col]
	tail := b.lines[end.Line][end.Col:]
	nl := append(append(make([]rune, 0, len(head)+len(tail)), head...), tail...)
	b.lines[start.Line] = nl
	b.lines = append(b.lines[:start.Line+1], b.lines[end.Line+1:]...)
	return text
}
