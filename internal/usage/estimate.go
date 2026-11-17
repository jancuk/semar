package usage

import (
	"fmt"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/provider"
)

// PipelineCriteria is the Pipeline of a record that a plan of the criteria pipeline wrote, one for
// each model that it called.
const PipelineCriteria = "criteria"

// Estimate is what planning some criteria would cost, from the jobs that earlier plans recorded.
type Estimate struct {
	Provider string
	Model    string // as the ladder names it; "" for the provider's own model
	Kind     string
	Criteria int
	Known    bool
	USD      float64 // the mean cost of a job times Criteria
	Jobs     int     // the earlier jobs that the mean is from
	// NoCost says that the provider has records and reports no cost, so no amount can be given.
	NoCost bool
}

// EstimateCost is the mean cost of a job of the criteria pipeline on this provider and model, from the
// usage log in home, times the number of criteria. A criterion costs one job, and a correction costs
// more, which the mean of earlier plans already holds. A model matches the model that a record asked
// for, such as haiku, or the one that the provider reported. With no record it is not known, and
// String says so.
func EstimateCost(home, providerID, model string, criteria int) (Estimate, error) {
	recs, err := Read(home, time.Time{})
	e := Estimate{Provider: providerID, Model: model, Criteria: criteria}
	if err != nil {
		return e, err
	}
	var jobs int
	var cost float64
	for _, r := range recs {
		if r.Provider != providerID || r.Pipeline != PipelineCriteria || strings.HasPrefix(r.Outcome, "failed") || r.Attempts <= 0 {
			continue
		}
		if model != "" && r.Asked != model && !(r.Asked == "" && strings.Contains(r.Model, model)) {
			continue
		}
		if model == "" && r.Asked != "" {
			continue
		}
		e.Kind = r.Kind
		if r.CostUSD <= 0 {
			if !provider.ReportsCost(r.Kind) {
				e.NoCost = true
			}
			continue
		}
		jobs += r.Attempts
		cost += r.CostUSD
	}
	if jobs == 0 {
		return e, nil
	}
	e.Known, e.Jobs, e.USD = true, jobs, cost/float64(jobs)*float64(criteria)
	return e, nil
}

// String is the estimate in a sentence that follows the question whether to plan.
func (e Estimate) String() string {
	who := e.Provider
	if e.Model != "" {
		who += " " + e.Model
	}
	switch {
	case e.Known:
		amount := fmt.Sprintf("about $%.2f", e.USD)
		if e.USD > 0 && e.USD < 0.005 {
			amount = "less than $0.01"
		}
		return fmt.Sprintf("%s for %s, from %s of %s", amount, plural(e.Criteria, "criterion", "criteria"), plural(e.Jobs, "earlier job", "earlier jobs"), who)
	case e.NoCost:
		return fmt.Sprintf("no estimate: %s reports no cost", who)
	}
	return fmt.Sprintf("no estimate yet: the usage log has no earlier plan of %s", who)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
