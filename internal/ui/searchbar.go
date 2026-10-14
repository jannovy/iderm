package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"

	"iderm/internal/index"
)

type resultKind uint8

const (
	resHeader resultKind = iota
	resFile
	resSymbol
	resText
)

type result struct {
	kind          resultKind
	badge         string
	label, detail string
	hl            [2]int // highlighted rune range of label; hl[1] == 0 means none
	file          string
	line, col     int
}

// searchBar is the "search everywhere" field above the editor. In picker
// mode it shows a fixed list of choices instead (e.g. several definitions).
type searchBar struct {
	active bool
	picker bool
	title  string
	text   []rune
	cur    int

	results []result
	sel     int
	scroll  int

	files, symbols, texts []result
	textBusy              bool
	gen                   int
	timer                 *time.Timer
	cancel                context.CancelFunc
}

// textSearchStart fires after the debounce delay; textSearchDone carries results.
type textSearchStart struct{ gen int }
type textSearchDone struct {
	gen     int
	matches []index.TextMatch
}

const (
	maxFileResults   = 8
	maxSymbolResults = 12
	maxTextResults   = 200
)

func (a *App) openSearch() {
	s := &a.search
	s.active, s.picker, s.title = true, false, ""
	s.cur = len(s.text)
	a.updateSearch()
}

func (a *App) closeSearch() {
	s := &a.search
	s.active, s.picker = false, false
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.timer != nil {
		s.timer.Stop()
	}
}

// showPicker lets the user choose one of the given results.
func (a *App) showPicker(title string, items []result) {
	a.closeSearch()
	s := &a.search
	s.active, s.picker, s.title = true, true, title
	s.results, s.sel, s.scroll = items, 0, 0
}

func (a *App) updateSearch() {
	s := &a.search
	q := strings.TrimSpace(string(s.text))
	s.gen++
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.timer != nil {
		s.timer.Stop()
	}
	s.files, s.symbols, s.texts = nil, nil, nil
	s.textBusy = false
	if q != "" {
		for _, p := range a.index.SearchFiles(q, maxFileResults) {
			name := filepath.Base(p)
			s.files = append(s.files, result{kind: resFile, badge: "file", label: name, detail: a.relPath(filepath.Dir(p)), hl: findFold(name, q), file: p})
		}
		for _, sym := range a.index.SearchSymbols(q, maxSymbolResults) {
			ns := ""
			if i := strings.LastIndexByte(sym.FQN, '\\'); i >= 0 {
				ns = sym.FQN[:i]
			}
			detail := a.relPath(sym.File)
			if ns != "" {
				detail = ns + "  " + detail
			}
			s.symbols = append(s.symbols, result{kind: resSymbol, badge: sym.Kind.String(), label: sym.Name, detail: detail,
				hl: findFold(sym.Name, q), file: sym.File, line: sym.Line, col: sym.Col})
		}
		if utf8.RuneCountInString(q) >= 2 {
			s.textBusy = true
			gen, scr := s.gen, a.scr
			s.timer = time.AfterFunc(250*time.Millisecond, func() {
				scr.PostEvent(tcell.NewEventInterrupt(textSearchStart{gen}))
			})
		}
	}
	a.rebuildResults()
	s.sel, s.scroll = 0, 0
	a.selectResult(0, 1)
}

func (a *App) startTextSearch(gen int) {
	s := &a.search
	if gen != s.gen || !s.active {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	q, ix, scr := strings.TrimSpace(string(s.text)), a.index, a.scr
	go func() {
		m := ix.SearchText(ctx, q, maxTextResults)
		if ctx.Err() == nil {
			scr.PostEvent(tcell.NewEventInterrupt(textSearchDone{gen, m}))
		}
	}()
}

func (a *App) textSearchFinished(d textSearchDone) {
	s := &a.search
	if d.gen != s.gen || !s.active {
		return
	}
	q := []rune(strings.TrimSpace(string(s.text)))
	s.textBusy = false
	s.texts = nil
	for _, m := range d.matches {
		line := []rune(m.Text)
		lead := len(leadingWhitespace(line))
		label := line[lead:]
		start := m.Col - lead
		if start > 40 {
			cut := start - 20
			label = append([]rune("…"), label[cut:]...)
			start -= cut - 1
		}
		s.texts = append(s.texts, result{kind: resText, badge: "text", label: string(label),
			detail: fmt.Sprintf("%s:%d", a.relPath(m.File), m.Line+1), hl: [2]int{start, start + len(q)},
			file: m.File, line: m.Line, col: m.Col})
	}
	selected := s.sel
	a.rebuildResults()
	s.sel = min(selected, len(s.results)-1)
	a.selectResult(0, 1)
}

func (a *App) rebuildResults() {
	s := &a.search
	s.results = s.results[:0]
	add := func(title string, items []result) {
		if len(items) == 0 {
			return
		}
		s.results = append(s.results, result{kind: resHeader, label: title})
		s.results = append(s.results, items...)
	}
	add("Files", s.files)
	add("Classes & functions", s.symbols)
	switch {
	case s.textBusy:
		s.results = append(s.results, result{kind: resHeader, label: "Text  (searching…)"})
	case len(s.texts) > 0:
		title := fmt.Sprintf("Text  (%d)", len(s.texts))
		if len(s.texts) >= maxTextResults {
			title = fmt.Sprintf("Text  (first %d)", maxTextResults)
		}
		add(title, s.texts)
	}
}

// selectResult moves the selection by delta to the nearest selectable row
// in direction dir (+1/-1).
func (a *App) selectResult(delta, dir int) {
	s := &a.search
	if len(s.results) == 0 {
		s.sel = 0
		return
	}
	i := max(0, min(s.sel+delta, len(s.results)-1))
	for i >= 0 && i < len(s.results) && s.results[i].kind == resHeader {
		i += dir
	}
	if i < 0 || i >= len(s.results) {
		// Nothing selectable in that direction; search the other way.
		i = max(0, min(s.sel+delta, len(s.results)-1))
		for i >= 0 && i < len(s.results) && s.results[i].kind == resHeader {
			i -= dir
		}
		if i < 0 || i >= len(s.results) {
			return
		}
	}
	s.sel = i
}

// nextGroup jumps to the first item of the next result group.
func (a *App) nextGroup() {
	s := &a.search
	for i := s.sel + 1; i < len(s.results); i++ {
		if s.results[i].kind == resHeader {
			s.sel = i
			a.selectResult(0, 1)
			return
		}
	}
	s.sel = 0
	a.selectResult(0, 1)
}

func (a *App) openResult(i int) {
	s := &a.search
	if i < 0 || i >= len(s.results) || s.results[i].kind == resHeader {
		return
	}
	r := s.results[i]
	a.closeSearch()
	if r.kind == resFile {
		a.pushHistory()
		a.openFile(r.file)
		return
	}
	a.jumpTo(r.file, r.line, r.col)
}

func (a *App) searchKey(ev *tcell.EventKey) {
	s := &a.search
	rows := max(1, a.geo.textH)
	changed := false
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlP, tcell.KeyCtrlC:
		a.closeSearch()
	case tcell.KeyEnter:
		a.openResult(s.sel)
	case tcell.KeyUp:
		a.selectResult(-1, -1)
	case tcell.KeyDown:
		a.selectResult(1, 1)
	case tcell.KeyPgUp:
		a.selectResult(-rows, -1)
	case tcell.KeyPgDn:
		a.selectResult(rows, 1)
	case tcell.KeyTab:
		a.nextGroup()
	case tcell.KeyCtrlQ:
		a.closeSearch()
		a.quitCmd()
	}
	if s.picker {
		return
	}
	switch ev.Key() {
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if s.cur > 0 {
			s.text = append(s.text[:s.cur-1], s.text[s.cur:]...)
			s.cur--
			changed = true
		}
	case tcell.KeyDelete:
		if s.cur < len(s.text) {
			s.text = append(s.text[:s.cur], s.text[s.cur+1:]...)
			changed = true
		}
	case tcell.KeyLeft:
		s.cur = max(0, s.cur-1)
	case tcell.KeyRight:
		s.cur = min(len(s.text), s.cur+1)
	case tcell.KeyHome, tcell.KeyCtrlA:
		s.cur = 0
	case tcell.KeyEnd, tcell.KeyCtrlE:
		s.cur = len(s.text)
	case tcell.KeyCtrlU:
		s.text, s.cur = nil, 0
		changed = true
	case tcell.KeyRune:
		a.searchInsert(string(ev.Rune()))
		return
	}
	if changed {
		a.updateSearch()
	}
}

func (a *App) searchInsert(text string) {
	s := &a.search
	if s.picker {
		return
	}
	r := []rune(strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(text))
	s.text = append(s.text[:s.cur], append(r, s.text[s.cur:]...)...)
	s.cur += len(r)
	a.updateSearch()
}

// findFold returns the rune range of the first case-insensitive occurrence of q in s.
func findFold(s, q string) [2]int {
	i := strings.Index(strings.ToLower(s), strings.ToLower(q))
	if i < 0 || len(strings.ToLower(s)) != len(s) {
		return [2]int{}
	}
	start := utf8.RuneCountInString(s[:i])
	return [2]int{start, start + utf8.RuneCountInString(q)}
}

// searchMouse handles clicks while the result list is open; it returns true when consumed.
func (a *App) searchMouse(x, y int, btn tcell.ButtonMask) bool {
	s := &a.search
	g := a.geo
	inList := x >= g.edX && y >= 1 && y < 1+a.listHeight()
	if btn&(tcell.WheelUp|tcell.WheelDown) != 0 {
		if inList {
			d := 3
			if btn&tcell.WheelUp != 0 {
				d = -3
			}
			s.scroll = max(0, min(s.scroll+d, len(s.results)-a.listHeight()))
			return true
		}
		return false
	}
	if btn&tcell.Button1 == 0 || a.mouseDown {
		return btn&tcell.Button1 != 0
	}
	a.mouseDown = true
	switch {
	case inList:
		a.openResult(s.scroll + y - 1)
	case y == 0 && x >= g.edX:
	default:
		a.closeSearch()
		a.mouseDown = false
		return false
	}
	return true
}

func (a *App) listHeight() int {
	n := len(a.search.results)
	if n == 0 {
		n = 1
	}
	return min(n, a.geo.textH+1)
}

func (a *App) drawSearchBar() {
	g := a.geo
	s := &a.search
	bar := styleDefault.Background(tcell.NewHexColor(0x2C313A)).Foreground(tcell.NewHexColor(0xD7DAE0))
	a.fill(g.edX, g.w, 0, bar)
	x := a.put(g.edX+1, 0, g.w, "⌕ ", bar.Foreground(tcell.NewHexColor(0x61AFEF)).Bold(true))
	switch {
	case s.active && s.picker:
		a.put(x, 0, g.w, s.title+"   (Enter open, Esc cancel)", bar.Bold(true))
	case s.active:
		if len(s.text) == 0 {
			a.put(x, 0, g.w, "Type a file name, class or text…   (Tab next group, Esc close)", bar.Foreground(tcell.NewHexColor(0x7F848E)))
		} else {
			a.put(x, 0, g.w, string(s.text), bar)
		}
		a.scr.ShowCursor(x+visualCol(s.text, s.cur), 0)
	default:
		a.put(x, 0, g.w, "Search files, classes and text    Ctrl+P", bar.Foreground(tcell.NewHexColor(0x7F848E)))
	}
}

var badgeColors = map[string]tcell.Color{
	"file":      tcell.NewHexColor(0x61AFEF),
	"class":     tcell.NewHexColor(0xE5C07B),
	"interface": tcell.NewHexColor(0x56B6C2),
	"trait":     tcell.NewHexColor(0xC678DD),
	"enum":      tcell.NewHexColor(0xD19A66),
	"function":  tcell.NewHexColor(0x98C379),
	"method":    tcell.NewHexColor(0x98C379),
	"const":     tcell.NewHexColor(0xD19A66),
	"property":  tcell.NewHexColor(0xE06C75),
	"variable":  tcell.NewHexColor(0xE06C75),
	"text":      tcell.NewHexColor(0x9DA5B4),
}

func (a *App) drawSearchResults() {
	g := a.geo
	s := &a.search
	h := a.listHeight()
	if s.sel < s.scroll {
		s.scroll = s.sel
	} else if s.sel >= s.scroll+h {
		s.scroll = s.sel - h + 1
	}
	s.scroll = max(0, min(s.scroll, len(s.results)-h))
	box := styleDefault.Background(tcell.NewHexColor(0x21252B)).Foreground(tcell.NewHexColor(0xD7DAE0))
	if len(s.results) == 0 {
		a.fill(g.edX, g.w, 1, box)
		msg := "Type to search"
		if len(s.text) > 0 {
			msg = "No results"
		}
		a.put(g.edX+2, 1, g.w, msg, box.Foreground(tcell.NewHexColor(0x7F848E)))
		return
	}
	for row := 0; row < h; row++ {
		i := s.scroll + row
		y := 1 + row
		if i >= len(s.results) {
			break
		}
		r := s.results[i]
		st := box
		if i == s.sel {
			st = box.Background(selBg)
		}
		a.fill(g.edX, g.w, y, st)
		if r.kind == resHeader {
			a.put(g.edX+1, y, g.w, strings.ToUpper(r.label), box.Foreground(tcell.NewHexColor(0x7F848E)).Bold(true))
			continue
		}
		x := g.edX + 2
		a.put(x, y, g.w, r.badge, st.Foreground(badgeColors[r.badge]))
		x += 10
		for k, ch := range []rune(r.label) {
			cs := st
			if k >= r.hl[0] && k < r.hl[1] {
				cs = st.Foreground(tcell.NewHexColor(0xE5C07B)).Bold(true)
			}
			x = a.put(x, y, g.w, string(ch), cs)
		}
		a.put(x+3, y, g.w, r.detail, st.Foreground(tcell.NewHexColor(0x7F848E)))
	}
}
