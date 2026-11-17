package confirm

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/idcheck"
)

// FixtureApproval records that a person approved one fixture, as the hash of its content.
type FixtureApproval struct {
	Schema   int              `json:"schema"` // 1
	Fixture  domain.FixtureID `json:"fixture"`
	Version  string           `json:"version"`
	Hash     domain.Hash      `json:"hash"`
	User     string           `json:"user"`
	Email    string           `json:"email,omitempty"`
	Identity string           `json:"identity"` // "local, unverified identity"
	At       time.Time        `json:"at"`
}

// fixtureRel is the directory of a fixture's approvals below the root.
func fixtureRel(id domain.FixtureID, h domain.Hash) (string, error) {
	if err := idcheck.Check("fixture ID", string(id)); err != nil {
		return "", err
	}
	if err := idcheck.Check("fixture hash", string(h)); err != nil {
		return "", err
	}
	rel := filepath.Join("store", "approvals", "fixtures", string(id), string(h))
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("path %q is not local", rel)
	}
	return rel, nil
}

// PutFixture keeps the first approval of a user for a fixture hash: a later one changes nothing.
// It has the same rules for the store as Put.
func (l *Local) PutFixture(r FixtureApproval) error {
	dir, err := fixtureRel(r.Fixture, r.Hash)
	if err != nil {
		return fmt.Errorf("store fixture approval: %w", err)
	}
	name := filepath.Join(dir, fileName(r.User))
	r.Schema = schema
	data, err := atomicfile.EncodeJSON(r)
	if err != nil {
		return fmt.Errorf("encode fixture approval %s: %w", filepath.Join(l.root, name), err)
	}
	root, err := os.OpenRoot(l.root)
	if err != nil {
		return fmt.Errorf("store fixture approval: %w", err)
	}
	defer root.Close()
	if _, err := atomicfile.MkdirIn(root, dir); err != nil {
		return fmt.Errorf("store fixture approval: %w", err)
	}
	if err := atomicfile.WriteNewIn(root, name, data, atomicfile.FileMode); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("store fixture approval: %w", err)
	}
	return nil
}

// FixtureApproved reports whether any user approved the fixture at this hash. A record that is a
// link, not a regular file, unreadable, of another schema, or kept under the wrong fixture or hash
// is an error and not a skip, because the repository under test can commit the store.
func (l *Local) FixtureApproved(id domain.FixtureID, h domain.Hash) (bool, error) {
	dir, err := fixtureRel(id, h)
	if err != nil {
		return false, fmt.Errorf("load fixture approval: %w", err)
	}
	root, err := os.OpenRoot(l.root)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load fixture approval: %w", err)
	}
	defer root.Close()
	if err := atomicfile.CheckDirIn(root, dir); err != nil {
		return false, fmt.Errorf("load fixture approval: %w", err)
	}
	shown := filepath.Join(l.root, dir)
	d, err := root.Open(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read fixture approvals in %s: %w", shown, err)
	}
	entries, err := d.ReadDir(-1)
	_ = d.Close()
	if err != nil {
		return false, fmt.Errorf("read fixture approvals in %s: %w", shown, err)
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	found := false
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		r, err := readFixtureApproval(root, filepath.Join(dir, e.Name()))
		if err != nil {
			return false, err
		}
		if r.Fixture != id || r.Hash != h {
			return false, fmt.Errorf("fixture approval %s is for fixture %q and hash %q, want %q and %q", filepath.Join(shown, e.Name()), r.Fixture, r.Hash, id, h)
		}
		found = true
	}
	return found, nil
}

// FixtureApprovals returns every approval of the fixture at this hash, in the order of the file names.
func (l *Local) FixtureApprovals(id domain.FixtureID, h domain.Hash) ([]FixtureApproval, error) {
	dir, err := fixtureRel(id, h)
	if err != nil {
		return nil, fmt.Errorf("load fixture approval: %w", err)
	}
	return readDir(l, dir, readFixtureApproval)
}

func readFixtureApproval(root *os.Root, name string) (FixtureApproval, error) {
	data, err := readRegular(root, name, "fixture approval")
	if err != nil {
		return FixtureApproval{}, err
	}
	p := filepath.Join(root.Name(), name)
	var r FixtureApproval
	if err := decodeStrict(data, &r); err != nil {
		return FixtureApproval{}, fmt.Errorf("parse fixture approval %s: %w", p, err)
	}
	if r.Schema != schema {
		return FixtureApproval{}, fmt.Errorf("fixture approval %s has schema %d, want %d", p, r.Schema, schema)
	}
	return r, nil
}
