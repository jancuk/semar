package cli_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/engine/mobile/mobiletest"
	"github.com/jancuk/semar/internal/sampleapp"
)

const paymentsToken = "tok-pay"

// journeySay calls a tool of the server and fails the test on an error result.
func journeySay(t *testing.T, p *mcpProc, name string, kv ...any) string {
	t.Helper()
	text, isErr := p.call(name, kvArgs(kv))
	if isErr {
		t.Fatalf("%s %v: %s\nstderr:\n%s", name, kv, text, p.stderr)
	}
	return text
}

func kvArgs(kv []any) map[string]any {
	args := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		args[kv[i].(string)] = kv[i+1]
	}
	return args
}

// capturedOf is what the api_request result says it captured.
func capturedOf(t *testing.T, text string) map[string]string {
	t.Helper()
	var rp struct {
		Captured map[string]string `json:"captured"`
	}
	if err := json.Unmarshal([]byte(strings.SplitN(text, "\n", 2)[0]), &rp); err != nil {
		// The note of a leg comes after the JSON, as a second block: read the whole text up to it.
		start := strings.Index(text, "{")
		end := strings.LastIndex(text, "}")
		if start < 0 || end < start || json.Unmarshal([]byte(text[start:end+1]), &rp) != nil {
			t.Fatalf("the result is not JSON: %v\n%s", err, text)
		}
	}
	return rp.Captured
}

func TestMCPJourneyOfTwoAPILegsIsSavedReplayedAndRunsWithNoModel(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	p := startMCP(t, r.opts, "--dir", r.dir, "--env", "local", "--surfaces", "api,journey")
	p.initialize()
	names := p.toolNames()
	for _, want := range []string{"semar_journey_start", "semar_journey_add_leg", "semar_journey_status", "semar_journey_undo_step", "semar_journey_save", "semar_journey_run", "semar_journey_discard"} {
		if !hasTool(names, want) {
			t.Fatalf("%s is not listed: %v", want, names)
		}
	}

	journeySay(t, p, "semar_journey_start", "id", "agent-paid", "title", "An order is placed and paid", "owner", "team-commerce")

	first := journeySay(t, p, "api_request", "service", "orders", "method", "POST", "path", "/v1/orders", "as", "support-agent",
		"json", `{"amount": 1000}`, "why", "place the order that is paid next",
		"captures", []map[string]string{{"name": "id", "jsonpath": "$.data.id"}, {"name": "amount", "jsonpath": "$.data.amount"}})
	if !strings.Contains(first, "journey: opened the leg api_1") {
		t.Errorf("the first request opened no leg:\n%s", first)
	}
	got := capturedOf(t, first)
	if got["id"] == "" {
		t.Fatalf("nothing was captured: %s", first)
	}

	// The model names the capture as {{id}}; the journey writes it as the output of the first leg, {{api_1.id}}.
	second := journeySay(t, p, "api_request", "service", "payments", "method", "POST", "path", "/v1/payments", "as", "payments-tester",
		"json", `{"orderId": "{{id}}"}`, "why", "pay the order",
		"captures", []map[string]string{{"name": "amount", "jsonpath": "$.data.amount"}},
		"expect", []map[string]any{{"status": 202}})
	if !strings.Contains(second, "journey: opened the leg api_2") || !strings.Contains(second, "needs api_1") {
		t.Errorf("the second request did not chain a leg:\n%s", second)
	}

	status := journeySay(t, p, "semar_journey_status")
	contains(t, "status", status, "id: api_1", "id: api_2", "{{api_1.id}}")

	saved := journeySay(t, p, "semar_journey_save", "invariants", [][]string{{"{{api_1.amount}}", "{{api_2.amount}}"}})
	contains(t, "save", saved, `"saved": true`, `"outcome": "passed"`, ".semar/journeys/agent-paid.yaml")

	yaml := r.readJourney(t, "agent-paid")
	contains(t, "the journey", yaml, "by: model", "source: baseline", "{{api_1.id}}", "needs:", "outputs:", "as: support-agent", "as: payments-tester")
	for _, secret := range []string{token, paymentsToken} {
		if strings.Contains(yaml, secret) {
			t.Errorf("the journey holds a token:\n%s", yaml)
		}
	}
	if code := p.finish(); code != cli.ExitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, p.stderr)
	}

	// A new process runs the saved journey: no provider is set, so a model could not be called, and the
	// usage file is not there.
	res := r.must(t, cli.ExitOK, "journey", "run", "agent-paid", "--env", "local")
	contains(t, "the summary", squash(res.stdout), "agent-paid passed", "api_1 passed", "api_2 passed")
	if _, err := os.Stat(filepath.Join(r.opts.Home, "usage.jsonl")); err == nil {
		t.Error("a model was used: the usage file exists")
	}
	r.assertNoSecret(t, []string{token, paymentsToken})
	for _, out := range append(r.output, p.stderr.String()) {
		for _, secret := range []string{token, paymentsToken} {
			if strings.Contains(out, secret) {
				t.Errorf("an output holds %q:\n%s", secret, out)
			}
		}
	}
}

func TestMCPJourneyIsRemovedWhenItsReplayDoesNotPass(t *testing.T) {
	r := journeyRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	p := startMCP(t, r.opts, "--dir", r.dir, "--env", "local", "--surfaces", "api,journey")
	p.initialize()
	journeySay(t, p, "semar_journey_start", "id", "never-kept", "title", "A payment of an order that a replay does not make", "owner", "team-commerce")
	first := journeySay(t, p, "api_request", "service", "orders", "method", "POST", "path", "/v1/orders", "as", "support-agent",
		"json", `{"amount": 1000}`, "why", "place an order",
		"captures", []map[string]string{{"name": "id", "jsonpath": "$.data.id"}})
	id := capturedOf(t, first)["id"]
	// The order ID is sent as a literal. It is too short to be lifted to a variable, so the replay pays the
	// order of the recording again, which the service refuses.
	journeySay(t, p, "api_request", "service", "payments", "method", "POST", "path", "/v1/payments", "as", "payments-tester",
		"json", `{"orderId": "`+id+`"}`, "why", "pay the order")

	text, isErr := p.call("semar_journey_save", nil)
	if !isErr {
		t.Fatalf("a journey whose replay does not pass was kept:\n%s", text)
	}
	contains(t, "the refusal", text, `"saved": false`, `"outcome": "failed"`, "api_2", "removed")
	if _, err := os.Stat(r.journeyFile("never-kept")); err == nil {
		t.Error("the journey file is still there")
	}
	// The last leg is open again, so the agent can undo its step and drop the draft.
	journeySay(t, p, "semar_journey_undo_step")
	journeySay(t, p, "semar_journey_discard")
	if code := p.finish(); code != cli.ExitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, p.stderr)
	}
}

func TestMCPJourneyProductionRefusesEveryWriteAndRun(t *testing.T) {
	var writes int
	r := newRepo(t, repoOptions{wrap: func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Method != http.MethodGet {
				writes++
			}
			next.ServeHTTP(w, req)
		})
	}})
	r.rewriteConfig(t, func(s string) string { return strings.Replace(s, "kind: test", "kind: production", 1) })
	p := startMCP(t, r.opts, "--dir", r.dir, "--env", "local", "--surfaces", "api,journey")
	p.initialize()
	for name, args := range map[string]map[string]any{
		"semar_journey_start":     {"id": "x", "title": "t", "owner": "o"},
		"semar_journey_add_leg":   {"id": "a", "surface": "api", "service": "orders"},
		"semar_journey_undo_step": {},
		"semar_journey_save":      {},
		"semar_journey_run":       {"id": "x"},
		"semar_journey_discard":   {},
	} {
		text, isErr := p.call(name, args)
		if !isErr || !strings.Contains(text, "only observes") {
			t.Errorf("%s in production: isError=%v %s", name, isErr, text)
		}
	}
	if text, isErr := p.call("semar_journey_status", nil); !isErr || !strings.Contains(text, "no draft") {
		t.Errorf("status = %v %s", isErr, text)
	}
	if writes != 0 {
		t.Errorf("%d writes reached the server", writes)
	}
	if _, err := os.Stat(r.path(".semar", "journeys")); err == nil {
		t.Error("a journeys folder was made")
	}
	if code := p.finish(); code != cli.ExitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, p.stderr)
	}
}

func TestMCPJourneyToolsAreLeftOutOfStandaloneAndNeedATap(t *testing.T) {
	r := localRun(t, androidOptions{})
	p := startMCP(t, r.opts, "--dir", t.TempDir())
	p.initialize()
	for _, n := range p.toolNames() {
		if strings.HasPrefix(n, "semar_journey_") {
			t.Errorf("standalone mode lists %s", n)
		}
	}
	p.finish()

	r2 := newRepo(t, repoOptions{})
	p2 := startMCP(t, r2.opts, "--dir", r2.dir, "--env", "local")
	p2.initialize()
	if !hasTool(p2.toolNames(), "semar_journey_start") {
		t.Errorf("the repository mode lacks the journey tools: %v", p2.toolNames())
	}
	p2.finish()
}

// journeyRig is an android rig whose app forgets its sign-in when it is started again, as an app that keeps
// its session in memory does, so that a replay of a journey starts on the sign-in screen as the recording did.
func journeyRig(t *testing.T) *androidRig {
	t.Helper()
	r := newAndroidRig(t, androidOptions{env: "device: emulator-5554", scenarios: map[string]string{}, apk: "an apk"})
	r.server.OnExecute(func(script string, _ map[string]any) (any, *mobiletest.Failure) {
		if script == "mobile: activateApp" {
			r.server.SetSource(signedOutScreen())
		}
		return nil, nil
	})
	return r
}

func TestMCPJourneyWithAnAPILegAndARecordedAndroidLegIsSavedAndReplayed(t *testing.T) {
	const password = "pw-Zk93-journey-canary"
	t.Setenv("SHOP_PASSWORD", password)
	r := journeyRig(t)
	p := startMCP(t, r.opts, "--dir", r.dir, "--env", "local")
	p.initialize()

	var transcript []string
	say := func(name string, kv ...any) string {
		t.Helper()
		text := journeySay(t, p, name, kv...)
		transcript = append(transcript, text)
		return text
	}
	byID := func(suffix string) func(id, desc, text string) bool {
		return func(id, _, _ string) bool { return strings.HasSuffix(id, ":id/"+suffix) }
	}
	byDesc := func(want string) func(id, desc, text string) bool {
		return func(_, desc, _ string) bool { return desc == want }
	}
	refOf := func(match func(id, desc, text string) bool) string {
		t.Helper()
		return mcpRef(t, say("mobile_list_elements_on_screen", "format", "json"), match)
	}

	say("semar_journey_start", "id", "order-and-app", "title", "An order is placed, then the app signs in", "owner", "team-shop")
	say("api_request", "service", "orders", "method", "POST", "path", "/v1/orders", "as", "support-agent",
		"json", `{"amount": 1200}`, "why", "place an order", "expect", []map[string]any{{"status": 201}},
		"captures", []map[string]string{{"name": "order_id", "jsonpath": "$.data.id"}})
	say("semar_journey_add_leg", "id", "app", "surface", "android", "service", "shop-app")
	// The first action of the leg starts the recording of its flow from a fresh start of the app. A launch
	// is answered by the recording, which has started the app already.
	if launch := say("mobile_launch_app", "packageName", androidPackage); !strings.Contains(launch, "started the app from a fresh start") {
		t.Errorf("launch: %s", launch)
	}
	say("mobile_type_keys", "text", "ana@example.com", "ref", refOf(byID("email")))
	say("mobile_type_keys", "secretEnv", "SHOP_PASSWORD", "ref", refOf(byID("password")))
	say("mobile_click_on_screen_at_coordinates", "ref", refOf(byDesc("Sign in")))
	say("semar_record_screen", "name", "Home")
	// The check is a check of the leg: it is judged after the steps of the flow, so it comes last.
	say("semar_record_assert", "ref", refOf(byID("welcome")), "kind", "equals", "value", "Hi Ana")
	say("semar_record_capture", "ref", refOf(byID("welcome")), "name", "greeting")

	status := say("semar_journey_status")
	contains(t, "status", status, "recording")

	saved := say("semar_journey_save")
	contains(t, "save", saved, `"saved": true`, `"outcome": "passed"`, ".semar/flows/order-and-app-app.yaml", ".semar/journeys/order-and-app.yaml")

	yaml, err := os.ReadFile(r.path(".semar", "journeys", "order-and-app.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "the journey", string(yaml), "by: model", "use: order-and-app-app", "surface: android", "surface: api", "- greeting", "do: wait", "equals: Hi Ana")
	flowText, err := os.ReadFile(r.path(".semar", "flows", "order-and-app-app.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "the flow", string(flowText), "secret: env:SHOP_PASSWORD", "name: greeting")
	if strings.Contains(string(flowText), "expect:") {
		t.Errorf("the flow holds a check:\n%s", flowText)
	}
	if code := p.finish(); code != cli.ExitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, p.stderr)
	}

	// A new process replays it with no model.
	res := r.must(t, cli.ExitOK, "journey", "run", "order-and-app", "--env", "local")
	contains(t, "the summary", squash(res.stdout), "order-and-app passed", "api_1 passed", "app passed")
	if _, err := os.Stat(filepath.Join(r.opts.Home, "usage.jsonl")); err == nil {
		t.Error("a model was used: the usage file exists")
	}

	for i, text := range transcript {
		for _, secret := range []string{password, token} {
			if strings.Contains(text, secret) {
				t.Fatalf("tool result %d holds a secret:\n%s", i, text)
			}
		}
	}
	for _, out := range append(r.output, p.stderr.String()) {
		for _, secret := range []string{password, token} {
			if strings.Contains(out, secret) {
				t.Errorf("an output holds a secret:\n%s", out)
			}
		}
	}
	r.assertNoSecret(t, []string{password, token})
}

// signInFlow is a saved flow that a person wrote: it signs in with one tap, and checks the greeting.
const signInFlow = `apiVersion: semar/v1
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
    expect:
      - check: { element: welcome, equals: "Hi Ana" }
        source: person
`

func TestMCPJourneyWithAnAndroidLegThatRunsASavedFlow(t *testing.T) {
	r := journeyRig(t)
	r.writeRel(t, ".semar/flows/login.yaml", signInFlow)

	p := startMCP(t, r.opts, "--dir", r.dir, "--env", "local")
	p.initialize()
	journeySay(t, p, "semar_journey_start", "id", "phone-login", "title", "An order is placed, then the app signs in with a saved flow", "owner", "team-shop")
	journeySay(t, p, "api_request", "service", "orders", "method", "POST", "path", "/v1/orders", "as", "support-agent",
		"json", `{"amount": 700}`, "why", "place an order")
	journeySay(t, p, "semar_journey_add_leg", "id", "phone", "surface", "android", "service", "shop-app")
	// The run of a saved flow is the first action of the leg: the leg is recorded, and the run is one step.
	run := journeySay(t, p, "semar_run_flow", "id", "login")
	contains(t, "the flow", run, "Hi Ana")
	listing := journeySay(t, p, "mobile_list_elements_on_screen", "format", "json")
	journeySay(t, p, "semar_record_assert", "ref", mcpRef(t, listing, func(id, _, _ string) bool { return strings.HasSuffix(id, ":id/welcome") }),
		"kind", "equals", "value", "Hi Ana")

	saved := journeySay(t, p, "semar_journey_save")
	contains(t, "save", saved, `"saved": true`, `"outcome": "passed"`, ".semar/journeys/phone-login.yaml", ".semar/flows/phone-login-phone.yaml")
	flowText, err := os.ReadFile(r.path(".semar", "flows", "phone-login-phone.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "the flow of the leg", string(flowText), "use: login")
	yaml, err := os.ReadFile(r.path(".semar", "journeys", "phone-login.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "the journey", string(yaml), "use: phone-login-phone", "surface: android", "app: shop-app", "by: model")
	if code := p.finish(); code != cli.ExitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, p.stderr)
	}
	res := r.must(t, cli.ExitOK, "journey", "run", "phone-login", "--env", "local")
	contains(t, "the summary", squash(res.stdout), "phone-login passed", "phone passed")
	r.assertNoSecret(t, []string{token})
}

func TestMCPJourneyScreenActionThatStartsTheRecordingIsNotSentAndAServiceWithNoBuildIsWarnedOf(t *testing.T) {
	// The service has no apk, no baseUrl, and no build probe, so a replay could not read its build.
	r := newAndroidRig(t, androidOptions{env: "device: emulator-5554", scenarios: map[string]string{}})
	p := startMCP(t, r.opts, "--dir", r.dir, "--env", "local")
	p.initialize()
	journeySay(t, p, "semar_journey_start", "id", "tap-first", "title", "A tap is the first action", "owner", "team-shop")
	journeySay(t, p, "mobile_list_elements_on_screen", "format", "json")
	// The y of the sign-in button of the screen that was listed: the tap would sign in if it was sent.
	// The device is behind a server, so the agent names the service of the leg itself.
	leg := journeySay(t, p, "semar_journey_add_leg", "id", "app", "surface", "android", "service", "shop-app")
	contains(t, "the leg", leg, `"warning"`, "the service shop-app has no build", "android.apk")
	got := journeySay(t, p, "mobile_click_on_screen_at_coordinates", "x", 500, "y", 950)
	contains(t, "the tap", got, "was not sent", "window app")
	if r.taps.Load() != 0 || len(r.server.Touches()) != 0 {
		t.Errorf("the tap reached the device: taps %d, touches %v", r.taps.Load(), r.server.Touches())
	}
	// The recording is on, so the next tap is sent.
	journeySay(t, p, "mobile_click_on_screen_at_coordinates", "x", 500, "y", 950)
	if r.taps.Load() != 1 {
		t.Errorf("the second tap was not sent: taps %d", r.taps.Load())
	}
	journeySay(t, p, "semar_journey_discard")
	if code := p.finish(); code != cli.ExitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, p.stderr)
	}
}

func TestMCPJourneyAnAndroidLegWithNoCheckCannotBeClosed(t *testing.T) {
	r := journeyRig(t)
	p := startMCP(t, r.opts, "--dir", r.dir, "--env", "local")
	p.initialize()
	journeySay(t, p, "semar_journey_start", "id", "no-check", "title", "A leg that checks nothing", "owner", "team-shop")
	journeySay(t, p, "semar_journey_add_leg", "id", "app", "surface", "android", "service", "shop-app")
	journeySay(t, p, "mobile_launch_app", "packageName", androidPackage)
	listing := journeySay(t, p, "mobile_list_elements_on_screen", "format", "json")
	journeySay(t, p, "mobile_click_on_screen_at_coordinates", "ref", mcpRef(t, listing, func(_, desc, _ string) bool { return desc == "Sign in" }))
	text, isErr := p.call("semar_journey_save", nil)
	if !isErr || !strings.Contains(text, "semar_record_assert") {
		t.Fatalf("save of a leg with no check: isError=%v %s", isErr, text)
	}
	if _, err := os.Stat(r.journeyFile("no-check")); err == nil {
		t.Error("a journey was written")
	}
	// The leg is still open, so the agent can add the check and save.
	listing = journeySay(t, p, "mobile_list_elements_on_screen", "format", "json")
	journeySay(t, p, "semar_record_assert", "ref", mcpRef(t, listing, func(id, _, _ string) bool { return strings.HasSuffix(id, ":id/welcome") }),
		"kind", "equals", "value", "Hi Ana")
	journeySay(t, p, "semar_journey_save", "run", false)
	if _, err := os.Stat(r.journeyFile("no-check")); err != nil {
		t.Error("the journey was not written after the check")
	}
	if code := p.finish(); code != cli.ExitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, p.stderr)
	}
}

func TestMCPJourneyWithAnAPILegAndAWebLegLiftsTheTypedValueAndReplays(t *testing.T) {
	r, _ := recordRepo(t)
	p := startMCP(t, r.opts, "--dir", r.dir, "--env", "local", "--surfaces", "api,web,journey")
	p.initialize()

	journeySay(t, p, "semar_journey_start", "id", "order-on-the-page", "title", "An order is made by the API and its ID is typed on a page", "owner", "team-shop")
	created := journeySay(t, p, "api_request", "service", "orders", "method", "POST", "path", "/v1/orders", "as", "support-agent",
		"json", `{"amount": 900}`, "why", "make an order whose ID the page shows",
		"captures", []map[string]string{{"name": "order_id", "jsonpath": "$.data.id"}})
	id := capturedOf(t, created)["order_id"]
	if len(id) < 3 {
		t.Fatalf("the order ID %q is too short to be lifted", id)
	}

	list := journeySay(t, p, "web_open", "path", "/echo")
	if !strings.Contains(list, "journey: opened the leg web_1") || !strings.Contains(list, "needs api_1") {
		t.Errorf("web_open opened no leg:\n%s", list)
	}
	journeySay(t, p, "web_fill", "ref", refOf(t, list, "textbox", "Name"), "text", id)
	journeySay(t, p, "web_click", "ref", refOf(t, list, "button", "Save"))
	after := journeySay(t, p, "web_list_elements")
	shown := ""
	for _, line := range strings.Split(after, "\n") {
		if strings.Contains(line, `"Saved `+id+`"`) {
			shown = strings.Fields(line)[0]
		}
	}
	if shown == "" {
		t.Fatalf("the page does not show the result:\n%s", after)
	}
	journeySay(t, p, "web_assert", "ref", shown)

	status := journeySay(t, p, "semar_journey_status")
	contains(t, "status", status, "{{api_1.order_id}}")
	saved := journeySay(t, p, "semar_journey_save")
	contains(t, "save", saved, `"saved": true`, `"outcome": "passed"`, ".semar/journeys/order-on-the-page.yaml", ".semar/screens/orders.yaml")

	yaml := r.readJourney(t, "order-on-the-page")
	contains(t, "the journey", yaml, "surface: web", "app: orders", "by: model", "{{api_1.order_id}}")
	if strings.Contains(yaml, id+"\n") && !strings.Contains(yaml, "{{api_1.order_id}}") {
		t.Errorf("the typed value is a literal:\n%s", yaml)
	}
	if code := p.finish(); code != cli.ExitOK {
		t.Fatalf("exit %d\nstderr:\n%s", code, p.stderr)
	}
	res := r.must(t, cli.ExitOK, "journey", "run", "order-on-the-page", "--env", "local")
	contains(t, "the summary", squash(res.stdout), "order-on-the-page passed", "api_1 passed", "web_1 passed")
	r.assertNoSecret(t, []string{token})
}
