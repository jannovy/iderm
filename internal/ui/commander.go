package ui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"

	"github.com/jannovy/iderm/internal/fileops"
)

type fmEntry struct {
	name string
	dir  bool
	link bool
	up   bool // ".."
	size int64
	mod  time.Time
	mode fs.FileMode
}

type fmPanel struct {
	dir     string
	entries []fmEntry
	sel     int
	scroll  int
	marked  map[string]bool
	err     string
}

// commander is the two-panel file manager (Midnight Commander style).
type commander struct {
	active   bool
	inited   bool
	panels   [2]fmPanel
	side     int
	busy     bool
	cancel   context.CancelFunc
	progress string
	jump     string
	jumpAt   time.Time
}

// Events of background file operations.
type (
	fmProgress struct{ text string }
	fmDone     struct {
		msg   string
		err   error
		moved [][2]string
	}
)

func (a *App) fmPanel() *fmPanel { return &a.cmd.panels[a.cmd.side] }

func (a *App) fmOther() *fmPanel { return &a.cmd.panels[1-a.cmd.side] }

func (a *App) toggleCommander() {
	if a.cmd.active {
		a.cmd.active = false
		return
	}
	a.closeGitPanel()
	c := &a.cmd
	if !c.inited {
		left := a.root
		if p := a.tab().buf.Path; p != "" {
			left = filepath.Dir(p)
		}
		a.fmLoad(&c.panels[0], left, "")
		a.fmLoad(&c.panels[1], a.root, "")
		c.inited = true
	} else {
		a.fmReload()
	}
	c.active = true
}

// fmLoad reads dir into the panel and selects the entry named keep.
func (a *App) fmLoad(p *fmPanel, dir, keep string) {
	if p.dir != dir {
		p.marked = map[string]bool{}
		p.scroll = 0
	}
	if p.marked == nil {
		p.marked = map[string]bool{}
	}
	p.dir, p.err = dir, ""
	des, err := os.ReadDir(dir)
	if err != nil {
		p.err = err.Error()
	}
	var list []fmEntry
	if parent := filepath.Dir(dir); parent != dir {
		list = append(list, fmEntry{name: "..", dir: true, up: true})
	}
	names := map[string]bool{}
	for _, de := range des {
		info, err := de.Info()
		if err != nil {
			continue
		}
		e := fmEntry{name: de.Name(), dir: de.IsDir(), size: info.Size(), mod: info.ModTime(), mode: info.Mode()}
		if info.Mode()&os.ModeSymlink != 0 {
			e.link = true
			if st, err := os.Stat(filepath.Join(dir, e.name)); err == nil {
				e.dir = st.IsDir()
				if !e.dir {
					e.size = st.Size()
				}
			}
		}
		list = append(list, e)
		names[e.name] = true
	}
	sort.SliceStable(list, func(i, j int) bool {
		x, y := list[i], list[j]
		if x.up != y.up {
			return x.up
		}
		if x.dir != y.dir {
			return x.dir
		}
		return strings.ToLower(x.name) < strings.ToLower(y.name)
	})
	p.entries = list
	for name := range p.marked {
		if !names[name] {
			delete(p.marked, name)
		}
	}
	p.sel = min(p.sel, max(len(list)-1, 0))
	for i, e := range list {
		if e.name == keep {
			p.sel = i
		}
	}
}

func (a *App) fmReload() {
	for i := range a.cmd.panels {
		p := &a.cmd.panels[i]
		keep := ""
		if e, ok := p.current(); ok {
			keep = e.name
		}
		a.fmLoad(p, p.dir, keep)
	}
}

func (p *fmPanel) current() (fmEntry, bool) {
	if p.sel >= 0 && p.sel < len(p.entries) {
		return p.entries[p.sel], true
	}
	return fmEntry{}, false
}

func (p *fmPanel) path(e fmEntry) string { return filepath.Join(p.dir, e.name) }

// targets returns the marked entries, or the current one when nothing is marked.
func (p *fmPanel) targets() []string {
	var out []string
	for _, e := range p.entries {
		if p.marked[e.name] {
			out = append(out, p.path(e))
		}
	}
	if len(out) == 0 {
		if e, ok := p.current(); ok && !e.up {
			out = append(out, p.path(e))
		}
	}
	return out
}

func describe(paths []string) string {
	if len(paths) == 1 {
		return filepath.Base(paths[0])
	}
	return fmt.Sprintf("%d items", len(paths))
}

func (a *App) fmEnter(p *fmPanel) {
	e, ok := p.current()
	if !ok {
		return
	}
	switch {
	case e.up:
		a.fmParent(p)
	case e.dir:
		a.fmLoad(p, p.path(e), "")
		p.sel = 0
	default:
		a.fmEdit(p.path(e))
	}
}

func (a *App) fmParent(p *fmPanel) {
	parent := filepath.Dir(p.dir)
	if parent == p.dir {
		return
	}
	a.fmLoad(p, parent, filepath.Base(p.dir))
}

func (a *App) fmEdit(path string) {
	a.cmd.active = false
	a.pushHistory()
	a.openFile(path)
}

func (a *App) commanderKey(ev *tcell.EventKey) bool {
	c := &a.cmd
	if c.busy {
		if ev.Key() == tcell.KeyEscape && c.cancel != nil {
			c.cancel()
			a.setMsg("Cancelling…")
		}
		return ev.Key() != tcell.KeyCtrlQ
	}
	p := a.fmPanel()
	page := max(1, a.fmRows()-1)
	shift := ev.Modifiers()&tcell.ModShift != 0
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyF2, tcell.KeyF10:
		c.active = false
	case tcell.KeyTab, tcell.KeyBacktab:
		c.side = 1 - c.side
	case tcell.KeyUp:
		p.sel = max(0, p.sel-1)
	case tcell.KeyDown:
		p.sel = min(len(p.entries)-1, p.sel+1)
	case tcell.KeyPgUp:
		p.sel = max(0, p.sel-page)
	case tcell.KeyPgDn:
		p.sel = max(0, min(len(p.entries)-1, p.sel+page))
	case tcell.KeyHome:
		p.sel = 0
	case tcell.KeyEnd:
		p.sel = max(0, len(p.entries)-1)
	case tcell.KeyEnter:
		a.fmEnter(p)
	case tcell.KeyRight:
		if e, ok := p.current(); ok && e.dir {
			a.fmEnter(p)
		}
	case tcell.KeyLeft, tcell.KeyBackspace, tcell.KeyBackspace2:
		a.fmParent(p)
	case tcell.KeyInsert:
		a.fmToggleMark(p)
	case tcell.KeyF3:
		a.fmView(p)
	case tcell.KeyF4, tcell.KeyF16:
		if ev.Key() == tcell.KeyF16 || shift {
			a.fmNewFile(p)
		} else if e, ok := p.current(); ok && !e.dir {
			a.fmEdit(p.path(e))
		}
	case tcell.KeyF5:
		a.fmTransfer(false)
	case tcell.KeyF6, tcell.KeyF18:
		if ev.Key() == tcell.KeyF18 || shift {
			a.fmRename(p)
		} else {
			a.fmTransfer(true)
		}
	case tcell.KeyF7:
		a.fmMkdir(p)
	case tcell.KeyF8, tcell.KeyDelete:
		a.fmDelete(p)
	case tcell.KeyCtrlR:
		a.fmReload()
		a.setMsg("Panels reloaded")
	case tcell.KeyCtrlU:
		c.panels[0], c.panels[1] = c.panels[1], c.panels[0]
	case tcell.KeyRune:
		return a.commanderRune(p, ev.Rune())
	default:
		return false
	}
	return true
}

func (a *App) commanderRune(p *fmPanel, r rune) bool {
	switch r {
	case ' ':
		a.fmToggleMark(p)
	case '*':
		for _, e := range p.entries {
			if !e.up && !e.dir {
				p.marked[e.name] = !p.marked[e.name]
			}
		}
	case '+', '-':
		a.fmSelectPattern(p, r == '+')
	case '=':
		o := a.fmOther()
		keep := ""
		if e, ok := p.current(); ok {
			keep = e.name
		}
		a.fmLoad(o, p.dir, keep)
	default:
		if !unicode.IsPrint(r) {
			return false
		}
		a.fmQuickJump(p, r)
	}
	return true
}

func (a *App) fmToggleMark(p *fmPanel) {
	if e, ok := p.current(); ok && !e.up {
		if p.marked[e.name] {
			delete(p.marked, e.name)
		} else {
			p.marked[e.name] = true
		}
	}
	p.sel = min(len(p.entries)-1, p.sel+1)
}

// fmQuickJump selects the first entry starting with the typed prefix.
func (a *App) fmQuickJump(p *fmPanel, r rune) {
	c := &a.cmd
	if time.Since(c.jumpAt) > 1200*time.Millisecond {
		c.jump = ""
	}
	c.jumpAt = time.Now()
	for _, prefix := range []string{c.jump + string(r), string(r)} {
		lp := strings.ToLower(prefix)
		for i, e := range p.entries {
			if !e.up && strings.HasPrefix(strings.ToLower(e.name), lp) {
				p.sel = i
				c.jump = prefix
				return
			}
		}
	}
}

func (a *App) fmSelectPattern(p *fmPanel, sel bool) {
	label := "Select files matching: "
	if !sel {
		label = "Unselect files matching: "
	}
	pr := newPrompt(label, "*.php")
	pr.onEnter = func(pat string) {
		n := 0
		for _, e := range p.entries {
			if ok, _ := filepath.Match(pat, e.name); ok && !e.up && !e.dir {
				if sel {
					p.marked[e.name] = true
				} else {
					delete(p.marked, e.name)
				}
				n++
			}
		}
		a.setMsg("%d file(s) matched", n)
	}
	a.prompt = pr
}

func (a *App) fmView(p *fmPanel) {
	e, ok := p.current()
	if !ok || e.dir {
		return
	}
	path := p.path(e)
	data, err := os.ReadFile(path)
	if err != nil {
		a.setErr("%v", err)
		return
	}
	if len(data) > 0 && strings.IndexByte(string(data[:min(len(data), 8000)]), 0) >= 0 {
		a.setErr("%s is a binary file", e.name)
		return
	}
	v := textView{active: true, title: a.relPath(path), file: path}
	for i, l := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if i >= 20000 {
			v.lines = append(v.lines, "… (truncated)")
			v.styles = append(v.styles, diffMeta)
			v.targets = append(v.targets, -1)
			break
		}
		v.lines = append(v.lines, strings.ReplaceAll(strings.TrimSuffix(l, "\r"), "\t", "    "))
		v.styles = append(v.styles, styleDefault)
		v.targets = append(v.targets, i)
	}
	a.view = v
}

func (a *App) fmMkdir(p *fmPanel) {
	pr := newPrompt("Create directory: ", "")
	pr.onEnter = func(name string) {
		if name = strings.TrimSpace(name); name == "" {
			return
		}
		dir := resolvePath(p.dir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			a.setErr("%v", err)
			return
		}
		a.fmAfterChange(nil)
		if filepath.Dir(dir) == p.dir {
			a.fmLoad(p, p.dir, filepath.Base(dir))
		}
		a.setMsg("Created %s", a.relPath(dir))
	}
	a.prompt = pr
}

func (a *App) fmNewFile(p *fmPanel) {
	pr := newPrompt("New file: ", "")
	pr.onEnter = func(name string) {
		if name = strings.TrimSpace(name); name == "" {
			return
		}
		path := resolvePath(p.dir, name)
		os.MkdirAll(filepath.Dir(path), 0o755)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil && !errors.Is(err, fs.ErrExist) {
			a.setErr("%v", err)
			return
		}
		if f != nil {
			f.Close()
		}
		a.fmAfterChange(nil)
		a.fmEdit(path)
	}
	a.prompt = pr
}

func (a *App) fmRename(p *fmPanel) {
	e, ok := p.current()
	if !ok || e.up {
		return
	}
	src := p.path(e)
	pr := newPrompt("Rename "+e.name+" to: ", e.name)
	pr.onEnter = func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || name == e.name {
			return
		}
		dst := resolvePath(p.dir, name)
		if _, err := os.Lstat(dst); err == nil {
			a.setErr("%s already exists", name)
			return
		}
		plan := fileops.Plan{Pairs: [][2]string{{src, dst}}}
		a.fmRun("Renaming", plan.Pairs, func(ctx context.Context, pr fileops.Progress) error {
			return fileops.Move(ctx, plan, fileops.Overwrite, pr)
		})
	}
	a.prompt = pr
}

// fmTransfer copies or moves the targets, by default into the other panel.
func (a *App) fmTransfer(move bool) {
	p := a.fmPanel()
	srcs := p.targets()
	if len(srcs) == 0 {
		return
	}
	verb, progress := "Copy", "Copying"
	if move {
		verb, progress = "Move", "Moving"
	}
	pr := newPrompt(fmt.Sprintf("%s %s to: ", verb, describe(srcs)), a.fmOther().dir+string(filepath.Separator))
	pr.complete = func(s string) string { return completePath(p.dir, s) }
	pr.onEnter = func(dst string) {
		if dst = strings.TrimSpace(dst); dst == "" {
			return
		}
		plan, err := fileops.PlanInto(srcs, resolvePath(p.dir, dst))
		if err != nil {
			a.setErr("%v", err)
			return
		}
		run := func(policy fileops.Policy) {
			var moved [][2]string
			if move {
				moved = plan.Pairs
			}
			a.fmRun(progress, moved, func(ctx context.Context, pr fileops.Progress) error {
				if move {
					return fileops.Move(ctx, plan, policy, pr)
				}
				return fileops.Copy(ctx, plan, policy, pr)
			})
		}
		conflicts := plan.Conflicts()
		if len(conflicts) == 0 {
			run(fileops.Overwrite)
			return
		}
		q := newPrompt(fmt.Sprintf("%s already exist(s): (o)verwrite, (s)kip existing, (c)ancel", describe(conflicts)), "")
		q.onKey = func(ev *tcell.EventKey) bool {
			switch {
			case ev.Rune() == 'o' || ev.Rune() == 'O':
				a.prompt = nil
				run(fileops.Overwrite)
			case ev.Rune() == 's' || ev.Rune() == 'S':
				a.prompt = nil
				run(fileops.SkipExisting)
			case ev.Rune() == 'c' || ev.Rune() == 'C' || ev.Key() == tcell.KeyEscape:
				a.prompt = nil
				a.setMsg("Cancelled")
			}
			return true
		}
		a.prompt = q
	}
	a.prompt = pr
}

func (a *App) fmDelete(p *fmPanel) {
	targets := p.targets()
	if len(targets) == 0 {
		return
	}
	a.ask(fmt.Sprintf("Delete %s permanently?", describe(targets)), func() {
		a.fmRun("Deleting", nil, func(ctx context.Context, pr fileops.Progress) error {
			return fileops.Delete(ctx, targets, pr)
		})
	})
}

// fmRun executes a file operation in the background.
func (a *App) fmRun(title string, moved [][2]string, fn func(context.Context, fileops.Progress) error) {
	c := &a.cmd
	ctx, cancel := context.WithCancel(context.Background())
	c.busy, c.cancel = true, cancel
	c.progress = title + "…"
	scr := a.scr
	go func() {
		var last time.Time
		err := fn(ctx, func(done, total int, cur string) {
			if time.Since(last) > 80*time.Millisecond {
				last = time.Now()
				scr.PostEvent(tcell.NewEventInterrupt(fmProgress{fmt.Sprintf("%s %d/%d  %s", title, done, total, cur)}))
			}
		})
		msg := title + " done"
		if errors.Is(err, context.Canceled) {
			msg, err = title+" cancelled", nil
		}
		scr.PostEvent(tcell.NewEventInterrupt(fmDone{msg: msg, err: err, moved: moved}))
	}()
}

func (a *App) fmFinished(d fmDone) {
	c := &a.cmd
	c.busy, c.cancel, c.progress = false, nil, ""
	if d.err != nil {
		a.setErr("%v", d.err)
	} else {
		a.setMsg("%s", d.msg)
	}
	for _, p := range c.panels {
		for name := range p.marked {
			delete(p.marked, name)
		}
	}
	a.fmAfterChange(d.moved)
}

// fmAfterChange refreshes everything after files changed on disk; moved
// files keep their open tabs.
func (a *App) fmAfterChange(moved [][2]string) {
	for _, pair := range moved {
		src, dst := pair[0], pair[1]
		if _, err := os.Lstat(src); err == nil {
			continue // not moved (skipped)
		}
		for _, t := range a.tabs {
			p := t.buf.Path
			if p == src || strings.HasPrefix(p, src+string(filepath.Separator)) {
				t.buf.Path = dst + strings.TrimPrefix(p, src)
				t.updateLanguage()
				a.loadHead(t)
			}
		}
	}
	a.fmReload()
	a.tree.reload()
	a.buildIndex()
	a.refreshGit()
}

func (a *App) fmRows() int { return max(1, a.geo.h-2-6) }

func humanSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fK", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1fM", float64(n)/(1024*1024))
	}
	return fmt.Sprintf("%.1fG", float64(n)/(1024*1024*1024))
}

func fmTime(t time.Time) string {
	if t.Year() == time.Now().Year() {
		return t.Format("Jan 02 15:04")
	}
	return t.Format("Jan 02  2006")
}

func (a *App) shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && (p == home || strings.HasPrefix(p, home+string(filepath.Separator))) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

var (
	fmBg     = tcell.NewHexColor(0x1E2127)
	fmBorder = tcell.NewHexColor(0x5C6370)
	fmMarked = tcell.NewHexColor(0xE5C07B)
)

func (a *App) drawCommander() {
	g := a.geo
	half := g.w / 2
	a.drawFmPanel(0, 0, half)
	a.drawFmPanel(1, half, g.w)
	_ = g
}

func (a *App) drawFmPanel(i, x0, x1 int) {
	c := &a.cmd
	p := &c.panels[i]
	g := a.geo
	box := styleDefault.Background(fmBg).Foreground(tcell.NewHexColor(0xD7DAE0))
	border := box.Foreground(fmBorder)
	active := i == c.side
	if active {
		border = box.Foreground(tcell.NewHexColor(0x61AFEF))
	}
	bottom := g.h - 2 // status bar row
	sepY := bottom - 3
	for y := 0; y < bottom; y++ {
		a.fill(x0, x1, y, box)
	}
	// Frame.
	for x := x0 + 1; x < x1-1; x++ {
		a.scr.SetContent(x, 0, '─', nil, border)
		a.scr.SetContent(x, sepY, '─', nil, border)
		a.scr.SetContent(x, bottom-1, '─', nil, border)
	}
	for y := 1; y < bottom-1; y++ {
		a.scr.SetContent(x0, y, '│', nil, border)
		a.scr.SetContent(x1-1, y, '│', nil, border)
	}
	a.scr.SetContent(x0, 0, '┌', nil, border)
	a.scr.SetContent(x1-1, 0, '┐', nil, border)
	a.scr.SetContent(x0, sepY, '├', nil, border)
	a.scr.SetContent(x1-1, sepY, '┤', nil, border)
	a.scr.SetContent(x0, bottom-1, '└', nil, border)
	a.scr.SetContent(x1-1, bottom-1, '┘', nil, border)

	inner := x1 - x0 - 2
	title := a.shortPath(p.dir)
	if w := runewidth.StringWidth(title); w > inner-4 {
		r := []rune(title)
		title = "…" + string(r[len(r)-(inner-5):])
	}
	titleSt := border.Bold(true)
	if active {
		titleSt = box.Background(tcell.NewHexColor(0x61AFEF)).Foreground(tcell.NewHexColor(0x1E2127)).Bold(true)
	}
	tx := x0 + max(1, (x1-x0-runewidth.StringWidth(title)-2)/2)
	a.put(tx, 0, x1-1, " "+title+" ", titleSt)

	// Columns.
	sizeW, dateW := 7, 12
	showDate, showSize := inner >= 44, inner >= 26
	nameW := inner
	if showSize {
		nameW -= sizeW + 1
	}
	if showDate {
		nameW -= dateW + 1
	}
	head := box.Foreground(fmMarked).Bold(true)
	a.put(x0+1+max(0, (nameW-4)/2), 1, x0+1+nameW, "Name", head)
	if showSize {
		a.put(x0+1+nameW+1+(sizeW-4)/2, 1, x1-1, "Size", head)
	}
	if showDate {
		a.put(x0+1+nameW+1+sizeW+1+(dateW-8)/2, 1, x1-1, "Modified", head)
	}

	rows := sepY - 2
	if p.sel < p.scroll {
		p.scroll = p.sel
	} else if p.sel >= p.scroll+rows {
		p.scroll = p.sel - rows + 1
	}
	p.scroll = max(0, min(p.scroll, len(p.entries)-rows))
	if p.err != "" {
		a.put(x0+2, 2, x1-1, p.err, styleErr.Background(fmBg))
	}
	for row := 0; row < rows && p.scroll+row < len(p.entries); row++ {
		idx := p.scroll + row
		e := p.entries[idx]
		y := 2 + row
		st := box
		name := e.name
		switch {
		case e.up:
			st = st.Bold(true)
		case e.dir:
			name = "/" + name
			st = st.Bold(true)
		case e.link:
			name = "@" + name
		case e.mode&0o111 != 0:
			name = "*" + name
			st = st.Foreground(tcell.NewHexColor(0x98C379))
		default:
			name = " " + name
		}
		if strings.HasPrefix(e.name, ".") && !e.up {
			st = st.Foreground(tcell.NewHexColor(0x7F848E))
		}
		if !e.up {
			if cls := a.gitClassOf(p.path(e), e.dir); cls != gcNone {
				st = st.Foreground(gitColors[cls])
			}
		}
		if p.marked[e.name] {
			st = st.Foreground(fmMarked).Bold(true)
		}
		if idx == p.sel {
			if active {
				st = st.Background(selBg)
			} else {
				st = st.Background(tcell.NewHexColor(0x3E4451))
			}
		}
		a.fill(x0+1, x1-1, y, st)
		a.put(x0+1, y, x0+1+nameW, truncate(name, nameW), st)
		x := x0 + 1 + nameW
		if showSize {
			a.scr.SetContent(x, y, '│', nil, st.Foreground(fmBorder))
			size := humanSize(e.size)
			switch {
			case e.up:
				size = "UP--DIR"
			case e.dir:
				size = "<DIR>"
			}
			a.put(x+1+sizeW-runewidth.StringWidth(size), y, x+1+sizeW, size, st)
			x += sizeW + 1
		}
		if showDate && !e.up {
			a.scr.SetContent(x, y, '│', nil, st.Foreground(fmBorder))
			a.put(x+1, y, x1-1, fmTime(e.mod), st)
		}
	}

	// Info line.
	info := ""
	if n := len(p.marked); n > 0 {
		var total int64
		for _, e := range p.entries {
			if p.marked[e.name] && !e.dir {
				total += e.size
			}
		}
		info = fmt.Sprintf("%d marked, %s bytes", n, humanSize(total))
		a.put(x0+2, sepY+1, x1-1, truncate(info, inner-2), box.Foreground(fmMarked).Bold(true))
	} else if e, ok := p.current(); ok && !e.up {
		info = fmt.Sprintf("%s  %s  %s", e.mode.String(), humanSize(e.size), e.mod.Format("2006-01-02 15:04"))
		a.put(x0+2, sepY+1, x1-1, truncate(e.name, inner-2), box.Bold(true))
		a.put(x0+2, sepY+2, x1-1, truncate(info, inner-2), box.Foreground(tcell.NewHexColor(0x7F848E)))
	}
}

func truncate(s string, w int) string {
	if runewidth.StringWidth(s) <= w {
		return s
	}
	if w <= 1 {
		return "…"
	}
	return runewidth.Truncate(s, w-1, "") + "…"
}

// drawFKeys draws the Midnight Commander style function key bar.
func (a *App) drawFKeys(y int) {
	g := a.geo
	keys := []string{"Help", "Close", "View", "Edit", "Copy", "Move", "Mkdir", "Delete", "Git", "Close"}
	w := max(1, g.w/len(keys))
	label := styleDefault.Background(tcell.NewHexColor(0x2C5A8C)).Foreground(tcell.NewHexColor(0xFFFFFF))
	for i, k := range keys {
		x := i * w
		nx := a.put(x, y, g.w, fmt.Sprint(i+1), styleDefault.Bold(true))
		end := (i + 1) * w
		if i == len(keys)-1 {
			end = g.w
		}
		a.fill(nx, end, y, label)
		a.put(nx, y, end, k, label)
	}
}

// commanderMouse handles mouse events while the commander is shown.
func (a *App) commanderMouse(x, y int, btn tcell.ButtonMask) {
	g := a.geo
	c := &a.cmd
	half := g.w / 2
	side := 0
	if x >= half {
		side = 1
	}
	p := &c.panels[side]
	if btn&(tcell.WheelUp|tcell.WheelDown) != 0 {
		d := 3
		if btn&tcell.WheelUp != 0 {
			d = -3
		}
		p.sel = max(0, min(len(p.entries)-1, p.sel+d))
		return
	}
	if btn&tcell.Button1 == 0 {
		a.mouseDown = false
		return
	}
	if a.mouseDown || c.busy {
		return
	}
	a.mouseDown = true
	c.side = side
	idx := p.scroll + y - 2
	if y < 2 || y >= g.h-2-3 || idx >= len(p.entries) {
		return
	}
	now := time.Now()
	key := Pos{-10 - side, idx}
	double := now.Sub(a.lastClick) < 400*time.Millisecond && a.lastPos == key
	a.lastClick, a.lastPos = now, key
	p.sel = idx
	if double {
		a.lastClick = time.Time{}
		a.fmEnter(p)
	}
}
