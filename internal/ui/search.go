package ui

import (
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/jannovy/iderm/internal/buffer"
)

// smartFold: the search ignores case unless the query contains an uppercase letter.
func smartFold(q string) bool { return q == strings.ToLower(q) }

// selectedWord returns the single-line selection, used to prefill prompts.
func (t *Tab) selectedWord() string {
	if s, e, ok := t.selection(); ok && s.Line == e.Line {
		return t.buf.TextRange(s, e)
	}
	return ""
}

func (a *App) searchFrom(t *Tab, q string, from Pos, backward bool) bool {
	p, ok := t.buf.Find(q, from, backward, smartFold(q), true)
	if !ok {
		a.setErr("Not found: %s", q)
		return false
	}
	t.buf.Seal()
	t.anchor, t.sel = p, true
	t.place(buffer.EndOf(p, q))
	return true
}

func (a *App) findNext(backward bool) {
	if a.lastQuery == "" {
		a.find()
		return
	}
	t := a.tab()
	from := t.cur
	if s, _, ok := t.selection(); ok && backward {
		from = s
	}
	a.showMatches = true
	a.searchFrom(t, a.lastQuery, from, backward)
}

func (a *App) find() {
	t := a.tab()
	initial := a.lastQuery
	if w := t.selectedWord(); w != "" {
		initial = w
	}
	origin, _, _ := t.selection()
	p := newPrompt("Find (Enter/↓ next, ↑ previous): ", initial)
	p.onChange = func(q string) {
		a.lastQuery = q
		a.showMatches = q != ""
		if q == "" {
			t.moveTo(origin, false)
			return
		}
		a.searchFrom(t, q, origin, false)
	}
	p.onEnter = func(q string) {
		a.findNext(false)
		a.prompt = p
	}
	p.onKey = func(ev *tcell.EventKey) bool {
		switch ev.Key() {
		case tcell.KeyDown:
			a.findNext(false)
		case tcell.KeyUp:
			a.findNext(true)
		default:
			return false
		}
		return true
	}
	a.prompt = p
	if initial != "" {
		p.onChange(initial)
	}
}

func (a *App) replace() {
	t := a.tab()
	initial := a.lastQuery
	if w := t.selectedWord(); w != "" {
		initial = w
	}
	p := newPrompt("Replace: ", initial)
	p.onEnter = func(q string) {
		if q == "" {
			return
		}
		a.lastQuery = q
		w := newPrompt("Replace \""+q+"\" with: ", a.lastReplace)
		w.onEnter = func(r string) {
			a.lastReplace = r
			a.replaceLoop(t, q, r)
		}
		a.prompt = w
	}
	a.prompt = p
}

// replaceLoop asks about every occurrence of q from the top of the file.
func (a *App) replaceLoop(t *Tab, q, r string) {
	fold := smartFold(q)
	pos := Pos{}
	count := 0
	a.showMatches = true
	doReplace := func(p Pos) {
		t.buf.Delete(p, buffer.EndOf(p, q))
		pos = t.buf.Insert(p, r)
		count++
	}
	finish := func() {
		a.prompt = nil
		t.sel = false
		a.setMsg("Replaced %d occurrence(s)", count)
		a.scheduleLint(t)
	}
	var next func()
	next = func() {
		p, ok := t.buf.Find(q, pos, false, fold, false)
		if !ok {
			finish()
			return
		}
		t.anchor, t.sel = p, true
		t.place(buffer.EndOf(p, q))
		pr := newPrompt("Replace this occurrence? (y)es (n)o (a)ll (q)uit", "")
		pr.onKey = func(ev *tcell.EventKey) bool {
			if ev.Key() == tcell.KeyEscape {
				finish()
				return true
			}
			switch ev.Rune() {
			case 'y', 'Y':
				t.buf.BeginGroup()
				doReplace(p)
				t.buf.EndGroup()
				next()
			case 'n', 'N':
				pos = buffer.EndOf(p, q)
				next()
			case 'a', 'A':
				t.buf.BeginGroup()
				for ok {
					doReplace(p)
					p, ok = t.buf.Find(q, pos, false, fold, false)
				}
				t.buf.EndGroup()
				finish()
			case 'q', 'Q':
				finish()
			}
			return true
		}
		a.prompt = pr
	}
	next()
}

func (a *App) gotoLine() {
	t := a.tab()
	p := newPrompt("Go to line[:column]: ", "")
	p.onEnter = func(s string) {
		parts := strings.SplitN(strings.TrimSpace(s), ":", 2)
		line, err := strconv.Atoi(parts[0])
		if err != nil {
			a.setErr("Not a line number: %s", s)
			return
		}
		col := 1
		if len(parts) == 2 {
			col, _ = strconv.Atoi(parts[1])
		}
		t.moveTo(Pos{line - 1, max(col-1, 0)}, false)
	}
	a.prompt = p
}
