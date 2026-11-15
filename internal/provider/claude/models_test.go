package claude

import (
	"context"
	"slices"
	"testing"
)

func TestModelsAreTheAliasesOfTheFlag(t *testing.T) {
	got, err := (&Driver{}).Models(context.Background())
	if err != nil || len(got) != 4 || got[0].ID != "fable" || got[3].ID != "haiku" {
		t.Fatalf("models %+v, error %v", got, err)
	}
	for _, m := range got {
		if m.Label == "" {
			t.Errorf("%s has no label", m.ID)
		}
		if !slices.Equal(m.Reasoning, []string{"low", "medium", "high", "xhigh", "max"}) || m.DefaultReasoning != "" {
			t.Errorf("%s: levels %v, default %q", m.ID, m.Reasoning, m.DefaultReasoning)
		}
	}
}
