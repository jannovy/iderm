package syntax

import (
	"strings"
	"testing"
)

type lines []string

func (l lines) LineCount() int    { return len(l) }
func (l lines) Line(i int) []rune { return []rune(l[i]) }

// kindAt returns the kind of the first rune of the first occurrence of sub.
func kindAt(t *testing.T, line string, kinds []Kind, sub string) Kind {
	t.Helper()
	i := strings.Index(line, sub)
	if i < 0 {
		t.Fatalf("%q not in %q", sub, line)
	}
	return kinds[len([]rune(line[:i]))]
}

func TestLexBasics(t *testing.T) {
	line := `$user = new User(); if ($x->name === null) { echo strlen('a') + 0x1F; } // done`
	k, st := Lex([]rune(line), State{Mode: ModePHP})
	cases := map[string]Kind{
		"$user":   Variable,
		"new":     Keyword,
		"User":    Type,
		"if":      Keyword,
		"name":    Plain,
		"null":    Constant,
		"strlen":  Function,
		"'a'":     String,
		"0x1F":    Number,
		"===":     Operator,
		"// done": Comment,
	}
	for sub, want := range cases {
		if got := kindAt(t, line, k, sub); got != want {
			t.Errorf("%q: got %d, want %d", sub, got, want)
		}
	}
	if st.Mode != ModePHP {
		t.Errorf("mode = %d", st.Mode)
	}
}

func TestLexHTMLAndTags(t *testing.T) {
	line := `<p><?= $title ?></p>`
	k, st := Lex([]rune(line), Initial)
	if kindAt(t, line, k, "<p>") != HTML || kindAt(t, line, k, "<?=") != Tag ||
		kindAt(t, line, k, "$title") != Variable || kindAt(t, line, k, "?>") != Tag ||
		kindAt(t, line, k, "</p>") != HTML {
		t.Fatalf("kinds = %v", k)
	}
	if st.Mode != ModeHTML {
		t.Fatalf("mode = %d", st.Mode)
	}
}

func TestLexMultiline(t *testing.T) {
	src := lines{
		"<?php",
		"/* start",
		"   end */ $a = \"x $b",
		"y\";",
		"$h = <<<EOT",
		"Hello $name",
		"  EOT;",
		"$n = <<<'RAW'",
		"$notvar",
		"RAW;",
		"return 1;",
	}
	h := NewHighlighter(LangPHP)
	check := func(li int, sub string, want Kind) {
		t.Helper()
		if got := kindAt(t, src[li], h.Line(src, li), sub); got != want {
			t.Errorf("line %d %q: got %d, want %d", li, sub, got, want)
		}
	}
	check(1, "start", Comment)
	check(2, "end", Comment)
	check(2, "$a", Variable)
	check(2, "x", String)
	check(2, "$b", Variable)
	check(3, "y", String)
	check(5, "Hello", String)
	check(5, "$name", Variable)
	check(6, ";", Plain)
	check(8, "$notvar", String)
	check(10, "return", Keyword)
}

func TestHighlighterInvalidate(t *testing.T) {
	src := lines{"<?php", "$a = 1;", "$b = 2;"}
	h := NewHighlighter(LangPHP)
	if kindAt(t, src[2], h.Line(src, 2), "$b") != Variable {
		t.Fatal("expected variable")
	}
	src[1] = "/* $a = 1;"
	h.Invalidate(1)
	if kindAt(t, src[2], h.Line(src, 2), "$b") != Comment {
		t.Fatal("expected comment after invalidation")
	}
}
