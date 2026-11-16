// Package confirm records that a person confirmed an expectation, and decides which expectations need it.
package confirm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/idcheck"
)

const schema = 1

type Record struct {
	Schema     int               `json:"schema"`
	Scenario   domain.ScenarioID `json:"scenario"`
	ExpectHash domain.Hash       `json:"expectHash"`
	User       string            `json:"user"`
	Identity   string            `json:"identity"` // "local, unverified identity" in this plan. FR-11
	At         time.Time         `json:"at"`
	// Sources are the sources of the expectations the person saw, sorted and without duplicates.
	// A record without any (one from an older build) confirms nothing.
	Sources []domain.Source `json:"sources"`
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

var userCleaner = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// rel is the directory of a scenario's confirmations below the root.
func rel(id domain.ScenarioID, h domain.Hash) (string, error) {
	if err := idcheck.Check("scenario ID", string(id)); err != nil {
		return "", err
	}
	if err := idcheck.Check("expectation hash", string(h)); err != nil {
		return "", err
	}
	rel := filepath.Join("store", "confirmations", string(id), string(h))
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("path %q is not local", rel)
	}
	return rel, nil
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
// A record that names no source confirms nothing, so Put refuses it. Every access goes through
// a root at the store, and every directory from store down must be a real one, because the
// repository under test can commit a link there.
func (l *Local) Put(r Record) error {
	r.Sources = slices.Compact(slices.Sorted(slices.Values(r.Sources)))
	if len(r.Sources) == 0 {
		return errors.New("store confirmation: the record names no source")
	}
	dir, err := rel(r.Scenario, r.ExpectHash)
	if err != nil {
		return fmt.Errorf("store confirmation: %w", err)
	}
	name := filepath.Join(dir, fileName(r.User))
	r.Schema = schema
	data, err := atomicfile.EncodeJSON(r)
	if err != nil {
		return fmt.Errorf("encode confirmation %s: %w", filepath.Join(l.root, name), err)
	}
	root, err := os.OpenRoot(l.root)
	if err != nil {
		return fmt.Errorf("store confirmation: %w", err)
	}
	defer root.Close()
	if _, err := atomicfile.MkdirIn(root, dir); err != nil {
		return fmt.Errorf("store confirmation: %w", err)
	}
	if err := atomicfile.WriteNewIn(root, name, data, atomicfile.FileMode); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("store confirmation: %w", err)
	}
	return nil
}

// Get returns the earliest confirmation of the scenario and hash. Its Sources are
// those of every user's confirmation, because each user's record counts.
func (l *Local) Get(id domain.ScenarioID, h domain.Hash) (Record, bool, error) {
	dir, err := rel(id, h)
	if err != nil {
		return Record{}, false, fmt.Errorf("load confirmation: %w", err)
	}
	root, err := os.OpenRoot(l.root)
	if errors.Is(err, fs.ErrNotExist) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("load confirmation: %w", err)
	}
	defer root.Close()
	if err := atomicfile.CheckDirIn(root, dir); err != nil {
		return Record{}, false, fmt.Errorf("load confirmation: %w", err)
	}
	shown := filepath.Join(l.root, dir)
	d, err := root.Open(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("read confirmations in %s: %w", shown, err)
	}
	entries, err := d.ReadDir(-1)
	_ = d.Close()
	if err != nil {
		return Record{}, false, fmt.Errorf("read confirmations in %s: %w", shown, err)
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	var best Record
	var sources []domain.Source
	found := false
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		r, err := read(root, filepath.Join(dir, e.Name()))
		if err != nil {
			return Record{}, false, err
		}
		if r.Scenario != id || r.ExpectHash != h {
			return Record{}, false, fmt.Errorf("confirmation %s is for scenario %q and hash %q, want %q and %q", filepath.Join(shown, e.Name()), r.Scenario, r.ExpectHash, id, h)
		}
		sources = append(sources, r.Sources...)
		if !found || r.At.Before(best.At) {
			best, found = r, true
		}
	}
	best.Sources = slices.Compact(slices.Sorted(slices.Values(sources)))
	return best, found, nil
}

// read decodes one confirmation. The file checks are shared with the other record kinds.
func read(root *os.Root, name string) (Record, error) {
	data, err := readRegular(root, name, "confirmation")
	if err != nil {
		return Record{}, err
	}
	p := filepath.Join(root.Name(), name)
	var r Record
	if err := decodeStrict(data, &r); err != nil {
		return Record{}, fmt.Errorf("parse confirmation %s: %w", p, err)
	}
	if r.Schema != schema {
		return Record{}, fmt.Errorf("confirmation %s has schema %d, want %d", p, r.Schema, schema)
	}
	return r, nil
}

func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// readRegular refuses a file that is a link or not a regular file, and one that was swapped
// between the check and the open. noun names the kind of record in an error.
func readRegular(root *os.Root, name, noun string) ([]byte, error) {
	p := filepath.Join(root.Name(), name)
	info, err := root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("read %s %s: %w", noun, p, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s %s is a symbolic link, and Semar reads only real files: replace it with the record", noun, p)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s %s is not a regular file", noun, p)
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("read %s %s: %w", noun, p, err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("read %s %s: %w", noun, p, err)
	}
	if !os.SameFile(info, opened) {
		return nil, fmt.Errorf("%s %s changed while it was read", noun, p)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read %s %s: %w", noun, p, err)
	}
	return data, nil
}
