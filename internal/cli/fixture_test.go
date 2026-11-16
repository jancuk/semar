package cli_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/sampleapp"
)

const (
	fixtureScenario = "SHOP-482-05"
	// The hex of the first 12 characters is how the report names a hash.
	hashWidth = 12
)

// fixtureRepo is a copy of testdata/e2e-fixtures, served by orders and payments on 127.0.0.1,
// with the script of its scenario stored.
func fixtureRepo(t *testing.T, app sampleapp.Options, pay sampleapp.PaymentOptions) *repo {
	t.Helper()
	r := newRepo(t, repoOptions{tree: "e2e-fixtures", app: app, payments: &pay})
	r.must(t, cli.ExitOK, "script", "add", r.path("scripts", fixtureScenario+".yaml"))
	return r
}

func defects(ds ...sampleapp.Defect) map[sampleapp.Defect]bool {
	m := map[sampleapp.Defect]bool{}
	for _, d := range ds {
		m[d] = true
	}
	return m
}

// call sends a request straight to a service of the repo, outside Semar.
func (r *repo) call(t *testing.T, method, base, path, tok string, body any) map[string]any {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, base+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode > 299 {
		t.Fatalf("%s %s answered %d", method, path, resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func (r *repo) orderStatus(t *testing.T, id string) string {
	t.Helper()
	data, _ := r.call(t, "GET", r.ordersURL, "/v1/orders/"+id, token, nil)["data"].(map[string]any)
	status, _ := data["status"].(string)
	return status
}

// seedPaidOrder makes a paid order of customer c-1 outside Semar, as a person's earlier test would.
func (r *repo) seedPaidOrder(t *testing.T) string {
	t.Helper()
	data, _ := r.call(t, "POST", r.ordersURL, "/v1/orders", token, map[string]any{"amount": 1000, "customer": "c-1"})["data"].(map[string]any)
	id, _ := data["id"].(string)
	r.call(t, "POST", r.paymentsURL, "/v1/payments", "tok-pay", map[string]any{"orderId": id})
	for range 3 {
		if r.orderStatus(t, id) == sampleapp.StatusPaid {
			return id
		}
	}
	t.Fatalf("order %s never became paid", id)
	return ""
}

// reached is how many requests with this path prefix any service has received.
func (r *repo) reached(prefix string) int {
	return r.app.Requests("", prefix) + r.pay.Requests("", prefix)
}

func (r *repo) lastRun(t *testing.T) domain.Run {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.latestRun(t), "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var run domain.Run
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatal(err)
	}
	return run
}

func (r *repo) lastReport(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.latestRun(t), "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (r *repo) approveFixture(t *testing.T) domain.Hash {
	t.Helper()
	r.must(t, cli.ExitOK, "confirm", "--fixture", "order.paid", "--yes")
	h, err := cli.FixtureHashOf(r.dir, "order.paid")
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func slicesContains(ids []domain.ActionID, id domain.ActionID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func TestFixtureListShowsApprovalAndProblems(t *testing.T) {
	r := newRepo(t, repoOptions{tree: "e2e-fixtures", offline: true})

	res := r.must(t, cli.ExitOK, "fixture", "list")
	contains(t, "the list", res.stdout, "order.paid", "1.0.0", "team-orders", "not approved", "used by SHOP-482-05")
	if strings.Contains(res.stdout, "invalid:") {
		t.Errorf("a valid catalogue lists a problem:\n%s", res.stdout)
	}

	r.approveFixture(t)
	res = r.must(t, cli.ExitOK, "fixture", "list")
	if strings.Contains(res.stdout, "not approved") || !strings.Contains(res.stdout, "approved") {
		t.Errorf("an approved fixture is listed as:\n%s", res.stdout)
	}

	// A file that cannot be used is listed with its problem, and the exit code says so.
	r.writeFixture(t, "order-refunded.yaml", strings.Replace(orderPaidFixture, "id: order.paid", "id: order.refunded", 1))
	r.writeFixture(t, "broken.yaml", "apiVersion: semar/v1\nkind: Fixture\nretries: 3\n")
	res = r.must(t, cli.ExitUndecided, "fixture", "list")
	contains(t, "the list", res.stdout,
		"order.paid",
		"invalid: .semar/fixtures/order-refunded.yaml:",
		"id order.refunded does not match the file name order-refunded.yaml",
		"invalid: .semar/fixtures/broken.yaml:",
		"retries")
}

func TestFixtureListWithAnEnvironmentShowsHealthAndPolicy(t *testing.T) {
	r := fixtureRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	r.approveFixture(t)

	res := r.must(t, cli.ExitOK, "fixture", "list", "--env", "local")
	contains(t, "the list", res.stdout, "not checked on local")

	r.must(t, cli.ExitOK, "fixture", "check", "order.paid", "--env", "local", "--auth-profile", "support-agent")
	res = r.must(t, cli.ExitOK, "fixture", "list", "--env", "local")
	contains(t, "the list", res.stdout, "healthy on local at 2026-11-14T10:")

	// A step that the environment would refuse is listed, so that a person sees it before a run.
	r.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "      mode: test_data\n", "      mode: test_data\n      deny: [{method: POST, path: /v1/payments}]\n", 1)
	})
	res = r.must(t, cli.ExitOK, "fixture", "list", "--env", "local")
	contains(t, "the list", res.stdout, "policy of local: order.paid: create[1]: POST /v1/payments is on the deny list of")
}

func TestFixtureCheckRecordsHealth(t *testing.T) {
	r := fixtureRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	hash := r.approveFixture(t)

	res := r.must(t, cli.ExitOK, "fixture", "check", "--all", "--env", "local", "--auth-profile", "support-agent")
	contains(t, "the check", res.stdout, "order.paid 1.0.0: healthy (find nothing, create made, cleanup done)", "1 fixture: 1 healthy, 0 unhealthy, 0 not fully checked")

	data, err := os.ReadFile(r.path(".semar", "store", "fixture-health", "local", "order.paid.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec fixture.HealthRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Fixture != "order.paid" || rec.Version != "1.0.0" || rec.Hash != hash || rec.Environment != "local" || !rec.Healthy ||
		rec.Find != "nothing" || rec.Create != "made" || rec.Cleanup != "done" || rec.Reason != "" || len(rec.Builds) != 2 {
		t.Errorf("health record = %+v", rec)
	}

	run := r.lastRun(t)
	if run.Purpose != domain.PurposeFixtureCheck || run.ID != rec.Run || len(run.Results) != 0 || run.EndedAt.IsZero() {
		t.Errorf("run = %+v", run)
	}
	if got := r.orderStatus(t, "o-1"); got != sampleapp.StatusCancelled {
		t.Errorf("the check left order o-1 %s", got)
	}
	acts := r.must(t, cli.ExitOK, "activity", "latest")
	contains(t, "the activity", acts.stdout, "fixture order.paid (as check): find step 1", "fixture order.paid (as check): cleanup step 1")
	r.assertNoSecret(t, []string{token, "tok-pay"})

	// A fixture that is healthy lets a run go on.
	r.must(t, cli.ExitOK, "run", "--scenario", fixtureScenario)
}

func TestFixtureCheckNeedsApprovalAndSendsNothingWithout(t *testing.T) {
	r := fixtureRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	res := r.must(t, cli.ExitUndecided, "fixture", "check", "order.paid", "--env", "local", "--auth-profile", "support-agent")
	contains(t, "the check", res.stdout, "order.paid 1.0.0: not fully checked: fixture order.paid is not approved: run semar confirm --fixture order.paid", "1 not fully checked")
	if n := r.reached("/v1"); n != 0 {
		t.Errorf("%d requests reached the services for a fixture nobody approved", n)
	}
	if exists(t, r.path(".semar", "store", "fixture-health")) {
		t.Error("a check that judged nothing stored a health record")
	}
}

func TestFixtureCheckProductionNeedsEnv(t *testing.T) {
	r := fixtureRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	r.approveFixture(t)
	r.rewriteConfig(t, func(s string) string { return strings.Replace(s, "kind: test", "kind: production", 1) })

	res := r.semar(t, t.Context(), "fixture", "check", "--all", "--auth-profile", "support-agent")
	if res.code != cli.ExitError {
		t.Fatalf("exit %d, want %d\n%s%s", res.code, cli.ExitError, res.stdout, res.stderr)
	}
	contains(t, "stderr", res.stderr, `the environment "local" is a production environment; name it with --env local`)
	if n := r.reached("/v1"); n != 0 {
		t.Errorf("%d requests reached a production environment that was not named", n)
	}
	if exists(t, r.path(".semar", "store", "fixture-health")) {
		t.Error("a refused check stored a health record")
	}
}

func TestFixtureCheckOnAReadOnlyEnvironmentSendsNoWrite(t *testing.T) {
	r := fixtureRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	r.approveFixture(t)
	r.rewriteConfig(t, func(s string) string { return strings.Replace(s, "mode: test_data", "mode: read_only", 1) })

	res := r.must(t, cli.ExitUndecided, "fixture", "check", "--all", "--env", "local", "--auth-profile", "support-agent")
	contains(t, "the check", res.stdout, "not fully checked: this environment is read-only", "1 not fully checked")
	if n := r.app.Requests("POST", "") + r.pay.Requests("POST", ""); n != 0 {
		t.Errorf("%d writes were sent to a read-only environment", n)
	}
	if exists(t, r.path(".semar", "store", "fixture-health")) {
		t.Error("a check that could not create stored a health record, which would block the scenarios of a read-only environment")
	}
}

func TestFixtureCheckUsage(t *testing.T) {
	r := newRepo(t, repoOptions{tree: "e2e-fixtures", offline: true})
	for _, args := range [][]string{
		{"fixture", "check", "--env", "local"},
		{"fixture", "check", "order.paid", "--all", "--env", "local"},
	} {
		res := r.semar(t, t.Context(), args...)
		if res.code != cli.ExitError || !strings.Contains(res.stderr, "name the fixtures, or use --all, and not both") {
			t.Errorf("%v: exit %d, stderr %q", args, res.code, res.stderr)
		}
	}
	res := r.semar(t, t.Context(), "fixture", "check", "order.missing", "--env", "local")
	if res.code != cli.ExitError || !strings.Contains(res.stderr, "fixture order.missing was not found") {
		t.Errorf("a fixture that is not there: exit %d, stderr %q", res.code, res.stderr)
	}
}

func TestRunLoadsFixtureSessions(t *testing.T) {
	r := fixtureRepo(t, sampleapp.Options{}, sampleapp.PaymentOptions{})
	r.approveFixture(t)
	// Only the payments profile is missing: the scenario's own session is there.
	t.Setenv(paymentsTokenVar, "")

	res := r.semar(t, t.Context(), "run", "--scenario", fixtureScenario)
	if res.code != cli.ExitError {
		t.Fatalf("exit %d, want %d\n%s%s", res.code, cli.ExitError, res.stdout, res.stderr)
	}
	contains(t, "stderr", res.stderr, "auth profile payments-tester: secret TOKEN")
	if n := r.reached(""); n != 0 {
		t.Errorf("%d requests reached the services before the secret was found missing", n)
	}
	if len(r.runDirs(t)) != 0 {
		t.Errorf("a run was started: %v", r.runDirs(t))
	}
}

func TestRunWithoutGivenReadsNoFixtureFile(t *testing.T) {
	// A catalogue that cannot be read must not stop a run that names no fixture.
	r := newRepo(t, repoOptions{})
	if err := os.MkdirAll(r.path(".semar", "fixtures"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(r.dir, filepath.Join(r.path(".semar", "fixtures"), "loop.yaml")); err != nil {
		t.Skip("no symbolic links:", err)
	}
	r.addScriptAndConfirm(t)
	r.must(t, cli.ExitOK, "run", "--scenario", scenarioID)
}

const ordersOnlyFixture = `apiVersion: semar/v1
kind: Fixture
id: order.created
description: An order of 1000
version: 1.0.0
owner: team-orders
create:
  - request: { service: orders, method: POST, path: /v1/orders, json: { amount: 1000 } }
    captures: [{ name: id, jsonpath: "$.data.id" }]
outputs: [id]
`

// fixturePlan plans a fresh repository that holds a valid fixture and an invalid one, and returns
// the result and the prompt that the planner was given.
func fixturePlan(t *testing.T, approve bool) (result, string) {
	t.Helper()
	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, goodDraft)
	p.write(t, ".semar/fixtures/order.created.yaml", ordersOnlyFixture)
	// The file name does not match its ID, and its create step calls a service that no environment has.
	p.write(t, ".semar/fixtures/order-paid.yaml", orderPaidFixture)
	if approve {
		p.must(t, cli.ExitOK, "confirm", "--fixture", "order.created", "--yes")
	}
	made := p.recordJobs()
	res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release")
	return res, onlyPrompt(t, *made)
}

func TestPlanSkipsInvalidFixtureWithOneLine(t *testing.T) {
	res, prompt := fixturePlan(t, false)
	var lines []string
	for _, l := range strings.Split(res.stderr, "\n") {
		if strings.Contains(l, "fixture file") {
			lines = append(lines, l)
		}
	}
	if len(lines) != 2 {
		t.Fatalf("%d lines about fixture files, want one for each of the 2 problems:\n%s", len(lines), res.stderr)
	}
	for _, l := range lines {
		contains(t, "the line", l, "semar: fixture file .semar/fixtures/order-paid.yaml: ", "; the planner does not see it")
	}
	contains(t, "stderr", res.stderr, "id order.paid does not match the file name order-paid.yaml", "payments is not a service of any environment")

	contains(t, "the prompt", prompt, "## Fixtures you may name", "- `order.created` (not approved yet): An order of 1000. Outputs: `id`.")
	if strings.Contains(prompt, "order.paid") {
		t.Errorf("the prompt names a fixture that is not valid:\n%s", prompt)
	}
}

func TestPlanMarksAnApprovedFixture(t *testing.T) {
	_, prompt := fixturePlan(t, true)
	contains(t, "the prompt", prompt, "- `order.created`: An order of 1000. Outputs: `id`.")
	if strings.Contains(prompt, "not approved yet") {
		t.Errorf("an approved fixture is marked as not approved:\n%s", prompt)
	}
}
