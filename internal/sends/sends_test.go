package sends_test

import (
	"slices"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/sends"
)

func TestLinesOfAnAPIStep(t *testing.T) {
	st := domain.ScriptStep{Step: "create", Request: &domain.APIRequest{
		Service: "shop", Method: "POST", Path: "/v1/orders/{{order.id}}/lines",
		Query:   map[string]string{"b": "2", "a": "{{datetime.plus7d}}"},
		Headers: map[string]string{"X-Mode": "test"},
		JSON:    map[string]any{"dueAt": "{{datetime.plus7d}}", "note": "a <b> & c"},
	}}
	want := []string{
		"POST /v1/orders/{{order.id}}/lines",
		"query a={{datetime.plus7d}} b=2",
		"headers X-Mode=test",
		`json {"dueAt":"{{datetime.plus7d}}","note":"a <b> & c"}`,
	}
	if got := sends.Lines(st); !slices.Equal(got, want) {
		t.Errorf("lines:\n%q\nwant:\n%q", got, want)
	}
}

func TestAnAPIStepWithoutAPayloadIsOneLine(t *testing.T) {
	st := domain.ScriptStep{Request: &domain.APIRequest{Method: "GET", Path: "/v1/orders/7"}}
	if got, want := sends.Lines(st), []string{"GET /v1/orders/7"}; !slices.Equal(got, want) {
		t.Errorf("lines %q, want %q", got, want)
	}
}

func TestLinesOfAWebStep(t *testing.T) {
	cases := []struct {
		name string
		web  domain.WebAction
		want []string
	}{
		{"open", domain.WebAction{Do: domain.WebOpen, Path: "/orders/{{order.id}}"}, []string{"open /orders/{{order.id}}"}},
		{"click", domain.WebAction{Do: domain.WebClick, Screen: "orders", Element: "refund"}, []string{"click refund on screen orders"}},
		{"fill", domain.WebAction{Do: domain.WebFill, Screen: "login", Element: "email", Value: "ada@example.com"}, []string{"fill email on screen login", `text "ada@example.com"`}},
		{"secret", domain.WebAction{Do: domain.WebFill, Screen: "login", Element: "password", Secret: "env:SHOP_PASSWORD"}, []string{"fill password on screen login", "text the value of $SHOP_PASSWORD"}},
		{"drag", domain.WebAction{Do: domain.WebDrag, Screen: "board", Element: "card", To: "done"}, []string{"drag card to done on screen board"}},
		{"press", domain.WebAction{Do: domain.WebPress, Screen: "login", Element: "email", Value: "Enter"}, []string{"press email on screen login", `text "Enter"`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sends.Lines(domain.ScriptStep{Web: &c.web}); !slices.Equal(got, c.want) {
				t.Errorf("lines %q, want %q", got, c.want)
			}
		})
	}
}

func TestAStepThatSendsNothingHasNoLines(t *testing.T) {
	if got := sends.Lines(domain.ScriptStep{}); len(got) != 0 {
		t.Errorf("lines %q", got)
	}
}

func TestDetailsOfARequestLeaveOutTheRoute(t *testing.T) {
	r := domain.APIRequest{Method: "POST", Path: "/v1/orders", JSON: map[string]any{"n": 1}}
	if got, want := sends.Details(r), []string{`json {"n":1}`}; !slices.Equal(got, want) {
		t.Errorf("details %q, want %q", got, want)
	}
}
