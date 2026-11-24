package domain

import (
	"fmt"
	"regexp"
	"time"
)

type Script struct {
	APIVersion string     `yaml:"apiVersion"`
	Kind       string     `yaml:"kind"`
	Scenario   ScenarioID `yaml:"scenario"`
	ExpectHash Hash       `yaml:"expectHash"`
	// ShapeHash is the ShapeHash of the scenario the script was stored for. Empty in a script stored
	// before Plan 36, which is used only on an exact expectation hash.
	ShapeHash Hash `yaml:"shapeHash,omitempty"`
	// StoredAt is when PutScript stored the script. Zero before Plan 36.
	StoredAt   time.Time    `yaml:"storedAt,omitempty"`
	AuthoredBy ProviderRef  `yaml:"authoredBy"`
	Steps      []ScriptStep `yaml:"steps"`
}

type ProviderRef struct {
	Instance string `yaml:"instance" json:"instance"`
	Model    string `yaml:"model" json:"model"`
	Harness  string `yaml:"harness" json:"harness"`
}

// ScriptStep is one action. Exactly one of Request, Web, Mobile, and Use is set.
type ScriptStep struct {
	Step    StepID      `yaml:"step"`
	Request *APIRequest `yaml:"request,omitempty"`
	Web     *WebAction  `yaml:"web,omitempty"`
	// Mobile is the action of a step of an android scenario.
	Mobile *MobileAction `yaml:"mobile,omitempty"`
	// Use runs the flow of that ID from .semar/flows, with the values of With for its params. The
	// script of a scenario holds it. A flow holds none.
	// A flow may also use another flow, up to three levels deep.
	Use      string            `yaml:"use,omitempty"`
	With     map[string]string `yaml:"with,omitempty"`
	Captures []Capture         `yaml:"captures,omitempty"`
	// Expect holds the checks of a step of a flow, on the screen after the step. A scenario script
	// has none: a scenario keeps its checks on its own steps. A check of a flow is never a check of
	// the scenario.
	Expect []Expectation `yaml:"expect,omitempty"`
}

// Path, Query, Headers, and JSON may hold {{name}} variables.
type APIRequest struct {
	Service string            `yaml:"service"`
	Method  string            `yaml:"method"`
	Path    string            `yaml:"path"`
	Query   map[string]string `yaml:"query,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty"`
	JSON    any               `yaml:"json,omitempty"`
}

// Capture takes one value from the answer of a step. Exactly one of JSONPath and Element is set: an
// Element capture takes the text of an element of the screen of the step. A Pattern is a regular
// expression: the value is its first group, or the whole match when it has no group.
type Capture struct {
	Name     string `yaml:"name"`
	JSONPath string `yaml:"jsonpath,omitempty"`
	Element  string `yaml:"element,omitempty"`
	Pattern  string `yaml:"pattern,omitempty"`
}

// MaxPatternLen is the most characters that a capture pattern may have.
const MaxPatternLen = 200

// CheckPattern says why a capture pattern cannot be used, or returns nil. An empty pattern is fine.
func CheckPattern(p string) error {
	if p == "" {
		return nil
	}
	if len(p) > MaxPatternLen {
		return fmt.Errorf("the pattern is longer than %d characters", MaxPatternLen)
	}
	if _, err := regexp.Compile(p); err != nil {
		return fmt.Errorf("the pattern is not a valid regular expression: %v", err)
	}
	return nil
}

// Extract gives the value of the capture from the text that was read. Without a Pattern it is the text.
// With one it is the first group of the first match, or the whole match when the pattern has no group.
// ok is false when the pattern does not match or does not compile.
func (c Capture) Extract(s string) (string, bool) {
	if c.Pattern == "" {
		return s, true
	}
	if len(c.Pattern) > MaxPatternLen {
		return "", false
	}
	re, err := regexp.Compile(c.Pattern)
	if err != nil {
		return "", false
	}
	m := re.FindStringSubmatch(s)
	switch {
	case m == nil:
		return "", false
	case len(m) > 1:
		return m[1], true
	}
	return m[0], true
}
