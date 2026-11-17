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
	for i, j := range jobs {
		// plan.maxTurns is 9, below RepairMaxTurns, so it still applies to the repair.
		if j.Workdir != in.SourceDir || j.Model != "some-model" || j.MaxTurns != 9 || j.Timeout != 2*time.Minute || string(j.Schema) != string(Schema()) {
			t.Errorf("job %d = %+v", i+1, j)
		}
	}
	if strings.Contains(jobs[0].Prompt, "previous draft was rejected") {
		t.Error("the first prompt says a draft was rejected")
	}
	for _, want := range []string{"previous draft was rejected", "change only what a problem names", "`scenarios[0].steps[1].method`", `method "DELETE" is not allowed`, string(bad)} {
		if !strings.Contains(jobs[1].Prompt, want) {
			t.Errorf("the second prompt lacks %q", want)
		}
	}
	if strings.Contains(jobs[1].Prompt, "previous.json") {
		t.Error("the second prompt sends the planner to a file for the previous draft")
	}
	if _, err := os.Stat(filepath.Join(in.SourceDir, PlanDir, "previous.json")); err == nil {
		t.Error("a file holds the previous draft: the prompt carries it")
	}
}

// A repair has the draft and its problems in the prompt, so it runs with fewer turns than a plan.
func TestRunRepairsWithAShorterTurnLimit(t *testing.T) {
	bad := withProblem(t, func(d *Draft) { d.Scenarios[0].Steps[1].Method = "DELETE" })
	cases := []struct {
		name      string
		maxTurns  int
		wantFirst int
		wantRetry int
	}{
		{"the default limit", 60, 60, RepairMaxTurns},
		{"a limit under the repair limit", 8, 8, 8},
		{"a limit at the repair limit", RepairMaxTurns, RepairMaxTurns, RepairMaxTurns},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &scripted{results: []provider.Result{{Output: bad}, {Output: []byte(goodDraft)}}}
			in := input(t)
			in.Limits.MaxTurns = tc.maxTurns
			if _, err := Run(t.Context(), s, in); err != nil {
				t.Fatal(err)
			}
			if got := []int{s.jobs[0].MaxTurns, s.jobs[1].MaxTurns}; got[0] != tc.wantFirst || got[1] != tc.wantRetry {
				t.Errorf("turn limits = %v, want %d then %d", got, tc.wantFirst, tc.wantRetry)
			}
		})
	}
}

// Every attempt after a decodable rejected draft is a repair of the latest draft, and an output
// that is no draft is planned again with the whole limit.
func TestRunRepairsTheLatestDraftAndPlansAgainAfterNoDraft(t *testing.T) {
	first := withProblem(t, func(d *Draft) { d.Scenarios[0].Steps[1].Method = "DELETE" })
	second := withProblem(t, func(d *Draft) { d.Tickets = nil })
	s := &scripted{results: []provider.Result{{Output: first}, {Output: second}, {Output: []byte(`{"summary": 7}`)}}}
	in := input(t)
	in.Limits.MaxTurns = 60
	_, err := Run(t.Context(), s, in)
	var rej *Rejected
	if !errors.As(err, &rej) {
		t.Fatalf("err = %v, want *Rejected", err)
	}
	if len(s.jobs) != MaxAttempts {
		t.Fatalf("%d jobs", len(s.jobs))
	}
	if s.jobs[1].MaxTurns != RepairMaxTurns || !strings.Contains(s.jobs[1].Prompt, string(first)) {
		t.Errorf("the first repair: %d turns, prompt holds the first draft %v", s.jobs[1].MaxTurns, strings.Contains(s.jobs[1].Prompt, string(first)))
	}
	if s.jobs[2].MaxTurns != RepairMaxTurns || !strings.Contains(s.jobs[2].Prompt, string(second)) || strings.Contains(s.jobs[2].Prompt, string(first)) {
		t.Errorf("the second repair must hold the second draft only: %d turns", s.jobs[2].MaxTurns)
	}

	s = &scripted{results: []provider.Result{{Output: []byte(`{"summary": 7}`)}, {Output: []byte(goodDraft)}}}
	if _, err := Run(t.Context(), s, in); err != nil {
		t.Fatal(err)
	}
	if s.jobs[1].MaxTurns != 60 || !strings.Contains(s.jobs[1].Prompt, "not a usable draft") || strings.Contains(s.jobs[1].Prompt, `{"summary": 7}`) {
		t.Errorf("a plan after an output that is no draft: %d turns, prompt %q", s.jobs[1].MaxTurns, s.jobs[1].Prompt[:200])
	}
}

// A repair that the harness cannot finish does not throw away a draft that passed, and does not
// hide a failure when none did.
func TestRunKeepsAnEarlierPassingDraftWhenARepairFails(t *testing.T) {
	stop := &provider.Error{Class: provider.ValidationError, Detail: "the harness reached the turn limit"}

	in := criteriaInput(t)
	in.Limits.MaxTurns = 60
	s := &failAfter{scripted: scripted{results: []provider.Result{{Output: mustJSON(t, citingSeven(t))}}}, err: stop}
	out, err := Run(t.Context(), s, in)
	if err != nil {
		t.Fatalf("err = %v, want the draft that passed", err)
	}
	if len(s.jobs) != 2 || s.jobs[1].MaxTurns != RepairMaxTurns || out.UsedAttempt != 1 || out.Attempts != 2 || len(out.Unaccounted) != 3 {
		t.Errorf("%d jobs, repair turns %d, used attempt %d of %d, %d unaccounted", len(s.jobs), s.jobs[1].MaxTurns, out.UsedAttempt, out.Attempts, len(out.Unaccounted))
	}

	missing := withProblem(t, func(d *Draft) { d.Tickets = nil })
	s = &failAfter{scripted: scripted{results: []provider.Result{{Output: missing}}}, err: stop}
	_, err = Run(t.Context(), s, input(t))
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Detail != stop.Detail || len(s.jobs) != 2 {
		t.Fatalf("err = %v after %d jobs, want the provider error of the repair", err, len(s.jobs))
	}
}

// failAfter returns the results in order and then err.
type failAfter struct {
	scripted
	err error
}

func (f *failAfter) Run(ctx context.Context, job provider.Job) (provider.Result, error) {
	if len(f.results) == 0 {
		f.jobs = append(f.jobs, job)
		return provider.Result{}, f.err
	}
	return f.scripted.Run(ctx, job)
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

// progressPlanner calls the function of each job as a provider does.
type progressPlanner struct {
	scripted
	calls [][]provider.Progress
}

func (p *progressPlanner) Run(ctx context.Context, job provider.Job) (provider.Result, error) {
	i := len(p.jobs)
	if job.Progress != nil {
		for _, c := range p.calls[i] {
			job.Progress(c)
		}
	}
	return p.scripted.Run(ctx, job)
}

func TestRunReportsProgressWithTheAttempt(t *testing.T) {
	bad := withProblem(t, func(d *Draft) { d.Scenarios[0].Steps[1].Service = "payments" })
	p := &progressPlanner{
		scripted: scripted{results: []provider.Result{{Output: bad}, {Output: []byte(goodDraft)}}},
		calls: [][]provider.Progress{
			{{Turns: 1, ToolCalls: 2, Tool: "Read"}},
			{{Turns: 1}},
		},
	}
	in := input(t)
	var got []Progress
	in.Progress = func(pr Progress) { got = append(got, pr) }
	if _, err := Run(t.Context(), p, in); err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[0] != (Progress{Attempt: 1}) ||
		got[1] != (Progress{Attempt: 1, Progress: provider.Progress{Turns: 1, ToolCalls: 2, Tool: "Read"}}) ||
		got[2].Attempt != 2 || got[2].Problems == 0 || got[2].Turns != 0 ||
		got[3] != (Progress{Attempt: 2, Problems: got[2].Problems, Progress: provider.Progress{Turns: 1}}) {
		t.Errorf("reports = %+v", got)
	}
}

func TestRunWithNoProgressGivesTheJobNoFunction(t *testing.T) {
	p := &scripted{results: []provider.Result{{Output: []byte(goodDraft)}}}
	if _, err := Run(t.Context(), p, input(t)); err != nil {
		t.Fatal(err)
	}
	if p.jobs[0].Progress != nil {
		t.Error("the job has a Progress function, want nil")
	}
}
