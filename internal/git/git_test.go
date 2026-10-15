package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiffMarks(t *testing.T) {
	base := strings.Split("a b c d e f", " ")
	cases := []struct {
		cur  string
		want map[int]LineMark
	}{
		{"a b c d e f", map[int]LineMark{}},
		{"a b X c d e f", map[int]LineMark{2: Added}},
		{"a B c d e f", map[int]LineMark{1: Modified}},
		{"a b d e f", map[int]LineMark{2: DeletedAbove}},
		{"a b c d e", map[int]LineMark{4: DeletedBelow}},
		{"a b c Y Z e f", map[int]LineMark{3: Modified, 4: Modified}},
		{"X a c d e f Y", map[int]LineMark{0: Added, 2: DeletedAbove, 6: Added}},
	}
	for _, c := range cases {
		cur := strings.Split(c.cur, " ")
		marks := Diff(base, cur)
		for i, m := range marks {
			if m != c.want[i] {
				t.Errorf("%q line %d: got %d, want %d", c.cur, i, m, c.want[i])
			}
		}
	}
}

func TestDiffLarge(t *testing.T) {
	var base, cur []string
	for i := range 20000 {
		base = append(base, "line"+string(rune('a'+i%26)))
	}
	cur = append(cur, base...)
	cur[5000] = "changed"
	cur = append(cur[:15000], append([]string{"new"}, cur[15000:]...)...)
	marks := Diff(base, cur)
	if marks[5000] != Modified || marks[15000] != Added {
		t.Fatalf("marks: %d %d", marks[5000], marks[15000])
	}
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func TestRepo(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "a.php"), []byte("<?php\necho 1;\n"), 0o644)
	run(t, dir, "add", ".")
	run(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init")
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)

	r, err := Open(filepath.Join(dir, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Root != dir {
		t.Fatalf("root = %s, want %s", r.Root, dir)
	}
	os.WriteFile(filepath.Join(dir, "a.php"), []byte("<?php\necho 2;\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "sub", "new.php"), []byte("x"), 0o644)
	st, err := r.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != "main" || len(st.Files) != 2 {
		t.Fatalf("status = %+v", st)
	}
	byPath := map[string]FileStatus{}
	for _, f := range st.Files {
		byPath[f.Path] = f
	}
	if !byPath["a.php"].Unstaged() || !byPath["sub/new.php"].Untracked() {
		t.Fatalf("files = %+v", st.Files)
	}
	if head, ok := r.Show("HEAD", "a.php"); !ok || head != "<?php\necho 1;\n" {
		t.Fatalf("show = %q %v", head, ok)
	}
	if d, err := r.Diff(byPath["a.php"], false); err != nil || !strings.Contains(d, "+echo 2;") {
		t.Fatalf("diff = %q %v", d, err)
	}
	if d, err := r.Diff(byPath["sub/new.php"], false); err != nil || !strings.Contains(d, "+x") {
		t.Fatalf("untracked diff = %q %v", d, err)
	}
	if err := r.Stage("a.php"); err != nil {
		t.Fatal(err)
	}
	st, _ = r.Status()
	for _, f := range st.Files {
		if f.Path == "a.php" && !f.Staged() {
			t.Fatalf("not staged: %+v", f)
		}
	}
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	hash, err := r.Commit("second")
	if err != nil || hash == "" {
		t.Fatalf("commit: %q %v", hash, err)
	}
	if err := r.Switch("feature", true); err != nil {
		t.Fatal(err)
	}
	branches, _ := r.Branches()
	if strings.Join(branches, ",") != "feature,main" {
		t.Fatalf("branches = %v", branches)
	}
	st, _ = r.Status()
	if st.Branch != "feature" {
		t.Fatalf("branch = %s", st.Branch)
	}
	if err := r.Discard(FileStatus{X: '?', Y: '?', Path: "sub/new.php"}); err != nil {
		t.Fatal(err)
	}
	if log, _ := r.Log(5); strings.Count(log, "\n") != 2 {
		t.Fatalf("log = %q", log)
	}
}

func TestParseBranch(t *testing.T) {
	st := &Status{}
	parseBranch(st, "main...origin/main [ahead 2, behind 1]")
	if st.Branch != "main" || st.Upstream != "origin/main" || st.Ahead != 2 || st.Behind != 1 {
		t.Fatalf("%+v", st)
	}
	st = &Status{}
	parseBranch(st, "No commits yet on main")
	if !st.NoCommits || st.Branch != "main" {
		t.Fatalf("%+v", st)
	}
}

func TestHistory(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
	}
	commit := func(msg string) {
		run(t, dir, "add", "-A")
		run(t, dir, "-c", "user.name=Tester", "-c", "user.email=t@t", "commit", "-q", "-m", msg)
	}
	run(t, dir, "init", "-q", "-b", "main")
	write("a.php", "a\n")
	commit("first")
	run(t, dir, "switch", "-q", "-c", "feature")
	write("b.php", "b\n")
	commit("feature work")
	run(t, dir, "switch", "-q", "main")
	run(t, dir, "mv", "a.php", "c.php")
	commit("rename a to c\n\nLonger body.")
	run(t, dir, "tag", "v1")
	run(t, dir, "-c", "user.name=Tester", "-c", "user.email=t@t", "merge", "-q", "--no-ff", "-m", "merge feature", "feature")

	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := r.LogGraph("", 50, "")
	if err != nil {
		t.Fatal(err)
	}
	var commits []*Commit
	connectors := 0
	for _, l := range lines {
		if l.Commit == nil {
			connectors++
			continue
		}
		commits = append(commits, l.Commit)
	}
	if len(commits) != 4 || connectors == 0 {
		t.Fatalf("commits=%d connectors=%d lines=%+v", len(commits), connectors, lines)
	}
	if commits[0].Subject != "merge feature" || commits[0].Refs[0] != "HEAD -> main" || !strings.HasPrefix(lines[0].Graph, "*") {
		t.Fatalf("top = %+v graph %q", commits[0], lines[0].Graph)
	}
	only, _ := r.LogGraph("feature", 50, "")
	if len(only) != 2 {
		t.Fatalf("feature log = %+v", only)
	}
	file, _ := r.LogGraph("", 50, "c.php")
	if len(file) != 2 || file[0].Commit.Subject != "rename a to c" || file[1].Commit.Subject != "first" || file[1].Commit.Path != "a.php" || file[0].Commit.Path != "c.php" {
		t.Fatalf("file log = %+v", file)
	}

	var renameHash string
	for _, c := range commits {
		if c.Subject == "rename a to c" {
			renameHash = c.Hash
			if strings.Join(c.Refs, ",") != "tag: v1" {
				t.Fatalf("refs = %v", c.Refs)
			}
		}
	}
	d, err := r.Detail(renameHash)
	if err != nil {
		t.Fatal(err)
	}
	if d.Author != "t" || d.Message != "rename a to c\n\nLonger body." || len(d.Parents) != 1 {
		t.Fatalf("detail = %+v", d)
	}
	if len(d.Files) != 1 || d.Files[0].Status != 'R' || d.Files[0].Path != "c.php" || d.Files[0].Orig != "a.php" {
		t.Fatalf("files = %+v", d.Files)
	}
	merge, _ := r.Detail(commits[0].Hash)
	if len(merge.Parents) != 2 || len(merge.Files) != 1 || merge.Files[0].Path != "b.php" {
		t.Fatalf("merge = %+v", merge)
	}
	diff, err := r.CommitDiff(merge, merge.Files[0])
	if err != nil || !strings.Contains(diff, "+b") {
		t.Fatalf("diff = %q %v", diff, err)
	}
	root, _ := r.Detail(commits[len(commits)-1].Hash)
	if len(root.Files) != 1 || root.Files[0].Status != 'A' {
		t.Fatalf("root = %+v", root)
	}
	if diff, err := r.CommitDiff(root, root.Files[0]); err != nil || !strings.Contains(diff, "+a") {
		t.Fatalf("root diff = %q %v", diff, err)
	}

	refs, _ := r.Refs()
	kinds := map[string]RefKind{}
	current := ""
	for _, ref := range refs {
		kinds[ref.Name] = ref.Kind
		if ref.Current {
			current = ref.Name
		}
	}
	if kinds["feature"] != LocalBranch || kinds["v1"] != Tag || current != "main" {
		t.Fatalf("refs = %+v", refs)
	}
	if err := r.SwitchNewAt("fix", renameHash); err != nil {
		t.Fatal(err)
	}
}

func TestDiscardFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) { os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644) }
	read := func(name string) string { b, _ := os.ReadFile(filepath.Join(dir, name)); return string(b) }
	run(t, dir, "init", "-q", "-b", "main")
	write("a.php", "a\n")
	write("b.php", "b\n")
	write("c.php", "c\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "init")
	r, _ := Open(dir)

	write("a.php", "changed\n")
	run(t, dir, "add", "a.php")
	write("a.php", "changed again\n")
	write("new.php", "n\n")
	run(t, dir, "add", "new.php")
	write("untracked.php", "u\n")
	run(t, dir, "mv", "b.php", "moved.php")
	os.Remove(filepath.Join(dir, "c.php"))
	st, _ := r.Status()
	for _, f := range st.Files {
		if err := r.DiscardFile(f); err != nil {
			t.Fatalf("%+v: %v", f, err)
		}
	}
	if st, _ = r.Status(); len(st.Files) != 0 {
		t.Fatalf("left over: %+v", st.Files)
	}
	if read("a.php") != "a\n" || read("b.php") != "b\n" || read("c.php") != "c\n" || read("new.php") != "" {
		t.Fatalf("contents: %q %q %q %q", read("a.php"), read("b.php"), read("c.php"), read("new.php"))
	}
}
