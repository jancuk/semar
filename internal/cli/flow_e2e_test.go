package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/flow"
)

const androidLoginFlow = `apiVersion: semar/v1
kind: Flow
id: login
surface: android
service: shop-app
app: shop-app
steps:
  - step: start
    mobile: { do: launch }
  - step: submit
    mobile: { do: tap, screen: SignIn, element: submit }
`

func useScenarioFiles(id string) string {
	feature := fmt.Sprintf(`Feature: Signing in with a flow

  @id:%s @ticket:SHOP-1 @surface:android @auth:support-agent
  Scenario: A customer signs in with the login flow
    The app greets the customer

    Given Sign in
    # step: login
    When Wait for the greeting
    # step: seen
    Then the element welcome equals "Hi Ana"
    # person
`, id)
	script := fmt.Sprintf(`apiVersion: semar/v1
kind: Script
scenario: %s
authoredBy: { instance: person, model: "", harness: file }
steps:
  - step: login
    use: login
  - step: seen
    mobile: { service: shop-app, app: shop-app, do: wait, screen: SignIn, element: welcome }
`, id)
	return feature + "\x00" + script
}

func runResults(t *testing.T, r *androidRig) []domain.ScenarioResult {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.latestRun(t), "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var run domain.Run
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatal(err)
	}
	return run.Results
}

func TestAndroidScenarioUsesAFlow(t *testing.T) {
	r := newAndroidRig(t, androidOptions{scenarios: map[string]string{"AND-4": useScenarioFiles("AND-4")}})
	r.write(t, ".semar/flows/login.yaml", androidLoginFlow)
	res := r.must(t, cli.ExitOK, "run", "--scenario", "AND-4")
	contains(t, "stdout", res.stdout, "AND-4  passed")
	if r.taps.Load() != 1 {
		t.Errorf("the sign-in button was tapped %d times, want 1", r.taps.Load())
	}
	contains(t, "the report", r.lastReport(t), "## Device steps", "flow login, step start", "flow login, step submit")
	results := runResults(t, r)
	want := domain.FlowRef{ID: "login", Digest: flow.Digest([]byte(androidLoginFlow))}
	if len(results) != 1 || len(results[0].Flows) != 1 || results[0].Flows[0] != want {
		t.Errorf("flows of the result = %+v, want %+v", results[0].Flows, want)
	}
	if len(results[0].Actions) != 3 {
		t.Errorf("the result lists %d actions, want the 2 of the flow and the wait", len(results[0].Actions))
	}
}

func TestAndroidFailingFlowStepStopsTheScenario(t *testing.T) {
	r := newAndroidRig(t, androidOptions{scenarios: map[string]string{"AND-4": useScenarioFiles("AND-4")}})
	r.write(t, ".semar/flows/login.yaml", androidLoginFlow)
	// The tap of the flow cannot find its button on the device.
	r.edit(t, ".semar/screens/shop-app.yaml", func(s string) string {
		return strings.Replace(s, "submit: { desc: Sign in }", "submit: { desc: Log in }", 1)
	})
	res := r.must(t, cli.ExitUndecided, "run", "--scenario", "AND-4")
	if strings.Contains(res.stdout, "AND-4  passed") {
		t.Errorf("a scenario whose flow step failed passed:\n%s", res.stdout)
	}
	if r.taps.Load() != 0 {
		t.Errorf("the button was tapped %d times", r.taps.Load())
	}
	results := runResults(t, r)
	if len(results[0].Actions) != 2 {
		t.Errorf("the result lists %d actions, want the start and the failed tap: the wait must not be sent", len(results[0].Actions))
	}
	contains(t, "the report", r.lastReport(t), "flow login, step submit")
}

func TestAndroidFlowThatDoesNotFitTheScreensBlocksTheScenario(t *testing.T) {
	r := newAndroidRig(t, androidOptions{scenarios: map[string]string{"AND-4": useScenarioFiles("AND-4")}})
	r.write(t, ".semar/flows/login.yaml", strings.Replace(androidLoginFlow, "element: submit", "element: nothing", 1))
	res := r.must(t, cli.ExitUndecided, "run", "--scenario", "AND-4")
	contains(t, "stdout", res.stdout, "AND-4  blocked")
	contains(t, "the report", r.lastReport(t), "element nothing is not on screen SignIn of shop-app")
}

func TestAndroidScenarioThatUsesAMissingFlowIsBlocked(t *testing.T) {
	r := newAndroidRig(t, androidOptions{scenarios: map[string]string{"AND-4": useScenarioFiles("AND-4")}})
	res := r.must(t, cli.ExitUndecided, "run", "--scenario", "AND-4")
	contains(t, "stdout", res.stdout, "AND-4  blocked")
	contains(t, "the report", r.lastReport(t), "use: flow login: .semar/flows/login.yaml: the flow file was not found")
}

// Twenty scenarios, each with a flow of ten steps, take more device actions than the default budget of
// 200. The budget grows with the run, so none of them is refused.
func TestAndroidTwentyScenariosWithALongFlowStayWithinTheBudget(t *testing.T) {
	long := androidLoginFlow
	for i := 0; i < 8; i++ {
		long += fmt.Sprintf("  - step: settle%d\n    mobile: { do: wait }\n", i)
	}
	scenarios := map[string]string{}
	args := []string{"run"}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("AND-%d", 10+i)
		scenarios[id] = useScenarioFiles(id)
		args = append(args, "--scenario", id)
	}
	r := newAndroidRig(t, androidOptions{scenarios: scenarios})
	r.write(t, ".semar/flows/login.yaml", long)
	r.must(t, cli.ExitOK, args...)
	results := runResults(t, r)
	if len(results) != 20 {
		t.Fatalf("%d results, want 20", len(results))
	}
	total := 0
	for _, res := range results {
		if res.Outcome != domain.OutcomePassed {
			t.Errorf("%s: %s %+v", res.Scenario, res.Outcome, res.Reasons)
		}
		total += len(res.Actions)
	}
	if total <= 200 {
		t.Errorf("the run took %d actions: the test must go over the default budget of 200 to prove the budget grew", total)
	}
}
