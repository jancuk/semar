package obligation_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/obligation"
)

func readFile(name string) ([]byte, error) { return os.ReadFile(filepath.Join("testdata", name)) }

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := readFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// problemsOf returns every problem of a document as "<file>:<line>: <path>: <message>", from the
// parse or from the validation.
func problemsOf(t *testing.T, doc string) []string {
	t.Helper()
	f, err := obligation.Parse([]byte(doc), "o.yaml")
	if err != nil {
		var pe *obligation.ParseError
		if !errors.As(err, &pe) {
			t.Fatalf("Parse returned %T, want *ParseError: %v", err, err)
		}
		var out []string
		for _, p := range pe.Problems {
			out = append(out, p.String())
		}
		return out
	}
	var out []string
	for _, p := range obligation.Validate(f, "o.yaml") {
		out = append(out, p.String())
	}
	return out
}

const head = "apiVersion: semar/v1\nkind: Obligations\n"

func entry(body string) string {
	return head + "obligations:\n  - id: refunds/a\n    title: T\n    source: diff\n" + body
}

func TestParseTheExample(t *testing.T) {
	f, err := obligation.Parse(readTestdata(t, "example.yaml"), "example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if ps := obligation.Validate(f, "example.yaml"); len(ps) > 0 {
		t.Fatalf("the example has problems: %v", ps)
	}
	if f.Ticket != "SHOP-482" || len(f.Obligations) != 4 {
		t.Fatalf("ticket %q with %d obligations", f.Ticket, len(f.Obligations))
	}
	wantIDs := []string{"SHOP-482/scenario-SHOP-482-01", "SHOP-482/not-drafted-1", "SHOP-482/uncovered-1", "SHOP-482/difference-1"}
	for i, o := range f.Obligations {
		if o.ID != wantIDs[i] {
			t.Errorf("obligation %d has id %q, want %q", i, o.ID, wantIDs[i])
		}
	}
	first := f.Obligations[0]
	if first.Source != obligation.SourceDiff || len(first.Covers) != 1 || first.Covers[0] != "SHOP-482-01" {
		t.Errorf("first obligation is %+v", first)
	}
	if got := f.Obligations[1].Gap; got != "not drafted: needs fixture order.paid" {
		t.Errorf("gap is %q", got)
	}
	m := f.Obligations[3].Manual
	if m == nil || m.Kind != "judgement" || m.WrittenBy != "code" || len(m.Steps) != 3 || m.Expected != "The document and the code agree" {
		t.Errorf("manual is %+v", m)
	}
	if f.Obligations[3].Cite != "docs/refunds.md:14" {
		t.Errorf("cite is %q", f.Obligations[3].Cite)
	}

	hand, err := obligation.Parse(readTestdata(t, "hand.yaml"), "hand.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if ps := obligation.Validate(hand, "hand.yaml"); len(ps) > 0 {
		t.Fatalf("the hand-written file has problems: %v", ps)
	}
	if o := hand.Obligations[0]; o.Source != obligation.SourceTestcase || o.Manual == nil || o.Manual.WrittenBy != "person" {
		t.Errorf("hand-written obligation is %+v", o)
	}
}

func TestValidateRules(t *testing.T) {
	long := func(n int) string { return strings.Repeat("a", n) }
	tests := []struct {
		name string
		doc  string
		want string // a substring of one problem. Empty means no problem
	}{
		{"valid", entry(""), ""},
		{"no cover, no gap, no manual is valid", entry(""), ""},

		// O1
		{"O1 unknown key", head + "colour: red\nobligations: []\n", "unknown key colour"},
		{"O1 unknown key in an entry", entry("    colour: red\n"), "o.yaml:7: obligations[0]: unknown key colour"},
		{"O1 two documents", entry("") + "---\nkind: Obligations\n", "more than one YAML document"},
		{"O1 empty", "", "the file is empty"},
		{"O1 too large", "apiVersion: semar/v1\nkind: Obligations\n# " + long(256*1024) + "\nobligations: []\n", "larger than 256 KiB"},
		{"O1 too many", head + "obligations:\n" + strings.Repeat("  - {id: g/a, title: T, source: diff}\n", 501), "more than 500 obligations"},

		// O2
		{"O2 apiVersion", "apiVersion: semar/v2\nkind: Obligations\nobligations: []\n", "apiVersion must be semar/v1"},
		{"O2 kind", "apiVersion: semar/v1\nkind: Obligation\nobligations: []\n", "o.yaml:2: kind: kind must be Obligations"},
		{"O2 ticket", head + "ticket: ../x\nobligations: []\n", "ticket: invalid ticket"},

		// O3
		{"O3 no group", strings.Replace(entry(""), "refunds/a", "refunds", 1), "id must be <group>/<name>"},
		{"O3 three parts", strings.Replace(entry(""), "refunds/a", "refunds/a/b", 1), "id must be <group>/<name>"},
		{"O3 bad group", strings.Replace(entry(""), "refunds/a", "../a", 1), "invalid id group"},
		{"O3 bad name", strings.Replace(entry(""), "refunds/a", "refunds/.a", 1), "invalid id name"},
		{"O3 too long", strings.Replace(entry(""), "refunds/a", "refunds/"+long(121), 1), "id is longer than 128 bytes"},
		{"O3 missing", strings.Replace(entry(""), "  - id: refunds/a\n    title", "  - title", 1), "id is required"},
		{"O3 group is the ticket", head + "ticket: SHOP-482\nobligations:\n  - id: refunds/a\n    title: T\n    source: diff\n", "id group must be the ticket SHOP-482"},
		{"O3 duplicate in a file", head + "obligations:\n  - {id: g/a, title: T, source: diff}\n  - {id: g/a, title: U, source: diff}\n", "id g/a is also at obligations[0]"},

		// O4
		{"O4 missing", strings.Replace(entry(""), "    title: T\n", "", 1), "title is required"},
		{"O4 blank", strings.Replace(entry(""), "title: T", `title: "  "`, 1), "title is required"},
		{"O4 two lines", strings.Replace(entry(""), "title: T", `title: "a\nb"`, 1), "title must be one line"},
		{"O4 too long", strings.Replace(entry(""), "title: T", "title: "+long(301), 1), "title is longer than 300 characters"},
		{"O4 300 characters is fine", strings.Replace(entry(""), "title: T", "title: "+long(300), 1), ""},

		// O5
		{"O5 missing", strings.Replace(entry(""), "    source: diff\n", "", 1), "source is required; use one of"},
		{"O5 unknown", strings.Replace(entry(""), "source: diff", "source: review", 1), "source review is not a source; use one of criterion, diff, knowledge, testcase, feature_map, defect"},

		// O6
		{"O6 testcase needs a cite", strings.Replace(entry(""), "source: diff", "source: testcase", 1), "cite is required for the source testcase"},
		{"O6 criterion needs a cite", strings.Replace(entry(""), "source: diff", "source: criterion", 1), "cite is required for the source criterion"},
		{"O6 defect needs a cite", strings.Replace(entry(""), "source: diff", "source: defect", 1), "cite is required for the source defect"},
		{"O6 diff needs none", entry(""), ""},
		{"O6 too long", entry("    cite: " + long(501) + "\n"), "cite is longer than 500 characters"},

		// O7
		{"O7 covers", entry("    covers: [\"../x\"]\n"), "obligations[0].covers[0]: invalid scenario ID"},
		{"O7 journeys", entry("    journeys: [ok, \"a b\"]\n"), "obligations[0].journeys[1]: invalid journey ID"},

		// O8
		{"O8 two lines", entry("    gap: \"a\\nb\"\n"), "gap must be one line"},
		{"O8 too long", entry("    gap: " + long(301) + "\n"), "gap is longer than 300 characters"},

		// O9
		{"O9 kind", entry("    manual: {kind: robot, text: t, steps: [s], expected: e, writtenBy: person}\n"), "manual.kind must be one of physical_device, third_party_otp, judgement, other"},
		{"O9 text", entry("    manual: {kind: other, steps: [s], expected: e, writtenBy: person}\n"), "manual.text is required"},
		{"O9 expected", entry("    manual: {kind: other, text: t, steps: [s], writtenBy: person}\n"), "manual.expected is required"},
		{"O9 no steps", entry("    manual: {kind: other, text: t, expected: e, writtenBy: person}\n"), "manual.steps needs at least one step"},
		{"O9 21 steps", entry("    manual: {kind: other, text: t, expected: e, writtenBy: person, steps: [" + strings.TrimSuffix(strings.Repeat("s, ", 21), ", ") + "]}\n"), "manual.steps has more than 20 steps"},
		{"O9 two-line step", entry("    manual: {kind: other, text: t, expected: e, writtenBy: person, steps: [\"a\\nb\"]}\n"), "manual.steps[0] must be one line"},
		{"O9 writtenBy", entry("    manual: {kind: other, text: t, steps: [s], expected: e, writtenBy: robot}\n"), "manual.writtenBy must be person, code, or ai"},
		{"O9 a good manual reason", entry("    manual: {kind: third_party_otp, text: t, steps: [s], expected: e, writtenBy: ai}\n"), ""},

		// O10
		{"O10 entry ticket differs", head + "ticket: SHOP-482\nobligations:\n  - {id: SHOP-482/a, title: T, source: diff, ticket: SHOP-480}\n", "ticket SHOP-480 differs from the file's ticket SHOP-482"},
		{"O10 entry ticket in a file with none", entry("    ticket: SHOP-480\n"), ""},
		{"O10 same ticket", head + "ticket: SHOP-482\nobligations:\n  - {id: SHOP-482/a, title: T, source: diff, ticket: SHOP-482}\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := problemsOf(t, tt.doc)
			if tt.want == "" {
				if len(got) > 0 {
					t.Fatalf("problems: %v", got)
				}
				return
			}
			for _, p := range got {
				if strings.Contains(p, tt.want) {
					if !strings.HasPrefix(p, "o.yaml:") {
						t.Errorf("problem %q does not start with the file", p)
					}
					return
				}
			}
			t.Fatalf("no problem contains %q; got %v", tt.want, got)
		})
	}
}

func TestStateKeyRefused(t *testing.T) {
	got := problemsOf(t, entry("    state: verified\n"))
	want := "o.yaml:7: obligations[0]: unknown key state: a state is computed by code and cannot be written"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %q, want [%q]", got, want)
	}
	top := problemsOf(t, head+"state: verified\nobligations: []\n")
	if len(top) != 1 || !strings.Contains(top[0], "a state is computed by code") {
		t.Fatalf("top-level state: %q", top)
	}
}

func TestProblemLineFollowsTheKey(t *testing.T) {
	doc := head + "obligations:\n  - id: g/a\n    title: T\n    source: review\n"
	got := problemsOf(t, doc)
	if len(got) != 1 || !strings.HasPrefix(got[0], "o.yaml:6: obligations[0].source: ") {
		t.Fatalf("got %q", got)
	}
}

func TestTitleWithEscapeIsData(t *testing.T) {
	doc := head + "obligations:\n  - id: g/a\n    title: \"hello \\e[2J\"\n    source: diff\n"
	f, err := obligation.Parse([]byte(doc), "o.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if ps := obligation.Validate(f, "o.yaml"); len(ps) > 0 {
		t.Fatalf("problems: %v", ps)
	}
	title := f.Obligations[0].Title
	if title != "hello \x1b[2J" {
		t.Fatalf("title is %q", title)
	}
	if safe := obligation.Safe(title); strings.ContainsRune(safe, 0x1b) || safe != `hello \x1b[2J` {
		t.Errorf("Safe gave %q", safe)
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	f, err := obligation.Parse(readTestdata(t, "example.yaml"), "example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	data, err := obligation.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	back, err := obligation.Parse(data, "back.yaml")
	if err != nil {
		t.Fatalf("%v\n%s", err, data)
	}
	if !reflect.DeepEqual(back.Obligations, f.Obligations) || back.Ticket != f.Ticket {
		t.Fatalf("round trip changed the file:\n%s", data)
	}
}

func TestMarshalRefusesInvalidUTF8(t *testing.T) {
	f := obligation.File{Obligations: []obligation.Obligation{{ID: "g/a", Title: "\xff", Source: obligation.SourceDiff}}}
	if _, err := obligation.Marshal(f); err == nil {
		t.Fatal("Marshal wrote text that is not UTF-8")
	}
}
