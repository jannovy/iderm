package ui

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/jannovy/iderm/internal/git"
)

// textView is a read-only overlay for diffs and the commit log.
type textView struct {
	active  bool
	title   string
	lines   []string
	styles  []tcell.Style
	targets []int // line in file to jump to on Enter, -1 when none
	file    string
	sel     int
	scroll  int
}

var (
	diffAdd  = styleDefault.Foreground(tcell.NewHexColor(0x98C379))
	diffDel  = styleDefault.Foreground(tcell.NewHexColor(0xE06C75))
	diffHunk = styleDefault.Foreground(tcell.NewHexColor(0x56B6C2))
	diffMeta = styleDefault.Foreground(tcell.NewHexColor(0x7F848E)).Bold(true)
	hunkRe   = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)`)
)

func (a *App) showDiff(f git.FileStatus, staged bool) {
	out, err := a.repo.Diff(f, staged)
	if err != nil {
		a.setErr("git: %v", err)
		return
	}
	title := "Diff " + f.Path
	if staged {
		title += " (staged)"
	}
	a.view = diffView(title, out, a.repo.Abs(f.Path))
}

// diffView builds a viewer for unified diff output; Enter jumps to file.
func diffView(title, out, file string) textView {
	v := textView{active: true, title: title, file: file}
	newLine := -1
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		st, target := styleDefault, -1
		switch {
		case strings.HasPrefix(l, "@@"):
			st = diffHunk
			if m := hunkRe.FindStringSubmatch(l); m != nil {
				n, _ := strconv.Atoi(m[1])
				newLine = n - 1
				target = max(newLine, 0)
			}
		case newLine < 0:
			st = diffMeta
		case strings.HasPrefix(l, "+"):
			st, target = diffAdd, newLine
			newLine++
		case strings.HasPrefix(l, "-"):
			st, target = diffDel, newLine
		case strings.HasPrefix(l, `\`):
			st = diffMeta
		default:
			target = newLine
			newLine++
		}
		v.lines = append(v.lines, strings.ReplaceAll(l, "\t", "    "))
		v.styles = append(v.styles, st)
		v.targets = append(v.targets, target)
	}
	if len(v.lines) == 1 && v.lines[0] == "" {
		v.lines[0] = "(no textual changes)"
	}
	for i, t := range v.targets {
		if t >= 0 && strings.HasPrefix(v.lines[i], "@@") {
			v.sel = i
			break
		}
	}
	return v
}

func (a *App) viewKey(ev *tcell.EventKey) bool {
	v := &a.view
	page := max(1, a.panelBottom()-3)
	switch ev.Key() {
	case tcell.KeyEscape:
		v.active = false
	case tcell.KeyUp:
		v.sel--
	case tcell.KeyDown:
		v.sel++
	case tcell.KeyPgUp:
		v.sel -= page
	case tcell.KeyPgDn:
		v.sel += page
	case tcell.KeyHome:
		v.sel = 0
	case tcell.KeyEnd:
		v.sel = len(v.lines) - 1
	case tcell.KeyEnter:
		if v.sel < len(v.targets) && v.targets[v.sel] >= 0 && v.file != "" {
			file, line := v.file, v.targets[v.sel]
			a.closeGitPanel()
			a.jumpTo(file, line, 0)
		}
	case tcell.KeyRune:
		if ev.Rune() != 'q' {
			return true
		}
		v.active = false
	default:
		return false
	}
	v.sel = max(0, min(v.sel, len(v.lines)-1))
	return true
}

func (a *App) drawTextView() {
	g := a.geo
	v := &a.view
	box := styleDefault.Background(tcell.NewHexColor(0x1E2127))
	for y := 1; y < a.panelBottom(); y++ {
		a.fill(g.edX, g.w, y, box)
	}
	a.put(g.edX, 1, g.w, " "+v.title+"   (↑↓ move, Enter go to line, Esc back)", box.Bold(true).Foreground(tcell.NewHexColor(0x61AFEF)))
	h := a.panelBottom() - 2
	if v.sel < v.scroll {
		v.scroll = v.sel
	} else if v.sel >= v.scroll+h {
		v.scroll = v.sel - h + 1
	}
	for row := 0; row < h && v.scroll+row < len(v.lines); row++ {
		i := v.scroll + row
		st := v.styles[i].Background(tcell.NewHexColor(0x1E2127))
		if i == v.sel {
			st = st.Background(selBg)
			a.fill(g.edX, g.w, 2+row, st)
		}
		a.put(g.edX+1, 2+row, g.w, v.lines[i], st)
	}
}

func (a *App) viewMouse(x, y int, btn tcell.ButtonMask, first bool) bool {
	g := a.geo
	if x < g.edX || y < 1 || y >= a.panelBottom() {
		return false
	}
	v := &a.view
	switch {
	case btn&tcell.WheelUp != 0:
		v.sel = max(0, v.sel-3)
	case btn&tcell.WheelDown != 0:
		v.sel = min(len(v.lines)-1, v.sel+3)
	case first && btn&tcell.Button1 != 0 && y >= 2:
		v.sel = max(0, min(v.scroll+y-2, len(v.lines)-1))
	}
	return true
}
