package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/sampleapp"
)

func planDirs(t *testing.T, p *planRepo) []string {
	t.Helper()
	entries, err := os.ReadDir(p.path(".semar", "plans"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestASecondPlanOfAKeptCriterionCallsNoModelAndWritesNothing(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.commitCriterionTicket(t)
	p.configure(t, criterionAnswerJSON)
	p.withCriterionKnowledge(t)
	made := p.recordJobs()
	args := []string{"plan", "--diff", "main..release", "--pipeline", "criteria", "--ladder", "planner:small", "--no-trial"}
	p.must(t, cli.ExitOK, args...)
	plansBefore := planDirs(t, p)
	scenario := p.path(".semar", "scenarios", "SHOP-482", "SHOP-482-01.feature")
	before, err := os.ReadFile(scenario)
	if err != nil {
		t.Fatal(err)
	}

	res := p.must(t, cli.ExitOK, args...)
	want := "Every criterion of SHOP-482 has a test or a manual check already. Nothing was planned. To plan one again, remove its test with semar rm.\n"
	if res.stdout != want {
		t.Errorf("stdout = %q, want %q", res.stdout, want)
	}
	if len(*made) != 1 || len((*made)[0].Jobs()) != 1 {
		t.Errorf("a model was called again: %d providers", len(*made))
	}
	if got := planDirs(t, p); strings.Join(got, ",") != strings.Join(plansBefore, ",") {
		t.Errorf("plan directories %v, were %v", got, plansBefore)
	}
	after, err := os.ReadFile(scenario)
	if err != nil || string(after) != string(before) {
		t.Errorf("the scenario changed: %v", err)
	}
	if matches, _ := filepath.Glob(p.path(".semar", "scenarios", "SHOP-482", "*.feature")); len(matches) != 1 {
		t.Errorf("scenario files: %v", matches)
	}
	p.assertNoTempCopy(t)
}
