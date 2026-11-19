package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/edge"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/openapi"
	"github.com/jancuk/semar/internal/scenario"
	"github.com/jancuk/semar/internal/script"
	"github.com/jancuk/semar/internal/stage/plan"
)

const (
	edgePerOperation = 6
	// The reasons that the trial gives for a step that it could not send and that say nothing about the
	// answer of the environment. A step that a rule refused is not one of them.
	trialWrites = "writes"
	trialNeeds  = "needs "
	trialBudget = "the trial budget was reached"

	reasonNotTried = "its request could not be tried as another account"
	reasonInvalid  = "Semar could not write it as a valid scenario"
)

// edgeOn says whether this plan drafts edge cases.
func edgeOn(f planFlags, cfg config.Config) bool { return !f.noEdge && cfg.Plan.EdgeLimit() > 0 }

// edgeSource is a step of a new draft that edge cases copy.
type edgeSource struct {
	base domain.Scenario
	bs   domain.Script
	step domain.Step
	req  domain.APIRequest
	sent bool // the trial sent the step and it passed
}

// planEdge adds the edge cases to the plan and returns the line that says what it did. It drafts from
// each API step of a new draft whose operation the API document has and whose trial did not fail, with
// no model. It tries each case once, as a trial does, so a request that the rules of the environment
// refuse is never sent. A case is a scenario like any other: it counts as soon as the plan writes it.
func planEdge(ctx context.Context, f planFlags, cfg config.Config, in plan.Input, out *plan.Output, rig *trialRig, known map[domain.ScenarioID]bool, dropped map[string]bool) (string, error) {
	if !edgeOn(f, cfg) || rig == nil || out.Trial == nil || out.Trial.Skipped != "" {
		return "", nil
	}
	docs := apiDocuments(in)
	if len(docs) == 0 {
		return "", nil
	}

	var left []edge.Left
	var cands []edge.Candidate
	var srcs []edgeSource
	seen := map[string]bool{}
	for _, sc := range out.Scenarios {
		for _, st := range sc.Steps {
			src, op, ok := edgeSourceOf(sc, scriptOf(out.Scripts, sc.ID), st, out.Trial, docs)
			if !ok {
				continue
			}
			route := strings.ToUpper(st.Hint.Method) + " " + st.Hint.Path
			add := func(c edge.Case) {
				key := edge.Key(sc.Ticket, st.Hint, c.Kind, c.On)
				switch {
				case seen[key]:
				case dropped[key]:
					seen[key] = true
					left = append(left, edge.Left{Kind: c.Kind, On: c.On, Reason: edge.ReasonDropped})
				default:
					seen[key] = true
					cands = append(cands, edge.Candidate{Case: c, Operation: route, Order: len(srcs)})
					srcs = append(srcs, src)
				}
			}
			cases, lefts := edge.Generate(op, src.req)
			for _, c := range cases {
				add(c)
			}
			for _, l := range lefts {
				if key := edge.Key(sc.Ticket, st.Hint, l.Kind, l.On); !seen[key] {
					seen[key] = true
					left = append(left, l)
				}
			}
			read := rig.policy == nil || rig.policy.Classify(effects.Request{Method: op.Method, URL: &url.URL{Path: placeholders(op.Path)}}) == effects.Read
			for _, account := range rig.others {
				switch {
				case account == sc.AuthProfile:
				case !read:
					left = append(left, edge.Left{Kind: edge.OtherAccount, On: account, Reason: edge.ReasonWrite})
				case !src.sent:
					left = append(left, edge.Left{Kind: edge.OtherAccount, On: account, Reason: reasonNotTried})
				default:
					add(edge.OtherAccountCase(src.req, account))
				}
			}
		}
	}
	kept, over := edge.Select(cands, edgePerOperation, cfg.Plan.EdgeLimit())
	left = append(left, over...)

	kept, left, err := probeAccounts(ctx, in, srcs, kept, left)
	if err != nil {
		return "", err
	}

	number := edgeNumbers(known, out.Scenarios)
	type made struct {
		sc domain.Scenario
		s  domain.Script
		c  edge.Candidate
	}
	var list []made
	for _, c := range kept {
		src := srcs[c.Order]
		number[src.base.Ticket]++
		id := edge.IDOf(src.base.Ticket, number[src.base.Ticket])
		sc, s, err := buildEdge(src, c.Case, id, nil)
		if err != nil {
			number[src.base.Ticket]--
			left = append(left, edge.Left{Kind: c.Kind, On: c.On, Reason: edgeFailure(err)})
			continue
		}
		list = append(list, made{sc, s, c})
	}

	// Each case is tried once, as a draft is. The trial sends no write and nothing that a rule refuses.
	var tr *plan.Trial
	if len(list) > 0 {
		scs := make([]domain.Scenario, len(list))
		scripts := make([]domain.Script, len(list))
		for i, m := range list {
			scs[i], scripts[i] = m.sc, m.s
		}
		got, err := in.Trial.Try(ctx, scs, scripts)
		if err != nil && ctx.Err() != nil {
			return "", err
		}
		tr = &got
	}
	for _, m := range list {
		sc, s, err := buildEdge(srcs[m.c.Order], m.c.Case, m.sc.ID, tr)
		if err != nil {
			return "", fmt.Errorf("edge case %s: %w", m.sc.ID, err)
		}
		out.Scenarios, out.Scripts = append(out.Scenarios, sc), append(out.Scripts, s)
	}
	if len(list)+len(left) == 0 {
		return "", nil
	}
	return edge.Summary(len(list), left), nil
}

// buildEdge makes a case into a scenario and a script that a scenario file and a script store can hold.
func buildEdge(src edgeSource, c edge.Case, id domain.ScenarioID, tr *plan.Trial) (domain.Scenario, domain.Script, error) {
	sc, s, err := edge.Build(src.base, src.bs, src.step.ID, c, id, edgeIntent(src, c, id, tr))
	if err != nil {
		return sc, s, err
	}
	if ps := scenario.Validate(sc, scenario.FromFile); len(ps) > 0 {
		return sc, s, errors.New(ps[0].Path + ": " + ps[0].Message)
	}
	if ps := script.Validate(sc, s); len(ps) > 0 {
		return sc, s, errors.New(ps[0].Message)
	}
	return sc, s, nil
}

func edgeFailure(err error) string {
	if err.Error() == edge.ReasonFixture {
		return edge.ReasonFixture
	}
	return reasonInvalid
}

// edgeSourceOf says whether a step is a source of edge cases, and gives its operation. The step is an API
// step of the test, with a request that the API document has an operation for. Its trial did not fail: it
// was sent and passed, or the trial could not send it because it writes or needs a value that a write
// gives, which says nothing about its answer. A step that a rule refused is no source.
func edgeSourceOf(sc domain.Scenario, bs domain.Script, st domain.Step, tr *plan.Trial, docs []openapi.Document) (edgeSource, openapi.Operation, bool) {
	if st.Setup || st.As != "" || st.Hint == nil || sc.Edge != nil {
		return edgeSource{}, openapi.Operation{}, false
	}
	i := slices.IndexFunc(bs.Steps, func(x domain.ScriptStep) bool { return x.Step == st.ID })
	if i < 0 || bs.Steps[i].Request == nil {
		return edgeSource{}, openapi.Operation{}, false
	}
	ts, ok := trialStepOf(tr, sc.ID, st.ID)
	if !ok {
		return edgeSource{}, openapi.Operation{}, false
	}
	sent := false
	switch {
	case ts.Result == plan.TrialSent:
		if ts.Status < 200 || ts.Status > 399 || ts.Err != "" {
			return edgeSource{}, openapi.Operation{}, false
		}
		sent = true
	case ts.Why == trialWrites, strings.HasPrefix(ts.Why, trialNeeds), ts.Why == trialBudget:
	default:
		return edgeSource{}, openapi.Operation{}, false
	}
	for _, d := range docs {
		if m := d.Lookup(st.Hint.Method, st.Hint.Path); m.Found {
			return edgeSource{base: sc, bs: bs, step: st, req: *bs.Steps[i].Request, sent: sent}, m.Operation, true
		}
	}
	return edgeSource{}, openapi.Operation{}, false
}

func trialStepOf(tr *plan.Trial, sc domain.ScenarioID, step domain.StepID) (plan.TrialStep, bool) {
	if tr == nil {
		return plan.TrialStep{}, false
	}
	for _, ts := range tr.Steps {
		if ts.Scenario == sc && ts.Step == step && ts.Given == "" {
			return ts, true
		}
	}
	return plan.TrialStep{}, false
}

// probeAccounts tries each case of another account once, as that account, and gives it the status that
// came. A case that got no answer is left out.
func probeAccounts(ctx context.Context, in plan.Input, srcs []edgeSource, kept []edge.Candidate, left []edge.Left) ([]edge.Candidate, []edge.Left, error) {
	var scs []domain.Scenario
	var scripts []domain.Script
	at := map[domain.ScenarioID]int{}
	drop := map[int]bool{}
	for i, c := range kept {
		if c.Kind != edge.OtherAccount {
			continue
		}
		src := srcs[c.Order]
		id := domain.ScenarioID(fmt.Sprintf("%s-Eprobe%d", src.base.Ticket, i+1))
		sc, s, err := edge.Build(src.base, src.bs, src.step.ID, c.Case, id, "")
		if err != nil {
			drop[i] = true
			left = append(left, edge.Left{Kind: c.Kind, On: c.On, Reason: edgeFailure(err)})
			continue
		}
		at[id] = i
		scs, scripts = append(scs, sc), append(scripts, s)
	}
	if len(scs) == 0 {
		return kept, left, nil
	}
	tr, err := in.Trial.Try(ctx, scs, scripts)
	if err != nil && ctx.Err() != nil {
		return nil, nil, err
	}
	for _, sc := range scs {
		i := at[sc.ID]
		c := kept[i]
		ts, ok := trialStepOf(&tr, sc.ID, sc.Steps[len(sc.Steps)-1].ID)
		reason := ""
		switch {
		case !ok || ts.Result != plan.TrialSent:
			reason = reasonNotTried
		case ts.Status == 0:
			reason = edge.ReasonNoAnswer
		case ts.Status >= 500:
			reason = edge.ReasonServerFail
		}
		if reason != "" {
			drop[i] = true
			left = append(left, edge.Left{Kind: c.Kind, On: c.On, Reason: reason})
			continue
		}
		kept[i].Status = ts.Status
		kept[i].Cite = fmt.Sprintf("observed in the plan trial as account %s, not required by the API document", c.On)
	}
	var out []edge.Candidate
	for i, c := range kept {
		if !drop[i] {
			out = append(out, c)
		}
	}
	return out, left, nil
}

// edgeIntent is what the description of the case says besides its title: where it comes from, where its
// expected status comes from, and what the trial of the plan found.
func edgeIntent(src edgeSource, c edge.Case, id domain.ScenarioID, tr *plan.Trial) string {
	lines := []string{fmt.Sprintf("An edge case of %s: %s %s.", src.base.ID, src.step.Hint.Method+" "+src.step.Hint.Path, c.Title)}
	if c.Kind == edge.OtherAccount {
		lines = append(lines, fmt.Sprintf("The plan sent this read as account %s, and the environment answered %d. That is what came, and the API document does not require it.", c.On, c.Status))
	} else {
		lines = append(lines, fmt.Sprintf("The expected status %d comes from the API document.", c.Status))
	}
	lines = append(lines, "A fixed rule drafted it from the API document, and no model took part.")
	switch ts, ok := trialStepOf(tr, id, src.step.ID); {
	case tr == nil:
	case c.Kind == edge.OtherAccount:
		lines = append(lines, "Tried once in the plan, as that account.")
	case !ok:
		lines = append(lines, "Not tried in the plan.")
	case ts.Result == plan.TrialSent && ts.Status == c.Status:
		lines = append(lines, fmt.Sprintf("Tried once in the plan: the environment answered %d.", ts.Status))
	case ts.Result == plan.TrialSent:
		lines = append(lines, fmt.Sprintf("Tried once in the plan: the environment answered %d, and the API document declares %d.", ts.Status, c.Status))
	default:
		lines = append(lines, "Not tried in the plan: "+triedWhy(ts.Why)+".")
	}
	return strings.Join(lines, "\n")
}

func triedWhy(why string) string {
	if why == trialWrites {
		return "the request writes, and a trial never sends a write"
	}
	return why
}

// edgeNumbers is the highest number of an edge case of each ticket, among the scenarios that the
// repository holds and those of the plan.
func edgeNumbers(known map[domain.ScenarioID]bool, scenarios []domain.Scenario) map[string]int {
	tickets := map[string]bool{}
	for _, sc := range scenarios {
		tickets[sc.Ticket] = true
	}
	high := map[string]int{}
	for t := range tickets {
		for id := range known {
			high[t] = max(high[t], edge.Number(t, id))
		}
		for _, sc := range scenarios {
			high[t] = max(high[t], edge.Number(t, sc.ID))
		}
	}
	return high
}

func apiDocuments(in plan.Input) []openapi.Document {
	var docs []openapi.Document
	for _, s := range in.Knowledge {
		if s.Usable() && s.API != nil {
			docs = append(docs, *s.API)
		}
	}
	return docs
}

func scriptOf(scripts []domain.Script, id domain.ScenarioID) domain.Script {
	for _, s := range scripts {
		if s.Scenario == id {
			return s
		}
	}
	return domain.Script{}
}

// placeholders writes the parameters of a path template as plain segments, for a rule that matches paths.
func placeholders(p string) string {
	return strings.NewReplacer("{", "", "}", "").Replace(p)
}
