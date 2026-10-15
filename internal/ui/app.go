// Package ui implements the terminal user interface of the editor.
package ui

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/jannovy/iderm/internal/buffer"
	"github.com/jannovy/iderm/internal/git"
	"github.com/jannovy/iderm/internal/index"
)

type focusArea int

const (
	focusEditor focusArea = iota
	focusTree
)

// geometry is the screen layout computed by the last draw.
type geometry struct {
	w, h      int
	treeW     int // including the separator column, 0 when hidden
	edX, edW  int
	textY     int
	textH     int
	gutterW   int
	tabRanges [][2]int // x ranges of tab titles
	branchX   [2]int   // x range of the branch in the status bar
	fileGitX  [2]int   // x range of the [->GIT] button in the status bar
	fileListX [2]int   // x range of the [< LIST] button in the status bar
	changesX  [2]int   // x range of "Changes: N" in the status bar
}

// App is the editor application.
type App struct {
	scr  tcell.Screen // drawing surface below the main bar
	real tcell.Screen // the whole terminal

	barRanges    [][2]int
	gitTabRanges [][2]int

	root string
	php  string

	tabs     []*Tab
	active   int
	tree     *fileTree
	showTree bool
	focus    focusArea
	geo      geometry

	index    *index.Index
	indexing bool
	search   searchBar
	history  []location

	prompt   *prompt
	msg      string
	msgErr   bool
	showHelp bool

	armed, lastArmed string

	clip     string
	clipLine bool

	lastQuery   string
	lastReplace string
	showMatches bool

	pasting  bool
	pasteBuf strings.Builder

	mouseDown bool
	lastClick time.Time
	lastPos   Pos

	repo         *git.Repo
	gitStatus    *git.Status
	gitFiles     map[string]git.FileStatus // absolute path -> status
	gitDirs      map[string]gitClass
	gitBusy      bool
	gitAgain     bool
	gitOpRunning bool
	gitPanel     gitPanel
	hist         historyView
	view         textView
	cmd          commander
	comp         completionPopup
	changes      changesWin

	lintTimer *time.Timer
	quit      bool
}

// New creates the application with root as the project directory.
func New(root string, files []string) *App {
	a := &App{root: root, php: findPHP(), showTree: true, index: index.New(root)}
	a.tree = newTree(root)
	for _, f := range files {
		a.openFile(resolvePath(root, f))
	}
	if len(a.tabs) == 0 {
		a.tabs = []*Tab{newTab(buffer.New())}
		a.focus = focusTree
	}
	if a.php == "" {
		a.setErr("php not found in PATH – syntax checking disabled (set IDERM_PHP)")
	}
	return a
}

// Run initializes the terminal and processes events until the user quits.
func (a *App) Run() (err error) {
	scr, err := tcell.NewScreen()
	if err != nil {
		return err
	}
	if err := scr.Init(); err != nil {
		return err
	}
	a.real = scr
	a.scr = &offsetScreen{Screen: scr, dy: 1}
	defer func() {
		if r := recover(); r != nil {
			scr.Fini()
			panic(r)
		}
		scr.Fini()
	}()
	scr.EnableMouse()
	scr.EnablePaste()
	scr.SetStyle(tcell.StyleDefault)
	for _, t := range a.tabs {
		a.startLint(t)
	}
	a.buildIndex()
	if a.php != "" {
		go index.LoadBuiltins(a.php)
	}
	a.openRepo()
	a.startGitTicker()
	for !a.quit {
		a.draw()
		a.handle(scr.PollEvent())
	}
	return nil
}

func (a *App) handle(ev tcell.Event) {
	switch ev := ev.(type) {
	case *tcell.EventResize:
		a.scr.Sync()
	case *tcell.EventPaste:
		if ev.Start() {
			a.pasting = true
			a.pasteBuf.Reset()
		} else {
			a.pasting = false
			a.pasted(a.pasteBuf.String())
		}
	case *tcell.EventKey:
		a.handleKey(normalizeKey(ev))
	case *tcell.EventMouse:
		a.handleMouse(ev)
	case *tcell.EventInterrupt:
		switch d := ev.Data().(type) {
		case lintRequest:
			a.startLint(d.tab)
		case lintResult:
			a.lintDone(d)
		case indexReady:
			a.indexDone(d)
		case textSearchStart:
			a.startTextSearch(d.gen)
		case textSearchDone:
			a.textSearchFinished(d)
		case gitStatusResult:
			a.gitStatusDone(d)
		case gitTick:
			a.refreshGit()
		case gitOpDone:
			a.gitOpFinished(d)
		case fmProgress:
			a.cmd.progress = d.text
		case fmDone:
			a.fmFinished(d)
		}
	}
}

// normalizeKey maps Ctrl+letter reported as a rune (CSI-u terminals) to tcell's control keys.
func normalizeKey(ev *tcell.EventKey) *tcell.EventKey {
	r := ev.Rune()
	if ev.Key() == tcell.KeyRune && ev.Modifiers()&tcell.ModCtrl != 0 && r >= 'a' && r <= 'z' {
		return tcell.NewEventKey(tcell.KeyCtrlA+tcell.Key(r-'a'), 0, ev.Modifiers())
	}
	return ev
}

func (a *App) tab() *Tab { return a.tabs[a.active] }

func (a *App) setMsg(format string, args ...any) {
	a.msg, a.msgErr = fmt.Sprintf(format, args...), false
}

func (a *App) setErr(format string, args ...any) {
	a.msg, a.msgErr = fmt.Sprintf(format, args...), true
}

// confirm returns true when action was already armed by the previous key press.
func (a *App) confirm(action, warning string) bool {
	if a.lastArmed == action {
		return true
	}
	a.armed = action
	a.setErr("%s", warning)
	return false
}

func (a *App) relPath(p string) string {
	if p == "" {
		return "untitled"
	}
	if r, err := filepath.Rel(a.root, p); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return p
}

func (a *App) openFile(path string) {
	for i, t := range a.tabs {
		if t.buf.Path == path {
			a.active = i
			a.focus = focusEditor
			return
		}
	}
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		a.root = path
		a.tree = newTree(path)
		a.showTree, a.focus = true, focusTree
		if a.scr != nil {
			a.buildIndex()
			a.openRepo()
		}
		return
	}
	b, err := buffer.Load(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		b = buffer.New()
		b.Path = path
		a.setMsg("New file %s", a.relPath(path))
	case err != nil:
		a.setErr("Cannot open %s: %v", a.relPath(path), err)
		return
	}
	t := newTab(b)
	if len(a.tabs) == 1 && a.tabs[0].pristine() {
		a.tabs = a.tabs[:0]
	}
	a.tabs = append(a.tabs, t)
	a.active = len(a.tabs) - 1
	a.focus = focusEditor
	if a.scr != nil {
		a.startLint(t)
		a.loadHead(t)
	}
}

func (a *App) openPrompt() {
	p := newPrompt("Open: ", "")
	p.complete = func(s string) string { return completePath(a.root, s) }
	p.onEnter = func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			a.openFile(resolvePath(a.root, s))
		}
	}
	a.prompt = p
}

func (a *App) newFile() {
	a.tabs = append(a.tabs, newTab(buffer.New()))
	a.active = len(a.tabs) - 1
	a.focus = focusEditor
}

func (a *App) save(t *Tab, after func()) {
	if t.buf.Path == "" {
		p := newPrompt("Save as: ", "")
		p.complete = func(s string) string { return completePath(a.root, s) }
		p.onEnter = func(s string) {
			if s = strings.TrimSpace(s); s == "" {
				return
			}
			t.buf.Path = resolvePath(a.root, s)
			t.updateLanguage()
			a.save(t, after)
		}
		a.prompt = p
		return
	}
	if err := t.buf.Save(); err != nil {
		a.setErr("Save failed: %v", err)
		return
	}
	a.setMsg("Saved %s", a.relPath(t.buf.Path))
	if index.IsPHP(t.buf.Path) {
		a.index.UpdateFile(t.buf.Path)
	}
	if !t.hasHead {
		a.loadHead(t) // "save as" or a file committed meanwhile
	}
	a.refreshGit()
	a.tree.reload()
	a.startLint(t)
	if after != nil {
		after()
	}
}

func (a *App) closeTab() {
	t := a.tab()
	if t.buf.Dirty() && !a.confirm("close", fmt.Sprintf("%s has unsaved changes – Ctrl+W or double-click again to discard, Ctrl+S to save", t.title())) {
		return
	}
	a.tabs = append(a.tabs[:a.active], a.tabs[a.active+1:]...)
	if len(a.tabs) == 0 {
		a.tabs = []*Tab{newTab(buffer.New())}
	}
	a.active = min(a.active, len(a.tabs)-1)
}

func (a *App) quitCmd() {
	dirty := 0
	for _, t := range a.tabs {
		if t.buf.Dirty() {
			dirty++
		}
	}
	if dirty > 0 && !a.confirm("quit", fmt.Sprintf("%d file(s) with unsaved changes – Ctrl+Q again to quit without saving", dirty)) {
		return
	}
	a.quit = true
}

func (a *App) switchTab(i int) {
	if len(a.tabs) == 0 {
		return
	}
	a.active = (i%len(a.tabs) + len(a.tabs)) % len(a.tabs)
	a.focus = focusEditor
}

func (a *App) toggleTree() {
	a.showTree = !a.showTree
	if !a.showTree {
		a.focus = focusEditor
	}
}

func (a *App) focusTreeCmd() {
	if a.focus == focusTree {
		a.focus = focusEditor
		return
	}
	a.showTree, a.focus = true, focusTree
	if p := a.tab().buf.Path; p != "" {
		a.tree.reveal(p)
	}
}

// inTree reports whether path is an existing file shown in the file tree.
func (a *App) inTree(path string) bool {
	if path == "" {
		return false
	}
	if r, err := filepath.Rel(a.tree.root.path, path); err != nil || strings.HasPrefix(r, "..") {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// revealInTree shows the tree with the current file selected.
func (a *App) revealInTree() {
	a.closeCompletion()
	if p := a.tab().buf.Path; a.tree.reveal(p) {
		a.showTree, a.focus = true, focusTree
	} else {
		a.tree.reload()
		if a.tree.reveal(p) {
			a.showTree, a.focus = true, focusTree
		} else {
			a.setErr("%s is not in the file tree", a.relPath(p))
		}
	}
}

func (a *App) hasTab(t *Tab) bool {
	for _, x := range a.tabs {
		if x == t {
			return true
		}
	}
	return false
}

// scheduleLint runs a syntax check shortly after the user stops typing.
func (a *App) scheduleLint(t *Tab) {
	if a.php == "" || !t.isPHP() {
		return
	}
	if a.lintTimer != nil {
		a.lintTimer.Stop()
	}
	scr := a.scr
	a.lintTimer = time.AfterFunc(700*time.Millisecond, func() {
		scr.PostEvent(tcell.NewEventInterrupt(lintRequest{t}))
	})
}

func (a *App) startLint(t *Tab) {
	if a.php == "" || !t.isPHP() || !a.hasTab(t) {
		return
	}
	src, ver, php, scr := t.buf.Text(), t.buf.Version(), a.php, a.scr
	go func() {
		errs, err := runLint(php, src)
		scr.PostEvent(tcell.NewEventInterrupt(lintResult{tab: t, version: ver, errs: errs, err: err}))
	}()
}

func (a *App) lintDone(r lintResult) {
	if !a.hasTab(r.tab) || r.tab.buf.Version() != r.version {
		return
	}
	if r.err != nil {
		a.setErr("php -l failed: %v", r.err)
		return
	}
	r.tab.lint = r.errs
	r.tab.lintVer = r.version
}

func (a *App) nextLintError() {
	t := a.tab()
	if len(t.lint) == 0 {
		a.setMsg("No syntax errors")
		return
	}
	target := t.lint[0]
	for _, e := range t.lint {
		if e.line > t.cur.Line {
			target = e
			break
		}
	}
	t.moveTo(Pos{target.line, 0}, false)
	t.moveTo(t.smartHome(), false)
	a.setErr("Line %d: %s", target.line+1, target.msg)
}

func (a *App) copy(cut bool) {
	t := a.tab()
	s, e, ok := t.selection()
	if ok {
		a.clip, a.clipLine = t.buf.TextRange(s, e), false
	} else {
		l := t.cur.Line
		a.clip, a.clipLine = string(t.buf.Line(l))+"\n", true
	}
	writeClipboard(a.clip)
	if !cut {
		a.setMsg("Copied")
		return
	}
	if ok {
		t.deleteSelection()
	} else {
		t.deleteLines()
	}
}

func (a *App) paste() {
	t := a.tab()
	text, ok := readClipboard()
	if !ok || text == "" {
		text = a.clip
	}
	if text == "" {
		return
	}
	t.buf.Seal()
	if _, _, sel := t.selection(); a.clipLine && text == a.clip && !sel {
		col := t.cur.Col
		t.buf.Insert(Pos{t.cur.Line, 0}, text)
		t.place(Pos{t.cur.Line + strings.Count(text, "\n"), col})
	} else {
		t.insert(text)
	}
	t.buf.Seal()
}

// pasted handles text that arrived via bracketed paste.
func (a *App) pasted(text string) {
	if text == "" {
		return
	}
	if a.search.active {
		a.searchInsert(text)
		return
	}
	if a.prompt != nil {
		a.prompt.insert(text)
		if a.prompt.onChange != nil {
			a.prompt.onChange(string(a.prompt.text))
		}
		return
	}
	if a.focus != focusEditor {
		return
	}
	t := a.tab()
	t.buf.Seal()
	t.insert(text)
	t.buf.Seal()
	a.scheduleLint(t)
}
