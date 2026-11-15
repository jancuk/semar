// Package evidence is the local store of runs, scripts, and the redaction key under .semar.
package evidence

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/script"
)

var ErrInterrupted = errors.New("the run was interrupted: it has no run.json")

const (
	runFile       = "run.json"
	activityFile  = "activity.jsonl"
	redactKeyFile = "redact.key"
	redactKeySize = 32
	schema        = 1
)

// Store's root is <repository>/.semar.
type Store struct{ root string }

func Open(root string) (*Store, error) {
	if root == "" {
		return nil, errors.New("open evidence store: the root is empty")
	}
	for _, dir := range []string{root, filepath.Join(root, "runs"), filepath.Join(root, "store")} {
		if err := ensureDir(dir); err != nil {
			return nil, fmt.Errorf("open evidence store %s: %w", root, err)
		}
	}
	return &Store{root: root}, nil
}

// ensureDir creates dir, or accepts it only when it is a real directory, and sets its mode.
// A repository can commit a link here, and following it would write evidence elsewhere
// and change the mode of the link's target.
func ensureDir(dir string) error {
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, atomicfile.DirMode); err != nil {
			return err
		}
		info, err = os.Lstat(dir)
	}
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symbolic link, and Semar keeps evidence only in a real directory: replace it with a directory", dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory: remove it so that Semar can create the directory", dir)
	}
	// MkdirAll leaves the mode of a directory that exists, so set it
	return os.Chmod(dir, atomicfile.DirMode)
}

func (s *Store) Root() string { return s.root }

// join builds a path under base from relative parts and refuses anything that could leave it.
func join(base string, parts ...string) (string, error) {
	rel := filepath.Join(parts...)
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("path %q is not local", rel)
	}
	return filepath.Join(base, rel), nil
}

func (s *Store) runDir(id domain.RunID) (string, error) {
	if err := idcheck.Check("run ID", string(id)); err != nil {
		return "", err
	}
	return join(s.root, "runs", string(id))
}

// scriptRel is the path of a stored script below the root.
func scriptRel(id domain.ScenarioID, h domain.Hash) (string, error) {
	if err := idcheck.Check("scenario ID", string(id)); err != nil {
		return "", err
	}
	if err := idcheck.Check("expectation hash", string(h)); err != nil {
		return "", err
	}
	return join("", "store", "scripts", string(id), string(h)+".yaml")
}

// openScripts opens the root for the script at rel. A repository can commit a link at
// store/scripts or at a scenario's directory below it, so each directory on the way must be
// a real one, and every access goes through the root so that a link cannot lead out of it.
func (s *Store) openScripts(rel string, create bool) (*os.Root, error) {
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, err
	}
	if create {
		_, err = atomicfile.MkdirIn(root, filepath.Dir(rel))
	} else {
		err = atomicfile.CheckDirIn(root, filepath.Dir(rel))
	}
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	return root, nil
}

func (s *Store) PutScript(sc domain.Script) error {
	rel, err := scriptRel(sc.Scenario, sc.ExpectHash)
	if err != nil {
		return fmt.Errorf("store script: %w", err)
	}
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(sc); err != nil {
		return fmt.Errorf("encode script %s: %w", sc.Scenario, err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("encode script %s: %w", sc.Scenario, err)
	}
	root, err := s.openScripts(rel, true)
	if err != nil {
		return fmt.Errorf("store script %s: %w", sc.Scenario, err)
	}
	defer root.Close()
	return atomicfile.WriteIn(root, rel, []byte(buf.String()), atomicfile.FileMode)
}

// RemoveScript removes a stored script, and its scenario's directory when nothing else is
// left in it. A script that is not stored is not an error.
func (s *Store) RemoveScript(id domain.ScenarioID, h domain.Hash) error {
	rel, err := scriptRel(id, h)
	if err != nil {
		return fmt.Errorf("remove script: %w", err)
	}
	root, err := s.openScripts(rel, false)
	if err != nil {
		return fmt.Errorf("remove script %s: %w", id, err)
	}
	defer root.Close()
	if err := root.Remove(rel); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove script %s: %w", id, err)
	}
	dir, err := root.Open(filepath.Dir(rel))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove script %s: %w", id, err)
	}
	names, err := dir.Readdirnames(1)
	_ = dir.Close()
	if len(names) == 0 && errors.Is(err, io.EOF) {
		if err := root.Remove(filepath.Dir(rel)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove script %s: %w", id, err)
		}
	}
	return nil
}

// Script reports false when no script is stored for the scenario and hash.
// A stored file that does not parse, or that belongs to another scenario or hash, is an error.
func (s *Store) Script(id domain.ScenarioID, h domain.Hash) (domain.Script, bool, error) {
	rel, err := scriptRel(id, h)
	if err != nil {
		return domain.Script{}, false, fmt.Errorf("load script: %w", err)
	}
	p := filepath.Join(s.root, rel)
	root, err := s.openScripts(rel, false)
	if err != nil {
		return domain.Script{}, false, fmt.Errorf("load script %s: %w", id, err)
	}
	defer root.Close()
	data, err := root.ReadFile(rel)
	if errors.Is(err, fs.ErrNotExist) {
		return domain.Script{}, false, nil
	}
	if err != nil {
		return domain.Script{}, false, fmt.Errorf("read script %s: %w", p, err)
	}
	sc, err := script.Parse(data, p)
	if err != nil {
		return domain.Script{}, false, err
	}
	if sc.Scenario != id || sc.ExpectHash != h {
		return domain.Script{}, false, fmt.Errorf("script %s holds scenario %q and hash %q, want %q and %q", p, sc.Scenario, sc.ExpectHash, id, h)
	}
	return sc, true, nil
}

// RedactKey returns the 32-byte key that redaction uses, and creates it on first use.
func (s *Store) RedactKey() ([]byte, error) {
	p, err := join(s.root, "store", redactKeyFile)
	if err != nil {
		return nil, err
	}
	if key, err := readKey(p); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return key, err
	}
	fresh := make([]byte, redactKeySize)
	if _, err := io.ReadFull(rand.Reader, fresh); err != nil {
		return nil, fmt.Errorf("generate redaction key: %w", err)
	}
	// a concurrent creator that won leaves a whole key, which is read below
	if err := atomicfile.WriteNew(p, fresh); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("create redaction key: %w", err)
	}
	return readKey(p)
}

// readKey refuses a key file that is a link, is not a regular file, or that others can read.
// It never repairs one: a key that others could read is no longer secret.
func readKey(p string) ([]byte, error) {
	info, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("read redaction key %s: %w", p, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("redaction key %s is a symbolic link: %s", p, deleteKeyHint)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("redaction key %s is not a regular file: %s", p, deleteKeyHint)
	}
	if !ownerOnly(info.Mode()) {
		return nil, fmt.Errorf("redaction key %s has mode %04o, want 0600, so others could read it: %s", p, info.Mode().Perm(), deleteKeyHint)
	}
	if info.Size() != redactKeySize {
		return nil, fmt.Errorf("redaction key %s holds %d bytes, want %d: %s", p, info.Size(), redactKeySize, deleteKeyHint)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("read redaction key %s: %w", p, err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("read redaction key %s: %w", p, err)
	}
	if !os.SameFile(info, opened) {
		return nil, fmt.Errorf("redaction key %s changed while it was read", p)
	}
	key, err := io.ReadAll(io.LimitReader(f, redactKeySize+1))
	if err != nil {
		return nil, fmt.Errorf("read redaction key %s: %w", p, err)
	}
	if len(key) != redactKeySize {
		return nil, fmt.Errorf("redaction key %s holds %d bytes, want %d: %s", p, len(key), redactKeySize, deleteKeyHint)
	}
	return key, nil
}

const deleteKeyHint = "delete it, and Semar makes a new key on the next run"

// ownerOnly reports whether only the owner can read and write a file of this mode.
// Windows has no permission bits for the group and others, so there it is always true.
func ownerOnly(m fs.FileMode) bool {
	return runtime.GOOS == "windows" || m.Perm() == atomicfile.FileMode
}

// Runs lists complete and interrupted runs, newest first by directory name.
func (s *Store) Runs() ([]domain.RunID, error) {
	dir := filepath.Join(s.root, "runs")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list runs in %s: %w", dir, err)
	}
	var ids []domain.RunID
	for _, e := range entries {
		if e.IsDir() && idcheck.Check("run ID", e.Name()) == nil {
			ids = append(ids, domain.RunID(e.Name()))
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] > ids[j] })
	return ids, nil
}

// LoadRun returns the run and its activity in Seq order. A directory with no run.json
// gives an error that wraps ErrInterrupted, and a missing directory one that wraps fs.ErrNotExist.
func (s *Store) LoadRun(id domain.RunID) (domain.Run, []domain.Activity, error) {
	run, dir, err := s.readRunFile(id)
	if err != nil {
		return domain.Run{}, nil, err
	}
	acts, err := readActivity(filepath.Join(dir, activityFile))
	if err != nil {
		return domain.Run{}, nil, fmt.Errorf("run %s: %w", id, err)
	}
	return run, acts, nil
}

// Activity returns the activity of a complete or an interrupted run in Seq order.
// An interrupted run's activity is not covered by the hashes of a run.json.
func (s *Store) Activity(id domain.RunID) ([]domain.Activity, error) {
	dir, err := s.runDir(id)
	if err != nil {
		return nil, fmt.Errorf("load activity: %w", err)
	}
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("run %s was not found in %s: %w", id, filepath.Dir(dir), err)
	}
	acts, err := readActivity(filepath.Join(dir, activityFile))
	if err != nil {
		return nil, fmt.Errorf("run %s: %w", id, err)
	}
	return acts, nil
}

// readRunFile reads and checks run.json only, and returns the run directory too.
func (s *Store) readRunFile(id domain.RunID) (domain.Run, string, error) {
	dir, err := s.runDir(id)
	if err != nil {
		return domain.Run{}, "", fmt.Errorf("load run: %w", err)
	}
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return domain.Run{}, "", fmt.Errorf("run %s was not found in %s: %w", id, filepath.Dir(dir), err)
	}
	if err != nil {
		return domain.Run{}, "", fmt.Errorf("load run %s: %w", id, err)
	}
	if !info.IsDir() {
		return domain.Run{}, "", fmt.Errorf("run %s: %s is not a directory", id, dir)
	}
	data, err := os.ReadFile(filepath.Join(dir, runFile))
	if errors.Is(err, fs.ErrNotExist) {
		return domain.Run{}, "", fmt.Errorf("run %s: %w", id, ErrInterrupted)
	}
	if err != nil {
		return domain.Run{}, "", fmt.Errorf("read %s of run %s: %w", runFile, id, err)
	}
	var run domain.Run
	if err := decodeStrict(data, &run); err != nil {
		return domain.Run{}, "", fmt.Errorf("parse %s of run %s: %w", runFile, id, err)
	}
	if run.Schema != schema {
		return domain.Run{}, "", fmt.Errorf("%s of run %s has schema %d, want %d", runFile, id, run.Schema, schema)
	}
	return run, dir, nil
}

func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func readActivity(p string) ([]domain.Activity, error) {
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", activityFile, err)
	}
	defer f.Close()
	var acts []domain.Activity
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	for {
		var a domain.Activity
		if err := dec.Decode(&a); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("parse %s entry %d: %w", activityFile, len(acts)+1, err)
		}
		acts = append(acts, a)
	}
	sort.SliceStable(acts, func(i, j int) bool { return acts[i].Seq < acts[j].Seq })
	return acts, nil
}

// relPath returns the slash-separated path of p under dir.
func relPath(dir, p string) (string, error) {
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return "", err
	}
	return path.Clean(filepath.ToSlash(rel)), nil
}
