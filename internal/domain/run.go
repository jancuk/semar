package domain

import (
	"strings"
	"time"
)

// BuildRef is the build of one service. Declared is true when a person named the commit on the
// command line, so nothing read it from the service.
type BuildRef struct {
	Service    string `json:"service"`
	Commit     string `json:"commit"`
	ConfigHash string `json:"configHash"`
	Declared   bool   `json:"declared,omitempty"`
	// Local is set with Declared when the commit is the working copy of a service on this machine, not
	// one that a person named.
	Local bool `json:"local,omitempty"`
}

// ShortCommit is the first 7 characters of a build's commit. The build of a service that runs the
// working copy ends in +<digest> when tracked files differ from the commit, and the digest is kept.
func ShortCommit(commit string) string {
	if len(commit) <= 7 {
		return commit
	}
	if i := strings.IndexByte(commit, '+'); i > 7 {
		return commit[:7] + commit[i:]
	}
	return commit[:7]
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
	// ByModel is true when a model wrote the expected result. A result stored before Plan 36 has no mark.
	ByModel bool `json:"byModel,omitempty"`
}

// ModelCount counts the expected results of the result: byModel those that a model wrote,
// results every expected result. Invariants are left out. A result stored before Plan 36 gives 0.
func (r ScenarioResult) ModelCount() (byModel, results int) {
	for _, c := range r.Checks {
		if c.Index < 0 {
			continue
		}
		results++
		if c.ByModel {
			byModel++
		}
	}
	return byModel, results
}

// Attempt is one replay of a scenario: its own outcome, reasons, checks, and the actions that hold its
// evidence.
type Attempt struct {
	Outcome Outcome       `json:"outcome"`
	Reasons []Reason      `json:"reasons"`
	Checks  []CheckResult `json:"checks"`
	Actions []ActionID    `json:"actions"`
}

// Settle is the attempt that decides a scenario, and whether the scenario is flaky. The deciding
// attempt is the last one, because a scenario is replayed again only after an attempt that did not
// pass. A scenario is flaky when it passed after an attempt that did not.
func Settle(attempts []Attempt) (deciding Attempt, flaky bool) {
	if len(attempts) == 0 {
		return Attempt{}, false
	}
	deciding = attempts[len(attempts)-1]
	return deciding, len(attempts) > 1 && deciding.Outcome == OutcomePassed
}

type ScenarioResult struct {
	Scenario ScenarioID    `json:"scenario"`
	Outcome  Outcome       `json:"outcome"`
	Reasons  []Reason      `json:"reasons"`
	Checks   []CheckResult `json:"checks"`
	Actions  []ActionID    `json:"actions"`
	// Attempts holds every attempt, in order, when the scenario was replayed more than once. The fields
	// above are those of the deciding attempt. It is empty, and not encoded, for a scenario that ran
	// once and in a result stored before attempts existed, whose single attempt is the fields above.
	Attempts []Attempt `json:"attempts,omitempty"`
	// AttemptsAllowed is how many attempts the run allowed, set only with Attempts.
	AttemptsAllowed int `json:"attemptsAllowed,omitempty"`
	// Flaky is true when the scenario passed on an attempt after the first.
	Flaky bool `json:"flaky,omitempty"`
	// ExpectHash is the ExpectHash of the scenario that was judged. It is empty, and not encoded,
	// in a result stored before it was recorded.
	ExpectHash Hash `json:"expectHash,omitempty"`
	// ScriptHash is the expectation hash that the script that ran was stored under. It differs from
	// ExpectHash when the script was found by its shape hash. Empty when no script ran, and in a
	// result stored before Plan 36.
	ScriptHash Hash `json:"scriptHash,omitempty"`
	// Fixtures is what each given entry did. It is empty for a scenario without given.
	Fixtures []FixtureRecord `json:"fixtures,omitempty"`
}

const (
	PurposeTrial        = "trial"
	PurposeFixtureCheck = "fixture-check"
	// PurposeRecording is the run of the fixtures that semar record makes for a recording.
	PurposeRecording = "recording"
)

type Run struct {
	Schema      int    `json:"schema"`
	ID          RunID  `json:"id"`
	Environment string `json:"environment"`
	// Purpose is empty for a run of scenarios.
	Purpose string `json:"purpose,omitempty"`
	// EnvironmentKind is empty in records written before the kind was stored.
	EnvironmentKind string           `json:"environmentKind,omitempty"`
	StartedAt       time.Time        `json:"startedAt"`
	EndedAt         time.Time        `json:"endedAt"`
	BuildsStart     []BuildRef       `json:"buildsStart"`
	BuildsEnd       []BuildRef       `json:"buildsEnd"`
	Results         []ScenarioResult `json:"results"`
	// Journeys is empty, and not encoded, for a run without journeys.
	Journeys []JourneyResult   `json:"journeys,omitempty"`
	Files    map[string]string `json:"files"` // relative path to SHA-256
}
