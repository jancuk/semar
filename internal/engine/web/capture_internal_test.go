package web

import (
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func TestCapturePatternTakesAGroupOfAnElementText(t *testing.T) {
	states := map[string]domain.ElementState{"ref": {Found: true, Text: "Order #A1B2 placed"}}
	got, missing := capture(states, []domain.Capture{
		{Name: "ref", Element: "ref", Pattern: `#([A-Z0-9]+)`},
		{Name: "plain", Element: "ref"},
		{Name: "none", Element: "ref", Pattern: `Invoice (\d+)`},
	})
	if got["ref"] != "A1B2" || got["plain"] != "Order #A1B2 placed" || len(missing) != 1 || missing[0] != "none" {
		t.Errorf("got = %v, missing = %v", got, missing)
	}
}
