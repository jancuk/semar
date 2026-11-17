package export_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/export"
)

// sampleRun holds one scenario of every outcome in two tickets, and one with no file.
func sampleRun() export.Run {
	file := func(ticket, id string) string { return ".semar/scenarios/" + ticket + "/" + id + ".feature" }
	check := func(step domain.StepID, idx int, source string, state domain.CheckState, exp, obs string, confirmed bool) domain.CheckResult {
		return domain.CheckResult{Step: step, Index: idx, Source: source, State: state, Expected: exp, Observed: obs, Confirmed: confirmed}
	}
	failed := result("SHOP-482-01", domain.OutcomeFailed,
		"step refund-too-much: expected status 422, observed status 200 (source ticket)",
		"step refund-too-much broke an invariant: expected no server error, observed status 500")
	failed.Checks = []domain.CheckResult{
		check("refund-too-much", 0, "ticket", domain.CheckFalse, "status 422", "status 200", true),
		check("refund-too-much", 1, "diff", domain.CheckFalse, "$.error.code equals REFUND_EXCEEDS_TOTAL", "$.error.code is missing", false),
		check("refund-too-much", 2, "ticket", domain.CheckTrue, "status 200", "status 200", true),
		check("refund-too-much", -1, domain.SourceInvariant, domain.CheckFalse, "no server error", "status 500", true),
	}
	legacy := result("LEGACY-1", domain.OutcomeFailed, "step x: expected status 200, observed status 404 (source person)")
	legacy.Checks = []domain.CheckResult{check("x", 0, "person", domain.CheckFalse, "status 200", "status 404", true)}

	scenarios := []export.Scenario{
		{Result: failed, Title: "Refund above the order total is rejected", Ticket: "SHOP-482", File: file("SHOP-482", "SHOP-482-01"),
			StepLines: map[domain.StepID]int{"create-order": 8, "refund-too-much": 14}, DurationMS: 412},
		{Result: result("SHOP-482-02", domain.OutcomeBlocked, "setup failed: create-order answered 500"),
			Title: "Refund of a partly refunded order", Ticket: "SHOP-482", File: file("SHOP-482", "SHOP-482-02"), DurationMS: 85},
		{Result: result("SHOP-482-03", domain.OutcomePassed), Title: "Refund of the full total", Ticket: "SHOP-482", File: file("SHOP-482", "SHOP-482-03"), DurationMS: 1500},
		{Result: result("SHOP-482-04", domain.OutcomeInconclusive, "an expected result is not confirmed"),
			Title: "Refund history is listed", Ticket: "SHOP-482", File: file("SHOP-482", "SHOP-482-04"), DurationMS: 30},
		{Result: result("PAY-9-01", domain.OutcomeNotRun, "the environment has no payment service"),
			Title: "Capture a held payment", Ticket: "PAY-9", File: file("PAY-9", "PAY-9-01")},
		{Result: result("PAY-9-02", domain.OutcomeStale, "the build changed during the run"),
			Title: "Void a captured payment", Ticket: "PAY-9", File: file("PAY-9", "PAY-9-02"), DurationMS: 2000, Unknown: true},
		{Result: legacy, DurationMS: 10},
	}
	return export.Run{
		Run: domain.Run{
			Schema: 1, ID: "r-20261117T090000Z-1a2b", Environment: "uat", EnvironmentKind: "test",
			StartedAt: time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC), EndedAt: time.Date(2026, 11, 17, 9, 0, 12, 0, time.UTC),
			BuildsStart: []domain.BuildRef{{Service: "orders", Commit: "9f3c2ab"}, {Service: "payments", Commit: "77aa01c"}},
			BuildsEnd:   []domain.BuildRef{{Service: "orders", Commit: "9f3c2ab"}, {Service: "payments", Commit: "77aa01c"}},
		},
		Scenarios: scenarios,
		Version:   "0.9.0",
	}
}

// oneOutcome is a run with one scenario of the outcome, with a file and a title.
func oneOutcome(o domain.Outcome) export.Run {
	r := sampleRun()
	r.Scenarios = []export.Scenario{{Result: result("SHOP-482-01", o, "a reason"), Title: "A title", Ticket: "SHOP-482",
		File: ".semar/scenarios/SHOP-482/SHOP-482-01.feature"}}
	return r
}

func render(t *testing.T, w func(*bytes.Buffer, export.Run) error, r export.Run) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := w(&buf, r); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// golden compares got with testdata/<name>, and rewrites it with -update.
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	p := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(p, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("%s differs from the output (run with -update to accept):\n%s", p, lineDiff(string(want), string(got)))
	}
}

func lineDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			return "line " + itoa(i+1) + "\n- " + a + "\n+ " + b
		}
	}
	return ""
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for ; n > 0; n /= 10 {
		d = append([]byte{byte('0' + n%10)}, d...)
	}
	return string(d)
}
