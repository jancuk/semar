package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/domain"
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
	cfg := filepath.Join(r.dir, ".semar", "semar.yaml")
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte(strings.ReplaceAll(string(data), "127.0.0.1:9", u.Host)), 0o600); err != nil {
		t.Fatal(err)
	}
	return r
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
	for _, root := range []string{r.path(".semar", "runs"), r.path(".semar", "store")} {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if bytes.Contains(data, []byte(token)) {
				t.Errorf("%s holds the session token", p)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, out := range r.output {
		if strings.Contains(out, token) {
			t.Errorf("the output holds the session token:\n%s", out)
		}
	}
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
	contains(t, "activity", act.stdout, "the run was interrupted", "/refunds")
	if n := r.app.Requests("POST", "/v1/orders/o-1/refunds"); n != 1 {
		t.Errorf("the refund was sent %d times, want 1", n)
	}
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
