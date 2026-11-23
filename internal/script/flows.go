package script

import (
	"fmt"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
)

// FlowResolver gives the flow of an ID, or an error that says why there is none. The flow package
// supplies it, and this package does not import that one.
type FlowResolver func(id string) (domain.Flow, error)

// ValidateWithFlows is ValidateWith with the flows of the repository: each step that has use is checked
// against the flow it names. With a nil resolver it is ValidateWith, which accepts a use step as one
// that it cannot look into.
func ValidateWithFlows(sc domain.Scenario, s domain.Script, outputs map[string][]string, flows FlowResolver) []Problem {
	return validate(sc, s, outputs, false, flows)
}

// ValidateShapeWithFlows is ValidateShapeWith with the flows, as ValidateWithFlows is to ValidateWith.
func ValidateShapeWithFlows(sc domain.Scenario, s domain.Script, outputs map[string][]string, flows FlowResolver) []Problem {
	return validate(sc, s, outputs, true, flows)
}

// RenderWith returns a copy of with in which every {{name}} of a value is replaced. The values are
// rendered in the scope of the caller, which is vars. A name without a value is an error that names the
// param.
func RenderWith(with map[string]string, vars map[string]string) (map[string]string, error) {
	if len(with) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(with))
	for _, k := range sortedStringKeys(with) {
		v, err := substitute(with[k], vars, nil)
		if err != nil {
			return nil, fmt.Errorf("with %s: %w", k, err)
		}
		out[k] = v
	}
	return out, nil
}

func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// WithVariables lists the variable names that the values of a with map use, in order and without repeats.
func WithVariables(with map[string]string) []string {
	var names []string
	for _, k := range sortedStringKeys(with) {
		for _, m := range variablePattern.FindAllStringSubmatch(with[k], -1) {
			if !slices.Contains(names, m[1]) {
				names = append(names, m[1])
			}
		}
	}
	return names
}

// useProblems checks one use step against its flow, and reports what the scenario then holds: the
// services that the flow starts, and the names that it captures. started and captured are changed.
func useProblems(ss domain.ScriptStep, f domain.Flow, started, captured map[string]bool, add func(format string, args ...any)) {
	if f.Surface != domain.SurfaceAndroid {
		add("use: flow %s is for the surface %q, and a scenario of the surface android needs an android flow", f.ID, f.Surface)
	}
	for _, k := range sortedStringKeys(ss.With) {
		if _, ok := f.Param(k); !ok {
			add("with: flow %s has no param %s; its params are %s", f.ID, k, paramList(f))
		}
	}
	for _, p := range f.Params {
		if _, given := ss.With[p.Name]; !given && p.Required() {
			add("with: flow %s needs the param %s", f.ID, p.Name)
		}
	}
	for _, st := range f.Steps {
		if st.Mobile == nil {
			continue
		}
		svc := st.Mobile.Service
		if svc == "" {
			svc = f.Service
		}
		if !started[svc] {
			started[svc] = true
			if do := st.Mobile.Do; do != domain.MobileLaunch && do != domain.MobileOpenLink {
				add("use: the first mobile step of the service %s must be launch or open, and flow %s starts with %s", svc, f.ID, do)
			}
		}
	}
	for _, name := range f.CaptureNames() {
		if captured[name] {
			add("use: flow %s captures %s, which an earlier step already captured: a flow that captures can be used once in a scenario", f.ID, name)
		}
		captured[name] = true
	}
}

func paramList(f domain.Flow) string {
	if len(f.Params) == 0 {
		return "none"
	}
	names := make([]string, len(f.Params))
	for i, p := range f.Params {
		names[i] = p.Name
	}
	return strings.Join(names, ", ")
}
