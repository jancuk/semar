package vcs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

// Skipped is a path of a commit that Archive did not copy, and why.
type Skipped struct{ Path, Reason string }

const (
	reasonLink     = "a link"
	reasonModule   = "a submodule"
	reasonTooLarge = "larger than 5 MiB"
	reasonCase     = "its name differs from another file only in case"
	reasonNotLocal = "its path is not local"
	reasonNotFile  = "not a regular file"
)

type treeBlob struct {
	path string
	id   string
	size int64
}

// Archive copies the regular files of a commit into dest, which must not exist,
// and returns what it skipped, sorted by path. It reads objects directly rather
// than through git archive, so that no attribute of the repository under test
// (export-ignore, export-subst, a smudge filter) changes the copy or runs a program.
// A copy above the total cap is an error and removes dest.
func (g *Git) Archive(ctx context.Context, commit, dest string) (skipped []Skipped, err error) {
	if err := checkName("commit", commit); err != nil {
		return nil, err
	}
	if _, statErr := os.Lstat(dest); statErr == nil {
		return nil, fmt.Errorf("archive: %s already exists", dest)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("archive: %w", statErr)
	}
	out, err := g.run(ctx, maxOutputBytes, "ls-tree", "-r", "-z", "-l", "--full-tree", "--end-of-options", commit)
	if err != nil {
		return nil, fmt.Errorf("archive %s: %w", commit, err)
	}
	blobs, skipped, err := g.planCopy(splitNUL(out))
	if err != nil {
		return nil, fmt.Errorf("archive %s: %w", commit, err)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return nil, fmt.Errorf("archive: %w", err)
	}
	if err := os.Mkdir(dest, 0o700); err != nil {
		return nil, fmt.Errorf("archive: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dest)
		}
	}()
	if err := os.Chmod(dest, 0o700); err != nil {
		return nil, fmt.Errorf("archive: %w", err)
	}
	collided, err := g.writeBlobs(ctx, blobs, dest)
	if err != nil {
		return nil, fmt.Errorf("archive %s: %w", commit, err)
	}
	skipped = append(skipped, collided...)
	slices.SortFunc(skipped, func(a, b Skipped) int { return strings.Compare(a.Path, b.Path) })
	return skipped, nil
}

// planCopy reads ls-tree entries "<mode> <type> <id> <size>\t<path>" and decides every
// skip from the mode and the size column, before any content is read.
func (g *Git) planCopy(entries []string) ([]treeBlob, []Skipped, error) {
	var blobs []treeBlob
	var skipped []Skipped
	var total int64
	for _, e := range entries {
		meta, path, ok := strings.Cut(e, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 4 {
			return nil, nil, fmt.Errorf("ls-tree: unexpected entry %q", e)
		}
		mode, id, sizeText := f[0], f[2], f[3]
		switch {
		case mode == "120000":
			skipped = append(skipped, Skipped{path, reasonLink})
			continue
		case mode == "160000":
			skipped = append(skipped, Skipped{path, reasonModule})
			continue
		case mode != "100644" && mode != "100755":
			skipped = append(skipped, Skipped{path, reasonNotFile})
			continue
		case !filepath.IsLocal(filepath.FromSlash(path)):
			skipped = append(skipped, Skipped{path, reasonNotLocal})
			continue
		}
		size, err := strconv.ParseInt(sizeText, 10, 64)
		if err != nil || size < 0 {
			return nil, nil, fmt.Errorf("ls-tree: bad size in entry %q", e)
		}
		if size > g.archiveFileCap {
			skipped = append(skipped, Skipped{path, reasonTooLarge})
			continue
		}
		if total+size > g.archiveTotalCap {
			return nil, nil, fmt.Errorf("the copy is larger than %d bytes", g.archiveTotalCap)
		}
		total += size
		blobs = append(blobs, treeBlob{path: path, id: id, size: size})
	}
	return blobs, skipped, nil
}

// writeBlobs reads the contents of every blob from one git cat-file --batch process.
func (g *Git) writeBlobs(ctx context.Context, blobs []treeBlob, dest string) (skipped []Skipped, err error) {
	if len(blobs) == 0 {
		return nil, nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := g.command(runCtx, "cat-file", "--batch")
	stderr := &capBuffer{limit: maxStderrBytes}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	fed := make(chan error, 1)
	go func() {
		w := bufio.NewWriter(stdin)
		var werr error
		for _, b := range blobs {
			if _, werr = w.WriteString(b.id + "\n"); werr != nil {
				break
			}
		}
		if werr == nil {
			werr = w.Flush()
		}
		if cerr := stdin.Close(); werr == nil {
			werr = cerr
		}
		fed <- werr
	}()

	r := bufio.NewReader(stdout)
	var readErr error
	for _, b := range blobs {
		var collided bool
		if collided, readErr = writeBlob(r, b, dest); readErr != nil {
			break
		}
		if collided {
			skipped = append(skipped, Skipped{b.path, reasonCase})
		}
	}
	if readErr != nil {
		cancel()
	} else {
		_, _ = io.Copy(io.Discard, r)
	}
	feedErr := <-fed
	waitErr := cmd.Wait()
	switch {
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case readErr != nil:
		return nil, readErr
	case waitErr != nil:
		return nil, &gitError{args: []string{"cat-file"}, stderr: strings.TrimSpace(stderr.buf.String()), err: waitErr}
	case feedErr != nil:
		return nil, fmt.Errorf("git cat-file: %w", feedErr)
	}
	return skipped, nil
}

// writeBlob reads one "<id> blob <size>\n<content>\n" record. It reports a path that
// collides with one already written, which happens on a case-insensitive file system.
func writeBlob(r *bufio.Reader, b treeBlob, dest string) (collided bool, err error) {
	header, err := r.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("git cat-file: header of %s: %w", b.path, err)
	}
	f := strings.Fields(header)
	if len(f) != 3 || f[0] != b.id || f[1] != "blob" || f[2] != strconv.FormatInt(b.size, 10) {
		return false, fmt.Errorf("git cat-file: unexpected header %q for %s", strings.TrimSpace(header), b.path)
	}
	target := filepath.Join(dest, filepath.FromSlash(b.path))
	file, err := createFile(target)
	switch {
	case isCollision(err):
		collided = true
		_, err = io.CopyN(io.Discard, r, b.size)
	case err == nil:
		_, err = io.CopyN(file, r, b.size)
		if cerr := file.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		return false, err
	}
	if c, err := r.ReadByte(); err != nil || c != '\n' {
		return false, fmt.Errorf("git cat-file: record of %s does not end with a newline", b.path)
	}
	return collided, nil
}

func createFile(target string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

// isCollision is true when a file or a directory of the same name, ignoring case, was
// already written: the create finds it, or a parent directory turns out to be a file.
func isCollision(err error) bool {
	return errors.Is(err, os.ErrExist) || errors.Is(err, syscall.ENOTDIR)
}
