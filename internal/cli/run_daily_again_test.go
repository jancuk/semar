package cli_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/knowledge"
	"github.com/jancuk/semar/internal/stage/plan"
	"github.com/jancuk/semar/internal/usage"
)

const offerTicketText = "# SHOP-490 Show the limit\n\n## Acceptance criteria\n\n1. The receipt shows the limit.\n2. The limit is in the API.\n3. The limit is in the email.\n"

// scenarioOfTicket writes a scenario file for a ticket, from the one that the repository has.
func (r *repo) scenarioOfTicket(t *testing.T, ticket, id string) {
	t.Helper()
	data, err := os.ReadFile(r.path(".semar", "scenarios", "SHOP-482", scenarioID+".feature"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(strings.Replace(string(data), "@id:"+scenarioID, "@id:"+id, 1), "@ticket:SHOP-482", "@ticket:"+ticket, 1)
	r.writeRel(t, ".semar/scenarios/"+ticket+"/"+id+".feature", text)
}

// plannedOffer is a branch that changes SHOP-490, whose ticket file has three criteria. The plan that is
// there wrote a test for each criterion in kept, and a case that was not drafted for each one in
// notDrafted, by its number, for the text in planned.
func plannedOffer(t *testing.T, planned string, kept, notDrafted []int) *repo {
	t.Helper()
	r := dailyRepo(t)
	r.git(t, "commit", "--quiet", "--allow-empty", "-m", "SHOP-490 show the limit in the receipt")
	r.writeRel(t, "docs/tickets/SHOP-490.md", offerTicketText)
	keys := map[int]string{}
	for _, c := range plan.Criteria(knowledge.Ticket{Key: "SHOP-490", Path: "docs/tickets/SHOP-490.md", Text: planned}) {
		keys[c.Number] = c.Key
	}
	var entries []string
	for _, n := range kept {
		id := "SHOP-490-0" + string(rune('0'+n))
		r.scenarioOfTicket(t, "SHOP-490", id)
		entries = append(entries, entry("SHOP-490/scenario-"+id, "Test of "+id, "covers: ["+id+"]", "criterion: {number: "+string(rune('0'+n))+", key: "+keys[n]+"}"))
	}
	for _, n := range notDrafted {
		entries = append(entries, entry("SHOP-490/not-drafted-"+string(rune('0'+n)), "Case "+string(rune('0'+n)), "gap: \"not drafted: needs a fixture\"", "criterion: {number: "+string(rune('0'+n))+", key: "+keys[n]+"}"))
	}
	r.writeRel(t, ".semar/obligations/SHOP-490.plan.yaml", obligations("SHOP-490", entries...))
	r.opts.Home = t.TempDir()
	rec := usage.Record{At: time.Now(), Provider: "claude", Kind: "claude", Pipeline: usage.PipelineCriteria, Asked: "haiku", Outcome: "ok", Attempts: 4, CostUSD: 0.08}
	if err := usage.Append(r.opts.Home, rec); err != nil {
		t.Fatal(err)
	}
	r.opts.Terminal = func() bool { return true }
	return r
}

func TestRunOffersToPlanAnAddedCriterionAndEstimatesOnlyIt(t *testing.T) {
	r := plannedOffer(t, offerTicketText, []int{1, 2}, nil)
	r.opts.Stdin = strings.NewReader("n\n")
	res := r.semar(t, t.Context(), "run")
	contains(t, "the offer", res.stdout, "SHOP-490 has criteria with no test or manual check yet. Plan it now with claude with haiku, about $0.02 for 1 criterion, from 4 earlier jobs of claude haiku? [y/N]")
}

func TestRunOffersToPlanAnEditedCriterion(t *testing.T) {
	r := plannedOffer(t, strings.Replace(offerTicketText, "The limit is in the API.", "The limit is in the API for a customer.", 1), []int{1, 2, 3}, nil)
	r.opts.Stdin = strings.NewReader("n\n")
	res := r.semar(t, t.Context(), "run")
	contains(t, "the offer", res.stdout, "SHOP-490 has criteria with no test or manual check yet. Plan it now with", "for 1 criterion")
}

func TestRunDoesNotOfferToPlanACriterionThatWasNotDraftedBefore(t *testing.T) {
	r := plannedOffer(t, offerTicketText, []int{1, 2}, []int{3})
	r.opts.Stdin = strings.NewReader("")
	res := r.semar(t, t.Context(), "run")
	if strings.Contains(res.stdout, "Plan it now") {
		t.Errorf("run offered to plan a criterion that has an entry:\n%s", res.stdout)
	}
}

func TestRunDoesNotOfferToPlanWhenEveryCriterionHasATest(t *testing.T) {
	r := plannedOffer(t, offerTicketText, []int{1, 2, 3}, nil)
	r.opts.Stdin = strings.NewReader("")
	res := r.semar(t, t.Context(), "run")
	if strings.Contains(res.stdout, "Plan it now") {
		t.Errorf("run offered to plan with nothing missing:\n%s", res.stdout)
	}
}
