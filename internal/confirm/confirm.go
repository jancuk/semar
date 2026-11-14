// Package confirm records that a person confirmed an expectation, and decides which expectations need it.
package confirm

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/domain"
)

const schema = 1

type Record struct {
	Schema     int               `json:"schema"`
	Scenario   domain.ScenarioID `json:"scenario"`
	ExpectHash domain.Hash       `json:"expectHash"`
	User       string            `json:"user"`
	Identity   string            `json:"identity"` // "local, unverified identity" in this plan. FR-11
	At         time.Time         `json:"at"`
}

type Store interface {
	Put(r Record) error
	Get(id domain.ScenarioID, h domain.Hash) (Record, bool, error)
}

// Needs reports whether an expectation from src must be confirmed by a person before it can fail a scenario.
// A person's own expectation never needs one.
func Needs(src domain.Source, trust []domain.Source) bool {
	return src != domain.SourcePerson && slices.Contains(trust, src)
}

// Local keeps one file per user at <root>/store/confirmations/<scenario>/<hash>/<user>.json.
type Local struct{ root string }

func NewLocal(root string) *Local { return &Local{root: root} }

var (
	idPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	userCleaner = regexp.MustCompile(`[^A-Za-z0-9._-]`)
)

func checkID(kind, id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("invalid %s %q: use letters, digits, dot, underscore, or hyphen, starting with a letter or digit", kind, id)
	}
	return nil
}

func (l *Local) dir(id domain.ScenarioID, h domain.Hash) (string, error) {
	if err := checkID("scenario ID", string(id)); err != nil {
		return "", err
	}
	if err := checkID("expectation hash", string(h)); err != nil {
		return "", err
	}
	rel := filepath.Join("store", "confirmations", string(id), string(h))
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("path %q is not local", rel)
	}
	return filepath.Join(l.root, rel), nil
}

// fileName turns a user name into a safe file name. Characters outside [A-Za-z0-9._-]
// become "_", and a name that is empty or only dots becomes "unknown".
func fileName(user string) string {
	clean := userCleaner.ReplaceAllString(user, "_")
	if strings.Trim(clean, ".") == "" {
		clean = "unknown"
	}
	return clean + ".json"
}

// Put keeps the first confirmation of a user: a later one for the same user changes nothing.
func (l *Local) Put(r Record) error {
	dir, err := l.dir(r.Scenario, r.ExpectHash)
	if err != nil {
		return fmt.Errorf("store confirmation: %w", err)
	}
	p := filepath.Join(dir, fileName(r.User))
	if _, err := os.Stat(p); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("store confirmation %s: %w", p, err)
	}
	r.Schema = schema
	return atomicfile.WriteJSON(p, r)
}

// Get returns the earliest confirmation of the scenario and hash.
func (l *Local) Get(id domain.ScenarioID, h domain.Hash) (Record, bool, error) {
	dir, err := l.dir(id, h)
	if err != nil {
		return Record{}, false, fmt.Errorf("load confirmation: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("read confirmations in %s: %w", dir, err)
	}
	var best Record
	found := false
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		p := filepath.Join(dir, e.Name())
		r, err := read(p)
		if err != nil {
			return Record{}, false, err
		}
		if r.Scenario != id || r.ExpectHash != h {
			return Record{}, false, fmt.Errorf("confirmation %s is for scenario %q and hash %q, want %q and %q", p, r.Scenario, r.ExpectHash, id, h)
		}
		if !found || r.At.Before(best.At) {
			best, found = r, true
		}
	}
	return best, found, nil
}

func read(p string) (Record, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return Record{}, fmt.Errorf("read confirmation %s: %w", p, err)
	}
	var r Record
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return Record{}, fmt.Errorf("parse confirmation %s: %w", p, err)
	}
	if r.Schema != schema {
		return Record{}, fmt.Errorf("confirmation %s has schema %d, want %d", p, r.Schema, schema)
	}
	return r, nil
}
