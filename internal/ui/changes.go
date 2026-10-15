package ui

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"

	"github.com/jannovy/iderm/internal/git"
	"github.com/jannovy/iderm/internal/index"
)

// changesWin is the floating window in the bottom right corner of the IDE
// listing uncommitted files with a preview of their changes.
type changesWin struct {
	hidden    bool
	hiddenKey string // file set when hidden; a different set shows the window again
	sel       int
	selPath   string
	fscroll   int
	pscroll   int

	// Geometry of the last draw, for the mouse.
	rect        [4]int
	fileY       int
	fileRows    int
	prevY       int
	discardX    [2]int // [Discard file]
	closeX      [2]int // [×]
	hunkDiscard [2]int // [Discard] on hunk headers

	cacheKey string
	diff     *fileDiff
	rows     []changeRow
}

// fileDiff compares a file with HEAD; tab is set when the file is open.
type fileDiff struct {
	f      git.FileStatus
	abs    string
	base   []string
	cur    []string
	hunks  []git.Hunk
	binary bool
	tab    *Tab
}

const (
	rowHeader = iota
	rowDel
	rowAdd
	rowInfo
)

type changeRow struct {
	kind int
	text string
	hunk int
	line int // line of the current file, -1 for none
}

const maxChangeRows = 2000

var (
	changesBg  = tcell.NewHexColor(0x21252B)
	changesDel = tcell.NewHexColor(0x4B2B2E)
	changesAdd = tcell.NewHexColor(0x2B4031)
)

func (a *App) changedFiles() []git.FileStatus {
	if a.repo == nil || a.gitStatus == nil {
		return nil
	}
	return a.gitStatus.Files
}

func changesKey(files []git.FileStatus) string {
	var b strings.Builder
	for _, f := range files {
		b.WriteString(f.Path)
		b.WriteByte(f.X)
		b.WriteByte(f.Y)
		b.WriteByte(0)
	}
	return b.String()
}

func (a *App) toggleChanges() {
	files := a.changedFiles()
	if len(files) == 0 {
		a.setMsg("No uncommitted changes")
		return
	}
	c := &a.changes
	c.hidden = !c.hidden
	c.hiddenKey = changesKey(files)
}

// changesVisible reports whether the window is shown, and its files.
func (a *App) changesVisible() ([]git.FileStatus, bool) {
	files := a.changedFiles()
	c := &a.changes
	if len(files) == 0 || a.mode() != modeIDE || a.search.active {
		return nil, false
	}
	if c.hidden && changesKey(files) != c.hiddenKey {
		c.hidden = false
	}
	if c.hidden {
		return nil, false
	}
	// Keep the selection on the same file when the list changes.
	c.sel = min(c.sel, len(files)-1)
	for i, f := range files {
		if f.Path == c.selPath {
			c.sel = i
		}
	}
	c.selPath = files[c.sel].Path
	return files, true
}

func splitLines(s string) []string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// loadDiff returns the diff of f, cached until the file or the status changes.
func (a *App) loadDiff(f git.FileStatus) *fileDiff {
	c := &a.changes
	abs := a.repo.Abs(f.Path)
	var tab *Tab
	for _, t := range a.tabs {
		if t.buf.Path == abs {
			tab = t
		}
	}
	key := fmt.Sprintf("%s\x00%c%c\x00%p", abs, f.X, f.Y, a.gitStatus)
	if tab != nil {
		key = fmt.Sprintf("%s\x00%c%c\x00tab %p %d %v", abs, f.X, f.Y, tab, tab.buf.Version(), tab.hasHead)
	}
	if c.diff != nil && c.cacheKey == key {
		return c.diff
	}
	d := &fileDiff{f: f, abs: abs, tab: tab}
	if tab != nil {
		tab.updateMarks()
		d.base = tab.head
		d.cur = make([]string, tab.buf.LineCount())
		for i := range d.cur {
			d.cur[i] = string(tab.buf.Line(i))
		}
	} else {
		if f.X != '?' && f.X != 'A' {
			rel := f.Path
			if f.Orig != "" {
				rel = f.Orig
			}
			if s, ok := a.repo.Show("HEAD", rel); ok {
				d.binary = strings.IndexByte(s, 0) >= 0
				d.base = splitLines(s)
			}
		}
		if data, err := os.ReadFile(abs); err == nil {
			d.binary = d.binary || bytes.IndexByte(data, 0) >= 0
			d.cur = splitLines(string(data))
		}
	}
	if !d.binary {
		d.hunks = git.Hunks(d.base, d.cur)
	}
	c.cacheKey, c.diff = key, d
	c.rows = buildChangeRows(d)
	return d
}

// canDiscardHunk reports whether single changes of the file can be
// discarded; new and deleted files are discarded as a whole.
func (d *fileDiff) canDiscardHunk() bool {
	return !d.binary && d.base != nil && d.cur != nil
}

func buildChangeRows(d *fileDiff) []changeRow {
	switch {
	case d.binary:
		return []changeRow{{kind: rowInfo, text: "Binary file", line: -1}}
	case len(d.hunks) == 0:
		if d.f.X == 'R' {
			return []changeRow{{kind: rowInfo, text: "Renamed from " + d.f.Orig + ", content unchanged", line: -1}}
		}
		return []changeRow{{kind: rowInfo, text: "No differences to HEAD", line: -1}}
	}
	var rows []changeRow
	for i, h := range d.hunks {
		if len(rows) >= maxChangeRows {
			rows = append(rows, changeRow{kind: rowInfo, text: "…", line: -1})
			break
		}
		title := fmt.Sprintf("line %d", h.Start+1)
		switch {
		case d.cur == nil:
			title = "file deleted"
		case d.base == nil:
			title = "new file"
		case h.Start == h.End:
			title = fmt.Sprintf("line %d: lines removed", h.Start+1)
		case h.BaseStart == h.BaseEnd:
			title = fmt.Sprintf("line %d: lines added", h.Start+1)
		}
		rows = append(rows, changeRow{kind: rowHeader, text: title, hunk: i, line: min(h.Start, max(len(d.cur)-1, 0))})
		// A trailing newline shows up as an empty last line; leave it out.
		for j := h.BaseStart; j < h.BaseEnd && len(rows) < maxChangeRows; j++ {
			if j == len(d.base)-1 && d.base[j] == "" && j > h.BaseStart {
				break
			}
			rows = append(rows, changeRow{kind: rowDel, text: d.base[j], hunk: i, line: min(h.Start, max(len(d.cur)-1, 0))})
		}
		for j := h.Start; j < h.End && len(rows) < maxChangeRows; j++ {
			if j == len(d.cur)-1 && d.cur[j] == "" && j > h.Start {
				break
			}
			rows = append(rows, changeRow{kind: rowAdd, text: d.cur[j], hunk: i, line: j})
		}
	}
	return rows
}

func (a *App) drawChanges() {
	c := &a.changes
	c.rect = [4]int{}
	files, ok := a.changesVisible()
	if !ok {
		return
	}
	g := a.geo
	w := min(g.edW-4, max(56, g.edW*45/100))
	h := min(g.textH-2, max(12, g.textH*45/100))
	if w < 30 || h < 6 {
		return
	}
	x := g.w - w
	y := g.textY + g.textH - h
	// Move to the top when the window would hide the cursor.
	t := a.tab()
	cy := g.textY + t.cur.Line - t.scrollY
	cx := g.edX + g.gutterW + visualCol(t.buf.Line(t.cur.Line), t.cur.Col) - t.scrollX
	if cy >= y && cx >= x {
		y = g.textY
	}
	c.rect = [4]int{x, y, w, h}

	box := styleDefault.Background(changesBg).Foreground(tcell.NewHexColor(0xD7DAE0))
	dim := box.Foreground(histDim)
	for yy := y; yy < y+h; yy++ {
		a.fill(x, x+w, yy, box)
	}
	for yy := y; yy < y+h; yy++ {
		a.scr.SetContent(x, yy, '│', nil, dim)
	}
	x1 := x + w

	// Title with buttons.
	a.put(x+2, y, x1, fmt.Sprintf("Uncommitted changes · %d file(s)", len(files)), box.Bold(true).Foreground(tcell.NewHexColor(0x61AFEF)))
	closeX := x1 - 4
	revX := closeX - 1 - runewidth.StringWidth("[Discard file]")
	a.put(revX, y, closeX, "[Discard file]", box.Foreground(gitColors[gcConflict]).Bold(true))
	a.put(closeX, y, x1, "[×]", box.Foreground(tcell.NewHexColor(0x61AFEF)).Bold(true))
	c.discardX, c.closeX = [2]int{revX, revX + 14}, [2]int{closeX, closeX + 3}

	// File list.
	c.fileY = y + 1
	c.fileRows = min(len(files), max(1, (h-2)/3), 6)
	c.fscroll = scrollTo(c.sel, c.fscroll, c.fileRows, len(files))
	for row := 0; row < c.fileRows && c.fscroll+row < len(files); row++ {
		i := c.fscroll + row
		f := files[i]
		st := box
		if i == c.sel {
			st = box.Background(selBg)
		}
		yy := c.fileY + row
		a.fill(x+1, x1, yy, st)
		letter, color := statusLetter(f)
		a.put(x+2, yy, x1, letter, st.Foreground(color).Bold(true))
		name := f.Path
		if f.Orig != "" {
			name += "  ← " + f.Orig
		}
		a.put(x+4, yy, x1-1, name, st.Foreground(color))
	}
	if len(files) > c.fileRows {
		a.put(x1-2, c.fileY+c.fileRows-1, x1, "↓", dim)
	}

	// Separator and preview of the selected file.
	sepY := c.fileY + c.fileRows
	for xx := x + 1; xx < x1; xx++ {
		a.scr.SetContent(xx, sepY, '─', nil, dim)
	}
	d := a.loadDiff(files[c.sel])
	where := "HEAD → disk"
	if d.tab != nil {
		where = "HEAD → editor"
	}
	a.put(x+2, sepY, x1, " "+where+" · double-click opens ", dim)
	c.prevY = sepY + 1
	rowsH := y + h - c.prevY
	c.pscroll = max(0, min(c.pscroll, len(c.rows)-rowsH))
	c.hunkDiscard = [2]int{}
	for row := 0; row < rowsH && c.pscroll+row < len(c.rows); row++ {
		r := c.rows[c.pscroll+row]
		yy := c.prevY + row
		switch r.kind {
		case rowHeader:
			a.fill(x+1, x1, yy, box)
			a.put(x+2, yy, x1, "@@ "+r.text, box.Foreground(tcell.NewHexColor(0xC678DD)))
			if d.canDiscardHunk() {
				bx := x1 - 1 - runewidth.StringWidth("[Discard]")
				a.put(bx, yy, x1, "[Discard]", box.Foreground(gitColors[gcConflict]).Bold(true))
				c.hunkDiscard = [2]int{bx, bx + 9}
			}
		case rowDel, rowAdd:
			bg, sign := changesDel, "-"
			if r.kind == rowAdd {
				bg, sign = changesAdd, "+"
			}
			st := box.Background(bg)
			a.fill(x+1, x1, yy, st)
			a.put(x+2, yy, x1, sign, st.Bold(true))
			a.put(x+4, yy, x1-1, strings.ReplaceAll(r.text, "\t", "    "), st)
		default:
			a.put(x+2, yy, x1, r.text, dim)
		}
	}
	if c.pscroll+rowsH < len(c.rows) {
		a.put(x1-2, y+h-1, x1, "↓", dim)
	}
}

func statusLetter(f git.FileStatus) (string, tcell.Color) {
	switch {
	case f.Conflicted():
		return "!", gitColors[gcConflict]
	case f.Untracked():
		return "?", gitColors[gcUntracked]
	case f.X == 'A':
		return "A", gitColors[gcAdded]
	case f.X == 'D' || f.Y == 'D':
		return "D", gitColors[gcDeleted]
	case f.X == 'R':
		return "R", gitColors[gcUntracked]
	}
	return "M", gitColors[gcModified]
}

// changesMouse handles the mouse over the window; false means outside.
func (a *App) changesMouse(x, y int, btn tcell.ButtonMask) bool {
	c := &a.changes
	r := c.rect
	if r[2] == 0 || x < r[0] || x >= r[0]+r[2] || y < r[1] || y >= r[1]+r[3] {
		return false
	}
	files := a.changedFiles()
	if btn&(tcell.WheelUp|tcell.WheelDown) != 0 {
		d := 3
		if btn&tcell.WheelUp != 0 {
			d = -3
		}
		if y >= c.fileY && y < c.fileY+c.fileRows {
			c.fscroll = max(0, min(c.fscroll+d/3, len(files)-c.fileRows))
		} else {
			c.pscroll = max(0, c.pscroll+d)
		}
		return true
	}
	if btn&tcell.Button1 == 0 {
		return false
	}
	if a.mouseDown {
		return true
	}
	a.mouseDown = true
	a.closeCompletion()
	now := time.Now()
	switch {
	case y == r[1]:
		switch {
		case x >= c.discardX[0] && x < c.discardX[1]:
			a.discardChangedFile()
		case x >= c.closeX[0] && x < c.closeX[1]:
			c.hidden, c.hiddenKey = true, changesKey(files)
			a.setMsg("Changes window hidden – click Changes in the status bar or Alt+G to show it")
		}
	case y >= c.fileY && y < c.fileY+c.fileRows:
		i := c.fscroll + y - c.fileY
		if i >= len(files) {
			break
		}
		p := Pos{-2, i}
		double := now.Sub(a.lastClick) < 400*time.Millisecond && a.lastPos == p
		a.lastClick, a.lastPos = now, p
		if i != c.sel {
			c.sel, c.selPath, c.pscroll = i, files[i].Path, 0
		}
		if double {
			a.openChange(-1)
			a.lastClick = time.Time{}
		}
	case y >= c.prevY:
		i := c.pscroll + y - c.prevY
		if i >= len(c.rows) {
			break
		}
		row := c.rows[i]
		if row.kind == rowHeader && x >= c.hunkDiscard[0] && x < c.hunkDiscard[1] {
			a.discardChange(row.hunk)
			break
		}
		if row.kind != rowInfo {
			a.openChange(row.line)
		}
	}
	return true
}

// openChange opens the selected file, at line when it is not -1.
func (a *App) openChange(line int) {
	d := a.changes.diff
	if d == nil {
		return
	}
	if d.cur == nil {
		a.setErr("%s is deleted", d.f.Path)
		return
	}
	a.openFile(d.abs)
	if t := a.tab(); t.buf.Path == d.abs && line >= 0 {
		t.sel = false
		t.place(t.buf.Clamp(Pos{line, 0}))
		t.follow = true
	}
}

// discardChange throws away hunk i of the selected file: an open file is
// changed and saved in the editor (Ctrl+Z brings the change back), any
// other file is rewritten on disk.
func (a *App) discardChange(i int) {
	d := a.changes.diff
	if d == nil || !d.canDiscardHunk() || i >= len(d.hunks) {
		return
	}
	if t := d.tab; t != nil {
		if !t.hasHead {
			a.setErr("The HEAD version of %s is not available", d.f.Path)
			return
		}
		t.marksVer = -1
		t.updateMarks()
		if i >= len(t.hunks) {
			a.setErr("The change is no longer there")
			return
		}
		dirty := t.buf.Dirty()
		revertHunk(t, t.hunks[i])
		a.save(t, nil)
		if dirty {
			a.setMsg("Change discarded – %s saved including your unsaved edits (Ctrl+Z undoes)", d.f.Path)
		} else {
			a.setMsg("Change discarded in %s (Ctrl+Z in the editor undoes)", d.f.Path)
		}
		return
	}
	data, err := os.ReadFile(d.abs)
	if err != nil {
		a.setErr("Cannot read %s: %v", d.f.Path, err)
		return
	}
	cur := splitLines(string(data))
	hunks := git.Hunks(d.base, cur)
	if i >= len(hunks) || hunks[i] != d.hunks[i] {
		a.setErr("%s changed meanwhile, try again", d.f.Path)
		a.changes.cacheKey = ""
		return
	}
	h := hunks[i]
	lines := append(append(append([]string{}, cur[:h.Start]...), d.base[h.BaseStart:h.BaseEnd]...), cur[h.End:]...)
	eol := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		eol = "\r\n"
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(d.abs); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.WriteFile(d.abs, []byte(strings.Join(lines, eol)), mode); err != nil {
		a.setErr("Cannot write %s: %v", d.f.Path, err)
		return
	}
	a.changes.cacheKey = ""
	if index.IsPHP(d.abs) {
		a.index.UpdateFile(d.abs)
	}
	a.refreshGit()
	a.setMsg("Change discarded in %s", d.f.Path)
}

// revertHunk replaces the hunk's lines with their HEAD version as one undo step.
func revertHunk(t *Tab, h git.Hunk) {
	base := t.head[h.BaseStart:h.BaseEnd]
	n := t.buf.LineCount()
	var from, to Pos
	var text string
	switch {
	case h.End < n:
		from, to = Pos{h.Start, 0}, Pos{h.End, 0}
		if len(base) > 0 {
			text = strings.Join(base, "\n") + "\n"
		}
	case h.Start > 0:
		from, to = Pos{h.Start - 1, len(t.buf.Line(h.Start - 1))}, t.buf.End()
		if len(base) > 0 {
			text = "\n" + strings.Join(base, "\n")
		}
	default:
		from, to, text = Pos{0, 0}, t.buf.End(), strings.Join(base, "\n")
	}
	t.buf.BeginGroup()
	t.buf.Delete(from, to)
	if text != "" {
		t.buf.Insert(from, text)
	}
	t.buf.EndGroup()
	t.sel = false
	t.place(t.buf.Clamp(Pos{h.Start, 0}))
}

// discardChangedFile resets the selected file to HEAD after a confirmation.
func (a *App) discardChangedFile() {
	d := a.changes.diff
	if d == nil {
		return
	}
	f := d.f
	if f.Conflicted() {
		a.setErr("Resolve the conflict in %s first", f.Path)
		return
	}
	if d.tab != nil && d.tab.buf.Dirty() {
		a.setErr("%s has unsaved changes – save or undo them first", f.Path)
		return
	}
	question := "Discard all changes in " + f.Path + "?"
	if f.Untracked() || f.X == 'A' {
		question = "Delete new file " + f.Path + "?"
	}
	a.ask(question, func() {
		if err := a.repo.DiscardFile(f); err != nil {
			a.setErr("git: %v", err)
			return
		}
		a.setMsg("Discarded changes in %s", f.Path)
		a.afterWorkTreeChange()
	})
}
