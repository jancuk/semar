package dashboard

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/usage"
)

// fakeLister is a provider that names models, or fails.
type fakeLister struct {
	fakeInstance
	models []provider.Model
	err    error
	wait   bool
}

func (f *fakeLister) Models(ctx context.Context) ([]provider.Model, error) {
	if f.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return f.models, f.err
}

func (fx *fixture) lister(id string, l func(cfg config.Provider) *fakeLister) {
	fx.instance(id, func(cfg config.Provider) (provider.Instance, error) { return l(cfg), nil })
}

func (fx *fixture) models(id string) modelsBody {
	fx.t.Helper()
	a := fx.call("GET", "/api/providers/"+id+"/models", "")
	want(fx.t, a, 200)
	var b modelsBody
	a.json(fx.t, &b)
	return b
}

func (fx *fixture) used(id, model string, ago time.Duration) {
	fx.t.Helper()
	at := fx.s.d.Now().Add(-ago)
	if err := usage.Append(fx.home, usage.Record{At: at, Provider: id, Model: model, Command: "plan", Outcome: "ok"}); err != nil {
		fx.t.Fatal(err)
	}
}

func TestModelsRefusals(t *testing.T) {
	fx := newFixture(t)
	fx.writeFile(twoProviders)
	_, port, _ := strings.Cut(fx.host(), ":")
	cases := []struct {
		name, method, path string
		edit               []func(*http.Request)
		code               int
	}{
		{"no token", "GET", "/api/providers/claude/models", []func(*http.Request){withoutHeader(TokenHeader)}, 403},
		{"wrong token", "GET", "/api/providers/claude/models", []func(*http.Request){withHeader(TokenHeader, "abc")}, 403},
		{"foreign host", "GET", "/api/providers/claude/models", []func(*http.Request){withHost("orders.example.com:" + port)}, 403},
		{"foreign origin", "GET", "/api/providers/claude/models", []func(*http.Request){withHeader("Origin", "https://orders.example.com")}, 403},
		{"post", "POST", "/api/providers/claude/models", nil, 405},
		{"head", "HEAD", "/api/providers/claude/models", nil, 405},
		{"unknown provider", "GET", "/api/providers/nothing/models", nil, 404},
		{"bad ID", "GET", "/api/providers/-x/models", nil, 400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := ""
			if c.method == "POST" {
				body = "{}"
			}
			want(t, fx.call(c.method, c.path, body, c.edit...), c.code)
		})
	}
}

func TestModelsFromTheLister(t *testing.T) {
	fx := newFixture(t)
	fx.writeFile(twoProviders)
	fx.lister("claude", func(cfg config.Provider) *fakeLister {
		return &fakeLister{models: []provider.Model{{ID: "acme-large", Label: "Acme Large"}, {ID: "acme-small"}}}
	})
	fx.used("claude", "acme-small", 2*time.Hour) // listed, so left out
	fx.used("claude", "acme-old", 48*time.Hour)  // older
	fx.used("claude", "acme-new", time.Hour)     // newest
	fx.used("claude", "acme-old", 72*time.Hour)  // a repeat
	fx.used("claude", "acme-ancient", 91*24*time.Hour)
	fx.used("deepseek", "acme-other", time.Hour) // another provider
	fx.used("claude", "", time.Hour)             // the default model
	b := fx.models("claude")
	if len(b.Models) != 2 || b.Models[0].ID != "acme-large" || b.Models[0].Label != "Acme Large" || b.Error != "" {
		t.Fatalf("body = %+v", b)
	}
	if got := strings.Join(b.Used, ","); got != "acme-new,acme-old" {
		t.Fatalf("used = %q", got)
	}
}

func TestModelsUsedIsCutAtTen(t *testing.T) {
	fx := newFixture(t)
	fx.writeFile(twoProviders)
	for i := 0; i < 15; i++ {
		fx.used("claude", "acme-"+string(rune('a'+i)), time.Duration(i+1)*time.Hour)
	}
	b := fx.models("claude")
	if len(b.Used) != 10 || b.Used[0] != "acme-a" || len(b.Models) != 0 {
		t.Fatalf("body = %+v", b)
	}
}

func TestModelsWithoutALister(t *testing.T) {
	fx := newFixture(t)
	fx.writeFile(twoProviders)
	a := fx.call("GET", "/api/providers/deepseek/models", "")
	want(t, a, 200)
	if !strings.Contains(a.body, `"models":[]`) || !strings.Contains(a.body, `"used":[]`) || strings.Contains(a.body, `"error"`) {
		t.Fatalf("body = %s", a.body)
	}
}

func TestModelsFailingLister(t *testing.T) {
	const key = "acme-test-key-0123456789abcdef"
	fx := newFixture(t)
	fx.writeFile(twoProviders)
	fx.used("deepseek", "acme-chat", time.Hour)
	fx.lister("deepseek", func(cfg config.Provider) *fakeLister {
		return &fakeLister{err: errors.New("the API key cannot be used: token=" + key + " for someone@example.com")}
	})
	b := fx.models("deepseek")
	if len(b.Models) != 0 || !strings.HasPrefix(b.Error, "The models could not be listed: ") || !strings.HasSuffix(b.Error, ".") {
		t.Fatalf("body = %+v", b)
	}
	if strings.Contains(b.Error, key) || strings.Contains(b.Error, "someone@") {
		t.Fatalf("the error holds a secret: %s", b.Error)
	}
	if len(b.Used) != 1 || b.Used[0] != "acme-chat" {
		t.Fatalf("used = %v", b.Used)
	}
}

func TestModelsListerThatHangs(t *testing.T) {
	fx := newFixture(t, func(s *Server) { s.listTimeout = 100 * time.Millisecond })
	fx.writeFile(twoProviders)
	fx.lister("claude", func(cfg config.Provider) *fakeLister { return &fakeLister{wait: true} })
	b := fx.models("claude")
	if len(b.Models) != 0 || !strings.HasPrefix(b.Error, "The models could not be listed: ") {
		t.Fatalf("body = %+v", b)
	}
}

func TestModelsInstanceThatCannotBeBuilt(t *testing.T) {
	fx := newFixture(t)
	fx.writeFile(twoProviders)
	fx.instance("claude", func(config.Provider) (provider.Instance, error) { return nil, errors.New("no binary") })
	b := fx.models("claude")
	if b.Error != "This provider cannot list its models: no binary." {
		t.Fatalf("error = %q", b.Error)
	}
}

func TestModelsCarryLevelsAndTheLastUse(t *testing.T) {
	fx := newFixture(t)
	fx.writeFile(twoProviders)
	fx.lister("claude", func(cfg config.Provider) *fakeLister {
		return &fakeLister{models: []provider.Model{{ID: "acme-large", Label: "Acme Large", Description: "For hard problems.", Reasoning: []string{"low", "medium", "high"}, DefaultReasoning: "medium"}}}
	})
	// The newest record is the last use, whatever its model and age, and it is not cut at 90 days.
	rec := func(model, level string, ago time.Duration) {
		if err := usage.Append(fx.home, usage.Record{At: fx.s.d.Now().Add(-ago), Provider: "claude", Model: model, Reasoning: level, Command: "plan", Outcome: "failed:usage_limit"}); err != nil {
			t.Fatal(err)
		}
	}
	rec("acme-old", "low", 5*time.Hour)
	rec("acme-large", "high", time.Hour)
	rec("acme-older", "max", 200*24*time.Hour)
	a := fx.call("GET", "/api/providers/claude/models", "")
	want(t, a, 200)
	for _, w := range []string{
		`"models":[{"id":"acme-large","label":"Acme Large","description":"For hard problems.","reasoning":["low","medium","high"],"defaultReasoning":"medium"}]`,
		`"used":["acme-old"]`,
		`"lastUsed":{"model":"acme-large","reasoning":"high","at":"2026-11-14T`,
	} {
		if !strings.Contains(a.body, w) {
			t.Errorf("body lacks %s:\n%s", w, a.body)
		}
	}

	none := fx.call("GET", "/api/providers/deepseek/models", "")
	want(t, none, 200)
	if strings.Contains(none.body, "lastUsed") {
		t.Fatalf("a provider that never ran has a last use: %s", none.body)
	}
}
