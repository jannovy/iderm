package fileops

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCopyTree(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	write(t, filepath.Join(src, "a.txt"), "A")
	write(t, filepath.Join(src, "sub", "b.txt"), "B")
	os.Symlink("a.txt", filepath.Join(src, "link"))
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	os.Chtimes(filepath.Join(src, "a.txt"), old, old)
	dst := filepath.Join(root, "dst")
	os.Mkdir(dst, 0o755)

	plan, err := PlanInto([]string{src}, dst)
	if err != nil {
		t.Fatal(err)
	}
	var last [2]int
	err = Copy(context.Background(), plan, Overwrite, func(done, total int, _ string) { last = [2]int{done, total} })
	if err != nil {
		t.Fatal(err)
	}
	if last != [2]int{3, 3} {
		t.Fatalf("progress = %v", last)
	}
	out := filepath.Join(dst, "src")
	if read(t, filepath.Join(out, "sub", "b.txt")) != "B" {
		t.Fatal("nested file not copied")
	}
	fi, _ := os.Stat(filepath.Join(out, "a.txt"))
	if fi.Mode().Perm() != 0o640 || !fi.ModTime().Equal(old) {
		t.Fatalf("mode %v time %v", fi.Mode(), fi.ModTime())
	}
	if l, _ := os.Readlink(filepath.Join(out, "link")); l != "a.txt" {
		t.Fatalf("symlink = %q", l)
	}
	if c := plan.Conflicts(); len(c) != 1 {
		t.Fatalf("conflicts = %v", c)
	}
	// Skipping keeps the modified target.
	write(t, filepath.Join(out, "a.txt"), "changed")
	Copy(context.Background(), plan, SkipExisting, nil)
	if read(t, filepath.Join(out, "a.txt")) != "changed" {
		t.Fatal("skip overwrote the file")
	}
	Copy(context.Background(), plan, Overwrite, nil)
	if read(t, filepath.Join(out, "a.txt")) != "A" {
		t.Fatal("overwrite did not replace the file")
	}
}

func TestPlanInto(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a.txt")
	b := filepath.Join(root, "b.txt")
	write(t, a, "a")
	write(t, b, "b")
	// Single source to a non-existing path: rename.
	p, _ := PlanInto([]string{a}, filepath.Join(root, "c.txt"))
	if p.Pairs[0][1] != filepath.Join(root, "c.txt") {
		t.Fatalf("pairs = %v", p.Pairs)
	}
	// Several sources to a non-existing path: new directory.
	p, _ = PlanInto([]string{a, b}, filepath.Join(root, "new"))
	if p.Pairs[1][1] != filepath.Join(root, "new", "b.txt") {
		t.Fatalf("pairs = %v", p.Pairs)
	}
	// Several sources onto a file: error.
	if _, err := PlanInto([]string{a, b}, a); err == nil {
		t.Fatal("expected error")
	}
	// Into itself.
	dir := filepath.Join(root, "dir")
	os.Mkdir(dir, 0o755)
	if _, err := PlanInto([]string{dir}, filepath.Join(dir, "inner")); err == nil {
		t.Fatal("expected nesting error")
	}
	if _, err := PlanInto([]string{a}, root); err == nil {
		t.Fatal("expected same-path error")
	}
}

func TestMoveAndDelete(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	write(t, filepath.Join(src, "x.txt"), "new")
	dst := filepath.Join(root, "dst")
	write(t, filepath.Join(dst, "src", "keep.txt"), "keep")
	write(t, filepath.Join(dst, "src", "x.txt"), "old")

	p, _ := PlanInto([]string{src}, dst)
	if err := Move(context.Background(), p, Overwrite, nil); err != nil {
		t.Fatal(err)
	}
	if exists(src) {
		t.Fatal("source still exists")
	}
	if read(t, filepath.Join(dst, "src", "x.txt")) != "new" || read(t, filepath.Join(dst, "src", "keep.txt")) != "keep" {
		t.Fatal("directories were not merged")
	}
	if err := Delete(context.Background(), []string{filepath.Join(dst, "src")}, nil); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dst, "src")) {
		t.Fatal("not deleted")
	}
}

func TestCancel(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a", "1"), "x")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p, _ := PlanInto([]string{filepath.Join(root, "a")}, filepath.Join(root, "b"))
	if err := Copy(ctx, p, Overwrite, nil); err == nil {
		t.Fatal("expected cancellation error")
	}
}
