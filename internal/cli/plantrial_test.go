package cli

import (
	"context"
	"io/fs"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/knowledge"
	"github.com/jancuk/semar/internal/openapi"
	"github.com/jancuk/semar/internal/stage/plan"
	"github.com/jancuk/semar/internal/vcs"
	"github.com/jancuk/semar/internal/vcs/gittest"
)

const baseSpec = `openapi: 3.0.3
paths:
  /v1/orders:
    get:
      responses:
        "200": {description: ok}
`

const headSpec = baseSpec + `  /v1/orders/{id}/summary:
    get:
      responses:
        "200": {description: ok}
`

func parsed(t *testing.T, text string) openapi.Document {
	t.Helper()
	d, err := openapi.Parse([]byte(text), "docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestBaseAPIReadAtMergeBaseWhenChanged(t *testing.T) {
	g := gittest.New(t)
	g.Commit("initial import", map[string]*string{"docs/openapi.yaml": gittest.S(baseSpec), "docs/other.yaml": gittest.S(baseSpec)})
	g.Git("checkout", "--quiet", "-b", "release")
	g.Commit("SHOP-1 add the summary", map[string]*string{"docs/openapi.yaml": gittest.S(headSpec)})
	git, err := vcs.Open(g.Dir)
	if err != nil {
		t.Fatal(err)
	}
	mergeBase := g.Git("rev-parse", "main")
	head := parsed(t, headSpec)
	source := func(path string, api *openapi.Document, problem string) knowledge.Source {
		return knowledge.Source{ID: "api", Kind: "openapi", Path: path, API: api, Problem: problem}
	}
	hasSummary := func(ds []openapi.Document) bool {
		return len(ds) == 1 && ds[0].Lookup("GET", "/v1/orders/{id}/summary").PathKnown
	}

	t.Run("a changed document is read at the merge base", func(t *testing.T) {
		cs := domain.ChangeSet{MergeBase: mergeBase, Files: []domain.FileChange{{Path: "docs/openapi.yaml"}}}
		got := baseDocuments(context.Background(), git, cs, []knowledge.Source{source("docs/openapi.yaml", &head, "")})
		if len(got) != 1 || hasSummary(got) || !got[0].Lookup("GET", "/v1/orders").PathKnown {
			t.Fatalf("base documents = %+v", got)
		}
	})
	t.Run("an unchanged document is the head document", func(t *testing.T) {
		cs := domain.ChangeSet{MergeBase: mergeBase, Files: []domain.FileChange{{Path: "main.go"}}}
		got := baseDocuments(context.Background(), git, cs, []knowledge.Source{source("docs/openapi.yaml", &head, "")})
		if !hasSummary(got) {
			t.Fatalf("base documents = %+v", got)
		}
	})
	t.Run("a document that cannot be used is left out", func(t *testing.T) {
		changed := domain.ChangeSet{MergeBase: mergeBase, Files: []domain.FileChange{{Path: "docs/new.yaml"}, {Path: "docs/openapi.yaml"}}}
		for name, srcs := range map[string][]knowledge.Source{
			"added at the head": {source("docs/new.yaml", &head, "")},
			"a source problem":  {source("docs/openapi.yaml", &head, "not in the copy")},
			"a plain document":  {source("docs/openapi.yaml", nil, "")},
			"no source":         nil,
		} {
			if got := baseDocuments(context.Background(), git, changed, srcs); len(got) != 0 {
				t.Errorf("%s: base documents = %+v", name, got)
			}
		}
	})
	t.Run("a base that is not an API document is left out", func(t *testing.T) {
		cs := domain.ChangeSet{MergeBase: mergeBase, Files: []domain.FileChange{{Path: "docs/other.yaml"}}}
		bad := &fakeShow{data: []byte("not: [")}
		if got := baseDocuments(context.Background(), bad, cs, []knowledge.Source{source("docs/other.yaml", &head, "")}); len(got) != 0 {
			t.Errorf("base documents = %+v", got)
		}
		gone := &fakeShow{err: fs.ErrNotExist}
		if got := baseDocuments(context.Background(), gone, cs, []knowledge.Source{source("docs/other.yaml", &head, "")}); len(got) != 0 {
			t.Errorf("base documents = %+v", got)
		}
	})
}

type fakeShow struct {
	data []byte
	err  error
}

func (f *fakeShow) Show(context.Context, string, string, int64) ([]byte, error) { return f.data, f.err }

func TestTrialSummaryLine(t *testing.T) {
	sentStep := func(step domain.StepID, status int) plan.TrialStep {
		return plan.TrialStep{Scenario: "SHOP-1-01", Step: step, Result: plan.TrialSent, Method: "GET", Route: "/v1/orders/{id}", Status: status}
	}
	scenarios := []domain.Scenario{{ID: "SHOP-1-01", Ticket: "SHOP-1", Title: "t", Steps: []domain.Step{
		{ID: "make", Setup: true, Hint: &domain.RequestHint{Method: "POST", Path: "/v1/orders"}},
		{ID: "read", Hint: &domain.RequestHint{Method: "GET", Path: "/v1/orders/{id}"}},
		{ID: "boom", Hint: &domain.RequestHint{Method: "GET", Path: "/v1/orders/{id}"}},
	}}}
	ran := plan.Output{
		Scenarios: scenarios, TrialReturned: 2,
		Trial: &plan.Trial{Steps: []plan.TrialStep{
			{Scenario: "SHOP-1-01", Given: "order", Result: plan.TrialSent, Found: true},
			{Scenario: "SHOP-1-01", Step: "make", Result: plan.TrialNotTried, Why: "writes", Method: "POST", Route: "/v1/orders"},
			sentStep("read", 200),
			sentStep("boom", 502),
		}},
	}
	for _, tc := range []struct {
		name string
		in   plan.Input
		out  plan.Output
		want string
	}{
		{"ran", plan.Input{}, ran, "tried: 2 of 3 steps, 2 problems returned, 1 for a second look"},
		{"skipped at the start", plan.Input{TrialOff: "skipped: no account for default: x"}, plan.Output{}, "trial: skipped"},
		{"stopped", plan.Input{}, plan.Output{Trial: &plan.Trial{Skipped: "the environment did not answer"}}, "trial: skipped"},
		{"turned off", plan.Input{TrialOff: "turned off with --no-trial"}, plan.Output{}, ""},
		{"never on", plan.Input{}, plan.Output{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := trialSummary(tc.in, tc.out); got != tc.want {
				t.Errorf("trialSummary = %q, want %q", got, tc.want)
			}
		})
	}
}
