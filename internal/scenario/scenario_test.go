package scenario_test

import (
	"math"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/scenario"
)

func load(t *testing.T) domain.Scenario {
	t.Helper()
	s, err := scenario.Load("testdata/valid.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParseRejectsUnknownKey(t *testing.T) {
	_, err := scenario.Parse([]byte("apiVersion: semar/v1\nkind: Scenario\nid: A-1\nsteps:\n  - id: s\n    expects: []\n"), "a.yaml")
	if err == nil || !strings.Contains(err.Error(), "a.yaml:6") || !strings.Contains(err.Error(), "expects") {
		t.Fatalf("want an error that names a.yaml:6 and the key, got %v", err)
	}
}

func TestParseRejectsMalformedYAML(t *testing.T) {
	_, err := scenario.Parse([]byte("id: [unclosed\n"), "b.yaml")
	if err == nil || !strings.Contains(err.Error(), "b.yaml") {
		t.Fatalf("want an error that names b.yaml, got %v", err)
	}
}

func TestParseGivesEncodableCheckValues(t *testing.T) {
	s := load(t)
	if got := domain.ExpectHash(s); len(got) != 64 {
		t.Fatalf("hash of a parsed scenario = %q, want 64 hex characters", got)
	}
	nested := "apiVersion: semar/v1\nkind: Scenario\nid: A-1\nsteps:\n  - id: s\n    expect:\n      - check: {jsonpath: $.a, equals: {k: [1, {m: x}]}}\n        source: diff\n"
	n, err := scenario.Parse([]byte(nested), "n.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got := domain.ExpectHash(n); got == "" {
		t.Fatal("a nested mapping in a check value must be encodable")
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name   string
		change func(*domain.Scenario)
		origin scenario.Origin
		want   string // substring of one Problem.Message. Empty means no problem
		path   string // Problem.Path of that problem. Empty means any
	}{
		{"valid", func(*domain.Scenario) {}, scenario.FromFile, "", ""},
		{"wrong apiVersion", func(s *domain.Scenario) { s.APIVersion = "semar/v2" }, scenario.FromFile, "apiVersion must be semar/v1", "apiVersion"},
		{"no ticket", func(s *domain.Scenario) { s.Ticket = "" }, scenario.FromFile, "ticket is required", "ticket"},
		{"unknown surface", func(s *domain.Scenario) { s.Surface = "ios" }, scenario.FromFile, "surface must be api, web, or android", "surface"},
		{"bad scenario id", func(s *domain.Scenario) { s.ID = "-x y" }, scenario.FromFile, "id must match", "id"},
		{"bad step id", func(s *domain.Scenario) { s.Steps[0].ID = "a b" }, scenario.FromFile, "id must match", "steps[0].id"},
		{"duplicate step id", func(s *domain.Scenario) { s.Steps[1].ID = s.Steps[0].ID }, scenario.FromFile, "duplicate step id", "steps[1].id"},
		{"setup with an expected result", func(s *domain.Scenario) { s.Steps[0].Expect = s.Steps[1].Expect }, scenario.FromFile, "a setup step has no expected result", "steps[0].expect"},
		{"two check kinds", func(s *domain.Scenario) { s.Steps[1].Expect[0].Check.JSONPath = "$.a" }, scenario.FromFile, "exactly one of status, jsonpath, header", "steps[1].expect[0].check"},
		{"jsonpath with no operator", func(s *domain.Scenario) { s.Steps[2].Expect[0].Check.Equals = nil }, scenario.FromFile, "exactly one operator", "steps[2].expect[0].check"},
		{"jsonpath that does not parse", func(s *domain.Scenario) { s.Steps[2].Expect[0].Check.JSONPath = "$..[" }, scenario.FromFile, "jsonpath", "steps[2].expect[0].check.jsonpath"},
		{"invariant in a file", func(s *domain.Scenario) { s.Steps[1].Expect[0].Source = "invariant" }, scenario.FromFile, "invariants are fixed in Semar", "steps[1].expect[0].source"},
		{"unknown source", func(s *domain.Scenario) { s.Steps[1].Expect[0].Source = "guess" }, scenario.FromFile, "source must be one of", "steps[1].expect[0].source"},
		{"person from an agent", func(s *domain.Scenario) { s.Steps[1].Expect[0].Source = "person" }, scenario.FromAgent, "an agent cannot use the source person", "steps[1].expect[0].source"},
		{"person from a file", func(s *domain.Scenario) { s.Steps[1].Expect[0].Source = "person" }, scenario.FromFile, "", ""},
		{"ticket with no cite", func(s *domain.Scenario) { s.Steps[1].Expect[0].Cite = "" }, scenario.FromFile, "cite is required for the source ticket", "steps[1].expect[0].cite"},
		{"check value that cannot be encoded", func(s *domain.Scenario) { s.Steps[2].Expect[0].Check.Equals = math.NaN() }, scenario.FromFile, "cannot be encoded", "steps[2].expect[0].check"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := load(t)
			tc.change(&s)
			problems := scenario.Validate(s, tc.origin)
			if tc.want == "" {
				if len(problems) != 0 {
					t.Fatalf("want no problem, got %v", problems)
				}
				return
			}
			for _, p := range problems {
				if strings.Contains(p.Message, tc.want) && (tc.path == "" || p.Path == tc.path) {
					return
				}
			}
			t.Fatalf("want a problem at %q containing %q, got %v", tc.path, tc.want, problems)
		})
	}
}

func TestValidateReturnsEveryProblem(t *testing.T) {
	s := load(t)
	s.APIVersion = "x"
	s.Ticket = ""
	s.Surface = "ios"
	if got := len(scenario.Validate(s, scenario.FromFile)); got != 3 {
		t.Fatalf("want 3 problems, got %d", got)
	}
}
