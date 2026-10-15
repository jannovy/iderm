package index

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// completeIn replaces old with new (containing "|" for the cursor) in file and completes there.
func completeIn(t *testing.T, ix *Index, root, file, old, repl string, manual bool) *Completion {
	t.Helper()
	text := project[file]
	if !strings.Contains(text, old) {
		t.Fatalf("%q not in %s", old, file)
	}
	text = strings.Replace(text, old, repl, 1)
	off := strings.Index(text, "|")
	text = text[:off] + text[off+1:]
	line := strings.Count(text[:off], "\n")
	col := len([]rune(text[strings.LastIndex(text[:off], "\n")+1 : off]))
	return ix.Complete(filepath.Join(root, file), SplitLines(text), line, col, manual)
}

func labels(c *Completion) []string {
	if c == nil {
		return nil
	}
	var out []string
	for _, it := range c.Items {
		out = append(out, it.Label)
	}
	return out
}

func has(list []string, want ...string) bool {
	set := map[string]bool{}
	for _, l := range list {
		set[l] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

func TestCompleteMembers(t *testing.T) {
	ix, root := setup(t)
	svc := "src/Service/UserService.php"

	// Inherited, trait and own public methods after ->, with the typed prefix.
	c := completeIn(t, ix, root, svc, "return $u->greet($city);", "return $u->|", false)
	got := labels(c)
	if !has(got, "greet", "getAddress", "getId", "touch", "jsonSerialize") || has(got, "address", "name") {
		t.Fatalf("members of User: %v", got)
	}
	c = completeIn(t, ix, root, svc, "return $u->greet($city);", "return $u->ge|", false)
	if c.Prefix != "ge" || c.Items[0].Label != "getAddress" && c.Items[0].Label != "getId" {
		t.Fatalf("prefix ge: %+v", labels(c))
	}
	for _, it := range c.Items {
		if it.Label == "getAddress" && (it.Detail != "(): Address" || !it.Call || it.Params) {
			t.Fatalf("getAddress item = %+v", it)
		}
	}
	// Chained call and property of the result.
	c = completeIn(t, ix, root, svc, "$city = $u->getAddress()->city;", "$city = $u->getAddress()->|", false)
	if got := labels(c); !has(got, "city", "owner") {
		t.Fatalf("members of Address: %v", got)
	}
	// Static access: constants and "class".
	c = completeIn(t, ix, root, svc, "echo User::TABLE;", "echo User::|", false)
	if got := labels(c); !has(got, "TABLE", "class") || has(got, "getId") {
		t.Fatalf("static members: %v", got)
	}
}

func TestCompleteVisibility(t *testing.T) {
	ix, root := setup(t)
	user := "src/Model/User.php"
	// Inside the class, $this-> sees private properties.
	c := completeIn(t, ix, root, user, "return $this->address;", "return $this->|", false)
	if got := labels(c); !has(got, "address", "name", "home", "getId") {
		t.Fatalf("$this members: %v", got)
	}
	// Signatures of promoted constructor parameters.
	for _, it := range c.Items {
		if it.Label == "__construct" && it.Detail != "(private string $name, protected readonly Address $home)" &&
			it.Detail != "(string $name, Address $home)" {
			t.Fatalf("constructor detail = %q", it.Detail)
		}
	}
}

func TestCompleteGlobal(t *testing.T) {
	ix, root := setup(t)
	svc := "src/Service/UserService.php"
	// Variables of the current method.
	c := completeIn(t, ix, root, svc, "helper();", "$ad|", false)
	if got := labels(c); !has(got, "$addr") || has(got, "$city") { // $city is declared later
		t.Fatalf("variables: %v", got)
	}
	c = completeIn(t, ix, root, svc, "helper();", "$|", false)
	if got := labels(c); !has(got, "$this", "$u", "$addr", "$_GET") {
		t.Fatalf("all variables: %v", got)
	}
	// Class from another namespace gets a use import.
	c = completeIn(t, ix, root, svc, "helper();", "$g = Gree|", false)
	var found *CompletionItem
	for i := range c.Items {
		if c.Items[i].Label == "Greets" {
			found = &c.Items[i]
		}
	}
	if found == nil || found.Import != `App\Support\Greets` {
		t.Fatalf("Greets item = %+v", found)
	}
	if c.ImportLine != 6 || c.ImportBlank {
		t.Fatalf("import position %d %v", c.ImportLine, c.ImportBlank)
	}
	// Already imported class: no import.
	c = completeIn(t, ix, root, svc, "helper();", "new Use|", false)
	for _, it := range c.Items {
		if it.Label == "User" && it.Import != "" {
			t.Fatalf("User should not be imported again: %+v", it)
		}
	}
	// No completion while declaring a name or inside a string.
	if c := completeIn(t, ix, root, svc, "public function run(", "public function ru|(", false); c != nil {
		t.Fatalf("declaration: %v", labels(c))
	}
	if c := completeIn(t, ix, root, svc, "helper();", "echo 'he|';", false); c != nil {
		t.Fatalf("string: %v", labels(c))
	}
	// Short prefixes need a manual trigger.
	if c := completeIn(t, ix, root, svc, "helper();", "h|", false); c != nil {
		t.Fatalf("one letter: %v", labels(c))
	}
	if c := completeIn(t, ix, root, svc, "helper();", "h|", true); !has(labels(c), "helper") {
		t.Fatalf("manual: %v", labels(c))
	}
}

func TestCompleteBuiltins(t *testing.T) {
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not installed")
	}
	if err := LoadBuiltins(php); err != nil {
		t.Fatal(err)
	}
	ix, root := setup(t)
	svc := "src/Service/UserService.php"
	c := completeIn(t, ix, root, svc, "helper();", "str_repl|", false)
	if len(c.Items) == 0 || c.Items[0].Label != "str_replace" || !strings.Contains(c.Items[0].Detail, "$search") {
		t.Fatalf("builtin function: %+v", c.Items)
	}
	c = completeIn(t, ix, root, svc, "helper();", "$d = new \\DateTime(); $d->form|", false)
	if len(c.Items) == 0 || c.Items[0].Label != "format" {
		t.Fatalf("DateTime methods: %v", labels(c))
	}
	c = completeIn(t, ix, root, svc, "helper();", "new DateTimeImm|", false)
	if len(c.Items) == 0 || c.Items[0].Label != "DateTimeImmutable" || c.Items[0].Import != "DateTimeImmutable" {
		t.Fatalf("builtin class import: %+v", c.Items)
	}
	// Built-in symbols are not jump targets.
	if res, msg := ix.Definition(filepath.Join(root, svc), SplitLines("<?php\nstrlen('x');\n"), 1, 2); len(res) != 0 || !strings.Contains(msg, "built into PHP") {
		t.Fatalf("definition of builtin: %v %q", res, msg)
	}
}

func TestCompleteAfterIncompleteLine(t *testing.T) {
	ix := New(t.TempDir())
	src := SplitLines("<?php\nfunction helper() {}\nfunction run() {\n    $d->form\n    hel\n}\n")
	c := ix.Complete("/tmp/x.php", src, 4, 7, false)
	if !has(labels(c), "helper") {
		t.Fatalf("got %v", labels(c))
	}
	// On the same line a name after a value is not completed.
	if c := ix.Complete("/tmp/x.php", SplitLines("<?php\n$a hel\n"), 1, 6, false); c != nil {
		t.Fatalf("same line: %v", labels(c))
	}
}

func TestCompleteArrInMethod(t *testing.T) {
	ix, root := setup(t)
	c := completeIn(t, ix, root, "src/Service/UserService.php", "helper();", "helper();\n        arr|", false)
	t.Logf("items: %v", labels(c))
	c = completeIn(t, ix, root, "src/Service/UserService.php", "helper();", "helper();\n        sel|", false)
	if !has(labels(c), "self") {
		t.Fatalf("keyword: %v", labels(c))
	}
}
