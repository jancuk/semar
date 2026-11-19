package cli_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/pagelist"
	"github.com/jancuk/semar/internal/sampleapp"
	"github.com/jancuk/semar/internal/script"
	"github.com/jancuk/semar/internal/stage/plan"
)

const flowCite = "docs/tickets/SHOP-482.md:9 a refund shows its total"

func ref(t *testing.T, page pagelist.List, role, name string) string {
	t.Helper()
	for _, e := range page.Elements {
		if e.Role == role && e.Name == name {
			return e.Ref
		}
	}
	t.Fatalf("the page has no %s %q:\n%s", role, name, page.String())
	return ""
}

// scriptedFlow plays the turns of a model that is written out: each turn is made from the page that the
// model sees, so that it names the references of that page.
func scriptedFlow(t *testing.T, turns ...func(in plan.TurnInput) plan.Turn) plan.FlowDriver {
	return func(ctx context.Context, b plan.FlowBrowser) (plan.FlowEnd, error) {
		i := 0
		model := func(_ context.Context, in plan.TurnInput) (plan.Turn, error) {
			if i >= len(turns) {
				t.Fatalf("the scripted model has no turn %d", i+1)
			}
			i++
			return turns[i-1](in), nil
		}
		return plan.RunFlow(ctx, plan.DefaultFlowLimits, "criterion", model, b)
	}
}

func refundTurns(t *testing.T, expectTotal string) []func(plan.TurnInput) plan.Turn {
	return []func(plan.TurnInput) plan.Turn{
		func(in plan.TurnInput) plan.Turn {
			return plan.TurnAct{Actions: []plan.Action{
				{Kind: plan.ActionFill, Ref: ref(t, in.Page, "textbox", "Amount"), Text: "5"},
				{Kind: plan.ActionClick, Ref: ref(t, in.Page, "button", "Refund")},
			}}
		},
		func(in plan.TurnInput) plan.Turn {
			return plan.TurnAct{Actions: []plan.Action{
				{Kind: plan.ActionCheck, Ref: ref(t, in.Page, "text", "5.00"), Expect: expectTotal},
				{Kind: plan.ActionCheck, Ref: ref(t, in.Page, "text", "partly refunded"), Expect: "partly refunded"},
			}}
		},
		func(plan.TurnInput) plan.Turn {
			return plan.TurnDone{Title: "A refund shows its total", Intent: "Before: no total. Now: the total is shown."}
		},
	}
}

func explorerOf(t *testing.T, r *repo, as string) plan.WebExplorer {
	t.Helper()
	x, err := cli.NewWebExplorer(r.opts, r.dir, "orders", as)
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func TestAnExplorerWritesATestThatReplaysAndRunsLikeARecordedOne(t *testing.T) {
	r, _ := recordRepoWith(t, repoOptions{app: sampleapp.Options{}})
	r.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "baseUrl: "+r.ordersURL, "baseUrl: "+r.ordersURL+"\n        web: {start: /refund}", 1)
	})
	out, err := explorerOf(t, r, "").Explore(t.Context(), plan.WebRequest{ID: "SHOP-482-05", Ticket: "SHOP-482", Cite: flowCite}, scriptedFlow(t, refundTurns(t, "5.00")...))
	if err != nil {
		t.Fatal(err)
	}
	if out.Manual != "" || out.Test == nil {
		t.Fatalf("outcome = %+v", out)
	}
	if out.Test.Replay != "passed" || len(out.Test.FailsNow) != 0 || out.End.Turns != 3 || out.End.Actions != 4 {
		t.Errorf("test = %+v, end = %+v", out.Test, out.End)
	}
	if out.Test.ScenarioPath != ".semar/scenarios/SHOP-482/SHOP-482-05.feature" || out.Test.Scenario.Title != "A refund shows its total" {
		t.Errorf("test = %+v", out.Test)
	}
	feature, err := os.ReadFile(r.path(".semar", "scenarios", "SHOP-482", "SHOP-482-05.feature"))
	must(t, err)
	contains(t, "the scenario", string(feature),
		"@id:SHOP-482-05 @ticket:SHOP-482 @surface:web",
		"Scenario: A refund shows its total",
		"Given Open /refund",
		`When Type "5" in amount`,
		"And Click refund",
		`Then the element total equals "5.00"`,
		"# ticket "+flowCite+", by model",
	)
	if strings.Contains(string(feature), "# person") {
		t.Errorf("a check of a flow has the source person:\n%s", feature)
	}
	if !exists(t, r.path(".semar", "screens", "orders.yaml")) {
		t.Error("no screens file")
	}
}

func TestACheckThatThePageDoesNotMeetIsWrittenWithItsClaimAndFailsOnReplay(t *testing.T) {
	r, _ := recordRepoWith(t, repoOptions{app: sampleapp.Options{}})
	r.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "baseUrl: "+r.ordersURL, "baseUrl: "+r.ordersURL+"\n        web: {start: /refund}", 1)
	})
	// The model is told once that the total shows another text, and sends its checks again.
	turns := refundTurns(t, "6.00")
	turns = slices.Insert(turns, 2, turns[1])
	out, err := explorerOf(t, r, "").Explore(t.Context(), plan.WebRequest{ID: "SHOP-482-05", Ticket: "SHOP-482", Cite: flowCite}, scriptedFlow(t, turns...))
	if err != nil {
		t.Fatal(err)
	}
	if out.Test == nil || out.Test.Replay != "failed" || len(out.Test.FailsNow) != 1 {
		t.Fatalf("outcome = %+v", out)
	}
	if f := out.Test.FailsNow[0]; f.Element != "total" || f.Expect != "6.00" || f.Shown != "5.00" || f.Cite != flowCite {
		t.Errorf("fails now = %+v", f)
	}
	feature, err := os.ReadFile(r.path(".semar", "scenarios", "SHOP-482", "SHOP-482-05.feature"))
	must(t, err)
	contains(t, "the scenario", string(feature), `Then the element total equals "6.00"`)
}

func TestAFlowThatEndsAsManualWritesNothing(t *testing.T) {
	r, _ := recordRepoWith(t, repoOptions{app: sampleapp.Options{}})
	r.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "baseUrl: "+r.ordersURL, "baseUrl: "+r.ordersURL+"\n        web: {start: /refund}", 1)
	})
	flow := scriptedFlow(t, func(in plan.TurnInput) plan.Turn {
		return plan.TurnAct{Actions: []plan.Action{{Kind: plan.ActionFill, Ref: ref(t, in.Page, "textbox", "Amount"), Text: "5"}}}
	}, func(plan.TurnInput) plan.Turn { return plan.TurnManual{Reason: "the page has no due date"} })
	out, err := explorerOf(t, r, "").Explore(t.Context(), plan.WebRequest{ID: "SHOP-482-05", Ticket: "SHOP-482", Cite: flowCite}, flow)
	if err != nil {
		t.Fatal(err)
	}
	if out.Test != nil || out.Manual != "the page has no due date" {
		t.Errorf("outcome = %+v", out)
	}
	for _, p := range []string{r.path(".semar", "screens"), r.path(".semar", "scenarios", "SHOP-482", "SHOP-482-05.feature")} {
		if exists(t, p) {
			t.Errorf("%s was made for a flow that ended as manual", p)
		}
	}
}

// A page that shows another number each time it loads: the flow reads one, the replay meets the next.
func TestATestWhoseReplayDoesNotEndAsPredictedIsRemoved(t *testing.T) {
	var loads atomic.Int32
	r, _ := recordRepoWith(t, repoOptions{app: sampleapp.Options{}, wrap: func(next http.Handler) http.Handler {
		mux := http.NewServeMux()
		mux.HandleFunc("/counter", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprintf(w, `<!doctype html><title>Counter</title><p id="n">load %d</p>`, loads.Add(1))
		})
		mux.Handle("/", next)
		return mux
	}})
	r.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "baseUrl: "+r.ordersURL, "baseUrl: "+r.ordersURL+"\n        web: {start: /counter}", 1)
	})
	flow := scriptedFlow(t, func(in plan.TurnInput) plan.Turn {
		return plan.TurnAct{Actions: []plan.Action{{Kind: plan.ActionCheck, Ref: ref(t, in.Page, "text", "load 1"), Expect: "load 1"}}}
	}, func(plan.TurnInput) plan.Turn {
		return plan.TurnDone{Title: "The counter", Intent: "Before: x. Now: y."}
	})
	out, err := explorerOf(t, r, "").Explore(t.Context(), plan.WebRequest{ID: "SHOP-482-05", Ticket: "SHOP-482", Cite: flowCite}, flow)
	if err != nil {
		t.Fatal(err)
	}
	if out.Test != nil || !strings.Contains(out.Manual, "did not end as the recording predicts") || !strings.Contains(out.Manual, "passed was expected") {
		t.Fatalf("outcome = %+v", out)
	}
	for _, p := range []string{r.path(".semar", "screens"), r.path(".semar", "scenarios", "SHOP-482", "SHOP-482-05.feature"), r.path(".semar", "store", "scripts")} {
		if exists(t, p) {
			entries, _ := os.ReadDir(p)
			if len(entries) > 0 || !strings.HasSuffix(p, "scripts") {
				t.Errorf("%s was left behind after the test was removed", p)
			}
		}
	}
}

func TestAFlowWithFixturesMakesThemAndCleansThemUp(t *testing.T) {
	r, _ := recordRepoWith(t, repoOptions{tree: "e2e-fixtures", app: sampleapp.Options{}, payments: &sampleapp.PaymentOptions{}})
	r.approveFixture(t)
	r.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "baseUrl: "+r.ordersURL, "baseUrl: "+r.ordersURL+"\n        web: {start: /orders/o-1}", 1)
	})
	var outputs []plan.GivenOutput
	flow := scriptedFlow(t, func(plan.TurnInput) plan.Turn {
		return plan.TurnGiven{Given: []plan.AnswerGiven{{Fixture: "order.paid", As: "o"}}}
	}, func(in plan.TurnInput) plan.Turn {
		outputs = in.Given
		return plan.TurnAct{Actions: []plan.Action{{Kind: plan.ActionCheck, Ref: ref(t, in.Page, "text", "o-1"), Expect: "o-1"}}}
	}, func(plan.TurnInput) plan.Turn {
		return plan.TurnDone{Title: "The order is shown", Intent: "Before: x. Now: y."}
	})
	out, err := explorerOf(t, r, "support-agent").Explore(t.Context(), plan.WebRequest{ID: "SHOP-482-06", Ticket: "SHOP-482", Cite: flowCite}, flow)
	if err != nil {
		t.Fatal(err)
	}
	if out.Test == nil || out.Test.Replay != "passed" {
		t.Fatalf("outcome = %+v", out)
	}
	if len(outputs) == 0 || outputs[0].Alias != "o" || outputs[0].Value != "o-1" {
		t.Errorf("the model got the outputs %+v", outputs)
	}
	feature, err := os.ReadFile(r.path(".semar", "scenarios", "SHOP-482", "SHOP-482-06.feature"))
	must(t, err)
	contains(t, "the scenario", string(feature), "Given the fixture order.paid as o", `Then the element shown equals "{{o.id}}"`)
	if strings.Contains(string(feature), "o-1") {
		t.Errorf("the scenario holds the ID that the fixture gave:\n%s", feature)
	}
	if got := r.orderStatus(t, "o-1"); got != sampleapp.StatusCancelled {
		t.Errorf("order o-1 is %s, want cancelled: the fixture was not cleaned up", got)
	}
}

// A name that a flow creates holds the ID of the run, and the test keeps the variable in the text that it
// types, in what it expects, and in the locators that follow the name, so that it runs again.
func TestAFlowKeepsTheIDOfTheRunAsAVariable(t *testing.T) {
	r, _ := recordRepoWith(t, repoOptions{app: sampleapp.Options{}})
	r.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "baseUrl: "+r.ordersURL, "baseUrl: "+r.ordersURL+"\n        web: {start: /echo}", 1)
	})
	flow := scriptedFlow(t, func(in plan.TurnInput) plan.Turn {
		return plan.TurnAct{Actions: []plan.Action{
			{Kind: plan.ActionFill, Ref: ref(t, in.Page, "textbox", "Name"), Text: "Board {{run.id}}"},
			{Kind: plan.ActionClick, Ref: ref(t, in.Page, "button", "Save")},
		}}
	}, func(in plan.TurnInput) plan.Turn {
		var saved string
		for _, e := range in.Page.Elements {
			if strings.HasPrefix(e.Name, "Saved Board r-test-") {
				saved = e.Ref
			}
		}
		return plan.TurnAct{Actions: []plan.Action{{Kind: plan.ActionCheck, Ref: saved, Expect: "Saved Board {{run.id}}"}}}
	}, func(plan.TurnInput) plan.Turn {
		return plan.TurnDone{Title: "A name is saved", Intent: "Before: x. Now: y."}
	})
	out, err := explorerOf(t, r, "").Explore(t.Context(), plan.WebRequest{ID: "SHOP-482-05", Ticket: "SHOP-482", Cite: flowCite}, flow)
	if err != nil {
		t.Fatal(err)
	}
	if out.Test == nil || out.Test.Replay != "passed" || len(out.Test.FailsNow) != 0 {
		t.Fatalf("outcome = %+v", out)
	}
	feature, err := os.ReadFile(r.path(".semar", "scenarios", "SHOP-482", "SHOP-482-05.feature"))
	must(t, err)
	contains(t, "the scenario", string(feature), `Type "Board {{run.id}}" in name`, `equals "Saved Board {{run.id}}"`)
	screensFile, err := os.ReadFile(r.path(".semar", "screens", "orders.yaml"))
	must(t, err)
	for name, data := range map[string]string{"the scenario": string(feature), "the screens": string(screensFile)} {
		if strings.Contains(data, "r-test-") {
			t.Errorf("%s holds the ID of the run that the flow made:\n%s", name, data)
		}
	}
}

func TestAFlowThatTypesADateKeepsTheVariableAndTheTestFollowsTheRun(t *testing.T) {
	old := time.Local
	time.Local = time.FixedZone("UTC+7", 7*3600)
	t.Cleanup(func() { time.Local = old })

	r, _ := recordRepoWith(t, repoOptions{app: sampleapp.Options{}})
	r.opts.RunID = func() domain.RunID { return "r-20261118T071502Z-0001" }
	r.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "baseUrl: "+r.ordersURL, "baseUrl: "+r.ordersURL+"\n        web: {start: /due}", 1)
	})
	flow := scriptedFlow(t, func(in plan.TurnInput) plan.Turn {
		return plan.TurnAct{Actions: []plan.Action{
			{Kind: plan.ActionFill, Ref: ref(t, in.Page, "date", "Due date"), Text: "{{date.plus7d}}"},
			{Kind: plan.ActionClick, Ref: ref(t, in.Page, "button", "Save")},
		}}
	}, func(in plan.TurnInput) plan.Turn {
		return plan.TurnAct{Actions: []plan.Action{
			{Kind: plan.ActionCheck, Ref: ref(t, in.Page, "text", "Due 2026-11-25"), Expect: "Due {{date.plus7d}}"},
		}}
	}, func(plan.TurnInput) plan.Turn {
		return plan.TurnDone{Title: "A due date is shown", Intent: "Before: no date. Now: the date is shown."}
	})
	out, err := explorerOf(t, r, "").Explore(t.Context(), plan.WebRequest{ID: "SHOP-482-05", Ticket: "SHOP-482", Cite: flowCite}, flow)
	if err != nil {
		t.Fatal(err)
	}
	if out.Test == nil || out.Test.Replay != "passed" {
		t.Fatalf("outcome = %+v", out)
	}

	feature, err := os.ReadFile(r.path(".semar", "scenarios", "SHOP-482", "SHOP-482-05.feature"))
	must(t, err)
	contains(t, "the scenario", string(feature), `equals "Due {{date.plus7d}}"`)
	if strings.Contains(string(feature), "2026-11-25") {
		t.Errorf("the scenario holds the typed date:\n%s", feature)
	}

	files, err := filepath.Glob(r.path(".semar", "store", "scripts", "SHOP-482-05", "*.yaml"))
	if err != nil || len(files) != 1 {
		t.Fatalf("scripts %v, %v", files, err)
	}
	data, err := os.ReadFile(files[0])
	must(t, err)
	if !strings.Contains(string(data), "{{date.plus7d}}") || strings.Contains(string(data), "2026-11-25") {
		t.Errorf("the script must hold the variable and not the date:\n%s", data)
	}
	s, err := script.Parse(data, files[0])
	must(t, err)
	var fill domain.WebAction
	for _, st := range s.Steps {
		if st.Web != nil && st.Web.Do == domain.WebFill {
			fill = *st.Web
		}
	}
	for runID, want := range map[domain.RunID]string{"r-20261118T071502Z-0001": "2026-11-25", "r-20261125T071502Z-0001": "2026-12-02"} {
		got, err := script.RenderWeb(fill, map[string]string{"run.id": string(runID)})
		if err != nil || got.Value != want {
			t.Errorf("run %s typed %q, %v, want %s", runID, got.Value, err, want)
		}
	}
}
