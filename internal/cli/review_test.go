package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/confirm"
)

const ticketFile = "# SHOP-482 Limit a refund\n\n## Acceptance criteria\n\n1. A refund larger than the paid amount is refused.\n"

// reviewRepo has a fixture that nobody approved, a scenario that needs a confirmation, and the use
// case that the scenario covers, which cites a line of the ticket.
func reviewRepo(t *testing.T) *repo {
	t.Helper()
	r := newRepo(t, repoOptions{offline: true})
	r.writeFixture(t, "order.paid.yaml", orderPaidFixture)
	r.writeRel(t, "docs/tickets/SHOP-482.md", ticketFile)
	r.ledgerFile(t, obligations("SHOP-482",
		entry("SHOP-482/1", "A refund above the paid amount is refused", "cite: docs/tickets/SHOP-482.md:5", "covers: ["+scenarioID+"]")))
	return r
}

func (r *repo) confirmations(t *testing.T) *confirm.Local { return confirm.NewLocal(r.path(".semar")) }

func TestReviewShowsTheRequirementBeforeTheFile(t *testing.T) {
	r := reviewRepo(t)
	res := r.must(t, cli.ExitOK, "review", "--yes")
	out := res.stdout

	fixtureAt, scenarioAt := strings.Index(out, "1 of 2 · fixture order.paid"), strings.Index(out, "2 of 2 · SHOP-482 (docs/tickets/SHOP-482.md:5)")
	if fixtureAt < 0 || scenarioAt < fixtureAt {
		t.Fatalf("the fixture does not come before the scenario:\n%s", out)
	}
	contains(t, "the fixture", out[fixtureAt:scenarioAt],
		"A paid order of 1000 that belongs to the given customer",
		"Sends:", "GET orders /v1/orders", "POST orders /v1/orders json {\"amount\":1000", "POST payments /v1/payments",
		"Removes afterwards:", "POST orders /v1/orders/{{id}}/cancel")
	contains(t, "the scenario", out[scenarioAt:],
		`"A refund larger than the paid amount is refused."`,
		"Test "+scenarioID+" · Refund above the order total is rejected · API · as support-agent",
		"When  Request a refund of 150000 for that order",
		"Then  status 422", "$.error.code equals REFUND_EXCEEDS_TOTAL (source diff)",
		"approved "+scenarioID)
	contains(t, "the fixture approval", out, "approved fixture order.paid")
	if strings.Contains(out, "hash ") {
		t.Errorf("the first view shows a hash, which nobody can judge:\n%s", out)
	}
	h := hashOfFixture(t, orderPaidFixture)
	if ok, err := r.approvals(t).FixtureApproved("order.paid", h); !ok || err != nil {
		t.Errorf("the fixture was not approved: %v %v", ok, err)
	}
	res = r.must(t, cli.ExitOK, "review", "--yes")
	contains(t, "the second review", res.stdout, "nothing needs review")
}

func TestReviewAsksOneItemAtATime(t *testing.T) {
	r := reviewRepo(t)
	r.opts.Terminal = func() bool { return true }
	r.opts.Stdin = strings.NewReader("d\nn\ny\n")
	res := r.must(t, cli.ExitUndecided, "review")
	contains(t, "the review", res.stdout,
		"[y] approve  [n] skip  [d] details  [q] stop",
		"Fixture order.paid 1.0.0 (owner team-orders), hash",
		"approved "+scenarioID, "approved 1 of 2 items; 1 left")
	if ok, _ := r.approvals(t).FixtureApproved("order.paid", hashOfFixture(t, orderPaidFixture)); ok {
		t.Error("a skipped fixture was approved")
	}
	if _, found, _ := r.confirmations(t).Get(scenarioID, "x"); found {
		t.Error("nothing should match a made-up hash")
	}
	if !exists(t, r.path(".semar", "store", "confirmations", scenarioID)) {
		t.Error("the scenario was not confirmed")
	}

	// q stops, and what was left stays for the next review.
	r2 := reviewRepo(t)
	r2.opts.Terminal = func() bool { return true }
	r2.opts.Stdin = strings.NewReader("q\n")
	res = r2.must(t, cli.ExitUndecided, "review")
	contains(t, "the review", res.stdout, "approved 0 of 2 items; 2 left")
	if exists(t, r2.path(".semar", "store", "approvals")) || exists(t, r2.path(".semar", "store", "confirmations")) {
		t.Error("a stopped review stored something")
	}

	// d shows the whole file as Gherkin, and an unknown answer is asked again.
	r3 := reviewRepo(t)
	r3.opts.Terminal = func() bool { return true }
	r3.opts.Stdin = strings.NewReader("y\nmaybe\nd\ny\n")
	res = r3.must(t, cli.ExitOK, "review")
	contains(t, "the review", res.stdout, "answer y, n, d, or q", "as Gherkin, with the source and cite of each expected result under it:", "    Feature: SHOP-482")
}

func TestReviewWithNoTerminalListsTheItemsAndApprovesNothing(t *testing.T) {
	r := reviewRepo(t)
	r.opts.Terminal = func() bool { return false }
	res := r.must(t, cli.ExitUndecided, "review")
	contains(t, "stdout", res.stdout, "2 items need review:", "fixture order.paid 1.0.0", "scenario "+scenarioID)
	contains(t, "stderr", res.stderr, "the input is not a terminal, so pass --yes to approve")
	if exists(t, r.path(".semar", "store", "approvals")) || exists(t, r.path(".semar", "store", "confirmations")) {
		t.Error("something was approved")
	}
}

func TestReviewNarrowsToTheIDsNamed(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		want       []string
		notWant    []string
		fixtureOK  bool
		scenarioOK bool
	}{
		{"a fixture", []string{"order.paid"}, []string{"1 of 1 · fixture order.paid"}, []string{"Test "}, true, false},
		{"a scenario", []string{scenarioID}, []string{"1 of 1 · SHOP-482 (docs/tickets/SHOP-482.md:5)"}, []string{"fixture order.paid"}, false, true},
		{"a ticket", []string{"SHOP-482"}, []string{"Test " + scenarioID}, []string{"fixture order.paid"}, false, true},
		{"both", []string{"order.paid", "SHOP-482"}, []string{"1 of 2 · fixture", "2 of 2 · SHOP-482"}, nil, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := reviewRepo(t)
			res := r.must(t, cli.ExitOK, append([]string{"review", "--yes"}, c.args...)...)
			contains(t, "the review", res.stdout, c.want...)
			for _, n := range c.notWant {
				if strings.Contains(res.stdout, n) {
					t.Errorf("the review holds %q:\n%s", n, res.stdout)
				}
			}
			ok, _ := r.approvals(t).FixtureApproved("order.paid", hashOfFixture(t, orderPaidFixture))
			if ok != c.fixtureOK {
				t.Errorf("fixture approved = %v, want %v", ok, c.fixtureOK)
			}
			if got := exists(t, r.path(".semar", "store", "confirmations", scenarioID)); got != c.scenarioOK {
				t.Errorf("scenario confirmed = %v, want %v", got, c.scenarioOK)
			}
		})
	}
}

func TestReviewStoresTheEmailOfGit(t *testing.T) {
	r := reviewRepo(t)
	r.opts.Email = func(string) string { return "tester@example.com" }
	r.must(t, cli.ExitOK, "review", "--yes")
	h := hashOfFixture(t, orderPaidFixture)
	fx, err := r.approvals(t).FixtureApprovals("order.paid", h)
	if err != nil || len(fx) != 1 || fx[0].Email != "tester@example.com" || fx[0].User != "tester" {
		t.Errorf("fixture approvals %+v, %v", fx, err)
	}
	data, err := os.ReadFile(filepath.Join(r.path(".semar", "store", "confirmations", scenarioID), mustSingleDir(t, r.path(".semar", "store", "confirmations", scenarioID)), "tester.json"))
	if err != nil || !strings.Contains(string(data), `"email": "tester@example.com"`) {
		t.Errorf("the confirmation holds no email: %v\n%s", err, data)
	}

	// With no email set, the record has none and the review goes on.
	r2 := reviewRepo(t)
	r2.opts.Email = func(string) string { return "" }
	r2.must(t, cli.ExitOK, "review", "--yes")
	fx, _ = r2.approvals(t).FixtureApprovals("order.paid", h)
	if len(fx) != 1 || fx[0].Email != "" {
		t.Errorf("approvals %+v, want one with no email", fx)
	}
}

func mustSingleDir(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("%v %v", entries, err)
	}
	return entries[0].Name()
}

func TestReviewApprovalFollowsAChangeOfTheScenario(t *testing.T) {
	r := reviewRepo(t)
	r.must(t, cli.ExitOK, "review", "--yes", scenarioID)
	r.editScenario(t, "the status is 422", "the status is 400")
	res := r.must(t, cli.ExitOK, "review", "--yes")
	contains(t, "the review", res.stdout, "approved "+scenarioID)
}
