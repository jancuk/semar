package scenario_test

import (
	"os"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/scenario"
)

// A scenario that Parse accepts and Validate passes must have a hash, or it could not run.
func FuzzParse(f *testing.F) {
	valid, err := os.ReadFile("testdata/valid.yaml")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add([]byte(""))
	f.Add([]byte("id: [unclosed\n"))
	f.Add([]byte("a: 1\n---\nb: 2\n"))
	f.Add([]byte("steps:\n  - id: s\n    expect:\n      - check: {jsonpath: $.a, greater: .nan}\n        source: diff\n"))
	f.Add([]byte("steps:\n  - id: s\n    expect:\n      - check: {jsonpath: $.a, equals: {1: x}}\n        source: diff\n"))
	f.Add([]byte("\x00\xff: !!binary ~\n"))
	f.Add([]byte("given:\n  - fixture: order.paid\n    as: order\n    with: { customer: c-1, count: 3 }\n    lease: exclusive\nsteps:\n  - id: s\n"))
	f.Add([]byte("given:\n  - fixture: order.paid\n    as: order\n    with: { status: paid, equals: 1.50, greater: .inf }\nsteps:\n  - id: s\n"))
	f.Add([]byte("given: [{fixture: a.b, as: x}, {fixture: a.b, as: x}]\nsteps:\n  - id: s\n"))
	f.Add([]byte("given:\n  - fixture: \"{{order.id}}\"\n    as: \"a.b\"\n    with: { \"{{x.y}}\": \"{{data.k}}\" }\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		s, err := scenario.Parse(data, "fuzz.yaml")
		if err != nil {
			return
		}
		if len(scenario.Validate(s, scenario.FromAgent)) > 0 {
			return
		}
		if got := domain.ExpectHash(s); len(got) != 64 {
			t.Fatalf("a valid scenario has hash %q, want 64 hex characters", got)
		}
	})
}
