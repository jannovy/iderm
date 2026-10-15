package index

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Built-in PHP symbols, shared by all indexes.
var builtins struct {
	sync.RWMutex
	funcs   map[string]*Symbol
	classes map[string]*ClassInfo
}

func builtinClass(fqn string) *ClassInfo {
	builtins.RLock()
	defer builtins.RUnlock()
	return builtins.classes[strings.ToLower(fqn)]
}

func builtinFunc(name string) *Symbol {
	builtins.RLock()
	defer builtins.RUnlock()
	return builtins.funcs[strings.ToLower(name)]
}

// BuiltinsLoaded reports whether LoadBuiltins succeeded.
func BuiltinsLoaded() bool {
	builtins.RLock()
	defer builtins.RUnlock()
	return builtins.funcs != nil
}

// builtinScript dumps internal functions and classes using reflection.
const builtinScript = `<?php
error_reporting(0);
function t($t) { return $t === null ? '' : (string)$t; }
function ps($f) {
    $out = [];
    foreach ($f->getParameters() as $p) {
        $s = ($p->hasType() ? t($p->getType()) . ' ' : '') . ($p->isPassedByReference() ? '&' : '')
            . ($p->isVariadic() ? '...' : '') . '$' . $p->getName();
        if ($p->isOptional() && !$p->isVariadic()) $s .= ' = …';
        $out[] = $s;
    }
    return '(' . implode(', ', $out) . ')';
}
$r = ['f' => [], 'c' => []];
foreach (get_defined_functions()['internal'] as $n) {
    $f = new ReflectionFunction($n);
    $r['f'][] = [$f->getName(), ps($f), t($f->getReturnType())];
}
foreach (array_merge(get_declared_classes(), get_declared_interfaces(), get_declared_traits()) as $n) {
    $c = new ReflectionClass($n);
    if (!$c->isInternal()) continue;
    $kind = $c->isInterface() ? 'interface' : ($c->isEnum() ? 'enum' : 'class');
    $m = [];
    foreach ($c->getMethods() as $x) {
        if ($x->getDeclaringClass()->getName() !== $c->getName() || $x->isPrivate()) continue;
        $m[] = ['m', $x->getName(), ps($x), t($x->getReturnType()), $x->isStatic(), $x->isProtected() ? 'protected' : 'public'];
    }
    foreach ($c->getReflectionConstants() as $x) {
        if ($x->getDeclaringClass()->getName() !== $c->getName() || $x->isPrivate()) continue;
        $m[] = ['c', $x->getName(), '', '', true, 'public'];
    }
    foreach ($c->getProperties() as $x) {
        if ($x->getDeclaringClass()->getName() !== $c->getName() || $x->isPrivate()) continue;
        $m[] = ['p', $x->getName(), '', t($x->getType()), $x->isStatic(), $x->isProtected() ? 'protected' : 'public'];
    }
    $parent = $c->getParentClass();
    $r['c'][] = [$c->getName(), $kind, $parent ? $parent->getName() : '', $c->getInterfaceNames(), $m];
}
echo json_encode($r);
`

// LoadBuiltins reads PHP's internal functions and classes from the php binary.
func LoadBuiltins(php string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, php, "-d", "display_errors=0")
	cmd.Stdin = strings.NewReader(builtinScript)
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	var data struct {
		F [][]string          `json:"f"`
		C [][]json.RawMessage `json:"c"`
	}
	if err := json.Unmarshal(out, &data); err != nil {
		return err
	}
	funcs := map[string]*Symbol{}
	for _, f := range data.F {
		if len(f) < 3 {
			continue
		}
		funcs[strings.ToLower(f[0])] = &Symbol{Kind: Function, Name: f[0], FQN: f[0], Params: f[1], Type: builtinType(f[2])}
	}
	classes := map[string]*ClassInfo{}
	for _, raw := range data.C {
		if len(raw) < 5 {
			continue
		}
		var name, kind, parent string
		var ifaces []string
		var members [][]any
		json.Unmarshal(raw[0], &name)
		json.Unmarshal(raw[1], &kind)
		json.Unmarshal(raw[2], &parent)
		json.Unmarshal(raw[3], &ifaces)
		json.Unmarshal(raw[4], &members)
		k := map[string]SymKind{"interface": Interface, "enum": Enum}[kind]
		c := &ClassInfo{Symbol: &Symbol{Kind: k, Name: name, FQN: name}, Implements: ifaces}
		if parent != "" {
			c.Extends = []string{parent}
		}
		for _, m := range members {
			if len(m) < 6 {
				continue
			}
			mk, _ := m[0].(string)
			mname, _ := m[1].(string)
			params, _ := m[2].(string)
			typ, _ := m[3].(string)
			static, _ := m[4].(bool)
			vis, _ := m[5].(string)
			s := &Symbol{Name: mname, Class: name, Params: params, Type: builtinType(typ), Static: static, Visibility: vis}
			switch mk {
			case "m":
				s.Kind, s.FQN = Method, name+"::"+mname
			case "c":
				s.Kind, s.FQN = Const, name+"::"+mname
			default:
				s.Kind, s.FQN = Property, name+"::$"+mname
			}
			c.Members = append(c.Members, s)
		}
		classes[strings.ToLower(name)] = c
	}
	builtins.Lock()
	builtins.funcs, builtins.classes = funcs, classes
	builtins.Unlock()
	return nil
}

// builtinType converts a reflection type to a class name or staticType.
func builtinType(t string) string {
	for _, part := range strings.FieldsFunc(t, func(r rune) bool { return r == '|' || r == '?' }) {
		switch strings.ToLower(part) {
		case "static", "self":
			return staticType
		}
		if !scalarTypes[strings.ToLower(part)] {
			return strings.TrimPrefix(part, "\\")
		}
	}
	return ""
}
