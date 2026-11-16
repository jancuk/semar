package plan

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/changeset"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/prompt"
	"github.com/jancuk/semar/internal/provider"
)

// MaxAttempts bounds the drafts one plan asks for (A4).
const MaxAttempts = 3

const (
	maxProblemsShown = 60
	maxProblemRunes  = 400
)

// Progress is where a plan is. Attempt starts at 1.
type Progress struct {
	Attempt  int
	Problems int // the problems of the previous draft, 0 on the first attempt
	provider.Progress
}

// Planner is the part of an agent provider that a plan uses.
type Planner interface {
	ID() string
	Kind() string
	Run(ctx context.Context, job provider.Job) (provider.Result, error)
}

// Output is an accepted plan. Result is the result of the accepted attempt, with the turns, the
// tokens, the cost, the tool calls and the refused calls summed over every attempt. Run returns
// the same totals with an error, so that a failed job can still be counted. Notes are where
// the accepted requests differ from an API document of the configuration.
type Output struct {
	Draft       Draft
	Scenarios   []domain.Scenario
	Scripts     []domain.Script
	Notes       []Note
	Attempts    int
	Result      provider.Result
	PlannerID   string
	PlannerKind string
}

// Rejected is the end of a plan whose every draft had problems. Nothing may be written (R16).
type Rejected struct {
	Problems []Problem
	Attempts int
}

func (r *Rejected) Error() string {
	return fmt.Sprintf("the planner's draft was rejected %d times; the last one had %d problems", r.Attempts, len(r.Problems))
}

// Run asks the planner for a draft and validates it. Problems go back to the planner with its
// previous draft in a file of the source copy, for at most MaxAttempts drafts. A provider
// failure ends the run at once.
func Run(ctx context.Context, p Planner, in Input) (Output, error) {
	var table strings.Builder
	if err := changeset.Table(&table, in.Change); err != nil {
		return Output{}, fmt.Errorf("plan: %w", err)
	}
	data := prompt.PlanData{
		ChangeTable:  strings.TrimRight(table.String(), "\n"),
		Services:     codeList(in.Services),
		Methods:      strings.Join(in.Methods, ", "),
		MaxScenarios: in.Limits.MaxScenarios,
		Existing:     existingList(in.Existing),
		Knowledge:    knowledgeList(in.Knowledge),
		Fixtures:     fixtureList(in.Fixtures),
	}
	var total provider.Result
	var problems []Problem
	var previous []byte
	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		if attempt > 1 {
			if err := atomicfile.Write(filepath.Join(in.SourceDir, PlanDir, "previous.json"), previous); err != nil {
				return Output{}, fmt.Errorf("plan: keep the previous draft: %w", err)
			}
			data.Attempt = true
			data.Problems = problemList(problems)
		}
		text, err := prompt.Plan(data)
		if err != nil {
			return Output{}, fmt.Errorf("plan: %w", err)
		}
		var report func(provider.Progress)
		if in.Progress != nil {
			prev := len(problems)
			in.Progress(Progress{Attempt: attempt, Problems: prev})
			report = func(pp provider.Progress) { in.Progress(Progress{Attempt: attempt, Problems: prev, Progress: pp}) }
		}
		res, err := p.Run(ctx, provider.Job{
			Prompt:    text,
			Workdir:   in.SourceDir,
			Schema:    Schema(),
			Model:     in.Model,
			Reasoning: in.Reasoning,
			MaxTurns:  in.Limits.MaxTurns,
			Timeout:   in.Limits.Timeout,
			Progress:  report,
		})
		sum(&total, res)
		if err != nil {
			return Output{Result: total, Attempts: attempt, PlannerID: p.ID(), PlannerKind: p.Kind()}, fmt.Errorf("plan, attempt %d: %w", attempt, err)
		}
		previous = res.Output

		d, err := DecodeDraft(res.Output)
		if err != nil {
			problems = []Problem{{Message: "the output is not a draft: " + err.Error()}}
			continue
		}
		scenarios, scripts, ps := Validate(d, in)
		if len(ps) > 0 {
			problems = ps
			continue
		}
		total.Output = res.Output
		return Output{
			Draft: d, Scenarios: scenarios, Scripts: scripts, Notes: compare(in, scenarios, scripts),
			Attempts: attempt, Result: total, PlannerID: p.ID(), PlannerKind: p.Kind(),
		}, nil
	}
	return Output{Result: total, Attempts: MaxAttempts, PlannerID: p.ID(), PlannerKind: p.Kind()}, &Rejected{Problems: problems, Attempts: MaxAttempts}
}

// sum adds one attempt to the totals. The limits, the loaded setup, and the model are those
// of the latest attempt that reported them.
func sum(total *provider.Result, res provider.Result) {
	total.Turns += res.Turns
	total.InputTokens += res.InputTokens
	total.OutputTokens += res.OutputTokens
	total.CacheReadTokens += res.CacheReadTokens
	total.CacheWriteTokens += res.CacheWriteTokens
	total.CostUSD += res.CostUSD
	total.Denied += res.Denied
	for _, u := range res.Tools {
		i := slices.IndexFunc(total.Tools, func(t provider.ToolUse) bool { return t.Name == u.Name })
		if i < 0 {
			total.Tools = append(total.Tools, u)
			continue
		}
		total.Tools[i].Calls += u.Calls
	}
	slices.SortFunc(total.Tools, func(a, b provider.ToolUse) int { return strings.Compare(a.Name, b.Name) })
	if len(res.Limits) > 0 {
		total.Limits = res.Limits
	}
	if res.Loaded.PermissionMode != "" || res.Loaded.Skills > 0 || len(res.Loaded.MCPServers) > 0 {
		total.Loaded = res.Loaded
	}
	if res.Model != "" {
		total.Model = res.Model
	}
	if res.Reasoning != "" {
		total.Reasoning = res.Reasoning
	}
}

// problemList is a Markdown list with one problem per line. The messages quote the draft, so
// each is kept to one line and cut.
func problemList(ps []Problem) string {
	var b strings.Builder
	for i, p := range ps {
		if i == maxProblemsShown {
			fmt.Fprintf(&b, "- and %d more problems\n", len(ps)-i)
			break
		}
		msg := oneLine(p.Message, maxProblemRunes)
		if p.Path == "" {
			fmt.Fprintf(&b, "- %s\n", msg)
		} else {
			fmt.Fprintf(&b, "- %s: %s\n", code(p.Path), msg)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func existingList(existing map[string][]domain.Scenario) string {
	var b strings.Builder
	for _, ticket := range slices.Sorted(maps.Keys(existing)) {
		for _, sc := range existing[ticket] {
			fmt.Fprintf(&b, "  - %s (%s): %s\n", oneLine(string(sc.ID), 80), oneLine(ticket, 80), oneLine(sc.Title, maxProblemRunes))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func codeList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = code(n)
	}
	return strings.Join(quoted, ", ")
}

func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if rs := []rune(s); len(rs) > limit {
		return string(rs[:limit]) + "…"
	}
	return s
}

func code(s string) string {
	return "`" + strings.ReplaceAll(oneLine(s, maxProblemRunes), "`", "'") + "`"
}
