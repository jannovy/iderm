package ui

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/jannovy/iderm/internal/buffer"
	"github.com/jannovy/iderm/internal/git"
	"github.com/jannovy/iderm/internal/syntax"
)

// gitClass is the git state of a file or directory for coloring.
type gitClass uint8

const (
	gcNone gitClass = iota
	gcModified
	gcAdded
	gcUntracked
	gcDeleted
	gcConflict
)

var gitColors = map[gitClass]tcell.Color{
	gcModified:  tcell.NewHexColor(0x61AFEF),
	gcAdded:     tcell.NewHexColor(0x98C379),
	gcUntracked: tcell.NewHexColor(0xD19A66),
	gcDeleted:   tcell.NewHexColor(0x7F848E),
	gcConflict:  tcell.NewHexColor(0xE06C75),
}

func classOf(f git.FileStatus) gitClass {
	switch {
	case f.Conflicted():
		return gcConflict
	case f.Untracked():
		return gcUntracked
	case f.X == 'A':
		return gcAdded
	case f.X == 'D' || f.Y == 'D':
		return gcDeleted
	}
	return gcModified
}

// Events of the git integration.
type (
	gitStatusResult struct {
		repo *git.Repo
		st   *git.Status
		err  error
	}
	gitTick   struct{}
	gitOpDone struct {
		msg     string
		err     error
		changed bool // work tree files may have changed
	}
)

const gitRefreshInterval = 3 * time.Second

// openRepo detects the repository of the project root.
func (a *App) openRepo() {
	a.repo, a.gitStatus = nil, nil
	a.gitFiles, a.gitDirs = map[string]git.FileStatus{}, map[string]gitClass{}
	if r, err := git.Open(a.root); err == nil {
		a.repo = r
	}
	a.loadHeads()
	a.refreshGit()
}

func (a *App) startGitTicker() {
	scr := a.scr
	go func() {
		for {
			time.Sleep(gitRefreshInterval)
			scr.PostEvent(tcell.NewEventInterrupt(gitTick{}))
		}
	}()
}

// refreshGit reloads git status in the background.
func (a *App) refreshGit() {
	if a.repo == nil {
		return
	}
	if a.gitBusy {
		a.gitAgain = true
		return
	}
	a.gitBusy = true
	repo, scr := a.repo, a.scr
	go func() {
		st, err := repo.Status()
		scr.PostEvent(tcell.NewEventInterrupt(gitStatusResult{repo, st, err}))
	}()
}

func (a *App) gitStatusDone(r gitStatusResult) {
	a.gitBusy = false
	if r.repo != a.repo {
		return
	}
	if a.gitAgain {
		a.gitAgain = false
		a.refreshGit()
	}
	if r.err != nil {
		return
	}
	headChanged := a.gitStatus != nil && a.gitStatus.Branch != r.st.Branch
	a.gitStatus = r.st
	a.gitFiles = map[string]git.FileStatus{}
	a.gitDirs = map[string]gitClass{}
	for _, f := range r.st.Files {
		abs := a.repo.Abs(f.Path)
		a.gitFiles[abs] = f
		cls := classOf(f)
		for dir := filepath.Dir(abs); len(dir) >= len(a.repo.Root); dir = filepath.Dir(dir) {
			if a.gitDirs[dir] != gcConflict {
				a.gitDirs[dir] = gcModified
				if cls == gcConflict {
					a.gitDirs[dir] = gcConflict
				}
			}
			if dir == a.repo.Root || dir == filepath.Dir(dir) {
				break
			}
		}
	}
	if headChanged {
		a.loadHeads() // the branch was switched outside the editor
	}
	if a.gitPanel.active {
		a.buildGitRows()
	}
}

func (a *App) gitClassOf(path string, dir bool) gitClass {
	if dir {
		return a.gitDirs[path]
	}
	if f, ok := a.gitFiles[path]; ok {
		return classOf(f)
	}
	return gcNone
}

// loadHead loads the HEAD version of a tab's file for the change markers.
func (a *App) loadHead(t *Tab) {
	t.head, t.hasHead, t.marks, t.marksVer = nil, false, nil, -1
	if a.repo == nil || t.buf.Path == "" {
		return
	}
	rel, ok := a.repo.Rel(t.buf.Path)
	if !ok {
		return
	}
	content, ok := a.repo.Show("HEAD", rel)
	if !ok {
		return
	}
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	t.head, t.hasHead = lines, true
}

func (a *App) loadHeads() {
	for _, t := range a.tabs {
		a.loadHead(t)
	}
}

// updateMarks recomputes the change markers when the buffer changed.
func (t *Tab) updateMarks() {
	if !t.hasHead || t.marksVer == t.buf.Version() {
		return
	}
	cur := make([]string, t.buf.LineCount())
	for i := range cur {
		cur[i] = string(t.buf.Line(i))
	}
	t.marks = git.Diff(t.head, cur)
	t.marksVer = t.buf.Version()
}

func (a *App) anyDirty() bool {
	for _, t := range a.tabs {
		if t.buf.Dirty() {
			return true
		}
	}
	return false
}

// reloadFromDisk refreshes unmodified tabs whose files changed on disk
// (after checkout, pull or discard).
func (a *App) reloadFromDisk() {
	for _, t := range a.tabs {
		if t.buf.Path == "" || t.buf.Dirty() {
			continue
		}
		data, err := os.ReadFile(t.buf.Path)
		if err != nil {
			continue
		}
		s := string(data)
		if t.buf.CRLF {
			s = strings.ReplaceAll(s, "\r\n", "\n")
		}
		if s == t.buf.Text() {
			continue
		}
		b, err := buffer.Load(t.buf.Path)
		if err != nil {
			continue
		}
		t.buf = b
		t.sel = false
		t.cur = b.Clamp(t.cur)
		if t.hl != nil {
			t.hl = syntax.NewHighlighter()
		}
		t.lint, t.lintVer = nil, -1
		a.startLint(t)
	}
}

// afterWorkTreeChange refreshes everything that depends on files on disk.
func (a *App) afterWorkTreeChange() {
	a.reloadFromDisk()
	a.loadHeads()
	a.tree.reload()
	a.buildIndex()
	a.refreshGit()
}

// gitAsync runs a slow git operation (network) in the background.
func (a *App) gitAsync(progress string, changes bool, fn func() (string, error)) {
	if a.gitOpRunning {
		a.setErr("Another git operation is still running")
		return
	}
	a.gitOpRunning = true
	a.setMsg("%s", progress)
	scr := a.scr
	go func() {
		msg, err := fn()
		scr.PostEvent(tcell.NewEventInterrupt(gitOpDone{msg: msg, err: err, changed: changes}))
	}()
}

func (a *App) gitOpFinished(d gitOpDone) {
	a.gitOpRunning = false
	if d.err != nil {
		a.setErr("git: %v", d.err)
	} else {
		a.setMsg("%s", d.msg)
	}
	if d.changed && d.err == nil {
		a.afterWorkTreeChange()
	} else {
		a.refreshGit()
	}
}

// ask shows a yes/no question in the message row.
func (a *App) ask(question string, yes func()) {
	p := newPrompt(question+" (y/n)", "")
	p.onKey = func(ev *tcell.EventKey) bool {
		switch {
		case ev.Rune() == 'y' || ev.Rune() == 'Y':
			a.prompt = nil
			yes()
		case ev.Rune() == 'n' || ev.Rune() == 'N' || ev.Key() == tcell.KeyEscape || ev.Key() == tcell.KeyCtrlC:
			a.prompt = nil
			a.setMsg("Cancelled")
		}
		return true
	}
	a.prompt = p
}

// branchLabel is shown in the status bar.
func (a *App) branchLabel() string {
	st := a.gitStatus
	if a.repo == nil || st == nil {
		return ""
	}
	s := "⎇ " + st.Branch
	if st.Ahead > 0 {
		s += " ↑" + itoa(st.Ahead)
	}
	if st.Behind > 0 {
		s += " ↓" + itoa(st.Behind)
	}
	if n := len(st.Files); n > 0 {
		s += " ●" + itoa(n)
	}
	return s
}
