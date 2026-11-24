package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/jancuk/semar/internal/domain"
)

// maxMissesShown is how many misses the summary and the report name before the rest is a count.
const maxMissesShown = 5

// Miss is an element that a step on a device looked for and did not find.
type Miss struct {
	Scenario, Step, Screen, Element string
	// Looked is the locator that the step used.
	Looked string
	// Candidates is how many locators the repair proposes. Closest are the visible elements that look
	// most like the element, up to five.
	Candidates int
	Closest    []string
}

// Misses lists the elements that the device actions of a run did not find. A later action of the same
// step that missed the same element replaces an earlier one, and the order is that of the first miss.
func Misses(acts []domain.Activity) []Miss {
	var out []Miss
	index := map[string]int{}
	for _, a := range acts {
		if a.Surface != domain.SurfaceAndroid {
			continue
		}
		for _, m := range a.Misses {
			x := Miss{Scenario: string(a.Use.Scenario), Step: string(a.Use.Step), Screen: m.Screen, Element: m.Element,
				Looked: m.Looked.String(), Candidates: len(m.Repair)}
			for _, c := range m.Nearby {
				x.Closest = append(x.Closest, c.Why)
			}
			key := strings.Join([]string{x.Scenario, x.Step, x.Screen, x.Element}, "\x00")
			if i, seen := index[key]; seen {
				out[i] = x
				continue
			}
			index[key] = len(out)
			out = append(out, x)
		}
	}
	return out
}

// repairCommand is the command that shows the screen of a run, or applies the first candidate.
func (m Miss) repairCommand(run string) string {
	if m.Candidates > 0 && m.Screen != "" {
		return fmt.Sprintf("semar screens repair %s --apply %s.%s=1", run, m.Screen, m.Element)
	}
	return fmt.Sprintf("semar screens repair %s", run)
}

// what is the element as a person names it.
func (m Miss) what() string {
	if m.Screen != "" {
		return m.Screen + "." + m.Element
	}
	return m.Element
}

// WriteMisses prints the elements that were not found, up to five, with the closest elements on the
// screen and the command that repairs the screens file. It prints nothing when there is no miss.
func WriteMisses(w io.Writer, runID string, misses []Miss) {
	if len(misses) == 0 {
		return
	}
	fmt.Fprintf(w, "elements not found: %d\n", len(misses))
	for i, m := range misses {
		if i == maxMissesShown {
			fmt.Fprintf(w, "  and %d more: semar screens repair %s lists them all\n", len(misses)-maxMissesShown, plain(runID))
			break
		}
		fmt.Fprintf(w, "  %s step %s: %s was not found (looked for %s)\n", plain(m.Scenario), plain(m.Step), plain(m.what()), plain(m.Looked))
		if len(m.Closest) > 0 {
			fmt.Fprintf(w, "    closest on the screen: %s\n", plain(strings.Join(m.Closest, "; ")))
		}
		if m.Candidates > 0 {
			fmt.Fprintf(w, "    to use the first candidate: %s\n", plain(m.repairCommand(runID)))
		} else {
			fmt.Fprintf(w, "    to see the screen: %s\n", plain(m.repairCommand(runID)))
		}
	}
}

// missView is one miss in the report.
type missView struct{ Scenario, Step, What, Looked, Closest, Command string }

func (vw viewer) missViews(run string, acts []domain.Activity) []missView {
	var out []missView
	for i, m := range Misses(acts) {
		if i == maxMissesShown {
			break
		}
		out = append(out, missView{
			Scenario: vw.show(m.Scenario), Step: vw.show(m.Step), What: vw.show(m.what()), Looked: vw.show(m.Looked),
			Closest: vw.show(strings.Join(m.Closest, "; ")), Command: vw.show(m.repairCommand(run)),
		})
	}
	return out
}
