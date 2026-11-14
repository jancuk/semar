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
		if err := os.MkdirAll(dir, atomicfile.DirMode); err != nil {
			return nil, fmt.Errorf("open evidence store %s: %w", root, err)
		}
		// MkdirAll leaves the mode of a directory that exists, so set it
		if err := os.Chmod(dir, atomicfile.DirMode); err != nil {
			return nil, fmt.Errorf("open evidence store %s: %w", root, err)
		}
	}
	return &Store{root: root}, nil
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

func (s *Store) scriptPath(id domain.ScenarioID, h domain.Hash) (string, error) {
	if err := idcheck.Check("scenario ID", string(id)); err != nil {
		return "", err
	}
	if err := idcheck.Check("expectation hash", string(h)); err != nil {
		return "", err
	}
	return join(s.root, "store", "scripts", string(id), string(h)+".yaml")
}

func (s *Store) PutScript(sc domain.Script) error {
	p, err := s.scriptPath(sc.Scenario, sc.ExpectHash)
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
	return atomicfile.Write(p, []byte(buf.String()))
}

// Script reports false when no script is stored for the scenario and hash.
// A stored file that does not parse, or that belongs to another scenario or hash, is an error.
func (s *Store) Script(id domain.ScenarioID, h domain.Hash) (domain.Script, bool, error) {
	p, err := s.scriptPath(id, h)
	if err != nil {
		return domain.Script{}, false, fmt.Errorf("load script: %w", err)
	}
	data, err := os.ReadFile(p)
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

func readKey(p string) ([]byte, error) {
	key, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("read redaction key %s: %w", p, err)
	}
	if len(key) != redactKeySize {
		return nil, fmt.Errorf("redaction key %s holds %d bytes, want %d", p, len(key), redactKeySize)
	}
	return key, nil
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
