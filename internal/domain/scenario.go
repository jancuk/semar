package domain

type (
	ScenarioID string
	StepID     string
	RunID      string
	ActionID   string
	Hash       string
	Surface    string
	Source     string
	Outcome    string
)

const (
	SurfaceAPI     Surface = "api"
	SurfaceWeb     Surface = "web"
	SurfaceAndroid Surface = "android"
)

const (
	SourceTicket   Source = "ticket"
	SourceTestcase Source = "testcase"
	SourcePerson   Source = "person"
	SourceDiff     Source = "diff"
	SourceBaseline Source = "baseline"
)

const (
	OutcomePassed       Outcome = "passed"
	OutcomeFailed       Outcome = "failed"
	OutcomeBlocked      Outcome = "blocked"
	OutcomeInconclusive Outcome = "inconclusive"
	OutcomeNotRun       Outcome = "not_run"
	OutcomeStale        Outcome = "stale"
)

type Scenario struct {
	APIVersion  string     `yaml:"apiVersion"`
	Kind        string     `yaml:"kind"`
	ID          ScenarioID `yaml:"id"`
	Title       string     `yaml:"title"`
	Ticket      string     `yaml:"ticket"`
	Intent      string     `yaml:"intent"`
	Surface     Surface    `yaml:"surface"`
	AuthProfile string     `yaml:"authProfile"`
	Given       []Given    `yaml:"given,omitempty"`
	Steps       []Step     `yaml:"steps"`
}

type Step struct {
	ID     StepID        `yaml:"id"`
	Setup  bool          `yaml:"setup,omitempty"`
	Action string        `yaml:"action"`
	Hint   *RequestHint  `yaml:"hint,omitempty"`
	Expect []Expectation `yaml:"expect,omitempty"`
}

type RequestHint struct {
	Method string `yaml:"method"`
	Path   string `yaml:"path"` // a template: /v1/orders/{id}/refunds
}

type Expectation struct {
	Check  Check  `yaml:"check"`
	Source Source `yaml:"source"`
	Cite   string `yaml:"cite,omitempty"`
}

// Exactly one of Status, JSONPath, Header is set. JSONPath and Header take exactly one operator.
type Check struct {
	Status     *int     `yaml:"status,omitempty"`
	JSONPath   string   `yaml:"jsonpath,omitempty"`
	Header     string   `yaml:"header,omitempty"`
	Equals     any      `yaml:"equals,omitempty"`
	NotEquals  any      `yaml:"notEquals,omitempty"`
	Contains   any      `yaml:"contains,omitempty"`
	Exists     *bool    `yaml:"exists,omitempty"`
	Greater    *float64 `yaml:"greater,omitempty"`
	Less       *float64 `yaml:"less,omitempty"`
	Count      *int     `yaml:"count,omitempty"`
	EqualsPath string   `yaml:"equalsPath,omitempty"`
}
