package cli

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/knowledge"
)

// PlanEstimate says what planning some tickets will use: who plans, with which model, and what it
// costs. Cost is empty when nothing can say yet.
type PlanEstimate struct {
	Provider, Model, Cost string
}

// planEstimator gives the estimate that semar run shows before it offers to plan (Plan 26, D6). The
// plan stage of Plan 27 supplies the real one, from the usage log. The default knows nothing.
type planEstimator func(ctx context.Context, o options, cfg config.Config, tickets []string) PlanEstimate

func noEstimateYet(_ context.Context, _ options, cfg config.Config, _ []string) PlanEstimate {
	p := cfg.PlanProvider()
	return PlanEstimate{Provider: p.ID, Model: p.Model}
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

// ticketsWithFiles is the tickets of keys that have a ticket file in the tickets source of the
// repository.
func ticketsWithFiles(dir string, cfg config.Config, keys []string) []string {
	list := cfg.Knowledge
	if len(list) == 0 {
		list = knowledge.Conventional(dir)
	}
	sources, err := knowledge.Resolve(dir, list)
	if err != nil {
		return nil
	}
	var out []string
	for _, t := range knowledge.ReadTickets(dir, sources, keys) {
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
