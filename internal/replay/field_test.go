package replay_test

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/replay"
)

// variant changes the scenario and binds the script to the changed scenario.
func variant(t *testing.T, change func(*domain.Scenario, *domain.Script)) (domain.Scenario, domain.Script) {
	t.Helper()
	sc, s := loadScenario(t), loadScript(t)
	change(&sc, &s)
	s.ExpectHash = domain.ExpectHash(sc)
	return sc, s
}

type seen struct {
	mu   sync.Mutex
	auth map[string][]string // "METHOD path" to the Authorization values
}

func (s *seen) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		if s.auth == nil {
			s.auth = map[string][]string{}
		}
		k := r.Method + " " + r.URL.Path
		s.auth[k] = append(s.auth[k], r.Header.Values("Authorization")...)
		s.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func TestStepAsSendsThatProfileAndNoOther(t *testing.T) {
	var sn seen
	r := newRig(t, rigOptions{wrap: sn.wrap, sessions: map[string]string{"bob": "tok-bob", "support-agent": "tok-alice"}})
	r.deps.Accounts = []string{"support-agent", "bob"}
	sc, s := variant(t, func(sc *domain.Scenario, _ *domain.Script) { sc.Steps[2].As = "bob" })
	r.putScript(t, s)
	r.confirmAll(t, sc)
	r.run(t, sc)

	sn.mu.Lock()
	defer sn.mu.Unlock()
	if got := sn.auth["POST /v1/orders"]; len(got) != 1 || got[0] != "Bearer tok-alice" {
		t.Errorf("the default step sent %v, want the scenario's profile", got)
	}
	var read []string
	for k, v := range sn.auth {
		if strings.HasPrefix(k, "GET /v1/orders/") {
			read = append(read, v...)
		}
	}
	if len(read) != 1 || read[0] != "Bearer tok-bob" {
		t.Errorf("the step with as: bob sent %v, want one header with bob's session", read)
	}
	a := r.storedAction(t, sc.ID, "order-unchanged")
	if got := a.Request.Headers["Authorization"]; len(got) != 1 || strings.Contains(got[0], "tok-bob") {
		t.Errorf("stored session header = %v, want one redacted value", got)
	}
}

func TestStepAsAlsoWorksOnASetupStep(t *testing.T) {
	var sn seen
	r := newRig(t, rigOptions{wrap: sn.wrap, sessions: map[string]string{"bob": "tok-bob", "support-agent": "tok-alice"}})
	r.deps.Accounts = []string{"support-agent", "bob"}
	sc, s := variant(t, func(sc *domain.Scenario, _ *domain.Script) { sc.Steps[0].As = "bob" })
	r.putScript(t, s)
	r.confirmAll(t, sc)
	r.run(t, sc)
	sn.mu.Lock()
	defer sn.mu.Unlock()
	if got := sn.auth["POST /v1/orders"]; len(got) != 1 || got[0] != "Bearer tok-bob" {
		t.Errorf("the setup step sent %v, want bob's session", got)
	}
}

func TestUnknownAccountBlocksBeforeAnyRequest(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.deps.Accounts = []string{"support-agent"}
	sc, s := variant(t, func(sc *domain.Scenario, _ *domain.Script) { sc.Steps[1].As = "dave" })
	r.putScript(t, s)
	r.confirmAll(t, sc)
	results, _, _ := r.run(t, sc)
	expect(t, results[0], domain.OutcomeBlocked, "account dave is not in the configuration")
	if n := r.app.Requests("", "/v1/"); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

func TestAnAccountOfTheScenarioIsKnownWithoutTheList(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc, s := variant(t, func(sc *domain.Scenario, _ *domain.Script) { sc.Steps[1].As = "support-agent" })
	r.putScript(t, s)
	r.confirmAll(t, sc)
	results, _, _ := r.run(t, sc)
	expect(t, results[0], domain.OutcomePassed, "")
}

func TestExpectedByModelNeedsConfirmationWhateverTheSource(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc, s := variant(t, func(sc *domain.Scenario, _ *domain.Script) { sc.Steps[1].Expect[0].By = "model" })
	r.putScript(t, s)
	r.confirmSources(t, sc, domain.SourceDiff, domain.SourceBaseline)
	results, _, _ := r.run(t, sc)
	expect(t, results[0], domain.OutcomeInconclusive, "an expected result is not confirmed")

	r = newRig(t, rigOptions{})
	r.putScript(t, s)
	r.confirmSources(t, sc, domain.SourceDiff, domain.SourceBaseline, domain.SourceTicket)
	results, _, _ = r.run(t, sc)
	expect(t, results[0], domain.OutcomePassed, "")
}

func operandScenario(equals string) func(*domain.Scenario, *domain.Script) {
	return func(sc *domain.Scenario, _ *domain.Script) {
		sc.Steps[2].Expect = append(sc.Steps[2].Expect, domain.Expectation{
			Check:  domain.Check{JSONPath: "$.data.id", Equals: equals},
			Source: domain.SourceTicket, Cite: "SHOP-482: the order keeps its ID",
		})
	}
}

func TestOperandVariableComparesWithTheCapture(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc, s := variant(t, operandScenario("{{order_id}}"))
	r.putScript(t, s)
	r.confirmAll(t, sc)
	results, _, _ := r.run(t, sc)
	expect(t, results[0], domain.OutcomePassed, "")
}

func TestOperandVariableShowsTheFilledValueInAFailure(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc, s := variant(t, operandScenario("not-{{order_id}}"))
	r.putScript(t, s)
	r.confirmAll(t, sc)
	results, _, _ := r.run(t, sc)
	id := r.storedAction(t, sc.ID, "create-order").Captures["order_id"]
	if id == "" {
		t.Fatal("no capture was stored")
	}
	expect(t, results[0], domain.OutcomeFailed, "equals not-"+id)
	if !hasReason(results[0], "{{order_id}}") {
		t.Errorf("the reason does not show the template: %+v", results[0].Reasons)
	}
}

func TestOperandWithAMissingValueIsNotEvaluated(t *testing.T) {
	r := newRig(t, rigOptions{})
	sc, s := variant(t, func(sc *domain.Scenario, s *domain.Script) {
		operandScenario("{{later}}")(sc, s)
		s.Steps[2].Captures = []domain.Capture{{Name: "later", JSONPath: "$.data.nothing"}}
	})
	r.putScript(t, s)
	r.confirmAll(t, sc)
	results, _, _ := r.run(t, sc)
	var got domain.CheckState
	for _, c := range results[0].Checks {
		if c.Step == "order-unchanged" && c.Index == 1 {
			got = c.State
		}
	}
	if got != domain.CheckNotEvaluated {
		t.Errorf("state = %q, want not_evaluated; %+v", got, results[0].Checks)
	}
}

func TestOperandVariableNeverShowsARedactedCapture(t *testing.T) {
	r := newRig(t, rigOptions{redact: config.Redact{JSONPaths: []string{"$.data.id"}}})
	sc, s := variant(t, operandScenario("not-{{order_id}}"))
	r.putScript(t, s)
	r.confirmAll(t, sc)
	results, _, _ := r.run(t, sc)
	for _, c := range results[0].Checks {
		if strings.Contains(c.Expected, "o-1") {
			t.Errorf("the report shows the redacted value: %+v", c)
		}
	}
	for _, rs := range results[0].Reasons {
		if strings.Contains(rs.Text, "o-1") {
			t.Errorf("a reason shows the redacted value: %q", rs.Text)
		}
	}
}

var _ = replay.New
