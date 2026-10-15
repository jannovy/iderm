package index

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// skipDirs are never walked.
var skipDirs = map[string]bool{".git": true, ".svn": true, ".hg": true, "node_modules": true, ".idea": true, ".vscode": true}

// IsPHP reports whether path should be indexed for symbols.
func IsPHP(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php", ".inc", ".phtml", ".module":
		return true
	}
	return false
}

// Index is a thread-safe symbol index of a project.
type Index struct {
	Root string

	mu        sync.RWMutex
	files     map[string]*FileInfo
	classes   map[string][]*ClassInfo // lower FQN
	functions map[string][]*Symbol    // lower FQN
	members   map[string][]*Symbol    // lower name
	paths     []string                // all files of the project
	ready     bool
}

// New creates an empty index for root.
func New(root string) *Index {
	return &Index{
		Root:      root,
		files:     map[string]*FileInfo{},
		classes:   map[string][]*ClassInfo{},
		functions: map[string][]*Symbol{},
		members:   map[string][]*Symbol{},
	}
}

// Ready reports whether the initial build has finished.
func (ix *Index) Ready() bool {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.ready
}

// Stats returns the number of indexed files and classes.
func (ix *Index) Stats() (files, classes int) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.files), len(ix.classes)
}

// Build walks the project and indexes all PHP files.
func (ix *Index) Build() {
	var paths, php []string
	filepath.WalkDir(ix.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != ix.Root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		paths = append(paths, p)
		if IsPHP(p) {
			php = append(php, p)
		}
		return nil
	})

	jobs := make(chan string)
	results := make(chan *FileInfo)
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				if fi := parseFile(p); fi != nil {
					results <- fi
				}
			}
		}()
	}
	go func() {
		for _, p := range php {
			jobs <- p
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	var infos []*FileInfo
	for fi := range results {
		infos = append(infos, fi)
	}

	ix.mu.Lock()
	defer ix.mu.Unlock()
	for _, fi := range infos {
		ix.put(fi)
	}
	sort.Strings(paths)
	ix.paths = paths
	ix.ready = true
}

func parseFile(path string) *FileInfo {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 4<<20 {
		return nil
	}
	return Parse(path, Tokenize(SplitLines(string(data))))
}

// UpdateFile re-reads and re-indexes a file from disk.
func (ix *Index) UpdateFile(path string) {
	fi := parseFile(path)
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.remove(path)
	if fi != nil {
		ix.put(fi)
	}
	if i := sort.SearchStrings(ix.paths, path); i >= len(ix.paths) || ix.paths[i] != path {
		// Copy so that slices returned by Paths stay valid.
		paths := append(append(make([]string, 0, len(ix.paths)+1), ix.paths...), path)
		sort.Strings(paths)
		ix.paths = paths
	}
}

// UpdateInfo replaces the declarations of a file (e.g. from an unsaved buffer).
func (ix *Index) UpdateInfo(fi *FileInfo) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.remove(fi.Path)
	ix.put(fi)
}

func (ix *Index) put(fi *FileInfo) {
	ix.files[fi.Path] = fi
	for _, c := range fi.Classes {
		k := strings.ToLower(c.FQN)
		ix.classes[k] = append(ix.classes[k], c)
		for _, m := range c.Members {
			mk := strings.ToLower(m.Name)
			ix.members[mk] = append(ix.members[mk], m)
		}
	}
	for _, fn := range fi.Functions {
		k := strings.ToLower(fn.FQN)
		ix.functions[k] = append(ix.functions[k], fn)
	}
}

func (ix *Index) remove(path string) {
	old := ix.files[path]
	if old == nil {
		return
	}
	delete(ix.files, path)
	for _, c := range old.Classes {
		k := strings.ToLower(c.FQN)
		ix.classes[k] = filterFile(ix.classes[k], path, func(c *ClassInfo) string { return c.File })
		if len(ix.classes[k]) == 0 {
			delete(ix.classes, k)
		}
		for _, m := range c.Members {
			mk := strings.ToLower(m.Name)
			ix.members[mk] = filterFile(ix.members[mk], path, func(s *Symbol) string { return s.File })
		}
	}
	for _, fn := range old.Functions {
		k := strings.ToLower(fn.FQN)
		ix.functions[k] = filterFile(ix.functions[k], path, func(s *Symbol) string { return s.File })
	}
}

func filterFile[T any](list []T, path string, file func(T) string) []T {
	out := list[:0]
	for _, x := range list {
		if file(x) != path {
			out = append(out, x)
		}
	}
	return out
}

// Paths returns all project files.
func (ix *Index) Paths() []string {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.paths
}

// InVendor reports whether path lies in a vendor directory of the project.
func (ix *Index) InVendor(path string) bool {
	rel, err := filepath.Rel(ix.Root, path)
	return err == nil && (strings.HasPrefix(rel, "vendor"+string(filepath.Separator)) || strings.Contains(rel, string(filepath.Separator)+"vendor"+string(filepath.Separator)))
}

// score rates how well name matches the lower-case query q; 0 means no match.
func score(name, full, q string) int {
	ln, lf := strings.ToLower(name), strings.ToLower(full)
	switch {
	case ln == q:
		return 1000
	case strings.HasPrefix(ln, q):
		return 800 - len(ln)
	case strings.Contains(ln, q):
		return 600 - len(ln)
	case strings.Contains(lf, q):
		return 400 - len(lf)/4
	case subsequence(ln, q):
		return 200 - len(ln)
	case subsequence(lf, q):
		return 100 - len(lf)/4
	}
	return 0
}

func subsequence(s, q string) bool {
	i := 0
	for _, r := range s {
		if i < len(q) && byte(r) == q[i] {
			i++
		}
	}
	return i == len(q)
}

type scored[T any] struct {
	item  T
	score int
}

func top[T any](items []scored[T], limit int) []T {
	sort.SliceStable(items, func(i, j int) bool { return items[i].score > items[j].score })
	out := make([]T, 0, min(limit, len(items)))
	for i := 0; i < len(items) && i < limit; i++ {
		out = append(out, items[i].item)
	}
	return out
}

// SearchFiles returns project files (outside vendor) matching the query.
func (ix *Index) SearchFiles(q string, limit int) []string {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil
	}
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	var res []scored[string]
	for _, p := range ix.paths {
		if ix.InVendor(p) {
			continue
		}
		rel, _ := filepath.Rel(ix.Root, p)
		if s := score(filepath.Base(p), rel, q); s > 0 {
			res = append(res, scored[string]{p, s})
		}
	}
	return top(res, limit)
}

// SearchSymbols returns classes and functions matching the query; project code ranks first.
func (ix *Index) SearchSymbols(q string, limit int) []*Symbol {
	q = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(q), "\\"))
	if q == "" {
		return nil
	}
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	var res []scored[*Symbol]
	add := func(s *Symbol) {
		if sc := score(s.Name, s.FQN, q); sc > 0 {
			if ix.InVendor(s.File) {
				sc -= 500
			}
			res = append(res, scored[*Symbol]{s, sc})
		}
	}
	for _, list := range ix.classes {
		for _, c := range list {
			add(c.Symbol)
		}
	}
	for _, list := range ix.functions {
		for _, f := range list {
			add(f)
		}
	}
	return top(res, limit)
}

func (ix *Index) classesByFQN(fqn string) []*ClassInfo {
	if c := ix.classes[strings.ToLower(fqn)]; len(c) > 0 {
		return c
	}
	if c := builtinClass(fqn); c != nil {
		return []*ClassInfo{c}
	}
	return nil
}

// findMember looks up a member in a class and its traits, parents and interfaces.
func (ix *Index) findMember(class, name string, kind SymKind) []*Symbol {
	seen := map[string]bool{}
	queue := []string{class}
	for len(queue) > 0 && len(seen) < 64 {
		c := queue[0]
		queue = queue[1:]
		if c == "" || seen[strings.ToLower(c)] {
			continue
		}
		seen[strings.ToLower(c)] = true
		var found []*Symbol
		for _, ci := range ix.classesByFQN(c) {
			for _, m := range ci.Members {
				if m.Kind == kind && (m.Name == name || kind == Method && strings.EqualFold(m.Name, name)) {
					found = append(found, m)
				}
			}
		}
		if len(found) > 0 {
			return found
		}
		for _, ci := range ix.classesByFQN(c) {
			queue = append(queue, ci.Traits...)
			queue = append(queue, ci.Extends...)
			queue = append(queue, ci.Implements...)
		}
	}
	return nil
}

func (ix *Index) membersNamed(name string, kind SymKind) []*Symbol {
	var out []*Symbol
	for _, m := range ix.members[strings.ToLower(name)] {
		if m.Kind == kind && (kind == Method || m.Name == name) {
			out = append(out, m)
		}
	}
	return out
}

func (ix *Index) lookupFunction(f *FileInfo, name string) []*Symbol {
	if strings.HasPrefix(name, "\\") {
		if s := ix.functions[strings.ToLower(name[1:])]; len(s) > 0 {
			return s
		}
		if b := builtinFunc(name[1:]); b != nil {
			return []*Symbol{b}
		}
		return nil
	}
	lower := strings.ToLower(name)
	if fq, ok := f.FuncUses[lower]; ok {
		return ix.functions[strings.ToLower(fq)]
	}
	if s := ix.functions[strings.ToLower(join(f.Namespace, name))]; len(s) > 0 {
		return s
	}
	if s := ix.functions[lower]; len(s) > 0 {
		return s
	}
	if b := builtinFunc(name); b != nil {
		return []*Symbol{b}
	}
	return nil
}
