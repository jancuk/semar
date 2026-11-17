package status_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/obligation"
	"github.com/jancuk/semar/internal/status"
)

func located(id string, mod func(*obligation.Obligation)) obligation.Located {
	o := obligation.Obligation{ID: id, Title: "Title of " + id, Source: obligation.SourceDiff, Ticket: strings.SplitN(id, "/", 2)[0]}
	if mod != nil {
		mod(&o)
	}
	return obligation.Located{Obligation: o}
}

func entry(id string, state obligation.State, mod func(*obligation.Entry)) obligation.Entry {
	e := obligation.Entry{Obligation: located(id, nil), State: state}
	if mod != nil {
		mod(&e)
	}
	return e
}

func TestEveryUseCaseIsInExactlyOneGroup(t *testing.T) {
	manual := func(e *obligation.Entry) {
		e.Obligation.Manual = &obligation.Manual{Kind: "judgement", Text: "Check the receipt", Steps: []string{"Look"}, Expected: "ok", WrittenBy: "person"}
	}
	passedNow := func(e *obligation.Entry) {
		manual(e)
		e.Manual = &obligation.ManualResult{Result: "passed", User: "tester"}
		e.ManualTie = obligation.TieCurrent
	}
	cases := []struct {
		name  string
		entry obligation.Entry
		want  status.Group
	}{
		{"verified", entry("SHOP-482/1", obligation.Verified, nil), status.Verified},
		{"a scenario failed", entry("SHOP-482/1", obligation.Failed, nil), status.Failing},
		{"a person failed it", entry("SHOP-482/1", obligation.NeedsAPerson, func(e *obligation.Entry) {
			manual(e)
			e.Manual = &obligation.ManualResult{Result: "failed", User: "tester"}
		}), status.Failing},
		{"blocked", entry("SHOP-482/1", obligation.Blocked, func(e *obligation.Entry) { e.Covers = []obligation.Cover{{Scenario: "A-1", Found: true}} }), status.Blocked},
		{"an approval is missing", entry("SHOP-482/1", obligation.Blocked, func(e *obligation.Entry) {
			e.Covers = []obligation.Cover{{Scenario: "A-1"}}
			e.Review = true
		}), status.NeedsReview},
		{"stale", entry("SHOP-482/1", obligation.Blocked, func(e *obligation.Entry) {
			e.Covers = []obligation.Cover{{Scenario: "A-1"}}
			e.Stale = true
		}), status.Stale},
		{"review comes before stale", entry("SHOP-482/1", obligation.Blocked, func(e *obligation.Entry) {
			e.Covers = []obligation.Cover{{Scenario: "A-1"}, {Scenario: "A-2"}}
			e.Stale, e.Review = true, true
		}), status.NeedsReview},
		{"for a person", entry("SHOP-482/1", obligation.NeedsAPerson, manual), status.ForAPerson},
		{"a pass of a person on the current build", entry("SHOP-482/1", obligation.Verified, passedNow), status.Verified},
		{"no test", entry("SHOP-482/1", obligation.Blocked, func(e *obligation.Entry) { e.NoReason = true }), status.NoTest},
		{"a gap with no cover", entry("SHOP-482/1", obligation.Blocked, func(e *obligation.Entry) { e.Obligation.Gap = "not drafted" }), status.NoTest},
		{"deferred", entry("SHOP-482/1", obligation.Deferred, nil), status.Deferred},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := status.Classify(c.entry); got != c.want {
				t.Errorf("group %q, want %q", got, c.want)
			}
		})
	}
}

func TestTheExitCodeIsTheOneOfTheLedger(t *testing.T) {
	failedByHand := entry("a/1", obligation.NeedsAPerson, func(e *obligation.Entry) { e.Manual = &obligation.ManualResult{Result: "failed"} })
	cases := []struct {
		name       string
		entries    []obligation.Entry
		buildsRead bool
		want       int
	}{
		{"all verified with the builds read", []obligation.Entry{entry("a/1", obligation.Verified, nil), entry("a/2", obligation.Deferred, nil)}, true, 0},
		{"all verified with the builds not read", []obligation.Entry{entry("a/1", obligation.Verified, nil)}, false, 2},
		{"a failure", []obligation.Entry{entry("a/1", obligation.Failed, nil), entry("a/2", obligation.Blocked, nil)}, true, 1},
		{"a failure beats unread builds", []obligation.Entry{entry("a/1", obligation.Failed, nil)}, false, 1},
		{"a manual failure", []obligation.Entry{failedByHand}, true, 1},
		{"something open", []obligation.Entry{entry("a/1", obligation.Verified, nil), entry("a/2", obligation.Blocked, nil)}, true, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := status.ExitCode(c.entries, c.buildsRead); got != c.want {
				t.Errorf("exit code %d, want %d", got, c.want)
			}
		})
	}
}

func render(t *testing.T, in status.Input) string {
	t.Helper()
	var b bytes.Buffer
	if err := status.Text(&b, status.Build(in)); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestTextHasTheShapeOfTheContract(t *testing.T) {
	in := status.Input{
		Branch: "feature/refund-limit", Base: "main", Environment: "uat",
		Builds:     []domain.BuildRef{{Service: "orders", Commit: "4f2a91c0000"}},
		BuildsRead: true,
		Tickets:    []string{"SHOP-482", "SHOP-490"},
		Entries: []obligation.Entry{
			entry("SHOP-482/1", obligation.Verified, nil),
			entry("SHOP-482/2", obligation.Failed, func(e *obligation.Entry) {
				e.Reasons = []string{"SHOP-482-02 failed in r-1: expected status 422, observed status 200"}
			}),
			entry("SHOP-482/3", obligation.Blocked, func(e *obligation.Entry) {
				e.Review = true
				e.Covers = []obligation.Cover{{Scenario: "SHOP-482-03", Word: "needs review"}}
				e.Reasons = []string{"SHOP-482-03 needs review in r-1: an expected result is not confirmed"}
			}),
			entry("SHOP-482/manual-1", obligation.NeedsAPerson, func(e *obligation.Entry) {
				e.Obligation.Manual = &obligation.Manual{Text: "Check the refund receipt on a phone"}
				e.Reasons = []string{"Check the refund receipt on a phone"}
			}),
			entry("SHOP-490/1", obligation.Blocked, func(e *obligation.Entry) {
				e.Obligation.Gap = "not planned"
				e.Reasons = []string{"not planned"}
			}),
		},
	}
	got := render(t, in)
	want := `On feature/refund-limit against main · uat · orders at 4f2a91c
5 use cases of SHOP-482, SHOP-490

  verified      1
  failing       1  SHOP-482/2         SHOP-482-02 failed in r-1: expected status 422, observed status 200
  needs review  1  SHOP-482/3         SHOP-482-03 needs review in r-1: an expected result is not confirmed → semar review SHOP-482-03
  for a person  1  SHOP-482/manual-1  Check the refund receipt on a phone → semar check SHOP-482/manual-1 pass
  no test       1  SHOP-490/1         not planned → semar plan

Ship: no. 1 failing, 3 open.
`
	if got != want {
		t.Errorf("text:\n%s\nwant:\n%s", got, want)
	}
}

func TestShipIsYesOnlyWhenTheCodeIsZero(t *testing.T) {
	in := status.Input{Branch: "b", Base: "main", Environment: "uat", BuildsRead: true, Tickets: []string{"SHOP-482"},
		Entries: []obligation.Entry{entry("SHOP-482/1", obligation.Verified, nil), entry("SHOP-482/2", obligation.Deferred, nil)}}
	got := render(t, in)
	if !strings.HasSuffix(got, "Ship: yes. 1 deferred.\n") {
		t.Errorf("ship line of a green change:\n%s", got)
	}
	in.BuildsRead = false
	in.Environment = ""
	got = render(t, in)
	if !strings.Contains(got, "Ship: no. The builds were not read.\n") {
		t.Errorf("ship line when the builds were not read:\n%s", got)
	}
}

func TestAnUnplannedTicketIsOpen(t *testing.T) {
	in := status.Input{Branch: "b", Base: "main", Environment: "uat", BuildsRead: true, Tickets: []string{"SHOP-482", "SHOP-490"},
		NotPlanned: []string{"SHOP-490"},
		Entries:    []obligation.Entry{entry("SHOP-482/1", obligation.Verified, nil)}}
	r := status.Build(in)
	if r.Code != 2 {
		t.Errorf("code %d, want 2: a ticket with no use case has no test", r.Code)
	}
	got := render(t, in)
	for _, want := range []string{"  not planned  1  SHOP-490 → semar plan\n", "Ship: no. 1 open.\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("text lacks %q:\n%s", want, got)
		}
	}
}

func TestTheNoteOfAnApprovalByAnAuthor(t *testing.T) {
	in := status.Input{Branch: "b", Base: "main", Environment: "uat", BuildsRead: true, Tickets: []string{"SHOP-482"},
		Entries:  []obligation.Entry{entry("SHOP-482/1", obligation.Verified, nil), entry("SHOP-482/2", obligation.Verified, nil)},
		ByAuthor: map[string]bool{"SHOP-482/2": true}}
	r := status.Build(in)
	if r.Code != 0 {
		t.Errorf("code %d: the mark must not change the code", r.Code)
	}
	if !r.Items[1].ByAuthor || r.Items[0].ByAuthor {
		t.Errorf("marks %v %v, want only the second", r.Items[0].ByAuthor, r.Items[1].ByAuthor)
	}
	if got := render(t, in); !strings.Contains(got, "approved by an author of the change") {
		t.Errorf("text lacks the mark:\n%s", got)
	}
}
