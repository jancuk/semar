package plan

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

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
	// Subject is the Feature line of the scenario file: what the ticket changes.
	Subject string
	// Cite is the cite of the criterion, as every expectation of the test carries it.
	Cite string
}

// FailsNow is a check of a written test that the page does not meet: the criterion says Expect, and the
// element showed Shown when the flow read it. The test fails on this build, which is its point.
type FailsNow struct {
	Element string `json:"element"`
	Expect  string `json:"expect"`
	Shown   string `json:"shown"`
	Cite    string `json:"cite"`
}

// WebTest is a test that a flow wrote, and the replay confirmed as the recording predicted.
type WebTest struct {
	Scenario     domain.Scenario
	FailsNow     []FailsNow
	ScenarioPath string
	ScreensPath  string
	Replay       string // the outcome of the replay: passed, or failed when the test fails now
	Cite         string
	// Remove takes the test back out of the repository: its scenario, its script, and what it added to
	// the screens file. A plan that cannot be written calls it, so that a failed plan leaves nothing.
	Remove func() error `json:"-"`
}

// WebOutcome is how the flow of a criterion ended. Exactly one of Test and Manual is set. Manual is why
// a person must do the check. End is what the flow did, with its turns and actions.
type WebOutcome struct {
	Test   *WebTest
	Manual string
	End    FlowEnd
}

// exploreWeb explores the criteria that a first job left as a manual check on a web page, one at a time,
// because an account has one browser profile and two flows in one application disturb each other. A flow
// that writes a test takes the place of the manual check of its criterion. The IDs of the tests are the
// next numbers of their tickets, and the scenarios that the plan drafts number after them.
func (rs *runState) exploreWeb(ctx context.Context, outcomes []CriterionOutcome) error {
	if rs.in.Web == nil {
		return nil
	}
	for i := range outcomes {
		o := &outcomes[i]
		if !o.Answered || o.Result.Manual == nil || o.Result.Manual.Surface != SurfaceWeb {
			continue
		}
		m := *o.Result.Manual
		res, err := rs.exploreOne(ctx, *o)
		if err != nil {
			rs.undoWeb(outcomes)
			return fmt.Errorf("criterion %s: %w", o.Packet.Cite, err)
		}
		o.Web = res
		if res.Test == nil {
			continue
		}
		o.Result.Manual = nil
		if res.Unproved != "" {
			o.Result.Manual = restOfManual(m, res.Test.Scenario.Title, res.Unproved)
		}
		sc := res.Test.Scenario
		rs.in.Existing[sc.Ticket] = append(slices.Clone(rs.in.Existing[sc.Ticket]), sc)
	}
	return nil
}

// undoWeb takes the tests that flows wrote back out, the newest first, for a plan that ends with an error:
// a plan that fails leaves nothing behind.
func (rs *runState) undoWeb(outcomes []CriterionOutcome) {
	for i := len(outcomes) - 1; i >= 0; i-- {
		if w := outcomes[i].Web; w != nil && w.Test != nil && w.Test.Remove != nil {
			_ = w.Test.Remove()
		}
	}
}

// nextID is the next number of a ticket that no scenario has, as the plan numbers its own scenarios.
func (rs *runState) nextID(ticket string) domain.ScenarioID {
	last := 0
	for _, sc := range rs.in.Existing[ticket] {
		if n, ok := numberOf(ticket, sc.ID); ok && n > last {
			last = n
		}
	}
	return domain.ScenarioID(fmt.Sprintf("%s-%02d", ticket, last+1))
}

// exploreOne asks the ladder for one criterion: a flow on the first model, and, when it ends as a manual
// check because of the model, the same flow on the next, in a new browser with new fixtures. The test is
// written by the explorer, and nothing is written by a flow that ends as manual.
func (rs *runState) exploreOne(ctx context.Context, o CriterionOutcome) (*WebResult, error) {
	p, m := o.Packet, *o.Result.Manual
	req := WebRequest{ID: rs.nextID(p.Ticket), Ticket: p.Ticket, Subject: ticketTitle(rs.in, p.Ticket), Cite: p.Why()}
	reading := manualReading(m)
	res := &WebResult{}
	started := time.Now()
	defer func() {
		rs.mu.Lock()
		defer rs.mu.Unlock()
		rs.web.Explored++
		rs.web.Turns += res.Turns
		rs.web.Took += time.Since(started)
		if res.Test != nil {
			rs.web.Written++
			rs.web.FailsNow += len(res.Test.FailsNow)
		} else {
			rs.web.Manual++
		}
	}()
	for rung := range rs.in.Ladder {
		if rs.overLimit() {
			res.Manual = notPlanned(rs.in)
			return res, nil
		}
		if rung > 0 {
			rs.mu.Lock()
			if res.Rung != rung {
				rs.web.Escalated++
			}
			rs.mu.Unlock()
		}
		res.Rung = rung
		catalogue := slices.Clone(rs.in.Fixtures)
		drive := func(ctx context.Context, b FlowBrowser) (FlowEnd, error) {
			return playFlow(ctx, DefaultFlowLimits, TurnInput{Criterion: p.Cite + "\n" + p.Criterion, Ticket: p.Ticket, Reading: reading}, rs.turnModel(rung, catalogue, DefaultFlowLimits.Turns), b)
		}
		out, err := rs.in.Web.Explore(ctx, req, drive)
		if err != nil {
			return nil, err
		}
		res.Turns += out.End.Turns
		res.Actions += out.End.Actions
		if out.Test != nil {
			res.Test, res.Manual = out.Test, ""
			if out.End.Done != nil {
				res.Unproved = oneLine(out.End.Done.Unproved, maxManualText)
			}
			return res, nil
		}
		res.Manual = out.Manual
		// A flow that never asked the model, because the browser or the fixtures failed, is not the
		// model's fault, and another model would meet the same failure.
		if out.End.Turns == 0 || rs.limited {
			return res, nil
		}
	}
	return res, nil
}

// restOfManual is the manual check that stays when a web test proves only part of a criterion: the part
// that it leaves out, with the cite of the criterion.
func restOfManual(m ManualCheckDraft, title, unproved string) *ManualCheckDraft {
	return &ManualCheckDraft{
		Ticket: m.Ticket, Surface: m.Surface, Cite: m.Cite,
		Title:    oneLine("The rest of the criterion, which the web test "+strings.TrimRight(title, ".")+" does not prove", maxManualTitle),
		Steps:    []string{oneLine("Check what the web test leaves out: "+unproved, maxManualText)},
		Expected: unproved,
	}
}

// manualReading is what a first job wrote for a person to do, as a help to the model of the flow.
func manualReading(m ManualCheckDraft) string {
	var b strings.Builder
	for _, s := range m.Steps {
		fmt.Fprintf(&b, "- %s\n", oneLine(s, maxProblemRunes))
	}
	fmt.Fprintf(&b, "Expected: %s", oneLine(m.Expected, maxProblemRunes))
	return b.String()
}
