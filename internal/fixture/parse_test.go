package fixture

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func TestParseTheExample(t *testing.T) {
	f := example(t)
	if f.ID != "order.paid" || f.Version != "1.0.0" || f.Owner != "team-orders" || f.Lease != "" {
		t.Errorf("header = %q %q %q %q", f.ID, f.Version, f.Owner, f.Lease)
	}
	if d := f.Params["customer"].Default; d == nil || *d != "{{data.customers.default}}" {
		t.Errorf("customer default = %v", d)
	}
	if len(f.Find) != 1 || len(f.Create) != 3 || len(f.Cleanup) != 1 {
		t.Fatalf("steps = %d, %d, %d", len(f.Find), len(f.Create), len(f.Cleanup))
	}
	if got := f.Create[0].Request.JSON.(map[string]any)["amount"]; got != 1000 {
		t.Errorf("amount = %#v", got)
	}
	u := f.Create[2].Until
	if u == nil || u.JSONPath != "$.data.status" || u.Equals != "paid" || u.Timeout.Seconds() != 30 || u.Every.Seconds() != 1 {
		t.Errorf("until = %+v", u)
	}
	if want := []string{"id"}; !reflect.DeepEqual(f.Outputs, want) {
		t.Errorf("outputs = %v", f.Outputs)
	}
	if ps := Validate(f, []string{"orders", "payments"}); len(ps) != 0 {
		t.Errorf("the example has problems: %v", ps)
	}
	if domain.FixtureHash(f) == "" {
		t.Error("the example cannot be hashed")
	}
}

func TestParseRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
	}{
		{"unknown key", edited(t, "version: 1.0.0", "version: 1.0.0\nretries: 3"), "retries"},
		{"unknown key in a step", edited(t, "captures: [{ name: id, jsonpath: \"$.data[0].id\" }]", "captures: [{ name: id, jsonpath: \"$.data[0].id\" }]\n    retry: 2"), "retry"},
		{"empty", "", "the file is empty"},
		{"second document", exampleText(t) + "---\n" + exampleText(t), "more than one YAML document"},
		{"a list", "- a\n- b\n", ".yaml"},
		{"too large", "apiVersion: semar/v1\n#" + strings.Repeat("x", MaxFileSize) + "\n", "larger than 256 KiB"},
		{"given", edited(t, "outputs: [id]", "outputs: [id]\ngiven: [{ fixture: customer.verified, as: customer }]"), "a fixture cannot use another fixture in this version"},
		{"timeout that is a number", edited(t, "timeout: 30s", "timeout: 30"), "timeout must be a duration such as 30s"},
		{"every that is a number", edited(t, "every: 1s", "every: 1"), "every must be a duration such as 30s"},
		{"number that is not exact", edited(t, "amount: 1000", "amount: 12345678901234567890123"), "cannot be held exactly"},
		{"non string key in a body", edited(t, "amount: 1000", "amount: 1000, 7: x"), "a mapping key must be a string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.text), "x.yaml")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to hold %q", err, tc.want)
			}
			if !strings.HasPrefix(err.Error(), "x.yaml:") {
				t.Errorf("err = %q does not start with the file name", err)
			}
		})
	}
}

func TestParseGivenNamesTheLine(t *testing.T) {
	_, err := Parse([]byte(edited(t, "outputs: [id]", "outputs: [id]\ngiven: []")), "x.yaml")
	if err == nil || !strings.HasPrefix(err.Error(), "x.yaml:20: ") {
		t.Fatalf("err = %v", err)
	}
}

// A request body and an until value keep the number the file says, as in a script and in a check.
func TestParseNumbersAreHeldExactly(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"exponent", "1e3"},
		{"two mebibytes", "2097152"},
		{"above two mebibytes", "2097153"},
		{"float form of two mebibytes", "2097152.0"},
		{"exponent form of two mebibytes", "2.097152e6"},
		{"negative", "-2097152"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := Parse([]byte(edited(t, "amount: 1000", "amount: "+tc.value)), "x.yaml")
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := f.Create[0].Request.JSON.(map[string]any)["amount"]; !ok {
				t.Fatal("no amount")
			}
			if domain.FixtureHash(f) == "" {
				t.Error("no hash")
			}
		})
	}
	a, _ := Parse([]byte(edited(t, "amount: 1000", "amount: 2097152")), "x.yaml")
	b, _ := Parse([]byte(edited(t, "amount: 1000", "amount: 2097153")), "x.yaml")
	if domain.FixtureHash(a) == domain.FixtureHash(b) {
		t.Error("two different sizes have one hash")
	}
}

func TestParseKeepsATimeoutOfABody(t *testing.T) {
	f, err := Parse([]byte(edited(t, "amount: 1000", "amount: 1000, timeout: 30")), "x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if f.Create[0].Request.JSON.(map[string]any)["timeout"] != 30 {
		t.Errorf("body = %v", f.Create[0].Request.JSON)
	}
}

func TestParseReadsTheAccountOfAStep(t *testing.T) {
	f, err := Parse([]byte(edited(t, "captures: [{ name: id, jsonpath: \"$.data[0].id\" }]", "captures: [{ name: id, jsonpath: \"$.data[0].id\" }]\n    as: carol")), "x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if f.Find[0].As != "carol" || f.Create[0].As != "" {
		t.Errorf("as = %q, %q", f.Find[0].As, f.Create[0].As)
	}
}
