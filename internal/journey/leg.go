package journey

import (
	"cmp"
	"maps"
	"slices"

	"github.com/jancuk/semar/internal/domain"
)

// LegScenario is the scenario and the script of one leg (TRD rule 5, plan decision D3). The
// journey's given entries are not copied: the runner resolves them once for the whole journey
// and passes their outputs as variables. An ID that is not a leg of the journey gives zero values.
func LegScenario(j domain.Journey, leg domain.LegID, authProfile string) (domain.Scenario, domain.Script) {
	for _, l := range j.Legs {
		if l.ID == leg {
			return legScenario(j, l, authProfile)
		}
	}
	return domain.Scenario{}, domain.Script{}
}

func legScenario(j domain.Journey, leg domain.Leg, authProfile string) (domain.Scenario, domain.Script) {
	sc := domain.Scenario{
		APIVersion:  "semar/v1",
		Kind:        "Scenario",
		ID:          legScenarioID(j.ID, leg.ID),
		Title:       j.Title + ": " + string(leg.ID),
		Ticket:      string(j.ID),
		Intent:      "Leg " + string(leg.ID) + " of journey " + string(j.ID) + ": " + j.Title,
		Surface:     cmp.Or(leg.Surface, domain.SurfaceAPI),
		AuthProfile: authProfile,
		Steps:       make([]domain.Step, len(leg.Steps)),
	}
	sr := domain.Script{
		APIVersion: "semar/v1",
		Kind:       "Script",
		Scenario:   sc.ID,
		AuthoredBy: domain.ProviderRef{Instance: "journey", Harness: "file"},
		Steps:      make([]domain.ScriptStep, len(leg.Steps)),
	}
	for i, st := range leg.Steps {
		as := cmp.Or(st.As, leg.As)
		sc.Steps[i] = domain.Step{ID: st.ID, Setup: st.Setup, Action: st.Action, Hint: st.Hint, Expect: st.Expect, As: as}
		if st.Web != nil {
			w := *st.Web
			w.Service, w.App = leg.Service, leg.App
			sr.Steps[i] = domain.ScriptStep{Step: st.ID, Web: &w, Captures: st.Captures}
			continue
		}
		req := st.Request
		req.Service = leg.Service
		req.Query = maps.Clone(req.Query)
		req.Headers = maps.Clone(req.Headers)
		sr.Steps[i] = domain.ScriptStep{Step: st.ID, Request: &req, Captures: st.Captures}
	}
	sr.ExpectHash = domain.ExpectHash(sc)
	return sc, sr
}

func legScenarioID(j domain.JourneyID, leg domain.LegID) domain.ScenarioID {
	return domain.ScenarioID(string(j) + "." + string(leg))
}

// ActedAs lists the auth profiles that the steps of a leg scenario act as, each once, in step order.
// It is nil when no step has an as.
func ActedAs(sc domain.Scenario) []string {
	var out []string
	for _, st := range sc.Steps {
		if st.As != "" && !slices.Contains(out, st.As) {
			out = append(out, st.As)
		}
	}
	return out
}
