package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"

	"iderm/internal/git"
)

func itoa(n int) string { return strconv.Itoa(n) }

const (
	groupConflicts = iota
	groupStaged
	groupChanges
	groupUntracked
)

var groupTitles = [...]string{"Merge conflicts", "Staged", "Changes", "Untracked"}

type gitRow struct {
	header bool
	title  string
	group  int
	f      git.FileStatus
}

// gitPanel lists changed files of the repository.
type gitPanel struct {
	active bool
	rows   []gitRow
	sel    int
	scroll int
}

const gitHints = "Space stage/unstage  Enter diff  o open  a stage all  u unstage all  c commit  d discard  p push  P pull  b branch  l history  Esc close"

func (a *App) openGitPanel() {
	if a.repo == nil {
		root := a.root
		a.ask("Not a git repository. Run git init in "+a.relPath(root)+"?", func() {
			if err := git.Init(root); err != nil {
				a.setErr("git init: %v", err)
				return
			}
			a.openRepo()
			a.setMsg("Initialized a git repository")
			a.openGitPanel()
		})
		return
	}
	a.closeSearch()
	a.gitPanel.active = true
	a.buildGitRows()
	a.refreshGit()
}

func (a *App) closeGitPanel() {
	a.gitPanel.active = false
	a.view.active = false
	a.hist.active = false
}

func (a *App) buildGitRows() {
	p := &a.gitPanel
	var selKey string
	if p.sel < len(p.rows) && !p.rows[p.sel].header {
		selKey = fmt.Sprint(p.rows[p.sel].group, p.rows[p.sel].f.Path)
	}
	groups := make([][]gitRow, len(groupTitles))
	if st := a.gitStatus; st != nil {
		for _, f := range st.Files {
			switch {
			case f.Conflicted():
				groups[groupConflicts] = append(groups[groupConflicts], gitRow{group: groupConflicts, f: f})
			case f.Untracked():
				groups[groupUntracked] = append(groups[groupUntracked], gitRow{group: groupUntracked, f: f})
			default:
				if f.Staged() {
					groups[groupStaged] = append(groups[groupStaged], gitRow{group: groupStaged, f: f})
				}
				if f.Unstaged() {
					groups[groupChanges] = append(groups[groupChanges], gitRow{group: groupChanges, f: f})
				}
			}
		}
	}
	p.rows = p.rows[:0]
	for g, rows := range groups {
		if len(rows) == 0 {
			continue
		}
		p.rows = append(p.rows, gitRow{header: true, group: g, title: fmt.Sprintf("%s (%d)", groupTitles[g], len(rows))})
		p.rows = append(p.rows, rows...)
	}
	for i, r := range p.rows {
		if !r.header && fmt.Sprint(r.group, r.f.Path) == selKey {
			p.sel = i
			return
		}
	}
	p.sel = min(p.sel, len(p.rows)-1)
	a.moveGitSel(0, 1)
}

func (a *App) moveGitSel(delta, dir int) {
	p := &a.gitPanel
	if len(p.rows) == 0 {
		p.sel = 0
		return
	}
	i := max(0, min(p.sel+delta, len(p.rows)-1))
	for i >= 0 && i < len(p.rows) && p.rows[i].header {
		i += dir
	}
	if i < 0 || i >= len(p.rows) {
		i = max(0, min(p.sel+delta, len(p.rows)-1))
		for i >= 0 && i < len(p.rows) && p.rows[i].header {
			i -= dir
		}
	}
	if i >= 0 && i < len(p.rows) {
		p.sel = i
	}
}

func (a *App) gitSelected() (gitRow, bool) {
	p := &a.gitPanel
	if p.sel >= 0 && p.sel < len(p.rows) && !p.rows[p.sel].header {
		return p.rows[p.sel], true
	}
	return gitRow{}, false
}

// gitPanelKey handles keys while the panel is open; false lets global keys run.
func (a *App) gitPanelKey(ev *tcell.EventKey) bool {
	rows := max(1, a.geo.textH-1)
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyF9:
		a.closeGitPanel()
	case tcell.KeyUp:
		a.moveGitSel(-1, -1)
	case tcell.KeyDown:
		a.moveGitSel(1, 1)
	case tcell.KeyPgUp:
		a.moveGitSel(-rows, -1)
	case tcell.KeyPgDn:
		a.moveGitSel(rows, 1)
	case tcell.KeyHome:
		a.gitPanel.sel = 0
		a.moveGitSel(0, 1)
	case tcell.KeyEnd:
		a.gitPanel.sel = len(a.gitPanel.rows) - 1
		a.moveGitSel(0, -1)
	case tcell.KeyEnter:
		if r, ok := a.gitSelected(); ok {
			a.showDiff(r.f, r.group == groupStaged)
		}
	case tcell.KeyRune:
		return a.gitPanelRune(ev.Rune())
	default:
		return false
	}
	return true
}

func (a *App) gitPanelRune(ch rune) bool {
	r, ok := a.gitSelected()
	switch ch {
	case ' ':
		if ok {
			a.toggleStage(r)
		}
	case 'o':
		if ok && r.f.X != 'D' && r.f.Y != 'D' {
			a.closeGitPanel()
			a.pushHistory()
			a.openFile(a.repo.Abs(r.f.Path))
		}
	case 'a':
		a.gitSync(a.repo.StageAll(), "Staged all changes")
	case 'u':
		var paths []string
		for _, f := range a.gitStatus.Files {
			if f.Staged() {
				paths = append(paths, f.Path)
			}
		}
		if len(paths) > 0 {
			a.gitSync(a.repo.Unstage(paths...), "Unstaged all files")
		}
	case 'c':
		a.commitCmd()
	case 'd':
		if ok {
			a.discardCmd(r)
		}
	case 'p':
		st := a.gitStatus
		repo := a.repo
		a.gitAsync("Pushing…", false, func() (string, error) { return repo.Push(st) })
	case 'P':
		if a.anyDirty() {
			a.setErr("Save or close modified tabs before pulling")
			break
		}
		repo := a.repo
		a.gitAsync("Pulling…", true, func() (string, error) { return repo.Pull() })
	case 'b':
		a.branchPicker()
	case 'l', 'h':
		a.openHistory()
	case 'r':
		a.refreshGit()
		a.setMsg("Refreshing git status…")
	case 'q':
		a.closeGitPanel()
	default:
		return false
	}
	return true
}

// gitSync reports the result of a quick git command and refreshes the status.
func (a *App) gitSync(err error, ok string) {
	if err != nil {
		a.setErr("git: %v", err)
	} else {
		a.setMsg("%s", ok)
	}
	a.refreshGit()
}

func (a *App) toggleStage(r gitRow) {
	if r.group == groupStaged {
		a.gitSync(a.repo.Unstage(r.f.Path), "Unstaged "+r.f.Path)
		return
	}
	msg := "Staged " + r.f.Path
	if r.group == groupConflicts {
		msg = "Marked " + r.f.Path + " as resolved"
	}
	a.gitSync(a.repo.Stage(r.f.Path), msg)
}

func (a *App) discardCmd(r gitRow) {
	switch r.group {
	case groupStaged:
		a.setErr("Unstage the file first (Space), then discard its changes")
		return
	case groupConflicts:
		a.setErr("Resolve the conflict in the editor, then stage it")
		return
	}
	abs := a.repo.Abs(r.f.Path)
	for _, t := range a.tabs {
		if t.buf.Path == abs && t.buf.Dirty() {
			a.setErr("%s has unsaved changes in the editor", r.f.Path)
			return
		}
	}
	question := "Discard changes in " + r.f.Path + "?"
	if r.f.Untracked() {
		question = "Delete untracked file " + r.f.Path + "?"
	}
	a.ask(question, func() {
		if err := a.repo.Discard(r.f); err != nil {
			a.setErr("git: %v", err)
			return
		}
		a.setMsg("Discarded %s", r.f.Path)
		a.afterWorkTreeChange()
	})
}

func (a *App) commitCmd() {
	st := a.gitStatus
	if st == nil {
		return
	}
	staged := 0
	for _, f := range st.Files {
		if f.Staged() {
			staged++
		}
	}
	if staged > 0 {
		a.commitMessage()
		return
	}
	if len(st.Files) == 0 {
		a.setMsg("Nothing to commit")
		return
	}
	a.ask("Nothing is staged. Stage all changes and commit?", func() {
		if err := a.repo.StageAll(); err != nil {
			a.setErr("git: %v", err)
			return
		}
		a.refreshGit()
		a.commitMessage()
	})
}

func (a *App) commitMessage() {
	label := "Commit message: "
	if a.anyDirty() {
		label = "Commit message (unsaved editor changes are not included): "
	}
	p := newPrompt(label, "")
	p.onEnter = func(msg string) {
		if msg = strings.TrimSpace(msg); msg == "" {
			a.setErr("Empty commit message – nothing committed")
			return
		}
		hash, err := a.repo.Commit(msg)
		if err != nil {
			a.setErr("git: %v", err)
			return
		}
		a.setMsg("Committed %s: %s", hash, msg)
		a.loadHeads()
		a.refreshGit()
	}
	a.prompt = p
}

func (a *App) branchPicker() {
	branches, err := a.repo.Branches()
	if err != nil {
		a.setErr("git: %v", err)
		return
	}
	current := ""
	if a.gitStatus != nil {
		current = a.gitStatus.Branch
	}
	items := []result{{kind: resSymbol, badge: "new", label: "Create new branch…", action: a.newBranchPrompt}}
	for _, b := range branches {
		detail := ""
		if b == current {
			detail = "current"
		}
		items = append(items, result{kind: resSymbol, badge: "branch", label: b, detail: detail, action: func() { a.switchBranch(b, false) }})
	}
	a.showPicker("Switch branch", items)
	if len(items) > 1 {
		a.search.sel = 1
	}
}

func (a *App) newBranchPrompt() {
	p := newPrompt("New branch name: ", "")
	p.onEnter = func(name string) {
		if name = strings.TrimSpace(name); name != "" {
			a.switchBranch(name, true)
		}
	}
	a.prompt = p
}

func (a *App) switchBranch(name string, create bool) {
	if a.anyDirty() {
		a.setErr("Save or close modified tabs before switching branches")
		return
	}
	if err := a.repo.Switch(name, create); err != nil {
		a.setErr("git: %v", err)
		return
	}
	a.setMsg("Switched to %s", name)
	a.afterWorkTreeChange()
}

func gitLetter(r gitRow) string {
	f := r.f
	switch r.group {
	case groupStaged:
		return string(f.X)
	case groupChanges:
		return string(f.Y)
	case groupUntracked:
		return "?"
	}
	return string([]byte{f.X, f.Y})
}

func (a *App) panelBottom() int { return a.geo.textY + a.geo.textH }

func (a *App) drawGitPanel() {
	g := a.geo
	p := &a.gitPanel
	box := styleDefault.Background(tcell.NewHexColor(0x21252B)).Foreground(tcell.NewHexColor(0xD7DAE0))
	for y := 1; y < a.panelBottom(); y++ {
		a.fill(g.edX, g.w, y, box)
	}
	head := " GIT"
	if l := a.branchLabel(); l != "" {
		head += "   " + l
	}
	if st := a.gitStatus; st != nil && st.Upstream != "" {
		head += "   → " + st.Upstream
	}
	a.put(g.edX, 1, g.w, head, box.Bold(true).Foreground(tcell.NewHexColor(0x61AFEF)))
	if len(p.rows) == 0 {
		msg := "Working tree clean"
		if a.gitStatus == nil {
			msg = "Loading…"
		}
		a.put(g.edX+2, 3, g.w, msg, box.Foreground(tcell.NewHexColor(0x7F848E)))
		return
	}
	h := a.panelBottom() - 2
	if p.sel < p.scroll {
		p.scroll = p.sel
	} else if p.sel >= p.scroll+h {
		p.scroll = p.sel - h + 1
	}
	p.scroll = max(0, min(p.scroll, len(p.rows)-1))
	for row := 0; row < h && p.scroll+row < len(p.rows); row++ {
		i := p.scroll + row
		r := p.rows[i]
		y := 2 + row
		if r.header {
			a.put(g.edX+1, y, g.w, strings.ToUpper(r.title), box.Foreground(tcell.NewHexColor(0x7F848E)).Bold(true))
			continue
		}
		st := box
		if i == p.sel {
			st = box.Background(selBg)
			a.fill(g.edX, g.w, y, st)
		}
		color := gitColors[classOf(r.f)]
		if r.group == groupUntracked {
			color = gitColors[gcUntracked]
		}
		x := a.put(g.edX+3, y, g.w, gitLetter(r), st.Foreground(color).Bold(true))
		name := r.f.Path
		if r.f.Orig != "" {
			name += "  ← " + r.f.Orig
		}
		a.put(x+3, y, g.w, name, st.Foreground(color))
	}
}

// gitPanelMouse handles clicks in the panel area; false means the click was outside.
func (a *App) gitPanelMouse(x, y int, btn tcell.ButtonMask, first bool) bool {
	g := a.geo
	if x < g.edX || y < 1 || y >= a.panelBottom() {
		return false
	}
	p := &a.gitPanel
	if btn&(tcell.WheelUp|tcell.WheelDown) != 0 {
		d := 3
		if btn&tcell.WheelUp != 0 {
			d = -3
		}
		a.moveGitSel(d, max(-1, min(1, d)))
		return true
	}
	if !first || btn&tcell.Button1 == 0 {
		return true
	}
	i := p.scroll + y - 2
	if y < 2 || i >= len(p.rows) || p.rows[i].header {
		return true
	}
	if i == p.sel {
		r := p.rows[i]
		a.showDiff(r.f, r.group == groupStaged)
		return true
	}
	p.sel = i
	return true
}
