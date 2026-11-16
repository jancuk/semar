package idcheck_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/idcheck"
)

func TestCheck(t *testing.T) {
	tests := []struct {
		name string
		id   string
		ok   bool
	}{
		{"letters", "refund", true},
		{"digits first", "1abc", true},
		{"inner separators", "a.b_c-d", true},
		{"empty", "", false},
		{"dot", ".", false},
		{"dotdot", "..", false},
		{"leading dot", ".hidden", false},
		{"leading underscore", "_run", false},
		{"leading hyphen", "-x", false},
		{"slash", "a/b", false},
		{"backslash", `a\b`, false},
		{"space", "a b", false},
		{"newline", "a\nb", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := idcheck.Check("scenario ID", tt.id)
			if (err == nil) != tt.ok {
				t.Fatalf("Check(%q) = %v, want ok=%v", tt.id, err, tt.ok)
			}
			if err != nil && !strings.Contains(err.Error(), "scenario ID") {
				t.Errorf("error %q does not name the kind", err)
			}
		})
	}
}

// The scenario of a journey leg is <journey>.<leg>, so a scenario that a person writes must not
// have a dot: it could share results, labels, and confirmations with a leg.
func TestScenarioRefusesADot(t *testing.T) {
	for id, ok := range map[string]bool{
		"SHOP-482-01":  true,
		"refund_1":     true,
		"checkout.pay": false, // the scenario of leg pay of journey checkout
		"a.b.c":        false,
		"../x":         false,
		"":             false,
	} {
		err := idcheck.Scenario(id)
		if (err == nil) != ok {
			t.Errorf("Scenario(%q) = %v, want ok=%v", id, err, ok)
		}
		if err != nil && !strings.Contains(err.Error(), "scenario ID") {
			t.Errorf("error %q does not name the kind", err)
		}
	}
	// A leg scenario is still an ID that evidence and confirmations accept.
	if err := idcheck.Check("scenario ID", "checkout.pay"); err != nil {
		t.Errorf("a leg scenario is not an ID: %v", err)
	}
}
