package export

import (
	"github.com/jancuk/semar/internal/domain"
)

// suiteForJourneys holds the journey cases of JUnit and CTRF, after every ticket suite.
const suiteForJourneys = "semar journeys"

// journeyCase is what a format writes for one journey: a test of its own, in addition to its legs.
type journeyCase struct {
	Result     domain.JourneyResult
	Name       string
	DurationMS int64 // the time of the legs that the run stores
	Message    string
	Detail     string
}

func journeyName(id domain.JourneyID) string { return "journey " + cutMessage(string(id)) }

// journeyCases gives one case per stored journey result, in stored order. A journey result gets
// no message when it has no reason.
func journeyCases(r Run) []journeyCase {
	ms := map[domain.ScenarioID]int64{}
	for _, sc := range r.Scenarios {
		ms[sc.Result.Scenario] += sc.DurationMS
	}
	cases := make([]journeyCase, len(r.Run.Journeys))
	for i, j := range r.Run.Journeys {
		c := journeyCase{Result: j, Name: journeyName(j.Journey)}
		for _, l := range j.Legs {
			c.DurationMS += ms[l.Scenario]
		}
		c.Message, c.Detail = reasonTexts(j.Reasons)
		cases[i] = c
	}
	return cases
}

// ctrfJourneyStatus maps a journey outcome as ctrfStatus maps a scenario outcome: blocked and
// stale are never a pass and never skipped.
func ctrfJourneyStatus(o domain.JourneyOutcome) string {
	switch o {
	case domain.JourneyPassed:
		return "passed"
	case domain.JourneyFailed:
		return "failed"
	case domain.JourneyNotRun:
		return "skipped"
	default:
		return "other"
	}
}
