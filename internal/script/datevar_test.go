package script_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/script"
)

const runAt = "r-20261118T071502Z-a1b2"

// inJakarta makes the machine zone UTC+7 for one test, so that no result depends on the zone of
// the machine that runs it.
func inJakarta(t *testing.T) {
	t.Helper()
	old := time.Local
	time.Local = time.FixedZone("UTC+7", 7*3600)
	t.Cleanup(func() { time.Local = old })
}

func TestRenderDateVariables(t *testing.T) {
	inJakarta(t)
	got, err := script.Render(domain.APIRequest{
		Path:    "/v1/orders",
		Query:   map[string]string{"from": "{{date.minus30d}}", "to": "{{date.today}}"},
		Headers: map[string]string{"X-Sent": "{{datetime.now}}"},
		JSON:    map[string]any{"dueAt": "{{datetime.plus7d}}", "slot": []any{"{{local.plus2h}}"}, "note": "due {{date.plus1d}} for {{run.id}}"},
	}, map[string]string{"run.id": runAt})
	if err != nil {
		t.Fatal(err)
	}
	body := got.JSON.(map[string]any)
	for what, c := range map[string][2]string{
		"from": {got.Query["from"], "2026-10-19"},
		"to":   {got.Query["to"], "2026-11-18"},
		"hdr":  {got.Headers["X-Sent"], "2026-11-18T07:15:02Z"},
		"due":  {body["dueAt"].(string), "2026-11-25T07:15:02Z"},
		"slot": {body["slot"].([]any)[0].(string), "2026-11-18T16:15"},
		"note": {body["note"].(string), "due 2026-11-19 for " + runAt},
	} {
		if c[0] != c[1] {
			t.Errorf("%s: got %q, want %q", what, c[0], c[1])
		}
	}
}

func TestRenderDateVariableInAPath(t *testing.T) {
	inJakarta(t)
	got, err := script.Render(domain.APIRequest{Path: "/v1/reports/{{date.today}}"}, map[string]string{"run.id": runAt})
	if err != nil || got.Path != "/v1/reports/2026-11-18" {
		t.Fatalf("got %q %v", got.Path, err)
	}
}

func TestRenderDateVariableWithoutATimeInTheRunID(t *testing.T) {
	_, err := script.Render(domain.APIRequest{Path: "/v1/orders", Query: map[string]string{"d": "{{date.today}}"}}, map[string]string{"run.id": "r1"})
	if err == nil || !strings.Contains(err.Error(), `unknown variable date.today: the run ID "r1" holds no time`) {
		t.Fatalf("got %v", err)
	}
	_, err = script.Render(domain.APIRequest{Path: "/v1/orders", Query: map[string]string{"d": "{{date.today}}"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "holds no time") {
		t.Fatalf("a run without an ID: got %v", err)
	}
}

func TestRenderNameThatIsNotADateVariableIsUnknown(t *testing.T) {
	for _, name := range []string{"date.tomorrow", "date.plus0d", "datetime.plus1w", "date.plus7h"} {
		_, err := script.Render(domain.APIRequest{Path: "/v1/orders", Query: map[string]string{"d": "{{" + name + "}}"}}, map[string]string{"run.id": runAt})
		if err == nil || !strings.Contains(err.Error(), "unknown variable "+name) || strings.Contains(err.Error(), "holds no time") {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestAVariableOfTheMapWinsOverADateVariable(t *testing.T) {
	inJakarta(t)
	got, err := script.Render(domain.APIRequest{Path: "/v1/orders", Query: map[string]string{"d": "{{date.today}}"}},
		map[string]string{"run.id": runAt, "date.today": "chosen"})
	if err != nil || got.Query["d"] != "chosen" {
		t.Fatalf("got %v %v", got.Query, err)
	}
}

func TestRenderWebAndLocatorDateVariables(t *testing.T) {
	inJakarta(t)
	vars := map[string]string{"run.id": runAt}
	w, err := script.RenderWeb(domain.WebAction{Do: domain.WebFill, Value: "{{date.plus7d}}", Path: "/day/{{date.today}}"}, vars)
	if err != nil || w.Value != "2026-11-25" || w.Path != "/day/2026-11-18" {
		t.Fatalf("got %+v %v", w, err)
	}
	l, err := script.RenderLocator(domain.Locator{Text: "Due {{date.plus7d}}"}, vars)
	if err != nil || l.Text != "Due 2026-11-25" {
		t.Fatalf("got %+v %v", l, err)
	}
	if _, err := script.RenderWeb(domain.WebAction{Do: domain.WebFill, Value: "{{date.today}}"}, map[string]string{"run.id": "r1"}); err == nil {
		t.Fatal("a run ID without a time must be an error")
	}
}

func TestLookupGivesDateVariablesToExpectedValues(t *testing.T) {
	inJakarta(t)
	lookup := script.Lookup(map[string]string{"run.id": runAt, "n": "7"})
	for name, want := range map[string]string{"n": "7", "date.plus7d": "2026-11-25", "datetime.now": "2026-11-18T07:15:02Z"} {
		if got, ok := lookup(name); !ok || got != want {
			t.Errorf("%s: got %q %v", name, got, ok)
		}
	}
	for _, name := range []string{"date.tomorrow", "nope"} {
		if _, ok := lookup(name); ok {
			t.Errorf("%s must have no value", name)
		}
	}
	if _, ok := script.Lookup(map[string]string{"run.id": "r1"})("date.today"); ok {
		t.Error("a run ID without a time gives no date")
	}
}

func TestValidateAcceptsDateVariables(t *testing.T) {
	sc, s := loadPair(t)
	s.Steps[0].Captures = nil
	s.Steps[0].Request.Query = map[string]string{"due": "{{date.plus3d}}"}
	s.Steps[0].Request.Headers = map[string]string{"X-At": "{{datetime.minus5m}}"}
	s.Steps[2].Request.Query = map[string]string{"at": "{{local.now}}"}
	if ps := script.Validate(sc, s); hasProblem(ps, "date.") || hasProblem(ps, "datetime.") || hasProblem(ps, "local.") {
		t.Fatalf("got %v", messages(ps))
	}
	s.Steps[0].Request.Query = map[string]string{"due": "{{date.tomorrow}}"}
	if ps := script.Validate(sc, s); !hasProblem(ps, "date.tomorrow") {
		t.Fatalf("an unknown date name must be refused: %v", messages(ps))
	}
}

func TestValidateAcceptsDateVariablesInExpectedValues(t *testing.T) {
	sc, s := withGiven(t)
	sc.Steps[2].Expect[0].Check = domain.Check{JSONPath: "$.data.dueOn", Equals: "{{date.plus7d}}"}
	s.ExpectHash = domain.ExpectHash(sc)
	if ps := script.ValidateWith(sc, s, map[string][]string{"order": {"id"}}); len(ps) != 0 {
		t.Fatalf("got %v", messages(ps))
	}
}

func TestValidateAcceptsDateVariablesInAWebStep(t *testing.T) {
	sc := webScenario()
	s := webScript(sc)
	s.Steps[0].Web = &domain.WebAction{Service: "board", App: "kanban", Do: domain.WebFill, Screen: "Board", Element: "card", Value: "{{date.plus7d}}"}
	if ps := script.Validate(sc, s); hasProblem(ps, "date.plus7d") {
		t.Fatalf("got %s", problemText(ps))
	}
}

func hasProblem(ps []script.Problem, sub string) bool {
	for _, m := range messages(ps) {
		if strings.Contains(m, sub) {
			return true
		}
	}
	return false
}
