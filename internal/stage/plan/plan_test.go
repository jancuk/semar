package plan

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/fake"
)

// scripted returns its results in order and records each job.
type scripted struct {
	results []provider.Result
	err     error
	jobs    []provider.Job
}

func (s *scripted) ID() string   { return "planner-1" }
func (s *scripted) Kind() string { return "scripted" }
func (s *scripted) Run(_ context.Context, job provider.Job) (provider.Result, error) {
	s.jobs = append(s.jobs, job)
	if s.err != nil {
		return provider.Result{}, s.err
	}
	r := s.results[0]
	s.results = s.results[1:]
	return r, nil
}

func withProblem(t *testing.T, change func(d *Draft)) []byte {
	t.Helper()
	d := draft(t)
	change(&d)
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func fakeWith(t *testing.T, outputs ...[]byte) *fake.Fake {
	t.Helper()
	root := t.TempDir()
	var names []string
	for i, o := range outputs {
		name := filepath.Join("out", string(rune('a'+i))+".json")
		if err := os.MkdirAll(filepath.Join(root, "out"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), o, 0o600); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	f, err := fake.New(config.Provider{ID: "fake-1", Kind: fake.Kind, Outputs: names}, root)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestRunCorrectsADraftOnTheSecondAttempt(t *testing.T) {
	bad := withProblem(t, func(d *Draft) { d.Scenarios[0].Steps[1].Method = "DELETE" })
	f := fakeWith(t, bad, []byte(goodDraft))
	in := input(t)
	in.Model = "some-model"
	in.Limits.MaxTurns, in.Limits.Timeout = 9, 2*time.Minute

	out, err := Run(t.Context(), f, in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Attempts != 2 || len(out.Scenarios) != 2 || len(out.Scripts) != 2 {
		t.Errorf("attempts %d, %d scenarios, %d scripts; want 2, 2, 2", out.Attempts, len(out.Scenarios), len(out.Scripts))
	}
	if out.PlannerID != "fake-1" || out.PlannerKind != fake.Kind || out.Result.Model != "fake" || out.Result.Turns != 2 {
		t.Errorf("planner %s/%s, result %+v", out.PlannerID, out.PlannerKind, out.Result)
	}
	jobs := f.Jobs()
	if len(jobs) != 2 {
		t.Fatalf("%d jobs, want 2", len(jobs))
	}
	for _, j := range jobs {
		if j.Workdir != in.SourceDir || j.Model != "some-model" || j.MaxTurns != 9 || j.Timeout != 2*time.Minute || string(j.Schema) != string(Schema()) {
			t.Errorf("job = %+v", j)
		}
	}
	if strings.Contains(jobs[0].Prompt, "previous draft was rejected") {
		t.Error("the first prompt says a draft was rejected")
	}
	for _, want := range []string{"previous draft was rejected", "`scenarios[0].steps[1].method`", `method "DELETE" is not allowed`} {
		if !strings.Contains(jobs[1].Prompt, want) {
			t.Errorf("the second prompt lacks %q", want)
		}
	}
	prev := filepath.Join(in.SourceDir, PlanDir, "previous.json")
	data, err := os.ReadFile(prev)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(bad) {
		t.Errorf("previous.json holds %s, want the rejected draft", data)
	}
	if info, err := os.Stat(prev); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("previous.json mode %v, %v; want 0600", info.Mode().Perm(), err)
	}
}

func TestRunStopsAfterThreeRejectedDrafts(t *testing.T) {
	bad := withProblem(t, func(d *Draft) { d.Tickets = nil })
	f := fakeWith(t, bad, []byte(`{"summary": 7}`), bad, bad)
	_, err := Run(t.Context(), f, input(t))
	var rej *Rejected
	if !errors.As(err, &rej) {
		t.Fatalf("err = %v, want *Rejected", err)
	}
	if rej.Attempts != MaxAttempts || len(rej.Problems) == 0 || rej.Problems[0].Path != "tickets" {
		t.Errorf("rejected = %+v", rej)
	}
	jobs := f.Jobs()
	if len(jobs) != MaxAttempts {
		t.Fatalf("%d jobs, want %d", len(jobs), MaxAttempts)
	}
	if !strings.Contains(jobs[2].Prompt, "the output is not a draft") {
		t.Error("the third prompt does not say that the second output was not a draft")
	}
}

func TestRunReturnsAProviderErrorAtOnce(t *testing.T) {
	p := &scripted{err: &provider.Error{Class: provider.UsageLimit, Detail: "out of credit"}}
	_, err := Run(t.Context(), p, input(t))
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Class != provider.UsageLimit {
		t.Fatalf("err = %v, want the provider error", err)
	}
	if len(p.jobs) != 1 {
		t.Errorf("%d jobs, want 1", len(p.jobs))
	}
}

func TestRunSumsTokens(t *testing.T) {
	p := &scripted{results: []provider.Result{
		{Output: []byte(`{}`), Model: "m-1", Turns: 3, InputTokens: 100, OutputTokens: 10},
		{Output: []byte(goodDraft), Model: "m-2", Turns: 4, InputTokens: 200, OutputTokens: 20},
	}}
	out, err := Run(t.Context(), p, input(t))
	if err != nil {
		t.Fatal(err)
	}
	r := out.Result
	if r.Model != "m-2" || r.Turns != 7 || r.InputTokens != 300 || r.OutputTokens != 30 || string(r.Output) != goodDraft {
		t.Errorf("result = %+v", r)
	}
}

// Text of a draft is data: it reaches the next prompt and plan.md as text, never as a template or as Markdown.
func TestDraftTextStaysData(t *testing.T) {
	hostile := "# Heading [[.Problems]] {{order_id}} [link](https://x.example) `code` *bold* <b>"
	d := draft(t)
	d.Scenarios[1].Title = d.Scenarios[0].Title + " " + hostile
	d.Scenarios = append(d.Scenarios, d.Scenarios[1])
	d.Scenarios[2].Steps = append([]StepDraft(nil), d.Scenarios[2].Steps...)
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	p := &scripted{results: []provider.Result{{Output: data}, {Output: []byte(goodDraft)}}}
	if _, err := Run(t.Context(), p, input(t)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.jobs[1].Prompt, hostile) {
		t.Errorf("the second prompt does not quote the title as it was:\n%s", p.jobs[1].Prompt)
	}

	in := input(t)
	d = draft(t)
	d.Summary = hostile
	d.Scenarios[0].Title = hostile
	d.Scenarios[0].Steps[1].Action = hostile
	d.Scenarios[0].Steps[1].Expect[0].Why = "internal/refund/handler.go:41 " + hostile
	scs, scripts, ps := Validate(d, in)
	if len(ps) > 0 {
		t.Fatalf("problems: %v", ps)
	}
	var b strings.Builder
	if err := Render(&b, in, Output{Draft: d, Scenarios: scs, Scripts: scripts, Attempts: 1}); err != nil {
		t.Fatal(err)
	}
	md := b.String()
	for _, raw := range []string{"# Heading", "[[", "[link]", "https://", "`code`", "*bold*", "<b>"} {
		if strings.Contains(md, " "+raw) || strings.HasPrefix(md, raw) {
			t.Errorf("plan.md holds %q unescaped:\n%s", raw, md)
		}
	}
	if !strings.Contains(md, `\# Heading \[\[.Problems\]\] {{order_id}} \[link\](https\://x.example) \`+"`code\\` \\*bold\\* &lt;b&gt;") {
		t.Errorf("plan.md does not hold the escaped text:\n%s", md)
	}
}

func TestExistingAndProblemLists(t *testing.T) {
	got := existingList(map[string][]domain.Scenario{
		"B-1": {{ID: "B-1-01", Title: "two\nlines"}},
		"A-1": {{ID: "A-1-02", Title: "first"}},
	})
	if want := "  - A-1-02 (A-1): first\n  - B-1-01 (B-1): two lines"; got != want {
		t.Errorf("existingList = %q, want %q", got, want)
	}
	ps := make([]Problem, maxProblemsShown+5)
	for i := range ps {
		ps[i] = Problem{Path: "p", Message: strings.Repeat("x", maxProblemRunes+10)}
	}
	list := problemList(ps)
	if !strings.HasSuffix(list, "- and 5 more problems") || strings.Contains(list, strings.Repeat("x", maxProblemRunes+1)) {
		t.Errorf("problemList is not bounded:\n%s", list[len(list)-200:])
	}
}
