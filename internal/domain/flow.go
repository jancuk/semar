package domain

// FlowRef names the version of a flow that ran: its ID and the sha256 digest of the file it was
// read from, as "sha256:<hex>". A script hash does not cover a flow, so a run records this.
type FlowRef struct {
	ID     string `json:"id" yaml:"id"`
	Digest string `json:"digest" yaml:"digest"`
}

// Flow is a reusable sequence of device steps, kept in .semar/flows/<id>.yaml. A scenario script runs
// it with a step that has use: <id>. A flow is android only in this version. It lives in domain so that
// the script package and the flow package need not import each other.
type Flow struct {
	APIVersion  string  `yaml:"apiVersion"`
	Kind        string  `yaml:"kind"`
	ID          string  `yaml:"id"`
	Surface     Surface `yaml:"surface"`
	Description string  `yaml:"description,omitempty"`
	// Service and App are the defaults of the steps that name none.
	Service string      `yaml:"service,omitempty"`
	App     string      `yaml:"app,omitempty"`
	Params  []FlowParam `yaml:"params,omitempty"`
	// Steps are the steps of the flow. Step is the name of the step inside the flow. A step has a
	// mobile action and captures, and no use.
	Steps []ScriptStep `yaml:"steps"`
}

// FlowParam is a value that the caller gives with its with map. Inside the flow it is {{name}}.
type FlowParam struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	// Default is used when the caller gives no value. A param with no default is required.
	Default *string `yaml:"default,omitempty"`
}

// Required reports whether the caller must give the param.
func (p FlowParam) Required() bool { return p.Default == nil }

// Param gives the param with the name.
func (f Flow) Param(name string) (FlowParam, bool) {
	for _, p := range f.Params {
		if p.Name == name {
			return p, true
		}
	}
	return FlowParam{}, false
}

// CaptureNames lists the names that the steps of the flow capture, in order.
func (f Flow) CaptureNames() []string {
	var out []string
	for _, st := range f.Steps {
		for _, c := range st.Captures {
			out = append(out, c.Name)
		}
	}
	return out
}
