package domain

import "time"

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

// ScriptStep is one action. Exactly one of Request and Web is set.
type ScriptStep struct {
	Step     StepID      `yaml:"step"`
	Request  *APIRequest `yaml:"request,omitempty"`
	Web      *WebAction  `yaml:"web,omitempty"`
	Captures []Capture   `yaml:"captures,omitempty"`
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
// Element capture takes the text of an element of the screen of the step.
type Capture struct {
	Name     string `yaml:"name"`
	JSONPath string `yaml:"jsonpath,omitempty"`
	Element  string `yaml:"element,omitempty"`
}
