package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
)

func orderScenario() domain.Scenario {
	created, open := 201, "open"
	return domain.Scenario{
		ID: "SHOP-7-02", Title: "Create an order", Ticket: "SHOP-7", Surface: domain.SurfaceAPI, AuthProfile: "default",
		Steps: []domain.Step{{
			ID: "create", Action: "Create an order for the customer.", Hint: &domain.RequestHint{Method: "POST", Path: "/v1/orders"},
			Expect: []domain.Expectation{
				{Check: domain.Check{Status: &created}, Source: domain.SourceTicket, By: "model"},
				{Check: domain.Check{JSONPath: "$.data.status", Equals: open}, Source: domain.SourceTicket},
			},
		}},
	}
}

func orderScript(sc domain.Scenario) domain.Script {
	return domain.Script{
		APIVersion: "semar/v1", Kind: "Script", Scenario: sc.ID, ExpectHash: domain.ExpectHash(sc),
		Steps: []domain.ScriptStep{{Step: "create", Request: &domain.APIRequest{
			Service: "shop", Method: "POST", Path: "/v1/orders",
			JSON: map[string]any{"customerId": "{{customer.id}}", "dueAt": "{{datetime.plus7d}}"},
		}}},
	}
}

func reviewStore(t *testing.T) *evidence.Store {
	t.Helper()
	s, err := evidence.Open(filepath.Join(t.TempDir(), ".semar"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// storeRun stores a run of sc whose one step was answered with status and body.
func storeRun(t *testing.T, s *evidence.Store, id domain.RunID, sc domain.Scenario, checks []domain.CheckResult, status int, body string) {
	t.Helper()
	r, err := s.NewRun(id)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 18, 7, 15, 2, 0, time.UTC)
	a := domain.Action{
		Schema: 1, ID: "a-1", Seq: 1, Run: id, Surface: domain.SurfaceAPI, Op: "request", At: at,
		Use:      domain.UseCase{Pass: domain.PassScenario, Scenario: sc.ID, Step: "create"},
		Request:  &domain.HTTPRequest{Method: "POST", URL: "http://shop.test/v1/orders", Body: domain.Body(`{"customerId":"c-1"}`)},
		Response: &domain.HTTPResponse{Status: status, Body: domain.Body(body)},
	}
	rel, err := r.WriteAction(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.AppendActivity(domain.Activity{Seq: 1, At: at, Use: a.Use, Evidence: rel, Verb: domain.VerbCreate, Target: "POST /v1/orders", Result: "ok"}); err != nil {
		t.Fatal(err)
	}
	res := domain.ScenarioResult{Scenario: sc.ID, ExpectHash: domain.ExpectHash(sc), Outcome: domain.OutcomeInconclusive, Checks: checks}
	if err := r.Close(domain.Run{ID: id, Environment: "uat", StartedAt: at, EndedAt: at, Results: []domain.ScenarioResult{res}}); err != nil {
		t.Fatal(err)
	}
}

func showWith(t *testing.T, s *evidence.Store, sc domain.Scenario) string {
	t.Helper()
	var b strings.Builder
	us := []unconfirmed{needsConfirmation(t.TempDir(), sc, []domain.Source{domain.SourceTicket})}
	showScenarioItem(&b, t.TempDir(), 1, 1, nil, us, func(domain.FixtureID) string { return "approved" }, func(sc domain.Scenario) scenarioView { return loadScenarioView(s, sc) })
	return b.String()
}

const orderBeforeRun = `  Test SHOP-7-02 · API · as default
    When  Create an order for the customer.
          POST /v1/orders
          json {"customerId":"{{customer.id}}","dueAt":"{{datetime.plus7d}}"}
    Then  the status is 201 (by model)
    And   $.data.status equals "open"
    not run yet: semar run --scenario SHOP-7-02 sends it and stores what the service answers
`

func TestReviewShowsWhatTheStepSendsBeforeAnyRun(t *testing.T) {
	sc := orderScenario()
	s := reviewStore(t)
	if err := s.PutScript(orderScript(sc)); err != nil {
		t.Fatal(err)
	}
	if got := showWith(t, s, sc); !strings.Contains(got, orderBeforeRun) {
		t.Errorf("review lacks:\n%s\nin:\n%s", orderBeforeRun, got)
	}
}

const orderAfterRun = `  Test SHOP-7-02 · API · as default
    When  Create an order for the customer.
          POST /v1/orders
          json {"customerId":"{{customer.id}}","dueAt":"{{datetime.plus7d}}"}
    Then  the status is 201 (by model)
          last run r-1: observed 201, met
    And   $.data.status equals "open"
          last run r-1: observed "draft", not met
`

func TestReviewShowsWhatTheLastRunObservedNextToEachExpectedResult(t *testing.T) {
	sc := orderScenario()
	s := reviewStore(t)
	if err := s.PutScript(orderScript(sc)); err != nil {
		t.Fatal(err)
	}
	storeRun(t, s, "r-1", sc, []domain.CheckResult{
		{Step: "create", Index: 0, State: domain.CheckTrue, Expected: "status 201", Observed: "201"},
		{Step: "create", Index: 1, State: domain.CheckFalse, Expected: `$.data.status equals "open"`, Observed: `"draft"`},
	}, 201, `{"data":{"status":"draft"}}`)
	got := showWith(t, s, sc)
	if !strings.Contains(got, orderAfterRun) {
		t.Errorf("review lacks:\n%s\nin:\n%s", orderAfterRun, got)
	}
	if strings.Contains(got, "not run yet") {
		t.Errorf("review says the scenario was not run:\n%s", got)
	}
}

func TestReviewSaysWhyACheckWasNotEvaluated(t *testing.T) {
	sc := orderScenario()
	s := reviewStore(t)
	storeRun(t, s, "r-1", sc, []domain.CheckResult{
		{Step: "create", Index: 0, State: domain.CheckNotEvaluated, Detail: "the service did not answer"},
	}, 0, "")
	got := showWith(t, s, sc)
	for _, want := range []string{
		"          last run r-1: not evaluated: the service did not answer\n",
		"          last run r-1: not evaluated: the run stored no result for this check\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("review lacks %q:\n%s", want, got)
		}
	}
}

func TestReviewSaysWhenNoScriptIsStoredAndIgnoresARunOfOtherExpectedResults(t *testing.T) {
	sc := orderScenario()
	s := reviewStore(t)
	other := sc
	other.Intent = "another intent, so another expectation hash"
	storeRun(t, s, "r-1", other, []domain.CheckResult{{Step: "create", Index: 0, State: domain.CheckTrue, Observed: "201"}}, 201, "{}")
	got := showWith(t, s, sc)
	for _, want := range []string{
		"    no script is stored for this scenario\n",
		"    not run yet: semar run --scenario SHOP-7-02 sends it and stores what the service answers\n",
	} {
		if strings.Count(got, want) != 1 {
			t.Errorf("review has %q %d times, want once:\n%s", want, strings.Count(got, want), got)
		}
	}
}

func TestReviewCutsAndEscapesWhatAScriptSends(t *testing.T) {
	sc := orderScenario()
	s := reviewStore(t)
	scr := orderScript(sc)
	scr.Steps[0].Request.JSON = map[string]any{"note": strings.Repeat("x", 700)}
	scr.Steps[0].Request.Headers = map[string]string{"X-Note": "a\nWhen  forged line\x1b[31m"}
	if err := s.PutScript(scr); err != nil {
		t.Fatal(err)
	}
	got := showWith(t, s, sc)
	if strings.Contains(got, "\nWhen  forged") || strings.Contains(got, "\x1b") {
		t.Errorf("a script forged a line or sent a control character:\n%q", got)
	}
	if !strings.Contains(got, `headers X-Note=a\nWhen  forged line\x1b[31m`) {
		t.Errorf("the header is not shown escaped:\n%s", got)
	}
	if !strings.Contains(got, "… [cut: ") {
		t.Errorf("a 700 character body is not cut:\n%s", got)
	}
	for _, l := range strings.Split(got, "\n") {
		if n := len([]rune(l)); n > 700 {
			t.Errorf("a line has %d characters", n)
		}
	}
}

func TestDetailsShowTheStoredExchangeOfTheLastRun(t *testing.T) {
	sc := orderScenario()
	s := reviewStore(t)
	storeRun(t, s, "r-1", sc, []domain.CheckResult{{Step: "create", Index: 0, State: domain.CheckTrue, Observed: "201"}},
		200, `{"data":{"status":"draft","pad":"`+strings.Repeat("y", 2100)+`"}}`)
	items := scenarioItems(t.TempDir(), nil, []unconfirmed{needsConfirmation(t.TempDir(), sc, []domain.Source{domain.SourceTicket})},
		func(domain.FixtureID) string { return "approved" }, func(sc domain.Scenario) scenarioView { return loadScenarioView(s, sc) })
	var b strings.Builder
	items[0].details(&b)
	got := b.String()
	for _, want := range []string{
		"Last run r-1, step create:",
		"  request: POST http://shop.test/v1/orders",
		`  request body: {"customerId":"c-1"}`,
		"  status: 200",
		`  response body: {"data":{"status":"draft","pad":"yyy`,
		"… [cut: ",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("details lack %q:\n%s", want, got)
		}
	}
}
