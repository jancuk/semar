package plan

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/jancuk/semar/internal/changeset"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/prompt"
	"github.com/jancuk/semar/internal/provider"
)

// MaxAttempts bounds the drafts one plan asks for (A4).
const MaxAttempts = 3

// RepairMaxTurns is the turn limit of an attempt that repairs a draft. The repair has the draft and
// its problems in the prompt and changes only what the problems name, so it needs a fraction of the
// turns of a plan. A smaller plan.maxTurns still applies.
const RepairMaxTurns = 12

const (
	maxProblemsShown = 60
	maxProblemRunes  = 400
)

// Progress is where a plan is. Attempt starts at 1. The criteria pipeline has no attempt: it
// reports how many of its criteria are answered, in Done of Total.
type Progress struct {
	Attempt     int
	Problems    int // the problems of the previous draft, 0 on the first attempt
	Done, Total int // the criteria that are answered, and all of them; Total is 0 outside the criteria pipeline
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
// the accepted requests differ from an API document of the configuration. Attempts is the
// number of drafts asked for, and UsedAttempt the one that was written: they differ only when
// the last drafts failed and an earlier draft that passed was kept (D9).
type Output struct {
	Draft       Draft
	Scenarios   []domain.Scenario
	Scripts     []domain.Script
	Notes       []Note
	Attempts    int
	UsedAttempt int
	Result      provider.Result
	PlannerID   string
	PlannerKind string
	Trial       *Trial              // the trial of the accepted draft; nil when none ran
	AfterTrial  []domain.ScenarioID // scenarios first drafted after a trial
	// TrialReturned counts the trial problems that went back to the planner over all attempts.
	TrialReturned int
	// Refused are the drafts that went back to the planner, with their problems, in order.
	Refused []RefusedDraft
	// Unaccounted are the acceptance criteria that nothing in the plan cites, after the last
	// attempt (D9). Code writes each as an obligation with a gap, so the ledger never loses it.
	Unaccounted []Criterion
	// Pipeline, Outcomes, and Answers are set by the criteria pipeline only. Answers are the accepted
	// outputs of its jobs, which the next plan of the same tickets can reuse.
	Pipeline *PipelineStats
	Outcomes []CriterionOutcome
	Answers  []StoredAnswer
	// Again is what a second plan did with each criterion: kept, new, or changed, and the entries of
	// criteria that the ticket no longer has. It is set by the criteria pipeline only.
	Again *Again
	// WebTests are the tests that flows wrote for web criteria. They are already on disk, replayed once, and
	// the plan lists them and links their use cases. WebOff is why no web criterion was explored, or "".
	WebTests []WebTest
	WebOff   string
	// FixtureNeeds are the distinct fixtures that the answers named and no catalogue entry gives.
	FixtureNeeds []FixtureNeed
	// FixtureDrafts are the fixtures that the plan drafted for them, or tried to, in the same order.
	FixtureDrafts []FixtureDraft
	// CostLimit says that Input.MaxCost stopped work that the plan would have done: a job that did not
	// start, or an attempt that was not asked for.
	CostLimit bool
	// MaxCost is the limit the plan ran under, 0 for none.
	MaxCost float64
}

// CostLimitError ends a plan whose cost reached Input.MaxCost before any draft passed.
type CostLimitError struct {
	Limit, Spent float64
	Attempts     int
}

func (e *CostLimitError) Error() string {
	return fmt.Sprintf("the cost limit of %s was reached after %d attempts, which spent %s, and no draft passed", Dollars(e.Limit), e.Attempts, Dollars(e.Spent))
}

// Dollars writes an amount as $0.02, or with more digits when it has them, such as $0.125.
func Dollars(x float64) string {
	s := strconv.FormatFloat(x, 'f', -1, 64)
	switch i := strings.IndexByte(s, '.'); {
	case i < 0:
		s += ".00"
	case len(s)-i-1 == 1:
		s += "0"
	}
	return "$" + s
}

// RefusedDraft is a draft that was not used: a problem of its own, or a problem that the trial
// found in it. Problems is every problem of the draft, uncut.
type RefusedDraft struct {
	Attempt  int
	Problems []Problem
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
// previous draft in the prompt, to be repaired in a job of at most RepairMaxTurns turns, for at
// most MaxAttempts drafts. An output that is not a draft cannot be repaired, so the next attempt
// plans again with the full turn limit. A provider failure ends the run at once, unless an
// earlier draft passed validation: that draft is kept.
func Run(ctx context.Context, p Planner, in Input) (Output, error) {
	var table strings.Builder
	if err := changeset.Table(&table, in.Change); err != nil {
		return Output{}, fmt.Errorf("plan: %w", err)
	}
	shown := chooseDiff(in.Diff, in.Change.Files, MaxDiffBytes)
	data := prompt.PlanData{
		ChangeTable:  strings.TrimRight(table.String(), "\n"),
		LeftOut:      leftOutList(in.LeftOut),
		DiffWhole:    shown.Whole,
		DiffOmitted:  strings.Join(shown.Omitted, "\n"),
		API:          apiExcerpt(in.Knowledge, in.Diff),
		Services:     codeList(in.Services),
		Methods:      strings.Join(in.Methods, ", "),
		MaxScenarios: in.Limits.MaxScenarios,
		Existing:     existingList(in.Existing),
		Knowledge:    knowledgeList(in.Knowledge),
		Fixtures:     fixtureList(in.Fixtures),
		Tickets:      ticketList(in.Tickets, ticketKeys(in.Tickets)),
		Accounts:     accountList(in.Accounts),
	}
	if shown.Text != "" {
		data.Diff = fenced("diff", shown.Text)
	}
	var total provider.Result
	var problems []Problem
	var previous []byte
	var repairable bool // the previous output is a draft
	var passed *accepted
	frozen := newFrozen()
	returned := 0
	var refused []RefusedDraft
	headAPI := headDocuments(in.Knowledge)
	finish := func(a *accepted, attempts int) Output {
		total.Output = a.output
		return Output{
			Draft: a.draft, Scenarios: a.scenarios, Scripts: a.scripts, Notes: a.notes,
			Attempts: attempts, UsedAttempt: a.attempt, Result: total, PlannerID: p.ID(), PlannerKind: p.Kind(),
			Trial: a.trial, AfterTrial: a.after, TrialReturned: returned, Refused: slices.Clone(refused),
			Unaccounted: a.unaccounted,
		}
	}
	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		if attempt > 1 && in.MaxCost > 0 && total.CostUSD >= in.MaxCost {
			// One job cannot be cut, so the single planner is held between jobs.
			if passed != nil {
				out := finish(passed, attempt-1)
				out.CostLimit, out.MaxCost = true, in.MaxCost
				return out, nil
			}
			return Output{Result: total, Attempts: attempt - 1, PlannerID: p.ID(), PlannerKind: p.Kind()},
				&CostLimitError{Limit: in.MaxCost, Spent: total.CostUSD, Attempts: attempt - 1}
		}
		turns := in.Limits.MaxTurns
		if attempt > 1 {
			data.Attempt = true
			data.Problems = problemList(problems)
			data.Previous = ""
			if repairable {
				data.Previous = fenced("json", string(previous))
				turns = min(turns, RepairMaxTurns)
			}
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
			MaxTurns:  turns,
			Timeout:   in.Limits.Timeout,
			Progress:  report,
		})
		sum(&total, res)
		if err != nil && passed != nil && ctx.Err() == nil {
			return finish(passed, attempt), nil
		}
		if err != nil {
			return Output{Result: total, Attempts: attempt, PlannerID: p.ID(), PlannerKind: p.Kind()}, fmt.Errorf("plan, attempt %d: %w", attempt, err)
		}
		previous = res.Output

		d, err := DecodeDraft(res.Output)
		repairable = err == nil
		if err != nil {
			problems = []Problem{{Message: "the output is not a draft: " + err.Error()}}
			refused = append(refused, RefusedDraft{Attempt: attempt, Problems: problems})
			continue
		}
		scenarios, scripts, ps := Validate(d, in)
		if len(ps) == 0 {
			ps = changedExpectations(frozen, scenarios)
		}
		if len(ps) > 0 {
			problems = ps
			refused = append(refused, RefusedDraft{Attempt: attempt, Problems: ps})
			continue
		}
		cand := &accepted{
			draft: d, scenarios: scenarios, scripts: scripts, notes: compare(in, scenarios, scripts),
			attempt: attempt, output: res.Output, after: afterTrial(frozen, scenarios),
			unaccounted: Unaccounted(d, in.Tickets),
		}
		passed = cand
		if len(cand.unaccounted) > 0 && attempt < MaxAttempts {
			problems = unaccountedProblems(cand.unaccounted)
			refused = append(refused, RefusedDraft{Attempt: attempt, Problems: problems})
			continue
		}
		if in.Trial == nil {
			return finish(cand, attempt), nil
		}
		tr, err := in.Trial.Try(ctx, scenarios, scripts)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return Output{Result: total, Attempts: attempt, PlannerID: p.ID(), PlannerKind: p.Kind()}, fmt.Errorf("plan, attempt %d: %w", attempt, err)
			}
			tr.Skipped = "the trial stopped: " + err.Error()
		}
		cand.trial = &tr
		if tr.Skipped != "" {
			return finish(cand, attempt), nil
		}
		freeze(frozen, scenarios)
		back, _ := trialProblems(tr, scenarios, in.BaseAPI, headAPI, in.AuthProfile)
		if len(back) == 0 || attempt == MaxAttempts {
			return finish(cand, attempt), nil
		}
		problems = back
		refused = append(refused, RefusedDraft{Attempt: attempt, Problems: back})
		returned += len(back)
	}
	if passed != nil {
		return finish(passed, MaxAttempts), nil
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

// oneLine is s on one line and never longer than limit: a cut text ends with an ellipsis that counts.
func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if rs := []rune(s); len(rs) > limit {
		return string(rs[:limit-1]) + "…"
	}
	return s
}

func code(s string) string {
	return "`" + strings.ReplaceAll(oneLine(s, maxProblemRunes), "`", "'") + "`"
}
