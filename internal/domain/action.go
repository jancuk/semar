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
}

type Action struct {
	Schema        int               `json:"schema"` // 1
	ID            ActionID          `json:"id"`
	Seq           int               `json:"seq"`
	Run           RunID             `json:"run"`
	Surface       Surface           `json:"surface"`
	Op            string            `json:"op"` // "request"
	Use           UseCase           `json:"use"`
	By            Actor             `json:"by"`
	Request       *HTTPRequest      `json:"request,omitempty"`
	Response      *HTTPResponse     `json:"response,omitempty"`
	Captures      map[string]string `json:"captures,omitempty"`
	Refused       string            `json:"refused,omitempty"`       // why policy refused it. No request was sent
	Err           string            `json:"error,omitempty"`         // transport error
	EffectUnknown bool              `json:"effectUnknown,omitempty"` // a write that failed after it may have reached the server
	TraceID       string            `json:"traceId"`
	At            time.Time         `json:"at"`
	DurationMS    int64             `json:"durationMs"`
	Why           string            `json:"why,omitempty"`
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
}

type ObjectRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}
