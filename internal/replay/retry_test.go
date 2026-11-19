package replay_test

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/api"
	"github.com/jancuk/semar/internal/sampleapp"
)

// failsFirstCreate answers the first order that a scenario creates with 503 and every later request
// as the application does.
func failsFirstCreate(next http.Handler) http.Handler {
	var n atomic.Int32
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost && req.URL.Path == "/v1/orders" && n.Add(1) == 1 {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, req)
	})
}

func retried(t *testing.T, o rigOptions, retries int, setup func(*rig)) (*rig, domain.ScenarioResult) {
	t.Helper()
	r := newRig(t, o)
	sc := loadScenario(t)
	r.putScript(t, loadScript(t))
	r.deps.Retries = retries
	if setup != nil {
		setup(r)
	}
	results, _, _ := r.run(t, sc)
	return r, results[0]
}

func TestAScenarioThatPassesOnALaterAttemptIsFlakyAndPassed(t *testing.T) {
	r, res := retried(t, rigOptions{wrap: failsFirstCreate}, 2, nil)
	expect(t, res, domain.OutcomePassed, "")
	if !res.Flaky || len(res.Attempts) != 2 || res.AttemptsAllowed != 3 {
		t.Fatalf("flaky %v, %d attempts of %d allowed; want flaky, 2 of 3", res.Flaky, len(res.Attempts), res.AttemptsAllowed)
	}
	if res.Attempts[0].Outcome != domain.OutcomeBlocked || res.Attempts[1].Outcome != domain.OutcomePassed {
		t.Errorf("attempt outcomes %s, %s; want blocked by the setup step, passed", res.Attempts[0].Outcome, res.Attempts[1].Outcome)
	}
	if len(res.Attempts[0].Actions) == 0 || len(res.Attempts[1].Actions) == 0 {
		t.Fatalf("an attempt kept no evidence: %+v", res.Attempts)
	}
	if res.Attempts[0].Actions[0] == res.Attempts[1].Actions[0] {
		t.Errorf("both attempts name action %s as their first", res.Attempts[0].Actions[0])
	}
	if !equalActions(res.Actions, res.Attempts[1].Actions) {
		t.Errorf("the result names actions %v, not those of the deciding attempt %v", res.Actions, res.Attempts[1].Actions)
	}
	if n := r.app.Requests("POST", "/v1/orders"); n != 2 {
		t.Errorf("the service saw %d writes, want the create and the refund of the second attempt", n)
	}
}

func equalActions(a, b []domain.ActionID) bool {
	return strings.Join(ids(a), ",") == strings.Join(ids(b), ",")
}

func ids(a []domain.ActionID) []string {
	out := make([]string, len(a))
	for i, id := range a {
		out[i] = string(id)
	}
	return out
}

func TestAScenarioThatFailsEveryAttemptFailsWithTheirCount(t *testing.T) {
	_, res := retried(t, rigOptions{defects: []sampleapp.Defect{sampleapp.RefundOverTotal}}, 2, nil)
	expect(t, res, domain.OutcomeFailed, "expected status 422")
	if res.Flaky || len(res.Attempts) != 3 || res.AttemptsAllowed != 3 {
		t.Fatalf("flaky %v, %d attempts of %d; want not flaky, 3 of 3", res.Flaky, len(res.Attempts), res.AttemptsAllowed)
	}
	for i, a := range res.Attempts {
		if a.Outcome != domain.OutcomeFailed {
			t.Errorf("attempt %d is %s, want failed", i+1, a.Outcome)
		}
	}
}

func TestNoRetriesLeavesTheResultAsItWas(t *testing.T) {
	_, res := retried(t, rigOptions{defects: []sampleapp.Defect{sampleapp.RefundOverTotal}}, 0, nil)
	expect(t, res, domain.OutcomeFailed, "")
	if res.Attempts != nil || res.Flaky || res.AttemptsAllowed != 0 {
		t.Errorf("a scenario that ran once has attempts %v, flaky %v, allowed %d", res.Attempts, res.Flaky, res.AttemptsAllowed)
	}
}

func TestAPassedScenarioIsNeverRunAgain(t *testing.T) {
	r, res := retried(t, rigOptions{}, 3, nil)
	expect(t, res, domain.OutcomePassed, "")
	if res.Attempts != nil || res.Flaky {
		t.Errorf("a first pass has attempts %v, flaky %v", res.Attempts, res.Flaky)
	}
	if n := r.app.Requests("POST", "/v1/orders"); n != 2 {
		t.Errorf("the scenario sent %d writes, want 2", n)
	}
}

func TestARetryNeverSendsARequestOfTheOnceListAgain(t *testing.T) {
	onceRefund := func(r *rig) {
		r.deps.Once = func(c api.Call) bool { return c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/refunds") }
	}
	r, res := retried(t, rigOptions{defects: []sampleapp.Defect{sampleapp.RefundOverTotal}}, 2, onceRefund)
	expect(t, res, domain.OutcomeFailed, "so it was not run again")
	if len(res.Attempts) != 0 {
		t.Errorf("the scenario was run again: %d attempts", len(res.Attempts))
	}
	if n := r.app.Requests("POST", "/v1/orders/"); n != 1 {
		t.Errorf("the refund was sent %d times, want 1", n)
	}

	// Without the once list the same scenario is sent three times.
	r, res = retried(t, rigOptions{defects: []sampleapp.Defect{sampleapp.RefundOverTotal}}, 2, nil)
	if n := r.app.Requests("POST", "/v1/orders/"); n != 3 || len(res.Attempts) != 3 {
		t.Errorf("without a once list: %d refunds, %d attempts, want 3 and 3", n, len(res.Attempts))
	}
}

func TestAnInconclusiveAttemptIsRetried(t *testing.T) {
	r := newRig(t, rigOptions{
		defects: []sampleapp.Defect{sampleapp.SlowRefund, sampleapp.RefundOverTotal}, mode: "test_data",
		timeout: time.Second, delay: time.Minute,
	})
	sc := loadScenario(t)
	r.putScript(t, loadScript(t))
	r.deps.Retries = 1
	results, _, _ := r.run(t, sc)
	expect(t, results[0], domain.OutcomeInconclusive, "a write timed out and its effect is unknown")
	if len(results[0].Attempts) != 2 {
		t.Errorf("%d attempts, want 2: an inconclusive attempt is tried again", len(results[0].Attempts))
	}
}

func TestAFailedAttemptThenAPassIsFlaky(t *testing.T) {
	var n atomic.Int32
	slowFirstRefund := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/refunds") && n.Add(1) == 1 {
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
				return
			}
			next.ServeHTTP(w, req)
		})
	}
	_, res := retried(t, rigOptions{wrap: slowFirstRefund}, 1, nil)
	expect(t, res, domain.OutcomePassed, "")
	if !res.Flaky || len(res.Attempts) != 2 || res.Attempts[0].Outcome != domain.OutcomeFailed {
		t.Fatalf("flaky %v, attempts %+v; want a failed attempt then a pass", res.Flaky, res.Attempts)
	}
}
