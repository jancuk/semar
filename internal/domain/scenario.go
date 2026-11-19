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
	// SourceRule is an expected result that a fixed rule of Semar wrote from the API document. No model took part.
	SourceRule Source = "rule"
	// SourceImport is an expected result that semar import took from a file. Nobody approved it, so it always needs a review.
	SourceImport Source = "import"
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
	// Suites names the sets that semar run --suite picks the scenario by. It is no part of ExpectHash,
	// so a person can regroup an approved scenario with no new review.
	Suites []string `yaml:"suites,omitempty"`
	// Edge is set on an edge case that a plan drafted from the API document, and is nil on every other scenario.
	Edge *Edge `yaml:"edge,omitempty"`
}

// Edge says what an edge case changes. Kind is one of the kinds of package edge, Base is the scenario
// of which the case is a copy with one change, and On is the field, parameter or account that changes.
type Edge struct {
	Kind string     `yaml:"kind"`
	Base ScenarioID `yaml:"base"`
	On   string     `yaml:"on,omitempty"`
}

type Step struct {
	ID     StepID        `yaml:"id"`
	Setup  bool          `yaml:"setup,omitempty"`
	Action string        `yaml:"action"`
	Hint   *RequestHint  `yaml:"hint,omitempty"`
	Expect []Expectation `yaml:"expect,omitempty"`
	// As names an auth profile: the step's requests use that profile's session, not the scenario's.
	As string `yaml:"as,omitempty"`
}

type RequestHint struct {
	Method string `yaml:"method"`
	Path   string `yaml:"path"` // a template: /v1/orders/{id}/refunds
}

type Expectation struct {
	Check  Check  `yaml:"check"`
	Source Source `yaml:"source"`
	Cite   string `yaml:"cite,omitempty"`
	// By is "model" when a model wrote the expectation; such an expectation always needs confirmation.
	By string `yaml:"by,omitempty"`
}

// Exactly one of Status, JSONPath, Header, Element, Page is set. JSONPath, Header, Element, and Page take
// exactly one operator. An Element check names an element of the screen of the step, and reads its text
// with Equals, NotEquals, and Contains, whether it is there with Exists, how many match with Count, and
// what state it is in with State. With Attribute, Equals, NotEquals, and Contains read that attribute
// of the element and not its text. A Page check reads the Path or the Title of the page with Equals,
// NotEquals, and Contains.
type Check struct {
	Status     *int     `yaml:"status,omitempty"`
	JSONPath   string   `yaml:"jsonpath,omitempty"`
	Header     string   `yaml:"header,omitempty"`
	Element    string   `yaml:"element,omitempty"`
	Equals     any      `yaml:"equals,omitempty"`
	NotEquals  any      `yaml:"notEquals,omitempty"`
	Contains   any      `yaml:"contains,omitempty"`
	Exists     *bool    `yaml:"exists,omitempty"`
	Greater    *float64 `yaml:"greater,omitempty"`
	Less       *float64 `yaml:"less,omitempty"`
	Count      *int     `yaml:"count,omitempty"`
	EqualsPath string   `yaml:"equalsPath,omitempty"`
	// Page is "path" or "title".
	Page string `yaml:"page,omitempty"`
	// Attribute is the attribute of an Element check that Equals, NotEquals, or Contains read.
	Attribute string `yaml:"attribute,omitempty"`
	// State is one of ElementStates.
	State string `yaml:"state,omitempty"`
}

// The pages of a Page check.
const (
	PagePath  = "path"
	PageTitle = "title"
)

// ElementStates maps each state of an element that a check may ask for to the words after "the element
// <id>" in a Gherkin line. A scenario file and a person read the words, and a check holds the key.
var ElementStates = map[string]string{
	"hidden":    "is hidden",
	"visible":   "is visible",
	"enabled":   "is enabled",
	"disabled":  "is disabled",
	"checked":   "is checked",
	"unchecked": "is not checked",
}

// StateOfWords gives the state that the words of a line name, and false for any other words.
func StateOfWords(words string) (string, bool) {
	for state, w := range ElementStates {
		if w == words {
			return state, true
		}
	}
	return "", false
}

// SubjectCount is how many of the subjects of a check are set: a status, a JSON path, a header, an
// element, or a page. A well-formed check has one.
func (c Check) SubjectCount() int {
	n := 0
	for _, set := range []bool{c.Status != nil, c.JSONPath != "", c.Header != "", c.Element != "", c.Page != ""} {
		if set {
			n++
		}
	}
	return n
}

// PresenceOnly reports whether an element check is answered by whether the element is there, how many
// are, or whether any is shown: its absence is then an answer and no reason for a repair.
func (c Check) PresenceOnly() bool {
	if c.Element == "" {
		return false
	}
	if c.State != "" {
		return c.State == "hidden" || c.State == "visible"
	}
	return c.Attribute == "" && c.Equals == nil && c.NotEquals == nil && c.Contains == nil
}
