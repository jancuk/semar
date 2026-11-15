package claude

import (
	"context"
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
	}
}
