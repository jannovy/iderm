package ui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// prompt is a one-line input shown in the message row.
type prompt struct {
	label    string
	text     []rune
	cur      int
	onChange func(string)
	onEnter  func(string)
	onCancel func()
	onKey    func(ev *tcell.EventKey) bool // gets keys first; true = consumed
	complete func(string) string
}

func newPrompt(label, initial string) *prompt {
	r := []rune(initial)
	return &prompt{label: label, text: r, cur: len(r)}
}

func (a *App) promptKey(ev *tcell.EventKey) {
	p := a.prompt
	if p.onKey != nil && p.onKey(ev) {
		return
	}
	changed := false
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		a.prompt = nil
		if p.onCancel != nil {
			p.onCancel()
		}
	case tcell.KeyEnter:
		a.prompt = nil
		if p.onEnter != nil {
			p.onEnter(string(p.text))
		}
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if p.cur > 0 {
			p.text = append(p.text[:p.cur-1], p.text[p.cur:]...)
			p.cur--
			changed = true
		}
	case tcell.KeyDelete:
		if p.cur < len(p.text) {
			p.text = append(p.text[:p.cur], p.text[p.cur+1:]...)
			changed = true
		}
	case tcell.KeyLeft:
		p.cur = max(0, p.cur-1)
	case tcell.KeyRight:
		p.cur = min(len(p.text), p.cur+1)
	case tcell.KeyHome, tcell.KeyCtrlA:
		p.cur = 0
	case tcell.KeyEnd, tcell.KeyCtrlE:
		p.cur = len(p.text)
	case tcell.KeyCtrlU:
		p.text, p.cur = nil, 0
		changed = true
	case tcell.KeyTab:
		if p.complete != nil {
			p.text = []rune(p.complete(string(p.text)))
			p.cur = len(p.text)
			changed = true
		}
	case tcell.KeyRune:
		p.insert(string(ev.Rune()))
		changed = true
	}
	if changed && p.onChange != nil {
		p.onChange(string(p.text))
	}
}

func (p *prompt) insert(s string) {
	s = strings.NewReplacer("\r", "", "\n", "").Replace(s)
	r := []rune(s)
	p.text = append(p.text[:p.cur], append(r, p.text[p.cur:]...)...)
	p.cur += len(r)
}

// resolvePath makes a user-entered path absolute relative to root.
func resolvePath(root, s string) string {
	if s == "~" || strings.HasPrefix(s, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			s = filepath.Join(home, s[1:])
		}
	}
	if !filepath.IsAbs(s) {
		s = filepath.Join(root, s)
	}
	return filepath.Clean(s)
}

// completePath extends s to the longest common prefix of matching entries.
func completePath(root, s string) string {
	dir, prefix := filepath.Split(s)
	entries, err := os.ReadDir(resolvePath(root, dir+"."))
	if err != nil {
		return s
	}
	var matches []os.DirEntry
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) && (!strings.HasPrefix(e.Name(), ".") || strings.HasPrefix(prefix, ".")) {
			matches = append(matches, e)
		}
	}
	if len(matches) == 0 {
		return s
	}
	common := matches[0].Name()
	for _, m := range matches[1:] {
		n := 0
		for n < len(common) && n < len(m.Name()) && common[n] == m.Name()[n] {
			n++
		}
		common = common[:n]
	}
	if len(matches) == 1 && matches[0].IsDir() {
		common += "/"
	}
	return dir + common
}
