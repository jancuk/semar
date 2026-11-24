package domain

import (
	"time"
)

type (
	Pass  string
	Actor string
	Verb  string
)

const (
	PassSetup    Pass = "setup"
	PassScenario Pass = "scenario"
	PassExplore  Pass = "explore"
	PassSecurity Pass = "security"
	PassCleanup  Pass = "cleanup"
	PassTrial    Pass = "trial"
)

const (
	ActorAgent  Actor = "agent"
	ActorPilot  Actor = "pilot"
	ActorReplay Actor = "replay"
)

const (
	VerbVisit  Verb = "visit"
	VerbRead   Verb = "read"
	VerbCreate Verb = "create"
	VerbUpdate Verb = "update"
	VerbDelete Verb = "delete"
	VerbSignIn Verb = "sign_in"
	VerbQuery  Verb = "query"
)

type UseCase struct {
	Pass     Pass       `json:"pass"`
	Scenario ScenarioID `json:"scenario"`
	Step     StepID     `json:"step"`
	// Fixture is set on an action that a fixture sent. Step is then empty.
	Fixture *FixtureUse `json:"fixture,omitempty"`
	// Attempt is 1-based for a repeated read and 0 for a single request.
	Attempt int `json:"attempt,omitempty"`
}

type Action struct {
	Schema   int           `json:"schema"` // 1
	ID       ActionID      `json:"id"`
	Seq      int           `json:"seq"`
	Run      RunID         `json:"run"`
	Surface  Surface       `json:"surface"`
	Op       string        `json:"op"` // "request" on the API, or the verb of a web action
	Use      UseCase       `json:"use"`
	By       Actor         `json:"by"`
	Request  *HTTPRequest  `json:"request,omitempty"`
	Response *HTTPResponse `json:"response,omitempty"`
	// Web is set on an action of the web engine, which has no Request and no Response.
	Web *WebRecord `json:"web,omitempty"`
	// Mobile is set on an action of the mobile engine, which has no Request and no Response either.
	Mobile        *MobileRecord     `json:"mobile,omitempty"`
	Captures      map[string]string `json:"captures,omitempty"`
	Refused       string            `json:"refused,omitempty"`       // why policy refused it. No request was sent
	Err           string            `json:"error,omitempty"`         // transport error
	EffectUnknown bool              `json:"effectUnknown,omitempty"` // a write that failed after it may have reached the server
	TraceID       string            `json:"traceId"`
	At            time.Time         `json:"at"`
	DurationMS    int64             `json:"durationMs"`
	Why           string            `json:"why,omitempty"`
	// Doc is what the check against the API document found, when it found anything.
	Doc *DocCheck `json:"doc,omitempty"`
}

type HTTPRequest struct {
	Method       string              `json:"method"`
	URL          string              `json:"url"`
	PathTemplate string              `json:"pathTemplate"`
	Headers      map[string][]string `json:"headers"`
	Body         Body                `json:"body,omitempty"`
}

type HTTPResponse struct {
	URL       string              `json:"url,omitempty"` // set only when redirects were followed to another URL
	Status    int                 `json:"status"`
	Headers   map[string][]string `json:"headers"`
	Body      Body                `json:"body,omitempty"`
	Truncated bool                `json:"truncated,omitempty"`
}

type Activity struct {
	Seq      int        `json:"seq"`
	At       time.Time  `json:"at"`
	Actor    Actor      `json:"actor"`
	Use      UseCase    `json:"use"`
	Surface  Surface    `json:"surface"`
	Verb     Verb       `json:"verb"`
	Target   string     `json:"target"`
	Object   *ObjectRef `json:"object,omitempty"`
	Result   string     `json:"result"`
	Why      string     `json:"why,omitempty"`
	TraceID  string     `json:"traceId"`
	Evidence string     `json:"evidence"` // path of the action file, relative to the run directory
	// Screenshot is the path of the screenshot of a web or a mobile action, relative to the run directory.
	Screenshot string `json:"screenshot,omitempty"`
	// Blocked lists the hosts that the browser refused to reach during a web action because the
	// environment does not list them, in the form of a hosts entry.
	Blocked []string `json:"blocked,omitempty"`
	// Doc is the finding of the check against the API document, as the action holds it.
	Doc *DocCheck `json:"doc,omitempty"`
	// Misses are the elements that a device action looked for and did not find, with what the screen held.
	Misses []ElementMiss `json:"misses,omitempty"`
	// CrashFile and LogcatFile are the paths, relative to the run directory, of the crash report and of the
	// log of the app that a device action kept as evidence of why it failed.
	CrashFile  string `json:"crashFile,omitempty"`
	LogcatFile string `json:"logcatFile,omitempty"`
}

// ElementMiss is an element that a device step looked for and did not find. Repair holds the locators
// that would find one element, Nearby the visible elements that look most like it.
type ElementMiss struct {
	App     string      `json:"app,omitempty"`
	Screen  string      `json:"screen,omitempty"`
	Element string      `json:"element"`
	Looked  Locator     `json:"looked"`
	Repair  []Candidate `json:"repair,omitempty"`
	Nearby  []Candidate `json:"nearby,omitempty"`
}

type ObjectRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}
