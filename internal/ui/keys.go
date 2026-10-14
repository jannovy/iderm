package ui

import (
	"time"

	"github.com/gdamore/tcell/v2"
)

func (a *App) handleKey(ev *tcell.EventKey) {
	if a.pasting {
		switch ev.Key() {
		case tcell.KeyRune:
			a.pasteBuf.WriteRune(ev.Rune())
		case tcell.KeyEnter:
			a.pasteBuf.WriteRune('\r')
		case tcell.KeyCtrlJ:
			a.pasteBuf.WriteRune('\n')
		case tcell.KeyTab:
			a.pasteBuf.WriteRune('\t')
		}
		return
	}
	a.lastArmed, a.armed = a.armed, ""
	if a.showHelp {
		a.showHelp = false
		return
	}
	a.msg = ""
	if a.search.active {
		a.searchKey(ev)
		return
	}
	if a.prompt != nil {
		a.promptKey(ev)
		return
	}
	if a.view.active {
		if !a.viewKey(ev) {
			a.globalKey(ev)
		}
		return
	}
	if a.hist.active {
		if !a.historyKey(ev) {
			a.globalKey(ev)
		}
		return
	}
	if a.gitPanel.active {
		if !a.gitPanelKey(ev) {
			a.globalKey(ev)
		}
		return
	}
	if a.globalKey(ev) {
		return
	}
	if a.focus == focusTree {
		a.treeKey(ev)
		return
	}
	a.editorKey(ev)
}

func (a *App) globalKey(ev *tcell.EventKey) bool {
	ctrl := ev.Modifiers()&tcell.ModCtrl != 0
	switch ev.Key() {
	case tcell.KeyCtrlQ:
		a.quitCmd()
	case tcell.KeyF1:
		a.showHelp = true
	case tcell.KeyCtrlB:
		a.toggleTree()
	case tcell.KeyCtrlE:
		a.focusTreeCmd()
	case tcell.KeyCtrlO:
		a.openPrompt()
	case tcell.KeyCtrlN:
		a.newFile()
	case tcell.KeyCtrlS:
		a.save(a.tab(), nil)
	case tcell.KeyCtrlW:
		a.closeTab()
	case tcell.KeyCtrlP:
		a.openSearch()
	case tcell.KeyF21:
		a.openHistory()
	case tcell.KeyF9, tcell.KeyCtrlBackslash:
		if ev.Key() == tcell.KeyF9 && ev.Modifiers()&tcell.ModShift != 0 {
			a.openHistory()
			return true
		}
		if a.gitPanel.active || a.hist.active {
			a.closeGitPanel()
		} else {
			a.openGitPanel()
		}
	case tcell.KeyF5:
		a.tree.reload()
		a.buildIndex()
		a.openRepo()
		a.setMsg("Reloading file tree, index and git status…")
	case tcell.KeyPgUp:
		if !ctrl {
			return false
		}
		a.switchTab(a.active - 1)
	case tcell.KeyPgDn:
		if !ctrl {
			return false
		}
		a.switchTab(a.active + 1)
	case tcell.KeyRune:
		r := ev.Rune()
		if ev.Modifiers()&tcell.ModAlt == 0 || r < '1' || r > '9' {
			return false
		}
		if i := int(r - '1'); i < len(a.tabs) {
			a.switchTab(i)
		}
	default:
		return false
	}
	return true
}

func (a *App) editorKey(ev *tcell.EventKey) {
	t := a.tab()
	before := t.buf.Version()
	defer func() {
		if t.buf.Version() != before {
			a.scheduleLint(t)
		}
	}()
	mod := ev.Modifiers()
	shift := mod&tcell.ModShift != 0
	ctrl := mod&tcell.ModCtrl != 0
	alt := mod&tcell.ModAlt != 0
	page := max(1, a.geo.textH-1)

	switch ev.Key() {
	case tcell.KeyLeft:
		if s, _, ok := t.selection(); ok && !shift && !ctrl && !alt {
			t.moveTo(s, false)
		} else if ctrl || alt {
			t.moveTo(t.wordLeft(t.cur), shift)
		} else {
			t.moveTo(t.left(t.cur), shift)
		}
	case tcell.KeyRight:
		if _, e, ok := t.selection(); ok && !shift && !ctrl && !alt {
			t.moveTo(e, false)
		} else if ctrl || alt {
			t.moveTo(t.wordRight(t.cur), shift)
		} else {
			t.moveTo(t.right(t.cur), shift)
		}
	case tcell.KeyUp:
		if alt {
			t.moveLines(-1)
		} else {
			t.moveVert(-1, shift)
		}
	case tcell.KeyDown:
		if alt {
			t.moveLines(1)
		} else {
			t.moveVert(1, shift)
		}
	case tcell.KeyHome:
		if ctrl {
			t.moveTo(Pos{}, shift)
		} else {
			t.moveTo(t.smartHome(), shift)
		}
	case tcell.KeyEnd:
		if ctrl {
			t.moveTo(t.buf.End(), shift)
		} else {
			t.moveTo(Pos{t.cur.Line, len(t.buf.Line(t.cur.Line))}, shift)
		}
	case tcell.KeyPgUp:
		t.scrollY = max(0, t.scrollY-page)
		t.moveVert(-page, shift)
	case tcell.KeyPgDn:
		t.scrollY = max(0, min(t.scrollY+page, t.buf.LineCount()-1))
		t.moveVert(page, shift)
	case tcell.KeyEnter:
		t.newline()
	case tcell.KeyTab:
		t.tabKey()
	case tcell.KeyBacktab:
		t.indentLines(-1)
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if alt || ctrl {
			t.deleteWordBack()
		} else {
			t.backspace()
		}
	case tcell.KeyDelete:
		if alt || ctrl {
			t.deleteWordForward()
		} else {
			t.deleteForward()
		}
	case tcell.KeyCtrlZ:
		if p, ok := t.buf.Undo(); ok {
			t.sel = false
			t.place(p)
		} else {
			a.setMsg("Nothing to undo")
		}
	case tcell.KeyCtrlY:
		if p, ok := t.buf.Redo(); ok {
			t.sel = false
			t.place(p)
		} else {
			a.setMsg("Nothing to redo")
		}
	case tcell.KeyCtrlA:
		t.selectAll()
	case tcell.KeyCtrlC:
		a.copy(false)
	case tcell.KeyCtrlX:
		a.copy(true)
	case tcell.KeyCtrlV:
		a.paste()
	case tcell.KeyCtrlF:
		a.find()
	case tcell.KeyCtrlR:
		a.replace()
	case tcell.KeyCtrlG:
		a.gotoLine()
	case tcell.KeyF3:
		a.findNext(shift)
	case tcell.KeyCtrlL:
		if t.hl == nil || a.php == "" {
			a.setErr("Syntax check is available for PHP files only (and needs php in PATH)")
		} else {
			a.startLint(t)
			a.setMsg("Checking syntax…")
		}
	case tcell.KeyF8:
		a.nextLintError()
	case tcell.KeyF12, tcell.KeyCtrlRightSq:
		a.gotoDefinition(false)
	case tcell.KeyCtrlT:
		a.back()
	case tcell.KeyCtrlD:
		t.duplicateLine()
	case tcell.KeyCtrlK:
		t.deleteLines()
	case tcell.KeyCtrlUnderscore:
		t.toggleComment()
	case tcell.KeyEscape:
		t.sel = false
		a.showMatches = false
	case tcell.KeyRune:
		if alt {
			switch ev.Rune() {
			case 'b':
				t.moveTo(t.wordLeft(t.cur), shift)
			case 'f':
				t.moveTo(t.wordRight(t.cur), shift)
			}
			return
		}
		t.typeRune(ev.Rune())
	}
}

func (a *App) treeKey(ev *tcell.EventKey) {
	tr := a.tree
	tr.follow = true
	page := max(1, a.geo.h-4)
	n := tr.selected()
	switch ev.Key() {
	case tcell.KeyUp:
		tr.sel = max(0, tr.sel-1)
	case tcell.KeyDown:
		tr.sel = min(len(tr.rows)-1, tr.sel+1)
	case tcell.KeyPgUp:
		tr.sel = max(0, tr.sel-page)
	case tcell.KeyPgDn:
		tr.sel = min(len(tr.rows)-1, tr.sel+page)
	case tcell.KeyHome:
		tr.sel = 0
	case tcell.KeyEnd:
		tr.sel = len(tr.rows) - 1
	case tcell.KeyEnter, tcell.KeyRight:
		if n == nil {
			return
		}
		switch {
		case !n.dir:
			a.openFile(n.path)
		case ev.Key() == tcell.KeyRight && n.open:
			tr.sel = min(len(tr.rows)-1, tr.sel+1)
		default:
			tr.toggle(n)
		}
	case tcell.KeyLeft:
		if n == nil {
			return
		}
		if n.dir && n.open {
			tr.toggle(n)
		} else if n.parent != tr.root {
			tr.selectNode(n.parent)
		}
	case tcell.KeyEscape, tcell.KeyTab:
		a.focus = focusEditor
	case tcell.KeyRune:
		if ev.Rune() == ' ' && n != nil {
			if n.dir {
				tr.toggle(n)
			} else {
				a.openFile(n.path)
				a.focus = focusTree
			}
			return
		}
		tr.jumpTo(ev.Rune())
	}
}

func (a *App) handleMouse(ev *tcell.EventMouse) {
	x, y := ev.Position()
	btn := ev.Buttons()
	g := a.geo
	if a.showHelp {
		if btn != tcell.ButtonNone {
			a.showHelp = false
		}
		return
	}
	if a.search.active {
		if btn == tcell.ButtonNone {
			a.mouseDown = false
			return
		}
		if a.searchMouse(x, y, btn) {
			return
		}
	}
	if a.view.active || a.hist.active || a.gitPanel.active {
		if btn == tcell.ButtonNone {
			a.mouseDown = false
			return
		}
		first := !a.mouseDown
		if btn&tcell.Button1 != 0 {
			a.mouseDown = true
		}
		handled := false
		switch {
		case a.view.active:
			handled = a.viewMouse(x, y, btn, first)
		case a.hist.active:
			handled = a.historyMouse(x, y, btn, first)
		default:
			handled = a.gitPanelMouse(x, y, btn, first)
		}
		if handled {
			return
		}
		a.closeGitPanel()
		a.mouseDown = false
	}
	if y == g.h-2 && btn&tcell.Button1 != 0 && !a.mouseDown && x >= g.branchX[0] && x < g.branchX[1] {
		a.mouseDown = true
		a.openGitPanel()
		return
	}
	inTree := a.showTree && x < g.treeW
	if btn&(tcell.WheelUp|tcell.WheelDown) != 0 {
		d := 3
		if btn&tcell.WheelUp != 0 {
			d = -3
		}
		if inTree {
			a.tree.scroll = max(0, min(a.tree.scroll+d, len(a.tree.rows)-1))
			a.tree.follow = false
		} else {
			t := a.tab()
			t.scrollY = max(0, min(t.scrollY+d, t.buf.LineCount()-1))
			t.follow = false
		}
		return
	}
	if btn&tcell.Button1 == 0 {
		a.mouseDown = false
		return
	}
	first := !a.mouseDown
	a.mouseDown = true

	switch {
	case inTree:
		if !first || a.prompt != nil {
			return
		}
		row := y - 1 + a.tree.scroll
		if y < 1 || row >= len(a.tree.rows) {
			return
		}
		a.tree.sel = row
		a.focus = focusTree
		if n := a.tree.rows[row]; n.dir {
			a.tree.toggle(n)
		} else {
			a.openFile(n.path)
		}
	case y == 0:
		if first {
			a.openSearch()
		}
	case y == 1:
		if !first {
			return
		}
		for i, r := range g.tabRanges {
			if x < r[0] || x >= r[1] {
				continue
			}
			a.switchTab(i)
			// Double-click closes the tab; tabs are remembered as Pos{-1, index}.
			now, p := time.Now(), Pos{-1, i}
			if now.Sub(a.lastClick) < 400*time.Millisecond && a.lastPos == p {
				a.lastArmed, a.armed = a.armed, ""
				a.closeTab()
				a.lastClick = time.Time{}
			} else {
				a.lastClick, a.lastPos = now, p
			}
		}
	case y >= g.textY && y < g.textY+g.textH:
		if a.prompt != nil {
			return
		}
		t := a.tab()
		a.focus = focusEditor
		line := max(0, min(t.scrollY+y-g.textY, t.buf.LineCount()-1))
		v := max(0, x-g.edX-g.gutterW+t.scrollX)
		p := Pos{line, colForVisual(t.buf.Line(line), v)}
		if !first {
			t.moveTo(p, true)
			return
		}
		now := time.Now()
		double := now.Sub(a.lastClick) < 400*time.Millisecond && p == a.lastPos
		a.lastClick, a.lastPos = now, p
		switch {
		case ev.Modifiers()&(tcell.ModCtrl|tcell.ModAlt|tcell.ModMeta) != 0:
			t.moveTo(p, false)
			a.gotoDefinition(false)
			a.mouseDown = false
		case double:
			// Double-click on a class or method name jumps to its source;
			// anywhere else it selects the word.
			t.moveTo(p, false)
			if a.gotoDefinition(true) {
				a.mouseDown = false
				a.lastClick = time.Time{}
			} else {
				t.selectWord(p)
			}
		case ev.Modifiers()&tcell.ModShift != 0:
			t.moveTo(p, true)
		default:
			t.moveTo(p, false)
		}
	}
}
