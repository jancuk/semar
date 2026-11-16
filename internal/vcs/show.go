package vcs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

// Show returns the content of path at commit, at most limit bytes. It runs
// git cat-file blob <commit>:<path>. A path that is not in the commit is fs.ErrNotExist.
func (g *Git) Show(ctx context.Context, commit, path string, limit int64) ([]byte, error) {
	if !fullCommitID(commit) {
		return nil, fmt.Errorf("show: commit %q is not a full commit ID", commit)
	}
	if err := checkShowPath(path); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, fmt.Errorf("show %s: limit must be above zero", path)
	}
	out, err := g.run(ctx, limit, "cat-file", "blob", commit+":"+path)
	if err != nil {
		var ge *gitError
		if errors.As(err, &ge) && strings.Contains(ge.stderr, "does not exist in") {
			return nil, fmt.Errorf("show %s at %s: %w", path, commit, fs.ErrNotExist)
		}
		return nil, fmt.Errorf("show %s at %s: %w", path, commit, err)
	}
	return out, nil
}

// fullCommitID accepts only the 40 or 64 lower-case hex digits of a SHA-1 or
// SHA-256 object name, so that a branch name, an option, or a revision
// expression never reaches git.
func fullCommitID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// checkShowPath refuses what git would read as anything but one path in the
// commit: a colon, an option, and a path that leaves the tree.
func checkShowPath(path string) error {
	if err := checkName("path", path); err != nil {
		return fmt.Errorf("show: %w", err)
	}
	if strings.Contains(path, ":") {
		return fmt.Errorf("show: path %q holds a colon", path)
	}
	if !filepath.IsLocal(filepath.FromSlash(path)) {
		return fmt.Errorf("show: path %q is not local", path)
	}
	return nil
}
