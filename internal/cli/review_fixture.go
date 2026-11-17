package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
)

// fixtureLines is what a person reads before approving a fixture: the header and then everything
// that its hash covers, one line each.
func fixtureLines(fx domain.Fixture, h domain.Hash) []string {
	lines := []string{fmt.Sprintf("Fixture %s %s (owner %s), hash %s:", fx.ID, fx.Version, fx.Owner, h)}
	add := func(format string, args ...any) { lines = append(lines, "  "+fmt.Sprintf(format, args...)) }
	add("description: %s", fx.Description)
	if len(fx.Params) > 0 {
		var ps []string
		for _, name := range slices.Sorted(maps.Keys(fx.Params)) {
			if d := fx.Params[name].Default; d != nil {
				ps = append(ps, fmt.Sprintf("%s (default %q)", name, *d))
			} else {
				ps = append(ps, name+" (required)")
			}
		}
		add("parameters: %s", strings.Join(ps, ", "))
	}
	if fx.Lease != "" {
		add("lease: %s", fx.Lease)
	}
	add("outputs: %s", strings.Join(fx.Outputs, ", "))
	for _, phase := range []struct {
		name  string
		steps []domain.FixtureStep
	}{{domain.PhaseFind, fx.Find}, {domain.PhaseCreate, fx.Create}, {domain.PhaseCleanup, fx.Cleanup}} {
		for i, st := range phase.steps {
			add("%s", fixtureStepLine(phase.name, i+1, st))
		}
	}
	return lines
}

func fixtureStepLine(phase string, n int, st domain.FixtureStep) string {
	r := st.Request
	var b strings.Builder
	fmt.Fprintf(&b, "%s %d: %s %s %s", phase, n, r.Method, r.Service, r.Path)
	pairs := func(label string, m map[string]string) {
		if len(m) == 0 {
			return
		}
		b.WriteString(" " + label)
		for _, k := range slices.Sorted(maps.Keys(m)) {
			fmt.Fprintf(&b, " %s=%s", k, m[k])
		}
	}
	pairs("query", r.Query)
	pairs("headers", r.Headers)
	if r.JSON != nil {
		b.WriteString(" json " + compactJSON(r.JSON))
	}
	if u := st.Until; u != nil {
		fmt.Fprintf(&b, " until %s equals %s within %s", u.JSONPath, compactJSON(u.Equals), u.Timeout)
		if u.Every > 0 {
			fmt.Fprintf(&b, " every %s", u.Every)
		}
	}
	if len(st.Captures) > 0 {
		// The JSONPath decides which record create captures, and so which one cleanup undoes.
		caps := make([]string, len(st.Captures))
		for i, c := range st.Captures {
			caps[i] = c.Name + "=" + c.JSONPath
			if c.Element != "" {
				caps[i] += " element " + c.Element
			}
		}
		b.WriteString(", captures " + strings.Join(caps, ", "))
	}
	return b.String()
}

// oneLine shows a line feed or a tab as an escape, so that a value cannot end its line and start
// another that looks like a step.
func oneLine(s string) string {
	return strings.NewReplacer("\n", `\n`, "\t", `\t`).Replace(s)
}

func compactJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Sprintf("(%v)", err)
	}
	return strings.TrimSpace(buf.String())
}
