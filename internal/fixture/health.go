package fixture

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/idcheck"
)

const healthSchema = 1

// HealthRecord is the result of the last `semar fixture check` of one fixture on one environment.
type HealthRecord struct {
	Schema      int               `json:"schema"` // 1
	Fixture     domain.FixtureID  `json:"fixture"`
	Version     string            `json:"version"`
	Hash        domain.Hash       `json:"hash"`
	Environment string            `json:"environment"`
	Run         domain.RunID      `json:"run"`
	At          time.Time         `json:"at"`
	Builds      []domain.BuildRef `json:"builds"`
	Find        string            `json:"find"`    // found | nothing | failed | none
	Create      string            `json:"create"`  // made | failed | not_tried | none
	Cleanup     string            `json:"cleanup"` // done | failed | none | not_needed
	Healthy     bool              `json:"healthy"`
	Reason      string            `json:"reason,omitempty"`
}

// HealthStore keeps one record per environment and fixture under <root>/store/fixture-health.
// The repository can commit the store, so a record can only block a scenario, never let one pass.
type HealthStore struct{ root string }

// NewHealthStore takes the root of the evidence store.
func NewHealthStore(root string) *HealthStore { return &HealthStore{root: root} }

func healthRel(env string, id domain.FixtureID) (dir, name string, err error) {
	if err := idcheck.Check("environment", env); err != nil {
		return "", "", err
	}
	if err := idcheck.Check("fixture ID", string(id)); err != nil {
		return "", "", err
	}
	dir = filepath.Join("store", "fixture-health", env)
	name = filepath.Join(dir, string(id)+".json")
	if !filepath.IsLocal(name) {
		return "", "", fmt.Errorf("path %q is not local", name)
	}
	return dir, name, nil
}

// Put replaces the record of the fixture on its environment.
func (h *HealthStore) Put(r HealthRecord) error {
	dir, name, err := healthRel(r.Environment, r.Fixture)
	if err != nil {
		return fmt.Errorf("store fixture health: %w", err)
	}
	r.Schema = healthSchema
	data, err := atomicfile.EncodeJSON(r)
	if err != nil {
		return fmt.Errorf("encode fixture health %s: %w", name, err)
	}
	if err := os.MkdirAll(h.root, atomicfile.DirMode); err != nil {
		return fmt.Errorf("store fixture health: %w", err)
	}
	root, err := os.OpenRoot(h.root)
	if err != nil {
		return fmt.Errorf("store fixture health: %w", err)
	}
	defer root.Close()
	if _, err := atomicfile.MkdirIn(root, dir); err != nil {
		return fmt.Errorf("store fixture health: %w", err)
	}
	if err := atomicfile.WriteIn(root, name, data, atomicfile.FileMode); err != nil {
		return fmt.Errorf("store fixture health: %w", err)
	}
	return nil
}

// Latest returns the record of the fixture on the environment. A record that is a link, not a
// regular file, swapped during the read, has an unknown field or another schema, or names another
// fixture or environment is an error and not a skip.
func (h *HealthStore) Latest(env string, id domain.FixtureID) (HealthRecord, bool, error) {
	dir, name, err := healthRel(env, id)
	if err != nil {
		return HealthRecord{}, false, fmt.Errorf("load fixture health: %w", err)
	}
	root, err := os.OpenRoot(h.root)
	if errors.Is(err, fs.ErrNotExist) {
		return HealthRecord{}, false, nil
	}
	if err != nil {
		return HealthRecord{}, false, fmt.Errorf("load fixture health: %w", err)
	}
	defer root.Close()
	if err := atomicfile.CheckDirIn(root, dir); err != nil {
		return HealthRecord{}, false, fmt.Errorf("load fixture health: %w", err)
	}
	shown := filepath.Join(h.root, name)
	data, err := readRecord(root, name, shown)
	if errors.Is(err, fs.ErrNotExist) {
		return HealthRecord{}, false, nil
	}
	if err != nil {
		return HealthRecord{}, false, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var r HealthRecord
	if err := dec.Decode(&r); err != nil {
		return HealthRecord{}, false, fmt.Errorf("parse fixture health %s: %w", shown, err)
	}
	if r.Schema != healthSchema {
		return HealthRecord{}, false, fmt.Errorf("fixture health %s has schema %d, want %d", shown, r.Schema, healthSchema)
	}
	if r.Fixture != id || r.Environment != env {
		return HealthRecord{}, false, fmt.Errorf("fixture health %s is for fixture %s on %s, want %s on %s", shown, r.Fixture, r.Environment, id, env)
	}
	return r, true, nil
}

// readRecord reads a regular file under root. A missing file wraps fs.ErrNotExist.
func readRecord(root *os.Root, name, shown string) ([]byte, error) {
	info, err := root.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, err
	case err != nil:
		return nil, fmt.Errorf("read fixture health %s: %w", shown, err)
	case info.Mode()&fs.ModeSymlink != 0:
		return nil, fmt.Errorf("fixture health %s is a symbolic link, and Semar reads only real files: replace it with the record", shown)
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("fixture health %s is not a regular file", shown)
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("read fixture health %s: %w", shown, err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("read fixture health %s: %w", shown, err)
	}
	if !os.SameFile(info, opened) {
		return nil, fmt.Errorf("fixture health %s changed while it was read", shown)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("read fixture health %s: %w", shown, err)
	}
	if len(data) > MaxFileSize {
		return nil, fmt.Errorf("fixture health %s is larger than %d bytes", shown, MaxFileSize)
	}
	return data, nil
}

// unhealthy is the blocked text of a failed check of the fixture as it is now, or "". A record of
// another hash says nothing about the current fixture.
func unhealthy(r HealthRecord, f domain.Fixture) string {
	if r.Healthy || r.Hash != domain.FixtureHash(f) {
		return ""
	}
	return fmt.Sprintf("fixture unhealthy: %s failed its last check on %s at %s: %s", f.ID, r.Environment, r.At.UTC().Format(time.RFC3339), r.Reason)
}
