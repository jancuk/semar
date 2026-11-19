package cli_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/fake"
	"github.com/jancuk/semar/internal/sampleapp"
	"github.com/jancuk/semar/internal/vcs/gittest"
)

const webTicket = criterionTicket + `2. Web: after a refund of 5 the refund page shows the total 5.00.
3. Android: the refund screen shows the total.
`

var refPattern = regexp.MustCompile(`(?m)^(e\d+) (\w+) "([^"]*)"`)

// webModel is a model that reads the page list in its prompt and answers as a person would: it names the
// references of the list that it was shown. The API criterion gets its scenario, the web criterion a
// manual check with the surface web, the Android one a manual check with the surface mobile, and each
// turn of a flow an action on the references of its page.
type webModel struct {
	mu    sync.Mutex
	turns int
	jobs  []provider.Job
	// script, when it is set, answers each turn of a flow in the place of the refund flow. It gets the
	// number of the turn, from 1, and the prompt.
	script func(n int, prompt string) string
}

func (m *webModel) answer(j provider.Job) (provider.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs = append(m.jobs, j)
	out := ""
	switch {
	case strings.Contains(j.Prompt, "You check one acceptance criterion of a web application"):
		m.turns++
		out = m.turn(j.Prompt)
	case strings.Contains(j.Prompt, "SHOP-482.md:5\n"):
		out = criterionAnswerJSON
	case strings.Contains(j.Prompt, "SHOP-482.md:6\n"):
		out = `{"answer":{"kind":"manualCheck","title":"The refund page shows the total","surface":"web","manualSteps":["Open the refund page","Refund 5"],"expected":"The total reads 5.00"}}`
	default:
		out = `{"answer":{"kind":"manualCheck","title":"The Android refund screen shows the total","surface":"mobile","manualSteps":["Open the app"],"expected":"The total shows"}}`
	}
	return provider.Result{Output: json.RawMessage(out), Model: j.Model, Turns: 1, InputTokens: 100, OutputTokens: 10, CostUSD: 0.01}, nil
}

func (m *webModel) turn(prompt string) string {
	if m.script != nil {
		return m.script(m.turns, prompt)
	}
	refs := map[string]string{}
	for _, r := range refPattern.FindAllStringSubmatch(prompt, -1) {
		refs[r[2]+" "+r[3]] = r[1]
	}
	switch {
	case strings.Contains(prompt, "check e"):
		return `{"turn":{"kind":"done","title":"A refund shows its total","intent":"Before: no total. Now: the total is shown."}}`
	case strings.Contains(prompt, "What was done so far"):
		return fmt.Sprintf(`{"turn":{"kind":"act","actions":[{"kind":"check","ref":%q,"expect":"5.00"},{"kind":"check","ref":%q,"expect":"partly refunded"}]}}`, refs["text 5.00"], refs["text partly refunded"])
	}
	return fmt.Sprintf(`{"turn":{"kind":"act","actions":[{"kind":"fill","ref":%q,"text":"5"},{"kind":"click","ref":%q}]}}`, refs["textbox Amount"], refs["button Refund"])
}

func planWithWeb(t *testing.T, m *webModel, extra ...string) (*planRepo, result) {
	t.Helper()
	return planWithWebPage(t, m, "", extra...)
}

// planWithWebPage is planWithWeb on a refund page that holds the extra HTML at its end.
func planWithWebPage(t *testing.T, m *webModel, html string, extra ...string) (*planRepo, result) {
	t.Helper()
	p := newPlanRepo(t, sampleapp.Options{})
	if html != "" {
		p.refundExtra.Store(&html)
	}
	p.git.Commit("SHOP-482 write the ticket and the API", map[string]*string{
		"docs/tickets/SHOP-482.md": gittest.S(webTicket),
		"docs/openapi.yaml":        gittest.S(criterionAPI),
	})
	p.configure(t, criterionAnswerJSON)
	p.withCriterionKnowledge(t)
	p.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "baseUrl: http://"+p.host, "baseUrl: http://"+p.host+"\n        web: {start: /refund}", 1)
	})
	p.opts.HTTP = http.DefaultClient
	p.opts.Providers = func(string) *provider.Registry {
		r := provider.NewRegistry()
		r.Register(fake.Kind, func(cfg config.Provider) (provider.Instance, error) { return fake.NewFunc(cfg.ID, m.answer), nil })
		return r
	}
	args := append([]string{"plan", "--diff", "main..release", "--pipeline", "criteria", "--ladder", "planner:small", "--no-trial"}, extra...)
	return p, p.semar(t, t.Context(), args...)
}

func TestPlanWritesAWebTestAndAnAPITestAndARunOfThemPasses(t *testing.T) {
	m := &webModel{}
	p, res := planWithWeb(t, m)
	if res.code != cli.ExitOK {
		t.Fatalf("plan exited %d\nstdout:\n%s\nstderr:\n%s", res.code, res.stdout, res.stderr)
	}
	if strings.Contains(res.stderr, "not explored") {
		t.Fatalf("stderr:\n%s", res.stderr)
	}
	contains(t, "stdout", res.stdout,
		"SHOP-482 (behaviour): 2 scenarios\n",
		"web: 1 criterion explored, 1 test written, 0 left for a person, 3 turns, $0.03\n",
		"model use: 3 jobs (3 small)",
	)
	if m.turns != 3 {
		t.Errorf("the model played %d turns, want 3", m.turns)
	}

	// The web test takes the next number of its ticket, and the API scenario numbers after it.
	web, err := os.ReadFile(p.path(".semar", "scenarios", "SHOP-482", "SHOP-482-01.feature"))
	must(t, err)
	contains(t, "the web scenario", string(web), "@surface:web", "Scenario: A refund shows its total", `When Type "5" in amount`, `Then the element total equals "5.00"`,
		"Feature: Refund above the total", "# ticket docs/tickets/SHOP-482.md:6 Web: after a refund of 5 the refund page shows the total 5.00, by model")
	api, err := os.ReadFile(p.path(".semar", "scenarios", "SHOP-482", "SHOP-482-02.feature"))
	must(t, err)
	contains(t, "the API scenario", string(api), "@surface:api")

	dir := p.path(".semar", "plans", p.head(t)[:12])
	md, err := os.ReadFile(dir + "/plan.md")
	must(t, err)
	contains(t, "plan.md", string(md),
		"1. SHOP-482-01: A refund shows its total. Cite: docs/tickets/SHOP-482.md:6",
		"| docs/tickets/SHOP-482.md:6 | web test | small | 0 |",
		"Surface: mobile",
		"web test: SHOP-482-01",
	)
	obligations, err := os.ReadFile(p.path(".semar", "obligations", "SHOP-482.plan.yaml"))
	must(t, err)
	contains(t, "the obligations", string(obligations), "SHOP-482/scenario-SHOP-482-01", "SHOP-482/scenario-SHOP-482-02", "surface: mobile")
	if n := strings.Count(string(obligations), "criterion: {number: "); n != 3 {
		t.Errorf("%d entries name their criterion, want the web test, the API scenario, and the manual check:\n%s", n, obligations)
	}
	if strings.Contains(string(obligations), "refund page shows the total") && strings.Contains(string(obligations), "manual-") && strings.Contains(string(obligations), "Open the refund page") {
		t.Errorf("the web criterion is still a manual check:\n%s", obligations)
	}

	// A person reads the web test and the screens, and then the tests run.
	run := p.must(t, cli.ExitOK, "run", "--scenario", "SHOP-482-01", "--build", p.head(t))
	contains(t, "the run", run.stdout, "SHOP-482-01  passed")
}

func TestPlanSaysOnceWhyItDoesNotExploreAndLeavesTheWebCriterionManual(t *testing.T) {
	for name, tc := range map[string]struct {
		args   []string
		change func(string) string
		want   string
	}{
		"--no-web": {args: []string{"--no-web"}, want: "web criteria are not explored: turned off with --no-web; each stays a manual check\n"},
		"a read only environment": {change: func(s string) string { return strings.Replace(s, "mode: test_data", "mode: read_only", 1) },
			want: "web criteria are not explored: uat allows only reads, and a flow clicks and types; each stays a manual check\n"},
		"a service that is an API only": {change: func(s string) string {
			return strings.Replace(s, "web: {start: /refund}", "web: {start: /api-only}", 1)
		},
			want: "web criteria are not explored: no service has a web page: GET http://127.0.0.1:"},
	} {
		t.Run(name, func(t *testing.T) {
			m := &webModel{}
			p := newPlanRepo(t, sampleapp.Options{})
			p.git.Commit("SHOP-482 write the ticket and the API", map[string]*string{
				"docs/tickets/SHOP-482.md": gittest.S(webTicket),
				"docs/openapi.yaml":        gittest.S(criterionAPI),
			})
			p.configure(t, criterionAnswerJSON)
			p.withCriterionKnowledge(t)
			p.rewriteConfig(t, func(s string) string {
				s = strings.Replace(s, "baseUrl: http://"+p.host, "baseUrl: http://"+p.host+"\n        web: {start: /refund}", 1)
				if tc.change != nil {
					s = tc.change(s)
				}
				return s
			})
			p.opts.HTTP = http.DefaultClient
			p.opts.Providers = func(string) *provider.Registry {
				r := provider.NewRegistry()
				r.Register(fake.Kind, func(cfg config.Provider) (provider.Instance, error) { return fake.NewFunc(cfg.ID, m.answer), nil })
				return r
			}
			args := append([]string{"plan", "--diff", "main..release", "--pipeline", "criteria", "--ladder", "planner:small", "--no-trial"}, tc.args...)
			res := p.semar(t, t.Context(), args...)
			if res.code != cli.ExitOK {
				t.Fatalf("plan exited %d\nstderr:\n%s", res.code, res.stderr)
			}
			contains(t, "stderr", res.stderr, tc.want)
			if strings.Count(res.stderr, "web criteria are not explored") != 1 {
				t.Errorf("the plan must say why once:\n%s", res.stderr)
			}
			if m.turns != 0 || strings.Contains(res.stdout, "web:") {
				t.Errorf("turns %d, stdout:\n%s", m.turns, res.stdout)
			}
			md, err := os.ReadFile(p.path(".semar", "plans", p.head(t)[:12], "plan.md"))
			must(t, err)
			contains(t, "plan.md", string(md), "- Web: no criterion was explored in a browser: ", "Surface: web", "Surface: mobile")
			if exists(t, p.path(".semar", "screens")) {
				t.Error("a screens file was written by a plan that explored nothing")
			}
		})
	}
}

func TestPlanRecordsTheTurnsOfWebFlowsInTheUsageFileApartFromTheCriteria(t *testing.T) {
	m := &webModel{}
	p, res := planWithWeb(t, m)
	if res.code != cli.ExitOK {
		t.Fatalf("plan exited %d\nstderr:\n%s", res.code, res.stderr)
	}
	data, err := os.ReadFile(p.path("..", "usage.jsonl"))
	if err != nil {
		data, err = os.ReadFile(filepath.Join(p.opts.Home, "usage.jsonl"))
	}
	must(t, err)
	var criteria, webRecords int
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var rec struct {
			Pipeline string
			Attempts int
		}
		must(t, json.Unmarshal([]byte(line), &rec))
		switch rec.Pipeline {
		case "criteria":
			criteria += rec.Attempts
		case "web":
			webRecords += rec.Attempts
		}
	}
	if criteria != 3 || webRecords != 3 {
		t.Errorf("the usage file holds %d criteria jobs and %d web turns, want 3 and 3:\n%s", criteria, webRecords, data)
	}
}

func TestPlanSaysWhichHostsTheBrowserBlockedWhileItExplored(t *testing.T) {
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "x") }))
	t.Cleanup(cdn.Close)
	m := &webModel{}
	p := newPlanRepo(t, sampleapp.Options{})
	extra := `<img src="` + cdn.URL + `/pixel.png?token=abc123">`
	p.refundExtra.Store(&extra)
	p.git.Commit("SHOP-482 write the ticket and the API", map[string]*string{
		"docs/tickets/SHOP-482.md": gittest.S(webTicket),
		"docs/openapi.yaml":        gittest.S(criterionAPI),
	})
	p.configure(t, criterionAnswerJSON)
	p.withCriterionKnowledge(t)
	p.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "baseUrl: http://"+p.host, "baseUrl: http://"+p.host+"\n        web: {start: /refund}", 1)
	})
	p.opts.HTTP = http.DefaultClient
	p.opts.Providers = func(string) *provider.Registry {
		r := provider.NewRegistry()
		r.Register(fake.Kind, func(cfg config.Provider) (provider.Instance, error) { return fake.NewFunc(cfg.ID, m.answer), nil })
		return r
	}
	res := p.semar(t, t.Context(), "plan", "--diff", "main..release", "--pipeline", "criteria", "--ladder", "planner:small", "--no-trial")
	if res.code != cli.ExitOK {
		t.Fatalf("plan exited %d\nstdout:\n%s\nstderr:\n%s", res.code, res.stdout, res.stderr)
	}
	host := strings.TrimPrefix(cdn.URL, "http://")
	contains(t, "stdout", res.stdout,
		"web: 1 criterion explored, 1 test written",
		"blocked hosts: the browser did not reach 1 host, because environments.uat.hosts does not list them\n",
		"  "+host+": docs/tickets/SHOP-482.md:6\n",
		"can pass or fail for the wrong reason",
		"run semar login, which offers to add them",
	)
	if strings.Contains(res.stdout, "abc123") {
		t.Errorf("the output holds the query of a blocked request:\n%s", res.stdout)
	}
}
