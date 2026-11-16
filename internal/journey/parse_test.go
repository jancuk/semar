package journey

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func TestParseTheExample(t *testing.T) {
	j := example(t)
	if j.ID != "checkout" || j.Owner != "team-commerce" || j.Criticality != "critical" || len(j.Legs) != 3 {
		t.Fatalf("header = %q %q %q, %d legs", j.ID, j.Owner, j.Criticality, len(j.Legs))
	}
	place, pay, settled := j.Legs[0], j.Legs[1], j.Legs[2]
	if place.Surface != domain.SurfaceAPI || place.Service != "orders" || !reflect.DeepEqual(place.Outputs, []string{"id", "amount"}) {
		t.Errorf("place = %+v", place)
	}
	if got := place.Steps[0].Request.JSON.(map[string]any)["amount"]; got != 1000 {
		t.Errorf("amount = %#v", got)
	}
	if !reflect.DeepEqual(pay.Needs, []domain.LegID{"place"}) || pay.Steps[0].Hint.Path != "/v1/payments" {
		t.Errorf("pay = %+v", pay)
	}
	w := settled.Wait
	if w == nil || w.Until.JSONPath != "$.data.status" || w.Until.Equals != "paid" || w.Timeout.Seconds() != 30 || w.Every.Seconds() != 1 {
		t.Errorf("wait = %+v", w)
	}
	if want := []string{"{{place.amount}}", "{{pay.amount}}", "{{settled.amount}}"}; !reflect.DeepEqual(j.Invariants[0].Equal, want) {
		t.Errorf("invariant = %v", j.Invariants)
	}
	if ps := Validate(j, nil); len(ps) != 0 {
		t.Errorf("the example has problems: %v", ps)
	}
	if ps := validate(j, "checkout.yaml", orderPaid); len(ps) != 0 {
		t.Errorf("the example has problems with its file name: %v", ps)
	}
}

// TestParseRefusals is rule J1, and rule J9, which Parse gives its own message.
func TestParseRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
	}{
		{"unknown key", edited(t, "criticality: critical", "criticality: critical\nschedule: nightly"), "x.yaml:7: "},
		{"unknown key names the key", edited(t, "criticality: critical", "criticality: critical\nschedule: nightly"), "schedule"},
		{"unknown key in a leg", edited(t, "surface: api\n    service: orders", "surface: api\n    retries: 2\n    service: orders"), "x.yaml:10: "},
		{"unknown key in a step", edited(t, "action: Pay the order", "action: Pay the order\n        retry: 2"), "retry"},
		{"unknown key in the wait", edited(t, "timeout: 30s", "timeout: 30s, poll: 1"), "poll"},
		{"empty", "", "the file is empty"},
		{"second document", exampleText(t) + "---\n" + exampleText(t), "more than one YAML document"},
		{"a list", "- a\n- b\n", "x.yaml"},
		{"too large", "apiVersion: semar/v1\n#" + strings.Repeat("x", MaxFileSize) + "\n", "larger than 256 KiB"},
		{"leg given", edited(t, "    service: payments\n", "    service: payments\n    given: [{ fixture: order.paid, as: order }]\n"), "x.yaml:22: legs[1].given: put the fixtures of a journey under its given"},
		{"timeout that is a number", edited(t, "timeout: 30s", "timeout: 30"), "timeout must be a duration such as 30s"},
		{"number that is not exact", edited(t, "amount: 1000,", "amount: 12345678901234567890123,"), "cannot be held exactly"},
		{"operand that is not exact", edited(t, "status: 201", "status: 201, equals: 1e400"), "equals"},
		{"non string key in a body", edited(t, "amount: 1000,", "amount: 1000, 7: x,"), "a mapping key must be a string"},
		{"non string key in an equals", edited(t, "equals: paid }, source", "equals: { 1: x } }, source"), "a mapping key must be a string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.text), "x.yaml")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to hold %q", err, tc.want)
			}
		})
	}
}

func TestParseKeepsNumbersExact(t *testing.T) {
	j, err := Parse([]byte(edited(t, "amount: 1000,", "amount: 9007199254740993,")), "x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got := j.Legs[0].Steps[0].Request.JSON.(map[string]any)["amount"]; got != 9007199254740993 {
		t.Errorf("amount = %#v", got)
	}
}

func TestParseLimitIsTheLimit(t *testing.T) {
	pad := edited(t, "invariants:", strings.Repeat("# x\n", (MaxFileSize-len(exampleText(t)))/4-2)+"invariants:")
	if len(pad) > MaxFileSize {
		t.Fatalf("test padding is %d bytes", len(pad))
	}
	if _, err := Parse([]byte(pad), "x.yaml"); err != nil {
		t.Fatalf("a file under the limit: %v", err)
	}
}
