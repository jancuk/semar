package plan

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/knowledge"
)

const againFile = `# Refunds

## Acceptance criteria

1. A refund above the total is refused. It ends in 422.
2. A refund of the whole total is accepted.
3. The owner sees the refund on the web.
`

func againTicket(text string) []knowledge.Ticket {
	return []knowledge.Ticket{{Key: "SHOP-7", Path: ticket482, Text: text}}
}

func keyOfNumber(t *testing.T, text string, n int) string {
	t.Helper()
	for _, c := range Criteria(knowledge.Ticket{Key: "SHOP-7", Path: ticket482, Text: text}) {
		if c.Number == n {
			return c.Key
		}
	}
	t.Fatalf("no criterion %d", n)
	return ""
}

func ids(s ...string) []domain.ScenarioID {
	var out []domain.ScenarioID
	for _, x := range s {
		out = append(out, domain.ScenarioID(x))
	}
	return out
}

func states(a Again) string {
	var parts []string
	for _, s := range a.Criteria {
		parts = append(parts, fmt.Sprintf("%d:%s", s.Number, s.State))
	}
	return strings.Join(parts, " ")
}

func TestAgainKeepsACriterionWhoseEntryHasATestOrIsAManualCheck(t *testing.T) {
	k1, k3 := keyOfNumber(t, againFile, 1), keyOfNumber(t, againFile, 3)
	a := Classify(againTicket(againFile), map[string][]Earlier{"SHOP-7": {
		{Number: 1, Key: k1, Tests: ids("SHOP-7-01")},
		{Number: 3, Key: k3, Manual: true},
	}})
	if got := states(a); got != "1:kept 2:new 3:kept" {
		t.Fatalf("states %q", got)
	}
	if !reflect.DeepEqual(a.Criteria[0].Tests, ids("SHOP-7-01")) || !a.Criteria[2].Manual {
		t.Errorf("kept criteria lost what keeps them: %+v", a.Criteria)
	}
	if len(a.Removed) != 0 {
		t.Errorf("removed %+v", a.Removed)
	}
}

func TestAgainPlansACriterionWhoseEntryHasNoTest(t *testing.T) {
	k1 := keyOfNumber(t, againFile, 1)
	a := Classify(againTicket(againFile), map[string][]Earlier{"SHOP-7": {{Number: 1, Key: k1}}})
	if got := states(a); got != "1:new 2:new 3:new" {
		t.Fatalf("states %q", got)
	}
	if !a.Criteria[0].Entered || a.Criteria[1].Entered {
		t.Errorf("only criterion 1 has an entry: %+v", a.Criteria)
	}
}

func TestAgainCallsAnEditedCriterionChangedAndNamesTheOldTest(t *testing.T) {
	old := keyOfNumber(t, againFile, 2)
	edited := strings.Replace(againFile, "is accepted", "is accepted and the order is closed", 1)
	a := Classify(againTicket(edited), map[string][]Earlier{"SHOP-7": {
		{Number: 1, Key: keyOfNumber(t, againFile, 1), Tests: ids("SHOP-7-01")},
		{Number: 2, Key: old, Tests: ids("SHOP-7-02")},
	}})
	if got := states(a); got != "1:kept 2:changed 3:new" {
		t.Fatalf("states %q", got)
	}
	if !reflect.DeepEqual(a.Criteria[1].Tests, ids("SHOP-7-02")) {
		t.Errorf("the changed criterion does not name the old test: %+v", a.Criteria[1])
	}
	if len(a.Removed) != 0 {
		t.Errorf("the old entry of a changed criterion is also removed: %+v", a.Removed)
	}
}

func TestAgainReportsAnEntryOfACriterionThatIsGoneAsRemoved(t *testing.T) {
	gone := strings.Repeat("c", 64)
	a := Classify(againTicket(againFile), map[string][]Earlier{"SHOP-7": {
		{Number: 9, Key: gone, Tests: ids("SHOP-7-09")},
		{Number: 8, Key: strings.Repeat("d", 64)}, // not drafted: nothing to remove
	}})
	if len(a.Removed) != 1 || a.Removed[0].Number != 9 || a.Removed[0].Ticket != "SHOP-7" || !reflect.DeepEqual(a.Removed[0].Tests, ids("SHOP-7-09")) {
		t.Errorf("removed %+v", a.Removed)
	}
	if got := states(a); got != "1:new 2:new 3:new" {
		t.Errorf("states %q", got)
	}
}

func TestAgainDoesNotCallASwappedOrRenumberedCriterionChanged(t *testing.T) {
	k := func(n int) string { return keyOfNumber(t, againFile, n) }
	renumbered := strings.NewReplacer("1. A refund above", "2. A refund above", "2. A refund of", "1. A refund of").Replace(againFile)
	a := Classify(againTicket(renumbered), map[string][]Earlier{"SHOP-7": {
		{Number: 1, Key: k(1), Tests: ids("SHOP-7-01")},
		{Number: 2, Key: k(2), Tests: ids("SHOP-7-02")},
	}})
	if got := states(a); got != "2:kept 1:kept 3:new" {
		t.Errorf("states %q", got)
	}
}

func TestAgainMatchesAnEntryWithoutAKeyByItsCite(t *testing.T) {
	cite := fmt.Sprintf("%s:5 A refund above the total is refused", ticket482)
	manual := fmt.Sprintf("%s:7 The owner sees the refund on the web", ticket482)
	a := Classify(againTicket(againFile), map[string][]Earlier{"SHOP-7": {
		{Cites: []string{cite}, Tests: ids("SHOP-7-01")},
		{Cites: []string{manual}, Manual: true},
	}})
	if got := states(a); got != "1:kept 2:new 3:kept" {
		t.Fatalf("states %q", got)
	}
	// The line of the cite is not read: a criterion that moved is still found.
	moved := Classify(againTicket("Intro.\n\n\n"+againFile), map[string][]Earlier{"SHOP-7": {{Cites: []string{cite}, Tests: ids("SHOP-7-01")}}})
	if got := states(moved); got != "1:kept 2:new 3:new" {
		t.Errorf("a moved criterion was not found: %q", got)
	}
}

func TestAgainReportsAnEntryWithoutAKeyThatMatchesNothingAsRemoved(t *testing.T) {
	cite := fmt.Sprintf("%s:5 A refund above the total is refused for a minor", ticket482)
	a := Classify(againTicket(againFile), map[string][]Earlier{"SHOP-7": {{Cites: []string{cite}, Tests: ids("SHOP-7-01")}}})
	if len(a.Removed) != 1 || a.Removed[0].Number != 0 || a.Removed[0].Cite == "" || !reflect.DeepEqual(a.Removed[0].Tests, ids("SHOP-7-01")) {
		t.Errorf("removed %+v", a.Removed)
	}
	if got := states(a); got != "1:new 2:new 3:new" {
		t.Errorf("states %q", got)
	}
}

func TestAgainSharesNothingBetweenTickets(t *testing.T) {
	a := Classify(againTicket(againFile), map[string][]Earlier{"SHOP-8": {{Number: 1, Key: keyOfNumber(t, againFile, 1), Tests: ids("SHOP-8-01")}}})
	if got := states(a); got != "1:new 2:new 3:new" || len(a.Removed) != 0 {
		t.Errorf("states %q, removed %+v", got, a.Removed)
	}
}

func TestAgainTakesAnEntryThatNamesOnlyAPlaceAsAnEntryAndNothingMore(t *testing.T) {
	a := Classify(againTicket(againFile), map[string][]Earlier{"SHOP-7": {{Cites: []string{ticket482 + ":6"}, Tests: ids("SHOP-7-02")}}})
	if got := states(a); got != "1:new 2:new 3:new" {
		t.Fatalf("states %q", got)
	}
	if a.Criteria[0].Entered || !a.Criteria[1].Entered || len(a.Removed) != 0 {
		t.Errorf("entered %v %v, removed %+v", a.Criteria[0].Entered, a.Criteria[1].Entered, a.Removed)
	}
}
