package plan

import (
	"context"

	"github.com/jancuk/semar/internal/domain"
)

// Trialer sends the read-only steps of an accepted draft once. A nil Trialer turns the trial off.
type Trialer interface {
	Try(ctx context.Context, scenarios []domain.Scenario, scripts []domain.Script) (Trial, error)
}

// Trial is what one trial run found. It holds statuses and names, never a value of a response.
type Trial struct {
	Skipped string            // why nothing was sent, as one line; "" when the trial ran
	Run     domain.RunID      // the run that holds the actions
	Env     string            // the environment name
	Builds  []domain.BuildRef // read once, before the first request
	Steps   []TrialStep
}

type TrialResult string

const (
	TrialSent     TrialResult = "sent"
	TrialNotTried TrialResult = "not_tried"
)

type TrialStep struct {
	Scenario domain.ScenarioID
	Step     domain.StepID // "" for a given entry
	Given    string        // the alias of a given entry
	Result   TrialResult
	Why      string // for not_tried, a fixed reason
	Method   string
	Route    string   // the step's hint, or the fixture's path template
	Status   int      // 0 when no response
	Missing  []string // capture names that found no value
	Err      string   // a transport error, from the recorded action
	Found    bool     // for a given entry: find gave a record
	Action   domain.ActionID
}
