package report

import (
	"fmt"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/words"
)

type journeyLegView struct{ Leg, Outcome, As, Reason string }

type invariantView struct{ Number, State, Values, Detail string }

type journeyView struct {
	ID, Title, Outcome, First string
	Next                      string
	Reasons                   []string
	Manifest, Hash            string
	ManifestEnd               string
	Legs                      []journeyLegView
	Invariants                []invariantView
	Fixtures                  []string
	Reproduce                 string
}

// JourneyExitCode is 0 when every journey passed, 1 when one failed, and 2 otherwise, so that a
// journey that was blocked, stale, or not run never reads as a pass.
func JourneyExitCode(journeys []domain.JourneyResult) int {
	if len(journeys) == 0 {
		return 2
	}
	code := 0
	for _, j := range journeys {
		switch j.Outcome {
		case domain.JourneyFailed:
			return 1
		case domain.JourneyPassed:
		default:
			code = 2
		}
	}
	return code
}

func (vw viewer) journeyViews(d Data) []journeyView {
	var out []journeyView
	for _, j := range d.Journeys {
		v := journeyView{
			ID:        vw.show(string(j.Journey)),
			Title:     vw.show(d.JourneyTitles[j.Journey]),
			Outcome:   vw.show(string(j.Outcome)),
			First:     vw.show(string(j.FirstFailing)),
			Manifest:  vw.manifestText(j.Manifest),
			Hash:      vw.show(string(j.Manifest.Hash[:min(len(j.Manifest.Hash), 12)])),
			Reproduce: vw.journeyCommand(j.Journey, d.Run.Environment),
			Next:      words.Next(string(j.Outcome), string(j.Journey)),
		}
		for _, r := range j.Reasons {
			v.Reasons = append(v.Reasons, vw.show(r.Text))
		}
		switch {
		case j.ManifestEnd == nil:
			v.ManifestEnd = "not read"
		case j.ManifestEnd.Hash != j.Manifest.Hash:
			v.ManifestEnd = vw.manifestText(*j.ManifestEnd)
		}
		for _, l := range j.Legs {
			lv := journeyLegView{Leg: vw.show(string(l.Leg)), Outcome: vw.show(string(l.Outcome))}
			if len(l.As) > 0 {
				lv.As = vw.show(strings.Join(l.As, ", "))
			}
			if len(l.Reasons) > 0 {
				lv.Reason = vw.show(l.Reasons[0].Text)
			}
			v.Legs = append(v.Legs, lv)
		}
		for _, inv := range j.Invariants {
			iv := invariantView{Number: fmt.Sprint(inv.Index + 1), State: vw.show(string(inv.State)), Detail: vw.show(inv.Detail)}
			shownValues := make([]string, len(inv.Values))
			for i, x := range inv.Values {
				shownValues[i] = vw.show(x)
			}
			iv.Values = strings.Join(shownValues, ", ")
			v.Invariants = append(v.Invariants, iv)
		}
		for _, rec := range j.Fixtures {
			v.Fixtures = append(v.Fixtures, vw.fixtureLine(rec))
		}
		out = append(out, v)
	}
	return out
}

// manifestText is each service with its commit, in the order of the manifest.
func (vw viewer) manifestText(m domain.Manifest) string {
	if len(m.Services) == 0 {
		return "none recorded"
	}
	parts := make([]string, len(m.Services))
	for i, b := range m.Services {
		if b.Declared {
			parts[i] = vw.show(declaredBuild(b))
		} else {
			parts[i] = vw.show(fmt.Sprintf("%s %s config %s", b.Service, b.Commit, b.ConfigHash))
		}
	}
	return strings.Join(parts, "; ")
}

// journeyCommand reruns a journey. The environment is left out when the run has none.
func (vw viewer) journeyCommand(id domain.JourneyID, env string) string {
	cmd := "semar journey run " + vw.show(string(id))
	if env != "" {
		cmd += " --env " + vw.show(env)
	}
	return cmd
}

// legJourneys maps the scenario of each leg to its journey, because a leg has no scenario file
// that semar run could replay.
func legJourneys(journeys []domain.JourneyResult) map[domain.ScenarioID]domain.JourneyID {
	m := map[domain.ScenarioID]domain.JourneyID{}
	for _, j := range journeys {
		for _, l := range j.Legs {
			m[l.Scenario] = j.Journey
		}
	}
	return m
}

// journeyStillExists names what a journey's fixtures left behind.
func (vw viewer) journeyStillExists(journeys []domain.JourneyResult) []string {
	var out []string
	for _, j := range journeys {
		for _, rec := range j.Fixtures {
			for _, left := range rec.Left {
				out = append(out, vw.show(string(j.Journey))+": "+vw.show(left))
			}
		}
	}
	return out
}

// journeyLines is the part of the summary that a journey run prints: one line per journey with
// its first reason, then one line per leg.
func journeyLines(journeys []domain.JourneyResult) []string {
	var lines []string
	for _, j := range journeys {
		word := string(j.Outcome)
		line := plain(string(j.Journey)) + "\t" + plain(word)
		if text := journeyHead(j); text != "" {
			head, more := cut(text, maxShown)
			line += "\t" + plain(head)
			if more > 0 {
				line += cutMark(more)
			}
		}
		line += nextMark(line, word, string(j.Journey))
		lines = append(lines, line)
		for _, l := range j.Legs {
			line := "  " + plain(string(l.Leg)) + "\t" + plain(string(l.Outcome))
			if len(l.As) > 0 {
				line += " as " + plain(strings.Join(l.As, ", "))
			}
			if len(l.Reasons) > 0 {
				head, more := cut(l.Reasons[0].Text, maxShown)
				line += "\t" + plain(head)
				if more > 0 {
					line += cutMark(more)
				}
			}
			lines = append(lines, line)
		}
	}
	return lines
}

// journeyHead says why a journey is not passed. When the first reason of the journey comes from its
// first failing leg, the head is that leg's own first reason. Otherwise, as for a changed build or an
// invariant, it is the journey's reason, and the first failing leg follows when there is one.
func journeyHead(j domain.JourneyResult) string {
	if j.Outcome == domain.JourneyPassed {
		return ""
	}
	first := ""
	if j.FirstFailing != "" {
		first = "first failing leg " + string(j.FirstFailing)
	}
	fromLeg := len(j.Reasons) == 0 || first != "" && strings.HasPrefix(j.Reasons[0].Text, "leg "+string(j.FirstFailing)+" ")
	if fromLeg {
		if i := slices.IndexFunc(j.Legs, func(l domain.LegResult) bool { return l.Leg == j.FirstFailing }); i >= 0 && len(j.Legs[i].Reasons) > 0 {
			return first + ": " + j.Legs[i].Reasons[0].Text
		}
		return first
	}
	if first != "" {
		return j.Reasons[0].Text + "; " + first
	}
	return j.Reasons[0].Text
}
