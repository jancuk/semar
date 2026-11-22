package redact_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/redact"
)

func TestMobileRecordHoldsNoSecret(t *testing.T) {
	const secret = "hunter2-very-secret"
	r, err := redact.New(config.Redact{}, func() []string { return []string{secret} }, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	loc := &domain.Locator{Desc: secret, ID: "id-" + secret, Class: "c" + secret, XPath: "//*[@text='" + secret + "']", Role: "textbox", Name: secret}
	a := domain.Action{
		Surface: domain.SurfaceAndroid, Op: "type",
		Err: "failed for " + secret,
		Mobile: &domain.MobileRecord{
			Op: "type", Link: "shop://login?token=" + secret, Package: "com.x." + secret, Device: "emulator-" + secret, Target: loc,
			Elements: map[string]domain.ElementState{"pw": {Found: true, Count: 1, Text: secret, Locator: loc, Attrs: map[string]string{"hint": "k=" + secret}}},
			Repair:   []domain.Candidate{{Element: "pw", Locator: *loc, Why: "same name " + secret}},
		},
	}
	out := r.Action(a)
	blob, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), secret) {
		t.Errorf("the redacted action holds the secret: %s", blob)
	}
	if !redact.IsMarker(out.Mobile.Elements["pw"].Text) {
		t.Errorf("text = %q, want a marker", out.Mobile.Elements["pw"].Text)
	}
	if strings.Contains(a.Mobile.Elements["pw"].Text, "redacted") || a.Mobile.Link != "shop://login?token="+secret {
		t.Errorf("the caller's action was changed")
	}
}
