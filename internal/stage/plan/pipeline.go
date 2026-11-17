package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/prompt"
	"github.com/jancuk/semar/internal/provider"
)

const (
	// MaxParallelJobs is how many criteria are answered at once.
	MaxParallelJobs = 4
	// CriterionMaxTurns is the turn limit of a job for one criterion. A job has no tools: it
	// answers once, and a harness may use a second turn to return the answer.
	CriterionMaxTurns = 2
)

// Rung is one model of the ladder: a provider, the model it runs, and the reasoning level.
type Rung struct {
	Planner   Planner
	Model     string
	Reasoning string
}

// Label is how the model use line names the rung: its model, or its provider when it has none.
func (r Rung) Label() string {
	if r.Model != "" {
		return r.Model
	}
	return r.Planner.ID()
}

// StoredAnswer is an accepted output of a job with the hash of the packet it answered. A later
// plan of the same ticket gives the answer back to the pipeline, which reuses it when the hash of
// the packet is the same.
type StoredAnswer struct {
	Ticket string          `json:"ticket"`
	Cite   string          `json:"cite"`
	Hash   string          `json:"hash"`
	Model  string          `json:"model,omitempty"`
	Output json.RawMessage `json:"output"`
}

// ModelCount is how many criteria a model answered.
type ModelCount struct {
	Model   string
	Answers int
}

// ModelUse is what the calls to one rung used.
type ModelUse struct {
	PlannerID, Kind, Model, Reasoning string
	Calls                             int
	Took                              time.Duration
	Result                            provider.Result
}

// PipelineStats is what a plan of the criteria pipeline did.
type PipelineStats struct {
	Criteria    int
	Calls       int          // every call to a model: first jobs, corrections, and jobs of a higher rung
	ByModel     []ModelCount // the criteria that each model answered, in the order of the ladder
	Corrections int          // calls that repaired an answer
	Escalated   int          // criteria that a higher rung answered or failed
	Reused      int          // criteria whose stored answer was reused without a call
	Unanswered  int          // criteria that no rung answered
	Uses        []ModelUse   // one for each rung that was called, in the order of the ladder
	Labels      []string     // the label of each rung of the ladder
}

// modelOf is the label of the model of a rung, "" when there is none.
func (st *PipelineStats) modelOf(rung int) string {
	if rung < 0 || rung >= len(st.Labels) {
		return ""
	}
	return st.Labels[rung]
}

// CriterionOutcome says how one criterion was answered.
type CriterionOutcome struct {
	Packet      Packet
	Result      CriterionResult
	Answered    bool
	Rung        int  // the rung that answered, or the last that tried
	Corrections int  // calls that repaired an answer of this criterion
	Reused      bool // the answer is a stored answer of an earlier plan
	Problems    []Problem
	Output      []byte // the accepted output; the last output when there is none
}

type runState struct {
	in    Input
	mu    sync.Mutex
	total provider.Result
	uses  []ModelUse
	calls int
	corr  int
	done  int
	cache map[string]StoredAnswer
}

// RunCriteria plans one criterion at a time. Code builds a packet for each criterion of each
// ticket file. A job with no tools answers each packet in a closed schema, and code checks the
// answer. An answer with a problem goes back once to the same model with the problems in plain
// words, and then to the next model of the ladder. Code puts the accepted answers into one draft
// and checks the draft as a whole, so that everything downstream is the same as for Run.
func RunCriteria(ctx context.Context, in Input) (Output, error) {
	if len(in.Ladder) == 0 {
		return Output{}, errors.New("plan: the criteria pipeline needs at least one model")
	}
	packets := Packets(in, prompt.CriterionVersion())
	if len(packets) == 0 {
		return Output{}, errors.New("plan: the criteria pipeline plans from the acceptance criteria in the ticket files, and the change has none; " +
			"list a tickets folder under knowledge in semar.yaml, and write each ticket's criteria under a heading that holds \"Acceptance criteria\", or plan with --pipeline single")
	}
	rs := &runState{in: in, uses: make([]ModelUse, len(in.Ladder)), cache: map[string]StoredAnswer{}}
	for i, r := range in.Ladder {
		rs.uses[i] = ModelUse{PlannerID: r.Planner.ID(), Kind: r.Planner.Kind(), Model: r.Label(), Reasoning: r.Reasoning}
	}
	for _, a := range in.Previous { // the newest plan comes first and wins
		if _, ok := rs.cache[a.Hash]; !ok {
			rs.cache[a.Hash] = a
		}
	}

	outcomes := make([]CriterionOutcome, len(packets))
	if in.Progress != nil {
		in.Progress(Progress{Total: len(packets)})
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(MaxParallelJobs)
	for i, p := range packets {
		g.Go(func() error {
			o, err := rs.answer(gctx, p)
			if err != nil {
				return fmt.Errorf("criterion %s: %w", p.Cite, err)
			}
			outcomes[i] = o
			rs.finished(len(packets))
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return rs.failed(), err
	}

	b, ps := rs.build(outcomes)
	if len(ps) > 0 {
		out := rs.failed()
		return out, &Rejected{Problems: ps, Attempts: 1}
	}
	var refused []RefusedDraft
	var tr *Trial
	returned := 0
	if in.Trial != nil && len(b.Scenarios) > 0 {
		var err error
		if b, tr, returned, refused, err = rs.trial(ctx, b); err != nil {
			return rs.failed(), err
		}
	}
	for i := range b.Scripts {
		o := b.Outcomes[b.Owners[i]]
		b.Scripts[i].AuthoredBy = domain.ProviderRef{Instance: in.Ladder[o.Rung].Planner.ID(), Model: rs.modelName(o.Rung), Harness: in.Ladder[o.Rung].Planner.Kind()}
	}

	out := rs.output(b.Outcomes)
	out.Draft, out.Scenarios, out.Scripts = b.Draft, b.Scenarios, b.Scripts
	out.Notes = compare(in, b.Scenarios, b.Scripts)
	out.Trial, out.TrialReturned, out.Refused = tr, returned, refused
	out.Unaccounted = Unaccounted(b.Draft, in.Tickets)
	return out, nil
}

// built is a draft made from the outcomes, checked as a whole. Owners[i] is the outcome that gave
// the i-th scenario.
type built struct {
	Outcomes  []CriterionOutcome
	Draft     Draft
	Scenarios []domain.Scenario
	Scripts   []domain.Script
	Owners    []int
}

func (rs *runState) build(outcomes []CriterionOutcome) (built, []Problem) {
	d, owners := assemble(rs.in, outcomes)
	scenarios, scripts, ps := Validate(d, rs.in)
	return built{outcomes, d, scenarios, scripts, owners}, ps
}

// modelName is the model that a rung reported, else the one that was asked for.
func (rs *runState) modelName(rung int) string {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if m := rs.uses[rung].Result.Model; m != "" {
		return m
	}
	return rs.in.Ladder[rung].Model
}

func (rs *runState) finished(total int) {
	rs.mu.Lock()
	rs.done++
	done := rs.done
	rs.mu.Unlock()
	if rs.in.Progress != nil {
		rs.in.Progress(Progress{Done: done, Total: total})
	}
}

// failed is the output of a run that stopped: the tokens and the calls that were spent.
func (rs *runState) failed() Output {
	out := rs.output(nil)
	out.Attempts = rs.calls
	return out
}

func (rs *runState) output(outcomes []CriterionOutcome) Output {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	st := &PipelineStats{Criteria: len(outcomes), Calls: rs.calls, Corrections: rs.corr}
	for _, r := range rs.in.Ladder {
		st.Labels = append(st.Labels, r.Label())
	}
	counts := make([]int, len(rs.in.Ladder))
	var answers []StoredAnswer
	for _, o := range outcomes {
		switch {
		case !o.Answered:
			st.Unanswered++
			continue
		case o.Reused:
			st.Reused++
		}
		if o.Rung > 0 {
			st.Escalated++
		}
		counts[o.Rung]++
		answers = append(answers, StoredAnswer{Ticket: o.Packet.Ticket, Cite: o.Packet.Cite, Hash: o.Packet.Hash(), Model: rs.in.Ladder[o.Rung].Label(), Output: slices.Clone(o.Output)})
	}
	for i, u := range rs.uses {
		if u.Calls == 0 && counts[i] == 0 {
			continue
		}
		st.Uses = append(st.Uses, u)
		st.ByModel = append(st.ByModel, ModelCount{Model: u.Model, Answers: counts[i]})
	}
	total := rs.total
	total.Model = ""
	first := rs.in.Ladder[0].Planner
	return Output{
		Attempts: rs.calls, UsedAttempt: 1, Result: total, PlannerID: first.ID(), PlannerKind: first.Kind(),
		Pipeline: st, Outcomes: outcomes, Answers: answers,
	}
}

// call runs one job at a rung and adds it to the totals. A provider error is returned.
func (rs *runState) call(ctx context.Context, rung int, p Packet, previous []byte, problems []Problem) ([]byte, error) {
	text, err := p.Prompt(previous, problems)
	if err != nil {
		return nil, err
	}
	r := rs.in.Ladder[rung]
	dir := rs.in.JobDir
	if dir == "" {
		dir = rs.in.SourceDir
	}
	started := time.Now()
	res, err := r.Planner.Run(ctx, provider.Job{
		Prompt: text, Workdir: dir, Schema: p.AnswerSchema(), Model: r.Model, Reasoning: r.Reasoning,
		MaxTurns: CriterionMaxTurns, Timeout: rs.in.Limits.Timeout, NoTools: true,
	})
	rs.mu.Lock()
	sum(&rs.total, res)
	u := &rs.uses[rung]
	u.Calls++
	u.Took += time.Since(started)
	sumUse(&u.Result, res)
	rs.calls++
	if len(problems) > 0 {
		rs.corr++
	}
	rs.mu.Unlock()
	return res.Output, err
}

// sumUse adds the tokens of a call to a rung's totals and keeps the model that it reported.
func sumUse(total *provider.Result, res provider.Result) {
	model := total.Model
	sum(total, res)
	if res.Model == "" {
		total.Model = model
	}
}

// check reads an output and runs the checks of the packet. An output that is no answer is a
// problem like any other.
func (rs *runState) check(p Packet, raw []byte) (CriterionResult, []Problem) {
	a, err := DecodeAnswer(raw)
	if err != nil {
		return CriterionResult{}, []Problem{{Message: "the output is not an answer in the schema: " + err.Error()}}
	}
	return CheckAnswer(rs.in, p, a)
}

// answer asks the ladder for one criterion: a job, a correction of its answer, and then the same
// again at the next rung. A stored answer of an earlier plan with the same packet hash stands in
// for the first job of the first rung, and goes through the same check.
func (rs *runState) answer(ctx context.Context, p Packet) (CriterionOutcome, error) {
	o := CriterionOutcome{Packet: p}
	for rung := range rs.in.Ladder {
		var raw []byte
		var res CriterionResult
		var ps []Problem
		fromCache := false
		if stored, ok := rs.cache[p.Hash()]; ok && rung == 0 {
			raw, fromCache = stored.Output, true
			if res, ps = rs.check(p, raw); len(ps) == 0 {
				o.Result, o.Answered, o.Reused, o.Rung, o.Output = res, true, true, rung, raw
				return o, nil
			}
		}
		if !fromCache {
			var err error
			if raw, err = rs.call(ctx, rung, p, nil, nil); err != nil {
				return o, err
			}
			if res, ps = rs.check(p, raw); len(ps) == 0 {
				o.Result, o.Answered, o.Rung, o.Output = res, true, rung, raw
				return o, nil
			}
		}
		o.Corrections++
		var err error
		if raw, err = rs.call(ctx, rung, p, raw, ps); err != nil {
			return o, err
		}
		if res, ps = rs.check(p, raw); len(ps) == 0 {
			o.Result, o.Answered, o.Rung, o.Output = res, true, rung, raw
			return o, nil
		}
		o.Rung, o.Problems, o.Output = rung, ps, raw
	}
	return o, nil
}

// assemble puts the accepted answers into one draft, in the order of the packets. A ticket's kind
// and the sentence of its behaviour come from code. owners[i] is the outcome of the i-th scenario.
func assemble(in Input, outcomes []CriterionOutcome) (Draft, []int) {
	d := Draft{Uncovered: []Uncovered{}}
	titles := map[string]bool{}
	for _, sc := range in.Existing {
		for _, s := range sc {
			titles[scenarioKey(s)] = true
		}
	}
	var owners []int
	var dropped []CriterionOutcome
	for i, o := range outcomes {
		if !o.Answered {
			continue
		}
		switch {
		case o.Result.Scenario != nil:
			if limit := in.Limits.MaxScenarios; limit > 0 && len(d.Scenarios) >= limit {
				dropped = append(dropped, o)
				continue
			}
			sd := *o.Result.Scenario
			key := sd.Ticket + "\x00" + normalTitle(sd.Title)
			if titles[key] {
				sd.Title = fmt.Sprintf("%s (criterion %d)", sd.Title, o.Packet.Number)
				key = sd.Ticket + "\x00" + normalTitle(sd.Title)
			}
			titles[key] = true
			d.Scenarios = append(d.Scenarios, sd)
			owners = append(owners, i)
		case o.Result.Manual != nil:
			d.ManualChecks = append(d.ManualChecks, *o.Result.Manual)
		case o.Result.NotDrafted != nil:
			d.NotDrafted = append(d.NotDrafted, *o.Result.NotDrafted)
		}
	}
	for _, o := range dropped {
		d.NotDrafted = append(d.NotDrafted, NotDrafted{
			Ticket: o.Packet.Ticket, What: o.Result.Scenario.Title,
			Needs: fmt.Sprintf("a higher limit: the plan has %d scenarios already, which is plan.maxScenarios or --max-scenarios", in.Limits.MaxScenarios),
			Cite:  o.Packet.Why(),
		})
	}
	planned := map[string]int{}
	for _, o := range outcomes {
		planned[o.Packet.Ticket]++
	}
	for _, t := range in.Change.Tickets {
		d.Tickets = append(d.Tickets, criteriaTicket(in, d, t.Key, planned[t.Key]))
	}
	scenarios, manual, nd := len(d.Scenarios), len(d.ManualChecks), len(d.NotDrafted)
	d.Summary = fmt.Sprintf("Planned one acceptance criterion at a time: %d scenarios, %d manual checks, and %d criteria or scenarios not drafted.", scenarios, manual, nd)
	return d, owners
}

// criteriaTicket says what code knows of a ticket: it changes behaviour when a criterion of it gave a
// scenario, a manual check, or a notDrafted entry.
func criteriaTicket(in Input, d Draft, key string, criteria int) TicketDraft {
	t := TicketDraft{Key: key, Kind: "none", Risk: "standard", Behaviour: ticketTitle(in, key)}
	covered := slices.ContainsFunc(d.Scenarios, func(s ScenarioDraft) bool { return s.Ticket == key }) ||
		slices.ContainsFunc(d.ManualChecks, func(m ManualCheckDraft) bool { return m.Ticket == key }) ||
		slices.ContainsFunc(d.NotDrafted, func(n NotDrafted) bool { return n.Ticket == key })
	switch {
	case covered:
		t.Kind = "behaviour"
		t.Reason = fmt.Sprintf("its %s were planned one at a time", counted(criteria, "acceptance criterion"))
	case criteria > 0:
		t.Reason = "no model gave an accepted answer for its acceptance criteria, so each is listed as a gap"
	default:
		t.Reason = "it has no acceptance criterion in a ticket file, so the criteria pipeline planned nothing for it: plan it with --pipeline single"
	}
	return t
}

// ticketTitle is the first heading of the ticket file without the key, else the key.
func ticketTitle(in Input, key string) string {
	for _, t := range in.Tickets {
		if t.Key != key {
			continue
		}
		for _, line := range strings.Split(t.Text, "\n") {
			if rest, ok := strings.CutPrefix(line, "# "); ok {
				rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), key))
				rest = strings.TrimSpace(strings.TrimLeft(rest, ":-– "))
				if rest != "" {
					return oneLine(rest, 200)
				}
			}
		}
	}
	return key
}

// trial sends the read-only steps of the draft to the environment once. The problems that it finds
// go back to the criterion that owns the scenario, in one correction at the rung that answered it.
// A correction counts only when the expectations of the scenario are the same, because the trial
// says what the request does and never what to expect. When something changed, the draft is checked
// again and tried again.
func (rs *runState) trial(ctx context.Context, b built) (built, *Trial, int, []RefusedDraft, error) {
	in := rs.in
	tr, err := in.Trial.Try(ctx, b.Scenarios, b.Scripts)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return b, nil, 0, nil, fmt.Errorf("plan, trial: %w", err)
		}
		tr.Skipped = "the trial stopped: " + err.Error()
	}
	if tr.Skipped != "" {
		return b, &tr, 0, nil, nil
	}
	frozen := newFrozen()
	freeze(frozen, b.Scenarios)
	back, _ := trialProblems(tr, b.Scenarios, in.BaseAPI, headDocuments(in.Knowledge), in.AuthProfile)
	if len(back) == 0 {
		return b, &tr, 0, nil, nil
	}
	refused := []RefusedDraft{{Attempt: 1, Problems: back}}
	byOutcome := map[int][]Problem{}
	for _, pr := range back {
		if i, ok := scenarioIndex(pr.Path); ok && i < len(b.Owners) {
			byOutcome[b.Owners[i]] = append(byOutcome[b.Owners[i]], pr)
		}
	}
	repaired := slices.Clone(b.Outcomes)
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(MaxParallelJobs)
	for idx, ps := range byOutcome {
		g.Go(func() error {
			o := b.Outcomes[idx]
			raw, err := rs.call(gctx, o.Rung, o.Packet, o.Output, ps)
			if err != nil {
				return fmt.Errorf("criterion %s: %w", o.Packet.Cite, err)
			}
			res, found := rs.check(o.Packet, raw)
			if len(found) > 0 || res.Scenario == nil {
				return nil
			}
			mu.Lock()
			defer mu.Unlock()
			o.Result, o.Output, o.Corrections = res, raw, o.Corrections+1
			repaired[idx] = o
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return b, nil, 0, nil, err
	}
	b2, ps := rs.build(repaired)
	if len(ps) > 0 || len(changedExpectations(frozen, b2.Scenarios)) > 0 {
		return b, &tr, len(back), refused, nil
	}
	tr2, err := in.Trial.Try(ctx, b2.Scenarios, b2.Scripts)
	if err != nil {
		if ctx.Err() != nil {
			return b, nil, 0, nil, fmt.Errorf("plan, trial: %w", err)
		}
		tr2.Skipped = "the trial stopped: " + err.Error()
	}
	return b2, &tr2, len(back), refused, nil
}

// scenarioIndex reads i from a problem path that starts with scenarios[i].
func scenarioIndex(path string) (int, bool) {
	rest, ok := strings.CutPrefix(path, "scenarios[")
	if !ok {
		return 0, false
	}
	num, _, ok := strings.Cut(rest, "]")
	if !ok || num == "" || strings.Trim(num, "0123456789") != "" {
		return 0, false
	}
	return atoi(num), true
}
