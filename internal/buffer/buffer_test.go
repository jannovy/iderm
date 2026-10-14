package buffer

import (
	"os"
	"path/filepath"
	"testing"
)

func newWith(text string) *Buffer {
	b := New()
	b.Insert(Pos{}, text)
	b.undo, b.redo = nil, nil
	b.savedID = 0
	return b
}

func TestInsertDelete(t *testing.T) {
	b := newWith("hello\nworld")
	end := b.Insert(Pos{0, 5}, ",\nbig")
	if got := b.Text(); got != "hello,\nbig\nworld" {
		t.Fatalf("text = %q", got)
	}
	if end != (Pos{1, 3}) {
		t.Fatalf("end = %v", end)
	}
	del := b.Delete(Pos{0, 5}, Pos{2, 0})
	if del != ",\nbig\n" || b.Text() != "helloworld" {
		t.Fatalf("deleted %q, text %q", del, b.Text())
	}
}

func TestUndoRedo(t *testing.T) {
	b := newWith("abc")
	b.Insert(Pos{0, 3}, "d")
	b.Insert(Pos{0, 4}, "e")
	b.Insert(Pos{0, 5}, "f")
	if len(b.undo) != 1 {
		t.Fatalf("typing should merge into one edit, got %d", len(b.undo))
	}
	b.Delete(Pos{0, 5}, Pos{0, 6})
	b.Delete(Pos{0, 4}, Pos{0, 5})
	if b.Text() != "abcd" {
		t.Fatalf("text = %q", b.Text())
	}
	b.Undo()
	if b.Text() != "abcdef" {
		t.Fatalf("after undo of backspaces: %q", b.Text())
	}
	b.Undo()
	if b.Text() != "abc" {
		t.Fatalf("after undo of typing: %q", b.Text())
	}
	if b.Dirty() {
		t.Fatal("buffer back at saved state should not be dirty")
	}
	b.Redo()
	b.Redo()
	if b.Text() != "abcd" {
		t.Fatalf("after redo: %q", b.Text())
	}
}

func TestGroup(t *testing.T) {
	b := newWith("one two")
	b.BeginGroup()
	b.Delete(Pos{0, 4}, Pos{0, 7})
	b.Insert(Pos{0, 4}, "three")
	b.EndGroup()
	b.Undo()
	if b.Text() != "one two" {
		t.Fatalf("group undo: %q", b.Text())
	}
}

func TestDirtyAfterSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.php")
	b := newWith("x")
	b.Path = path
	b.Insert(Pos{0, 1}, "y")
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	if b.Dirty() {
		t.Fatal("dirty after save")
	}
	b.Insert(Pos{0, 2}, "z")
	if !b.Dirty() {
		t.Fatal("not dirty after typing")
	}
	b.Undo()
	if b.Dirty() {
		t.Fatal("undo back to the save point must not be dirty")
	}
	b.Undo()
	if !b.Dirty() {
		t.Fatal("undo past the save point must be dirty")
	}
}

func TestLoadCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.php")
	os.WriteFile(path, []byte("a\r\nb\r\n"), 0o644)
	b, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !b.CRLF || b.LineCount() != 3 || b.Text() != "a\nb\n" {
		t.Fatalf("crlf=%v lines=%d text=%q", b.CRLF, b.LineCount(), b.Text())
	}
	b.Save()
	data, _ := os.ReadFile(path)
	if string(data) != "a\r\nb\r\n" {
		t.Fatalf("saved %q", data)
	}
}

func TestFind(t *testing.T) {
	b := newWith("foo bar\nBar baz\nbar")
	p, ok := b.Find("bar", Pos{0, 5}, false, true, true)
	if !ok || p != (Pos{1, 0}) {
		t.Fatalf("fold forward: %v %v", p, ok)
	}
	p, ok = b.Find("bar", Pos{0, 5}, false, false, true)
	if !ok || p != (Pos{2, 0}) {
		t.Fatalf("case-sensitive: %v %v", p, ok)
	}
	p, ok = b.Find("bar", Pos{2, 3}, false, false, true)
	if !ok || p != (Pos{0, 4}) {
		t.Fatalf("wrap: %v %v", p, ok)
	}
	_, ok = b.Find("bar", Pos{2, 1}, false, false, false)
	if ok {
		t.Fatal("no wrap should not find")
	}
	p, ok = b.Find("bar", Pos{2, 0}, true, false, true)
	if !ok || p != (Pos{0, 4}) {
		t.Fatalf("backward: %v %v", p, ok)
	}
}
