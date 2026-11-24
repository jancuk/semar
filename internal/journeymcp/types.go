// Package journeymcp serves the journey tools of semar mcp: an agent starts a draft journey, works on
// the API, the web, and the Android tools as it does for any other task, and the legs of the journey are
// made from what it did. semar_journey_save writes the journey, replays it once with no model, and keeps
// it only when that replay passes.
//
// The package never imports internal/cli, apimcp, webmcp, or mobilemcp. The command wires the three taps
// and the backend with an Options value.
package journeymcp

import (
	"context"
	"errors"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/journey"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/record"
)

// Limits of the tools.
const (
	// MaxInvariants and MaxEqual bound the invariants of a journey, as the journey rules do.
	MaxInvariants = 8
	MaxEqual      = 6
	// MaxShown is the most problems that a result lists.
	MaxShown = 12
	// DefaultWaitSec and MaxWaitSec bound a wait of an api leg, in seconds.
	DefaultWaitSec = 30
	MaxWaitSec     = 900
)

// ErrNoWindow is what a WebTap answers to Mark when no browser window is open.
var ErrNoWindow = errors.New("no browser window is open")

// APIStep is one request that was sent, as a journey leg would write it. It is the shape of apimcp.APIStep,
// copied here so that the package does not import apimcp.
type APIStep struct {
	Service      string
	Request      domain.APIRequest
	Captures     []domain.Capture
	Expect       []domain.Expectation
	As           string
	Status       int
	Write        bool
	Action       string
	PathTemplate string
}

// APITap reads the log of the API tools and sets their variables.
type APITap interface {
	Seq() int
	Since(i int) []APIStep
	// DropLast removes the last step and the variables that it captured, and reports whether there was one.
	DropLast() bool
	// Vars are the session variables: the captures by name, and <leg>.<name> for the outputs of closed legs.
	Vars() map[string]string
	SetVar(k, v string)
}

// WebTap reads what the one browser window of the web tools heard.
type WebTap interface {
	// Mark is the number of events that the window has heard and the address that it shows. It gives
	// ErrNoWindow when no window is open.
	Mark(ctx context.Context) (mark int, url string, err error)
	EventsFrom(from int) []record.Event
	// Vars are the variables that the recording used, with the value of each.
	Vars() map[string]string
	// Service is the web service that the window was opened for, or "".
	Service() string
	// DefaultService is the one web service that web_open uses when it is given none, or "" when the
	// environment has several.
	DefaultService() string
	// BaseURL is the address of a service of the environment, or "".
	BaseURL(service string) string
}

// LegFlow is what the recording of an android leg made, before anything is written: the flow of its actions,
// and its checks, which the leg holds on steps of its own after the flow.
type LegFlow struct {
	Flow   domain.Flow
	App    string
	Added  []record.ScreenAdd
	Checks []LegCheck
	Steps  int
	Notes  []string
	// Captures are the names that the steps of the flow capture, which the leg may offer as outputs.
	Captures []string
}

// LegCheck is a check of an android leg: a wait for an element of a screen, with what it must show.
type LegCheck struct {
	ID      string
	Action  string
	Screen  string
	Element string
	Expect  []domain.Expectation
}

// MobileTap drives the recording of an android leg as a flow, and reads the flows that ran.
type MobileTap interface {
	// StartLegFlow begins to record the steps of the leg as the flow flowID for the service, from a fresh
	// start of the app (reset: restart), because a journey replays the flow from the launch. ticket is
	// the ID of the journey.
	StartLegFlow(ctx context.Context, flowID, service, ticket string) error
	// EndLegFlow ends the recording and gives the flow it made. Nothing is written.
	EndLegFlow(ctx context.Context) (LegFlow, error)
	// DiscardLegFlow drops the recording, if one goes on.
	DiscardLegFlow(ctx context.Context) error
	// UndoLegStep drops the last step of the recording.
	UndoLegStep(ctx context.Context) error
	// LegFlowSteps is how many steps the recording has, or 0 without one.
	LegFlowSteps() int
	// ForegroundService is the service of the app that the device shows, or an error when it is none
	// that the environment names.
	ForegroundService(ctx context.Context) (string, error)
	// LegScreen lists the screen that the recording of the leg is on now, as the recording shows it. It is
	// an error when no recording goes on.
	LegScreen(ctx context.Context) (string, error)
}

// ScreensFile is what a journey adds to one screens file.
type ScreensFile struct {
	App  string
	Adds []record.ScreenAdd
}

// Pending is what the draft would write besides the journey: the flows of its android legs and the
// elements of its web and android legs.
type Pending struct {
	Flows   []domain.Flow
	Screens []ScreensFile
}

// Saved is what Backend.Write wrote. Undo puts the repository back as it was.
type Saved struct {
	Files []string
	Undo  func() error
}

// RunOutcome is the verdict of a replay, decided by code.
type RunOutcome struct {
	// Outcome is a domain.JourneyOutcome: passed, failed, blocked, stale, or not_run.
	Outcome      string
	FirstFailing string
	Reasons      []string
	Report       string
	Legs         []LegOutcome
}

// LegOutcome is how one leg of a replay ended.
type LegOutcome struct {
	Leg     string
	Outcome string
	Reasons []string
}

// Backend checks, writes, and replays journeys. The command line implements it.
type Backend interface {
	// Validate judges the journey as it would be once the pending files are written.
	Validate(j domain.Journey, p Pending) []journey.Problem
	// Write writes the pending flows and screens and the journey. On an error nothing is left.
	Write(j domain.Journey, p Pending, replace bool) (*Saved, error)
	// Run replays the saved journey once with no model.
	Run(ctx context.Context, id domain.JourneyID) (RunOutcome, error)
	// Exists reports whether a journey of the ID is saved.
	Exists(id domain.JourneyID) bool
}

// BuildChecker is an optional part of a Backend. BuildProblem says why the replay of a journey that has a
// leg of the service could not read its build, or gives "" when it can.
type BuildChecker interface {
	BuildProblem(ctx context.Context, service string) string
}

// Options make a Manager.
type Options struct {
	Guard mcpkit.Guard
	// Scrub removes secrets from every text. Without it, a scrubber with no known secrets is used.
	Scrub   *mcpkit.Scrubber
	Backend Backend
	// API, Web, and Mobile are the taps. A nil tap leaves its surface out: a leg of it is refused.
	API    APITap
	Web    WebTap
	Mobile MobileTap
	// Screens gives the screens file of an app as it is now, or the zero value when there is none. It is
	// used to name the elements of a web leg.
	Screens func(app string) domain.Screens
	// Clock is time.Now when nil.
	Clock func() time.Time
}
