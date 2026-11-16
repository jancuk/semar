package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"
)

type (
	JourneyID      string
	LegID          string
	JourneyOutcome string
)

// A journey has no inconclusive: a leg that ends inconclusive makes the journey blocked.
const (
	JourneyPassed  JourneyOutcome = "passed"
	JourneyFailed  JourneyOutcome = "failed"
	JourneyBlocked JourneyOutcome = "blocked"
	JourneyStale   JourneyOutcome = "stale"
	JourneyNotRun  JourneyOutcome = "not_run"
)

const PurposeJourney = "journey"

type Journey struct {
	APIVersion  string      `yaml:"apiVersion"`
	Kind        string      `yaml:"kind"`
	ID          JourneyID   `yaml:"id"`
	Title       string      `yaml:"title"`
	Owner       string      `yaml:"owner"`
	Criticality string      `yaml:"criticality"` // critical | standard
	AuthProfile string      `yaml:"authProfile,omitempty"`
	Given       []Given     `yaml:"given,omitempty"`
	Legs        []Leg       `yaml:"legs"`
	Invariants  []Invariant `yaml:"invariants,omitempty"`
}

type Leg struct {
	ID      LegID   `yaml:"id"`
	Surface Surface `yaml:"surface"`
	Service string  `yaml:"service"`
	// As names an auth profile: every step of the leg is sent with that profile's session, unless
	// the step has its own as.
	As      string    `yaml:"as,omitempty"`
	Needs   []LegID   `yaml:"needs,omitempty"`
	Wait    *LegWait  `yaml:"wait,omitempty"`
	Steps   []LegStep `yaml:"steps"`
	Outputs []string  `yaml:"outputs,omitempty"`
}

type LegWait struct {
	Until   LegUntil      `yaml:"until"`
	Timeout time.Duration `yaml:"timeout"`
	Every   time.Duration `yaml:"every,omitempty"`
}

type LegUntil struct {
	JSONPath string `yaml:"jsonpath"`
	Equals   any    `yaml:"equals"`
}

// LegStep is a scenario step with the request that carries it out.
type LegStep struct {
	ID       StepID        `yaml:"id"`
	Setup    bool          `yaml:"setup,omitempty"`
	Action   string        `yaml:"action"`
	Hint     *RequestHint  `yaml:"hint"`
	Expect   []Expectation `yaml:"expect,omitempty"`
	Request  APIRequest    `yaml:"request"`
	Captures []Capture     `yaml:"captures,omitempty"`
	// As names an auth profile for this step. It wins over the leg's.
	As string `yaml:"as,omitempty"`
}

type Invariant struct {
	Equal []string `yaml:"equal"`
}

// Manifest is the builds a journey ran against.
type Manifest struct {
	Services []BuildRef `json:"services"` // sorted by service
	Hash     Hash       `json:"hash"`
}

type LegResult struct {
	Leg      LegID      `json:"leg"`
	Scenario ScenarioID `json:"scenario"` // <journey>.<leg>
	Outcome  Outcome    `json:"outcome"`
	Reasons  []Reason   `json:"reasons"`
	// As lists the auth profiles that the steps of the leg acted as, in step order, and only when a
	// step acted as one by as. A leg that sent with its own profile has none.
	As []string `json:"as,omitempty"`
}

type InvariantResult struct {
	Index  int        `json:"index"`
	State  CheckState `json:"state"`
	Values []string   `json:"values"` // redacted, in the order of Equal
	Detail string     `json:"detail,omitempty"`
}

type JourneyResult struct {
	Journey      JourneyID         `json:"journey"`
	Manifest     Manifest          `json:"manifest"`
	ManifestEnd  *Manifest         `json:"manifestEnd,omitempty"`
	Outcome      JourneyOutcome    `json:"outcome"`
	FirstFailing LegID             `json:"firstFailing,omitempty"`
	Legs         []LegResult       `json:"legs"`
	Invariants   []InvariantResult `json:"invariants"`
	Fixtures     []FixtureRecord   `json:"fixtures,omitempty"`
	Reasons      []Reason          `json:"reasons"`
}

// The hashed shape is private and has fixed keys, so a change to a json tag of BuildRef
// cannot change a stored manifest hash. Declared is left out: it says who named the commit,
// not what was built.
type hashedBuild struct {
	Service    string `json:"service"`
	Commit     string `json:"commit"`
	ConfigHash string `json:"configHash"`
}

// ManifestHash is the SHA-256 of the builds sorted by service, so the order in which the
// services were read does not matter. When encoding fails it returns the empty Hash.
func ManifestHash(services []BuildRef) Hash {
	sorted := slices.Clone(services)
	slices.SortStableFunc(sorted, func(a, b BuildRef) int { return strings.Compare(a.Service, b.Service) })
	hb := make([]hashedBuild, len(sorted))
	for i, s := range sorted {
		hb[i] = hashedBuild{Service: s.Service, Commit: s.Commit, ConfigHash: s.ConfigHash}
	}
	b, err := json.Marshal(hb)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return Hash(hex.EncodeToString(sum[:]))
}
