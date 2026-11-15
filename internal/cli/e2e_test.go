package cli_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/keychain"
	"github.com/jancuk/semar/internal/sampleapp"
	"github.com/jancuk/semar/internal/scenario"
	"github.com/jancuk/semar/internal/script"
)

const (
	token      = "tok-alice"
	tokenVar   = "SEMAR_SAMPLE_TOKEN"
	scenarioID = "SHOP-482-01"
)

// repo is a copy of testdata/e2e, served by one sample application.
type repo struct {
	dir    string
	app    *sampleapp.App
	opts   cli.Options
	output []string // every stdout and stderr so far
}

type repoOptions struct {
	app     sampleapp.Options
	wrap    func(http.Handler) http.Handler
	offline bool // keep the committed address, where nothing listens
	// session replaces the session value of the auth profile in semar.yaml.
	session string
}

func newRepo(t *testing.T, o repoOptions) *repo {
	t.Helper()
	r := &repo{dir: t.TempDir()}
	copyTree(t, filepath.Join("..", "..", "testdata", "e2e"), r.dir)
	t.Setenv(tokenVar, token)

	var clock, ids atomic.Int64
	start := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	r.opts = cli.Options{
		Clock: func() time.Time { return start.Add(time.Duration(clock.Add(1)) * time.Second) },
		RunID: func() domain.RunID { return domain.RunID(fmt.Sprintf("r-test-%04d", ids.Add(1))) },
		User:  func() (string, error) { return "tester", nil },
		Stdin: strings.NewReader(""),
		// Home and Keychain keep a test away from the person's own files and keychain.
		Home:     t.TempDir(),
		Keychain: keychain.Memory(),
		// A test never has a status line, whatever its standard error is.
		StderrTerminal: func() bool { return false },
	}
	if o.session != "" {
		r.rewriteConfig(t, func(s string) string {
			return strings.Replace(s, `"Bearer {{secret.TOKEN}}"`, `"`+o.session+`"`, 1)
		})
	}
	if o.offline {
		return r
	}

	r.app = sampleapp.New(o.app)
	var h http.Handler = r.app
	if o.wrap != nil {
		h = o.wrap(r.app)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	r.rewriteConfig(t, func(s string) string { return strings.ReplaceAll(s, "127.0.0.1:9", u.Host) })
	return r
}

func (r *repo) rewriteConfig(t *testing.T, change func(string) string) {
	t.Helper()
	cfg := filepath.Join(r.dir, ".semar", "semar.yaml")
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	changed := change(string(data))
	if changed == string(data) {
		t.Fatal("the change to semar.yaml did not apply")
	}
	if err := os.WriteFile(cfg, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
}

// copyTree copies the committed files and leaves out what a manual run may have written.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		if d.IsDir() {
			if rel == filepath.Join(".semar", "runs") || rel == filepath.Join(".semar", "store") {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(to, rel), 0o700)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, rel), data, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

type result struct {
	code           int
	stdout, stderr string
}

func (r *repo) semar(t *testing.T, ctx context.Context, args ...string) result {
	t.Helper()
	var out, errw bytes.Buffer
	code := cli.RunWith(ctx, append(args, "--dir", r.dir), &out, &errw, r.opts)
	r.output = append(r.output, out.String(), errw.String())
	return result{code: code, stdout: out.String(), stderr: errw.String()}
}

func (r *repo) must(t *testing.T, want int, args ...string) result {
	t.Helper()
	res := r.semar(t, t.Context(), args...)
	if res.code != want {
		t.Fatalf("semar %s: exit %d, want %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), res.code, want, res.stdout, res.stderr)
	}
	return res
}

func (r *repo) path(parts ...string) string {
	return filepath.Join(append([]string{r.dir}, parts...)...)
}

func (r *repo) scriptFile() string { return r.path("scripts", scenarioID+".yaml") }

func (r *repo) addScriptAndConfirm(t *testing.T) {
	t.Helper()
	r.must(t, cli.ExitOK, "script", "add", r.scriptFile())
	r.must(t, cli.ExitOK, "confirm", scenarioID, "--yes")
}

func (r *repo) runDirs(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(r.path(".semar", "runs"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	return dirs
}

func (r *repo) latestRun(t *testing.T) string {
	t.Helper()
	dirs := r.runDirs(t)
	if len(dirs) == 0 {
		t.Fatal("no run directory")
	}
	return r.path(".semar", "runs", dirs[len(dirs)-1])
}

func exists(t *testing.T, p string) bool {
	t.Helper()
	_, err := os.Stat(p)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return err == nil
}

// forms are the ways a secret can reach a file or a stream: as written, in base64 (both
// alphabets, with and without padding), and percent-encoded.
func forms(secret string) []string {
	b := []byte(secret)
	return []string{
		secret,
		base64.StdEncoding.EncodeToString(b),
		base64.RawStdEncoding.EncodeToString(b),
		base64.URLEncoding.EncodeToString(b),
		base64.RawURLEncoding.EncodeToString(b),
		url.QueryEscape(secret),
		url.PathEscape(secret),
	}
}

// assertNoSecret fails when any file under .semar, except the listed ones below it, or any output
// stream so far holds a secret in any of its forms. The scan needs a repo that ran something to be meaningful.
func (r *repo) assertNoSecret(t *testing.T, secrets []string, skip ...string) {
	t.Helper()
	var needles []string
	for _, s := range secrets {
		needles = append(needles, forms(s)...)
	}
	root := r.path(".semar")
	files := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if slices.Contains(skip, filepath.ToSlash(rel)) {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files++
		for _, n := range needles {
			if bytes.Contains(data, []byte(n)) {
				t.Errorf("%s holds a secret as %q", p, n)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Error("the scan found no file under .semar")
	}
	for _, out := range r.output {
		for _, n := range needles {
			if strings.Contains(out, n) {
				t.Errorf("an output stream holds a secret as %q:\n%s", n, out)
			}
		}
	}
}

func contains(t *testing.T, what, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("%s lacks %q:\n%s", what, w, got)
		}
	}
}

func TestRunEndToEnd(t *testing.T) {
	r := newRepo(t, repoOptions{})

	r.must(t, cli.ExitOK, "script", "add", r.scriptFile())

	first := r.must(t, cli.ExitUndecided, "run", "--scenario", scenarioID)
	contains(t, "the first summary", first.stdout, "inconclusive", "not confirmed")

	confirmed := r.must(t, cli.ExitOK, "confirm", scenarioID, "--yes")
	contains(t, "the confirmation", confirmed.stdout, "refund-too-much", "diff", "local, unverified identity")

	second := r.must(t, cli.ExitOK, "run", "--scenario", scenarioID)
	contains(t, "the second summary", second.stdout, "passed", "report.md")

	dir := r.latestRun(t)
	for _, name := range []string{"run.json", "report.md", "activity.jsonl"} {
		if !exists(t, filepath.Join(dir, name)) {
			t.Errorf("the run directory has no %s", name)
		}
	}
	exchanges, err := os.ReadDir(filepath.Join(dir, "exchanges", scenarioID))
	if err != nil {
		t.Fatal(err)
	}
	if len(exchanges) != 3 {
		t.Errorf("%d exchange files, want 3", len(exchanges))
	}

	writes := r.must(t, cli.ExitOK, "activity", "latest", "--writes")
	lines := strings.Split(strings.TrimSpace(writes.stdout), "\n")
	if len(lines) != 3 { // the header and two writes
		t.Fatalf("activity --writes printed:\n%s", writes.stdout)
	}
	contains(t, "the create line", lines[1], "create", "POST /v1/orders", "201")
	contains(t, "the refund line", lines[2], "create", "/refunds", "422")

	// Review Focus: no secret reaches the disk or the terminal.
	r.assertNoSecret(t, []string{token})
}

func TestRunFindsTheSeededDefect(t *testing.T) {
	r := newRepo(t, repoOptions{app: sampleapp.Options{Defects: map[sampleapp.Defect]bool{sampleapp.RefundOverTotal: true}}})
	r.addScriptAndConfirm(t)

	res := r.must(t, cli.ExitFailed, "run", "--scenario", scenarioID)
	contains(t, "the summary", res.stdout, "failed")
	report, err := os.ReadFile(filepath.Join(r.latestRun(t), "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "report.md", string(report),
		"refund-too-much", "expected status 422", "observed status 201", "source ticket",
		"Source: ticket, cite: SHOP-482: a refund above the order total must be rejected")
	r.assertNoSecret(t, []string{token})
}

func TestRunWithNoScenario(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	searched := r.path(".semar", "scenarios")

	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"unknown ID", []string{"run", "--scenario", "NOPE-1"}, []string{"NOPE-1", searched}},
		{"ID that leaves the directory", []string{"run", "--scenario", "../SHOP-482-01"}, []string{"invalid scenario ID"}},
		{"neither --scenario nor --all", []string{"run"}, []string{"--scenario", "--all"}},
		{"both --scenario and --all", []string{"run", "--all", "--scenario", scenarioID}, []string{"--scenario", "--all"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errw bytes.Buffer
			code := cli.Run(t.Context(), append(tc.args, "--dir", r.dir), &out, &errw)
			if code != cli.ExitError {
				t.Fatalf("exit %d, want %d; stderr %q", code, cli.ExitError, errw.String())
			}
			contains(t, "stderr", errw.String(), tc.want...)
		})
	}

	t.Run("no scenario file", func(t *testing.T) {
		if err := os.RemoveAll(searched); err != nil {
			t.Fatal(err)
		}
		var out, errw bytes.Buffer
		if code := cli.Run(t.Context(), []string{"run", "--all", "--dir", r.dir}, &out, &errw); code != cli.ExitUndecided {
			t.Fatalf("exit %d, want %d; stderr %q", code, cli.ExitUndecided, errw.String())
		}
		contains(t, "stdout", out.String(), "no scenario ran")
	})
}

func TestInterruptedRunIsVisible(t *testing.T) { // Review Focus 4
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var refunds atomic.Int64
	// The sample application delays only a refund it accepts, so the refund above the total must be accepted to be slow.
	r := newRepo(t, repoOptions{
		app: sampleapp.Options{
			Defects: map[sampleapp.Defect]bool{sampleapp.SlowRefund: true, sampleapp.RefundOverTotal: true},
			Delay:   2 * time.Second,
		},
		// The run is cancelled while the refund is in flight, without a timer that a slow machine could beat.
		wrap: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if strings.HasSuffix(req.URL.Path, "/refunds") {
					refunds.Add(1) // counted before cancel, so that no scheduler can make the test read 0
					cancel()
				}
				next.ServeHTTP(w, req)
			})
		},
	})
	r.addScriptAndConfirm(t)

	res := r.semar(t, ctx, "run", "--scenario", scenarioID)
	if res.code != cli.ExitUndecided {
		t.Fatalf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", res.code, cli.ExitUndecided, res.stdout, res.stderr)
	}
	contains(t, "the output", res.stdout+res.stderr, "interrupted")
	dir := r.latestRun(t)
	if exists(t, filepath.Join(dir, "run.json")) {
		t.Error("an interrupted run has a run.json")
	}
	report, err := os.ReadFile(filepath.Join(dir, "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "report.md", string(report), "This run did not finish")

	act := r.must(t, cli.ExitOK, "activity", "latest")
	contains(t, "activity", act.stdout, "the run did not finish (it was interrupted or stopped by an error)", "/refunds")
	if n := refunds.Load(); n != 1 {
		t.Errorf("the refund was sent %d times, want 1", n)
	}
	r.assertNoSecret(t, []string{token})
}

func TestScriptAddRefusesAScriptThatDoesNotFit(t *testing.T) {
	cases := []struct {
		name   string
		change func(s string) string
		want   func(t *testing.T, r *repo, file string) []string
	}{
		{
			name: "another method",
			change: func(s string) string {
				return strings.Replace(s, "method: POST, path: \"/v1/orders/{{order_id}}/refunds\"", "method: PUT, path: \"/v1/orders/{{order_id}}/refunds\"", 1)
			},
			want: func(t *testing.T, r *repo, file string) []string {
				sc, err := scenario.Load(r.path(".semar", "scenarios", "SHOP-482", scenarioID+".yaml"))
				if err != nil {
					t.Fatal(err)
				}
				s, err := script.Load(file)
				if err != nil {
					t.Fatal(err)
				}
				var msgs []string
				for _, p := range script.Validate(sc, s) {
					msgs = append(msgs, p.Message)
				}
				if len(msgs) == 0 {
					t.Fatal("the changed script has no problem")
				}
				return msgs
			},
		},
		{
			name:   "another expectation hash",
			change: func(s string) string { return strings.Replace(s, "8e44c6f1", "0000c6f1", 1) },
			want: func(*testing.T, *repo, string) []string {
				return []string{"0000c6f1", "8e44c6f1", "differs"}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRepo(t, repoOptions{offline: true})
			data, err := os.ReadFile(r.scriptFile())
			if err != nil {
				t.Fatal(err)
			}
			changed := tc.change(string(data))
			if changed == string(data) {
				t.Fatal("the change did not apply")
			}
			file := r.path("scripts", "changed.yaml")
			if err := os.WriteFile(file, []byte(changed), 0o600); err != nil {
				t.Fatal(err)
			}
			res := r.must(t, cli.ExitError, "script", "add", file)
			contains(t, "stderr", res.stderr, tc.want(t, r, file)...)
			if exists(t, r.path(".semar", "store", "scripts")) {
				t.Error("a script was stored")
			}
		})
	}
}

func TestConfirmWithoutYes(t *testing.T) {
	cases := []struct {
		name     string
		terminal bool
		answer   string
		want     int
		stored   bool
	}{
		{"no terminal", false, "yes\n", cli.ExitUndecided, false},
		{"answer no", true, "n\n", cli.ExitUndecided, false},
		{"no answer", true, "", cli.ExitUndecided, false},
		{"answer yes", true, "yes\n", cli.ExitOK, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRepo(t, repoOptions{offline: true})
			r.opts.Stdin = strings.NewReader(tc.answer)
			r.opts.Terminal = func() bool { return tc.terminal }
			res := r.must(t, tc.want, "confirm", scenarioID)
			contains(t, "stdout", res.stdout, "refund-too-much", "diff")
			if got := exists(t, r.path(".semar", "store", "confirmations")); got != tc.stored {
				t.Errorf("stored = %v, want %v", got, tc.stored)
			}
		})
	}
}

func TestMissingSecretSendsNothing(t *testing.T) {
	r := newRepo(t, repoOptions{})
	r.addScriptAndConfirm(t)
	t.Setenv(tokenVar, "")

	res := r.must(t, cli.ExitError, "run", "--scenario", scenarioID)
	contains(t, "stderr", res.stderr, tokenVar)
	if n := r.app.Requests("", "/"); n != 0 {
		t.Errorf("%d requests reached the service", n)
	}
	if dirs := r.runDirs(t); len(dirs) != 0 {
		t.Errorf("run directories %v were created", dirs)
	}
}

func TestRunAgainstAServiceThatIsNotThere(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	r.must(t, cli.ExitOK, "script", "add", r.scriptFile())
	res := r.must(t, cli.ExitUndecided, "run", "--all")
	contains(t, "stdout", res.stdout, "blocked", "the build of orders could not be read")
}

func TestDuplicatedScenarioID(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	original := r.path(".semar", "scenarios", "SHOP-482", scenarioID+".yaml")
	copied := r.path(".semar", "scenarios", "OTHER", scenarioID+".yaml")
	data, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(copied), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copied, data, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"run", "--scenario", scenarioID}, {"run", "--all"}, {"confirm", scenarioID, "--yes"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			res := r.must(t, cli.ExitError, args...)
			contains(t, "stderr", res.stderr, original, copied)
		})
	}
}

func TestActivityFilters(t *testing.T) {
	r := newRepo(t, repoOptions{})
	r.must(t, cli.ExitError, "activity", "latest")
	r.addScriptAndConfirm(t)
	r.must(t, cli.ExitOK, "run", "--scenario", scenarioID)

	cases := []struct {
		name string
		args []string
		rows int
		want string
	}{
		{"everything", nil, 3, ""},
		{"setup pass", []string{"--pass", "setup"}, 1, "POST /v1/orders"},
		{"scenario pass and writes", []string{"--pass", "scenario", "--writes"}, 1, "/refunds"},
		{"another scenario", []string{"--scenario", "SHOP-482-02"}, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := r.must(t, cli.ExitOK, append([]string{"activity", "latest"}, tc.args...)...)
			lines := strings.Split(strings.TrimSpace(res.stdout), "\n")
			if len(lines)-1 != tc.rows {
				t.Fatalf("%d rows, want %d:\n%s", len(lines)-1, tc.rows, res.stdout)
			}
			if tc.want != "" {
				contains(t, "the row", lines[1], tc.want)
			}
		})
	}
	for _, args := range [][]string{{"activity", "latest", "--pass", "nope"}, {"activity", "r-nope"}, {"activity", "../runs"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			r.must(t, cli.ExitError, args...)
		})
	}
}

func TestRepeatedScenarioRunsOnce(t *testing.T) {
	r := newRepo(t, repoOptions{})
	r.addScriptAndConfirm(t)

	res := r.must(t, cli.ExitOK, "run", "--scenario", scenarioID, "--scenario", scenarioID)
	if n := strings.Count(res.stdout, scenarioID+" "); n != 1 {
		t.Errorf("the summary names the scenario %d times, want 1:\n%s", n, res.stdout)
	}
	data, err := os.ReadFile(filepath.Join(r.latestRun(t), "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var run domain.Run
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatal(err)
	}
	if len(run.Results) != 1 {
		t.Errorf("run.json has %d results, want 1", len(run.Results))
	}
	if n := r.app.Requests("POST", "/v1/orders"); n != 2 { // the order and the refund
		t.Errorf("%d writes reached the service, want 2", n)
	}
}

func TestACloseThatFailsLeavesAReportThatSaysSo(t *testing.T) {
	var r *repo
	// A directory where run.json must go makes the close fail, as a full disk would.
	failClose := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if err := os.MkdirAll(r.path(".semar", "runs", "r-test-0001", "run.json"), 0o700); err != nil {
				t.Error(err)
			}
			next.ServeHTTP(w, req)
		})
	}
	r = newRepo(t, repoOptions{wrap: failClose})
	r.addScriptAndConfirm(t)

	res := r.must(t, cli.ExitError, "run", "--scenario", scenarioID)
	if strings.Contains(res.stdout, "passed") {
		t.Errorf("the summary of a run that did not close says passed:\n%s", res.stdout)
	}
	report, err := os.ReadFile(filepath.Join(r.latestRun(t), "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "report.md", string(report), "This run did not finish")
	if strings.Contains(string(report), "Outcomes: 1 passed") {
		t.Errorf("report.md of a run that did not close says it passed:\n%s", report)
	}
	r.assertNoSecret(t, []string{token})
}

func TestASessionValueWrittenInThePlaceIsRedacted(t *testing.T) {
	const literal = "hardcoded-literal-9f3"
	r := newRepo(t, repoOptions{
		session: "Bearer " + literal,
		// The service echoes the header in full and the token alone, in the body and in a header.
		wrap: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/version" {
					next.ServeHTTP(w, req)
					return
				}
				auth := req.Header.Get("Authorization")
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Echo", auth)
				_ = json.NewEncoder(w).Encode(map[string]any{"echo": auth, "token": strings.TrimPrefix(auth, "Bearer ")})
			})
		},
	})
	r.addScriptAndConfirm(t)

	r.must(t, cli.ExitUndecided, "run", "--scenario", scenarioID)
	if dir := r.latestRun(t); !exists(t, filepath.Join(dir, "exchanges", scenarioID)) {
		t.Fatal("the run recorded no exchange, so the scan below proves nothing")
	}
	r.assertNoSecret(t, []string{literal, "Bearer " + literal}, "semar.yaml")
}

func TestConfirmListsTheWholeOperandAndNothingHostile(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	scenarioFile := r.path(".semar", "scenarios", "SHOP-482", scenarioID+".yaml")
	head, tail := strings.Repeat("A", 150), strings.Repeat("C", 150)
	data, err := os.ReadFile(scenarioFile)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(data), "equals: REFUND_EXCEEDS_TOTAL", `equals: "`+head+`\u001b[31m\u202eB\u200b\t`+tail+`"`, 1)
	if changed == string(data) {
		t.Fatal("the change did not apply")
	}
	if err := os.WriteFile(scenarioFile, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}

	res := r.must(t, cli.ExitOK, "confirm", scenarioID, "--yes")
	contains(t, "the listing", res.stdout, head+`\x1b[31mB`+"\t"+tail)
	for _, hostile := range []string{"\x1b", "\u202e", "\u200b"} {
		if strings.Contains(res.stdout, hostile) {
			t.Errorf("the listing holds the character %+q", hostile)
		}
	}
}

func TestConfirmRefusesAnEmptyName(t *testing.T) {
	for _, name := range []string{"", "  "} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			r := newRepo(t, repoOptions{offline: true})
			r.opts.User = func() (string, error) { return name, nil }
			res := r.must(t, cli.ExitError, "confirm", scenarioID, "--yes")
			contains(t, "stderr", res.stderr, "the confirmer has no name; set git config user.name")
			if exists(t, r.path(".semar", "store")) {
				t.Error("something was stored")
			}
		})
	}
}

func TestScenarioFilesOtherThanRegularYAMLAreNotSkipped(t *testing.T) {
	dir := func(r *repo) string { return r.path(".semar", "scenarios", "SHOP-482") }

	t.Run("a symlinked scenario is an error that names it", func(t *testing.T) {
		for _, ext := range []string{".yaml", ".yml"} {
			r := newRepo(t, repoOptions{offline: true})
			link := filepath.Join(dir(r), "LINK-1"+ext)
			if err := os.Symlink(filepath.Join(dir(r), scenarioID+".yaml"), link); err != nil {
				t.Skipf("no symlinks here: %v", err)
			}
			for _, args := range [][]string{{"run", "--all"}, {"confirm", scenarioID, "--yes"}} {
				res := r.must(t, cli.ExitError, args...)
				contains(t, "stderr", res.stderr, link)
			}
		}
	})

	t.Run("a .yml scenario is loaded like a .yaml one", func(t *testing.T) {
		r := newRepo(t, repoOptions{offline: true})
		if err := os.Rename(filepath.Join(dir(r), scenarioID+".yaml"), filepath.Join(dir(r), scenarioID+".yml")); err != nil {
			t.Fatal(err)
		}
		res := r.must(t, cli.ExitOK, "confirm", scenarioID, "--yes")
		contains(t, "stdout", res.stdout, "confirmed scenario "+scenarioID)
	})

	t.Run("the same ID in .yaml and .yml is a duplicate", func(t *testing.T) {
		r := newRepo(t, repoOptions{offline: true})
		yaml, yml := filepath.Join(dir(r), scenarioID+".yaml"), filepath.Join(dir(r), scenarioID+".yml")
		data, err := os.ReadFile(yaml)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(yml, data, 0o600); err != nil {
			t.Fatal(err)
		}
		res := r.must(t, cli.ExitError, "run", "--all")
		contains(t, "stderr", res.stderr, yaml, yml, "more than one file")
	})
}

func TestCommandsStopWhenTheirContextIsDone(t *testing.T) {
	r := newRepo(t, repoOptions{})
	r.addScriptAndConfirm(t)
	r.must(t, cli.ExitOK, "run", "--scenario", scenarioID)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	t.Run("activity", func(t *testing.T) {
		res := r.semar(t, ctx, "activity", "latest")
		if res.code != cli.ExitError {
			t.Errorf("exit %d, want %d; stdout %q", res.code, cli.ExitError, res.stdout)
		}
		contains(t, "stderr", res.stderr, "context canceled")
	})

	t.Run("script add", func(t *testing.T) {
		fresh := newRepo(t, repoOptions{offline: true})
		res := fresh.semar(t, ctx, "script", "add", fresh.scriptFile())
		if res.code != cli.ExitError {
			t.Errorf("exit %d, want %d; stdout %q", res.code, cli.ExitError, res.stdout)
		}
		if exists(t, fresh.path(".semar", "store")) {
			t.Error("a script was stored")
		}
	})
}

func TestRunWithADeclaredBuild(t *testing.T) {
	addServices := func(s string) string {
		return strings.Replace(s, "    services:\n", "    services:\n      - name: billing\n        baseUrl: http://127.0.0.1:9\n      - name: ledger\n        baseUrl: http://127.0.0.1:9\n", 1)
	}
	cases := []struct {
		name   string
		config func(string) string
		args   []string
		want   string
	}{
		{"a service with a probe", nil, []string{"--build", "orders=abcdef0"}, "service orders has a build probe"},
		{"the bare form when every service has a probe", nil, []string{"--build", "abcdef0"}, "every service of the environment has a build probe"},
		{"the bare form with two services with no probe", addServices, []string{"--build", "abcdef0"}, "billing, ledger have no build probe"},
		{"an unknown service", addServices, []string{"--build", "payments=abcdef0"}, `no service "payments"`},
		{"the same service twice", addServices, []string{"--build", "billing=abcdef0", "--build", "billing=abcdef1"}, "names service billing more than once"},
		{"a commit that is not hex", addServices, []string{"--build", "billing=release"}, "7 to 40 lower-case hexadecimal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRepo(t, repoOptions{offline: true})
			if tc.config != nil {
				r.rewriteConfig(t, tc.config)
			}
			res := r.must(t, cli.ExitError, append([]string{"run", "--all"}, tc.args...)...)
			contains(t, "stderr", res.stderr, tc.want)
			if dirs := r.runDirs(t); len(dirs) != 0 {
				t.Errorf("a usage error created runs %v", dirs)
			}
		})
	}

	t.Run("a service with no probe passes with a declared build", func(t *testing.T) {
		r := newRepo(t, repoOptions{})
		r.rewriteConfig(t, func(s string) string {
			i, j := strings.Index(s, "        build:\n"), strings.Index(s, "authProfiles:")
			return s[:i] + s[j:]
		})
		r.addScriptAndConfirm(t)
		res := r.must(t, cli.ExitUndecided, "run", "--all")
		contains(t, "a run with no build", res.stdout, "no build was recorded")
		res = r.must(t, cli.ExitOK, "run", "--all", "--build", "0123456789abcdef")
		contains(t, "a run with a declared build", res.stdout, "passed", "build: orders 0123456789abcdef (declared")
		data, err := os.ReadFile(filepath.Join(r.latestRun(t), "run.json"))
		if err != nil {
			t.Fatal(err)
		}
		var run domain.Run
		if err := json.Unmarshal(data, &run); err != nil {
			t.Fatal(err)
		}
		want := []domain.BuildRef{{Service: "orders", Commit: "0123456789abcdef", Declared: true}}
		if !slices.Equal(run.BuildsStart, want) || !slices.Equal(run.BuildsEnd, want) {
			t.Errorf("builds %+v and %+v, want %+v at the start and the end", run.BuildsStart, run.BuildsEnd, want)
		}
		if n := r.app.Requests("GET", "/version"); n != 0 {
			t.Errorf("the service was asked for its build %d times", n)
		}
	})
}
