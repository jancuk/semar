package plan

import "strings"

// CheckAnswer converts an answer and then runs it through the rules that the whole draft goes
// through, as a draft of its own: the scenario rules, the rules of a manual check and of a
// notDrafted entry, the cites, the titles that exist already, and the variables that a step uses.
// The problems say where in the answer they are. The result comes back only when there are none.
func CheckAnswer(in Input, p Packet, a Answer) (CriterionResult, []Problem) {
	res, ps := p.Convert(a, in)
	if len(ps) > 0 {
		return CriterionResult{}, ps
	}
	d := Draft{}
	for _, t := range in.Change.Tickets {
		d.Tickets = append(d.Tickets, TicketDraft{Key: t.Key, Kind: "behaviour", Risk: "standard"})
	}
	var prefix string
	switch {
	case res.Scenario != nil:
		d.Scenarios, prefix = []ScenarioDraft{*res.Scenario}, "scenarios[0]"
	case res.Manual != nil:
		d.ManualChecks, prefix = []ManualCheckDraft{*res.Manual}, "manualChecks[0]"
	default:
		d.NotDrafted, prefix = []NotDrafted{*res.NotDrafted}, "notDrafted[0]"
	}
	// The limit of scenarios and the entries of other criteria are the business of the whole draft.
	one := in
	one.Limits.MaxScenarios = 0
	_, _, found := Validate(d, one)
	for _, pr := range found {
		path, ok := strings.CutPrefix(pr.Path, prefix)
		if !ok {
			continue
		}
		ps = append(ps, Problem{Path: strings.TrimPrefix(path, "."), Message: pr.Message})
	}
	if len(ps) > 0 {
		return CriterionResult{}, ps
	}
	return res, nil
}
