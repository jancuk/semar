package plan

import (
	"context"

	"github.com/jancuk/semar/internal/domain"
)

// WebExplorer runs the web flow of one criterion in a browser, and writes the test that the flow made.
// The command that runs the plan gives it, so that this package does not know the browser. It is the
// same writing and replaying that semar record does, with a model in the seat of the person.
type WebExplorer interface {
	// Explore starts a browser that is signed in and records, and hands it to drive. When drive ends the
	// flow as done, Explore writes the screens, the scenario, and the script of the recording, replays the
	// scenario once, and keeps what it wrote only when the replay ends as the recording predicts. A flow
	// that drive ends as manual writes nothing. The browser and the fixtures of the flow are gone when
	// Explore returns.
	Explore(ctx context.Context, req WebRequest, drive FlowDriver) (WebOutcome, error)
}

// FlowDriver plays one flow on the browser that Explore gives it.
type FlowDriver func(ctx context.Context, b FlowBrowser) (FlowEnd, error)

// WebRequest is what the explorer needs to write the test of one flow.
type WebRequest struct {
	ID     domain.ScenarioID // the next number of the ticket
	Ticket string
	// Cite is the cite of the criterion, as every expectation of the test carries it.
	Cite string
}

// FailsNow is a check of a written test that the page does not meet: the criterion says Expect, and the
// element showed Shown when the flow read it. The test fails on this build, which is its point.
type FailsNow struct{ Element, Expect, Shown, Cite string }

// WebTest is a test that a flow wrote, and the replay confirmed as the recording predicted.
type WebTest struct {
	Scenario     domain.Scenario
	FailsNow     []FailsNow
	ScenarioPath string
	ScreensPath  string
	Replay       string // the outcome of the replay: passed, or failed when the test fails now
	Cite         string
}

// WebOutcome is how the flow of a criterion ended. Exactly one of Test and Manual is set. Manual is why
// a person must do the check. End is what the flow did, with its turns and actions.
type WebOutcome struct {
	Test   *WebTest
	Manual string
	End    FlowEnd
}
