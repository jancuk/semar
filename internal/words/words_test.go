package words_test

import (
	"testing"

	"github.com/jancuk/semar/internal/words"
)

func TestNextNamesOnlyRealCommands(t *testing.T) {
	cases := []struct{ word, subject, want string }{
		{"needs review", "SHOP-482-03", ""},
		{"stale", "SHOP-482-03", "semar run"},
		{"passed", "SHOP-482-03", ""},
		{"failed", "SHOP-482-03", ""},
		{"blocked", "SHOP-482-03", ""},
		{"inconclusive", "SHOP-482-03", ""},
	}
	for _, c := range cases {
		if got := words.Next(c.word, c.subject); got != c.want {
			t.Errorf("Next(%q, %q) = %q, want %q", c.word, c.subject, got, c.want)
		}
	}
}

func TestLine(t *testing.T) {
	cases := []struct{ word, subject, detail, want string }{
		{"passed", "SHOP-482-01", "", "SHOP-482-01  passed"},
		{"failed", "SHOP-482-02", "step refund-too-large: expected status 422, observed status 200", "SHOP-482-02  failed  step refund-too-large: expected status 422, observed status 200"},
		{"stale", "SHOP-482-03", "the build changed", "SHOP-482-03  stale  the build changed → semar run"},
		{"inconclusive", "SHOP-482-03", "", "SHOP-482-03  inconclusive"},
	}
	for _, c := range cases {
		if got := words.Line(c.subject, c.word, c.detail); got != c.want {
			t.Errorf("Line(%q, %q, %q) = %q, want %q", c.subject, c.word, c.detail, got, c.want)
		}
	}
}

func TestAttempts(t *testing.T) {
	cases := []struct {
		outcome      string
		flaky        bool
		ran, allowed int
		want         string
	}{
		{"passed", false, 0, 0, ""},
		{"passed", false, 1, 3, ""},
		{"passed", true, 2, 3, "flaky, passed on attempt 2 of 3"},
		{"failed", false, 3, 3, "failed on all 3 attempts"},
		{"failed", false, 2, 3, "failed on attempt 2 of 3"},
		{"blocked", false, 2, 3, "blocked on attempt 2 of 3"},
	}
	for _, c := range cases {
		if got := words.Attempts(c.outcome, c.flaky, c.ran, c.allowed); got != c.want {
			t.Errorf("Attempts(%s, %v, %d, %d) = %q, want %q", c.outcome, c.flaky, c.ran, c.allowed, got, c.want)
		}
	}
}
