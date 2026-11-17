package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/sampleapp"
)

// A plan that types a date near the day of the plan is sent back with the variable to write, the
// script that is stored holds the variable, and each run sends the date of its own time.
func TestPlanRefusesATypedDateAndTheStoredScriptFollowsTheRun(t *testing.T) {
	old := time.Local
	time.Local = time.FixedZone("UTC+7", 7*3600)
	t.Cleanup(func() { time.Local = old })

	// The clock of the test runs from 2026-11-14 10.00 UTC, so a week later is 2026-11-21.
	typed := strings.ReplaceAll(goodDraft, `"json": {"amount": 100000}`, `"json": {"amount": 100000, "ref": "2026-11-21T10:00:00Z"}`)
	typed = strings.Replace(typed, `{"check": {"jsonpath": "$.data.amount", "equals": 100000.0}, "why": "internal/orders/read.go:5 returns the order"}`,
		`{"check": {"jsonpath": "$.data.ref", "equals": "2026-11-21T10:00:00Z"}, "why": "internal/orders/read.go:5 returns the order"}`, 1)
	dated := strings.ReplaceAll(typed, "2026-11-21T10:00:00Z", "{{datetime.plus7d}}")

	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, typed, dated)
	p.must(t, cli.ExitOK, "plan", "--diff", "main..release")
	md, err := os.ReadFile(p.path(".semar", "plans", p.head(t)[:12], "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "plan.md", string(md), "Attempts: 2 of 3")

	stored, err := filepath.Glob(p.path(".semar", "store", "scripts", "*", "*.yaml"))
	if err != nil || len(stored) != 2 {
		t.Fatalf("stored scripts %v, %v; want 2", stored, err)
	}
	for _, f := range stored {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "{{datetime.plus7d}}") || strings.Contains(string(data), "2026-11-21") {
			t.Errorf("%s must hold the variable and no date:\n%s", f, data)
		}
	}
	scenario, err := os.ReadFile(p.path(".semar", "scenarios", "SHOP-483", "SHOP-483-01.feature"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "the scenario", string(scenario), `equals "{{datetime.plus7d}}"`)

	p.must(t, cli.ExitOK, "review", "--yes")
	sent := map[string]string{}
	for _, id := range []domain.RunID{"r-20261114T100000Z-0001", "r-20261121T100000Z-0001"} {
		p.opts.RunID = func() domain.RunID { return id }
		p.must(t, cli.ExitOK, "run", "--all", "--build", p.head(t)[:12])
		files, err := filepath.Glob(p.path(".semar", "runs", string(id), "exchanges", "SHOP-483-01", "*-create-order.json"))
		if err != nil || len(files) != 1 {
			t.Fatalf("exchanges of run %s: %v, %v", id, files, err)
		}
		data, err := os.ReadFile(files[0])
		if err != nil {
			t.Fatal(err)
		}
		sent[string(id)] = string(data)
	}
	if !strings.Contains(sent["r-20261114T100000Z-0001"], "2026-11-21T10:00:00Z") || !strings.Contains(sent["r-20261121T100000Z-0001"], "2026-11-28T10:00:00Z") {
		t.Errorf("the dates that the two runs sent do not follow the runs:\n%s\n%s", sent["r-20261114T100000Z-0001"], sent["r-20261121T100000Z-0001"])
	}
}
