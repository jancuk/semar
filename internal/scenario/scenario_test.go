package scenario_test

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/scenario"
)

func load(t *testing.T) domain.Scenario {
	t.Helper()
	s, err := scenario.Load("testdata/valid.feature")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParseRejectsUnknownKey(t *testing.T) {
	_, err := scenario.ParseYAML([]byte("apiVersion: semar/v1\nkind: Scenario\nid: A-1\nsteps:\n  - id: s\n    expects: []\n"), "a.yaml")
	if err == nil || !strings.Contains(err.Error(), "a.yaml:6") || !strings.Contains(err.Error(), "expects") {
		t.Fatalf("want an error that names a.yaml:6 and the key, got %v", err)
	}
}

func TestParseRejectsMalformedYAML(t *testing.T) {
	_, err := scenario.ParseYAML([]byte("id: [unclosed\n"), "b.yaml")
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
	n, err := scenario.ParseYAML([]byte(nested), "n.yaml")
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
		{"as that is not a name", func(s *domain.Scenario) { s.Steps[1].As = "a b" }, scenario.FromFile, "invalid auth profile name", "steps[1].as"},
		{"as that is a name", func(s *domain.Scenario) { s.Steps[1].As = "carol" }, scenario.FromFile, "", ""},
		{"by model", func(s *domain.Scenario) { s.Steps[1].Expect[0].By = "model" }, scenario.FromFile, "", ""},
		{"by something else", func(s *domain.Scenario) { s.Steps[1].Expect[0].By = "robot" }, scenario.FromFile, "by must be model or empty", "steps[1].expect[0].by"},
		{"operand with a variable", func(s *domain.Scenario) { s.Steps[2].Expect[0].Check.Equals = "board {{o.board}} {{run.id}}" }, scenario.FromFile, "", ""},
		{"operand with a secret", func(s *domain.Scenario) { s.Steps[2].Expect[0].Check.Equals = "{{secret.TOKEN}}" }, scenario.FromFile, "a check cannot use a secret", "steps[2].expect[0].check.equals"},
		{"operand with data", func(s *domain.Scenario) { s.Steps[2].Expect[0].Check.Equals = "{{data.a.b}}" }, scenario.FromFile, "data value", "steps[2].expect[0].check.equals"},
		{"operand with a malformed variable", func(s *domain.Scenario) { s.Steps[2].Expect[0].Check.Equals = "{{ x }}" }, scenario.FromFile, "malformed variable", "steps[2].expect[0].check.equals"},
		{"greater that is NaN", func(s *domain.Scenario) {
			c := &s.Steps[2].Expect[0].Check
			n := math.NaN()
			c.Equals, c.Greater = nil, &n
		}, scenario.FromFile, "greater must be a finite number", "steps[2].expect[0].check"},
		{"less that is infinite", func(s *domain.Scenario) {
			c := &s.Steps[2].Expect[0].Check
			n := math.Inf(1)
			c.Equals, c.Less = nil, &n
		}, scenario.FromFile, "less must be a finite number", "steps[2].expect[0].check"},
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

func TestParseRejectsSecondDocument(t *testing.T) {
	data := []byte("apiVersion: semar/v1\nkind: Scenario\nid: A-1\nsteps:\n  - id: s\n---\nid: A-2\n")
	_, err := scenario.ParseYAML(data, "multi.yaml")
	if err == nil || !strings.Contains(err.Error(), "multi.yaml") || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("want an error that names multi.yaml and says more than one document, got %v", err)
	}
}

const givenHead = "apiVersion: semar/v1\nkind: Scenario\nid: A-1\nticket: T-1\nsurface: api\nsteps:\n  - id: s\n"

func TestParseGiven(t *testing.T) {
	src := "apiVersion: semar/v1\nkind: Scenario\nid: A-1\nticket: T-1\nsurface: api\nauthProfile: tester\n" +
		"given:\n" +
		"  - fixture: order.paid\n    as: order\n    with: { customer: c-1, count: \"3\" }\n    lease: exclusive\n" +
		"  - { fixture: customer.verified, as: customer }\n" +
		"steps:\n  - id: s\n"
	s, err := scenario.ParseYAML([]byte(src), "g.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.Given{
		{Fixture: "order.paid", As: "order", With: map[string]string{"customer": "c-1", "count": "3"}, Lease: domain.LeaseExclusive},
		{Fixture: "customer.verified", As: "customer"},
	}
	if !reflect.DeepEqual(s.Given, want) {
		t.Fatalf("got %#v, want %#v", s.Given, want)
	}
	if domain.ExpectHash(s) == "" {
		t.Fatal("a scenario with given must have a hash")
	}
}

func TestParseRejectsUnknownGivenKey(t *testing.T) {
	src := givenHead[:strings.Index(givenHead, "steps:")] + "given:\n  - fixture: order.paid\n    as: order\n    retries: 3\nsteps:\n  - id: s\n"
	_, err := scenario.ParseYAML([]byte(src), "g.yaml")
	if err == nil || !strings.Contains(err.Error(), "g.yaml:9") || !strings.Contains(err.Error(), "retries") {
		t.Fatalf("want an error that names g.yaml:9 and the key, got %v", err)
	}
}

func TestParseGivenWithKeysThatNameCheckOperators(t *testing.T) {
	src := givenHead[:strings.Index(givenHead, "steps:")] + "given:\n  - fixture: order.paid\n    as: order\n    with: { status: paid, count: 3, equals: 1.50, greater: x, header: y }\nsteps:\n  - id: s\n"
	s, err := scenario.ParseYAML([]byte(src), "g.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"status": "paid", "count": "3", "equals": "1.50", "greater": "x", "header": "y"}
	if !reflect.DeepEqual(s.Given[0].With, want) {
		t.Fatalf("got %#v", s.Given[0].With)
	}
}

func TestValidateGiven(t *testing.T) {
	g := func(fixture, as string) domain.Given { return domain.Given{Fixture: domain.FixtureID(fixture), As: as} }
	many := make([]domain.Given, 9)
	for i := range many {
		many[i] = g("f.x", "a"+string(rune('a'+i)))
	}
	cases := []struct {
		name   string
		given  []domain.Given
		want   string // substring of one Problem.Message. Empty means no problem
		path   string
		wantOK bool
	}{
		{"none", nil, "", "", true},
		{"valid", []domain.Given{{Fixture: "order.paid", As: "order", With: map[string]string{"customer": "c-1"}, Lease: domain.LeaseExclusive}, g("customer.verified", "customer")}, "", "", true},
		{"nine entries", many, "at most 8 given entries", "given", false},
		{"eight entries", many[:8], "", "", true},
		{"fixture in upper case", []domain.Given{g("Order.paid", "order")}, "must match ^[a-z][a-z0-9_-]*", "given[0].fixture", false},
		{"fixture with a slash", []domain.Given{g("order/paid", "order")}, "must match", "given[0].fixture", false},
		{"fixture with a trailing dot", []domain.Given{g("order.", "order")}, "must match", "given[0].fixture", false},
		{"empty fixture", []domain.Given{g("", "order")}, "must match", "given[0].fixture", false},
		{"fixture too long", []domain.Given{g("a"+strings.Repeat("b", 64), "order")}, "longer than 64 bytes", "given[0].fixture", false},
		{"fixture at the limit", []domain.Given{g("a"+strings.Repeat("b", 63), "order")}, "", "", true},
		{"as in upper case", []domain.Given{g("order.paid", "Order")}, "must match ^[a-z][a-z0-9_]*$", "given[0].as", false},
		{"as with a dot", []domain.Given{g("order.paid", "or.der")}, "must match", "given[0].as", false},
		{"as run", []domain.Given{g("order.paid", "run")}, "the name run is reserved", "given[0].as", false},
		{"as data", []domain.Given{g("order.paid", "data")}, "the name data is reserved", "given[0].as", false},
		{"as secret", []domain.Given{g("order.paid", "secret")}, "the name secret is reserved", "given[0].as", false},
		{"as date", []domain.Given{g("order.paid", "date")}, "the name date is reserved", "given[0].as", false},
		{"as datetime", []domain.Given{g("order.paid", "datetime")}, "the name datetime is reserved", "given[0].as", false},
		{"as local", []domain.Given{g("order.paid", "local")}, "the name local is reserved", "given[0].as", false},
		{"with key secret", []domain.Given{{Fixture: "order.paid", As: "order", With: map[string]string{"secret": "x"}}}, `key "secret" must match ^[a-z][a-z0-9_]*$ and not be run, data, or secret`, "given[0].with", false},
		{"as used twice", []domain.Given{g("order.paid", "order"), g("order.refunded", "order")}, "order is used twice", "given[1].as", false},
		{"with key in upper case", []domain.Given{{Fixture: "order.paid", As: "order", With: map[string]string{"Customer": "x"}}}, `key "Customer" must match`, "given[0].with", false},
		{"with key run", []domain.Given{{Fixture: "order.paid", As: "order", With: map[string]string{"run": "x"}}}, `key "run"`, "given[0].with", false},
		{"with value of 256 bytes", []domain.Given{{Fixture: "order.paid", As: "order", With: map[string]string{"c": strings.Repeat("x", 256)}}}, "", "", true},
		{"with value of 257 bytes", []domain.Given{{Fixture: "order.paid", As: "order", With: map[string]string{"c": strings.Repeat("x", 257)}}}, "longer than 256 bytes", "given[0].with.c", false},
		{"with value with a control character", []domain.Given{{Fixture: "order.paid", As: "order", With: map[string]string{"c": "a\x1bb"}}}, "control character", "given[0].with.c", false},
		{"with value with a line feed", []domain.Given{{Fixture: "order.paid", As: "order", With: map[string]string{"c": "a\nb"}}}, "control character", "given[0].with.c", false},
		{"with value that is not UTF-8", []domain.Given{{Fixture: "order.paid", As: "order", With: map[string]string{"c": "\xff"}}}, "not valid UTF-8", "given[0].with.c", false},
		{"lease shared", []domain.Given{{Fixture: "order.paid", As: "order", Lease: "shared"}}, "lease must be exclusive or empty", "given[0].lease", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := load(t)
			s.Given = tc.given
			problems := scenario.Validate(s, scenario.FromFile)
			if tc.wantOK {
				if len(problems) != 0 {
					t.Fatalf("want no problem, got %v", problems)
				}
				return
			}
			for _, p := range problems {
				if strings.Contains(p.Message, tc.want) && p.Path == tc.path {
					return
				}
			}
			t.Fatalf("want a problem at %q containing %q, got %v", tc.path, tc.want, problems)
		})
	}
}
