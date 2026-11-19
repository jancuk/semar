package check_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/domain"
)

func page(elements map[string]domain.ElementState) domain.Action {
	return domain.Action{Surface: domain.SurfaceWeb, Op: "click", Web: &domain.WebRecord{Op: "click", Elements: elements}}
}

func TestElementChecks(t *testing.T) {
	one := domain.ElementState{Found: true, Count: 1, Visible: true, Text: "Buy milk"}
	gone := domain.ElementState{}
	many := domain.ElementState{Count: 3, Visible: true}
	hidden := domain.ElementState{Count: 1}
	cases := []struct {
		name   string
		check  string
		state  domain.ElementState
		want   domain.CheckState
		detail string
	}{
		{"equals", `{element: card, equals: Buy milk}`, one, "true", ""},
		{"equals differs", `{element: card, equals: Buy eggs}`, one, "false", ""},
		{"notEquals", `{element: card, notEquals: Buy eggs}`, one, "true", ""},
		{"contains", `{element: card, contains: milk}`, one, "true", ""},
		{"exists", `{element: card, exists: true}`, one, "true", ""},
		{"exists on a hidden element", `{element: card, exists: true}`, hidden, "true", ""},
		{"absent", `{element: card, exists: false}`, gone, "true", ""},
		{"exists fails when absent", `{element: card, exists: true}`, gone, "false", ""},
		{"count", `{element: card, count: 3}`, many, "true", ""},
		{"count differs", `{element: card, count: 1}`, many, "false", ""},
		{"text of an element that is not found", `{element: card, equals: Buy milk}`, gone, "not_evaluated", check.NotFound},
		{"text of several elements", `{element: card, equals: Buy milk}`, many, "not_evaluated", "the locator matches 3 elements"},
		{"text of a hidden element", `{element: card, contains: milk}`, hidden, "not_evaluated", check.NotFound},
		{"redacted text", `{element: card, equals: x}`, domain.ElementState{Found: true, Count: 1, Text: "[redacted:ab12cd34]"}, "not_evaluated", "redacted"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := check.Eval(yamlCheck(t, c.check), page(map[string]domain.ElementState{"card": c.state}))
			if res.State != c.want {
				t.Fatalf("state = %s (%s), want %s", res.State, res.Detail, c.want)
			}
			if !strings.Contains(res.Detail, c.detail) {
				t.Errorf("detail = %q, want it to hold %q", res.Detail, c.detail)
			}
		})
	}
}

func TestElementCheckNeedsAWebAction(t *testing.T) {
	res := check.Eval(yamlCheck(t, `{element: card, exists: true}`), ok(200, `{}`))
	if res.State != domain.CheckNotEvaluated || !strings.Contains(res.Detail, "not a browser step") {
		t.Errorf("result = %+v", res)
	}
	res = check.Eval(yamlCheck(t, `{element: card, exists: true}`), page(nil))
	if res.State != domain.CheckNotEvaluated || !strings.Contains(res.Detail, "did not read element card") {
		t.Errorf("result = %+v", res)
	}
}

func TestElementCheckIsMalformedWithTwoSubjects(t *testing.T) {
	res := check.Eval(yamlCheck(t, `{element: card, jsonpath: "$.a", exists: true}`), page(nil))
	if res.State != domain.CheckNotEvaluated || !strings.Contains(res.Detail, "exactly one of status, jsonpath, header, element, or page") {
		t.Errorf("result = %+v", res)
	}
}

func TestDescribeElement(t *testing.T) {
	if got := check.Describe(yamlCheck(t, `{element: doing-card, equals: Buy milk}`)); got != "element doing-card equals Buy milk" {
		t.Errorf("Describe = %q", got)
	}
}
