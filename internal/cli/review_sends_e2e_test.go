package cli_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/sampleapp"
)

const refundID = "SHOP-482-01"

// A person who reviews a planned test sees what it sends. Once it has run, the review also shows what
// the service answered, and a check that the service did not meet.
func TestReviewShowsWhatIsSentAndWhatTheLastRunObserved(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{Defects: map[sampleapp.Defect]bool{sampleapp.RefundOverTotal: true}})
	p.configure(t, goodDraft)
	p.must(t, cli.ExitOK, "plan", "--diff", "main..release")

	review := func() string {
		p.opts.Terminal = func() bool { return true }
		p.opts.Stdin = strings.NewReader("d\nq\n")
		return p.must(t, cli.ExitUndecided, "review", refundID).stdout
	}

	before := review()
	contains(t, "the review before a run", before,
		"    Given Create a paid order for 100000\n          POST /v1/orders\n          json {\"amount\":100000}\n",
		"    When  Request a refund of 150000\n          POST /v1/orders/{{order_id}}/refunds\n          json {\"amount\":150000}\n",
		"    Then  the status is 422 (source diff, by model)\n",
		"    not run yet: semar run --scenario "+refundID+" sends it and stores what the service answers\n",
	)
	if strings.Contains(before, "last run") {
		t.Errorf("the review shows a run before any:\n%s", before)
	}

	// The expected result does not hold, so the run fails, and it stores what the service answered.
	p.must(t, cli.ExitFailed, "run", "--scenario", refundID)
	runID := p.latestRunID(t)

	after := review()
	contains(t, "the review after a run", after,
		"    Then  the status is 422 (source diff, by model)\n          last run "+runID+": observed status 201, not met\n",
		"  request: POST ",
		"  request body: {\"amount\":150000}\n",
		"  status: 201\n",
	)
	if strings.Contains(after, "not run yet") {
		t.Errorf("the review says the scenario was not run:\n%s", after)
	}
}

func (p *planRepo) latestRunID(t *testing.T) string {
	t.Helper()
	dirs := p.runDirs(t)
	if len(dirs) == 0 {
		t.Fatal("no run was stored")
	}
	return dirs[len(dirs)-1]
}
