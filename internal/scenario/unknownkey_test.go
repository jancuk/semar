package scenario_test

import (
	"testing"

	"github.com/jancuk/semar/internal/scenario"
)

func TestParseNamesAnUnknownKeyAndATypeMismatchInPlainWords(t *testing.T) {
	cases := []struct{ name, text, want string }{
		{"top level", "apiVersion: semar/v1\nkind: Scenario\nid: a\nsurfaces: api\n", "s.yaml:4: unknown key surfaces"},
		{"in a step", "kind: Scenario\nsteps:\n  - id: a\n    ass: bob\n", "s.yaml:4: unknown key ass in steps"},
		{"type mismatch", "kind: Scenario\nsteps:\n  - id: a\n    expect:\n      - check:\n          status: ok\n", "s.yaml:6: status must be a number"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := scenario.Parse([]byte(c.text), "s.yaml")
			if err == nil || err.Error() != c.want {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}
