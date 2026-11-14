package redact_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/redact"
)

func FuzzNoSecretSurvives(f *testing.F) {
	f.Add("prefix", "suffix", "hunter2-token")
	f.Add(`{"k":"`, `"}`, "hunter2-token")
	f.Add(`"`, `"`, `x\nxxxx`)
	f.Add("a=", "&b=1", "hunter2-token")
	f.Fuzz(func(t *testing.T, a, b, secretValue string) {
		if len(secretValue) < 6 || strings.ContainsAny(secretValue, "\x00") {
			t.Skip()
		}
		// Every marker contains this text, so a secret inside it cannot be removed.
		if strings.Contains("[redacted:0123456789abcdef]", secretValue) {
			t.Skip()
		}
		r, err := redact.New(config.Redact{}, func() []string { return []string{secretValue} })
		if err != nil {
			t.Fatal(err)
		}
		in := []byte(a + secretValue + b)
		out := r.Action(domain.Action{Response: &domain.HTTPResponse{Body: in}})
		if bytes.Contains(out.Response.Body, []byte(secretValue)) {
			t.Fatalf("secret survived in %q", out.Response.Body)
		}
		if json.Valid(in) && !json.Valid(out.Response.Body) {
			t.Fatalf("valid JSON %q became invalid: %q", in, out.Response.Body)
		}
	})
}
