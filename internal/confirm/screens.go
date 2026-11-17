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

// ScreensApproval records that a person approved the screens file of one app, as the hash of its
// content. Locators is the content that the person saw, so that the next approval can say what changed.
type ScreensApproval struct {
	Schema   int                                  `json:"schema"` // 1
	App      string                               `json:"app"`
	Hash     domain.Hash                          `json:"hash"`
	User     string                               `json:"user"`
	Identity string                               `json:"identity"` // "local, unverified identity"
	At       time.Time                            `json:"at"`
	Locators map[string]map[string]domain.Locator `json:"locators"`
}

// Screens is the content of the approval as a screens value.
func (a ScreensApproval) Screens() domain.Screens {
	return domain.Screens{APIVersion: "semar/v1", Kind: "Screens", App: a.App, Screens: a.Locators}
}

func screensRel(app string, h domain.Hash) (string, error) {
	if err := idcheck.Check("app name", app); err != nil {
		return "", err
	}
	if err := idcheck.Check("screens hash", string(h)); err != nil {
		return "", err
	}
	rel := filepath.Join("store", "approvals", "screens", app, string(h))
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("path %q is not local", rel)
	}
	return rel, nil
}

// PutScreens keeps the first approval of a user for a screens hash: a later one changes nothing.
func (l *Local) PutScreens(r ScreensApproval) error {
	dir, err := screensRel(r.App, r.Hash)
	if err != nil {
		return fmt.Errorf("store screens approval: %w", err)
	}
	name := filepath.Join(dir, fileName(r.User))
	r.Schema = schema
	data, err := atomicfile.EncodeJSON(r)
	if err != nil {
		return fmt.Errorf("encode screens approval %s: %w", filepath.Join(l.root, name), err)
	}
	root, err := os.OpenRoot(l.root)
	if err != nil {
		return fmt.Errorf("store screens approval: %w", err)
	}
	defer root.Close()
	if _, err := atomicfile.MkdirIn(root, dir); err != nil {
		return fmt.Errorf("store screens approval: %w", err)
	}
	if err := atomicfile.WriteNewIn(root, name, data, atomicfile.FileMode); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("store screens approval: %w", err)
	}
	return nil
}

// ScreensApproved reports whether any user approved the screens of the app at this hash. A record
// that is a link, not a regular file, unreadable, of another schema, or kept under the wrong app or
// hash is an error and not a skip, because the repository under test can commit the store.
func (l *Local) ScreensApproved(app string, h domain.Hash) (bool, error) {
	dir, err := screensRel(app, h)
	if err != nil {
		return false, fmt.Errorf("load screens approval: %w", err)
	}
	recs, err := l.readScreensDir(dir)
	if err != nil {
		return false, err
	}
	for _, r := range recs {
		if r.App != app || r.Hash != h {
			return false, fmt.Errorf("screens approval is for app %q and hash %q, want %q and %q", r.App, r.Hash, app, h)
		}
	}
	return len(recs) > 0, nil
}

// LatestScreens gives the newest approval of the app, whatever its hash. It reports false when the
// app was never approved.
func (l *Local) LatestScreens(app string) (ScreensApproval, bool, error) {
	if err := idcheck.Check("app name", app); err != nil {
		return ScreensApproval{}, false, fmt.Errorf("load screens approval: %w", err)
	}
	base := filepath.Join("store", "approvals", "screens", app)
	root, err := os.OpenRoot(l.root)
	if errors.Is(err, fs.ErrNotExist) {
		return ScreensApproval{}, false, nil
	}
	if err != nil {
		return ScreensApproval{}, false, fmt.Errorf("load screens approval: %w", err)
	}
	defer root.Close()
	if err := atomicfile.CheckDirIn(root, base); err != nil {
		return ScreensApproval{}, false, fmt.Errorf("load screens approval: %w", err)
	}
	d, err := root.Open(base)
	if errors.Is(err, fs.ErrNotExist) {
		return ScreensApproval{}, false, nil
	}
	if err != nil {
		return ScreensApproval{}, false, fmt.Errorf("read screens approvals in %s: %w", filepath.Join(l.root, base), err)
	}
	entries, err := d.ReadDir(-1)
	_ = d.Close()
	if err != nil {
		return ScreensApproval{}, false, fmt.Errorf("read screens approvals in %s: %w", filepath.Join(l.root, base), err)
	}
	var best ScreensApproval
	found := false
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		recs, err := l.readScreensDir(filepath.Join(base, e.Name()))
		if err != nil {
			return ScreensApproval{}, false, err
		}
		for _, r := range recs {
			if !found || r.At.After(best.At) {
				best, found = r, true
			}
		}
	}
	return best, found, nil
}

// readScreensDir reads every approval in one hash directory. A directory that is not there has none.
func (l *Local) readScreensDir(dir string) ([]ScreensApproval, error) {
	root, err := os.OpenRoot(l.root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load screens approval: %w", err)
	}
	defer root.Close()
	if err := atomicfile.CheckDirIn(root, dir); err != nil {
		return nil, fmt.Errorf("load screens approval: %w", err)
	}
	d, err := root.Open(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read screens approvals in %s: %w", filepath.Join(l.root, dir), err)
	}
	entries, err := d.ReadDir(-1)
	_ = d.Close()
	if err != nil {
		return nil, fmt.Errorf("read screens approvals in %s: %w", filepath.Join(l.root, dir), err)
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	var out []ScreensApproval
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		r, err := readScreensApproval(root, filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func readScreensApproval(root *os.Root, name string) (ScreensApproval, error) {
	data, err := readRegular(root, name, "screens approval")
	if err != nil {
		return ScreensApproval{}, err
	}
	p := filepath.Join(root.Name(), name)
	var r ScreensApproval
	if err := decodeStrict(data, &r); err != nil {
		return ScreensApproval{}, fmt.Errorf("parse screens approval %s: %w", p, err)
	}
	if r.Schema != schema {
		return ScreensApproval{}, fmt.Errorf("screens approval %s has schema %d, want %d", p, r.Schema, schema)
	}
	return r, nil
}
