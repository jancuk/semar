package redact_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/redact"
)

func TestWebRecordHoldsNoSecret(t *testing.T) {
	const secret = "hunter2-very-secret"
	r, err := redact.New(config.Redact{}, func() []string { return []string{secret} }, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	loc := &domain.Locator{Role: "textbox", Name: secret}
	a := domain.Action{
		Surface: domain.SurfaceWeb, Op: "fill",
		Err: "failed for " + secret,
		Web: &domain.WebRecord{
			Op: "fill", URL: "http://shop.test/cart?token=" + secret, Target: loc,
			Elements: map[string]domain.ElementState{"pw": {Found: true, Count: 1, Text: secret, Locator: loc, Attrs: map[string]string{"href": "/a?k=" + secret}}},
			Title:    "Cart " + secret,
			Repair:   []domain.Candidate{{Element: "pw", Locator: *loc, Why: "same name " + secret}},
			Blocked:  []string{secret + ".evil.test"},
			SignIn:   &domain.WebSignIn{Account: "ana", User: "ana-" + secret},
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
	if !redact.IsMarker(out.Web.Elements["pw"].Text) {
		t.Errorf("text = %q, want a marker", out.Web.Elements["pw"].Text)
	}
	if strings.Contains(a.Web.Elements["pw"].Text, "redacted") {
		t.Errorf("the caller's action was changed")
	}
}
