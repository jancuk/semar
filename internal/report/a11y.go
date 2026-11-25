package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/jancuk/semar/internal/domain"
)

// maxA11yLines is how many accessibility warnings CLI stdout prints before the rest is a count.
const maxA11yLines = 20

// A11yFinding is one non-failing accessibility warning found on a mobile screen during a run.
type A11yFinding struct {
	Scenario   string
	Step       string
	Screen     string
	Kind       string
	Class      string
	Role       string
	ResourceID string
	Label      string
	Bounds     string
	WidthDP    int
	HeightDP   int
}

// Text describes the finding in one plain phrase.
func (f A11yFinding) Text() string {
	who := a11ySubject(f)
	switch f.Kind {
	case "missing_label":
		if f.Bounds != "" {
			return fmt.Sprintf("missing label on %s (%s)", who, f.Bounds)
		}
		return fmt.Sprintf("missing label on %s", who)
	case "small_target":
		size := ""
		if f.WidthDP > 0 && f.HeightDP > 0 {
			size = fmt.Sprintf(" (%dx%ddp, min 48dp)", f.WidthDP, f.HeightDP)
		}
		if f.Bounds != "" {
			return fmt.Sprintf("small touch target%s on %s (%s)", size, who, f.Bounds)
		}
		return fmt.Sprintf("small touch target%s on %s", size, who)
	default:
		return fmt.Sprintf("%s on %s", f.Kind, who)
	}
}

func a11ySubject(f A11yFinding) string {
	var parts []string
	kind := f.Role
	if kind == "" && f.Class != "" {
		kind = f.Class
		if i := strings.LastIndex(kind, "."); i >= 0 {
			kind = kind[i+1:]
		}
	}
	if kind != "" {
		parts = append(parts, kind)
	}
	if f.Label != "" {
		parts = append(parts, fmt.Sprintf("%q", f.Label))
	}
	if f.ResourceID != "" {
		id := f.ResourceID
		if i := strings.Index(id, ":id/"); i >= 0 {
			id = id[i+4:]
		}
		parts = append(parts, "id="+id)
	}
	if len(parts) == 0 {
		return "control"
	}
	return strings.Join(parts, " ")
}

// A11yFindings extracts accessibility warnings from device activities, deduplicating identical
// (Scenario, Screen, Kind, ResourceID, Class, Bounds) entries per scenario in order of first sight.
func A11yFindings(acts []domain.Activity) []A11yFinding {
	var out []A11yFinding
	seen := map[string]bool{}
	for _, a := range acts {
		if a.Surface != domain.SurfaceAndroid || len(a.Accessibility) == 0 {
			continue
		}
		scenario := string(a.Use.Scenario)
		step := string(a.Use.Step)
		for _, iss := range a.Accessibility {
			key := strings.Join([]string{scenario, iss.Screen, iss.Kind, iss.ResourceID, iss.Class, iss.Bounds}, "\x00")
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, A11yFinding{
				Scenario:   scenario,
				Step:       step,
				Screen:     iss.Screen,
				Kind:       iss.Kind,
				Class:      iss.Class,
				Role:       iss.Role,
				ResourceID: iss.ResourceID,
				Label:      iss.Label,
				Bounds:     iss.Bounds,
				WidthDP:    iss.WidthDP,
				HeightDP:   iss.HeightDP,
			})
		}
	}
	return out
}

// WriteA11yWarnings prints accessibility warnings to standard output. It prints nothing when there
// are none.
func WriteA11yWarnings(w io.Writer, env string, findings []A11yFinding) {
	if len(findings) == 0 {
		return
	}
	noun := "warnings"
	if len(findings) == 1 {
		noun = "warning"
	}
	fmt.Fprintf(w, "accessibility checks: %d %s from environments.%s.checks, no outcome changed\n", len(findings), noun, plain(env))
	for i, f := range findings {
		if i == maxA11yLines {
			fmt.Fprintf(w, "  and %d more, in the report\n", len(findings)-i)
			break
		}
		where := ""
		if f.Screen != "" {
			where = " (" + plain(f.Screen) + ")"
		}
		fmt.Fprintf(w, "  %s step %s%s: %s\n", plain(f.Scenario), plain(f.Step), where, plain(f.Text()))
	}
}

type a11yView struct{ Scenario, Step, Screen, Text string }

func (vw viewer) a11yViews(acts []domain.Activity) []a11yView {
	findings := A11yFindings(acts)
	if len(findings) == 0 {
		return nil
	}
	out := make([]a11yView, 0, len(findings))
	for _, f := range findings {
		out = append(out, a11yView{
			Scenario: vw.show(f.Scenario),
			Step:     vw.show(f.Step),
			Screen:   vw.show(f.Screen),
			Text:     vw.show(f.Text()),
		})
	}
	return out
}
