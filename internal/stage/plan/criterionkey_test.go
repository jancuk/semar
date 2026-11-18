package plan

import (
	"strings"
	"testing"
)

func criterionKeys(text string) []string {
	var keys []string
	for _, c := range criteriaOf(text) {
		keys = append(keys, c.Key)
	}
	return keys
}

func TestCriterionKeyIsOfTheWholeTextWithoutItsNumberOrSpacing(t *testing.T) {
	base := criterionKeys("## Acceptance criteria\n\n1. A refund above the total is refused.\n")
	moved := criterionKeys("# Title\n\nMore text.\n\n## Acceptance criteria\n\n7. A refund  above\n   the total is refused.\n")
	if len(base) != 1 || len(base[0]) != 64 {
		t.Fatalf("keys %v, want one SHA-256 in hex", base)
	}
	if base[0] != moved[0] {
		t.Errorf("a move to another line and number, and other spacing, changed the key: %s and %s", base[0], moved[0])
	}
	if edited := criterionKeys("## Acceptance criteria\n\n1. A refund above the total is allowed.\n"); edited[0] == base[0] {
		t.Error("an edited criterion kept its key")
	}
}

func TestCriterionKeySeesAChangePastTheCutOfTheText(t *testing.T) {
	long := strings.Repeat("The order is checked. ", 80)
	a := criterionKeys("## Acceptance criteria\n\n1. " + long + "It ends in 422.\n")
	b := criterionKeys("## Acceptance criteria\n\n1. " + long + "It ends in 409.\n")
	if a[0] == b[0] {
		t.Error("two criteria that differ only after the 1200th character share a key")
	}
}
