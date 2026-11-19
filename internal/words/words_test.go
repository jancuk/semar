package words_test

import (
	"testing"

	"github.com/jancuk/semar/internal/words"
)

func TestOfResult(t *testing.T) {
	cases := []struct {
		name    string
		outcome string
		reasons []string
		want    string
	}{
		{"a pass", "passed", nil, "passed"},
		{"a failure", "failed", []string{"step refund-too-large: expected status 422, observed status 200"}, "failed"},
		{"an expected result nobody confirmed", "inconclusive", []string{"an expected result is not confirmed"}, "needs review"},
		{"one that is not confirmed and not met", "inconclusive", []string{"an expected result is not confirmed and not met"}, "needs review"},
		{"a fixture nobody approved", "blocked", []string{"fixture order.paid is not approved: run semar review order.paid"}, "needs review"},
		{"a screens file nobody approved", "blocked", []string{"the screens of shop are not approved: semar review shop"}, "needs review"},
		{"the approval among other reasons", "inconclusive", []string{"a check could not be evaluated", "an expected result is not confirmed"}, "needs review"},
		{"another inconclusive result keeps its word", "inconclusive", []string{"the scenario has no check besides the invariants"}, "inconclusive"},
		{"a blocked result keeps its word", "blocked", []string{"the build of orders could not be read"}, "blocked"},
		{"a result that did not run keeps its word", "not_run", []string{"no web engine here"}, "not_run"},
		{"a stored stale result", "stale", []string{"the build changed during the run"}, "stale"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := words.Of(c.outcome, c.reasons); got != c.want {
				t.Errorf("Of(%q, %q) = %q, want %q", c.outcome, c.reasons, got, c.want)
			}
		})
	}
}

func TestNext(t *testing.T) {
	cases := []struct{ word, subject, want string }{
		{"needs review", "SHOP-482-03", "semar review SHOP-482-03"},
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
		{"needs review", "SHOP-482-03", "not confirmed", "SHOP-482-03  needs review  not confirmed → semar review SHOP-482-03"},
		{"needs review", "SHOP-482-03", "", "SHOP-482-03  needs review → semar review SHOP-482-03"},
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
