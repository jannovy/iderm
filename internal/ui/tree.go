package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var hiddenNames = map[string]bool{".git": true, ".svn": true, ".hg": true, ".DS_Store": true}

type node struct {
	name, path string
	dir        bool
	open       bool
	loaded     bool
	depth      int
	parent     *node
	children   []*node
}

// fileTree is the project sidebar.
type fileTree struct {
	root   *node
	rows   []*node // visible nodes in display order
	sel    int
	scroll int
	follow bool
}

func newTree(dir string) *fileTree {
	r := &node{name: filepath.Base(dir), path: dir, dir: true, open: true, depth: -1}
	t := &fileTree{root: r, follow: true}
	t.load(r)
	t.flatten()
	return t
}

func (t *fileTree) load(n *node) {
	entries, err := os.ReadDir(n.path)
	if err != nil {
		n.loaded = true
		n.children = nil
		return
	}
	old := map[string]*node{}
	for _, c := range n.children {
		old[c.name] = c
	}
	n.children = n.children[:0]
	for _, e := range entries {
		name := e.Name()
		if hiddenNames[name] {
			continue
		}
		path := filepath.Join(n.path, name)
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if fi, err := os.Stat(path); err == nil {
				isDir = fi.IsDir()
			}
		}
		c := old[name]
		if c == nil || c.dir != isDir {
			c = &node{name: name, path: path, dir: isDir}
		}
		c.parent, c.depth = n, n.depth+1
		n.children = append(n.children, c)
	}
	sort.Slice(n.children, func(i, j int) bool {
		a, b := n.children[i], n.children[j]
		if a.dir != b.dir {
			return a.dir
		}
		return strings.ToLower(a.name) < strings.ToLower(b.name)
	})
	n.loaded = true
}

func (t *fileTree) flatten() {
	t.rows = t.rows[:0]
	var walk func(n *node)
	walk = func(n *node) {
		for _, c := range n.children {
			t.rows = append(t.rows, c)
			if c.dir && c.open {
				walk(c)
			}
		}
	}
	walk(t.root)
	t.sel = max(0, min(t.sel, len(t.rows)-1))
}

func (t *fileTree) selected() *node {
	if t.sel >= 0 && t.sel < len(t.rows) {
		return t.rows[t.sel]
	}
	return nil
}

func (t *fileTree) selectNode(n *node) {
	for i, r := range t.rows {
		if r == n {
			t.sel = i
			t.follow = true
			return
		}
	}
}

func (t *fileTree) toggle(n *node) {
	if !n.loaded {
		t.load(n)
	}
	n.open = !n.open
	t.flatten()
	t.selectNode(n)
}

// reload re-reads all expanded directories.
func (t *fileTree) reload() {
	sel := t.selected()
	var walk func(n *node)
	walk = func(n *node) {
		t.load(n)
		for _, c := range n.children {
			if c.dir && c.loaded {
				walk(c)
			}
		}
	}
	walk(t.root)
	t.flatten()
	if sel != nil {
		t.selectNode(sel)
	}
}

// reveal expands the directories leading to path and selects it.
func (t *fileTree) reveal(path string) bool {
	rel, err := filepath.Rel(t.root.path, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return false
	}
	n := t.root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if !n.loaded {
			t.load(n)
		}
		n.open = true
		var next *node
		for _, c := range n.children {
			if c.name == part {
				next = c
				break
			}
		}
		if next == nil {
			t.flatten()
			return false
		}
		n = next
	}
	t.flatten()
	t.selectNode(n)
	return true
}

// jumpTo selects the next entry whose name starts with r.
func (t *fileTree) jumpTo(r rune) {
	p := strings.ToLower(string(r))
	if len(t.rows) == 0 {
		return
	}
	for k := 1; k <= len(t.rows); k++ {
		i := (t.sel + k) % len(t.rows)
		if strings.HasPrefix(strings.ToLower(t.rows[i].name), p) {
			t.sel = i
			t.follow = true
			return
		}
	}
}
