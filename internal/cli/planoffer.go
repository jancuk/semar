package cli

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/knowledge"
	"github.com/jancuk/semar/internal/stage/plan"
	"github.com/jancuk/semar/internal/usage"
)

// PlanEstimate says what planning some tickets will use: who plans, with which model, and what it
// costs. Cost is empty when nothing can say yet.
type PlanEstimate struct {
	Provider, Model, Cost string
}

// planEstimator gives the estimate that semar run shows before it offers to plan (Plan 26, D6).
type planEstimator func(ctx context.Context, o options, dir string, cfg config.Config, tickets []string) PlanEstimate

// usageEstimate names the provider and the model that would plan first, and what the criteria of the
// tickets cost on them in the usage log (Plan 27, D12). What it cannot read stays empty, and the
// offer then says that there is no estimate yet.
func usageEstimate(_ context.Context, o options, dir string, cfg config.Config, tickets []string) PlanEstimate {
	p := cfg.PlanProvider()
	est := PlanEstimate{Provider: p.ID, Model: p.Model}
	id, model, err := firstRung(o, dir, cfg)
	if err != nil {
		return est
	}
	est.Provider, est.Model = id, model
	home, err := o.home()
	if err != nil {
		return est
	}
	again := criteriaAgain(dir, cfg, tickets)
	criteria := again.Count(plan.StateNew) + again.Count(plan.StateChanged)
	if criteria == 0 {
		return est
	}
	if e, err := usage.EstimateCost(home, id, model, criteria); err == nil && e.Known {
		est.Cost = e.String()
	}
	return est
}

func (e PlanEstimate) String() string {
	who := e.Provider
	if who == "" {
		who = "the planner"
	}
	model := e.Model
	if model == "" {
		model = "its default model"
	}
	cost := e.Cost
	if cost == "" {
		cost = "no estimate yet"
	}
	return fmt.Sprintf("%s with %s, %s", who, model, cost)
}

// criteriaAgain is what a plan would do with the criteria of the tickets that have a ticket file: the
// plan of a criterion that has a test or a manual check costs nothing, so an estimate counts the rest.
func criteriaAgain(dir string, cfg config.Config, keys []string) plan.Again {
	tickets := readTickets(dir, cfg, keys)
	scs, err := loadAllScenarios(dir)
	if err != nil {
		return plan.Classify(tickets, nil)
	}
	inChange := map[string]bool{}
	for _, k := range keys {
		inChange[k] = true
	}
	return plan.Classify(tickets, readEarlier(dir, inChange, scs))
}

// ticketsWithMissingCriteria is the tickets of keys with a criterion that no obligation entry names, or
// one that was edited since a plan wrote its test (D12). A criterion that a plan could not draft has an
// entry, so it does not count: a plan would try it again and fail the same way.
func ticketsWithMissingCriteria(dir string, cfg config.Config, keys []string) []string {
	var out []string
	for _, s := range criteriaAgain(dir, cfg, keys).Criteria {
		if (s.State == plan.StateNew && !s.Entered || s.State == plan.StateChanged) && !slices.Contains(out, s.Ticket) {
			out = append(out, s.Ticket)
		}
	}
	slices.Sort(out)
	return out
}

// readTickets is the ticket files of keys in the tickets source of the repository.
func readTickets(dir string, cfg config.Config, keys []string) []knowledge.Ticket {
	list := cfg.Knowledge
	if len(list) == 0 {
		list = knowledge.Conventional(dir)
	}
	sources, err := knowledge.Resolve(dir, list)
	if err != nil {
		return nil
	}
	return knowledge.ReadTickets(dir, sources, keys)
}

// ticketsWithFiles is the tickets of keys that have a ticket file.
func ticketsWithFiles(dir string, cfg config.Config, keys []string) []string {
	var out []string
	for _, t := range readTickets(dir, cfg, keys) {
		out = append(out, t.Key)
	}
	slices.Sort(out)
	return out
}

// offerToPlan asks whether to plan the tickets, with the estimate, and says whether the person agreed.
// fresh are tickets with a ticket file and no use case. missing have use cases, and a criterion that
// none of them names.
func offerToPlan(ctx context.Context, stdout io.Writer, in *lineReader, est PlanEstimate, fresh, missing []string) bool {
	verb := func(keys []string) string { return map[bool]string{true: "has", false: "have"}[len(keys) == 1] }
	var what []string
	if len(fresh) > 0 {
		what = append(what, fmt.Sprintf("%s %s a ticket file and no use cases", strings.Join(fresh, ", "), verb(fresh)))
	}
	if len(missing) > 0 {
		what = append(what, fmt.Sprintf("%s %s criteria with no test or manual check yet", strings.Join(missing, ", "), verb(missing)))
	}
	fmt.Fprintf(stdout, "%s. Plan %s now with %s? [y/N] ",
		strings.Join(what, ", and "), map[bool]string{true: "it", false: "them"}[len(fresh)+len(missing) == 1], est)
	line, ok := in.read(ctx)
	if !ok {
		fmt.Fprintln(stdout)
		return false
	}
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes"
}
