// Package git runs git commands for the editor and computes line diffs.
package git

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// Repo is a git working tree.
type Repo struct {
	Root string // working tree root, spelled like the directory passed to Open
}

// Open finds the repository containing dir; it fails when dir is not in a repository.
func Open(dir string) (*Repo, error) {
	r := &Repo{Root: dir}
	prefix, err := r.git("", "rev-parse", "--show-prefix")
	if err != nil {
		return nil, err
	}
	root := dir
	for _, part := range strings.Split(strings.Trim(strings.TrimSpace(prefix), "/"), "/") {
		if part != "" {
			root = filepath.Dir(root)
		}
	}
	r.Root = root
	return r, nil
}

// Rel returns path relative to the repository root with forward slashes.
func (r *Repo) Rel(path string) (string, bool) {
	rel, err := filepath.Rel(r.Root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// Abs converts a repository-relative path to an absolute one.
func (r *Repo) Abs(rel string) string { return filepath.Join(r.Root, filepath.FromSlash(rel)) }

// git runs a git command in the repository root. The process gets its own
// session so that it can never prompt on the editor's terminal.
func (r *Repo) git(stdin string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Root
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), errors.New(firstLines(msg, 3))
	}
	return stdout.String(), nil
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " ")
}

// FileStatus is one entry of git status.
type FileStatus struct {
	Path string // relative to the repository root
	Orig string // original path of a rename
	X, Y byte   // index and work tree status as in git status --porcelain
}

// Conflicted reports an unmerged path.
func (f FileStatus) Conflicted() bool {
	switch string([]byte{f.X, f.Y}) {
	case "DD", "AU", "UD", "UA", "DU", "AA", "UU":
		return true
	}
	return false
}

// Untracked reports a file unknown to git.
func (f FileStatus) Untracked() bool { return f.X == '?' }

// Staged reports changes in the index.
func (f FileStatus) Staged() bool { return f.X != ' ' && f.X != '?' && !f.Conflicted() }

// Unstaged reports changes in the work tree that are not staged.
func (f FileStatus) Unstaged() bool { return f.Y != ' ' && f.Y != '?' && !f.Conflicted() }

// Status is the state of the work tree.
type Status struct {
	Branch    string
	Upstream  string
	Ahead     int
	Behind    int
	Detached  bool
	NoCommits bool
	Files     []FileStatus
}

var aheadBehind = regexp.MustCompile(`(ahead|behind) (\d+)`)

// Status runs git status.
func (r *Repo) Status() (*Status, error) {
	out, err := r.git("", "status", "--porcelain=v1", "-z", "-b", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	st := &Status{}
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if strings.HasPrefix(e, "## ") {
			parseBranch(st, e[3:])
			continue
		}
		if len(e) < 4 {
			continue
		}
		f := FileStatus{X: e[0], Y: e[1], Path: e[3:]}
		if (f.X == 'R' || f.X == 'C') && i+1 < len(entries) {
			i++
			f.Orig = entries[i]
		}
		st.Files = append(st.Files, f)
	}
	return st, nil
}

func parseBranch(st *Status, s string) {
	switch {
	case strings.HasPrefix(s, "No commits yet on "):
		st.Branch, st.NoCommits = strings.TrimPrefix(s, "No commits yet on "), true
		return
	case strings.HasPrefix(s, "HEAD (no branch)"):
		st.Branch, st.Detached = "HEAD", true
		return
	}
	info := ""
	if i := strings.Index(s, " ["); i >= 0 {
		s, info = s[:i], s[i:]
	}
	if i := strings.Index(s, "..."); i >= 0 {
		st.Branch, st.Upstream = s[:i], s[i+3:]
	} else {
		st.Branch = s
	}
	for _, m := range aheadBehind.FindAllStringSubmatch(info, -1) {
		n, _ := strconv.Atoi(m[2])
		if m[1] == "ahead" {
			st.Ahead = n
		} else {
			st.Behind = n
		}
	}
}

// Show returns the content of a file at a revision (e.g. "HEAD"); ok is
// false when the file does not exist there.
func (r *Repo) Show(rev, rel string) (string, bool) {
	out, err := r.git("", "show", rev+":"+rel)
	if err != nil {
		return "", false
	}
	return out, true
}

// Stage adds paths to the index (also records deletions and resolves conflicts).
func (r *Repo) Stage(rels ...string) error {
	_, err := r.git("", append([]string{"add", "-A", "--"}, rels...)...)
	return err
}

// StageAll stages every change in the work tree.
func (r *Repo) StageAll() error {
	_, err := r.git("", "add", "-A")
	return err
}

// Unstage removes paths from the index, keeping the work tree.
func (r *Repo) Unstage(rels ...string) error {
	if _, err := r.git("", append([]string{"restore", "--staged", "--"}, rels...)...); err != nil {
		// A repository without commits has nothing to restore from.
		_, err2 := r.git("", append([]string{"rm", "--cached", "-q", "-r", "--"}, rels...)...)
		if err2 != nil {
			return err
		}
	}
	return nil
}

// Discard reverts unstaged changes of a tracked file or deletes an untracked one.
func (r *Repo) Discard(f FileStatus) error {
	if f.Untracked() {
		return os.Remove(r.Abs(f.Path))
	}
	_, err := r.git("", "restore", "--", f.Path)
	return err
}

// DiscardFile resets a file to its HEAD version in both the index and the
// work tree; files not in HEAD are deleted.
func (r *Repo) DiscardFile(f FileStatus) error {
	if f.Untracked() {
		return os.Remove(r.Abs(f.Path))
	}
	if f.X == 'A' || f.X == 'R' || f.X == 'C' {
		if _, err := r.git("", "rm", "-q", "-f", "--", f.Path); err != nil {
			return err
		}
		if f.X != 'R' {
			return nil
		}
		_, err := r.git("", "restore", "--source=HEAD", "--staged", "--worktree", "--", f.Orig)
		return err
	}
	_, err := r.git("", "restore", "--source=HEAD", "--staged", "--worktree", "--", f.Path)
	return err
}

// Commit commits the index with message.
func (r *Repo) Commit(message string) (string, error) {
	if _, err := r.git(message, "commit", "-q", "-F", "-"); err != nil {
		return "", err
	}
	hash, _ := r.git("", "rev-parse", "--short", "HEAD")
	return strings.TrimSpace(hash), nil
}

// Push pushes the current branch; a branch without upstream gets one on origin.
func (r *Repo) Push(st *Status) (string, error) {
	args := []string{"push"}
	if st.Upstream == "" && !st.Detached {
		args = append(args, "-u", "origin", st.Branch)
	}
	if _, err := r.git("", args...); err != nil {
		return "", err
	}
	return "Pushed " + st.Branch, nil
}

// Pull fetches and fast-forwards (or rebases/merges according to the user's config).
func (r *Repo) Pull() (string, error) {
	out, err := r.git("", "pull")
	if err != nil {
		return "", err
	}
	return firstLines(strings.TrimSpace(out), 1), nil
}

// Branches lists local branches.
func (r *Repo) Branches() ([]string, error) {
	out, err := r.git("", "branch", "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// Switch checks out a branch, creating it when create is set.
func (r *Repo) Switch(branch string, create bool) error {
	args := []string{"switch", branch}
	if create {
		args = []string{"switch", "-c", branch}
	}
	_, err := r.git("", args...)
	return err
}

// Diff returns the unified diff of a file; staged compares the index with HEAD.
func (r *Repo) Diff(f FileStatus, staged bool) (string, error) {
	if f.Untracked() {
		out, err := r.git("", "diff", "--no-color", "--no-index", "--", os.DevNull, f.Path)
		if out != "" {
			return out, nil // exit code 1 means "differences found"
		}
		return "", err
	}
	args := []string{"diff", "--no-color"}
	if staged {
		args = append(args, "--cached")
	}
	return r.git("", append(args, "--", f.Path)...)
}

// Log returns recent commits, one per line.
func (r *Repo) Log(n int) (string, error) {
	return r.git("", "log", "-n", strconv.Itoa(n), "--date=short", "--format=%h  %ad  %<(18,trunc)%an  %s")
}

// Init creates a new repository in dir.
func Init(dir string) error {
	_, err := (&Repo{Root: dir}).git("", "init", "-q")
	return err
}
