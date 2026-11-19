package check_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/domain"
)

func TestElementStateAttributeAndPageChecks(t *testing.T) {
	one := func(mod func(*domain.ElementState)) domain.ElementState {
		st := domain.ElementState{Found: true, Count: 1, Visible: true, Enabled: true}
		mod(&st)
		return st
	}
	cases := []struct {
		name   string
		check  string
		state  domain.ElementState
		want   domain.CheckState
		detail string
	}{
		{"enabled", `{element: b, state: enabled}`, one(func(*domain.ElementState) {}), "true", ""},
		{"disabled", `{element: b, state: disabled}`, one(func(s *domain.ElementState) { s.Enabled = false }), "true", ""},
		{"checked", `{element: b, state: checked}`, one(func(s *domain.ElementState) { s.Checked = true }), "true", ""},
		{"unchecked is false for a checked box", `{element: b, state: unchecked}`, one(func(s *domain.ElementState) { s.Checked = true }), "false", ""},
		{"hidden when not shown", `{element: b, state: hidden}`, domain.ElementState{Count: 1}, "true", ""},
		{"hidden when absent", `{element: b, state: hidden}`, domain.ElementState{}, "true", ""},
		{"visible when absent is false", `{element: b, state: visible}`, domain.ElementState{}, "false", ""},
		{"enabled when absent is not evaluated", `{element: b, state: enabled}`, domain.ElementState{}, "not_evaluated", check.NotFound},
		{"enabled of several is not evaluated", `{element: b, state: enabled}`, domain.ElementState{Count: 2, Visible: true}, "not_evaluated", "the locator matches 2 elements"},
		{"hidden of several that are hidden", `{element: b, state: hidden}`, domain.ElementState{Count: 2}, "true", ""},
		{"enabled of one that is not shown", `{element: b, state: enabled}`, domain.ElementState{Count: 1, Enabled: true}, "true", ""},
		{"attribute equals", `{element: b, attribute: href, equals: /a}`, one(func(s *domain.ElementState) { s.Attrs = map[string]string{"href": "/a"} }), "true", ""},
		{"attribute contains", `{element: b, attribute: href, contains: "a"}`, one(func(s *domain.ElementState) { s.Attrs = map[string]string{"href": "/a"} }), "true", ""},
		{"attribute that is not there", `{element: b, attribute: href, equals: /a}`, one(func(*domain.ElementState) {}), "false", ""},
		{"attribute of several is not evaluated", `{element: b, attribute: href, equals: /a}`, domain.ElementState{Count: 4}, "not_evaluated", "the locator matches 4 elements"},
		{"redacted attribute", `{element: b, attribute: href, equals: x}`, one(func(s *domain.ElementState) { s.Attrs = map[string]string{"href": "[redacted:ab12cd34]"} }), "not_evaluated", "redacted"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := check.Eval(yamlCheck(t, c.check), page(map[string]domain.ElementState{"b": c.state}))
			if res.State != c.want || !strings.Contains(res.Detail, c.detail) {
				t.Fatalf("result = %+v, want %s with %q", res, c.want, c.detail)
			}
		})
	}
}

func TestAnElementThatMatchesSeveralIsNamed(t *testing.T) {
	a := page(map[string]domain.ElementState{"card": {Found: true, Count: 3, Visible: true, Text: "x"}})
	a.Web.Repair = []domain.Candidate{
		{Element: "card", Locator: domain.Locator{TestID: "c1"}}, {Element: "card", Locator: domain.Locator{Role: "button", Name: "Save"}},
		{Element: "other", Locator: domain.Locator{Text: "no"}},
	}
	res := check.Eval(yamlCheck(t, `{element: card, equals: x}`), a)
	if res.State != domain.CheckNotEvaluated || !strings.HasPrefix(res.Detail, "the locator matches 3 elements: { testId: c1 }, { role: button, name: Save }.") {
		t.Errorf("result = %+v", res)
	}
	if strings.Contains(res.Detail, "no }") {
		t.Errorf("an element of another name is listed: %q", res.Detail)
	}
}

func TestPageChecks(t *testing.T) {
	a := domain.Action{Surface: domain.SurfaceWeb, Web: &domain.WebRecord{URL: "http://shop.example/orders/12?tab=1#top", Title: "Orders"}}
	for _, c := range []struct {
		check string
		want  domain.CheckState
	}{
		{`{page: path, equals: /orders/12}`, "true"},
		{`{page: path, contains: orders}`, "true"},
		{`{page: path, equals: "/orders/12?tab=1"}`, "false"},
		{`{page: path, notEquals: /orders}`, "true"},
		{`{page: title, equals: Orders}`, "true"},
		{`{page: title, contains: Invoices}`, "false"},
	} {
		if res := check.Eval(yamlCheck(t, c.check), a); res.State != c.want {
			t.Errorf("%s: %+v", c.check, res)
		}
	}
	root := domain.Action{Web: &domain.WebRecord{URL: "http://shop.example"}}
	if res := check.Eval(yamlCheck(t, `{page: path, equals: /}`), root); res.State != domain.CheckTrue {
		t.Errorf("the path of the root = %+v", res)
	}
	if res := check.Eval(yamlCheck(t, `{page: path, equals: /}`), domain.Action{Web: &domain.WebRecord{}}); res.State != domain.CheckNotEvaluated {
		t.Errorf("no address = %+v", res)
	}
	if res := check.Eval(yamlCheck(t, `{page: path, equals: /}`), ok(200, `{}`)); res.State != domain.CheckNotEvaluated {
		t.Errorf("an API step has no page: %+v", res)
	}
}

func TestDescribeNewChecks(t *testing.T) {
	for src, want := range map[string]string{
		`{element: b, state: disabled}`:             "element b is disabled",
		`{element: b, state: unchecked}`:            "element b is not checked",
		`{element: b, attribute: href, equals: /a}`: "element b attribute href equals /a",
		`{page: title, contains: Orders}`:           "page title contains Orders",
	} {
		if got := check.Describe(yamlCheck(t, src)); got != want {
			t.Errorf("Describe(%s) = %q, want %q", src, got, want)
		}
	}
}
