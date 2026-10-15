package ui

import (
	"strings"
	"testing"

	"github.com/jannovy/iderm/internal/git"
)

func TestRevertHunk(t *testing.T) {
	head := "a\nb\nc\nd\ne\n"
	cases := []string{
		"a\nB\nc\nd\ne\n",       // changed line
		"a\nb\nX\nY\nc\nd\ne\n", // added lines
		"a\nb\ne\n",             // deleted lines
		"a\nb\nc\nd\ne\nf\ng",   // added at the end
		"a\nb\nc",               // deleted at the end
		"Z\nb\nc\nd\ne\n",       // changed first line
	}
	for _, cur := range cases {
		tab := tabWith(cur, Pos{})
		tab.head, tab.hasHead = strings.Split(head, "\n"), true
		tab.updateMarks()
		if len(tab.hunks) != 1 {
			t.Fatalf("%q: hunks %+v", cur, tab.hunks)
		}
		revertHunk(tab, tab.hunks[0])
		if got := tab.buf.Text(); got != head {
			t.Errorf("%q: reverted to %q", cur, got)
		}
		tab.buf.Undo()
		if got := tab.buf.Text(); got != cur {
			t.Errorf("%q: undo gave %q", cur, got)
		}
	}
}

func TestChangeRows(t *testing.T) {
	base := strings.Split("a\nb\nc\n", "\n")
	cur := strings.Split("a\nB\nc\nd\n", "\n")
	d := &fileDiff{base: base, cur: cur, hunks: git.Hunks(base, cur)}
	var got []string
	for _, r := range buildChangeRows(d) {
		got = append(got, string("H-+i"[r.kind])+r.text)
	}
	want := "Hline 2|-b|+B|Hline 4: lines added|+d"
	if strings.Join(got, "|") != want {
		t.Fatalf("rows = %q, want %q", strings.Join(got, "|"), want)
	}
	// A new file is one block without the empty line of the final newline.
	d = &fileDiff{cur: strings.Split("x\ny\n", "\n")}
	d.hunks = git.Hunks(nil, d.cur)
	if rows := buildChangeRows(d); len(rows) != 3 || rows[0].text != "new file" || rows[2].text != "y" || d.canDiscardHunk() {
		t.Fatalf("new file rows = %+v", rows)
	}
}
