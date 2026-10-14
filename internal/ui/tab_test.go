package ui

import (
	"testing"

	"iderm/internal/buffer"
)

func tabWith(text string, cur Pos) *Tab {
	b := buffer.New()
	b.Insert(Pos{}, text)
	t := newTab(b)
	t.place(cur)
	return t
}

func TestNewlineIndent(t *testing.T) {
	tab := tabWith("    if ($a) {}", Pos{0, 13})
	tab.newline()
	if got := tab.buf.Text(); got != "    if ($a) {\n        \n    }" {
		t.Fatalf("text = %q", got)
	}
	if tab.cur != (Pos{1, 8}) {
		t.Fatalf("cursor = %v", tab.cur)
	}
	tab.buf.Undo()
	if tab.buf.Text() != "    if ($a) {}" {
		t.Fatalf("newline should undo in one step: %q", tab.buf.Text())
	}
}

func TestBackspaceIndent(t *testing.T) {
	tab := tabWith("      x", Pos{0, 6})
	tab.backspace()
	if tab.buf.Text() != "    x" {
		t.Fatalf("text = %q", tab.buf.Text())
	}
}

func TestClosingBraceDedent(t *testing.T) {
	tab := tabWith("{\n        ", Pos{1, 8})
	tab.typeRune('}')
	if tab.buf.Text() != "{\n    }" {
		t.Fatalf("text = %q", tab.buf.Text())
	}
}

func TestToggleComment(t *testing.T) {
	tab := tabWith("  a();\n    b();", Pos{0, 0})
	tab.moveTo(Pos{1, 2}, true)
	tab.toggleComment()
	if tab.buf.Text() != "  // a();\n  //   b();" {
		t.Fatalf("comment: %q", tab.buf.Text())
	}
	tab.toggleComment()
	if tab.buf.Text() != "  a();\n    b();" {
		t.Fatalf("uncomment: %q", tab.buf.Text())
	}
}

func TestMoveAndDuplicateLines(t *testing.T) {
	tab := tabWith("a\nb\nc", Pos{0, 0})
	tab.moveLines(1)
	if tab.buf.Text() != "b\na\nc" || tab.cur.Line != 1 {
		t.Fatalf("move down: %q %v", tab.buf.Text(), tab.cur)
	}
	tab.moveLines(-1)
	if tab.buf.Text() != "a\nb\nc" || tab.cur.Line != 0 {
		t.Fatalf("move up: %q %v", tab.buf.Text(), tab.cur)
	}
	tab.duplicateLine()
	if tab.buf.Text() != "a\na\nb\nc" || tab.cur.Line != 1 {
		t.Fatalf("duplicate: %q %v", tab.buf.Text(), tab.cur)
	}
}

func TestParseLint(t *testing.T) {
	errs := parseLint("\nParse error: Unclosed '{' on line 2 in Standard input code on line 3\nErrors parsing Standard input code\n")
	if len(errs) != 1 || errs[0].line != 2 || errs[0].msg != "Parse error: Unclosed '{' on line 2" {
		t.Fatalf("errs = %+v", errs)
	}
}
