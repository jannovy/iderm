package ui

import (
	"path/filepath"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
)

// offsetScreen shifts all drawing down by dy rows, reserving the top of the
// terminal for the main bar.
type offsetScreen struct {
	tcell.Screen
	dy int
}

func (s *offsetScreen) SetContent(x, y int, mainc rune, combc []rune, st tcell.Style) {
	if y >= 0 {
		s.Screen.SetContent(x, y+s.dy, mainc, combc, st)
	}
}

func (s *offsetScreen) ShowCursor(x, y int) { s.Screen.ShowCursor(x, y+s.dy) }

func (s *offsetScreen) Size() (int, int) {
	w, h := s.Screen.Size()
	return w, max(h-s.dy, 1)
}

// Main modes of the application.
const (
	modeIDE = iota
	modeFiles
	modeGit
)

var modeNames = [...]string{"IDE", "Files", "Git"}

func (a *App) mode() int {
	switch {
	case a.cmd.active:
		return modeFiles
	case a.gitPanel.active || a.hist.active:
		return modeGit
	}
	return modeIDE
}

func (a *App) setMode(m int) {
	if m == a.mode() {
		return
	}
	a.closeSearch()
	switch m {
	case modeIDE:
		a.cmd.active = false
		a.closeGitPanel()
	case modeFiles:
		a.closeGitPanel()
		a.toggleCommander()
	case modeGit:
		a.cmd.active = false
		a.openGitPanel()
	}
}

var (
	barBg     = tcell.NewHexColor(0x181A1F)
	barStyle  = styleDefault.Background(barBg).Foreground(tcell.NewHexColor(0x9DA5B4))
	barActive = styleDefault.Background(tcell.NewHexColor(0x61AFEF)).Foreground(tcell.NewHexColor(0x181A1F)).Bold(true)
)

// putReal writes on the real screen (row 0 belongs to the main bar).
func (a *App) putReal(x, y, maxX int, s string, st tcell.Style) int {
	for _, r := range s {
		w := max(runewidth.RuneWidth(r), 1)
		if x+w > maxX {
			break
		}
		a.real.SetContent(x, y, r, nil, st)
		x += w
	}
	return x
}

func (a *App) drawMainBar() {
	w, _ := a.real.Size()
	for x := 0; x < w; x++ {
		a.real.SetContent(x, 0, ' ', nil, barStyle)
	}
	x := a.putReal(1, 0, w, "◆ iderm", barStyle.Foreground(tcell.NewHexColor(0x61AFEF)).Bold(true))
	x += 2
	a.barRanges = a.barRanges[:0]
	cur := a.mode()
	for m, name := range modeNames {
		st := barStyle
		if m == cur {
			st = barActive
		}
		start := x
		x = a.putReal(x, 0, w, " "+name+" ", st)
		a.barRanges = append(a.barRanges, [2]int{start, x})
		x++
	}
	for _, path := range []string{a.shortPath(a.root), filepath.Base(a.root)} {
		right := path + " "
		if rx := w - runewidth.StringWidth(right); rx > x+2 {
			a.putReal(rx, 0, w, right, barStyle)
			return
		}
	}
}

// mainBarClick switches the mode when a mode button is clicked.
func (a *App) mainBarClick(x int) {
	for m, r := range a.barRanges {
		if x >= r[0] && x < r[1] {
			a.setMode(m)
			return
		}
	}
}

// Sub-tabs of the git mode, drawn in the first row of the work area.
var gitTabs = [...]string{"Changes", "History"}

func (a *App) drawGitTabs() {
	g := a.geo
	a.fill(0, g.w, 0, styleTabBar)
	x := 1
	a.gitTabRanges = a.gitTabRanges[:0]
	for i, name := range gitTabs {
		st := styleTabBar
		if i == 0 && !a.hist.active || i == 1 && a.hist.active {
			st = styleTabAct
		}
		start := x
		x = a.put(x, 0, g.w, " "+name+" ", st)
		a.gitTabRanges = append(a.gitTabRanges, [2]int{start, x})
		x = a.put(x, 0, g.w, "│", styleTabBar)
	}
}

func (a *App) gitTabClick(x int) {
	for i, r := range a.gitTabRanges {
		if x < r[0] || x >= r[1] {
			continue
		}
		a.view.active = false
		if i == 0 {
			a.hist.active = false
			if !a.gitPanel.active {
				a.openGitPanel()
			}
		} else if !a.hist.active {
			a.gitPanel.active = true
			a.buildGitRows()
			a.openHistory()
		}
	}
}
