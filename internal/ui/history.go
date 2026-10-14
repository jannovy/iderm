package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"

	"iderm/internal/git"
)

const (
	paneRefs = iota
	paneCommits
	paneFiles
)

const historyLimit = 1500

type histRef struct {
	label   string
	ref     string // "" = all branches
	header  bool
	kind    git.RefKind
	current bool
}

// historyView shows branches, the commit graph and details of a commit.
type historyView struct {
	active bool
	focus  int

	refs           []histRef
	rsel, rscroll  int
	ref            string
	lines          []git.LogLine
	csel, cscroll  int
	details        map[string]*git.CommitDetail
	fsel, fscroll  int
	remoteBranches map[string]bool
}

type histLayout struct {
	x0, refsW, listX, x1   int
	top, listH, detY, detH int
}

func (a *App) histLayout() histLayout {
	g := a.geo
	bottom := a.panelBottom()
	l := histLayout{x0: g.edX, x1: g.w, top: 2}
	l.refsW = min(26, max(14, g.edW/5))
	l.listX = l.x0 + l.refsW + 1
	avail := bottom - l.top
	l.listH = max(3, avail*55/100)
	l.detY = l.top + l.listH + 1
	l.detH = max(0, bottom-l.detY)
	return l
}

func (a *App) openHistory() {
	if a.repo == nil {
		a.setErr("Not a git repository")
		return
	}
	a.cmd.active = false
	h := &a.hist
	h.active, h.focus = true, paneCommits
	h.details = map[string]*git.CommitDetail{}
	a.loadHistory()
}

// loadHistory reloads refs and the log, keeping the selection where possible.
func (a *App) loadHistory() {
	h := &a.hist
	refs, err := a.repo.Refs()
	if err != nil {
		a.setErr("git: %v", err)
	}
	h.refs = []histRef{{label: "All branches", ref: ""}, {label: "HEAD", ref: "HEAD"}}
	h.remoteBranches = map[string]bool{}
	sections := []struct {
		title string
		kind  git.RefKind
	}{{"Local", git.LocalBranch}, {"Remote", git.RemoteBranch}, {"Tags", git.Tag}}
	for _, s := range sections {
		first := true
		for _, r := range refs {
			if r.Kind != s.kind {
				continue
			}
			if r.Kind == git.RemoteBranch {
				h.remoteBranches[r.Name] = true
			}
			if first {
				h.refs = append(h.refs, histRef{label: s.title, header: true})
				first = false
			}
			h.refs = append(h.refs, histRef{label: r.Name, ref: r.Name, kind: r.Kind, current: r.Current})
		}
	}
	h.rsel = 0
	for i, r := range h.refs {
		if !r.header && r.ref == h.ref {
			h.rsel = i
		}
	}
	a.loadLog()
}

func (a *App) loadLog() {
	h := &a.hist
	selected := ""
	if c := a.histCommit(); c != nil {
		selected = c.Hash
	}
	lines, err := a.repo.LogGraph(h.ref, historyLimit)
	if err != nil {
		a.setErr("git: %v", err)
	}
	h.lines = lines
	h.csel, h.fsel, h.fscroll = 0, 0, 0
	for i, l := range lines {
		if l.Commit != nil && l.Commit.Hash == selected {
			h.csel = i
		}
	}
	a.moveCommit(0, 1)
}

func (a *App) histCommit() *git.Commit {
	h := &a.hist
	if h.csel >= 0 && h.csel < len(h.lines) {
		return h.lines[h.csel].Commit
	}
	return nil
}

func (a *App) histDetail() *git.CommitDetail {
	c := a.histCommit()
	if c == nil {
		return nil
	}
	h := &a.hist
	if d, ok := h.details[c.Hash]; ok {
		return d
	}
	d, err := a.repo.Detail(c.Hash)
	if err != nil {
		a.setErr("git: %v", err)
	}
	h.details[c.Hash] = d
	return d
}

// moveCommit moves the commit selection, skipping graph connector rows.
func (a *App) moveCommit(delta, dir int) {
	h := &a.hist
	if len(h.lines) == 0 {
		return
	}
	i := max(0, min(h.csel+delta, len(h.lines)-1))
	for i >= 0 && i < len(h.lines) && h.lines[i].Commit == nil {
		i += dir
	}
	if i < 0 || i >= len(h.lines) {
		return
	}
	if i != h.csel {
		h.fsel, h.fscroll = 0, 0
	}
	h.csel = i
}

func (a *App) moveRef(delta, dir int) {
	h := &a.hist
	i := max(0, min(h.rsel+delta, len(h.refs)-1))
	for i >= 0 && i < len(h.refs) && h.refs[i].header {
		i += dir
	}
	if i >= 0 && i < len(h.refs) {
		h.rsel = i
	}
}

func (a *App) moveFile(delta int) {
	h := &a.hist
	if d := a.histDetail(); d != nil && len(d.Files) > 0 {
		h.fsel = max(0, min(h.fsel+delta, len(d.Files)-1))
	}
}

func (a *App) historyKey(ev *tcell.EventKey) bool {
	h := &a.hist
	l := a.histLayout()
	page := map[int]int{paneRefs: l.listH + l.detH, paneCommits: l.listH - 1, paneFiles: max(1, l.detH-2)}[h.focus]
	move := func(delta, dir int) {
		switch h.focus {
		case paneRefs:
			a.moveRef(delta, dir)
		case paneCommits:
			a.moveCommit(delta, dir)
		default:
			a.moveFile(delta)
		}
	}
	switch ev.Key() {
	case tcell.KeyEscape:
		h.active = false
	case tcell.KeyTab:
		h.focus = (h.focus + 1) % 3
	case tcell.KeyBacktab:
		h.focus = (h.focus + 2) % 3
	case tcell.KeyLeft:
		h.focus = max(paneRefs, h.focus-1)
	case tcell.KeyRight:
		h.focus = min(paneFiles, h.focus+1)
	case tcell.KeyUp:
		move(-1, -1)
	case tcell.KeyDown:
		move(1, 1)
	case tcell.KeyPgUp:
		move(-page, -1)
	case tcell.KeyPgDn:
		move(page, 1)
	case tcell.KeyHome:
		move(-1<<20, 1)
	case tcell.KeyEnd:
		move(1<<20, -1)
	case tcell.KeyEnter:
		switch h.focus {
		case paneRefs:
			if r := h.refs[h.rsel]; !r.header {
				h.ref = r.ref
				a.loadLog()
				h.focus = paneCommits
			}
		case paneCommits:
			h.focus = paneFiles
		default:
			a.showCommitFileDiff()
		}
	case tcell.KeyRune:
		return a.historyRune(ev.Rune())
	default:
		return false
	}
	return true
}

func (a *App) historyRune(ch rune) bool {
	h := &a.hist
	switch {
	case ch == 'q':
		h.active = false
	case ch == 'r':
		a.loadHistory()
		a.setMsg("History reloaded")
	case ch == 'c' && h.focus == paneRefs:
		r := h.refs[h.rsel]
		switch {
		case r.header || r.ref == "" || r.ref == "HEAD":
		case r.kind == git.Tag:
			a.setErr("Tags cannot be checked out as a branch – select a commit and press n")
		case r.kind == git.RemoteBranch:
			a.historySwitch(r.ref[strings.IndexByte(r.ref, '/')+1:], false, "")
		default:
			a.historySwitch(r.ref, false, "")
		}
	case ch == 'n' && h.focus == paneCommits:
		if c := a.histCommit(); c != nil {
			p := newPrompt("New branch at "+c.Short+": ", "")
			p.onEnter = func(name string) {
				if name = strings.TrimSpace(name); name != "" {
					a.historySwitch(name, true, c.Hash)
				}
			}
			a.prompt = p
		}
	case ch == 'y' && h.focus == paneCommits:
		if c := a.histCommit(); c != nil {
			a.clip, a.clipLine = c.Hash, false
			writeClipboard(c.Hash)
			a.setMsg("Copied %s", c.Hash)
		}
	}
	return true
}

func (a *App) historySwitch(name string, create bool, start string) {
	if a.anyDirty() {
		a.setErr("Save or close modified tabs before switching branches")
		return
	}
	var err error
	if start != "" {
		err = a.repo.SwitchNewAt(name, start)
	} else {
		err = a.repo.Switch(name, create)
	}
	if err != nil {
		a.setErr("git: %v", err)
		return
	}
	a.setMsg("Switched to %s", name)
	a.afterWorkTreeChange()
	a.loadHistory()
}

func (a *App) showCommitFileDiff() {
	d := a.histDetail()
	h := &a.hist
	if d == nil || h.fsel >= len(d.Files) {
		return
	}
	f := d.Files[h.fsel]
	out, err := a.repo.CommitDiff(d, f)
	if err != nil {
		a.setErr("git: %v", err)
		return
	}
	short := d.Hash
	if len(short) > 8 {
		short = short[:8]
	}
	a.view = diffView(short+"  "+f.Path, out, a.repo.Abs(f.Path))
}

// historyMouse handles clicks in the history area; false means outside.
func (a *App) historyMouse(x, y int, btn tcell.ButtonMask, first bool) bool {
	g := a.geo
	if x < g.edX || y < 1 || y >= a.panelBottom() {
		return false
	}
	h := &a.hist
	l := a.histLayout()
	pane := paneCommits
	switch {
	case x < l.listX:
		pane = paneRefs
	case y >= l.detY:
		pane = paneFiles
	}
	if btn&(tcell.WheelUp|tcell.WheelDown) != 0 {
		d := 3
		if btn&tcell.WheelUp != 0 {
			d = -3
		}
		switch pane {
		case paneRefs:
			a.moveRef(d, max(-1, min(1, d)))
		case paneCommits:
			a.moveCommit(d, max(-1, min(1, d)))
		default:
			a.moveFile(d)
		}
		return true
	}
	if !first || btn&tcell.Button1 == 0 || y < l.top {
		return true
	}
	h.focus = pane
	switch pane {
	case paneRefs:
		if i := h.rscroll + y - l.top; i < len(h.refs) && !h.refs[i].header {
			if i == h.rsel {
				h.ref = h.refs[i].ref
				a.loadLog()
			}
			h.rsel = i
		}
	case paneCommits:
		if i := h.cscroll + y - l.top; i < len(h.lines) && h.lines[i].Commit != nil {
			if i != h.csel {
				h.fsel, h.fscroll = 0, 0
			}
			h.csel = i
		}
	default:
		if d := a.histDetail(); d != nil {
			if i := h.fscroll + y - l.detY - a.detailHeaderRows(d); i >= 0 && i < len(d.Files) {
				if i == h.fsel {
					a.showCommitFileDiff()
				}
				h.fsel = i
			}
		}
	}
	return true
}

var (
	graphColors = []tcell.Color{
		tcell.NewHexColor(0x61AFEF), tcell.NewHexColor(0x98C379), tcell.NewHexColor(0xE5C07B),
		tcell.NewHexColor(0xC678DD), tcell.NewHexColor(0x56B6C2), tcell.NewHexColor(0xE06C75),
	}
	graphGlyphs = map[rune]rune{'*': '●', '|': '│', '/': '╱', '\\': '╲'}
	histBg      = tcell.NewHexColor(0x1E2127)
	histDim     = tcell.NewHexColor(0x7F848E)
)

func (a *App) drawHistory() {
	g := a.geo
	h := &a.hist
	l := a.histLayout()
	bottom := a.panelBottom()
	box := styleDefault.Background(histBg).Foreground(tcell.NewHexColor(0xD7DAE0))
	for y := 1; y < bottom; y++ {
		a.fill(g.edX, g.w, y, box)
	}
	filter := "All branches"
	if h.ref != "" {
		filter = h.ref
	}
	a.put(g.edX, 1, g.w, " HISTORY   "+filter+"   (Tab pane, Enter open, c checkout, n new branch, y copy hash, Esc close)", box.Bold(true).Foreground(tcell.NewHexColor(0x61AFEF)))
	for y := l.top; y < bottom; y++ {
		a.scr.SetContent(l.listX-1, y, '│', nil, box.Foreground(histDim))
	}
	for x := l.listX; x < l.x1; x++ {
		a.scr.SetContent(x, l.detY-1, '─', nil, box.Foreground(histDim))
	}
	a.drawHistRefs(l, box)
	a.drawHistCommits(l, box)
	a.drawHistDetail(l, box)
}

func paneStyle(box tcell.Style, selected, focused bool) tcell.Style {
	switch {
	case selected && focused:
		return box.Background(selBg)
	case selected:
		return box.Background(tcell.NewHexColor(0x3E4451))
	}
	return box
}

func scrollTo(sel, scroll, h, n int) int {
	if sel < scroll {
		scroll = sel
	} else if sel >= scroll+h {
		scroll = sel - h + 1
	}
	return max(0, min(scroll, n-h))
}

func (a *App) drawHistRefs(l histLayout, box tcell.Style) {
	h := &a.hist
	rows := a.panelBottom() - l.top
	h.rscroll = scrollTo(h.rsel, h.rscroll, rows, len(h.refs))
	right := l.listX - 1
	for row := 0; row < rows && h.rscroll+row < len(h.refs); row++ {
		i := h.rscroll + row
		r := h.refs[i]
		y := l.top + row
		if r.header {
			a.put(l.x0+1, y, right, strings.ToUpper(r.label), box.Foreground(histDim).Bold(true))
			continue
		}
		st := paneStyle(box, i == h.rsel, h.focus == paneRefs)
		a.fill(l.x0, right, y, st)
		label := "  " + r.label
		if r.current {
			label = "▶ " + r.label
			st = st.Foreground(gitColors[gcAdded]).Bold(true)
		}
		if r.ref == h.ref && i != h.rsel {
			st = st.Bold(true)
		}
		a.put(l.x0+1, y, right, label, st)
	}
}

func (a *App) refStyle(ref string, base tcell.Style) (string, tcell.Style) {
	h := &a.hist
	switch {
	case strings.HasPrefix(ref, "HEAD -> "):
		return "▶ " + strings.TrimPrefix(ref, "HEAD -> "), base.Foreground(tcell.NewHexColor(0x56B6C2)).Bold(true)
	case ref == "HEAD":
		return "HEAD", base.Foreground(tcell.NewHexColor(0x56B6C2)).Bold(true)
	case strings.HasPrefix(ref, "tag: "):
		return "◆ " + strings.TrimPrefix(ref, "tag: "), base.Foreground(tcell.NewHexColor(0xE5C07B))
	case h.remoteBranches[ref]:
		return ref, base.Foreground(tcell.NewHexColor(0xC678DD))
	}
	return ref, base.Foreground(gitColors[gcAdded])
}

func (a *App) drawHistCommits(l histLayout, box tcell.Style) {
	h := &a.hist
	h.cscroll = scrollTo(h.csel, h.cscroll, l.listH, len(h.lines))
	if len(h.lines) == 0 {
		a.put(l.listX+1, l.top, l.x1, "No commits", box.Foreground(histDim))
		return
	}
	for row := 0; row < l.listH && h.cscroll+row < len(h.lines); row++ {
		i := h.cscroll + row
		line := h.lines[i]
		y := l.top + row
		st := paneStyle(box, i == h.csel, h.focus == paneCommits)
		a.fill(l.listX, l.x1, y, st)
		x := l.listX + 1
		for col, ch := range line.Graph {
			if ch == ' ' {
				x++
				continue
			}
			if gl, ok := graphGlyphs[ch]; ok {
				ch = gl
			}
			x = a.put(x, y, l.x1, string(ch), st.Foreground(graphColors[(col/2)%len(graphColors)]))
		}
		c := line.Commit
		if c == nil {
			continue
		}
		x = a.put(x+1, y, l.x1, c.Short, st.Foreground(tcell.NewHexColor(0xD19A66)))
		x++
		for _, ref := range c.Refs {
			label, rs := a.refStyle(ref, st)
			x = a.put(x+1, y, l.x1, "["+label+"]", rs)
		}
		meta := "  " + c.Author + "  " + c.Date + " "
		metaX := max(x+2, l.x1-len([]rune(meta)))
		a.put(x+1, y, metaX, c.Subject, st)
		a.put(metaX, y, l.x1, meta, st.Foreground(histDim))
	}
}

// detailHeaderRows is the number of rows above the file list.
func (a *App) detailHeaderRows(d *git.CommitDetail) int {
	return len(a.detailHeader(d))
}

func (a *App) detailHeader(d *git.CommitDetail) []string {
	parents := make([]string, len(d.Parents))
	for i, p := range d.Parents {
		parents[i] = p[:min(8, len(p))]
	}
	rows := []string{fmt.Sprintf("%s   %s <%s>   %s", d.Hash[:min(10, len(d.Hash))], d.Author, d.Email, d.Date)}
	if len(parents) > 1 {
		rows = append(rows, "Merge of "+strings.Join(parents, " + "))
	}
	msg := strings.Split(d.Message, "\n")
	if len(msg) > 4 {
		msg = append(msg[:4], "…")
	}
	rows = append(rows, msg...)
	rows = append(rows, fmt.Sprintf("── %d changed file(s) ──", len(d.Files)))
	return rows
}

func (a *App) drawHistDetail(l histLayout, box tcell.Style) {
	h := &a.hist
	if l.detH <= 0 {
		return
	}
	d := a.histDetail()
	if d == nil {
		return
	}
	head := a.detailHeader(d)
	y := l.detY
	for i, row := range head {
		if y >= l.detY+l.detH {
			return
		}
		st := box
		switch {
		case i == 0:
			st = box.Foreground(tcell.NewHexColor(0xD19A66))
		case i == len(head)-1:
			st = box.Foreground(histDim)
		case i == 1 || len(d.Parents) > 1 && i == 2:
			st = box.Bold(true)
		}
		a.put(l.listX+1, y, l.x1, row, st)
		y++
	}
	rows := l.detY + l.detH - y
	if rows <= 0 {
		return
	}
	h.fscroll = scrollTo(h.fsel, h.fscroll, rows, len(d.Files))
	for row := 0; row < rows && h.fscroll+row < len(d.Files); row++ {
		i := h.fscroll + row
		f := d.Files[i]
		st := paneStyle(box, i == h.fsel, h.focus == paneFiles)
		a.fill(l.listX, l.x1, y+row, st)
		color := gitColors[gcModified]
		switch f.Status {
		case 'A':
			color = gitColors[gcAdded]
		case 'D':
			color = gitColors[gcDeleted]
		case 'R', 'C':
			color = gitColors[gcUntracked]
		}
		x := a.put(l.listX+3, y+row, l.x1, string(f.Status), st.Foreground(color).Bold(true))
		name := f.Path
		if f.Orig != "" {
			name += "  ← " + f.Orig
		}
		a.put(x+3, y+row, l.x1, name, st.Foreground(color))
	}
}
