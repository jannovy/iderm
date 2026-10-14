// Package fileops implements recursive copy, move and delete with progress
// reporting and cancellation.
package fileops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Progress is called after each processed file.
type Progress func(done, total int, current string)

// Conflict policy for existing targets.
type Policy int

const (
	Overwrite Policy = iota
	SkipExisting
)

// Plan maps each source to its destination path.
type Plan struct {
	Pairs [][2]string // source, destination
}

// PlanInto computes destinations for copying or moving srcs according to dst:
// an existing directory receives the sources; otherwise a single source is
// renamed to dst and several sources go into a new directory dst.
func PlanInto(srcs []string, dst string) (Plan, error) {
	var p Plan
	fi, err := os.Stat(dst)
	isDir := err == nil && fi.IsDir()
	if !isDir && len(srcs) > 1 {
		if err == nil {
			return p, fmt.Errorf("%s is not a directory", dst)
		}
		isDir = true // will be created
	}
	for _, s := range srcs {
		d := dst
		if isDir {
			d = filepath.Join(dst, filepath.Base(s))
		}
		if err := checkNesting(s, d); err != nil {
			return p, err
		}
		p.Pairs = append(p.Pairs, [2]string{s, d})
	}
	return p, nil
}

// checkNesting refuses to copy or move a directory into itself.
func checkNesting(src, dst string) error {
	if same(src, dst) {
		return fmt.Errorf("%s: source and destination are the same", filepath.Base(src))
	}
	rel, err := filepath.Rel(src, dst)
	if err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		return fmt.Errorf("cannot copy or move %s into itself", filepath.Base(src))
	}
	return nil
}

func same(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	fa, err1 := os.Stat(a)
	fb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(fa, fb)
}

// Conflicts returns destinations that already exist.
func (p Plan) Conflicts() []string {
	var out []string
	for _, pair := range p.Pairs {
		if _, err := os.Lstat(pair[1]); err == nil {
			out = append(out, pair[1])
		}
	}
	return out
}

// countFiles returns the number of non-directory entries below the paths.
func countFiles(paths []string) int {
	n := 0
	for _, p := range paths {
		filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				n++
			}
			return nil
		})
	}
	return n
}

func sources(p Plan) []string {
	out := make([]string, len(p.Pairs))
	for i, pair := range p.Pairs {
		out[i] = pair[0]
	}
	return out
}

type counter struct {
	done, total int
	progress    Progress
}

func (c *counter) step(name string) {
	c.done++
	if c.progress != nil {
		c.progress(c.done, c.total, name)
	}
}

// Copy copies files and directories recursively, keeping permissions,
// modification times and symlinks.
func Copy(ctx context.Context, p Plan, policy Policy, progress Progress) error {
	c := &counter{total: countFiles(sources(p)), progress: progress}
	for _, pair := range p.Pairs {
		if err := copyTree(ctx, pair[0], pair[1], policy, c); err != nil {
			return err
		}
	}
	return nil
}

func copyTree(ctx context.Context, src, dst string, policy Policy, c *counter) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			if err := os.MkdirAll(target, info.Mode().Perm()|0o700); err != nil {
				return err
			}
			return nil
		case info.Mode()&os.ModeSymlink != 0:
			defer c.step(rel)
			if exists(target) {
				if policy == SkipExisting {
					return nil
				}
				if err := os.Remove(target); err != nil {
					return err
				}
			}
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			defer c.step(rel)
			if exists(target) && policy == SkipExisting {
				return nil
			}
			return copyFile(path, target, info)
		}
		c.step(rel) // sockets, devices: skipped
		return nil
	})
}

func isDir(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.IsDir()
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func copyFile(src, dst string, info fs.FileInfo) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if fi, err := os.Lstat(dst); err == nil && fi.IsDir() {
		return fmt.Errorf("%s: a directory is in the way", dst)
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	os.Chmod(dst, info.Mode().Perm())
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}

// Move renames sources to their destinations, falling back to copy and
// delete across file systems. Existing destinations are replaced or skipped.
func Move(ctx context.Context, p Plan, policy Policy, progress Progress) error {
	c := &counter{total: len(p.Pairs), progress: progress}
	for _, pair := range p.Pairs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		src, dst := pair[0], pair[1]
		if exists(dst) {
			if policy == SkipExisting {
				c.step(filepath.Base(src))
				continue
			}
			if isDir(src) && isDir(dst) {
				// Merge into the existing directory instead of replacing it.
				if err := copyTree(ctx, src, dst, Overwrite, &counter{}); err != nil {
					return err
				}
				if err := os.RemoveAll(src); err != nil {
					return err
				}
				c.step(filepath.Base(src))
				continue
			}
			if err := os.RemoveAll(dst); err != nil {
				return err
			}
		}
		err := os.Rename(src, dst)
		if errors.Is(err, syscall.EXDEV) {
			if err = copyTree(ctx, src, dst, Overwrite, &counter{}); err == nil {
				err = os.RemoveAll(src)
			}
		}
		if err != nil {
			return err
		}
		c.step(filepath.Base(src))
	}
	return nil
}

// Delete removes files and directories recursively.
func Delete(ctx context.Context, paths []string, progress Progress) error {
	c := &counter{total: len(paths), progress: progress}
	for _, p := range paths {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := os.RemoveAll(p); err != nil {
			return err
		}
		c.step(filepath.Base(p))
	}
	return nil
}
