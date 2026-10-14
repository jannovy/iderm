package ui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type lintError struct {
	line int // 0-based
	msg  string
}

// lintRequest is posted when the debounce timer for a tab fires.
type lintRequest struct{ tab *Tab }

// lintResult is posted when php -l finishes.
type lintResult struct {
	tab     *Tab
	version int
	errs    []lintError
	err     error
}

var lintRe = regexp.MustCompile(`(?m)^(?:PHP )?((?:Parse|Fatal) error|Warning|Deprecated|Notice):\s+(.*) in Standard input code on line (\d+)`)

// findPHP returns the PHP binary used for syntax checks, or "".
func findPHP() string {
	if p := os.Getenv("IDERM_PHP"); p != "" {
		return p
	}
	p, _ := exec.LookPath("php")
	return p
}

// runLint checks src with php -l.
func runLint(php, src string) ([]lintError, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, php, "-n", "-d", "display_errors=1", "-d", "log_errors=0", "-d", "error_reporting=-1", "-l")
	cmd.Stdin = strings.NewReader(src)
	out, err := cmd.CombinedOutput()
	errs := parseLint(string(out))
	if err != nil && len(errs) == 0 {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, errors.New(strings.TrimSpace(string(out)))
		}
		return nil, err
	}
	return errs, nil
}

func parseLint(out string) []lintError {
	var errs []lintError
	for _, m := range lintRe.FindAllStringSubmatch(out, -1) {
		n, _ := strconv.Atoi(m[3])
		errs = append(errs, lintError{line: n - 1, msg: m[1] + ": " + m[2]})
	}
	return errs
}
