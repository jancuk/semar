package plan

import (
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/knowledge"
)

// planDay is the day of the plan in these tests: 2026-11-18 in UTC+7, whatever the zone of the machine.
func planDay(t *testing.T) time.Time {
	t.Helper()
	old := time.Local
	time.Local = time.FixedZone("UTC+7", 7*3600)
	t.Cleanup(func() { time.Local = old })
	return time.Date(2026, 11, 18, 7, 15, 2, 0, time.UTC)
}

func datedInput(t *testing.T) Input {
	in := input(t)
	in.Now = planDay(t)
	return in
}

func TestValidateRefusesATypedDateNearToday(t *testing.T) {
	refund := func(d *Draft) *StepDraft { return &d.Scenarios[0].Steps[1] }
	tests := []struct {
		name   string
		change func(d *Draft)
		path   string
		says   []string
	}{
		{"a query value", func(d *Draft) { refund(d).Query = map[string]string{"from": "2026-11-25"} },
			"scenarios[0].steps[1]", []string{"query from holds the date 2026-11-25", "near today", "{{date.plus7d}}", "10 years"}},
		{"a body string", func(d *Draft) { refund(d).JSON = map[string]any{"dueAt": "2026-11-25T10:00:00Z"} },
			"scenarios[0].steps[1]", []string{"json.dueAt holds the date 2026-11-25T10:00:00Z", "{{datetime.plus7d}}"}},
		{"a string deep in the body", func(d *Draft) {
			refund(d).JSON = map[string]any{"items": []any{map[string]any{"due": "2026-11-17"}}}
		}, "scenarios[0].steps[1]", []string{"json.items[0].due holds the date 2026-11-17", "{{date.minus1d}}"}},
		{"today", func(d *Draft) { refund(d).Query = map[string]string{"on": "2026-11-18"} },
			"scenarios[0].steps[1]", []string{"{{date.today}}"}},
		{"a local time with a space", func(d *Draft) { refund(d).Query = map[string]string{"at": "2026-11-19 10:00"} },
			"scenarios[0].steps[1]", []string{"{{local.plus1d}}"}},
		{"a local time with a T", func(d *Draft) { refund(d).Query = map[string]string{"at": "2026-11-19T10:00"} },
			"scenarios[0].steps[1]", []string{"{{local.plus1d}}"}},
		{"an expected value", func(d *Draft) {
			refund(d).Expect[1].Check = CheckDraft{JSONPath: "$.data.dueOn", Equals: "2026-11-25"}
		}, "scenarios[0].steps[1].expect[1].check", []string{"equals holds the date 2026-11-25", "{{date.plus7d}}"}},
		{"a with value of a given entry", func(d *Draft) {
			d.Scenarios[0].Given = []GivenDraft{{Fixture: "order.paid", As: "order", With: map[string]string{"dueOn": "2026-11-20"}}}
		}, "scenarios[0].given[0]", []string{"with dueOn holds the date 2026-11-20", "{{date.plus2d}}"}},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			d := draft(t)
			c.change(&d)
			_, _, ps := Validate(d, datedInput(t))
			for _, p := range ps {
				if p.Path == c.path && containsAll(p.Message, c.says) {
					return
				}
			}
			t.Fatalf("want a problem at %s that says %q, got %s", c.path, c.says, problemTexts(ps))
		})
	}
}

func containsAll(s string, parts []string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}

func TestValidateKeepsADateThatDoesNotExpire(t *testing.T) {
	step := func(d *Draft) *StepDraft { return &d.Scenarios[0].Steps[1] }
	tests := []struct {
		name   string
		change func(d *Draft)
	}{
		{"a date long ago", func(d *Draft) { step(d).Query = map[string]string{"from": "2000-01-01"} }},
		{"a moment far ahead", func(d *Draft) { step(d).JSON = map[string]any{"dueAt": "2099-01-01T09:00"} }},
		{"a date that is no day", func(d *Draft) { step(d).Query = map[string]string{"from": "2026-02-30"} }},
		{"digits after the day", func(d *Draft) { step(d).Query = map[string]string{"ref": "2026-11-251"} }},
		{"a date inside a word", func(d *Draft) { step(d).Query = map[string]string{"ref": "x2026-11-25"} }},
		{"a date after other text", func(d *Draft) { step(d).Query = map[string]string{"q": "due 2026-11-25"} }},
		{"a variable", func(d *Draft) { step(d).Query = map[string]string{"from": "{{date.plus7d}}"} }},
		{"a header", func(d *Draft) { step(d).Headers = map[string]string{"X-Day": "2026-11-25"} }},
		{"a number", func(d *Draft) { step(d).JSON = map[string]any{"amount": 20261125} }},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			d := draft(t)
			c.change(&d)
			if _, _, ps := Validate(d, datedInput(t)); len(ps) > 0 {
				t.Fatalf("refused: %s", problemTexts(ps))
			}
		})
	}
}

func TestDateIsNearWhenLessThan3650DaysAway(t *testing.T) {
	today := planDay(t)
	for days, near := range map[int]bool{3649: true, 3650: false, -3649: true, -3650: false} {
		d := draft(t)
		d.Scenarios[0].Steps[1].Query = map[string]string{"on": today.In(time.Local).AddDate(0, 0, days).Format("2006-01-02")}
		_, _, ps := Validate(d, datedInput(t))
		if (len(ps) > 0) != near {
			t.Errorf("%d days away: near = %v, problems %s", days, near, problemTexts(ps))
		}
	}
}

func TestDayOfThePlanIsADayOfTheMachineZone(t *testing.T) {
	planDay(t)
	in := input(t)
	in.Now = time.Date(2026, 11, 18, 20, 0, 0, 0, time.UTC) // 03.00 on the 19th at UTC+7
	d := draft(t)
	d.Scenarios[0].Steps[1].Query = map[string]string{"on": "2026-11-19"}
	_, _, ps := Validate(d, in)
	if !strings.Contains(problemTexts(ps), "{{date.today}}") {
		t.Fatalf("got %s", problemTexts(ps))
	}
}

func TestValidateKeepsADateThatTheTicketNames(t *testing.T) {
	in := datedInput(t)
	in.Tickets = []knowledge.Ticket{{Key: "SHOP-482", Path: "docs/tickets/SHOP-482.md", Text: "# SHOP-482\n\n1. An order due on 2026-11-25 is late.\n"}}
	d := draft(t)
	d.Scenarios[0].Steps[1].Query = map[string]string{"on": "2026-11-25"}
	if _, _, ps := Validate(d, in); len(ps) > 0 {
		t.Fatalf("a date of the ticket is refused: %s", problemTexts(ps))
	}
	d.Scenarios[0].Steps[1].Query = map[string]string{"on": "2026-11-26"}
	if _, _, ps := Validate(d, in); len(ps) == 0 {
		t.Fatal("a date that the ticket does not name must be refused")
	}
	d.Scenarios[0].Steps[1].Query = map[string]string{"on": "2026-11-25"}
	d.Scenarios[1].Steps[1].Query = map[string]string{"on": "2026-11-25"}
	if _, _, ps := Validate(d, in); !strings.Contains(problemTexts(ps), "scenarios[1].steps[1]") {
		t.Fatalf("the ticket file of another ticket does not excuse the date: %s", problemTexts(ps))
	}
}

func TestValidateWithoutAClockDoesNotLookAtDates(t *testing.T) {
	d := draft(t)
	d.Scenarios[0].Steps[1].Query = map[string]string{"from": "2026-11-25"}
	if _, _, ps := Validate(d, input(t)); len(ps) > 0 {
		t.Fatalf("refused with no clock: %s", problemTexts(ps))
	}
}
