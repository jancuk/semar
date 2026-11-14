package verdict_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/verdict"
)

func intp(n int) *int { return &n }

func ok(id string) *domain.Action {
	return &domain.Action{ID: domain.ActionID(id), Response: &domain.HTTPResponse{Status: 200}}
}

func trueResult() check.Result {
	return check.Result{State: domain.CheckTrue, Expected: "status is 200", Observed: "status 200"}
}

func expectation(cite string) domain.Expectation {
	return domain.Expectation{Check: domain.Check{Status: intp(200)}, Source: domain.SourceTicket, Cite: cite}
}

func scenario() domain.Scenario {
	return domain.Scenario{
		ID: "refunds",
		Steps: []domain.Step{
			{ID: "create-order", Setup: true},
			{ID: "refund-too-much", Expect: []domain.Expectation{expectation("refund-too-much/0"), expectation("refund-too-much/1")}},
			{ID: "order-unchanged", Expect: []domain.Expectation{expectation("order-unchanged/0")}},
		},
	}
}

func pass() verdict.Input {
	inv := func() []check.Result {
		return []check.Result{{State: domain.CheckTrue, Expected: "no server error", Observed: "status 200"}}
	}
	return verdict.Input{
		Scenario: scenario(),
		Steps: map[domain.StepID]verdict.StepEvidence{
			"create-order":    {Action: ok("a1"), Invariants: inv()},
			"refund-too-much": {Action: ok("a2"), Checks: []check.Result{trueResult(), trueResult()}, Invariants: inv()},
			"order-unchanged": {Action: ok("a3"), Checks: []check.Result{trueResult()}, Invariants: inv()},
		},
		Confirmed:   func(domain.Expectation) bool { return true },
		BuildsStart: []domain.BuildRef{{Service: "orders", Commit: "bbbbbbb", ConfigHash: "c1"}},
		BuildsEnd:   []domain.BuildRef{{Service: "orders", Commit: "bbbbbbb", ConfigHash: "c1"}},
	}
}

type mod func(*verdict.Input)

func with(base func() verdict.Input, mods ...mod) func() verdict.Input {
	return func() verdict.Input {
		in := base()
		for _, m := range mods {
			m(&in)
		}
		return in
	}
}

func change(id domain.StepID, f func(*verdict.StepEvidence)) mod {
	return func(in *verdict.Input) {
		ev := in.Steps[id]
		f(&ev)
		in.Steps[id] = ev
	}
}

func notRun(why string) mod     { return func(in *verdict.Input) { in.NotRun = why } }
func blocked(why string) mod    { return func(in *verdict.Input) { in.Blocked = append(in.Blocked, why) } }
func retried(in *verdict.Input) { in.Retried = true }

func buildsEnd(service, commit string) mod {
	return func(in *verdict.Input) {
		in.BuildsEnd = []domain.BuildRef{{Service: service, Commit: commit, ConfigHash: "c1"}}
	}
}

func buildsEndAdds(service string) mod {
	return func(in *verdict.Input) {
		in.BuildsEnd = append(append([]domain.BuildRef{}, in.BuildsEnd...), domain.BuildRef{Service: service, Commit: "x", ConfigHash: "y"})
	}
}

func configChanged(in *verdict.Input) {
	in.BuildsEnd = []domain.BuildRef{{Service: "orders", Commit: "bbbbbbb", ConfigHash: "c2"}}
}

func setupStatus(status int) mod {
	return change("create-order", func(ev *verdict.StepEvidence) {
		ev.Action = &domain.Action{ID: "a1", Response: &domain.HTTPResponse{Status: status}}
	})
}

func setupRefused(why string) mod {
	return change("create-order", func(ev *verdict.StepEvidence) {
		ev.Action = &domain.Action{ID: "a1", Refused: why}
	})
}

func setupMissing(in *verdict.Input) { delete(in.Steps, "create-order") }

func setupTransportError(in *verdict.Input) {
	ev := in.Steps["create-order"]
	ev.Action = &domain.Action{ID: "a1", Err: "connection reset"}
	in.Steps["create-order"] = ev
}

func stepRefused(id domain.StepID, why string) mod {
	return change(id, func(ev *verdict.StepEvidence) {
		ev.Action = &domain.Action{ID: "a9", Refused: why}
		for i := range ev.Checks {
			ev.Checks[i] = check.Result{State: domain.CheckNotEvaluated, Detail: "no response was recorded"}
		}
	})
}

func checkState(id domain.StepID, i int, state domain.CheckState) mod {
	return change(id, func(ev *verdict.StepEvidence) {
		ev.Checks[i].State = state
		ev.Checks[i].Expected = "status is 200"
		ev.Checks[i].Observed = "status 402"
		if state == domain.CheckNotEvaluated {
			ev.Checks[i].Detail = "no response was recorded"
		}
	})
}

func invariant(id domain.StepID, state domain.CheckState) mod {
	return change(id, func(ev *verdict.StepEvidence) { ev.Invariants[0].State = state })
}

func unconfirmed(id domain.StepID, i int) mod {
	return func(in *verdict.Input) {
		prev := in.Confirmed
		cite := string(id) + "/" + string(rune('0'+i))
		in.Confirmed = func(e domain.Expectation) bool { return e.Cite != cite && prev(e) }
	}
}

func noAction(id domain.StepID) mod {
	return func(in *verdict.Input) { delete(in.Steps, id) }
}

func effectUnknown(id domain.StepID) mod {
	return change(id, func(ev *verdict.StepEvidence) {
		a := *ev.Action
		a.EffectUnknown = true
		ev.Action = &a
	})
}

func noChecks() verdict.Input {
	in := pass()
	for i := range in.Scenario.Steps {
		in.Scenario.Steps[i].Expect = nil
	}
	for id, ev := range in.Steps {
		ev.Checks = nil
		in.Steps[id] = ev
	}
	return in
}

func nilConfirmed(in *verdict.Input) { in.Confirmed = nil }

func emptyScenario() verdict.Input {
	in := pass()
	in.Scenario.Steps = nil
	in.Steps = nil
	return in
}

func shortChecks(in *verdict.Input) {
	ev := in.Steps["refund-too-much"]
	ev.Checks = ev.Checks[:1]
	in.Steps["refund-too-much"] = ev
}

func unknownStepInMap(in *verdict.Input) {
	in.Steps["ghost"] = verdict.StepEvidence{Action: ok("zz"), Checks: []check.Result{trueResult()}}
}

func longChecks(in *verdict.Input) {
	ev := in.Steps["order-unchanged"]
	ev.Checks = append(ev.Checks, trueResult(), trueResult())
	in.Steps["order-unchanged"] = ev
}

func singleStep() verdict.Input {
	return verdict.Input{
		Scenario: domain.Scenario{ID: "one", Steps: []domain.Step{{ID: "only", Expect: []domain.Expectation{expectation("only/0")}}}},
		Steps: map[domain.StepID]verdict.StepEvidence{
			"only": {Action: ok("a1"), Checks: []check.Result{trueResult()}},
		},
		Confirmed:   func(domain.Expectation) bool { return true },
		BuildsStart: []domain.BuildRef{{Service: "orders", Commit: "bbbbbbb", ConfigHash: "c1"}},
		BuildsEnd:   []domain.BuildRef{{Service: "orders", Commit: "bbbbbbb", ConfigHash: "c1"}},
	}
}

// writeNeverRan: the write under test (refund) has no expectation and no recorded result,
// while the setup and the verifying step both look fine.
func writeNeverRan() verdict.Input {
	return verdict.Input{
		Scenario: domain.Scenario{ID: "write", Steps: []domain.Step{
			{ID: "setup", Setup: true},
			{ID: "refund"},
			{ID: "verify", Expect: []domain.Expectation{expectation("verify/0")}},
		}},
		Steps: map[domain.StepID]verdict.StepEvidence{
			"setup":  {Action: ok("a1")},
			"verify": {Action: ok("a3"), Checks: []check.Result{trueResult()}},
		},
		Confirmed:   func(domain.Expectation) bool { return true },
		BuildsStart: []domain.BuildRef{{Service: "orders", Commit: "bbbbbbb", ConfigHash: "c1"}},
		BuildsEnd:   []domain.BuildRef{{Service: "orders", Commit: "bbbbbbb", ConfigHash: "c1"}},
	}
}

func refundAction(a *domain.Action) mod {
	return func(in *verdict.Input) { in.Steps["refund"] = verdict.StepEvidence{Action: a} }
}

// failedOnly: one non-setup step, one expectation, a check recorded as true.
func failedOnly(a *domain.Action) func() verdict.Input {
	return func() verdict.Input {
		in := singleStep()
		in.Steps["only"] = verdict.StepEvidence{Action: a, Checks: []check.Result{trueResult()}}
		return in
	}
}

// setupOnlyExpectations: the only expectation sits on the setup step.
func setupOnlyExpectations() verdict.Input {
	in := pass()
	for i := range in.Scenario.Steps {
		in.Scenario.Steps[i].Expect = nil
	}
	in.Scenario.Steps[0].Expect = []domain.Expectation{expectation("create-order/0")}
	for id, ev := range in.Steps {
		ev.Checks = nil
		in.Steps[id] = ev
	}
	ev := in.Steps["create-order"]
	ev.Checks = []check.Result{trueResult()}
	in.Steps["create-order"] = ev
	return in
}

func duplicateStep(in *verdict.Input) {
	in.Scenario.Steps = append(in.Scenario.Steps, in.Scenario.Steps[2])
}

func noBuilds(start, end bool) mod {
	return func(in *verdict.Input) {
		if start {
			in.BuildsStart = nil
		}
		if end {
			in.BuildsEnd = nil
		}
	}
}

func TestCompute(t *testing.T) {
	cases := []struct {
		name        string
		in          func() verdict.Input
		want        domain.Outcome
		wantReasons []string
	}{
		{"all true and confirmed", pass, domain.OutcomePassed, nil},
		{"single step, all true and confirmed", singleStep, domain.OutcomePassed, nil},
		{"not run", with(pass, notRun("the surface is not supported")), domain.OutcomeNotRun, []string{"the surface is not supported"}},
		{"not run wins over blocked", with(pass, notRun("filtered out"), blocked("sign-in failed")), domain.OutcomeNotRun, []string{"filtered out", "sign-in failed"}},
		{"blocked before the run", with(pass, blocked("sign-in failed")), domain.OutcomeBlocked, []string{"sign-in failed"}},
		{"setup step answered 500", with(pass, setupStatus(500)), domain.OutcomeBlocked, []string{"setup failed: create-order answered 500"}},
		{"setup step answered 400", with(pass, setupStatus(400)), domain.OutcomeBlocked, []string{"setup failed: create-order answered 400"}},
		{"setup step answered 399 is fine", with(pass, setupStatus(399)), domain.OutcomePassed, nil},
		{"setup step has no action", with(pass, setupMissing), domain.OutcomeBlocked, []string{"setup failed: create-order"}},
		{"setup step has a transport error", with(pass, setupTransportError), domain.OutcomeBlocked, []string{"setup failed: create-order failed: connection reset"}},
		{"setup step was refused", with(pass, setupRefused("read_only")), domain.OutcomeBlocked, []string{"the environment does not allow a step create-order: read_only"}},
		{"step refused by policy", with(pass, stepRefused("refund-too-much", "denied")), domain.OutcomeBlocked, []string{"the environment does not allow a step refund-too-much: denied"}},
		{"build changed", with(pass, buildsEnd("orders", "aaaaaaa")), domain.OutcomeStale, []string{"the build changed during the run"}},
		{"configuration changed", with(pass, configChanged), domain.OutcomeStale, []string{"the build changed during the run"}},
		{"builds list different services", with(pass, buildsEndAdds("billing")), domain.OutcomeStale, []string{"the build changed during the run"}},
		{"builds list a different service", with(pass, func(in *verdict.Input) {
			in.BuildsEnd = []domain.BuildRef{{Service: "billing", Commit: "bbbbbbb", ConfigHash: "c1"}}
		}), domain.OutcomeStale, []string{"the build changed during the run"}},
		{"confirmed check is false", with(pass, checkState("refund-too-much", 0, domain.CheckFalse)), domain.OutcomeFailed, []string{"refund-too-much"}},
		{"invariant is false", with(pass, invariant("refund-too-much", domain.CheckFalse)), domain.OutcomeFailed, []string{"no server error"}},
		{"two false checks in two steps", with(pass, checkState("refund-too-much", 1, domain.CheckFalse), checkState("order-unchanged", 0, domain.CheckFalse)), domain.OutcomeFailed, []string{"refund-too-much", "order-unchanged"}},
		{"unconfirmed and true", with(pass, unconfirmed("refund-too-much", 1)), domain.OutcomeInconclusive, []string{"an expected result is not confirmed"}},
		{"unconfirmed and false", with(pass, unconfirmed("refund-too-much", 1), checkState("refund-too-much", 1, domain.CheckFalse)), domain.OutcomeInconclusive, []string{"an expected result is not confirmed and not met"}},
		{"nil Confirmed means not confirmed", with(pass, nilConfirmed), domain.OutcomeInconclusive, []string{"an expected result is not confirmed"}},
		{"check not evaluated", with(pass, checkState("order-unchanged", 0, domain.CheckNotEvaluated)), domain.OutcomeInconclusive, []string{"a check could not be evaluated: no response was recorded"}},
		{"invariant not evaluated", with(pass, invariant("order-unchanged", domain.CheckNotEvaluated)), domain.OutcomeInconclusive, []string{"a check could not be evaluated"}},
		{"step did not run", with(pass, noAction("order-unchanged")), domain.OutcomeInconclusive, []string{"step order-unchanged has no recorded result"}},
		{"write timed out", with(pass, effectUnknown("refund-too-much")), domain.OutcomeInconclusive, []string{"a write timed out and its effect is unknown"}},
		{"needed a retry", with(pass, retried), domain.OutcomeInconclusive, []string{"the script needed a retry"}},
		{"no check at all", noChecks, domain.OutcomeInconclusive, []string{"the scenario has no check besides the invariants"}},
		{"empty scenario", emptyScenario, domain.OutcomeInconclusive, []string{"the scenario has no check besides the invariants"}},
		{"checks shorter than expect", with(pass, shortChecks), domain.OutcomeInconclusive, []string{"a check could not be evaluated"}},
		{"checks longer than expect", with(pass, longChecks), domain.OutcomeInconclusive, []string{"the recorded evidence does not match the scenario: step order-unchanged"}},
		{"unknown step in the map", with(pass, unknownStepInMap), domain.OutcomeInconclusive, []string{"the recorded evidence does not match the scenario: the evidence holds step ghost"}},
		{"duplicate step id", with(pass, duplicateStep), domain.OutcomeInconclusive, []string{"the recorded evidence does not match the scenario: step order-unchanged"}},
		{"write step never ran", writeNeverRan, domain.OutcomeInconclusive, []string{"step refund has no recorded result"}},
		{"write step has a transport error", with(writeNeverRan, refundAction(&domain.Action{ID: "a2", Err: "connection reset"})), domain.OutcomeInconclusive, []string{"step refund failed: connection reset"}},
		{"write step has no response and no error", with(writeNeverRan, refundAction(&domain.Action{ID: "a2"})), domain.OutcomeInconclusive, []string{"step refund failed: no response was recorded"}},
		{"transport error with a check recorded as true", failedOnly(&domain.Action{ID: "a1", Err: "timeout"}), domain.OutcomeInconclusive, []string{"step only failed: timeout"}},
		{"no response with a check recorded as true", failedOnly(&domain.Action{ID: "a1"}), domain.OutcomeInconclusive, []string{"step only failed: no response was recorded"}},
		{"expectations only on a setup step", setupOnlyExpectations, domain.OutcomeInconclusive, []string{"the scenario has no check besides the invariants"}},
		{"invariant false on a setup step", with(pass, invariant("create-order", domain.CheckFalse)), domain.OutcomeBlocked, []string{"setup failed: create-order no server error was not met"}},
		{"no build recorded at all", with(pass, noBuilds(true, true)), domain.OutcomeInconclusive, []string{"no build was recorded"}},
		{"only the start build recorded", with(pass, noBuilds(false, true)), domain.OutcomeInconclusive, []string{"no build was recorded"}},
		{"only the end build recorded", with(pass, noBuilds(true, false)), domain.OutcomeInconclusive, []string{"no build was recorded"}},
		{"stale with a failed check lists both", with(pass, buildsEnd("orders", "aaaaaaa"), checkState("refund-too-much", 0, domain.CheckFalse)), domain.OutcomeStale, []string{"the build changed during the run", "refund-too-much"}},
		{"blocked wins over failed", with(pass, blocked("gate failed"), checkState("refund-too-much", 0, domain.CheckFalse)), domain.OutcomeBlocked, []string{"gate failed", "refund-too-much"}},
		{"blocked wins over stale", with(pass, blocked("gate failed"), buildsEnd("orders", "aaaaaaa")), domain.OutcomeBlocked, []string{"gate failed", "the build changed during the run"}},
		{"failed wins over inconclusive", with(pass, checkState("refund-too-much", 0, domain.CheckFalse), unconfirmed("order-unchanged", 0)), domain.OutcomeFailed, []string{"refund-too-much", "an expected result is not confirmed"}},
		{"failed setup keeps the earlier cause first", with(pass, setupStatus(500), noAction("refund-too-much")), domain.OutcomeBlocked, []string{"setup failed: create-order answered 500", "step refund-too-much has no recorded result"}},
		{"refused step is reported once", with(pass, stepRefused("refund-too-much", "denied")), domain.OutcomeBlocked, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := verdict.Compute(tc.in())
			if got.Outcome != tc.want {
				t.Fatalf("outcome = %s, want %s; reasons: %+v", got.Outcome, tc.want, got.Reasons)
			}
			if got.Outcome != domain.OutcomePassed && len(got.Reasons) == 0 {
				t.Fatal("a result that did not pass has no reason")
			}
			if len(got.Reasons) > 0 && got.Reasons[0].Rule != tc.want {
				t.Errorf("first reason rule = %s, want %s", got.Reasons[0].Rule, tc.want)
			}
			pos := 0
			for _, want := range tc.wantReasons {
				found := false
				for ; pos < len(got.Reasons); pos++ {
					if strings.Contains(got.Reasons[pos].Text, want) {
						found = true
						pos++
						break
					}
				}
				if !found {
					t.Errorf("reason %q missing or out of order in %+v", want, got.Reasons)
				}
			}
		})
	}
}

func TestComputeRefusedStepAddsNoInconclusiveReason(t *testing.T) {
	got := verdict.Compute(with(pass, stepRefused("refund-too-much", "denied"))())
	for _, r := range got.Reasons {
		if r.Rule == domain.OutcomeInconclusive && r.Step == "refund-too-much" {
			t.Errorf("unexpected inconclusive reason for the refused step: %+v", r)
		}
	}
	n := 0
	for _, r := range got.Reasons {
		if strings.Contains(r.Text, "denied") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the refusal is reported %d times, want once", n)
	}
}

func TestComputeRefusedStepAddsNoReasonBesidesTheRefusal(t *testing.T) {
	cases := []struct {
		name string
		mods []mod
	}{
		{"expectation results", []mod{stepRefused("refund-too-much", "denied")}},
		{"false invariant", []mod{stepRefused("refund-too-much", "denied"), invariant("refund-too-much", domain.CheckFalse)}},
		{"not evaluated invariant", []mod{stepRefused("refund-too-much", "denied"), invariant("refund-too-much", domain.CheckNotEvaluated)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := verdict.Compute(with(pass, tc.mods...)())
			if got.Outcome != domain.OutcomeBlocked {
				t.Fatalf("outcome = %s", got.Outcome)
			}
			if len(got.Reasons) != 1 || !strings.Contains(got.Reasons[0].Text, "denied") {
				t.Errorf("reasons = %+v, want only the refusal", got.Reasons)
			}
		})
	}
}

func TestComputeFailedStepNamesTheCauseOnce(t *testing.T) {
	cases := []struct {
		name   string
		action *domain.Action
	}{
		{"error", &domain.Action{ID: "a1", Err: "timeout"}},
		{"no response", &domain.Action{ID: "a1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := failedOnly(tc.action)()
			ev := in.Steps["only"]
			ev.Checks = []check.Result{{State: domain.CheckNotEvaluated, Detail: "no response was recorded"}}
			ev.Invariants = []check.Result{{State: domain.CheckNotEvaluated, Detail: "no response was recorded"}}
			in.Steps["only"] = ev
			got := verdict.Compute(in)
			if len(got.Reasons) != 1 || !strings.Contains(got.Reasons[0].Text, "step only failed") {
				t.Errorf("reasons = %+v, want only the failed step", got.Reasons)
			}
		})
	}
}

func TestComputeDuplicateStepListsEachActionOnce(t *testing.T) {
	got := verdict.Compute(with(pass, duplicateStep)())
	if want := []domain.ActionID{"a1", "a2", "a3"}; !reflect.DeepEqual(got.Actions, want) {
		t.Errorf("actions = %v, want %v", got.Actions, want)
	}
}

func TestComputeFailedReasonNamesStepExpectedObservedAndSource(t *testing.T) {
	got := verdict.Compute(with(pass, checkState("refund-too-much", 0, domain.CheckFalse))())
	text := got.Reasons[0].Text
	for _, want := range []string{"refund-too-much", "status is 200", "status 402", "ticket"} {
		if !strings.Contains(text, want) {
			t.Errorf("reason %q lacks %q", text, want)
		}
	}
	if got.Reasons[0].Step != "refund-too-much" {
		t.Errorf("reason step = %q", got.Reasons[0].Step)
	}
}

func TestComputeTwoFalseChecksAreListedInStepOrder(t *testing.T) {
	got := verdict.Compute(with(pass, checkState("order-unchanged", 0, domain.CheckFalse), checkState("refund-too-much", 0, domain.CheckFalse))())
	if len(got.Reasons) != 2 || got.Reasons[0].Step != "refund-too-much" || got.Reasons[1].Step != "order-unchanged" {
		t.Errorf("reasons = %+v", got.Reasons)
	}
}

func TestComputeChecksAndActions(t *testing.T) {
	in := with(pass, unconfirmed("refund-too-much", 1), checkState("order-unchanged", 0, domain.CheckFalse), invariant("create-order", domain.CheckFalse))()
	got := verdict.Compute(in)

	wantActions := []domain.ActionID{"a1", "a2", "a3"}
	if !reflect.DeepEqual(got.Actions, wantActions) {
		t.Errorf("actions = %v, want %v", got.Actions, wantActions)
	}
	if got.Scenario != "refunds" {
		t.Errorf("scenario = %q", got.Scenario)
	}
	type key struct {
		step  domain.StepID
		index int
	}
	var order []key
	for _, c := range got.Checks {
		order = append(order, key{c.Step, c.Index})
	}
	wantOrder := []key{{"create-order", -1}, {"refund-too-much", 0}, {"refund-too-much", 1}, {"refund-too-much", -1}, {"order-unchanged", 0}, {"order-unchanged", -1}}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("check order = %v, want %v", order, wantOrder)
	}
	if c := got.Checks[0]; c.Source != domain.SourceInvariant || c.State != domain.CheckFalse {
		t.Errorf("invariant result = %+v", c)
	}
	if c := got.Checks[1]; c.Source != "ticket" || !c.Confirmed || c.Expected != "status is 200" {
		t.Errorf("first expectation = %+v", c)
	}
	if c := got.Checks[2]; c.Confirmed {
		t.Errorf("unconfirmed expectation reported as confirmed: %+v", c)
	}
	if c := got.Checks[4]; c.State != domain.CheckFalse || c.Observed != "status 402" {
		t.Errorf("false check = %+v", c)
	}
}

func TestComputeKeepsEveryCheckWhenStepHasNoEvidence(t *testing.T) {
	got := verdict.Compute(with(pass, noAction("refund-too-much"))())
	var n int
	for _, c := range got.Checks {
		if c.Step == "refund-too-much" && c.Index >= 0 {
			n++
			if c.State != domain.CheckNotEvaluated {
				t.Errorf("state = %s, want not_evaluated", c.State)
			}
		}
	}
	if n != 2 {
		t.Errorf("got %d checks for the step without evidence, want 2", n)
	}
}

func TestComputeIsPure(t *testing.T) {
	in := with(pass, checkState("refund-too-much", 0, domain.CheckFalse), buildsEnd("orders", "aaaaaaa"), unconfirmed("order-unchanged", 0), retried)()
	snapshot := with(pass, checkState("refund-too-much", 0, domain.CheckFalse), buildsEnd("orders", "aaaaaaa"), unconfirmed("order-unchanged", 0), retried)()

	first := verdict.Compute(in)
	second := verdict.Compute(in)
	if !reflect.DeepEqual(first, second) {
		t.Errorf("two calls differ:\n%+v\n%+v", first, second)
	}
	if !reflect.DeepEqual(in.Scenario, snapshot.Scenario) || !reflect.DeepEqual(in.Steps, snapshot.Steps) ||
		!reflect.DeepEqual(in.BuildsStart, snapshot.BuildsStart) || !reflect.DeepEqual(in.BuildsEnd, snapshot.BuildsEnd) ||
		in.NotRun != snapshot.NotRun || !reflect.DeepEqual(in.Blocked, snapshot.Blocked) || in.Retried != snapshot.Retried {
		t.Error("Compute changed its input")
	}
}

func TestComputeDoesNotReorderBuilds(t *testing.T) {
	in := pass()
	in.BuildsStart = []domain.BuildRef{{Service: "b", Commit: "1"}, {Service: "a", Commit: "2"}}
	in.BuildsEnd = []domain.BuildRef{{Service: "a", Commit: "2"}, {Service: "b", Commit: "1"}}
	got := verdict.Compute(in)
	if got.Outcome != domain.OutcomePassed {
		t.Errorf("same builds in a different order: outcome = %s", got.Outcome)
	}
	if in.BuildsStart[0].Service != "b" || in.BuildsEnd[0].Service != "a" {
		t.Error("Compute reordered the builds of its input")
	}
}

func TestComputeIsDeterministic(t *testing.T) {
	build := func() verdict.Input {
		sc := domain.Scenario{ID: "many"}
		steps := map[domain.StepID]verdict.StepEvidence{}
		for _, id := range []domain.StepID{"s1", "s2", "s3", "s4", "s5", "s6", "s7"} {
			sc.Steps = append(sc.Steps, domain.Step{ID: id, Expect: []domain.Expectation{expectation(string(id))}})
			steps[id] = verdict.StepEvidence{
				Action:     ok("act-" + string(id)),
				Checks:     []check.Result{{State: domain.CheckFalse, Expected: "e", Observed: "o"}},
				Invariants: []check.Result{{State: domain.CheckFalse, Expected: "no server error"}},
			}
		}
		return verdict.Input{Scenario: sc, Steps: steps, Confirmed: func(domain.Expectation) bool { return true }}
	}
	first := verdict.Compute(build())
	for i := 0; i < 50; i++ {
		if got := verdict.Compute(build()); !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d differs from the first:\n%+v\n%+v", i, got, first)
		}
	}
}

func FuzzCompute(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1})
	f.Add([]byte{2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2})
	f.Add([]byte{255, 3, 1, 4, 1, 5, 9, 2, 6, 5, 3, 5, 8, 9, 7, 9})

	states := []domain.CheckState{domain.CheckTrue, domain.CheckFalse, domain.CheckNotEvaluated}
	f.Fuzz(func(t *testing.T, data []byte) {
		at := 0
		next := func() byte {
			if at >= len(data) {
				return 0
			}
			b := data[at]
			at++
			return b
		}
		const (
			modeOK = iota
			modeMissing
			modeErr
			modeStatus400
			modeRefused
			modes
		)
		sc := domain.Scenario{ID: "fz"}
		steps := map[domain.StepID]verdict.StepEvidence{}
		confirmed := map[string]bool{}

		// The oracle reads only these variables, which the generator sets from its own choices.
		noPassReason := ""
		setupFailed, anyRefused, anyExpectation := false, false, false
		for i := 0; i < 3; i++ {
			id := domain.StepID([]string{"s1", "s2", "s3"}[i])
			setup := next()&1 == 1
			mode := int(next()) % modes
			unknownEffect := next()&3 == 0
			n := int(next() % 3)
			truncate := next()&3 == 0
			step := domain.Step{ID: id, Setup: setup}
			ev := verdict.StepEvidence{}
			switch mode {
			case modeOK:
				ev.Action = ok("a" + string(id))
			case modeStatus400:
				ev.Action = &domain.Action{ID: domain.ActionID("a" + string(id)), Response: &domain.HTTPResponse{Status: 400 + int(next())%200}}
			case modeErr:
				ev.Action = &domain.Action{ID: domain.ActionID("a" + string(id)), Err: "reset"}
			case modeRefused:
				ev.Action = &domain.Action{ID: domain.ActionID("a" + string(id)), Refused: "denied"}
			}
			if ev.Action != nil && unknownEffect {
				ev.Action.EffectUnknown = true
				noPassReason = "an action has an unknown effect"
			}
			if mode == modeRefused {
				anyRefused = true
			}
			if setup && mode != modeOK {
				setupFailed = true
			}
			if !setup && (mode == modeMissing || mode == modeErr || mode == modeRefused) {
				noPassReason = "a non-setup step has no response"
			}
			for j := 0; j < n; j++ {
				cite := string(id) + string(rune('0'+j))
				step.Expect = append(step.Expect, expectation(cite))
				confirmed[cite] = next()&1 == 1
				if !setup {
					anyExpectation = true
				}
				if !confirmed[cite] {
					noPassReason = "an expectation is unconfirmed"
				}
				st := states[int(next())%3]
				if st != domain.CheckTrue {
					noPassReason = "a check is false or not evaluated"
				}
				ev.Checks = append(ev.Checks, check.Result{State: st})
			}
			if truncate {
				ev.Checks = ev.Checks[:len(ev.Checks)/2]
				if len(ev.Checks) < len(step.Expect) {
					noPassReason = "a check is missing"
				}
			}
			st := states[int(next())%3]
			if st != domain.CheckTrue {
				noPassReason = "an invariant is false or not evaluated"
			}
			ev.Invariants = []check.Result{{State: st}}
			sc.Steps = append(sc.Steps, step)
			steps[id] = ev
		}
		if !anyExpectation {
			noPassReason = "no non-setup step has an expectation"
		}
		if next()%4 == 0 {
			steps["ghost"] = verdict.StepEvidence{Action: ok("zz")}
			noPassReason = "the evidence has an unknown step"
		}

		in := verdict.Input{
			Scenario:    sc,
			Steps:       steps,
			Confirmed:   func(e domain.Expectation) bool { return confirmed[e.Cite] },
			BuildsStart: []domain.BuildRef{{Service: "orders", Commit: "a"}},
			BuildsEnd:   []domain.BuildRef{{Service: "orders", Commit: "a"}},
		}
		if next()%4 == 0 {
			in.Confirmed = nil
			for _, step := range sc.Steps {
				if len(step.Expect) > 0 {
					noPassReason = "an expectation is unconfirmed"
				}
			}
		}
		switch next() % 5 {
		case 1:
			in.BuildsEnd[0].Commit = "b"
			noPassReason = "the builds differ"
		case 2:
			in.BuildsStart = nil
			noPassReason = "a build side is empty"
		case 3:
			in.BuildsEnd = nil
			noPassReason = "a build side is empty"
		}
		if next()&1 == 1 {
			in.Blocked = []string{"blocked"}
			noPassReason = "something is blocked"
		}
		if next()%4 == 0 {
			in.NotRun = "not run"
			noPassReason = "not run"
		}
		in.Retried = next()&1 == 1
		if in.Retried {
			noPassReason = "the script needed a retry"
		}

		got := verdict.Compute(in)
		switch got.Outcome {
		case domain.OutcomePassed, domain.OutcomeFailed, domain.OutcomeBlocked,
			domain.OutcomeInconclusive, domain.OutcomeNotRun, domain.OutcomeStale:
		default:
			t.Fatalf("unknown outcome %q", got.Outcome)
		}
		if got.Outcome == domain.OutcomePassed && noPassReason != "" {
			t.Errorf("passed although: %s", noPassReason)
		}
		if got.Outcome == domain.OutcomePassed && len(got.Reasons) != 0 {
			t.Errorf("passed with reasons: %+v", got.Reasons)
		}
		if (setupFailed || anyRefused) && in.NotRun == "" && got.Outcome != domain.OutcomeBlocked {
			t.Errorf("outcome = %s, want blocked: a setup step failed or a step was refused", got.Outcome)
		}
	})
}
