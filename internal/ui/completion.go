package ui

import (
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"

	"github.com/jannovy/iderm/internal/index"
)

// completionPopup is the autocomplete list shown below the cursor.
type completionPopup struct {
	active bool
	manual bool // opened with Ctrl+Space: keep offering even short prefixes
	tab    *Tab
	c      *index.Completion
	sel    int
	scroll int
	rect   [4]int // x, y, w, h of the last drawn popup (screen below the main bar)
}

const completionRows = 10

var kindLetters = map[index.SymKind]string{
	index.Class: "C", index.Interface: "I", index.Trait: "T", index.Enum: "E", index.Function: "f",
	index.Method: "m", index.Const: "c", index.Property: "p", index.Variable: "v", index.Keyword: "k",
}

func (a *App) closeCompletion() { a.comp = completionPopup{} }

// updateCompletion recomputes suggestions at the cursor.
func (a *App) updateCompletion(manual bool) {
	t := a.tab()
	manual = manual || a.comp.active && a.comp.manual && a.comp.tab == t
	if !t.isPHP() {
		a.closeCompletion()
		return
	}
	if _, _, sel := t.selection(); sel {
		a.closeCompletion()
		return
	}
	c := a.index.Complete(t.buf.Path, t.buf, t.cur.Line, t.cur.Col, manual)
	if c == nil {
		if manual && !a.comp.active {
			a.setMsg("No suggestions")
		}
		a.closeCompletion()
		return
	}
	prev := ""
	if a.comp.active && a.comp.sel < len(a.comp.c.Items) {
		prev = a.comp.c.Items[a.comp.sel].Label
	}
	a.comp = completionPopup{active: true, manual: manual, tab: t, c: c}
	for i, it := range c.Items {
		if it.Label == prev && strings.HasPrefix(strings.ToLower(it.Label), strings.ToLower(c.Prefix)) {
			a.comp.sel = i
		}
	}
}

// completionKey handles keys while the popup is open; false passes the key on.
func (a *App) completionKey(ev *tcell.EventKey) bool {
	p := &a.comp
	n := len(p.c.Items)
	switch ev.Key() {
	case tcell.KeyUp:
		p.sel = (p.sel - 1 + n) % n
	case tcell.KeyDown:
		p.sel = (p.sel + 1) % n
	case tcell.KeyPgUp:
		p.sel = max(0, p.sel-completionRows)
	case tcell.KeyPgDn:
		p.sel = min(n-1, p.sel+completionRows)
	case tcell.KeyEnter:
		a.acceptCompletion(false)
	case tcell.KeyTab:
		a.acceptCompletion(true)
	case tcell.KeyEscape:
		a.closeCompletion()
	default:
		return false
	}
	return true
}

// acceptCompletion inserts the selected item; replaceTail also replaces the
// rest of the identifier after the cursor.
func (a *App) acceptCompletion(replaceTail bool) {
	p := a.comp
	a.closeCompletion()
	t := a.tab()
	if p.tab != t || p.sel >= len(p.c.Items) {
		return
	}
	it := p.c.Items[p.sel]
	c := p.c
	line := t.buf.Line(t.cur.Line)
	end := t.cur
	if replaceTail {
		for end.Col < len(line) && (unicode.IsLetter(line[end.Col]) || unicode.IsDigit(line[end.Col]) || line[end.Col] == '_') {
			end.Col++
		}
	}
	text := it.Insert
	cursorBack := 0
	if it.Call && (end.Col >= len(line) || line[end.Col] != '(') {
		text += "()"
		if it.Params {
			cursorBack = 1
		}
	}
	t.buf.BeginGroup()
	start := Pos{c.Line, c.Col}
	t.buf.Delete(start, end)
	pos := t.buf.Insert(start, text)
	pos.Col -= cursorBack
	if it.Import != "" {
		use := "use " + it.Import + ";\n"
		if c.ImportBlank {
			use = "\n" + use
		}
		at := min(c.ImportLine, t.buf.LineCount())
		if at == t.buf.LineCount() {
			t.buf.Insert(t.buf.End(), "\n"+strings.TrimSuffix(use, "\n"))
		} else {
			t.buf.Insert(Pos{at, 0}, use)
		}
		if at <= pos.Line {
			pos.Line += strings.Count(use, "\n")
		}
		a.setMsg("Added use %s", it.Import)
	}
	t.buf.EndGroup()
	t.sel = false
	t.place(pos)
	if it.Call && it.Params {
		a.setMsg("%s%s", it.Label, it.Detail)
	}
	a.scheduleLint(t)
}

// afterEditorKey decides whether the popup opens, refreshes or closes.
func (a *App) afterEditorKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyCtrlSpace:
		a.updateCompletion(true)
	case tcell.KeyRune:
		r := ev.Rune()
		if ev.Modifiers()&tcell.ModAlt != 0 {
			a.closeCompletion()
			return
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_$>:\\", r) {
			a.updateCompletion(false)
		} else {
			a.closeCompletion()
		}
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if a.comp.active {
			a.updateCompletion(false)
		}
	default:
		a.closeCompletion()
	}
}

func (a *App) drawCompletion() {
	p := &a.comp
	t := a.tab()
	if !p.active || p.tab != t || a.mode() != modeIDE || a.focus != focusEditor || a.prompt != nil || a.search.active {
		p.rect = [4]int{}
		return
	}
	g := a.geo
	items := p.c.Items
	rows := min(completionRows, len(items))
	if p.sel < p.scroll {
		p.scroll = p.sel
	} else if p.sel >= p.scroll+rows {
		p.scroll = p.sel - rows + 1
	}
	// Width from the visible items.
	labelW, detailW, ownerW := 0, 0, 0
	for i := p.scroll; i < p.scroll+rows; i++ {
		labelW = max(labelW, runewidth.StringWidth(items[i].Label))
		detailW = max(detailW, runewidth.StringWidth(items[i].Detail))
		ownerW = max(ownerW, runewidth.StringWidth(items[i].Owner))
	}
	w := 3 + labelW + 2 + min(detailW, 60) + 2 + ownerW + 1
	w = min(w, g.w-g.edX)
	textX := g.edX + g.gutterW
	cursorRow := g.textY + p.c.Line - t.scrollY
	x := textX + visualCol(t.buf.Line(p.c.Line), p.c.Col) - t.scrollX - 3
	x = max(g.edX, min(x, g.w-w))
	y := cursorRow + 1
	if y+rows > g.textY+g.textH && cursorRow-rows >= g.textY {
		y = cursorRow - rows
	}
	p.rect = [4]int{x, y, w, rows}

	box := styleDefault.Background(tcell.NewHexColor(0x2C313A)).Foreground(tcell.NewHexColor(0xD7DAE0))
	prefix := []rune(p.c.Prefix)
	for row := 0; row < rows; row++ {
		i := p.scroll + row
		it := items[i]
		st := box
		if i == p.sel {
			st = box.Background(selBg)
		}
		yy := y + row
		a.fill(x, x+w, yy, st)
		kind := it.Kind.String()
		a.put(x+1, yy, x+w, kindLetters[it.Kind], st.Foreground(badgeColors[kind]).Bold(true))
		lx := x + 3
		for k, ch := range []rune(it.Label) {
			cs := st
			if k < len(prefix) && strings.EqualFold(string(ch), string(prefix[k])) {
				cs = st.Foreground(tcell.NewHexColor(0xE5C07B)).Bold(true)
			}
			lx = a.put(lx, yy, x+w, string(ch), cs)
		}
		ownerX := x + w - 1 - runewidth.StringWidth(it.Owner)
		a.put(x+3+labelW+2, yy, ownerX-1, truncate(it.Detail, max(1, ownerX-1-(x+3+labelW+2))), st.Foreground(tcell.NewHexColor(0x7F848E)))
		a.put(ownerX, yy, x+w, it.Owner, st.Foreground(tcell.NewHexColor(0x7F848E)))
	}
	if len(items) > rows {
		a.put(x+w-1, y+rows-1, x+w, "↓", box.Foreground(tcell.NewHexColor(0x7F848E)))
		if p.scroll > 0 {
			a.put(x+w-1, y, x+w, "↑", box.Foreground(tcell.NewHexColor(0x7F848E)))
		}
	}
}

// completionMouse accepts a clicked item; clicks elsewhere close the popup.
func (a *App) completionMouse(x, y int, btn tcell.ButtonMask) bool {
	p := &a.comp
	r := p.rect
	inside := x >= r[0] && x < r[0]+r[2] && y >= r[1] && y < r[1]+r[3]
	if btn&(tcell.WheelUp|tcell.WheelDown) != 0 && inside {
		if btn&tcell.WheelUp != 0 {
			p.sel = max(0, p.sel-1)
		} else {
			p.sel = min(len(p.c.Items)-1, p.sel+1)
		}
		return true
	}
	if btn&tcell.Button1 == 0 {
		return false
	}
	if inside && !a.mouseDown {
		a.mouseDown = true
		p.sel = p.scroll + y - r[1]
		a.acceptCompletion(false)
		return true
	}
	a.closeCompletion()
	return false
}
