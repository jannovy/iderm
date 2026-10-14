package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"

	"iderm/internal/index"
)

// indexReady is posted when the background index build finishes.
type indexReady struct{ ix *index.Index }

type location struct {
	path string
	pos  Pos
}

const maxHistory = 100

func (a *App) buildIndex() {
	ix := index.New(a.root)
	a.index = ix
	a.indexing = true
	scr := a.scr
	go func() {
		ix.Build()
		scr.PostEvent(tcell.NewEventInterrupt(indexReady{ix}))
	}()
}

func (a *App) indexDone(r indexReady) {
	if r.ix != a.index {
		return
	}
	a.indexing = false
	files, classes := r.ix.Stats()
	if a.msg == "" {
		a.setMsg("Indexed %d PHP files, %d classes", files, classes)
	}
	if a.search.active && !a.search.picker {
		a.updateSearch()
	}
}

func (a *App) pushHistory() {
	t := a.tab()
	a.history = append(a.history, location{t.buf.Path, t.cur})
	if len(a.history) > maxHistory {
		a.history = a.history[len(a.history)-maxHistory:]
	}
}

func (a *App) back() {
	if len(a.history) == 0 {
		a.setMsg("No previous location")
		return
	}
	l := a.history[len(a.history)-1]
	a.history = a.history[:len(a.history)-1]
	a.openFile(l.path)
	if t := a.tab(); t.buf.Path == l.path {
		t.moveTo(l.pos, false)
	}
}

// jumpTo opens file and moves the cursor, remembering the current location.
func (a *App) jumpTo(file string, line, col int) {
	a.pushHistory()
	a.openFile(file)
	t := a.tab()
	if t.buf.Path != file {
		return
	}
	t.moveTo(Pos{line, col}, false)
	if line < t.scrollY || line >= t.scrollY+a.geo.textH {
		t.scrollY = max(0, line-a.geo.textH/3)
	}
}

// gotoDefinition jumps to the declaration of the symbol under the cursor.
// In quiet mode (double-click) variables are ignored, nothing is reported
// on failure, and the result tells whether a jump happened.
func (a *App) gotoDefinition(quiet bool) bool {
	t := a.tab()
	if t.hl == nil {
		if !quiet {
			a.setErr("Go to definition works in PHP files only")
		}
		return false
	}
	res, msg := a.index.Definition(t.buf.Path, t.buf, t.cur.Line, t.cur.Col)
	if quiet && len(res) > 0 && strings.HasPrefix(res[0].Name, "$") {
		return false
	}
	switch {
	case len(res) == 0:
		if quiet {
			return false
		}
		if a.indexing {
			msg += " (index is still being built)"
		}
		a.setErr("%s", msg)
		return false
	case len(res) == 1:
		a.jumpTo(res[0].File, res[0].Line, res[0].Col)
	default:
		items := make([]result, len(res))
		for i, l := range res {
			items[i] = result{kind: resSymbol, badge: l.Kind.String(), label: l.Name,
				detail: fmt.Sprintf("%s:%d", a.relPath(l.File), l.Line+1), file: l.File, line: l.Line, col: l.Col}
		}
		a.showPicker(fmt.Sprintf("%d declarations – choose one", len(res)), items)
	}
	return true
}
