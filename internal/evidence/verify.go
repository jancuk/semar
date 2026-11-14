package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/jancuk/semar/internal/domain"
)

// Verify reads only run.json and the files on disk, so a file that no longer parses is reported, not an error.
// It compares the run directory with the hashes in run.json. Each entry is a
// relative path and a reason: "changed", "missing", "not listed in run.json", or "not a regular file".
func (s *Store) Verify(id domain.RunID) (changed []string, err error) {
	run, dir, err := s.readRunFile(id)
	if err != nil {
		return nil, err
	}
	actual, problems, err := hashTree(dir)
	if err != nil {
		return nil, fmt.Errorf("verify run %s: %w", id, err)
	}
	for rel, want := range run.Files {
		got, ok := actual[rel]
		switch {
		case !ok && !containsPrefix(problems, rel+": "):
			problems = append(problems, rel+": missing")
		case ok && got != want:
			problems = append(problems, rel+": changed")
		}
	}
	for rel := range actual {
		if _, ok := run.Files[rel]; !ok {
			problems = append(problems, rel+": not listed in run.json")
		}
	}
	sort.Strings(problems)
	return problems, nil
}

func containsPrefix(list []string, prefix string) bool {
	for _, s := range list {
		if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// hashTree returns the SHA-256 of every regular file below dir except run.json,
// keyed by slash-separated relative path. Anything else is reported as a problem.
func hashTree(dir string) (files map[string]string, problems []string, err error) {
	files = map[string]string{}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := relPath(dir, p)
		if err != nil {
			return err
		}
		if rel == runFile {
			return nil
		}
		if !d.Type().IsRegular() {
			problems = append(problems, rel+": not a regular file")
			return nil
		}
		sum, err := hashFile(p)
		if err != nil {
			return err
		}
		files[rel] = sum
		return nil
	})
	return files, problems, err
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", p, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
