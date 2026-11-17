package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/stage/plan"
)

func TestPipelineUseLine(t *testing.T) {
	res := provider.Result{InputTokens: 92_000, OutputTokens: 21_000, CostUSD: 0.09}
	for name, tc := range map[string]struct {
		out  plan.Output
		want string
	}{
		"the example of the plan": {
			plan.Output{Result: res, Pipeline: &plan.PipelineStats{Criteria: 16, Calls: 21, Corrections: 3,
				ByModel: []plan.ModelCount{{Model: "haiku", Answers: 14}, {Model: "sonnet", Answers: 2}}}},
			"model use: 16 jobs (14 haiku, 2 sonnet), 3 corrections, 92k input, 21k output, $0.09",
		},
		"one job, a cache, and a reused answer": {
			plan.Output{Result: provider.Result{InputTokens: 6000, CacheReadTokens: 4000, OutputTokens: 900}, Pipeline: &plan.PipelineStats{Criteria: 2, Calls: 1, Reused: 1,
				ByModel: []plan.ModelCount{{Model: "haiku", Answers: 2}}}},
			"model use: 2 jobs (2 haiku), 0 corrections, 6.0k input (4.0k cached), 900 output, 1 reused",
		},
		"a model that answered nothing is left out, and a gap is counted": {
			plan.Output{Result: res, Pipeline: &plan.PipelineStats{Criteria: 3, Calls: 5, Corrections: 1, Unanswered: 1,
				ByModel: []plan.ModelCount{{Model: "haiku", Answers: 2}, {Model: "sonnet", Answers: 0}}}},
			"model use: 2 jobs (2 haiku), 1 correction, 92k input, 21k output, $0.09, 1 unanswered",
		},
		"a run that stopped": {
			plan.Output{Result: provider.Result{InputTokens: 700, OutputTokens: 30}, Pipeline: &plan.PipelineStats{Calls: 3}},
			"model use: 3 calls, 700 input, 30 output",
		},
		"nothing ran": {plan.Output{Pipeline: &plan.PipelineStats{}}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := useLine(tc.out, false); got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
	if got := useLine(plan.Output{Attempts: 2, Result: provider.Result{Turns: 5, InputTokens: 1500, OutputTokens: 100}}, false); got != "model use: 2 attempts, 5 turns, 1.5k input, 100 output" {
		t.Errorf("the single pipeline line changed: %q", got)
	}
}

func TestLoadAnswersReadsTheNewestPlanFirstAndSkipsWhatItCannotRead(t *testing.T) {
	dir := t.TempDir()
	write := func(plan, content string, age time.Duration) {
		d := filepath.Join(dir, ".semar", "plans", plan)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(d, answersFile)
		if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		if err := os.Chtimes(d, at, at); err != nil {
			t.Fatal(err)
		}
	}
	write("aaaa", `[{"ticket":"SHOP-1","cite":"t.md:3","hash":"h-old","output":{"answer":{}}}]`, 48*time.Hour)
	write("bbbb", `[{"ticket":"SHOP-1","cite":"t.md:3","hash":"h-new","output":{"answer":{}}}]`, time.Hour)
	write("cccc", `not json`, 0)
	if err := os.MkdirAll(filepath.Join(dir, ".semar", "plans", "dddd"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := loadAnswers(dir)
	if len(got) != 2 || got[0].Hash != "h-new" || got[1].Hash != "h-old" {
		t.Errorf("answers = %+v", got)
	}
	if got := loadAnswers(t.TempDir()); len(got) != 0 {
		t.Errorf("a repository with no plan has answers: %+v", got)
	}
}
