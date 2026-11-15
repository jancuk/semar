package vcs

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Archive copies the regular files of a commit into dest, which must not
// exist. Links, devices, and files above the per-file cap are skipped. A path
// that is not local, or a copy above the total cap, is an error and removes dest.
func (g *Git) Archive(ctx context.Context, commit, dest string) (err error) {
	if err := checkName("commit", commit); err != nil {
		return err
	}
	if _, statErr := os.Lstat(dest); statErr == nil {
		return fmt.Errorf("archive: %s already exists", dest)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("archive: %w", statErr)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return fmt.Errorf("archive: %w", err)
	}
	if err := os.Mkdir(dest, 0o700); err != nil {
		return fmt.Errorf("archive: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dest)
		}
	}()
	if err := os.Chmod(dest, 0o700); err != nil {
		return fmt.Errorf("archive: %w", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := g.command(runCtx, "archive", "--format=tar", "--end-of-options", commit)
	stderr := &capBuffer{limit: maxStderrBytes}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("archive %s: %w", commit, err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("archive %s: %w", commit, err)
	}
	extractErr := extractTar(stdout, dest, g.archiveFileCap, g.archiveTotalCap)
	if extractErr != nil {
		cancel()
	} else {
		_, _ = io.Copy(io.Discard, stdout)
	}
	waitErr := cmd.Wait()
	if extractErr != nil {
		return fmt.Errorf("archive %s: %w", commit, extractErr)
	}
	if waitErr != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("archive %s: %w", commit, ctx.Err())
		}
		return fmt.Errorf("archive %s: %w", commit, &gitError{args: []string{"archive"}, stderr: stderr.buf.String(), err: waitErr})
	}
	return nil
}

func extractTar(r io.Reader, dest string, fileCap, totalCap int64) error {
	tr := tar.NewReader(r)
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.FromSlash(h.Name)
		if !filepath.IsLocal(name) {
			return fmt.Errorf("path %q is not local", h.Name)
		}
		if h.Size > fileCap {
			continue
		}
		if total+h.Size > totalCap {
			return fmt.Errorf("the copy is larger than %d bytes", totalCap)
		}
		total += h.Size
		target := filepath.Join(dest, name)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(f, tr)
		if closeErr := f.Close(); copyErr == nil {
			copyErr = closeErr
		}
		if copyErr != nil {
			return copyErr
		}
	}
}
