package ui

import (
	"os/exec"
	"runtime"
	"strings"
)

// System clipboard commands, tried in order.
var (
	copyCmds  = [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}}
	pasteCmds = [][]string{{"wl-paste", "--no-newline"}, {"xclip", "-selection", "clipboard", "-o"}, {"xsel", "--clipboard", "--output"}}
)

func init() {
	if runtime.GOOS == "darwin" {
		copyCmds = [][]string{{"pbcopy"}}
		pasteCmds = [][]string{{"pbpaste"}}
	}
}

func writeClipboard(s string) bool {
	for _, c := range copyCmds {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Stdin = strings.NewReader(s)
		if cmd.Run() == nil {
			return true
		}
	}
	return false
}

func readClipboard() (string, bool) {
	for _, c := range pasteCmds {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		if out, err := exec.Command(c[0], c[1:]...).Output(); err == nil {
			return string(out), true
		}
	}
	return "", false
}
