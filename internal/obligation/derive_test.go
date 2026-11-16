package obligation_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/obligation"
)

var now = time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)

func ob(id string, mod func(*obligation.Obligation)) obligation.Located {
	o := obligation.Obligation{ID: id, Title: "Title of " + id, Source: obligation.SourceDiff}
	if mod != nil {
		mod(&o)
	}
	return obligation.Located{Obligation: o, File: ".semar/obligations/x.yaml", Line: 1}
}

func covers(ids ...domain.ScenarioID) func(*obligation.Obligation) {
	return func(o *obligation.Obligation) { o.Covers = ids }
}

var (
	ordersBuild = domain.BuildRef{Service: "orders", Commit: "9f3c2ab0000", ConfigHash: "cfg1"}
	current     = []domain.BuildRef{{Service: "orders", Commit: "9f3c2ab0000", ConfigHash: "cfg1"}}
)

func res(id domain.ScenarioID, outcome, reason string) obligation.Cover {
	return obligation.Cover{Scenario: id, Found: true, Outcome: outcome, Run: "r-20261117T090000Z-1a2b", Reason: reason, Builds: []domain.BuildRef{ordersBuild}}
}

func results(cs ...obligation.Cover) obligation.Results {
	r := obligation.Results{Scenarios: map[domain.ScenarioID]obligation.Cover{}, Current: current}
	for _, c := range cs {
		r.Scenarios[c.Scenario] = c
	}
	return r
}

func manualReason(o *obligation.Obligation) {
	o.Manual = &obligation.Manual{Kind: "judgement", Text: "A person decides", Steps: []string{"Look"}, Expected: "Same", WrittenBy: "code"}
}

func one(t *testing.T, o obligation.Located, r obligation.Results, d map[string]obligation.Deferral, m map[string]obligation.ManualResult) obligation.Entry {
	t.Helper()
	got := obligation.Derive([]obligation.Located{o}, r, d, m, now)
	if len(got) != 1 {
		t.Fatalf("got %d entries", len(got))
	}
	return got[0]
}

func hasReason(e obligation.Entry, want string) bool {
	for _, r := range e.Reasons {
		if r == want {
			return true
		}
	}
	return false
}

func mustReason(t *testing.T, e obligation.Entry, want string) {
	t.Helper()
	if !hasReason(e, want) {
		t.Errorf("reasons %q lack %q", e.Reasons, want)
	}
}

func active(id string) map[string]obligation.Deferral {
	return map[string]obligation.Deferral{id: {Obligation: id, User: "tester", Reason: "later", Until: time.Date(2026, 12, 1, 23, 59, 59, 0, time.UTC)}}
}

func TestDeriveTable(t *testing.T) {
	tests := []struct {
		name   string
		o      obligation.Located
		r      obligation.Results
		defers map[string]obligation.Deferral
		want   obligation.State
		reason string // an exact reason, or empty for none
	}{
		{"1 failed", ob("g/a", covers("A-1")), results(res("A-1", "failed", "status was 500")), nil, obligation.Failed,
			"A-1 failed in r-20261117T090000Z-1a2b: status was 500"},
		{"1 failed beats a deferral", ob("g/a", covers("A-1")), results(res("A-1", "failed", "x")), active("g/a"), obligation.Failed, ""},
		{"1 failed beats a manual reason", ob("g/a", func(o *obligation.Obligation) { o.Covers = []domain.ScenarioID{"A-1"}; manualReason(o) }), results(res("A-1", "failed", "x")), nil, obligation.Failed, ""},
		{"2 deferred", ob("g/a", nil), results(), active("g/a"), obligation.Deferred, "deferred by tester until 2026-12-01: later"},
		{"2 deferred beats a manual reason", ob("g/a", manualReason), results(), active("g/a"), obligation.Deferred, ""},
		{"2 deferred beats passed", ob("g/a", covers("A-1")), results(res("A-1", "passed", "")), active("g/a"), obligation.Deferred, ""},
		{"3 needs a person", ob("g/a", manualReason), results(), nil, obligation.NeedsAPerson, "A person decides"},
		{"3 needs a person even when the covers passed", ob("g/a", func(o *obligation.Obligation) { o.Covers = []domain.ScenarioID{"A-1"}; manualReason(o) }), results(res("A-1", "passed", "")), nil, obligation.NeedsAPerson, ""},
		{"4 verified", ob("g/a", covers("A-1", "A-2")), results(res("A-1", "passed", ""), res("A-2", "passed", "")), nil, obligation.Verified, ""},
		{"4 verified with a gap that does not matter", ob("g/a", func(o *obligation.Obligation) { o.Covers = []domain.ScenarioID{"A-1"}; o.Gap = "x" }), results(res("A-1", "passed", "")), nil, obligation.Verified, ""},
		{"5 one cover not passed", ob("g/a", covers("A-1", "A-2")), results(res("A-1", "passed", ""), res("A-2", "inconclusive", "an expected result is not confirmed")), nil, obligation.Blocked,
			"A-2 is inconclusive in r-20261117T090000Z-1a2b: an expected result is not confirmed"},
		{"5 a gap", ob("g/a", func(o *obligation.Obligation) { o.Gap = "not drafted: needs fixture order.paid" }), results(), nil, obligation.Blocked, "not drafted: needs fixture order.paid"},
		{"5 no test and no reason", ob("g/a", nil), results(), nil, obligation.Blocked, "use case with no test and no reason"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := one(t, tt.o, tt.r, tt.defers, nil)
			if e.State != tt.want {
				t.Fatalf("state %s (%q), want %s", e.State, e.Reasons, tt.want)
			}
			if tt.reason != "" {
				mustReason(t, e, tt.reason)
			}
			if e.NoReason != (tt.name == "5 no test and no reason") {
				t.Errorf("NoReason is %v", e.NoReason)
			}
		})
	}
}

func TestFailedShowsUnderDeferral(t *testing.T) {
	e := one(t, ob("g/a", covers("A-1")), results(res("A-1", "failed", "boom")), active("g/a"), nil)
	if e.State != obligation.Failed {
		t.Fatalf("state %s", e.State)
	}
	if e.Deferral == nil {
		t.Error("the deferral is not shown beside the failure")
	}
	mustReason(t, e, "deferred by tester until 2026-12-01: later")
}

func TestExpiredDeferralIgnored(t *testing.T) {
	d := map[string]obligation.Deferral{"g/a": {Obligation: "g/a", User: "tester", Reason: "later", Until: time.Date(2026, 11, 1, 23, 59, 59, 0, time.UTC)}}
	e := one(t, ob("g/a", covers("A-1")), results(res("A-1", "passed", "")), d, nil)
	if e.State != obligation.Verified || e.Deferral != nil || e.Expired == nil {
		t.Fatalf("state %s, deferral %v, expired %v", e.State, e.Deferral, e.Expired)
	}
	mustReason(t, e, "the deferral by tester expired on 2026-11-01")

	e = one(t, ob("g/a", nil), results(), d, nil)
	if e.State != obligation.Blocked || !e.NoReason {
		t.Errorf("without a deferral the use case has no reason: %s %v", e.State, e.NoReason)
	}

	at := d["g/a"]
	at.Until = now
	e = one(t, ob("g/a", nil), results(), map[string]obligation.Deferral{"g/a": at}, nil)
	if e.State == obligation.Deferred {
		t.Error("a deferral that ends now is not after now")
	}
}

func TestManualResultDoesNotChangeState(t *testing.T) {
	for _, res := range []string{"passed", "failed"} {
		m := map[string]obligation.ManualResult{"g/a": {Obligation: "g/a", Result: res, User: "tester", At: now}}
		e := one(t, ob("g/a", manualReason), results(), nil, m)
		if e.State != obligation.NeedsAPerson || e.Manual == nil {
			t.Fatalf("%s: state %s", res, e.State)
		}
		mustReason(t, e, res+", checked by a person")
	}
	// Not even a pass of a person makes a use case verified.
	m := map[string]obligation.ManualResult{"g/a": {Obligation: "g/a", Result: "passed", User: "tester", At: now}}
	if e := one(t, ob("g/a", nil), results(), nil, m); e.State == obligation.Verified {
		t.Error("a manual result verified an obligation")
	}
}

func TestInconclusiveStaleNotRunAreBlocked(t *testing.T) {
	r := results(
		res("A-inc", "inconclusive", "an expected result is not confirmed"),
		res("A-blk", "blocked", "setup step s1 failed"),
		res("A-nr", "not_run", ""),
		res("A-st", "stale", "the expectation changed"),
		obligation.Cover{Scenario: "A-none"},
	)
	tests := []struct{ id, want string }{
		{"A-inc", "A-inc is inconclusive in r-20261117T090000Z-1a2b: an expected result is not confirmed"},
		{"A-blk", "A-blk is blocked in r-20261117T090000Z-1a2b: setup step s1 failed"},
		{"A-nr", "A-nr is not run in r-20261117T090000Z-1a2b"},
		{"A-st", "A-st is stale in r-20261117T090000Z-1a2b: the expectation changed"},
		{"A-none", "A-none has no result: run it"},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			e := one(t, ob("g/a", covers(domain.ScenarioID(tt.id))), r, nil, nil)
			if e.State != obligation.Blocked {
				t.Fatalf("state %s", e.State)
			}
			mustReason(t, e, tt.want)
			if e.NoReason {
				t.Error("a covered use case has a reason")
			}
		})
	}
}

func TestDeletedScenarioBlocked(t *testing.T) {
	e := one(t, ob("g/a", covers("A-gone")), results(), nil, nil)
	if e.State != obligation.Blocked {
		t.Fatalf("state %s", e.State)
	}
	mustReason(t, e, "A-gone was deleted: no scenario or journey covers this")
}

func TestStaleByCurrentBuilds(t *testing.T) {
	old := res("A-1", "passed", "")
	old.Builds = []domain.BuildRef{{Service: "orders", Commit: "9f3c2ab0000", ConfigHash: "cfg1"}}
	newer := []domain.BuildRef{{Service: "orders", Commit: "c0ffee10000", ConfigHash: "cfg1"}}

	t.Run("another commit", func(t *testing.T) {
		r := results(old)
		r.Current = newer
		e := one(t, ob("g/a", covers("A-1")), r, nil, nil)
		if e.State != obligation.Blocked {
			t.Fatalf("state %s", e.State)
		}
		mustReason(t, e, "A-1 ran on orders 9f3c2ab, and the environment runs c0ffee1: rerun on the current build")
	})
	t.Run("another configuration", func(t *testing.T) {
		r := results(old)
		r.Current = []domain.BuildRef{{Service: "orders", Commit: "9f3c2ab0000", ConfigHash: "cfg2"}}
		e := one(t, ob("g/a", covers("A-1")), r, nil, nil)
		if e.State != obligation.Blocked || !strings.Contains(strings.Join(e.Reasons, "|"), "with another configuration") {
			t.Fatalf("state %s, %q", e.State, e.Reasons)
		}
	})
	t.Run("a service the result did not touch is not compared", func(t *testing.T) {
		r := results(old)
		r.Current = append([]domain.BuildRef{{Service: "payments", Commit: "abc1234000", ConfigHash: "c"}}, current...)
		if e := one(t, ob("g/a", covers("A-1")), r, nil, nil); e.State != obligation.Verified {
			t.Fatalf("state %s: %q", e.State, e.Reasons)
		}
	})
	t.Run("a result with no recorded build", func(t *testing.T) {
		r := results(old)
		none := old
		none.Builds = nil
		r.Scenarios["A-1"] = none
		e := one(t, ob("g/a", covers("A-1")), r, nil, nil)
		if e.State != obligation.Blocked {
			t.Fatalf("state %s", e.State)
		}
		mustReason(t, e, "A-1 ran with no recorded build, so it cannot be matched to the current build: rerun on the current build")
	})
	t.Run("builds not read", func(t *testing.T) {
		r := results(old)
		r.Current = nil
		if e := one(t, ob("g/a", covers("A-1")), r, nil, nil); e.State != obligation.Verified {
			t.Fatalf("state %s", e.State)
		}
	})
	t.Run("the same builds", func(t *testing.T) {
		if e := one(t, ob("g/a", covers("A-1")), results(old), nil, nil); e.State != obligation.Verified {
			t.Fatalf("state %s", e.State)
		}
	})
	t.Run("a service of the result that was not read now", func(t *testing.T) {
		r := results(old)
		old2 := old
		old2.Builds = append(old2.Builds, domain.BuildRef{Service: "retired", Commit: "1"})
		r.Scenarios["A-1"] = old2
		e := one(t, ob("g/a", covers("A-1")), r, nil, nil)
		if e.State != obligation.Blocked {
			t.Fatalf("state %s", e.State)
		}
		mustReason(t, e, "A-1 ran on retired 1, and the build of retired was not read now: rerun on the current build")
	})
	t.Run("a failure on another build still shows", func(t *testing.T) {
		bad := res("A-1", "failed", "boom")
		r := results(bad)
		r.Current = newer
		if e := one(t, ob("g/a", covers("A-1")), r, nil, nil); e.State != obligation.Failed {
			t.Fatalf("state %s", e.State)
		}
	})
}

// Under --env, Current holds every probed service. A journey records only its own services, and
// that must not make it stale.
func TestAJourneyOnAFewServicesIsCurrent(t *testing.T) {
	paymentsBuild := domain.BuildRef{Service: "payments", Commit: "abc1234000", ConfigHash: "c"}
	r := results()
	r.Current = []domain.BuildRef{ordersBuild, paymentsBuild}
	r.Journeys = map[string]obligation.Cover{"checkout": {Journey: "checkout", Found: true, Outcome: "passed", Run: "r-1", Builds: []domain.BuildRef{ordersBuild}}}
	o := ob("g/a", func(o *obligation.Obligation) { o.Journeys = []string{"checkout"} })
	if e := one(t, o, r, nil, nil); e.State != obligation.Verified {
		t.Fatalf("state %s: %q", e.State, e.Reasons)
	}
}

// In the report of a journey run, Current holds the services of its journeys only. A scenario that
// ran on an old build of another service is not current for that reason.
func TestAScenarioOnAServiceNotReadNowIsNotCurrent(t *testing.T) {
	paymentsBuild := domain.BuildRef{Service: "payments", Commit: "abc1234000", ConfigHash: "c"}
	c := res("A-1", "passed", "")
	c.Builds = []domain.BuildRef{{Service: "orders", Commit: "0ld0ld00000", ConfigHash: "cfg1"}, paymentsBuild}
	r := results(c)
	r.Current = []domain.BuildRef{paymentsBuild}
	e := one(t, ob("g/a", covers("A-1")), r, nil, nil)
	if e.State != obligation.Blocked {
		t.Fatalf("state %s: %q", e.State, e.Reasons)
	}
	mustReason(t, e, "A-1 ran on orders 0ld0ld0, and the build of orders was not read now: rerun on the current build")
}

func TestNoReasonListedFirst(t *testing.T) {
	obs := []obligation.Located{
		ob("g/verified", covers("A-1")),
		ob("g/deferred", nil),
		ob("g/person", manualReason),
		ob("g/blocked", func(o *obligation.Obligation) { o.Gap = "x" }),
		ob("g/failed", covers("A-2")),
		ob("g/empty", nil),
		ob("g/empty2", nil),
	}
	r := results(res("A-1", "passed", ""), res("A-2", "failed", "x"))
	got := obligation.Derive(obs, r, active("g/deferred"), nil, now)
	var ids []string
	for _, e := range got {
		ids = append(ids, e.Obligation.ID)
	}
	want := "g/empty g/empty2 g/failed g/blocked g/person g/deferred g/verified"
	if strings.Join(ids, " ") != want {
		t.Fatalf("order %v, want %s", ids, want)
	}
	if !got[0].NoReason || !got[1].NoReason || got[2].NoReason {
		t.Errorf("NoReason flags: %v %v %v", got[0].NoReason, got[1].NoReason, got[2].NoReason)
	}
}

func TestJourneyCoverWithoutResults(t *testing.T) {
	o := ob("g/a", func(o *obligation.Obligation) { o.Journeys = []string{"checkout"} })
	e := one(t, o, results(), nil, nil)
	if e.State != obligation.Blocked {
		t.Fatalf("state %s", e.State)
	}
	mustReason(t, e, "journey checkout: journey results are not read yet")
	if e.NoReason {
		t.Error("a journey is a cover")
	}

	r := results()
	r.Journeys = map[string]obligation.Cover{
		"checkout": {Journey: "checkout", Found: true, Outcome: "passed", Run: "r-1", Builds: []domain.BuildRef{ordersBuild}},
		"unrun":    {Journey: "unrun"},
		"broken":   {Journey: "broken", Found: true, Outcome: "failed", Run: "r-1", Reason: "step 2"},
	}
	if e := one(t, o, r, nil, nil); e.State != obligation.Verified {
		t.Errorf("a passed journey: %s %q", e.State, e.Reasons)
	}
	for id, want := range map[string]string{
		"unrun":  "journey unrun has no result: run it",
		"gone":   "journey gone was deleted: no scenario or journey covers this",
		"broken": "journey broken failed in r-1: step 2",
	} {
		o := ob("g/a", func(o *obligation.Obligation) { o.Journeys = []string{id} })
		e := one(t, o, r, nil, nil)
		mustReason(t, e, want)
	}
}

func TestCoversInEntryAreOrdered(t *testing.T) {
	o := ob("g/a", func(o *obligation.Obligation) {
		o.Covers = []domain.ScenarioID{"A-1", "A-9"}
		o.Journeys = []string{"j"}
	})
	e := one(t, o, results(res("A-1", "passed", "")), nil, nil)
	if len(e.Covers) != 3 || e.Covers[0].Scenario != "A-1" || !e.Covers[0].Found || e.Covers[1].Scenario != "A-9" || e.Covers[2].Journey != "j" {
		t.Errorf("covers %+v", e.Covers)
	}
}

func TestSummaryLine(t *testing.T) {
	mk := func(counts map[obligation.State]int) []obligation.Entry {
		var es []obligation.Entry
		for _, s := range []obligation.State{obligation.Verified, obligation.Failed, obligation.Blocked, obligation.NeedsAPerson, obligation.Deferred} {
			for range counts[s] {
				es = append(es, obligation.Entry{State: s})
			}
		}
		return es
	}
	tests := []struct {
		name string
		es   []obligation.Entry
		want string
	}{
		{"the PRD example", mk(map[obligation.State]int{obligation.Verified: 35, obligation.Failed: 2, obligation.Blocked: 3, obligation.NeedsAPerson: 2}), "42 use cases: 35 verified, 2 failed, 3 blocked, 2 need a person"},
		{"one verified", mk(map[obligation.State]int{obligation.Verified: 1}), "1 use case: 1 verified"},
		{"one needs a person", mk(map[obligation.State]int{obligation.NeedsAPerson: 1}), "1 use case: 0 verified, 1 needs a person"},
		{"one of two needs a person", mk(map[obligation.State]int{obligation.Verified: 1, obligation.NeedsAPerson: 1}), "2 use cases: 1 verified, 1 needs a person"},
		{"deferred last", mk(map[obligation.State]int{obligation.Verified: 1, obligation.Deferred: 2, obligation.Blocked: 1}), "4 use cases: 1 verified, 1 blocked, 2 deferred"},
		{"none verified", mk(map[obligation.State]int{obligation.Blocked: 3}), "3 use cases: 0 verified, 3 blocked"},
		{"empty", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := obligation.SummaryLine(tt.es); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("manual results are counted apart", func(t *testing.T) {
		es := mk(map[obligation.State]int{obligation.Verified: 1, obligation.NeedsAPerson: 2})
		es[1].Manual = &obligation.ManualResult{Result: "passed"}
		es[2].Manual = &obligation.ManualResult{Result: "failed"}
		want := "3 use cases: 1 verified, 2 need a person. Checked by a person: 1 passed, 1 failed."
		if got := obligation.SummaryLine(es); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestStateNames(t *testing.T) {
	for s, want := range map[obligation.State]string{obligation.Verified: "verified", obligation.Failed: "failed", obligation.Blocked: "blocked", obligation.NeedsAPerson: "needs_a_person", obligation.Deferred: "deferred"} {
		if string(s) != want {
			t.Errorf("%s != %s", s, want)
		}
	}
}
