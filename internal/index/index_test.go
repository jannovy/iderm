package index

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var project = map[string]string{
	"src/Model/Entity.php": `<?php
namespace App\Model;

abstract class Entity
{
    public const TABLE = 'x';

    public function getId(): int { return 1; }

    public function touch(): static { return $this; }
}
`,
	"src/Model/User.php": `<?php
namespace App\Model;

use App\Support\Greets;

/**
 * A user.
 */
final class User extends Entity implements \JsonSerializable
{
    use Greets;

    private ?Address $address = null;

    public function __construct(private string $name, protected readonly Address $home) {}

    public function getAddress(): ?Address { return $this->address; }

    public function jsonSerialize(): mixed { return []; }
}
`,
	"src/Model/Address.php": `<?php
namespace App\Model;

class Address
{
    public string $city = '';

    /** @return User */
    public function owner() { return new User('a', $this); }
}
`,
	"src/Support/Greets.php": `<?php
namespace App\Support;

trait Greets
{
    public function greet(string $who): string { return "Hi $who"; }
}

function helper(): void {}
`,
	"src/Service/UserService.php": `<?php
namespace App\Service;

use App\Model\User;
use App\Model\{Address, Entity as BaseEntity};
use function App\Support\helper;

class UserService
{
    public function __construct(private User $user) {}

    public function run(Address $addr): string
    {
        $u = new User('x', $addr);
        helper();
        $city = $u->getAddress()->city;
        $this->user->touch()->getAddress();
        /** @var BaseEntity $e */
        $e = load();
        $e->getId();
        $addr->owner()->greet('x');
        echo User::TABLE;
        return $u->greet($city);
    }
}
`,
}

func setup(t *testing.T) (*Index, string) {
	t.Helper()
	root := t.TempDir()
	for p, c := range project {
		full := filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(c), 0o644)
	}
	ix := New(root)
	ix.Build()
	return ix, root
}

// defAt resolves the symbol at the first occurrence of marker in file;
// the cursor is placed at the offset of "|" inside marker.
func defAt(t *testing.T, ix *Index, root, file, marker string) []Location {
	t.Helper()
	text := project[file]
	bar := strings.Index(marker, "|")
	plain := strings.Replace(marker, "|", "", 1)
	i := strings.Index(text, plain)
	if i < 0 {
		t.Fatalf("marker %q not found", marker)
	}
	off := i + bar
	line := strings.Count(text[:off], "\n")
	col := off - (strings.LastIndex(text[:off], "\n") + 1)
	res, msg := ix.Definition(filepath.Join(root, file), SplitLines(text), line, col)
	if len(res) == 0 {
		t.Fatalf("%s: no result: %s", marker, msg)
	}
	return res
}

func TestDefinition(t *testing.T) {
	ix, root := setup(t)
	cases := []struct{ marker, want string }{
		{"new Us|er('x'", `App\Model\User`},
		{"Address $ad|dr", ""}, // variable: handled separately
		{"run(Addr|ess", `App\Model\Address`},
		{"hel|per();", `App\Support\helper`},
		{"getAddress()->ci|ty", `App\Model\Address::$city`},
		{"$u->getAdd|ress()", `App\Model\User::getAddress`},
		{"touch()->getAdd|ress", `App\Model\User::getAddress`},
		{"$this->user->to|uch", `App\Model\Entity::touch`},
		{"$e->get|Id", `App\Model\Entity::getId`},
		{"owner()->gr|eet", `App\Support\Greets::greet`},
		{"User::TA|BLE", `App\Model\Entity::TABLE`},
		{"$u->gre|et($city)", `App\Support\Greets::greet`},
		{"use App\\Model\\Us|er;", `App\Model\User`},
		{"Entity as Base|Entity", `App\Model\Entity`},
	}
	for _, c := range cases {
		if c.want == "" {
			continue
		}
		res := defAt(t, ix, root, "src/Service/UserService.php", c.marker)
		if res[0].Name != c.want {
			t.Errorf("%s: got %s, want %s", c.marker, res[0].Name, c.want)
		}
	}
	// A variable jumps to its first occurrence (the parameter).
	res := defAt(t, ix, root, "src/Service/UserService.php", "$u = new User('x', $ad|dr)")
	if res[0].Line != 11 {
		t.Errorf("variable: got line %d", res[0].Line)
	}
	// Promoted property and extends in the model.
	res = defAt(t, ix, root, "src/Model/User.php", "extends Ent|ity")
	if res[0].Name != `App\Model\Entity` {
		t.Errorf("extends: %s", res[0].Name)
	}
}

func TestParseClass(t *testing.T) {
	ix, root := setup(t)
	fi := ix.files[filepath.Join(root, "src/Model/User.php")]
	if len(fi.Classes) != 1 {
		t.Fatalf("classes: %d", len(fi.Classes))
	}
	c := fi.Classes[0]
	if c.FQN != `App\Model\User` || c.Extends[0] != `App\Model\Entity` || c.Implements[0] != "JsonSerializable" || c.Traits[0] != `App\Support\Greets` {
		t.Fatalf("class = %+v", c)
	}
	types := map[string]string{}
	for _, m := range c.Members {
		types[m.Kind.String()+" "+m.Name] = m.Type
	}
	want := map[string]string{
		"property address":     `App\Model\Address`,
		"property name":        "",
		"property home":        `App\Model\Address`,
		"method getAddress":    `App\Model\Address`,
		"method __construct":   "",
		"method jsonSerialize": "",
	}
	for k, v := range want {
		got, ok := types[k]
		if !ok || got != v {
			t.Errorf("%s: got %q (present %v), want %q", k, got, ok, v)
		}
	}
}

func TestSearch(t *testing.T) {
	ix, _ := setup(t)
	if s := ix.SearchSymbols("user", 10); len(s) == 0 || s[0].FQN != `App\Model\User` {
		t.Fatalf("symbols: %+v", s)
	}
	if s := ix.SearchSymbols("usrsvc", 10); len(s) == 0 || s[0].Name != "UserService" {
		t.Fatalf("fuzzy symbols: %+v", s)
	}
	if f := ix.SearchFiles("addr", 10); len(f) == 0 || filepath.Base(f[0]) != "Address.php" {
		t.Fatalf("files: %v", f)
	}
	m := ix.SearchText(context.Background(), "getaddress", 10)
	if len(m) != 3 {
		t.Fatalf("text matches: %+v", m)
	}
}

func TestUpdateInfo(t *testing.T) {
	ix, root := setup(t)
	path := filepath.Join(root, "src/Model/Address.php")
	src := SplitLines(strings.Replace(project["src/Model/Address.php"], "class Address", "class Place", 1))
	ix.UpdateInfo(Parse(path, Tokenize(src)))
	if len(ix.classesByFQN(`App\Model\Address`)) != 0 || len(ix.classesByFQN(`App\Model\Place`)) != 1 {
		t.Fatal("update did not replace declarations")
	}
}
