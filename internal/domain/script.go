package domain

type Script struct {
	APIVersion string       `yaml:"apiVersion"`
	Kind       string       `yaml:"kind"`
	Scenario   ScenarioID   `yaml:"scenario"`
	ExpectHash Hash         `yaml:"expectHash"`
	AuthoredBy ProviderRef  `yaml:"authoredBy"`
	Steps      []ScriptStep `yaml:"steps"`
}

type ProviderRef struct {
	Instance string `yaml:"instance" json:"instance"`
	Model    string `yaml:"model" json:"model"`
	Harness  string `yaml:"harness" json:"harness"`
}

type ScriptStep struct {
	Step     StepID      `yaml:"step"`
	Request  *APIRequest `yaml:"request"`
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

type Capture struct {
	Name     string `yaml:"name"`
	JSONPath string `yaml:"jsonpath"`
}
