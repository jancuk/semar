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
	criteria := 0
	for _, t := range readTickets(dir, cfg, tickets) {
		criteria += len(plan.Criteria(t))
	}
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
func offerToPlan(ctx context.Context, stdout io.Writer, in *lineReader, est PlanEstimate, tickets []string) bool {
	fmt.Fprintf(stdout, "%s %s a ticket file and no use cases. Plan %s now with %s? [y/N] ",
		strings.Join(tickets, ", "), map[bool]string{true: "has", false: "have"}[len(tickets) == 1], map[bool]string{true: "it", false: "them"}[len(tickets) == 1], est)
	line, ok := in.read(ctx)
	if !ok {
		fmt.Fprintln(stdout)
		return false
	}
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes"
}
