// Package vcs reads a git repository through the git command. It never writes
// to the work tree, the index, or a branch of the repository.
package vcs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	maxDiffBytes   = 64 << 20
	maxOutputBytes = 64 << 20
	maxStderrBytes = 4 << 10
)

// Commit is one commit of a log.
type Commit struct {
	ID, Subject, Body, Author string
	At                        time.Time
	Parents                   []string
	Files                     []string
}

// FileStat is one file of a diff.
type FileStat struct {
	Path, OldPath, Status string
	Added, Deleted        int
	Binary                bool
}

// Git reads one repository.
type Git struct {
	dir             string
	archiveFileCap  int64
	archiveTotalCap int64
}

// Open returns a reader for the work tree that holds dir.
func Open(dir string) (*Git, error) {
	if dir == "" {
		return nil, errors.New("open repository: directory is empty")
	}
	g := &Git{dir: dir, archiveFileCap: 5 << 20, archiveTotalCap: 500 << 20}
	out, err := g.run(context.Background(), maxOutputBytes, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		return nil, fmt.Errorf("open repository: %s is not inside a git work tree", dir)
	}
	return g, nil
}

// RemoteURL returns the URL of a configured remote.
func (g *Git) RemoteURL(ctx context.Context, remote string) (string, error) {
	if err := checkName("remote", remote); err != nil {
		return "", err
	}
	out, err := g.run(ctx, maxOutputBytes, "remote", "get-url", "--", remote)
	if err != nil {
		return "", fmt.Errorf("remote %q: %w", remote, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// TopLevel is the root of the work tree that holds the directory.
func (g *Git) TopLevel(ctx context.Context) (string, error) {
	out, err := g.run(ctx, maxOutputBytes, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("find the root of the work tree: %w", err)
	}
	top := strings.TrimRight(string(out), "\r\n")
	if top == "" {
		return "", errors.New("find the root of the work tree: git named no directory")
	}
	return top, nil
}

// Fetch updates the remote-tracking references of the named branches, or of
// every branch when none is named. A name that the remote has no branch of, such
// as a tag, a commit ID, or "origin/x", is left for Resolve, unless this clone
// tracks a branch of that name: then the branch was deleted on the remote, and
// Resolve would quietly use its last fetched commit. Nothing else in the
// repository changes.
//
// The fetch uses the transport, credential helper, and URL rewrites of the user's
// own git configuration on purpose: that is how the user reaches the remote, and
// .git/config belongs to the user, not to the content of the repository under test.
func (g *Git) Fetch(ctx context.Context, remote string, names ...string) error {
	for _, n := range names {
		if err := checkBranch(n); err != nil {
			return err
		}
	}
	if _, err := g.RemoteURL(ctx, remote); err != nil {
		return err
	}
	args := []string{"fetch", "--quiet", "--no-tags", "--no-recurse-submodules", "--no-auto-gc", "--no-write-fetch-head", "--", remote}
	if len(names) > 0 {
		branches, err := g.remoteBranches(ctx, remote, names)
		if err != nil {
			return err
		}
		for _, n := range names {
			if slices.Contains(branches, n) {
				continue
			}
			tracked, err := g.hasRef(ctx, "refs/remotes/"+remote+"/"+n)
			if err != nil {
				return err
			}
			if tracked {
				return &BranchGoneError{Branch: n, Remote: remote}
			}
		}
		if len(branches) == 0 {
			return nil
		}
		for _, b := range branches {
			args = append(args, "+refs/heads/"+b+":refs/remotes/"+remote+"/"+b)
		}
	}
	if _, err := g.run(ctx, maxOutputBytes, args...); err != nil {
		return fmt.Errorf("fetch %s: %w", remote, err)
	}
	return nil
}

// remoteBranches returns the names, in the given order, that are branches of the remote.
// ls-remote matches a pattern against the tail of a reference, so only an exact name counts.
func (g *Git) remoteBranches(ctx context.Context, remote string, names []string) ([]string, error) {
	args := []string{"ls-remote", "--heads", remote, "--"}
	for _, n := range names {
		args = append(args, "refs/heads/"+n)
	}
	out, err := g.run(ctx, maxOutputBytes, args...)
	if err != nil {
		return nil, fmt.Errorf("list the branches of %s: %w", remote, err)
	}
	found := map[string]bool{}
	for line := range strings.Lines(string(out)) {
		_, ref, ok := strings.Cut(strings.TrimRight(line, "\n"), "\t")
		if ok {
			found[ref] = true
		}
	}
	var branches []string
	for _, n := range names {
		if found["refs/heads/"+n] && !slices.Contains(branches, n) {
			branches = append(branches, n)
		}
	}
	return branches, nil
}

// BranchGoneError is a branch that this clone tracks and the remote no longer has.
type BranchGoneError struct{ Branch, Remote string }

func (e *BranchGoneError) Error() string {
	return fmt.Sprintf("branch %s no longer exists on %s; pass --no-fetch to use the last fetched commit", e.Branch, e.Remote)
}

func (g *Git) hasRef(ctx context.Context, ref string) (bool, error) {
	_, err := g.run(ctx, maxOutputBytes, "rev-parse", "--verify", "--quiet", "--end-of-options", ref)
	if err == nil {
		return true, nil
	}
	var ge *gitError
	if errors.As(err, &ge) && ge.code == 1 {
		return false, nil
	}
	return false, fmt.Errorf("look up %s: %w", ref, err)
}

// Resolve returns the commit that refs/remotes/<remote>/<name> names, or else
// the one that name itself names: a local branch, a tag, or a commit.
func (g *Git) Resolve(ctx context.Context, remote, name string) (string, error) {
	if err := checkName("name", name); err != nil {
		return "", err
	}
	candidates := []string{name}
	if remote != "" {
		if err := checkName("remote", remote); err != nil {
			return "", err
		}
		candidates = []string{"refs/remotes/" + remote + "/" + name, name}
	}
	for _, c := range candidates {
		out, err := g.run(ctx, maxOutputBytes, "rev-parse", "--verify", "--quiet", "--end-of-options", c+"^{commit}")
		if err == nil {
			return strings.TrimSpace(string(out)), nil
		}
		var ge *gitError
		if !errors.As(err, &ge) || ge.code != 1 {
			return "", fmt.Errorf("resolve %q: %w", name, err)
		}
	}
	return "", fmt.Errorf("cannot resolve %q: tried %s", name, strings.Join(candidates, " and "))
}

// MergeBase returns the best common ancestor of two commits.
func (g *Git) MergeBase(ctx context.Context, a, b string) (string, error) {
	if err := checkName("commit", a); err != nil {
		return "", err
	}
	if err := checkName("commit", b); err != nil {
		return "", err
	}
	out, err := g.run(ctx, maxOutputBytes, "merge-base", "--end-of-options", a, b)
	if err != nil {
		var ge *gitError
		if errors.As(err, &ge) && ge.code == 1 {
			return "", fmt.Errorf("%s and %s have no common ancestor", a, b)
		}
		return "", fmt.Errorf("merge base of %s and %s: %w", a, b, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Log returns the commits reachable from to and not from from, oldest first.
// A commit that is not a merge carries the paths it touched.
func (g *Git) Log(ctx context.Context, from, to string) ([]Commit, error) {
	if err := checkName("commit", from); err != nil {
		return nil, err
	}
	if err := checkName("commit", to); err != nil {
		return nil, err
	}
	const fields = 6
	out, err := g.run(ctx, maxOutputBytes, "log", "-z", "--topo-order", "--reverse", "--no-color", "--no-show-signature",
		"--format=%H%x00%P%x00%an%x00%at%x00%s%x00%b", "--end-of-options", from+".."+to)
	if err != nil {
		return nil, fmt.Errorf("log %s..%s: %w", from, to, err)
	}
	tokens := strings.Split(string(out), "\x00")
	if len(tokens) > 0 && tokens[len(tokens)-1] == "" {
		tokens = tokens[:len(tokens)-1]
	}
	if len(tokens)%fields != 0 {
		return nil, fmt.Errorf("log %s..%s: unexpected output", from, to)
	}
	var commits []Commit
	for i := 0; i < len(tokens); i += fields {
		sec, err := strconv.ParseInt(strings.TrimSpace(tokens[i+3]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("log %s..%s: commit date: %w", from, to, err)
		}
		c := Commit{
			ID:      strings.TrimSpace(tokens[i]),
			Parents: strings.Fields(tokens[i+1]),
			Author:  tokens[i+2],
			At:      time.Unix(sec, 0).UTC(),
			Subject: tokens[i+4],
			Body:    strings.TrimRight(tokens[i+5], "\n"),
		}
		if len(c.Parents) < 2 {
			files, err := g.commitFiles(ctx, c.ID)
			if err != nil {
				return nil, err
			}
			c.Files = files
		}
		commits = append(commits, c)
	}
	return commits, nil
}

func (g *Git) commitFiles(ctx context.Context, id string) ([]string, error) {
	out, err := g.run(ctx, maxOutputBytes, "diff-tree", "-r", "-z", "--root", "--no-commit-id", "--name-only", "--no-renames", "--no-ext-diff", "--end-of-options", id)
	if err != nil {
		return nil, fmt.Errorf("files of commit %s: %w", id, err)
	}
	return splitNUL(out), nil
}

// DiffStat returns the files that differ between base and head, with their
// line counts. Pass the merge base as base to get the net change of a branch.
func (g *Git) DiffStat(ctx context.Context, base, head string) ([]FileStat, error) {
	if err := checkName("commit", base); err != nil {
		return nil, err
	}
	if err := checkName("commit", head); err != nil {
		return nil, err
	}
	common := []string{"diff", "-z", "--find-renames", "--no-ext-diff", "--no-textconv", "--no-color"}
	status, err := g.run(ctx, maxOutputBytes, append(append([]string{}, common...), "--name-status", "--end-of-options", base, head)...)
	if err != nil {
		return nil, fmt.Errorf("diff %s %s: %w", base, head, err)
	}
	numstat, err := g.run(ctx, maxOutputBytes, append(append([]string{}, common...), "--numstat", "--end-of-options", base, head)...)
	if err != nil {
		return nil, fmt.Errorf("diff %s %s: %w", base, head, err)
	}
	counts, err := parseNumstat(splitNUL(numstat))
	if err != nil {
		return nil, err
	}
	stats, err := parseNameStatus(splitNUL(status))
	if err != nil {
		return nil, err
	}
	for i := range stats {
		c, ok := counts[stats[i].Path]
		if !ok {
			return nil, fmt.Errorf("diff %s %s: no line count for %q", base, head, stats[i].Path)
		}
		stats[i].Added, stats[i].Deleted, stats[i].Binary = c.added, c.deleted, c.binary
	}
	return stats, nil
}

// Diff returns the patch from base to head.
func (g *Git) Diff(ctx context.Context, base, head string) ([]byte, error) {
	if err := checkName("commit", base); err != nil {
		return nil, err
	}
	if err := checkName("commit", head); err != nil {
		return nil, err
	}
	out, err := g.run(ctx, maxDiffBytes, "diff", "--find-renames", "--no-ext-diff", "--no-textconv", "--no-color", "--end-of-options", base, head)
	if err != nil {
		return nil, fmt.Errorf("diff %s %s: %w", base, head, err)
	}
	return out, nil
}

type lineCount struct {
	added, deleted int
	binary         bool
}

func parseNumstat(tokens []string) (map[string]lineCount, error) {
	counts := make(map[string]lineCount)
	for i := 0; i < len(tokens); {
		parts := strings.SplitN(tokens[i], "\t", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("diff numstat: unexpected entry %q", tokens[i])
		}
		path := parts[2]
		i++
		if path == "" {
			if i+1 >= len(tokens) {
				return nil, errors.New("diff numstat: truncated rename entry")
			}
			path = tokens[i+1]
			i += 2
		}
		var c lineCount
		if parts[0] == "-" && parts[1] == "-" {
			c.binary = true
		} else {
			var err error
			if c.added, err = strconv.Atoi(parts[0]); err != nil {
				return nil, fmt.Errorf("diff numstat: %q: %w", tokens[i-1], err)
			}
			if c.deleted, err = strconv.Atoi(parts[1]); err != nil {
				return nil, fmt.Errorf("diff numstat: %q: %w", tokens[i-1], err)
			}
		}
		counts[path] = c
	}
	return counts, nil
}

func parseNameStatus(tokens []string) ([]FileStat, error) {
	var stats []FileStat
	for i := 0; i < len(tokens); {
		code := tokens[i]
		i++
		switch code[:1] {
		case "R", "C":
			if i+1 >= len(tokens) {
				return nil, errors.New("diff name-status: truncated rename entry")
			}
			st := FileStat{Path: tokens[i+1], OldPath: tokens[i], Status: "renamed"}
			if code[:1] == "C" {
				st.OldPath, st.Status = "", "added"
			}
			stats = append(stats, st)
			i += 2
		default:
			if i >= len(tokens) {
				return nil, errors.New("diff name-status: truncated entry")
			}
			st := FileStat{Path: tokens[i], Status: "modified"}
			switch code[:1] {
			case "A":
				st.Status = "added"
			case "D":
				st.Status = "deleted"
			}
			stats = append(stats, st)
			i++
		}
	}
	return stats, nil
}

func splitNUL(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	b = bytes.TrimSuffix(b, []byte{0})
	return strings.Split(string(b), "\x00")
}

// checkName refuses a name that git could read as an option or that holds a
// control character.
func checkName(kind, s string) error {
	if s == "" {
		return fmt.Errorf("%s is empty", kind)
	}
	if s[0] == '-' {
		return fmt.Errorf("%s %q starts with a dash", kind, s)
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return fmt.Errorf("%s %q holds a control character", kind, s)
		}
	}
	return nil
}

// checkBranch also refuses the characters that would change the meaning of a refspec.
func checkBranch(b string) error {
	if err := checkName("branch", b); err != nil {
		return err
	}
	if strings.ContainsAny(b, " ~^:?*[\\") || strings.Contains(b, "..") || strings.HasSuffix(b, "/") {
		return fmt.Errorf("branch %q is not a valid branch name", b)
	}
	return nil
}

type gitError struct {
	args   []string
	code   int
	stderr string
	err    error
}

func (e *gitError) Error() string {
	msg := e.stderr
	if msg == "" {
		msg = e.err.Error()
	}
	return fmt.Sprintf("git %s: %s", e.args[0], msg)
}

func (e *gitError) Unwrap() error { return e.err }

var scrubbedEnv = []string{
	"GIT_DIR=", "GIT_WORK_TREE=", "GIT_INDEX_FILE=", "GIT_OBJECT_DIRECTORY=",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES=", "GIT_COMMON_DIR=", "GIT_NAMESPACE=",
}

func (g *Git) command(ctx context.Context, args ...string) *exec.Cmd {
	full := append([]string{
		"-C", g.dir,
		"-c", "core.hooksPath=" + os.DevNull,
		"-c", "core.fsmonitor=false",
		// a signature check would start gpg.program, which .git/config can set
		"-c", "log.showSignature=false",
		"--no-pager",
	}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	var env []string
next:
	for _, kv := range os.Environ() {
		for _, p := range scrubbedEnv {
			if strings.HasPrefix(kv, p) {
				continue next
			}
		}
		env = append(env, kv)
	}
	cmd.Env = append(env, "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "LC_ALL=C")
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

func (g *Git) run(ctx context.Context, limit int64, args ...string) ([]byte, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := &capBuffer{limit: limit, cancel: cancel}
	errOut := &capBuffer{limit: maxStderrBytes}
	cmd := g.command(runCtx, args...)
	cmd.Stdout, cmd.Stderr = out, errOut
	err := cmd.Run()
	if out.exceeded {
		return nil, fmt.Errorf("git %s: output is larger than %d bytes", args[0], limit)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("git %s: %w", args[0], ctx.Err())
		}
		ge := &gitError{args: args, code: -1, stderr: strings.TrimSpace(errOut.buf.String()), err: err}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			ge.code = ee.ExitCode()
		}
		return nil, ge
	}
	return out.buf.Bytes(), nil
}

type capBuffer struct {
	buf      bytes.Buffer
	limit    int64
	cancel   context.CancelFunc
	exceeded bool
}

func (b *capBuffer) Write(p []byte) (int, error) {
	room := b.limit - int64(b.buf.Len())
	if int64(len(p)) <= room {
		return b.buf.Write(p)
	}
	if room > 0 {
		b.buf.Write(p[:room])
	}
	if b.cancel != nil && !b.exceeded {
		b.exceeded = true
		b.cancel()
	}
	return len(p), nil
}

// CurrentBranch returns the name of the checked-out branch, or "" for a detached HEAD.
func (g *Git) CurrentBranch(ctx context.Context) (string, error) {
	out, err := g.run(ctx, maxOutputBytes, "symbolic-ref", "--short", "--quiet", "HEAD")
	if err != nil {
		var ge *gitError
		if errors.As(err, &ge) && ge.code == 1 {
			return "", nil
		}
		return "", fmt.Errorf("read the current branch: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// DefaultBranch returns the branch that the remote's HEAD names in this clone, as it was when the
// clone last learned it, or "" when the clone has none. It never talks to the remote.
func (g *Git) DefaultBranch(ctx context.Context, remote string) (string, error) {
	if err := checkName("remote", remote); err != nil {
		return "", err
	}
	out, err := g.run(ctx, maxOutputBytes, "symbolic-ref", "--short", "--quiet", "refs/remotes/"+remote+"/HEAD")
	if err != nil {
		var ge *gitError
		if errors.As(err, &ge) && ge.code == 1 {
			return "", nil
		}
		return "", fmt.Errorf("read the default branch of %s: %w", remote, err)
	}
	return strings.TrimPrefix(strings.TrimSpace(string(out)), remote+"/"), nil
}

// Config returns the value of a git setting as the person's git reads it, or "" when it is not set.
func (g *Git) Config(ctx context.Context, key string) (string, error) {
	if err := checkName("setting", key); err != nil {
		return "", err
	}
	out, err := g.run(ctx, maxOutputBytes, "config", "--get", "--", key)
	if err != nil {
		var ge *gitError
		if errors.As(err, &ge) && ge.code == 1 {
			return "", nil
		}
		return "", fmt.Errorf("read git setting %s: %w", key, err)
	}
	return strings.TrimSpace(string(out)), nil
}
