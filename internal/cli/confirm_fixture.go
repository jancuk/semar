package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/confirm"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/yamlfile"
)

const maxFixtureFile = 256 << 10

var fixtureIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)*$`)

func fixturesDir(repo string) string { return filepath.Join(semarDir(repo), "fixtures") }

// loadFixtureForApproval reads .semar/fixtures/<id>.yaml for a person who is about to approve it.
// The ID comes from the command line, so it must match the ID pattern before it names a file, and
// .semar, .semar/fixtures, and the file must be real. Only the rules that name the file and the
// format are checked here; the rest of the rules of a fixture file are the catalogue's.
func loadFixtureForApproval(repo string, id domain.FixtureID) (domain.Fixture, error) {
	if !fixtureIDPattern.MatchString(string(id)) || len(id) > 64 {
		return domain.Fixture{}, fmt.Errorf("invalid fixture ID %q: use lower-case letters, digits, hyphen, and underscore in parts that a dot separates, starting with a letter, at most 64 bytes", id)
	}
	dir := fixturesDir(repo)
	for _, d := range []string{semarDir(repo), dir} {
		info, err := os.Lstat(d)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return domain.Fixture{}, fmt.Errorf("fixture %s was not found: no file %s.yaml or %s.yml in %s", id, id, id, dir)
		case err != nil:
			return domain.Fixture{}, fmt.Errorf("find fixture %s: %w", id, err)
		case info.Mode()&fs.ModeSymlink != 0:
			return domain.Fixture{}, fmt.Errorf("%s is a symbolic link, and Semar reads fixtures only from real directories: replace it with a directory", d)
		case !info.IsDir():
			return domain.Fixture{}, fmt.Errorf("%s is not a directory, and Semar reads fixtures from a directory there", d)
		}
	}
	var found []string
	for _, ext := range []string{".yaml", ".yml"} {
		p := filepath.Join(dir, string(id)+ext)
		info, err := os.Lstat(p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return domain.Fixture{}, fmt.Errorf("find fixture %s: %w", id, err)
		case info.Mode()&fs.ModeSymlink != 0:
			return domain.Fixture{}, fmt.Errorf("%s is a symbolic link, and Semar reads fixtures only from real files: replace it with the file", p)
		case !info.Mode().IsRegular():
			return domain.Fixture{}, fmt.Errorf("%s is not a regular file", p)
		case info.Size() > maxFixtureFile:
			return domain.Fixture{}, fmt.Errorf("%s is larger than %d KiB", p, maxFixtureFile>>10)
		}
		found = append(found, p)
	}
	switch len(found) {
	case 0:
		return domain.Fixture{}, fmt.Errorf("fixture %s was not found: no file %s.yaml or %s.yml in %s", id, id, id, dir)
	case 2:
		return domain.Fixture{}, fmt.Errorf("fixture %s has two files, %s and %s: keep one", id, found[0], found[1])
	}
	return parseFixtureForApproval(found[0], id)
}

func parseFixtureForApproval(path string, id domain.FixtureID) (domain.Fixture, error) {
	f, err := os.Open(path)
	if err != nil {
		return domain.Fixture{}, fmt.Errorf("read fixture %s: %w", path, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFixtureFile+1))
	if err != nil {
		return domain.Fixture{}, fmt.Errorf("read fixture %s: %w", path, err)
	}
	if len(data) > maxFixtureFile {
		return domain.Fixture{}, fmt.Errorf("%s is larger than %d KiB", path, maxFixtureFile>>10)
	}
	var fx domain.Fixture
	if err := yamlfile.Decode(data, path, &fx); err != nil {
		return domain.Fixture{}, err
	}
	// Numbers in a request body and in an until are held exactly, as in a script and a scenario check,
	// so that the hash a person approves is the hash of the value that a run sends.
	numbers := func(key string) (yamlfile.NumberKind, bool) {
		return yamlfile.AnyNumber, key == "json" || key == "equals"
	}
	if err := yamlfile.ExactNumbers(data, path, numbers); err != nil {
		return domain.Fixture{}, err
	}
	for _, phase := range []struct {
		name  string
		steps []domain.FixtureStep
	}{{domain.PhaseFind, fx.Find}, {domain.PhaseCreate, fx.Create}, {domain.PhaseCleanup, fx.Cleanup}} {
		for i := range phase.steps {
			st := &phase.steps[i]
			where := fmt.Sprintf("%s[%d]", phase.name, i)
			if st.Request.JSON, err = yamlfile.JSONable(st.Request.JSON); err != nil {
				return domain.Fixture{}, fmt.Errorf("%s: %s.request.json: %w", path, where, err)
			}
			if st.Until != nil {
				if st.Until.Equals, err = yamlfile.JSONable(st.Until.Equals); err != nil {
					return domain.Fixture{}, fmt.Errorf("%s: %s.until.equals: %w", path, where, err)
				}
			}
		}
	}
	switch {
	case fx.APIVersion != "semar/v1":
		return domain.Fixture{}, fmt.Errorf("%s: apiVersion must be semar/v1", path)
	case fx.Kind != "Fixture":
		return domain.Fixture{}, fmt.Errorf("%s: kind must be Fixture", path)
	case fx.ID != id:
		return domain.Fixture{}, fmt.Errorf("%s: id %s does not match the file name %s", path, fx.ID, filepath.Base(path))
	}
	return fx, nil
}

// fixtureLines is what a person reads before approving a fixture: the header and then everything
// that its hash covers, one line each.
func fixtureLines(fx domain.Fixture, h domain.Hash) []string {
	lines := []string{fmt.Sprintf("Fixture %s %s (owner %s), hash %s:", fx.ID, fx.Version, fx.Owner, h)}
	add := func(format string, args ...any) { lines = append(lines, "  "+fmt.Sprintf(format, args...)) }
	add("description: %s", fx.Description)
	if len(fx.Params) > 0 {
		var ps []string
		for _, name := range slices.Sorted(maps.Keys(fx.Params)) {
			if d := fx.Params[name].Default; d != nil {
				ps = append(ps, fmt.Sprintf("%s (default %q)", name, *d))
			} else {
				ps = append(ps, name+" (required)")
			}
		}
		add("parameters: %s", strings.Join(ps, ", "))
	}
	if fx.Lease != "" {
		add("lease: %s", fx.Lease)
	}
	add("outputs: %s", strings.Join(fx.Outputs, ", "))
	for _, phase := range []struct {
		name  string
		steps []domain.FixtureStep
	}{{domain.PhaseFind, fx.Find}, {domain.PhaseCreate, fx.Create}, {domain.PhaseCleanup, fx.Cleanup}} {
		for i, st := range phase.steps {
			add("%s", fixtureStepLine(phase.name, i+1, st))
		}
	}
	return lines
}

func fixtureStepLine(phase string, n int, st domain.FixtureStep) string {
	r := st.Request
	var b strings.Builder
	fmt.Fprintf(&b, "%s %d: %s %s %s", phase, n, r.Method, r.Service, r.Path)
	pairs := func(label string, m map[string]string) {
		if len(m) == 0 {
			return
		}
		b.WriteString(" " + label)
		for _, k := range slices.Sorted(maps.Keys(m)) {
			fmt.Fprintf(&b, " %s=%s", k, m[k])
		}
	}
	pairs("query", r.Query)
	pairs("headers", r.Headers)
	if r.JSON != nil {
		b.WriteString(" json " + compactJSON(r.JSON))
	}
	if u := st.Until; u != nil {
		fmt.Fprintf(&b, " until %s equals %s within %s", u.JSONPath, compactJSON(u.Equals), u.Timeout)
		if u.Every > 0 {
			fmt.Fprintf(&b, " every %s", u.Every)
		}
	}
	if len(st.Captures) > 0 {
		names := make([]string, len(st.Captures))
		for i, c := range st.Captures {
			names[i] = c.Name
		}
		b.WriteString(", captures " + strings.Join(names, ", "))
	}
	return b.String()
}

// oneLine shows a line feed or a tab as an escape, so that a value cannot end its line and start
// another that looks like a step.
func oneLine(s string) string {
	return strings.NewReplacer("\n", `\n`, "\t", `\t`).Replace(s)
}

func compactJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Sprintf("(%v)", err)
	}
	return strings.TrimSpace(buf.String())
}

// approveFixtures shows each named fixture, asks once, and stores one approval for the hash of
// each. A fixture that is already approved at its current hash is left as it is.
func approveFixtures(ctx context.Context, stdout, stderr io.Writer, o options, f confirmFlags) error {
	type pending struct {
		fx domain.Fixture
		h  domain.Hash
	}
	approvals := confirm.NewLocal(semarDir(f.dir))
	var todo []pending
	seen := map[string]bool{}
	for _, raw := range f.fixtures {
		if seen[raw] {
			continue
		}
		seen[raw] = true
		fx, err := loadFixtureForApproval(f.dir, domain.FixtureID(raw))
		if err != nil {
			return err
		}
		h := domain.FixtureHash(fx)
		if h == "" {
			return fmt.Errorf("fixture %s cannot be hashed, so it cannot be approved", fx.ID)
		}
		done, err := approvals.FixtureApproved(fx.ID, h)
		if err != nil {
			return err
		}
		if done {
			fmt.Fprintf(stdout, "fixture %s %s is already approved for hash %s\n", fx.ID, fx.Version, h)
			continue
		}
		todo = append(todo, pending{fx, h})
	}
	if len(todo) == 0 {
		return nil
	}
	for _, p := range todo {
		for _, line := range fixtureLines(p.fx, p.h) {
			fmt.Fprintln(stdout, oneLine(terminalSafe(line)))
		}
	}

	if !f.yes {
		if !o.Terminal() {
			fmt.Fprintln(stderr, "semar: nothing was approved: the input is not a terminal, so pass --yes to approve")
			return exitError{ExitUndecided}
		}
		question := "Approve it? [y/N] "
		if len(todo) > 1 {
			question = "Approve them? [y/N] "
		}
		fmt.Fprint(stdout, question)
		if a := strings.ToLower(strings.TrimSpace(readAnswer(ctx, o.Stdin))); a != "y" && a != "yes" {
			fmt.Fprintln(stdout, "nothing was approved")
			return exitError{ExitUndecided}
		}
	}

	name, err := o.User()
	if err != nil {
		return err
	}
	if strings.TrimSpace(name) == "" {
		return errors.New("the approver has no name; set git config user.name")
	}
	store, err := evidence.Open(semarDir(f.dir))
	if err != nil {
		return err
	}
	approvals = confirm.NewLocal(store.Root())
	for _, p := range todo {
		rec := confirm.FixtureApproval{
			Fixture:  p.fx.ID,
			Version:  p.fx.Version,
			Hash:     p.h,
			User:     name,
			Identity: localIdentity,
			At:       o.Clock().UTC(),
		}
		if err := approvals.PutFixture(rec); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "approved fixture %s %s for hash %s as %s (%s)\n", p.fx.ID, p.fx.Version, p.h, name, localIdentity)
	}
	return nil
}
