package cli_test

import (
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/keychain"
	"github.com/jancuk/semar/internal/sampleapp"
	"github.com/jancuk/semar/internal/vcs/gittest"
)

const trialTree = "e2e-trial"

// trialRepo is a git repository built from testdata/e2e-trial: a service source, an API document,
// and a fixture, with orders and payments on 127.0.0.1. Every request that reaches orders is logged,
// and a read of a summary answers 500, as a service that is broken for it would.
type trialRepo struct {
	*planRepo
	mu  sync.Mutex
	log []string
}

// readTree reads the files below dir as the content of a commit.
func readTree(t *testing.T, dir string) map[string]*string {
	t.Helper()
	files := map[string]*string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = gittest.S(string(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func newTrialRepo(t *testing.T) *trialRepo {
	t.Helper()
	root := filepath.Join("..", "..", "testdata", trialTree)
	g := gittest.New(t)
	g.Commit("initial import", readTree(t, filepath.Join(root, "main")))
	g.Git("checkout", "--quiet", "-b", "release")
	g.Commit("SHOP-482 return the status of a paid order", readTree(t, filepath.Join(root, "release")))
	t.Setenv(tokenVar, token)
	t.Setenv(paymentsTokenVar, "tok-pay")

	tr := &trialRepo{}
	app := sampleapp.New(sampleapp.Options{})
	orders := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tr.mu.Lock()
		tr.log = append(tr.log, r.Method+" "+r.URL.Path)
		tr.mu.Unlock()
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/summary") {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"code":"SUMMARY_BROKEN"}}`))
			return
		}
		app.ServeHTTP(w, r)
	}))
	t.Cleanup(orders.Close)
	pay := sampleapp.NewPayments(app, sampleapp.PaymentOptions{})
	payments := httptest.NewServer(pay)
	t.Cleanup(payments.Close)
	ou, err := url.Parse(orders.URL)
	if err != nil {
		t.Fatal(err)
	}
	pu, err := url.Parse(payments.URL)
	if err != nil {
		t.Fatal(err)
	}

	var clock, ids atomic.Int64
	start := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	p := &planRepo{git: g, host: ou.Host, temp: t.TempDir()}
	p.repo = &repo{dir: g.Dir, app: app, pay: pay, ordersURL: orders.URL, paymentsURL: payments.URL, opts: cli.Options{
		Clock:          func() time.Time { return start.Add(time.Duration(clock.Add(1)) * time.Second) },
		RunID:          func() domain.RunID { return domain.RunID(fmt.Sprintf("r-test-%04d", ids.Add(1))) },
		User:           func() (string, error) { return "tester", nil },
		Stdin:          strings.NewReader(""),
		TempRoot:       p.temp,
		Home:           t.TempDir(),
		Keychain:       keychain.Memory(),
		StderrTerminal: func() bool { return false },
	}}
	copyTree(t, filepath.Join(root, "work"), g.Dir)
	p.rewriteConfig(t, strings.NewReplacer("127.0.0.1:9", ou.Host, "127.0.0.1:7", pu.Host).Replace)
	tr.planRepo = p

	p.must(t, cli.ExitOK, "confirm", "--fixture", "order.paid", "--yes")
	p.seedPaidOrder(t)
	return tr
}

// requests are the requests that reached orders since the mark.
func (r *trialRepo) requests(from int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.log[from:]...)
}

func (r *trialRepo) mark() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.log)
}

func TestTrialEndToEnd(t *testing.T) {
	r := newTrialRepo(t)
	made := r.recordJobs()
	before := r.mark()
	writes := r.app.Requests("POST", "")

	res := r.must(t, cli.ExitOK, "plan", "--diff", "main..release", "--auth-profile", "support-agent")

	// The plan took two drafts: the typo of the first came back from the trial.
	contains(t, "stdout", res.stdout, "tried: 2 of 3 steps, 1 problems returned, 1 for a second look\n")
	if len(*made) != 1 || len((*made)[0].Jobs()) != 2 {
		t.Fatalf("the plan built %d providers; want one that ran twice", len(*made))
	}
	first, second := (*made)[0].Jobs()[0].Prompt, (*made)[0].Jobs()[1].Prompt
	if strings.Contains(first, "trial:") {
		t.Errorf("the first prompt has a trial problem:\n%s", first)
	}
	contains(t, "the second prompt", second,
		"trial: the environment answered 404 for GET /v1/order/{id}, and no API document has this route: the route is likely wrong")
	for _, absent := range []string{"answered 500", "the service failed", "SUMMARY_BROKEN", "NOT_FOUND"} {
		if strings.Contains(second, absent) {
			t.Errorf("the second prompt holds %q, which no problem of the trial may carry", absent)
		}
	}

	// Only reads reached the service: find, the typo, and the summary on the first try, the same
	// on the second with the repaired path, and one read of the build.
	got := r.requests(before)
	want := []string{
		"GET /version",
		"GET /v1/orders", "GET /v1/order/o-1", "GET /v1/orders/o-1/summary",
		"GET /v1/orders", "GET /v1/orders/o-1", "GET /v1/orders/o-1/summary",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the service received:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if n := r.app.Requests("POST", "") - writes; n != 0 {
		t.Errorf("the service received %d writes during the plan", n)
	}

	md := r.planMD(t)
	contains(t, "plan.md", md,
		"## Trial run\n\nCode sent the read-only steps once to uat before the plan was written. The environment ran orders ",
		"A trial result is evidence about a request. It is not a test result, and no expected result was compared. Run r-test-",
		"### SHOP-482-01 A paid order can be read\n\n",
		"- given order (order.paid): find found a record\n",
		"- read-order: GET /v1/orders/{id} answered 200, capture status found\n",
		"- summary: GET /v1/orders/{id}/summary answered 500",
		"- refund: not tried: writes\n",
		"### Needs a second look\n\n- SHOP-482-01, step summary: GET /v1/orders/{id}/summary answered 500: the service failed; this may be a defect\n",
		"Attempts: 2 of 3",
		"The read-only requests were tried once. The others were not sent before the first run.")
	if strings.Contains(md, "/v1/order/{id}") {
		t.Errorf("plan.md holds the typo of the first draft:\n%s", md)
	}
	if strings.Contains(md, "o-1") || strings.Contains(md, "SUMMARY_BROKEN") {
		t.Errorf("plan.md holds a value of a response:\n%s", md)
	}
	stored, err := filepath.Glob(r.path(".semar", "store", "scripts", "SHOP-482-01", "*.yaml"))
	if err != nil || len(stored) != 1 {
		t.Fatalf("stored scripts %v, %v; want 1", stored, err)
	}
	sc, err := os.ReadFile(stored[0])
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "the script", string(sc), "/v1/orders/{{order.id}}")
	if strings.Contains(string(sc), "/v1/order/") {
		t.Errorf("the script holds the path of the first draft:\n%s", sc)
	}

	// The trial run is a run of its own, with no result, and its requests are in the trial pass.
	var trialRun domain.Run
	for _, run := range r.trialRuns(t) {
		if run.Purpose == domain.PurposeTrial {
			trialRun = run
		}
	}
	if trialRun.ID == "" || len(trialRun.Results) != 0 || len(trialRun.BuildsStart) != 1 {
		t.Fatalf("trial run = %+v", trialRun)
	}
	acts := r.must(t, cli.ExitOK, "activity", string(trialRun.ID), "--pass", "trial")
	contains(t, "the activity", acts.stdout, "GET /v1/orders", "GET /v1/order/o-1", "GET /v1/orders/o-1/summary")
	if strings.Contains(acts.stdout, "POST") {
		t.Errorf("the activity of the trial lists a write:\n%s", acts.stdout)
	}
	r.assertNoSecret(t, []string{token, "tok-pay"})
	r.assertNoTempCopy(t)
}

func TestTrialOffSendsNothing(t *testing.T) {
	r := newTrialRepo(t)
	made := r.recordJobs()
	before := r.mark()

	res := r.must(t, cli.ExitOK, "plan", "--diff", "main..release", "--auth-profile", "support-agent", "--no-trial")

	if n := len(r.requests(before)); n != 0 {
		t.Errorf("the service received %d requests: %v", n, r.requests(before))
	}
	if len(*made) != 1 || len((*made)[0].Jobs()) != 1 {
		t.Fatalf("the plan built %d providers; want one that ran once", len(*made))
	}
	if strings.Contains(res.stdout, "tried:") || strings.Contains(res.stderr, "trial run") {
		t.Errorf("the output mentions the trial:\nstdout:\n%s\nstderr:\n%s", res.stdout, res.stderr)
	}
	md := r.planMD(t)
	contains(t, "plan.md", md, "## Trial run\n\nThe trial run was turned off with --no-trial.\n",
		"The requests were drafted from the source and were not sent before the first run.")
	// The first draft is accepted as it is, with its typo, because nothing met the environment.
	contains(t, "plan.md", md, "/v1/order/{id}")
	if runs := r.trialRuns(t); len(runs) != 0 {
		t.Errorf("a trial run was made: %+v", runs)
	}
}
