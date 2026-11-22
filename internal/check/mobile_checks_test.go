package check_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/domain"
)

func device(elements map[string]domain.ElementState) domain.Action {
	return domain.Action{Surface: domain.SurfaceAndroid, Op: "tap", Mobile: &domain.MobileRecord{Op: "tap", Elements: elements}}
}

// The checks of an element read a device exactly as they read a page.
func TestElementChecksReadADevice(t *testing.T) {
	one := domain.ElementState{Found: true, Count: 1, Visible: true, Enabled: true, Text: "Hi Ana", Attrs: map[string]string{"content-desc": "Greeting"}}
	for name, c := range map[string]struct {
		check string
		state domain.ElementState
		want  domain.CheckState
		note  string
	}{
		"equals":                  {`{element: w, equals: "Hi Ana"}`, one, domain.CheckTrue, ""},
		"equals a different text": {`{element: w, equals: "Hi Bob"}`, one, domain.CheckFalse, ""},
		"exists":                  {`{element: w, exists: true}`, one, domain.CheckTrue, ""},
		"does not exist":          {`{element: w, exists: false}`, domain.ElementState{}, domain.CheckTrue, ""},
		"enabled":                 {`{element: w, state: enabled}`, one, domain.CheckTrue, ""},
		"attribute":               {`{element: w, attribute: content-desc, equals: Greeting}`, one, domain.CheckTrue, ""},
		"not found":               {`{element: w, equals: x}`, domain.ElementState{}, domain.CheckNotEvaluated, check.NotFound},
		"several":                 {`{element: w, equals: x}`, domain.ElementState{Count: 2, Visible: true}, domain.CheckNotEvaluated, "the locator matches 2 elements"},
		"a page":                  {`{page: title, equals: x}`, one, domain.CheckNotEvaluated, "no page"},
		"a path":                  {`{page: path, equals: /x}`, one, domain.CheckNotEvaluated, "no page"},
	} {
		res := check.Eval(yamlCheck(t, c.check), device(map[string]domain.ElementState{"w": c.state}))
		if res.State != c.want || !strings.Contains(res.Detail, c.note) {
			t.Errorf("%s: result = %+v, want %s with %q", name, res, c.want, c.note)
		}
	}
}

func TestAnElementThatMatchesSeveralOnADeviceIsNamed(t *testing.T) {
	a := device(map[string]domain.ElementState{"row": {Count: 2, Visible: true}})
	a.Mobile.Repair = []domain.Candidate{{Element: "row", Locator: domain.Locator{Desc: "Row 1"}}, {Element: "row", Locator: domain.Locator{ID: "r2"}}}
	res := check.Eval(yamlCheck(t, `{element: row, equals: x}`), a)
	if !strings.HasPrefix(res.Detail, "the locator matches 2 elements: { desc: \"Row 1\" }, { id: r2 }.") {
		t.Errorf("detail = %q", res.Detail)
	}
}

func TestACheckOfAnElementOnAnAPIActionIsNotEvaluated(t *testing.T) {
	res := check.Eval(yamlCheck(t, `{element: w, equals: x}`), domain.Action{Response: &domain.HTTPResponse{Status: 200}})
	if res.State != domain.CheckNotEvaluated {
		t.Errorf("result = %+v", res)
	}
}
