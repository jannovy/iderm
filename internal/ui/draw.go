package ui

import (
	"fmt"
	"strconv"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"

	"iderm/internal/buffer"
	"iderm/internal/git"
	"iderm/internal/syntax"
)

var (
	styleDefault = tcell.StyleDefault
	styleDim     = styleDefault.Foreground(tcell.NewHexColor(0x5C6370))
	styleGutter  = styleDim
	styleCurNum  = styleDefault.Foreground(tcell.NewHexColor(0xD7DAE0)).Bold(true)
	styleErrMark = styleDefault.Foreground(tcell.NewHexColor(0xE06C75)).Bold(true)
	styleStatus  = styleDefault.Background(tcell.NewHexColor(0x3E4451)).Foreground(tcell.NewHexColor(0xD7DAE0))
	styleTabBar  = styleDefault.Background(tcell.NewHexColor(0x21252B)).Foreground(tcell.NewHexColor(0x9DA5B4))
	styleTabAct  = styleDefault.Background(tcell.NewHexColor(0x3E4451)).Foreground(tcell.NewHexColor(0xFFFFFF)).Bold(true)
	errColor     = tcell.NewHexColor(0xE06C75)
	okColor      = tcell.NewHexColor(0x98C379)
	styleErr     = styleDefault.Foreground(errColor).Bold(true)
	styleDir     = styleDefault.Bold(true)
	styleActive  = styleDefault.Foreground(tcell.NewHexColor(0xE5C07B)).Bold(true)
	styleTreeSel = styleDefault.Background(tcell.NewHexColor(0x2C5A8C)).Foreground(tcell.NewHexColor(0xFFFFFF))
	treeDimBg    = tcell.NewHexColor(0x3E4451)
	selBg        = tcell.NewHexColor(0x264F78)
	matchBg      = tcell.NewHexColor(0x7A5A00)

	syntaxStyles = map[syntax.Kind]tcell.Style{
		syntax.Plain:    styleDefault,
		syntax.Keyword:  styleDefault.Foreground(tcell.NewHexColor(0xC678DD)),
		syntax.Type:     styleDefault.Foreground(tcell.NewHexColor(0xE5C07B)),
		syntax.Variable: styleDefault.Foreground(tcell.NewHexColor(0xE06C75)),
		syntax.String:   styleDefault.Foreground(tcell.NewHexColor(0x98C379)),
		syntax.Number:   styleDefault.Foreground(tcell.NewHexColor(0xD19A66)),
		syntax.Comment:  styleDefault.Foreground(tcell.NewHexColor(0x7F848E)).Italic(true),
		syntax.Tag:      styleDefault.Foreground(tcell.NewHexColor(0x56B6C2)).Bold(true),
		syntax.HTML:     styleDefault.Foreground(tcell.NewHexColor(0x9DA5B4)),
		syntax.Constant: styleDefault.Foreground(tcell.NewHexColor(0xD19A66)),
		syntax.Function: styleDefault.Foreground(tcell.NewHexColor(0x61AFEF)),
		syntax.Operator: styleDefault.Foreground(tcell.NewHexColor(0x56B6C2)),
	}
)

// put writes s at (x, y) clipped to maxX and returns the next x.
func (a *App) put(x, y, maxX int, s string, st tcell.Style) int {
	for _, r := range s {
		w := max(runewidth.RuneWidth(r), 1)
		if x+w > maxX {
			break
		}
		a.scr.SetContent(x, y, r, nil, st)
		x += w
	}
	return x
}

func (a *App) fill(x0, x1, y int, st tcell.Style) {
	for x := x0; x < x1; x++ {
		a.scr.SetContent(x, y, ' ', nil, st)
	}
}

func (a *App) draw() {
	a.scr.Clear()
	w, h := a.scr.Size()
	g := geometry{w: w, h: h, textY: 2, textH: max(h-4, 1)}
	if a.showTree && a.mode() == modeIDE {
		g.treeW = min(32, max(w/4, 16)) + 1
	}
	g.edX = g.treeW
	g.edW = max(w-g.edX, 1)
	a.geo = g

	if a.showTree && a.mode() == modeIDE {
		a.drawTree()
	}
	a.drawTabs()
	a.drawEditor()
	a.drawStatus()
	a.drawMessage()
	if a.cmd.active {
		a.drawCommander()
		if a.prompt == nil {
			a.scr.HideCursor()
		}
	}
	if a.gitPanel.active {
		a.drawGitPanel()
	}
	if a.hist.active {
		a.drawHistory()
	}
	if a.view.active {
		a.drawTextView()
	}
	if (a.gitPanel.active || a.hist.active || a.view.active) && a.prompt == nil {
		a.scr.HideCursor()
	}
	switch {
	case a.search.active || a.mode() == modeIDE:
		a.drawSearchBar()
	case a.mode() == modeGit:
		a.drawGitTabs()
	}
	if a.search.active {
		a.drawSearchResults()
	}
	if a.showHelp {
		a.scr.HideCursor()
		a.drawHelp()
	}
	a.drawMainBar()
	a.scr.Show()
}

func (a *App) drawTree() {
	g, tr := &a.geo, a.tree
	right := g.treeW - 1
	for y := 0; y < g.h-2; y++ {
		a.scr.SetContent(right, y, '│', nil, styleDim)
	}
	a.put(1, 0, right, tr.root.name, styleDefault.Bold(true))
	rows := g.h - 3
	if tr.follow {
		if tr.sel < tr.scroll {
			tr.scroll = tr.sel
		} else if tr.sel >= tr.scroll+rows {
			tr.scroll = tr.sel - rows + 1
		}
		tr.follow = false
	}
	tr.scroll = max(0, min(tr.scroll, len(tr.rows)-1))
	activePath := a.tab().buf.Path
	for i := 0; i < rows && tr.scroll+i < len(tr.rows); i++ {
		n := tr.rows[tr.scroll+i]
		y := 1 + i
		var st tcell.Style
		switch {
		case n.dir:
			st = styleDir
		case n.path == activePath:
			st = styleActive
		default:
			st = styleDefault
		}
		if c := a.gitClassOf(n.path, n.dir); c != gcNone {
			st = st.Foreground(gitColors[c])
		}
		if tr.scroll+i == tr.sel {
			if a.focus == focusTree {
				st = styleTreeSel
			} else {
				st = st.Background(treeDimBg)
			}
			a.fill(0, right, y, st)
		}
		icon := "  "
		if n.dir {
			icon = "▸ "
			if n.open {
				icon = "▾ "
			}
		}
		x := a.put(1+2*n.depth, y, right, icon, st)
		a.put(x, y, right, n.name, st)
	}
}

func (a *App) drawTabs() {
	g := &a.geo
	a.fill(g.edX, g.w, 1, styleTabBar)
	x := g.edX
	g.tabRanges = g.tabRanges[:0]
	for i, t := range a.tabs {
		label := " " + t.title()
		if t.buf.Dirty() {
			label += " ●"
		}
		label += " "
		st := styleTabBar
		if i == a.active {
			st = styleTabAct
		}
		if c := a.gitClassOf(t.buf.Path, false); c != gcNone {
			st = st.Foreground(gitColors[c])
		}
		start := x
		x = a.put(x, 1, g.w, label, st)
		g.tabRanges = append(g.tabRanges, [2]int{start, x})
		x = a.put(x, 1, g.w, "│", styleTabBar)
	}
}

func (a *App) ensureVisible(t *Tab, w, h int) {
	n := t.buf.LineCount()
	if t.follow {
		t.follow = false
		l := t.cur.Line
		switch {
		case l < t.scrollY-h || l >= t.scrollY+2*h:
			t.scrollY = l - h/2
		case l < t.scrollY:
			t.scrollY = l
		case l >= t.scrollY+h:
			t.scrollY = l - h + 1
		}
		v := visualCol(t.buf.Line(l), t.cur.Col)
		if v < t.scrollX {
			t.scrollX = max(0, v-w/4)
		} else if v >= t.scrollX+w {
			t.scrollX = v - w + w/4
		}
	}
	t.scrollY = max(0, min(t.scrollY, n-1))
}

func (a *App) drawEditor() {
	g := &a.geo
	t := a.tab()
	g.gutterW = len(strconv.Itoa(t.buf.LineCount())) + 2
	textX := g.edX + g.gutterW
	textW := max(g.edW-g.gutterW, 1)
	dirty := t.buf.TakeDirtyFrom()
	if t.hl != nil {
		t.hl.Invalidate(dirty)
	}
	a.ensureVisible(t, textW, g.textH)
	t.updateMarks()

	errLines := map[int]bool{}
	for _, e := range t.lint {
		errLines[e.line] = true
	}
	selS, selE, hasSel := t.selection()
	var query []rune
	if a.showMatches && a.lastQuery != "" {
		query = []rune(a.lastQuery)
	}
	fold := smartFold(a.lastQuery)

	for row := 0; row < g.textH; row++ {
		li := t.scrollY + row
		y := g.textY + row
		if li >= t.buf.LineCount() {
			a.put(g.edX+g.gutterW-2, y, textX, "~", styleDim)
			continue
		}
		num := strconv.Itoa(li + 1)
		numSt := styleGutter
		if li == t.cur.Line {
			numSt = styleCurNum
		}
		if errLines[li] {
			a.scr.SetContent(g.edX, y, '●', nil, styleErrMark)
			numSt = styleErrMark
		}
		a.put(textX-1-len(num), y, textX, num, numSt)
		if li < len(t.marks) && t.marks[li] != 0 {
			m := t.marks[li]
			ch, color := '▎', gitColors[gcModified]
			switch {
			case m&git.Added != 0:
				color = gitColors[gcAdded]
			case m&git.Modified != 0:
			case m&git.DeletedAbove != 0:
				ch, color = '▔', gitColors[gcConflict]
			default:
				ch, color = '▁', gitColors[gcConflict]
			}
			a.scr.SetContent(textX-1, y, ch, nil, styleDefault.Foreground(color))
		}

		line := t.buf.Line(li)
		var kinds []syntax.Kind
		if t.hl != nil {
			kinds = t.hl.Line(t.buf, li)
		}
		var match []bool
		if query != nil {
			for i := buffer.Index(line, query, 0, fold); i >= 0; i = buffer.Index(line, query, i+len(query), fold) {
				if match == nil {
					match = make([]bool, len(line))
				}
				for k := i; k < i+len(query); k++ {
					match[k] = true
				}
			}
		}
		v := 0
		for i, r := range line {
			cw := cellWidth(r, v)
			if v >= t.scrollX+textW {
				break
			}
			if v >= t.scrollX {
				st := styleDefault
				if kinds != nil {
					st = syntaxStyles[kinds[i]]
				}
				if errLines[li] {
					st = st.Underline(true)
				}
				if match != nil && match[i] {
					st = st.Background(matchBg)
				}
				if hasSel && !(Pos{li, i}).Less(selS) && (Pos{li, i}).Less(selE) {
					st = st.Background(selBg)
				}
				x := textX + v - t.scrollX
				switch {
				case r == '\t':
					for k := 0; k < cw && x+k < textX+textW; k++ {
						a.scr.SetContent(x+k, y, ' ', nil, st)
					}
				case r < 0x20 || r == 0x7f:
					a.scr.SetContent(x, y, '?', nil, st.Reverse(true))
				case x+cw <= textX+textW:
					a.scr.SetContent(x, y, r, nil, st)
				}
			}
			v += cw
		}
		// Show the selected line break.
		if hasSel && li >= selS.Line && li < selE.Line && (li > selS.Line || selS.Col <= len(line)) {
			if x := textX + v - t.scrollX; x >= textX && x < textX+textW {
				a.scr.SetContent(x, y, ' ', nil, styleDefault.Background(selBg))
			}
		}
	}

	if a.focus == focusEditor && a.prompt == nil && !a.search.active {
		cy := t.cur.Line - t.scrollY
		cx := visualCol(t.buf.Line(t.cur.Line), t.cur.Col) - t.scrollX
		if cy >= 0 && cy < g.textH && cx >= 0 && cx < textW {
			a.scr.ShowCursor(textX+cx, g.textY+cy)
			return
		}
	}
	a.scr.HideCursor()
}

func (a *App) drawStatus() {
	g := a.geo
	t := a.tab()
	y := g.h - 2
	a.fill(0, g.w, y, styleStatus)
	name := a.relPath(t.buf.Path)
	if t.buf.Dirty() {
		name += " [+]"
	}
	lang := "Text"
	if t.hl != nil {
		lang = "PHP"
	}
	eol := "LF"
	if t.buf.CRLF {
		eol = "CRLF"
	}
	a.put(1, y, g.w, name, styleStatus.Bold(true))

	lint, lintSt := "", styleStatus
	switch {
	case a.indexing:
		lint = "⟳ indexing…"
	case t.hl == nil || a.php == "":
	case t.lintVer != t.buf.Version():
		lint = "… checking"
	case len(t.lint) == 0:
		lint, lintSt = "✓ syntax OK", styleStatus.Foreground(okColor)
	default:
		lint, lintSt = fmt.Sprintf("✗ %d error(s) – F8", len(t.lint)), styleStatus.Foreground(errColor).Bold(true)
	}
	right := fmt.Sprintf("Ln %d, Col %d   %s   %s ", t.cur.Line+1, visualCol(t.buf.Line(t.cur.Line), t.cur.Col)+1, eol, lang)
	rx := g.w - runewidth.StringWidth(right)
	a.put(rx, y, g.w, right, styleStatus)
	if lint != "" {
		rx -= runewidth.StringWidth(lint) + 3
		a.put(rx, y, g.w, lint, lintSt)
	}
	a.geo.branchX = [2]int{}
	if b := a.branchLabel(); b != "" {
		bx := rx - runewidth.StringWidth(b) - 3
		a.put(bx, y, rx, b, styleStatus.Foreground(gitColors[gcAdded]))
		a.geo.branchX = [2]int{bx, bx + runewidth.StringWidth(b)}
	}
}

func (a *App) drawMessage() {
	g := a.geo
	y := g.h - 1
	if p := a.prompt; p != nil {
		x := a.put(0, y, g.w, p.label, styleDefault.Bold(true))
		start := x
		a.put(x, y, g.w, string(p.text), styleDefault)
		a.scr.ShowCursor(start+runewidth.StringWidth(string(p.text[:p.cur])), y)
		return
	}
	if a.msg != "" {
		st := styleDefault
		if a.msgErr {
			st = styleErr
		}
		a.put(0, y, g.w, a.msg, st)
		return
	}
	if a.cmd.active && !a.gitPanel.active && !a.view.active && !a.hist.active {
		if a.cmd.busy {
			a.put(0, y, g.w, a.cmd.progress+"   (Esc cancel)", styleActive)
		} else {
			a.drawFKeys(y)
		}
		return
	}
	if a.gitPanel.active && !a.view.active && !a.hist.active {
		a.put(0, y, g.w, gitHints, styleDim)
		return
	}
	t := a.tab()
	for _, e := range t.lint {
		if e.line == t.cur.Line {
			a.put(0, y, g.w, "✗ "+e.msg, styleErr)
			return
		}
	}
	a.put(0, y, g.w, "F1 help  ^S save  ^O open  ^F find  ^R replace  ^G line  ^E files  ^W close  ^Q quit", styleDim)
}

var helpLines = []string{
	"iderm – PHP editor",
	"",
	"Modes: click IDE | Files | Git in the top bar, or F2 (Files), F9 (Git), Esc (back to IDE)",
	"F2  file manager (two panels, Midnight Commander keys):",
	"  Tab other panel  Enter open  Insert/Space mark  + - * select  = same dir  Ctrl+U swap",
	"  F3 view  F4 edit  Shift+F4 new file  F5 copy  F6 move  Shift+F6 rename  F7 mkdir  F8 delete",
	"",
	"Files                             Editing",
	"  Ctrl+O   open (Tab completes)     Ctrl+Z / Ctrl+Y   undo / redo",
	"  Ctrl+N   new file                 Ctrl+C / X / V    copy / cut / paste",
	"  Ctrl+S   save                     Ctrl+A            select all",
	"  Ctrl+W   close tab                Ctrl+D            duplicate line",
	"  Ctrl+Q   quit                     Ctrl+K            delete line",
	"  Ctrl+PgUp/PgDn, Alt+1..9  tabs    Ctrl+/            toggle // comment",
	"                                    Tab / Shift+Tab   indent / dedent",
	"File tree                           Alt+↑ / Alt+↓     move line",
	"  Ctrl+E   focus tree / editor      Ctrl/Alt+←→       word left / right",
	"  Ctrl+B   show / hide tree         Shift+arrows      select",
	"  Enter    open / expand            Home              smart home",
	"  F5       reload",
	"                                  PHP",
	"Search                              Ctrl+L            check syntax (php -l)",
	"  Ctrl+P   search everywhere:       F8                jump to next error",
	"           files, classes, text     F12, Ctrl+]       go to definition",
	"  Ctrl+F   find in file (↑↓)        double-click      go to definition",
	"  F3 / Shift+F3  next / previous    Ctrl+T            back",
	"  Ctrl+R   replace",
	"  Ctrl+G   go to line[:col]       Syntax is checked while you type.",
	"",
	"Git  (gutter: green ▎ added, blue ▎ changed, red ▔ deleted)",
	"  F9, Ctrl+\\  git panel            Space stage/unstage   c commit   d discard",
	"  click ⎇ in status bar             Enter diff  p push  P pull  b branches",
	"  Shift+F9 or l in panel  history: branches, commit graph, commit files and diffs",
	"",
	"Press any key to close.",
}

func (a *App) drawHelp() {
	g := a.geo
	bw := 0
	for _, l := range helpLines {
		bw = max(bw, runewidth.StringWidth(l))
	}
	bw += 4
	bh := len(helpLines) + 2
	x0 := max(0, (g.w-bw)/2)
	y0 := max(0, (g.h-bh)/2)
	box := styleDefault.Background(tcell.NewHexColor(0x21252B)).Foreground(tcell.NewHexColor(0xD7DAE0))
	for y := y0; y < y0+bh && y < g.h; y++ {
		a.fill(x0, min(x0+bw, g.w), y, box)
	}
	for i, l := range helpLines {
		st := box
		if i == 0 {
			st = box.Bold(true).Foreground(tcell.NewHexColor(0x61AFEF))
		}
		a.put(x0+2, y0+1+i, min(x0+bw, g.w), l, st)
	}
}
