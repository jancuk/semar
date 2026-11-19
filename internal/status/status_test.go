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
		{"stale", entry("SHOP-482/1", obligation.Blocked, func(e *obligation.Entry) {
			e.Covers = []obligation.Cover{{Scenario: "A-1"}}
			e.Stale = true
		}), status.Stale},
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
				e.Obligation.Title = "A refund above the paid amount is refused"
				e.Reasons = []string{"SHOP-482-02 failed in r-1: expected status 422, observed status 200"}
			}),
			entry("SHOP-482/3", obligation.Blocked, func(e *obligation.Entry) {
				e.Obligation.Title = "A refund is confirmed by the buyer"
				e.Covers = []obligation.Cover{{Scenario: "SHOP-482-03", Found: true, Outcome: "inconclusive"}}
				e.Reasons = []string{"SHOP-482-03 is inconclusive in r-1: a check could not be evaluated"}
			}),
			entry("SHOP-482/manual-1", obligation.NeedsAPerson, func(e *obligation.Entry) {
				e.Obligation.Title = "Check the refund receipt on a phone"
				e.Obligation.Manual = &obligation.Manual{Text: "Only a person can check this criterion"}
				e.Reasons = []string{"Only a person can check this criterion"}
			}),
			entry("SHOP-490/1", obligation.Blocked, func(e *obligation.Entry) {
				e.Obligation.Title = "Refund limit per day"
				e.Obligation.Gap = "not planned"
				e.Reasons = []string{"not planned"}
			}),
		},
	}
	got := render(t, in)
	want := `On feature/refund-limit against main · uat · orders at 4f2a91c
5 use cases of SHOP-482, SHOP-490

  verified      1
  failing       1
    SHOP-482/2  A refund above the paid amount is refused
      SHOP-482-02 failed in r-1: expected status 422, observed status 200
  blocked       1
    SHOP-482/3  A refund is confirmed by the buyer
      SHOP-482-03 is inconclusive in r-1: a check could not be evaluated
  for a person  1  → semar check <id> pass
    SHOP-482/manual-1  Check the refund receipt on a phone
  no test       1  → semar plan
    SHOP-490/1  Refund limit per day
      not planned

Ship: no. 1 failing, 3 open.
`
	if got != want {
		t.Errorf("text:\n%s\nwant:\n%s", got, want)
	}
}

func TestAGroupNamesItsCommandOnceAndTheSameSentenceIsNotRepeated(t *testing.T) {
	var entries []obligation.Entry
	entries = append(entries,
		entry("SHOP-482/manual-1", obligation.NeedsAPerson, func(e *obligation.Entry) {
			e.Obligation.Title = "Check the receipt on a phone"
			e.Obligation.Manual = &obligation.Manual{Text: "Only a person can check this criterion, on a screen, on a device, or by a gesture"}
			e.Reasons = []string{e.Obligation.Manual.Text}
		}),
		entry("SHOP-482/stale-1", obligation.Blocked, func(e *obligation.Entry) {
			e.Stale = true
			e.Covers = []obligation.Cover{{Scenario: "A-1"}}
			e.Obligation.Title = "Refund after a change"
			e.Reasons = []string{"A-1 passed on another build"}
		}),
	)
	got := render(t, status.Input{Branch: "b", Base: "main", Tickets: []string{"SHOP-482"}, Entries: entries})
	for _, want := range []string{
		"  for a person  1  → semar check <id> pass\n    SHOP-482/manual-1  Check the receipt on a phone\n",
		"  stale         1  → semar run\n    SHOP-482/stale-1  Refund after a change\n      A-1 passed on another build\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("text lacks %q:\n%s", want, got)
		}
	}
	for _, not := range []string{"Only a person can check", "→ semar review"} {
		if strings.Contains(got, not) {
			t.Errorf("text holds %q:\n%s", not, got)
		}
	}
	if n := strings.Count(got, "→"); n != 2 {
		t.Errorf("%d commands, want one per group:\n%s", n, got)
	}
}

func TestATitleIsCutAtAReadableWidth(t *testing.T) {
	long := "A refund of an order that was paid in two parts, by card and by wallet, is split in proportion to what each part paid and is shown on the receipt"
	e := entry("SHOP-482/1", obligation.Blocked, func(e *obligation.Entry) {
		e.Obligation.Title = long
		e.NoReason = true
	})
	got := render(t, status.Input{Entries: []obligation.Entry{e}})
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, "    SHOP-482/1") {
			if n := len([]rune(l)); n > 100 {
				t.Errorf("a use case line has %d characters: %q", n, l)
			}
			if !strings.HasSuffix(l, "…") {
				t.Errorf("a cut title has no mark: %q", l)
			}
			return
		}
	}
	t.Errorf("no line for the use case:\n%s", got)
}

func TestCommitsWithNoTicketAreOneLine(t *testing.T) {
	commits := []string{`4405647 "start without the tool"`, `435164a "QA: setup"`, `2e14da4 "tidy"`, `1cdf78f "Set up"`}
	cases := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{commits[:1], `1 commit has no ticket: 4405647 "start without the tool"`},
		{commits[:2], `2 commits have no ticket: 4405647 "start without the tool", 435164a "QA: setup"`},
		{commits[:3], `3 commits have no ticket: 4405647 "start without the tool", 435164a "QA: setup", 2e14da4 "tidy"`},
		{commits, `4 commits have no ticket: 4405647 "start without the tool", 435164a "QA: setup", and 2 more`},
	}
	for _, c := range cases {
		if got := status.NoTicketLine(c.in); got != c.want {
			t.Errorf("line %q, want %q", got, c.want)
		}
	}
	got := render(t, status.Input{Entries: []obligation.Entry{entry("a/1", obligation.Verified, nil)}, NoTicket: commits})
	if !strings.Contains(got, "\n"+cases[4].want+"\n") || strings.Count(got, "no ticket") != 1 {
		t.Errorf("text:\n%s", got)
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
	for _, want := range []string{"  not planned   1  → semar plan\n    SHOP-490\n", "Ship: no. 1 open.\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("text lacks %q:\n%s", want, got)
		}
	}
}

func TestAVerifiedUseCaseHasNoNoteOfWhoApprovedIt(t *testing.T) {
	in := status.Input{Branch: "b", Base: "main", Environment: "uat", BuildsRead: true, Tickets: []string{"SHOP-482"},
		Entries: []obligation.Entry{entry("SHOP-482/1", obligation.Verified, nil), entry("SHOP-482/2", obligation.Verified, nil)}}
	if got := render(t, in); strings.Contains(got, "approved") {
		t.Errorf("text mentions an approval:\n%s", got)
	}
	for _, g := range status.Groups {
		if g == "needs review" {
			t.Error("the groups hold needs review")
		}
	}
}

func htmlInput() status.Input {
	return status.Input{
		Branch: "feature/refund-limit", Base: "main", Environment: "uat",
		Builds:     []domain.BuildRef{{Service: "orders", Commit: "4f2a91c0000"}},
		BuildsRead: true,
		Tickets:    []string{"SHOP-482"},
		Entries: []obligation.Entry{
			entry("SHOP-482/1", obligation.Verified, func(e *obligation.Entry) {
				e.Covers = []obligation.Cover{{Scenario: "SHOP-482-01", Found: true, Outcome: "passed", Run: "r-20261117T101502Z-ab12"}}
			}),
			entry("SHOP-482/2", obligation.Failed, func(e *obligation.Entry) {
				e.Obligation.Title = `A refund <b>above</b> the paid amount & more`
				e.Covers = []obligation.Cover{{Scenario: "SHOP-482-02", Found: true, Outcome: "failed", Run: "r-20261117T101502Z-ab12"}}
				e.Reasons = []string{"SHOP-482-02 failed in r-20261117T101502Z-ab12: expected status 422, observed status 200"}
			}),
			entry("SHOP-482/manual-1", obligation.NeedsAPerson, func(e *obligation.Entry) {
				e.Obligation.Manual = &obligation.Manual{Kind: "judgement", Text: "Check the refund receipt on a phone",
					Steps: []string{"Open the receipt", "Read the refund line"}, Expected: "The refund is shown", WrittenBy: "person"}
				e.Reasons = []string{"Check the refund receipt on a phone"}
			}),
		},
	}
}

func TestHTMLHoldsTheSameContentAsOneFileWithNoOutsideResource(t *testing.T) {
	var b bytes.Buffer
	if err := status.HTML(&b, status.Build(htmlInput())); err != nil {
		t.Fatal(err)
	}
	page := b.String()
	for _, want := range []string{
		"On feature/refund-limit against main · uat · orders at 4f2a91c",
		"3 use cases of SHOP-482",
		"Ship: no. 1 failing, 1 open.",
		"verified", "failing", "for a person",
		"SHOP-482/1", "SHOP-482/2", "SHOP-482/manual-1",
		"expected status 422, observed status 200",
		"r-20261117T101502Z-ab12",
		"Open the receipt", "Read the refund line", "The refund is shown",
		"semar check SHOP-482/manual-1 pass",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	for _, outside := range []string{"http://", "https://", "<script", "<link", "@import", "url(", "src="} {
		if strings.Contains(page, outside) {
			t.Errorf("the page holds %q, so it needs the network or runs code", outside)
		}
	}
	if strings.Contains(page, "<b>above</b>") || !strings.Contains(page, "&lt;b&gt;above&lt;/b&gt; the paid amount &amp; more") {
		t.Errorf("text from a file is not escaped:\n%s", page)
	}
	if !strings.HasPrefix(page, "<!doctype html>") || !strings.Contains(page, `<meta charset="utf-8">`) || !strings.Contains(page, "<title>") {
		t.Errorf("the page is not a complete document:\n%.200s", page)
	}
}

func TestHTMLIsAStableFunctionOfItsInput(t *testing.T) {
	var a, b bytes.Buffer
	if err := status.HTML(&a, status.Build(htmlInput())); err != nil {
		t.Fatal(err)
	}
	if err := status.HTML(&b, status.Build(htmlInput())); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Error("two renderings of one input differ")
	}
}

func TestAFlakyPassIsNamedUnderTheVerifiedUseCase(t *testing.T) {
	flaky := entry("SHOP-482/1", obligation.Verified, func(e *obligation.Entry) {
		e.Covers = []obligation.Cover{{Scenario: "SHOP-482-01", Found: true, Outcome: "passed", Attempts: "flaky, passed on attempt 2 of 3"}}
	})
	steady := entry("SHOP-482/2", obligation.Verified, func(e *obligation.Entry) {
		e.Covers = []obligation.Cover{{Scenario: "SHOP-482-02", Found: true, Outcome: "passed"}}
	})
	rep := status.Build(status.Input{Environment: "local", BuildsRead: true, Entries: []obligation.Entry{flaky, steady}})
	if rep.Code != 0 {
		t.Errorf("a flaky pass changed the exit code to %d", rep.Code)
	}
	var b bytes.Buffer
	if err := status.Text(&b, rep); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "SHOP-482-01: flaky, passed on attempt 2 of 3") {
		t.Errorf("the status does not name the flaky scenario:\n%s", out)
	}
	if strings.Contains(out, "SHOP-482/2") {
		t.Errorf("a steady use case is listed under verified:\n%s", out)
	}
}

func TestTheLeadNamesWhatARunPickedWhenItReadNoChange(t *testing.T) {
	r := status.Build(status.Input{Scope: "the smoke suite", Environment: "local"})
	if got := r.Lead(); !strings.HasPrefix(got, "The smoke suite") || strings.Contains(got, "git repository") {
		t.Errorf("lead = %q", got)
	}
}

func TestTheStatusCountsTheResultsAModelWrote(t *testing.T) {
	covers := func(c ...obligation.Cover) func(*obligation.Entry) {
		return func(e *obligation.Entry) { e.Covers = c }
	}
	a := obligation.Cover{Scenario: "A", Found: true, ByModel: 1, Results: 2}
	b := obligation.Cover{Scenario: "B", Found: true, ByModel: 0, Results: 1}
	in := status.Input{Entries: []obligation.Entry{
		entry("SHOP-482/1", obligation.Verified, covers(a)),
		entry("SHOP-482/2", obligation.Verified, covers(a, b)),
	}}
	r := status.Build(in)
	if r.ByModel != 1 || r.Results != 3 {
		t.Fatalf("ByModel %d Results %d, want 1 and 3: A counts once", r.ByModel, r.Results)
	}
	var out bytes.Buffer
	if err := status.Text(&out, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "results written by a model: 1 of 3\n") {
		t.Errorf("text lacks the line:\n%s", out.String())
	}

	none := status.Build(status.Input{Entries: []obligation.Entry{
		entry("SHOP-482/1", obligation.Verified, covers(obligation.Cover{Scenario: "A", Found: true})),
	}})
	out.Reset()
	if err := status.Text(&out, none); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "written by a model") {
		t.Errorf("the line is there with no model result:\n%s", out.String())
	}
}
