// Package sends says in words what a step of a stored script sends. A person who reads a test
// reads these lines in plan.md, and so they hold the values as the script does:
// a variable stays {{name}}, and nothing is cut or escaped. The caller makes the text safe for where
// it prints it.
package sends

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/jancuk/semar/internal/domain"
)

// Lines are what the step sends, one entry per line: for an API step its method and path and then
// Details, for a web or a mobile step its action and then its text, for a step that uses a flow the
// flow and its values. A step with none of them
// sends nothing.
func Lines(st domain.ScriptStep) []string {
	switch {
	case st.Request != nil:
		return append([]string{st.Request.Method + " " + st.Request.Path}, Details(*st.Request)...)
	case st.Web != nil:
		return webLines(*st.Web)
	case st.Mobile != nil:
		return mobileLines(*st.Mobile)
	case st.Use != "":
		return useLines(st)
	}
	return nil
}

// useLines says that a step runs a flow, and with which values. The values stay as the script writes
// them, so a variable stays {{name}}.
func useLines(st domain.ScriptStep) []string {
	out := []string{"use flow " + st.Use}
	if len(st.With) > 0 {
		var b strings.Builder
		b.WriteString("with")
		for _, k := range slices.Sorted(maps.Keys(st.With)) {
			fmt.Fprintf(&b, " %s=%s", k, st.With[k])
		}
		out = append(out, b.String())
	}
	return out
}

// Details are the query, the headers, and the JSON body of a request, in the form of a fixture step.
// A part that the request does not have gives no entry.
func Details(r domain.APIRequest) []string {
	var out []string
	pairs := func(label string, m map[string]string) {
		if len(m) == 0 {
			return
		}
		var b strings.Builder
		b.WriteString(label)
		for _, k := range slices.Sorted(maps.Keys(m)) {
			fmt.Fprintf(&b, " %s=%s", k, m[k])
		}
		out = append(out, b.String())
	}
	pairs("query", r.Query)
	pairs("headers", r.Headers)
	if r.JSON != nil {
		out = append(out, "json "+CompactJSON(r.JSON))
	}
	return out
}

// CompactJSON is v as one line of JSON that keeps <, >, and & as they are.
func CompactJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Sprintf("(%v)", err)
	}
	return strings.TrimSpace(buf.String())
}

func webLines(w domain.WebAction) []string {
	parts := []string{string(w.Do)}
	if w.Path != "" {
		parts = append(parts, w.Path)
	}
	if w.Element != "" {
		parts = append(parts, w.Element)
	}
	if w.To != "" {
		parts = append(parts, "to", w.To)
	}
	if w.Screen != "" {
		parts = append(parts, "on screen", w.Screen)
	}
	out := []string{strings.Join(parts, " ")}
	switch {
	case w.Secret != "":
		out = append(out, "text "+secretWords(w.Secret))
	case w.Value != "":
		out = append(out, "text "+strconv.Quote(w.Value))
	}
	return out
}

// secretWords names the secret that a step types and never its value. Semar reads a secret of the
// kind env:NAME from the environment variable NAME.
func secretWords(ref string) string {
	if name, ok := strings.CutPrefix(ref, "env:"); ok {
		return "the value of $" + name
	}
	return "the value of " + ref
}

func mobileLines(m domain.MobileAction) []string {
	parts := []string{string(m.Do)}
	if m.Link != "" {
		parts = append(parts, m.Link)
	}
	if m.Do == domain.MobilePress {
		parts = append(parts, m.Value)
	}
	if m.Element != "" {
		parts = append(parts, m.Element)
	}
	if m.Within != "" {
		parts = append(parts, "in", m.Within)
	}
	if m.Screen != "" {
		parts = append(parts, "on screen", m.Screen)
	}
	out := []string{strings.Join(parts, " ")}
	switch {
	case m.Secret != "":
		out = append(out, "text "+secretWords(m.Secret))
	case m.Do == domain.MobilePress:
	case m.Value != "" && (m.Do == domain.MobileType):
		out = append(out, "text "+strconv.Quote(m.Value))
	case m.Value != "":
		out = append(out, "direction "+m.Value)
	}
	return out
}
