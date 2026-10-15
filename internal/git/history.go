package git

import (
	"strconv"
	"strings"
)

const fieldSep = "\x1f"

// Commit is one entry of the history.
type Commit struct {
	Hash    string
	Short   string
	Refs    []string // e.g. "HEAD -> main", "origin/main", "tag: v1.0"
	Author  string
	Date    string
	Subject string
	Path    string // path of the followed file in this commit (file logs only)
}

// LogLine is a row of the history graph; Commit is nil for connector rows.
type LogLine struct {
	Graph  string
	Commit *Commit
}

// LogGraph returns up to n commits of ref (all refs when ref is "") with
// the ASCII graph drawn by git. A non-empty path limits the log to commits
// touching that file, following renames, without a graph.
func (r *Repo) LogGraph(ref string, n int, path string) ([]LogLine, error) {
	format := strings.Join([]string{"", "%H", "%h", "%D", "%an", "%ad", "%s"}, "%x1f")
	args := []string{"log", "--color=never", "--date=format:%Y-%m-%d %H:%M", "--format=" + format, "-n", strconv.Itoa(n)}
	if ref == "" {
		args = append(args, "--all")
	} else {
		args = append(args, ref)
	}
	if path != "" {
		args = append(args, "--follow", "--name-only") // git cannot draw a graph of a followed file
	} else {
		args = append(args, "--graph")
	}
	args = append(args, "--")
	if path != "" {
		args = append(args, path)
	}
	out, err := r.git("", args...)
	if err != nil {
		if strings.Contains(err.Error(), "does not have any commits") {
			return nil, nil
		}
		return nil, err
	}
	var lines []LogLine
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		i := strings.Index(l, fieldSep)
		if i < 0 {
			if path != "" {
				if n := len(lines); n > 0 && l != "" {
					lines[n-1].Commit.Path = l
				}
			} else if l != "" {
				lines = append(lines, LogLine{Graph: strings.TrimRight(l, " ")})
			}
			continue
		}
		f := strings.Split(l[i+1:], fieldSep)
		if len(f) < 6 {
			continue
		}
		c := &Commit{Hash: f[0], Short: f[1], Author: f[3], Date: f[4], Subject: f[5]}
		if f[2] != "" {
			c.Refs = strings.Split(f[2], ", ")
		}
		lines = append(lines, LogLine{Graph: strings.TrimRight(l[:i], " "), Commit: c})
	}
	return lines, nil
}

// ChangedFile is a file touched by a commit.
type ChangedFile struct {
	Status byte // A, M, D, R, C, T
	Path   string
	Orig   string // source of a rename or copy
}

// CommitDetail describes a commit and the files it changed.
type CommitDetail struct {
	Hash    string
	Author  string
	Email   string
	Date    string
	Parents []string
	Message string
	Files   []ChangedFile
}

// Detail loads a commit; for merges the files are compared with the first parent.
func (r *Repo) Detail(hash string) (*CommitDetail, error) {
	format := strings.Join([]string{"%H", "%an", "%ae", "%ad", "%P", "%B"}, "%x1f")
	out, err := r.git("", "show", "-s", "--date=format:%Y-%m-%d %H:%M", "--format="+format, hash)
	if err != nil {
		return nil, err
	}
	f := strings.SplitN(out, fieldSep, 6)
	if len(f) < 6 {
		return nil, nil
	}
	d := &CommitDetail{Hash: f[0], Author: f[1], Email: f[2], Date: f[3], Parents: strings.Fields(f[4]), Message: strings.TrimSpace(f[5])}
	args := []string{"diff-tree", "-r", "-M", "--no-commit-id", "--name-status", "-z"}
	if len(d.Parents) > 0 {
		args = append(args, d.Parents[0], hash)
	} else {
		args = append(args, "--root", hash)
	}
	out, err = r.git("", args...)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(out, "\x00")
	for i := 0; i+1 < len(parts); i += 2 {
		st := parts[i]
		if st == "" {
			break
		}
		cf := ChangedFile{Status: st[0], Path: parts[i+1]}
		if (cf.Status == 'R' || cf.Status == 'C') && i+2 < len(parts) {
			cf.Orig, cf.Path = parts[i+1], parts[i+2]
			i++
		}
		d.Files = append(d.Files, cf)
	}
	return d, nil
}

// CommitDiff returns the diff of one file in a commit.
func (r *Repo) CommitDiff(d *CommitDetail, f ChangedFile) (string, error) {
	paths := []string{"--", f.Path}
	if f.Orig != "" {
		paths = append(paths, f.Orig)
	}
	if len(d.Parents) == 0 {
		return r.git("", append([]string{"show", "--no-color", "--format=", "-M", d.Hash}, paths...)...)
	}
	return r.git("", append([]string{"diff", "--no-color", "-M", d.Parents[0], d.Hash}, paths...)...)
}

// RefKind classifies a reference.
type RefKind uint8

const (
	LocalBranch RefKind = iota
	RemoteBranch
	Tag
)

// Ref is a branch or tag.
type Ref struct {
	Name    string // short name, e.g. main, origin/main, v1.0
	Kind    RefKind
	Current bool
}

// Refs lists local branches, remote branches and tags.
func (r *Repo) Refs() ([]Ref, error) {
	out, err := r.git("", "for-each-ref", "--format=%(refname)%1f%(HEAD)", "refs/heads", "refs/remotes", "refs/tags")
	if err != nil {
		return nil, err
	}
	var refs []Ref
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		name, head, _ := strings.Cut(l, fieldSep)
		var ref Ref
		switch {
		case strings.HasPrefix(name, "refs/heads/"):
			ref = Ref{Name: strings.TrimPrefix(name, "refs/heads/"), Kind: LocalBranch, Current: head == "*"}
		case strings.HasPrefix(name, "refs/remotes/"):
			if strings.HasSuffix(name, "/HEAD") {
				continue
			}
			ref = Ref{Name: strings.TrimPrefix(name, "refs/remotes/"), Kind: RemoteBranch}
		case strings.HasPrefix(name, "refs/tags/"):
			ref = Ref{Name: strings.TrimPrefix(name, "refs/tags/"), Kind: Tag}
		default:
			continue
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// SwitchNewAt creates a branch at start (a commit or ref) and checks it out.
func (r *Repo) SwitchNewAt(branch, start string) error {
	_, err := r.git("", "switch", "-c", branch, start)
	return err
}
