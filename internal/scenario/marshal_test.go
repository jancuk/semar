package scenario_test

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/scenario"
)

func ptr[T any](v T) *T { return &v }

func oneCheck(c domain.Check) domain.Scenario {
	return domain.Scenario{
		APIVersion: "semar/v1", Kind: "Scenario", ID: "A-1", Title: "t", Ticket: "T-1", Surface: domain.SurfaceAPI,
		Steps: []domain.Step{{
			ID: "s", Action: "do it",
			Expect: []domain.Expectation{{Check: c, Source: domain.SourceDiff}},
		}},
	}
}

func roundTrip(t *testing.T, in, want domain.Scenario) []byte {
	t.Helper()
	data, err := scenario.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := scenario.Parse(data, "m.yaml")
	if err != nil {
		t.Fatalf("Parse(Marshal(x)): %v\n%s", err, data)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse(Marshal(x)) = %#v, want %#v\n%s", got, want, data)
	}
	if domain.ExpectHash(got) != domain.ExpectHash(in) {
		t.Fatalf("the expectation hash changed\n%s", data)
	}
	return data
}

func TestMarshalRoundTripsEveryOperator(t *testing.T) {
	tests := []struct {
		name  string
		check domain.Check
	}{
		{"status", domain.Check{Status: ptr(422)}},
		{"status zero", domain.Check{Status: ptr(0)}},
		{"jsonpath equals", domain.Check{JSONPath: "$.a", Equals: "x"}},
		{"jsonpath notEquals", domain.Check{JSONPath: "$.a", NotEquals: "x"}},
		{"jsonpath contains", domain.Check{JSONPath: "$.a", Contains: "x"}},
		{"jsonpath exists true", domain.Check{JSONPath: "$.a", Exists: ptr(true)}},
		{"jsonpath exists false", domain.Check{JSONPath: "$.a", Exists: ptr(false)}},
		{"greater", domain.Check{JSONPath: "$.a", Greater: ptr(0.1)}},
		{"greater whole", domain.Check{JSONPath: "$.a", Greater: ptr(5.0)}},
		{"less", domain.Check{JSONPath: "$.a", Less: ptr(-2.5e-9)}},
		{"less large", domain.Check{JSONPath: "$.a", Less: ptr(1e300)}},
		{"count", domain.Check{JSONPath: "$.a", Count: ptr(3)}},
		{"count zero", domain.Check{JSONPath: "$.a", Count: ptr(0)}},
		{"equalsPath", domain.Check{JSONPath: "$.a", EqualsPath: "$.b"}},
		{"header equals", domain.Check{Header: "Content-Type", Equals: "application/json"}},
		{"header exists false", domain.Check{Header: "X-Debug", Exists: ptr(false)}},
		{"empty check", domain.Check{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := oneCheck(tt.check)
			roundTrip(t, in, in)
		})
	}
}

func TestMarshalRoundTripsAnOperand(t *testing.T) {
	tests := []struct {
		name    string
		operand any
	}{
		{"int", 422},
		{"negative int", -7},
		{"zero", 0},
		{"2^53", 9007199254740992},
		{"largest uint64", uint64(18446744073709551615)},
		{"float", 0.1},
		{"whole float", 100000.0},
		{"negative zero", math.Copysign(0, -1)},
		{"small float", 4.9e-324},
		{"large float", 1.7976931348623157e308},
		{"exponent float", 1e21},
		{"true", true},
		{"false", false},
		{"string of digits", "422"},
		{"string true", "true"},
		{"string null", "null"},
		{"string exponent", "1e3"},
		{"string octal", "010"},
		{"string tilde", "~"},
		{"string empty", ""},
		{"string no", "No"},
		{"string nan", ".nan"},
		{"string date", "2026-11-14"},
		{"string hex", "0x1F"},
		{"leading space", " x"},
		{"trailing space", "x "},
		{"colon space", "a: b"},
		{"hash", "a #b"},
		{"leading hash", "#b"},
		{"newline", "a\nb"},
		{"newline at end", "a\nb\n"},
		{"leading newline", "\na"},
		{"blank lines", "a\n\nb"},
		{"indented second line", "a\n  b"},
		{"tab", "a\tb"},
		{"carriage return", "a\r\nb"},
		{"variable", "{{order_id}}"},
		{"brackets", "[a, b]"},
		{"anchor", "&a"},
		{"alias", "*a"},
		{"tag", "!!str x"},
		{"dash", "- a"},
		{"quote", `say "hi"`},
		{"unicode", "pengembalian dana \u00e9 \u2028 \u0000"},
		{"nil operand in list", []any{nil, 1, "a"}},
		{"nested", map[string]any{"b": []any{1, 2.5, map[string]any{"z": "y", "a": nil}}, "a": true}},
		{"empty list", []any{}},
		{"empty object", map[string]any{}},
		{"key that looks like a number", map[string]any{"1": "a", "": "b", "true": "c", "a: b": "d"}},
	}
	for _, tt := range tests {
		for _, op := range []string{"equals", "notEquals", "contains"} {
			t.Run(tt.name+"/"+op, func(t *testing.T) {
				c := domain.Check{JSONPath: "$.a"}
				switch op {
				case "equals":
					c.Equals = tt.operand
				case "notEquals":
					c.NotEquals = tt.operand
				default:
					c.Contains = tt.operand
				}
				in := oneCheck(c)
				roundTrip(t, in, in)
			})
		}
	}
}

func TestMarshalReadsOtherIntegerTypesAsInt(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want any
	}{
		{"int64", int64(5), 5},
		{"small uint64", uint64(5), 5},
		{"json integer", json.Number("100000"), 100000},
		{"json negative", json.Number("-12"), -12},
		{"json uint64", json.Number("18446744073709551615"), uint64(18446744073709551615)},
		{"json float", json.Number("0.1"), 0.1},
		{"json float exponent", json.Number("1e+21"), 1e21},
		{"json in a list", []any{json.Number("1"), json.Number("2.5")}, []any{1, 2.5}},
		{"json in an object", map[string]any{"a": json.Number("7")}, map[string]any{"a": 7}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roundTrip(t, oneCheck(domain.Check{JSONPath: "$.a", Equals: tt.in}), oneCheck(domain.Check{JSONPath: "$.a", Equals: tt.want}))
		})
	}
}

func TestMarshalRefusesWhatParseCannotHold(t *testing.T) {
	tests := []struct {
		name  string
		check domain.Check
		want  []string
	}{
		{"NaN operand", domain.Check{JSONPath: "$.a", Equals: math.NaN()}, []string{"steps[0].expect[0].check.equals", "finite"}},
		{"infinite operand", domain.Check{JSONPath: "$.a", NotEquals: math.Inf(1)}, []string{"notEquals", "finite"}},
		{"NaN in a list", domain.Check{JSONPath: "$.a", Contains: []any{1, math.NaN()}}, []string{"contains[1]", "finite"}},
		{"infinite in an object", domain.Check{JSONPath: "$.a", Equals: map[string]any{"k": math.Inf(-1)}}, []string{"equals.k", "finite"}},
		{"NaN bound", domain.Check{JSONPath: "$.a", Greater: ptr(math.NaN())}, []string{"check.greater", "finite"}},
		{"infinite bound", domain.Check{JSONPath: "$.a", Less: ptr(math.Inf(1))}, []string{"check.less", "finite"}},
		{"wide json integer", domain.Check{JSONPath: "$.a", Equals: json.Number("123456789012345678901234")}, []string{"equals", "too wide"}},
		{"json float with no exact short text", domain.Check{JSONPath: "$.a", Equals: json.Number("0.1000000000000000055511151231257827")}, []string{"equals", "exactly"}},
		{"json float out of range", domain.Check{JSONPath: "$.a", Equals: json.Number("1e400")}, []string{"equals", "range"}},
		{"json text that is not a number", domain.Check{JSONPath: "$.a", Equals: json.Number("0x10")}, []string{"equals", "not a JSON number"}},
		{"json float stored under another text", domain.Check{JSONPath: "$.a", Equals: json.Number("100.0")}, []string{"equals", "write 100"}},
		{"json negative zero", domain.Check{JSONPath: "$.a", Equals: json.Number("-0")}, []string{"equals", "write 0"}},
		{"json exponent text", domain.Check{JSONPath: "$.a", Equals: json.Number("1e3")}, []string{"equals", "write 1000"}},
		{"invalid UTF-8 operand", domain.Check{JSONPath: "$.a", Equals: "a\xffb"}, []string{"equals", "UTF-8"}},
		{"invalid UTF-8 key", domain.Check{JSONPath: "$.a", Equals: map[string]any{"a\xff": 1}}, []string{"equals", "UTF-8"}},
		{"nil object", domain.Check{JSONPath: "$.a", Equals: map[string]any(nil)}, []string{"equals", "nil object"}},
		{"nil list", domain.Check{JSONPath: "$.a", Equals: []any(nil)}, []string{"equals", "nil list"}},
		{"unsupported type", domain.Check{JSONPath: "$.a", Equals: []string{"a"}}, []string{"equals", "[]string"}},
		{"float32", domain.Check{JSONPath: "$.a", Equals: float32(0.5)}, []string{"equals", "float32"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := scenario.Marshal(oneCheck(tt.check))
			if err == nil {
				t.Fatal("Marshal accepted the value")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("err = %v, want it to contain %q", err, w)
				}
			}
		})
	}

	t.Run("invalid UTF-8 title", func(t *testing.T) {
		in := oneCheck(domain.Check{Status: ptr(200)})
		in.Title = "a\xff"
		if _, err := scenario.Marshal(in); err == nil || !strings.Contains(err.Error(), "title") {
			t.Fatalf("err = %v, want one that names title", err)
		}
	})
	t.Run("deeply nested", func(t *testing.T) {
		var v any = "x"
		for range 1000 {
			v = []any{v}
		}
		if _, err := scenario.Marshal(oneCheck(domain.Check{JSONPath: "$.a", Equals: v})); err == nil {
			t.Fatal("Marshal accepted a value nested 1000 levels")
		}
	})
}

func TestMarshalSetsApiVersionAndKind(t *testing.T) {
	in := oneCheck(domain.Check{Status: ptr(200)})
	want := in
	in.APIVersion, in.Kind = "", ""
	roundTrip(t, in, want)

	for _, tt := range []struct {
		name string
		edit func(*domain.Scenario)
		want string
	}{
		{"other apiVersion", func(s *domain.Scenario) { s.APIVersion = "semar/v2" }, "apiVersion"},
		{"other kind", func(s *domain.Scenario) { s.Kind = "Script" }, "kind"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := oneCheck(domain.Check{Status: ptr(200)})
			tt.edit(&s)
			if _, err := scenario.Marshal(s); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want one that names %s", err, tt.want)
			}
		})
	}
}

func TestMarshalOmitsEmptyOptionalFields(t *testing.T) {
	in := oneCheck(domain.Check{JSONPath: "$.a", Exists: ptr(false)})
	in.Steps = append(in.Steps, domain.Step{ID: "t", Setup: true, Action: "x", Hint: &domain.RequestHint{Method: "GET", Path: "/x"}})
	data := roundTrip(t, in, in)
	text := string(data)
	for _, absent := range []string{"cite", "setup: false", "equals", "status", "count", "header", "greater", "equalsPath"} {
		if strings.Contains(text, absent) {
			t.Errorf("output holds %q\n%s", absent, text)
		}
	}
	for _, present := range []string{"exists: false", "setup: true"} {
		if !strings.Contains(text, present) {
			t.Errorf("output lacks %q\n%s", present, text)
		}
	}
}

func TestMarshalKeepsAnEmptyAndANilStepList(t *testing.T) {
	for name, steps := range map[string][]domain.Step{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			in := oneCheck(domain.Check{})
			in.Steps = steps
			roundTrip(t, in, in)
		})
	}
}

func TestMarshalWritesEveryMapInSortedOrder(t *testing.T) {
	in := oneCheck(domain.Check{JSONPath: "$.a", Equals: map[string]any{"b": 1, "c": 2, "a": map[string]any{"z": 1, "y": 2}}})
	first := roundTrip(t, in, in)
	for range 20 {
		again, err := scenario.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatalf("output differs between calls:\n%s\n%s", first, again)
		}
	}
	if !strings.Contains(string(first), "a:\n") || strings.Index(string(first), "          a:") > strings.Index(string(first), "          b:") {
		t.Errorf("keys are not sorted:\n%s", first)
	}
}

func TestMarshalRoundTripsTheExampleFiles(t *testing.T) {
	for _, path := range []string{
		"testdata/valid.yaml",
		"../../testdata/e2e/.semar/scenarios/SHOP-482/SHOP-482-01.yaml",
		"../replay/testdata/SHOP-482-01.scenario.yaml",
	} {
		t.Run(path, func(t *testing.T) {
			s, err := scenario.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			roundTrip(t, s, s)
		})
	}
}

func TestMarshalWritesReadableBlockYAML(t *testing.T) {
	s, err := scenario.Load("../../testdata/e2e/.semar/scenarios/SHOP-482/SHOP-482-01.yaml")
	if err != nil {
		t.Fatal(err)
	}
	data, err := scenario.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.HasPrefix(text, "apiVersion: semar/v1\nkind: Scenario\nid: SHOP-482-01\ntitle: Refund above the order total is rejected\nticket: SHOP-482\nintent: |\n") {
		t.Errorf("the head of the file is not in struct order:\n%s", text)
	}
	for _, line := range []string{
		"steps:",
		"  - id: create-order",
		"    setup: true",
		"    action: Create a paid order for 100000",
		"    hint:",
		"      method: POST",
		`      path: "/v1/orders/{id}/refunds"`,
		"    expect:",
		"      - check:",
		"          status: 422",
		"        source: ticket",
		`        cite: "SHOP-482: a refund above the order total must be rejected"`,
		`          jsonpath: "$.error.code"`,
		"          equals: REFUND_EXCEEDS_TOTAL",
		"          equals: 0",
	} {
		if !strings.Contains(text, "\n"+line+"\n") {
			t.Errorf("output lacks the line %q\n%s", line, text)
		}
	}
	for _, bad := range []string{"{ ", "{}", "[", "&", "*", "!!", "\t"} {
		if strings.Contains(text, bad) {
			t.Errorf("output holds %q\n%s", bad, text)
		}
	}
}

func FuzzMarshal(f *testing.F) {
	f.Add("title", "action", "cite", "422", "text/html", "$.a", 0.1, int64(5), "1e3", uint8(0))
	f.Add("a: b", "#x", "line\nbreak", "true", " x ", "$..b", 1e21, int64(-1), "100.0", uint8(1))
	f.Add("", "\x00", "\u2028", "010", "{{a}}", "$", math.Copysign(0, -1), int64(0), "0.1000000000000000055511151231257827", uint8(2))
	f.Add("a\xff", "x", "y", "~", "", "$.x", math.NaN(), int64(1), "-0", uint8(3))
	f.Add("\na", " a\n", "a\n\n", "- a", "\t", "$.x", math.Inf(1), int64(2), "123456789012345678901234", uint8(4))
	f.Fuzz(func(t *testing.T, title, action, cite, str, header, path string, fl float64, n int64, num string, shape uint8) {
		var operand any
		exact := true
		switch shape % 7 {
		case 0:
			operand = str
		case 1:
			operand = fl
		case 2:
			operand = n
		case 3:
			operand = json.Number(num)
			exact = false
		case 4:
			operand = []any{str, fl, nil, true, map[string]any{str: int(n), header: []any{}}}
		case 5:
			operand = map[string]any{str: map[string]any{header: fl}, "z": []any{str}}
		default:
			operand = nil
		}
		in := domain.Scenario{
			Title: title, Ticket: cite, Surface: domain.SurfaceAPI, ID: "A-1",
			Steps: []domain.Step{{
				ID: "s", Action: action,
				Expect: []domain.Expectation{
					{Check: domain.Check{JSONPath: path, Equals: operand}, Source: domain.SourceTicket, Cite: cite},
					{Check: domain.Check{Header: header, Contains: str}, Source: domain.SourceDiff},
					{Check: domain.Check{JSONPath: path, Greater: &fl}, Source: domain.SourceDiff},
				},
			}},
		}
		data, err := scenario.Marshal(in)
		if err != nil {
			refusable := !math.IsNaN(fl) && !math.IsInf(fl, 0) && shape%7 != 3
			for _, s := range []string{title, action, cite, str, header, path} {
				refusable = refusable && utf8.ValidString(s)
			}
			if refusable {
				t.Fatalf("Marshal refused a value that Parse can hold: %v", err)
			}
			return
		}
		got, err := scenario.Parse(data, "fuzz.yaml")
		if err != nil {
			t.Fatalf("Parse refused what Marshal wrote: %v\n%q", err, data)
		}
		if domain.ExpectHash(got) != domain.ExpectHash(in) {
			t.Fatalf("the expectation hash changed\n%q", data)
		}
		want := in
		want.APIVersion, want.Kind = "semar/v1", "Scenario"
		if exact {
			if n, ok := operand.(int64); ok {
				want.Steps = append([]domain.Step(nil), in.Steps...)
				want.Steps[0].Expect = append([]domain.Expectation(nil), in.Steps[0].Expect...)
				want.Steps[0].Expect[0].Check.Equals = int(n)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Parse(Marshal(x)) = %#v, want %#v\n%q", got, want, data)
			}
		}
	})
}

func TestMarshalRoundTripsGiven(t *testing.T) {
	tests := []struct {
		name  string
		given []domain.Given
		want  []domain.Given // what Parse gives back. Nil means the same as given
	}{
		{"one entry", []domain.Given{{Fixture: "order.paid", As: "order"}}, nil},
		{"with and lease", []domain.Given{{Fixture: "order.paid", As: "order", With: map[string]string{"customer": "c-1", "amount": "1000"}, Lease: domain.LeaseExclusive}}, nil},
		{"two entries", []domain.Given{{Fixture: "customer.verified", As: "customer"}, {Fixture: "order.paid", As: "order", With: map[string]string{"customer": "{{data.customers.default}}"}}}, nil},
		{"values that look like other types", []domain.Given{{Fixture: "f.x", As: "a", With: map[string]string{"status": "true", "count": "3", "equals": "1.50", "n": "null", "d": "2026-11-14", "e": "", "m": "a: b", "o": "010"}}}, nil},
		{"keys that look like other types", []domain.Given{{Fixture: "f.x", As: "a", With: map[string]string{"y": "1", "n": "2", "on": "3"}}}, nil},
		{"unicode and control text", []domain.Given{{Fixture: "f.x", As: "a", With: map[string]string{"c": "café   \x1b"}}}, nil},
		{"an empty with is none", []domain.Given{{Fixture: "f.x", As: "a", With: map[string]string{}}}, []domain.Given{{Fixture: "f.x", As: "a"}}},
		{"an empty list is none", []domain.Given{}, nil},
		{"no list", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := oneCheck(domain.Check{Status: ptr(200)})
			in.Given = tt.given
			want := in
			switch {
			case tt.want != nil:
				want.Given = tt.want
			case len(tt.given) == 0:
				want.Given = nil
			}
			data := roundTrip(t, in, want)
			if len(want.Given) == 0 && strings.Contains(string(data), "given") {
				t.Errorf("no given entries must write no given key\n%s", data)
			}
		})
	}
}

func TestMarshalWritesGivenBetweenAuthProfileAndSteps(t *testing.T) {
	in := oneCheck(domain.Check{Status: ptr(200)})
	in.AuthProfile = "tester"
	in.Given = []domain.Given{{Fixture: "order.paid", As: "order", With: map[string]string{"zeta": "1", "alpha": "2"}}}
	data := string(roundTrip(t, in, in))
	auth, given, steps := strings.Index(data, "authProfile:"), strings.Index(data, "given:"), strings.Index(data, "steps:")
	if auth < 0 || !(auth < given && given < steps) {
		t.Fatalf("given must follow authProfile and precede steps\n%s", data)
	}
	if strings.Index(data, "alpha:") > strings.Index(data, "zeta:") {
		t.Fatalf("with must be in key order\n%s", data)
	}
	for i := range 20 {
		again, err := scenario.Marshal(in)
		if err != nil || string(again) != data {
			t.Fatalf("call %d differs: %v\n%s", i, err, again)
		}
	}
}

func TestMarshalGivenKeepsTheHash(t *testing.T) {
	in := oneCheck(domain.Check{Status: ptr(200)})
	in.Given = []domain.Given{{Fixture: "order.paid", As: "order", With: map[string]string{"customer": "c-1"}, Lease: domain.LeaseExclusive}}
	data, err := scenario.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	back, err := scenario.Parse(data, "m.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if domain.ExpectHash(back) != domain.ExpectHash(in) {
		t.Fatal("the hash changed in the round trip")
	}
	in.Given = nil
	if domain.ExpectHash(back) == domain.ExpectHash(in) {
		t.Fatal("the hash must cover given")
	}
}

func TestMarshalRefusesGivenTextThatParseCannotHold(t *testing.T) {
	in := oneCheck(domain.Check{Status: ptr(200)})
	in.Given = []domain.Given{{Fixture: "f.x", As: "a", With: map[string]string{"c": "a\xffb"}}}
	if _, err := scenario.Marshal(in); err == nil || !strings.Contains(err.Error(), "given[0].with.c") || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("got %v", err)
	}
}

func TestMarshalRoundTripsAsAndBy(t *testing.T) {
	in := oneCheck(domain.Check{JSONPath: "$.board", Equals: "{{o.board}}"})
	in.Steps[0].As = "carol"
	in.Steps[0].Expect[0].By = "model"
	data := roundTrip(t, in, in)
	for _, want := range []string{"as: carol", "by: model"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("want %q in\n%s", want, data)
		}
	}
	plain := oneCheck(domain.Check{Status: ptr(200)})
	data = roundTrip(t, plain, plain)
	if strings.Contains(string(data), "as:") || strings.Contains(string(data), "by:") {
		t.Errorf("an unset as or by must write nothing\n%s", data)
	}
}
