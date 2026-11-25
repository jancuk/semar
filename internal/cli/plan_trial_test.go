package cli_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/sampleapp"
)

// listDraft is goodDraft with a read that needs no record: GET /v1/orders. The trial sends it and
// no other step, because the rest write or need what a write captures.
var listDraft = strings.Replace(goodDraft,
	`"hint": "/v1/orders/{id}", "path": "/v1/orders/{{order_id}}"`, `"hint": "/v1/orders", "path": "/v1/orders"`, 1)

func (p *planRepo) planMD(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(p.path(".semar", "plans", p.head(t)[:12], "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// trialRuns are the runs of .semar/runs, read from run.json.
func (p *planRepo) trialRuns(t *testing.T) []domain.Run {
	t.Helper()
	var runs []domain.Run
	for _, d := range p.runDirs(t) {
		data, err := os.ReadFile(p.path(".semar", "runs", d, "run.json"))
		if err != nil {
			t.Fatal(err)
		}
		var r domain.Run
		if err := json.Unmarshal(data, &r); err != nil {
			t.Fatal(err)
		}
		runs = append(runs, r)
	}
	return runs
}

func TestTrialSkipLines(t *testing.T) {
	const prefix = "semar: trial run skipped: "
	for _, tc := range []struct {
		name   string
		args   []string
		change func(t *testing.T, p *planRepo)
		line   string // the line on standard error, or "" for none
		md     string // what plan.md says
		sums   string // the line on standard output, or ""
	}{
		{
			name: "no-trial", args: []string{"--no-trial"},
			md: "The trial run was turned off with --no-trial.",
		},
		{
			name: "plan.trial false",
			change: func(t *testing.T, p *planRepo) {
				p.rewriteConfig(t, func(s string) string { return s + "plan:\n  trial: false\n" })
			},
			md: "The trial run was turned off by plan.trial in semar.yaml.",
		},
		{
			name: "production",
			change: func(t *testing.T, p *planRepo) {
				p.rewriteConfig(t, func(s string) string { return strings.Replace(s, "kind: test", "kind: production", 1) })
			},
			line: prefix + "uat is a production environment; name it with --env to try the reads against it\n",
			md:   "The trial run was skipped: uat is a production environment; name it with --env to try the reads against it.",
			sums: "trial: skipped\n",
		},
		{
			name: "no account",
			change: func(t *testing.T, p *planRepo) {
				t.Setenv(planTokenEnv, "")
				if err := os.Unsetenv(planTokenEnv); err != nil {
					t.Fatal(err)
				}
			},
			line: prefix + "no account for default: auth profile default: secret TOKEN: ",
			md:   "The trial run was skipped: no account for default: ",
			sums: "trial: skipped\n",
		},
		{
			// The profile that has no account is the one of the fixture's service, not the plan's.
			name: "no account for a fixture service",
			change: func(t *testing.T, p *planRepo) {
				p.rewriteConfig(t, func(s string) string {
					s = strings.Replace(s, "        baseUrl: http://"+p.host+"\n", "        baseUrl: http://"+p.host+"\n        authProfile: payer\n", 1)
					return strings.Replace(s, "authProfiles:\n", "authProfiles:\n  payer:\n    session: {header: Authorization, value: \"Bearer {{secret.TOKEN}}\"}\n    secrets: {TOKEN: \"env:SEMAR_TEST_NO_PAYER_TOKEN\"}\n", 1)
				})
				p.write(t, ".semar/fixtures/order.open.yaml", `apiVersion: semar/v1
kind: Fixture
id: order.open
description: An open order
version: 1.0.0
owner: team-orders
find:
  - request: { service: orders, method: GET, path: /v1/orders }
    captures: [{ name: id, jsonpath: "$.data[0].id" }]
outputs: [id]
`)
			},
			line: prefix + "no account for payer: auth profile payer: secret TOKEN: ",
			md:   "The trial run was skipped: no account for payer: ",
			sums: "trial: skipped\n",
		},
		{
			name: "evidence store",
			change: func(t *testing.T, p *planRepo) {
				if err := os.MkdirAll(p.path(".semar", "store", "redact.key"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
			line: prefix + "redaction key {key} is not a regular file: delete it, and Semar makes a new key on the next run\n",
			md:   "The trial run was skipped: redaction key {key} is not a regular file: delete it, and Semar makes a new key on the next run.",
			sums: "trial: skipped\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPlanRepo(t, sampleapp.Options{})
			p.configure(t, listDraft)
			if tc.change != nil {
				tc.change(t, p)
			}
			// {key} stands for the path of the redaction key, which holds the temporary directory.
			key := p.path(".semar", "store", "redact.key")
			tc.line = strings.ReplaceAll(tc.line, "{key}", key)
			tc.md = strings.ReplaceAll(tc.md, "{key}", key)
			args := append([]string{"plan", "--diff", "main..release"}, tc.args...)
			res := p.must(t, cli.ExitOK, args...)
			if tc.line == "" {
				if strings.Contains(res.stderr, "trial run") {
					t.Errorf("stderr mentions the trial:\n%s", res.stderr)
				}
			} else {
				contains(t, "stderr", res.stderr, tc.line)
				if n := strings.Count(res.stderr, "trial run skipped"); n != 1 {
					t.Errorf("stderr has %d skip lines, want one:\n%s", n, res.stderr)
				}
			}
			if tc.sums != "" {
				contains(t, "stdout", res.stdout, tc.sums)
			} else if strings.Contains(res.stdout, "trial:") || strings.Contains(res.stdout, "tried:") {
				t.Errorf("stdout mentions the trial:\n%s", res.stdout)
			}
			// plan.md escapes Markdown in the key path, so a "_" in the temporary directory
			// (as in macOS's /var/folders/_0/...) shows as "\_". Compare the plain path.
			contains(t, "plan.md", strings.ReplaceAll(p.planMD(t), `\_`, "_"), "## Trial run\n\n"+tc.md)
			if n := p.app.Requests("", "/v1"); n != 0 {
				t.Errorf("the environment received %d requests, want none", n)
			}
			p.assertNoTempCopy(t)
		})
	}
}

// config.Load refuses an address that no host lists, so no trial can meet an unlisted host.
func TestTrialNeverReachesAnUnlistedHost(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, listDraft)
	p.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "baseUrl: http://"+p.host, "baseUrl: http://127.0.0.1:1", 1)
	})
	res := p.must(t, cli.ExitError, "plan", "--diff", "main..release")
	contains(t, "stderr", res.stderr, "baseUrl host 127.0.0.1:1 is not in hosts")
	if n := p.app.Requests("", "/v1"); n != 0 {
		t.Errorf("the environment received %d requests", n)
	}
	p.assertNothingPlanned(t)
}

func TestTrialSendsReadsOnly(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, listDraft)
	res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release")
	contains(t, "stdout", res.stdout, "plan: ", "tried: 1 of 4 steps, 0 problems returned, 0 for a second look\n")
	if n := p.app.Requests("GET", "/v1/orders"); n != 1 {
		t.Errorf("the environment received %d reads of /v1/orders, want 1", n)
	}
	if n := p.app.Requests("POST", ""); n != 0 {
		t.Errorf("the environment received %d writes", n)
	}
	md := p.planMD(t)
	contains(t, "plan.md", md, "## Trial run\n\nCode sent the read-only steps once to uat", "- read-order: GET /v1/orders answered 200", "- create-order: not tried: writes")
	// The run of record is the one that semar run makes later; a trial leaves a run of its own.
	runs := p.trialRuns(t)
	if len(runs) != 1 || runs[0].Purpose != domain.PurposeTrial || len(runs[0].Results) != 0 || runs[0].Environment != "uat" {
		t.Fatalf("runs = %+v", runs)
	}
	acts := p.must(t, cli.ExitOK, "show", string(runs[0].ID), "--activity", "--pass", "trial")
	contains(t, "the activity", acts.stdout, "GET /v1/orders")
	other := p.must(t, cli.ExitOK, "show", string(runs[0].ID), "--activity", "--pass", "scenario")
	if strings.Contains(other.stdout, "GET /v1/orders") {
		t.Errorf("a trial request is in another pass:\n%s", other.stdout)
	}
	p.assertNoSecret(t, []string{token})
}

func TestTrialRunClosedWhenPlanRejected(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, badDraft, badDraft, badDraft)
	res := p.must(t, cli.ExitError, "plan", "--diff", "main..release")
	contains(t, "stderr", res.stderr, "the planner's draft was rejected 3 times")
	runs := p.trialRuns(t)
	if len(runs) != 1 || runs[0].Purpose != "trial" || runs[0].Results == nil || len(runs[0].Results) != 0 || runs[0].EndedAt.IsZero() {
		t.Fatalf("runs = %+v", runs)
	}
	p.assertNothingPlanned(t)
}

// A service that never answers ends the trial at its limit, and the plan is still written.
func TestTrialStopsWhenTheServiceStalls(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, listDraft)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	p.rewriteConfig(t, func(s string) string { return strings.ReplaceAll(s, p.host, u.Host) })
	p.opts.TrialTimeout = 200 * time.Millisecond

	start := time.Now()
	res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release")
	if took := time.Since(start); took > 8*time.Second {
		t.Errorf("the plan took %s", took)
	}
	contains(t, "stdout", res.stdout, "trial: skipped\n")
	contains(t, "plan.md", p.planMD(t), "The trial run was skipped: the environment did not answer.")
	// A trial that stops while it runs says so in one line, as a trial that cannot start does (T6).
	contains(t, "stderr", res.stderr, "\nsemar: trial run skipped: the environment did not answer\n")
	if n := strings.Count(res.stderr, "trial run skipped"); n != 1 {
		t.Errorf("stderr has %d skip lines, want one:\n%s", n, res.stderr)
	}
}

// The trial prints nothing while the status line is up, so the status line is never cut.
func TestTrialLeavesTheStatusLineAlone(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, listDraft)
	p.opts.StderrTerminal = func() bool { return true }
	res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release")
	for _, line := range strings.Split(res.stderr, "\n") {
		if strings.Contains(line, "trial") && strings.Contains(line, "\x1b") {
			t.Errorf("a trial text shares a line with the status line: %q", line)
		}
	}
	if strings.Contains(res.stderr, "trial run") {
		t.Errorf("a trial line on standard error:\n%q", res.stderr)
	}
	contains(t, "stdout", res.stdout, "tried: 1 of 4 steps")

	// A skip line comes before the status line starts, so it is a whole line of its own.
	q := newPlanRepo(t, sampleapp.Options{})
	q.configure(t, listDraft)
	q.opts.StderrTerminal = func() bool { return true }
	if err := os.Unsetenv(planTokenEnv); err != nil {
		t.Fatal(err)
	}
	res = q.must(t, cli.ExitOK, "plan", "--diff", "main..release")
	var skipLines int
	for _, line := range strings.Split(res.stderr, "\n") {
		if strings.Contains(line, "trial run skipped") {
			skipLines++
			if !strings.HasPrefix(line, "semar: trial run skipped: ") {
				t.Errorf("the skip line shares its line with other text: %q", line)
			}
		}
	}
	if skipLines != 1 {
		t.Errorf("%d skip lines:\n%q", skipLines, res.stderr)
	}
}
