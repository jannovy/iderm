package syntax

import (
	"strings"
	"testing"
)

// expect checks the kind of the first rune of each substring, in order of
// appearance on the line (a later "sub" is searched after the previous one).
type expect struct {
	line int
	sub  string
	want Kind
}

func checkKinds(t *testing.T, lang Lang, src lines, cases []expect) {
	t.Helper()
	h := NewHighlighter(lang)
	from := map[int]int{}
	for _, c := range cases {
		line := src[c.line]
		i := strings.Index(line[from[c.line]:], c.sub)
		if i < 0 {
			t.Fatalf("%q not in %q", c.sub, line)
		}
		i += from[c.line]
		from[c.line] = i + len(c.sub)
		k := h.Line(src, c.line)[len([]rune(line[:i]))]
		if k != c.want {
			t.Errorf("%s line %d %q: got %d, want %d", lang, c.line, c.sub, k, c.want)
		}
	}
}

func TestLangFor(t *testing.T) {
	cases := map[string]Lang{
		"a.php": LangPHP, "x/b.latte": LangLatte, "i.HTML": LangHTML, "s.scss": LangCSS, "app.ts": LangJS,
		"composer.json": LangJSON, "composer.lock": LangJSON, "config.neon": LangNEON, "local.neon.dist": LangNEON,
		"README.md": LangText, "": LangPHP,
	}
	for path, want := range cases {
		if got := LangFor(path); got != want {
			t.Errorf("%s: %s, want %s", path, got, want)
		}
	}
}

func TestHTML(t *testing.T) {
	src := lines{
		`<!DOCTYPE html>`,
		`<a href="/x" class='y' data-n=1>Hi &amp; bye</a> <!-- note`,
		`still --> <script type="module">`,
		`const x = foo('a', 1); // c`,
		`</script><style>`,
		`.btn, a:hover { color: #fff; margin: 0 2px !important; }`,
		`</style>`,
	}
	checkKinds(t, LangHTML, src, []expect{
		{0, "<!DOCTYPE", Tag},
		{1, "<a", Tag}, {1, "href", Attr}, {1, "=", Operator}, {1, `"/x"`, String}, {1, "'y'", String},
		{1, "Hi", Plain}, {1, "&amp;", Constant}, {1, "</a>", Tag}, {1, "<!--", Comment},
		{2, "still", Comment}, {2, "<script", Tag}, {2, "type", Attr},
		{3, "const", Keyword}, {3, "foo", Function}, {3, "'a'", String}, {3, "1", Number}, {3, "// c", Comment},
		{4, "</script>", Tag}, {4, "<style>", Tag},
		{5, ".btn", Type}, {5, "a", Tag}, {5, ":hover", Function}, {5, "color", Key}, {5, "#fff", Number},
		{5, "0", Number}, {5, "2px", Number}, {5, "!important", Keyword},
		{6, "</style>", Tag},
	})
}

func TestPHPTemplate(t *testing.T) {
	src := lines{
		`<div class="<?= $cls ?>">`,
		`<script>var url = '<?php echo $u ?>';</script>`,
		`<?php if ($a): ?><b>x</b><?php endif ?>`,
	}
	checkKinds(t, LangPHP, src, []expect{
		{0, "<div", Tag}, {0, "class", Attr}, {0, "<?=", Tag}, {0, "$cls", Variable}, {0, "?>", Tag}, {0, `">`, String},
		{1, "var", Keyword}, {1, "'", String}, {1, "<?php", Tag}, {1, "echo", Keyword}, {1, "?>", Tag}, {1, "';", String}, {1, "</script>", Tag},
		{2, "if", Keyword}, {2, "<b>", Tag}, {2, "endif", Keyword},
	})
	// The index keeps seeing inline HTML as opaque.
	k, _ := Lex([]rune(src[0]), Initial)
	if k[0] != HTML {
		t.Fatalf("Lex marks inline HTML as %d", k[0])
	}
}

func TestLatte(t *testing.T) {
	src := lines{
		`{block content}`,
		`<ul n:if="$items" class="{$cls}">`,
		`{foreach $items as $item}<li>{$item->name|upper}</li>{/foreach}`,
		`{* comment`,
		`end *} {='x'} <script>if (a) { b() }</script>`,
	}
	checkKinds(t, LangLatte, src, []expect{
		{0, "{", Tag}, {0, "block", Keyword}, {0, "content", Plain}, {0, "}", Tag},
		{1, "<ul", Tag}, {1, "n:if", Keyword}, {1, "$items", Variable}, {1, "class", Attr}, {1, "{", Tag}, {1, "$cls", Variable},
		{2, "foreach", Keyword}, {2, "$items", Variable}, {2, "as", Keyword}, {2, "<li>", Tag}, {2, "$item", Variable},
		{2, "name", Plain}, {2, "|", Operator}, {2, "upper", Function}, {2, "/", Tag}, {2, "foreach", Keyword},
		{3, "{*", Comment}, {4, "end", Comment}, {4, "'x'", String},
		{4, "if", Keyword}, {4, "{", Plain}, {4, "b", Function},
	})
}

func TestCSS(t *testing.T) {
	src := lines{
		`@media (max-width: 600px) {`,
		`  #main > .item::before { content: "x"; background: url(http://x/a.png) rgb(0 0 0 / 50%); }`,
		`}`,
		`/* multi`,
		`line */ h1 { --gap: 4px; margin: var(--gap); }`,
	}
	checkKinds(t, LangCSS, src, []expect{
		{0, "@media", Keyword}, {0, "max-width", Key}, {0, "600px", Number},
		{1, "#main", Constant}, {1, ".item", Type}, {1, "::before", Function}, {1, "content", Key}, {1, `"x"`, String},
		{1, "url", Function}, {1, "rgb", Function}, {1, "50%", Number},
		{3, "/*", Comment}, {4, "line", Comment}, {4, "h1", Tag}, {4, "--gap", Variable}, {4, "var", Function},
	})
}

func TestJS(t *testing.T) {
	src := lines{
		"import { a } from './a';",
		"export interface User { name: string; age?: number }",
		"const s = `hi ${name}",
		"done`; /* c",
		"c */ obj.method(1).prop; new Map(); let n = null;",
	}
	checkKinds(t, LangJS, src, []expect{
		{0, "import", Keyword}, {0, "from", Keyword}, {0, "'./a'", String},
		{1, "export", Keyword}, {1, "interface", Keyword}, {1, "User", Type}, {1, "string", Type},
		{2, "const", Keyword}, {2, "`hi", String}, {2, "${name}", Variable},
		{3, "done", String}, {3, "/* c", Comment},
		{4, "c */", Comment}, {4, "obj", Plain}, {4, "method", Function}, {4, "prop", Plain}, {4, "new", Keyword},
		{4, "Map", Function}, {4, "null", Constant},
	})
}

func TestJSON(t *testing.T) {
	src := lines{`{"name": "x", "n": -1.5e3, "ok": true, "list": [null]}`}
	checkKinds(t, LangJSON, src, []expect{
		{0, `"name"`, Key}, {0, `"x"`, String}, {0, `"n"`, Key}, {0, "-1.5e3", Number}, {0, "true", Constant}, {0, "null", Constant},
	})
}

func TestNEON(t *testing.T) {
	src := lines{
		`# config`,
		`parameters:`,
		`	db: %appDir%/db # path`,
		`services:`,
		`	- App\Model\UserFacade(@database, true)`,
		`	cache: Nette\Caching\Storages\FileStorage`,
		`	"quoted key": 'value'`,
	}
	checkKinds(t, LangNEON, src, []expect{
		{0, "# config", Comment}, {1, "parameters", Key},
		{2, "db", Key}, {2, "%appDir%", Variable}, {2, "# path", Comment},
		{4, "-", Operator}, {4, `App\Model\UserFacade`, Function}, {4, "@database", Variable}, {4, "true", Constant},
		{5, "cache", Key}, {5, `Nette\Caching`, Type},
		{6, `"quoted key"`, Key}, {6, "'value'", String},
	})
}
