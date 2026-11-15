package domain

import "time"

// BuildRef is the build of one service. Declared is true when a person named the commit on the
// command line, so nothing read it from the service.
type BuildRef struct {
	Service    string `json:"service"`
	Commit     string `json:"commit"`
	ConfigHash string `json:"configHash"`
	Declared   bool   `json:"declared,omitempty"`
}

type Reason struct {
	Rule Outcome `json:"rule"` // which rule matched
	Text string  `json:"text"` // plain English
	Step StepID  `json:"step,omitempty"`
}

type CheckState string

const (
	CheckTrue         CheckState = "true"
	CheckFalse        CheckState = "false"
	CheckNotEvaluated CheckState = "not_evaluated"
)

// SourceInvariant labels a CheckResult from a built-in invariant. It never appears in a file.
const SourceInvariant = "invariant"

type CheckResult struct {
	Step      StepID     `json:"step"`
	Index     int        `json:"index"`  // position in Step.Expect. -1 for an invariant
	Source    string     `json:"source"` // a Source, or "invariant"
	State     CheckState `json:"state"`
	Expected  string     `json:"expected"`
	Observed  string     `json:"observed"`
	Detail    string     `json:"detail,omitempty"`
	Confirmed bool       `json:"confirmed"`
}

type ScenarioResult struct {
	Scenario ScenarioID    `json:"scenario"`
	Outcome  Outcome       `json:"outcome"`
	Reasons  []Reason      `json:"reasons"`
	Checks   []CheckResult `json:"checks"`
	Actions  []ActionID    `json:"actions"`
}

type Run struct {
	Schema      int    `json:"schema"`
	ID          RunID  `json:"id"`
	Environment string `json:"environment"`
	// EnvironmentKind is empty in records written before the kind was stored.
	EnvironmentKind string            `json:"environmentKind,omitempty"`
	StartedAt       time.Time         `json:"startedAt"`
	EndedAt         time.Time         `json:"endedAt"`
	BuildsStart     []BuildRef        `json:"buildsStart"`
	BuildsEnd       []BuildRef        `json:"buildsEnd"`
	Results         []ScenarioResult  `json:"results"`
	Files           map[string]string `json:"files"` // relative path to SHA-256
}
