package obligation

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
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/idcheck"
)

const (
	recordSchema = 1
	// LocalIdentity is what every record says about who made it. A name from the person's own
	// environment proves nothing (FR-11).
	LocalIdentity = "local, unverified identity"

	maxReason     = 300
	maxNotes      = 2000
	maxBuilds     = 32
	maxBuildField = 128
	maxDeferDays  = 180
	maxRecordSize = 64 << 10

	kindDeferral = "deferral"
	kindManual   = "manual"
	kindLink     = "link"
)

// Deferral is a person's decision to leave an obligation untested until a day.
type Deferral struct {
	Schema     int       `json:"schema"`
	Obligation string    `json:"obligation"`
	User       string    `json:"user"`
	Identity   string    `json:"identity"`
	At         time.Time `json:"at"`
	Reason     string    `json:"reason"`
	Until      time.Time `json:"until"` // the end of that day, UTC
}

// ManualResult is what a person saw when they ran the manual checklist.
type ManualResult struct {
	Schema     int       `json:"schema"`
	Obligation string    `json:"obligation"`
	Result     string    `json:"result"` // passed | failed
	User       string    `json:"user"`
	Identity   string    `json:"identity"`
	At         time.Time `json:"at"`
	Notes      string    `json:"notes"`
	// Builds are the builds that the environment ran when the person recorded the result (Plan 21,
	// D16). It is empty for a result recorded without --env.
	Builds []ManualBuild `json:"builds,omitempty"`
}

// ManualBuild is a service and the commit that it ran.
type ManualBuild struct {
	Service string `json:"service"`
	Commit  string `json:"commit"`
}

// Link says that a scenario does the manual check of an obligation. A person made the link: Semar
// never joins a scenario to a manual check on its own.
type Link struct {
	Schema     int               `json:"schema"`
	Obligation string            `json:"obligation"`
	Scenario   domain.ScenarioID `json:"scenario"`
	User       string            `json:"user"`
	Identity   string            `json:"identity"`
	At         time.Time         `json:"at"`
}

// Store keeps deferrals and manual results at <root>/store/obligations/<group>/<name>/<kind>/.
// A new record never replaces an old one, and the newest counts.
type Store struct{ root string }

// NewStore returns a store in the evidence store root, the folder that holds store/.
func NewStore(root string) *Store { return &Store{root: root} }

var userCleaner = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// userFile turns a user name into a safe part of a file name, as confirmations do.
func userFile(user string) string {
	clean := userCleaner.ReplaceAllString(user, "_")
	if strings.Trim(clean, ".") == "" {
		clean = "unknown"
	}
	return clean
}

func splitID(id string) (group, name string, err error) {
	group, name, ok := strings.Cut(id, "/")
	if !ok || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("obligation %q must be <group>/<name>", id)
	}
	if err := idcheck.Check("obligation group", group); err != nil {
		return "", "", err
	}
	if err := idcheck.Check("obligation name", name); err != nil {
		return "", "", err
	}
	return group, name, nil
}

func recordDir(id, kind string) (string, error) {
	group, name, err := splitID(id)
	if err != nil {
		return "", err
	}
	rel := filepath.Join("store", "obligations", group, name, kind)
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("path %q is not local", rel)
	}
	return rel, nil
}

func oneLineText(field, s string, limit int, required bool) error {
	switch {
	case strings.TrimSpace(s) == "":
		if required {
			return fmt.Errorf("%s is required", field)
		}
	case multiline(s):
		return fmt.Errorf("%s must be one line", field)
	case utf8.RuneCountInString(s) > limit:
		return fmt.Errorf("%s is longer than %d characters", field, limit)
	}
	return nil
}

// Defer stores a deferral. It needs a name, a reason, and an expiry (L7).
func (s *Store) Defer(d Deferral) error {
	if err := checkDeferral(d); err != nil {
		return fmt.Errorf("store deferral: %w", err)
	}
	if d.Identity == "" {
		d.Identity = LocalIdentity
	}
	d.Schema = recordSchema
	return s.put(d.Obligation, kindDeferral, d.User, d.At, d)
}

// Record stores a manual result. It never changes a state (L5).
func (s *Store) Record(m ManualResult) error {
	if err := checkManual(m); err != nil {
		return fmt.Errorf("store manual result: %w", err)
	}
	if m.Identity == "" {
		m.Identity = LocalIdentity
	}
	m.Schema = recordSchema
	return s.put(m.Obligation, kindManual, m.User, m.At, m)
}

// Link stores the decision of a person that a scenario does the manual check of an obligation.
func (s *Store) Link(l Link) error {
	if err := checkLink(l); err != nil {
		return fmt.Errorf("store link: %w", err)
	}
	if l.Identity == "" {
		l.Identity = LocalIdentity
	}
	l.Schema = recordSchema
	return s.put(l.Obligation, kindLink, l.User, l.At, l)
}

func checkLink(l Link) error {
	switch {
	case strings.TrimSpace(l.User) == "":
		return errors.New("the user is required")
	case l.At.IsZero():
		return errors.New("at is required")
	}
	return idcheck.Scenario(string(l.Scenario))
}

// Links returns the newest valid link of each obligation, with the problems of the records that it
// left out, as Deferrals does.
func (s *Store) Links(now time.Time) (map[string]Link, []string, error) {
	return readAll[Link](s, kindLink, now, func(l Link) recordHead {
		return recordHead{l.Obligation, l.User, l.At, l.Schema, checkLink(l)}
	})
}

// Root is the folder that holds store/.
func (s *Store) Root() string { return s.root }

// checkDeferral is what a deferral needs, when it is written and when it is read: a name, a
// reason, a day to end it, and no more than maxDeferDays after the day it was made.
func checkDeferral(d Deferral) error {
	switch {
	case strings.TrimSpace(d.User) == "":
		return errors.New("the user is required")
	case d.Until.IsZero():
		return errors.New("until is required")
	case d.At.IsZero():
		return errors.New("at is required")
	}
	if err := oneLineText("reason", d.Reason, maxReason, true); err != nil {
		return err
	}
	if limit := d.At.UTC().Truncate(24*time.Hour).AddDate(0, 0, maxDeferDays).Add(24*time.Hour - time.Second); d.Until.After(limit) {
		return fmt.Errorf("until %s is more than %d days after the deferral was made", d.Until.UTC().Format(time.RFC3339), maxDeferDays)
	}
	return nil
}

// checkManual is what a manual result needs, when it is written and when it is read.
func checkManual(m ManualResult) error {
	switch {
	case m.Result != "passed" && m.Result != "failed":
		return errors.New("the result must be passed or failed")
	case strings.TrimSpace(m.User) == "":
		return errors.New("the user is required")
	case m.At.IsZero():
		return errors.New("at is required")
	case utf8.RuneCountInString(m.Notes) > maxNotes:
		return fmt.Errorf("the notes are longer than %d characters", maxNotes)
	case len(m.Builds) > maxBuilds:
		return fmt.Errorf("a result names at most %d builds, not %d", maxBuilds, len(m.Builds))
	}
	for _, b := range m.Builds {
		if err := oneLineText("the service of a build", b.Service, maxBuildField, true); err != nil {
			return err
		}
		if err := oneLineText("the commit of a build", b.Commit, maxBuildField, true); err != nil {
			return err
		}
	}
	return nil
}

// checkRecordName refuses a record whose file name was not made by put from its user and its time:
// <user>-<unix seconds>.json, or with -<n> before the extension.
func checkRecordName(file, user string, at time.Time) error {
	want := userFile(user) + "-" + strconv.FormatInt(at.Unix(), 10)
	base := strings.TrimSuffix(file, ".json")
	rest, ok := strings.CutPrefix(base, want)
	if ok && rest != "" {
		n, found := strings.CutPrefix(rest, "-")
		_, err := strconv.ParseUint(n, 10, 16)
		ok = found && err == nil
	}
	if !ok {
		return fmt.Errorf("its name does not say its time: want %s.json for at %s", want, at.UTC().Format(time.RFC3339))
	}
	return nil
}

// put writes <user>-<unix seconds>.json, or the first free <user>-<seconds>-2.json, and so on.
// Every write goes through a root at the store, and every directory down to the record must
// be a real one, because the repository under test can commit a link there.
func (s *Store) put(id, kind, user string, at time.Time, rec any) error {
	dir, err := recordDir(id, kind)
	if err != nil {
		return fmt.Errorf("store %s: %w", kind, err)
	}
	data, err := atomicfile.EncodeJSON(rec)
	if err != nil {
		return fmt.Errorf("encode %s of %s: %w", kind, id, err)
	}
	if err := os.MkdirAll(s.root, atomicfile.DirMode); err != nil {
		return fmt.Errorf("store %s: %w", kind, err)
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return fmt.Errorf("store %s: %w", kind, err)
	}
	defer root.Close()
	if _, err := atomicfile.MkdirIn(root, dir); err != nil {
		return fmt.Errorf("store %s: %w", kind, err)
	}
	base := userFile(user) + "-" + strconv.FormatInt(at.Unix(), 10)
	for n := 1; n <= 1000; n++ {
		name := base + ".json"
		if n > 1 {
			name = base + "-" + strconv.Itoa(n) + ".json"
		}
		err := atomicfile.WriteNewIn(root, filepath.Join(dir, name), data, atomicfile.FileMode)
		if err == nil {
			return nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("store %s: %w", kind, err)
		}
	}
	return fmt.Errorf("store %s: %d records of %s were made in one second already", kind, 1000, id)
}

// maxClockSkew is how far after now a record may say it was made: the clock of the machine that
// wrote it is not ours.
const maxClockSkew = 5 * time.Minute

// recordHead is what readAll needs from a record, and check is the rules that the writer applied.
type recordHead struct {
	obligation string
	user       string
	at         time.Time
	schema     int
	check      error
}

// Deferrals returns the newest valid deferral of each obligation, even one that has expired. The
// store is not trusted: a record that the writer would have refused, one that says it was made
// after now, and one whose file name does not say its time are left out, and each is a problem line
// of the second result. A record that cannot be read at all is an error.
func (s *Store) Deferrals(now time.Time) (map[string]Deferral, []string, error) {
	return readAll[Deferral](s, kindDeferral, now, func(d Deferral) recordHead {
		return recordHead{d.Obligation, d.User, d.At, d.Schema, checkDeferral(d)}
	})
}

// Manual returns the newest valid manual result of each obligation, with the problems of the
// records that it left out, as Deferrals does.
func (s *Store) Manual(now time.Time) (map[string]ManualResult, []string, error) {
	return readAll[ManualResult](s, kindManual, now, func(m ManualResult) recordHead {
		return recordHead{m.Obligation, m.User, m.At, m.Schema, checkManual(m)}
	})
}

func readAll[T any](s *Store, kind string, now time.Time, head func(T) recordHead) (map[string]T, []string, error) {
	out := map[string]T{}
	var problems []string
	root, err := os.OpenRoot(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read %ss: %w", kind, err)
	}
	defer root.Close()
	base := filepath.Join("store", "obligations")
	if err := atomicfile.CheckDirIn(root, base); err != nil {
		return nil, nil, fmt.Errorf("read %ss: %w", kind, err)
	}
	newest := map[string]time.Time{}
	newestName := map[string]string{}
	groups, err := subdirs(root, base)
	if err != nil {
		return nil, nil, err
	}
	for _, g := range groups {
		names, err := subdirs(root, filepath.Join(base, g))
		if err != nil {
			return nil, nil, err
		}
		for _, n := range names {
			dir := filepath.Join(base, g, n, kind)
			files, err := jsonFiles(root, dir)
			if err != nil {
				return nil, nil, err
			}
			for _, f := range files {
				name := filepath.Join(dir, f)
				path := filepath.Join(root.Name(), name)
				data, err := readRecord(root, name)
				if err != nil {
					return nil, nil, err
				}
				var rec T
				if err := decodeStrict(data, &rec); err != nil {
					return nil, nil, fmt.Errorf("parse %s %s: %w", kind, path, err)
				}
				h := head(rec)
				if h.schema != recordSchema {
					return nil, nil, fmt.Errorf("%s %s has schema %d, want %d", kind, path, h.schema, recordSchema)
				}
				if h.obligation != g+"/"+n {
					return nil, nil, fmt.Errorf("%s %s is for obligation %q, and its directory says %q", kind, path, h.obligation, g+"/"+n)
				}
				problem := h.check
				if problem == nil && h.at.After(now.Add(maxClockSkew)) {
					problem = fmt.Errorf("it says it was made at %s, which is after now", h.at.UTC().Format(time.RFC3339))
				}
				if problem == nil {
					problem = checkRecordName(f, h.user, h.at)
				}
				if problem != nil {
					problems = append(problems, fmt.Sprintf("%s record %s is not counted: %v", kind, path, problem))
					continue
				}
				id, at := h.obligation, h.at
				if prev, ok := newest[id]; !ok || at.After(prev) || (at.Equal(prev) && strings.TrimSuffix(f, ".json") > strings.TrimSuffix(newestName[id], ".json")) {
					newest[id], newestName[id], out[id] = at, f, rec
				}
			}
		}
	}
	return out, problems, nil
}

func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("data follows the record")
	}
	return nil
}

// subdirs lists the directories of dir that can be an ID part. A link is an error, and any
// other entry is not ours and is left alone.
func subdirs(root *os.Root, dir string) ([]string, error) {
	entries, err := readDir(root, dir)
	if err != nil || entries == nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		switch {
		case e.Type()&fs.ModeSymlink != 0:
			return nil, fmt.Errorf("%s is a symbolic link, and Semar reads records only from real directories: replace it with a directory", filepath.Join(root.Name(), dir, e.Name()))
		case !e.IsDir() || idcheck.Check("part", e.Name()) != nil:
		default:
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// jsonFiles lists the .json entries of dir that are not directories. A link is refused later,
// by readRecord, so that its message names the record.
func jsonFiles(root *os.Root, dir string) ([]string, error) {
	entries, err := readDir(root, dir)
	if err != nil || entries == nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// readDir returns the entries of dir sorted by name, or nil when dir does not exist.
func readDir(root *os.Root, dir string) ([]fs.DirEntry, error) {
	if err := atomicfile.CheckDirIn(root, dir); err != nil {
		return nil, err
	}
	d, err := root.Open(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read records in %s: %w", filepath.Join(root.Name(), dir), err)
	}
	entries, err := d.ReadDir(-1)
	_ = d.Close()
	if err != nil {
		return nil, fmt.Errorf("read records in %s: %w", filepath.Join(root.Name(), dir), err)
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries, nil
}

// readRecord refuses a record that is a link or not a regular file, and one that was swapped
// between the check and the open.
func readRecord(root *os.Root, name string) ([]byte, error) {
	p := filepath.Join(root.Name(), name)
	info, err := root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("read record %s: %w", p, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("record %s is a symbolic link, and Semar reads only real files: replace it with the record", p)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("record %s is not a regular file", p)
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("read record %s: %w", p, err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("read record %s: %w", p, err)
	}
	if !os.SameFile(info, opened) {
		return nil, fmt.Errorf("record %s changed while it was read", p)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxRecordSize+1))
	if err != nil {
		return nil, fmt.Errorf("read record %s: %w", p, err)
	}
	if len(data) > maxRecordSize {
		return nil, fmt.Errorf("record %s is larger than %d KiB", p, maxRecordSize>>10)
	}
	return data, nil
}
