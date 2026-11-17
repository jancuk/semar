package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/domain"
)

func manualRepo(t *testing.T) *repo {
	t.Helper()
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482",
		entry("SHOP-482/person", "The refund receipt", manualEntryFields),
		entry("SHOP-482/other", "Another", `gap: "not drafted"`)))
	return r
}

func TestCheckByLetsAScenarioDoTheManualCheck(t *testing.T) {
	r := manualRepo(t)
	res := r.must(t, cli.ExitOK, "check", "SHOP-482/person", "--by", scenarioID)
	contains(t, "stdout", res.stdout, "SHOP-482/person", "by test "+scenarioID, "linked by tester", "local, unverified identity")

	data, err := os.ReadFile(r.path(".semar", "obligations", "SHOP-482.plan.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "covers: ["+scenarioID+"]") {
		t.Errorf("the scenario is not in covers:\n%s", data)
	}
	links, err := filepath.Glob(r.path(".semar", "store", "obligations", "SHOP-482", "person", "link", "*.json"))
	if err != nil || len(links) != 1 {
		t.Fatalf("%d link records, want 1: %v %v", len(links), links, err)
	}

	// From now on the state comes from the scenario: it has no result yet.
	before := r.must(t, cli.ExitUndecided, "status")
	contains(t, "before a run", squash(before.stdout), "blocked 1 → semar run\n SHOP-482/person The refund receipt\n "+scenarioID+" has no result: run it")

	r.putRun(t, "r-old-1", "", nil, true, scenarioResult(scenarioID, domain.OutcomePassed, ""))
	after := r.must(t, cli.ExitUndecided, "status")
	contains(t, "after a pass", squash(after.stdout), "verified 1\n SHOP-482/person The refund receipt\n by test "+scenarioID+", linked by tester")

	r.putRun(t, "r-z-new", "", nil, true, scenarioResult(scenarioID, domain.OutcomeFailed, "boom"))
	failed := r.must(t, cli.ExitFailed, "status")
	contains(t, "after a failure", squash(failed.stdout), "failing 1\n SHOP-482/person The refund receipt\n "+scenarioID+" failed in r-z-new: boom")
}

func TestCheckByIsRepeatableAndBlocksAHandResult(t *testing.T) {
	r := manualRepo(t)
	r.must(t, cli.ExitOK, "check", "SHOP-482/person", "--by", scenarioID)
	r.must(t, cli.ExitOK, "check", "SHOP-482/person", "--by", scenarioID)
	data, err := os.ReadFile(r.path(".semar", "obligations", "SHOP-482.plan.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), scenarioID); n != 1 {
		t.Errorf("the scenario is in the file %d times:\n%s", n, data)
	}
	res := r.must(t, cli.ExitError, "check", "SHOP-482/person", "pass")
	contains(t, "stderr", res.stderr, "SHOP-482/person", "only a use case with a manual check that no test does")
}

func TestCheckByRefusesWhatIsNotALink(t *testing.T) {
	r := manualRepo(t)
	file := r.path(".semar", "obligations", "SHOP-482.plan.yaml")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"a use case with no manual check": {[]string{"check", "SHOP-482/other", "--by", scenarioID}, "has no manual check"},
		"a scenario that is not there":    {[]string{"check", "SHOP-482/person", "--by", "SHOP-482-99"}, "SHOP-482-99"},
		"an unknown use case":             {[]string{"check", "SHOP-482/none", "--by", scenarioID}, "SHOP-482/none was not found"},
		"a verb with --by":                {[]string{"check", "SHOP-482/person", "pass", "--by", scenarioID}, "--by takes no pass or fail"},
		"neither a verb nor --by":         {[]string{"check", "SHOP-482/person"}, "pass or fail"},
	} {
		t.Run(name, func(t *testing.T) {
			res := r.must(t, cli.ExitError, c.args...)
			contains(t, "stderr", res.stderr, c.want)
		})
	}
	after, err := os.ReadFile(file)
	if err != nil || string(before) != string(after) {
		t.Errorf("a refused link changed the obligations file: %v\n%s", err, after)
	}
	if exists(t, r.path(".semar", "store", "obligations")) {
		t.Error("a refused link left a record")
	}
}
