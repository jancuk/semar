package script_test

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/script"
)

func withBody(body any) domain.Script {
	return domain.Script{
		APIVersion: "semar/v1", Kind: "Script", Scenario: "A-1",
		ExpectHash: "8e44c6f1b96eebc0d6553cbdff28198a8fd28bbff9730ae2dcd094ac449ad121",
		AuthoredBy: domain.ProviderRef{Instance: "i", Model: "m", Harness: "h"},
		Steps: []domain.ScriptStep{{
			Step:    "s",
			Request: &domain.APIRequest{Service: "orders", Method: "POST", Path: "/v1/orders", JSON: body},
		}},
	}
}

func roundTrip(t *testing.T, in, want domain.Script) []byte {
	t.Helper()
	data, err := script.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := script.Parse(data, "m.yaml")
	if err != nil {
		t.Fatalf("Parse(Marshal(x)): %v\n%s", err, data)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse(Marshal(x)) = %+v, want %+v\n%s", got, want, data)
	}
	return data
}

func TestMarshalRoundTripsABody(t *testing.T) {
	tests := []struct {
		name string
		body any
	}{
		{"int", 100000},
		{"negative int", -3},
		{"2^53", 9007199254740992},
		{"largest uint64", uint64(18446744073709551615)},
		{"float", 0.1},
		{"whole float", 5.0},
		{"large float", 1e21},
		{"negative zero", math.Copysign(0, -1)},
		{"true", true},
		{"false", false},
		{"string of digits", "422"},
		{"string true", "true"},
		{"string null", "null"},
		{"string exponent", "1e3"},
		{"string octal", "010"},
		{"string tilde", "~"},
		{"string empty", ""},
		{"string with colon", "a: b"},
		{"string with hash", "a #b"},
		{"leading and trailing space", " a "},
		{"newline", "a\nb"},
		{"variable", "{{order_id}}"},
		{"variable inside text", "order {{order_id}} is open"},
		{"unicode", "dana é  "},
		{"object", map[string]any{"b": []any{1, 2.5, nil, "x"}, "a": map[string]any{"z": true}}},
		{"object with odd keys", map[string]any{"1": 1, "": 2, "true": 3, "a: b": 4, "json": "x"}},
		{"empty object", map[string]any{}},
		{"empty list", []any{}},
		{"list", []any{"a", map[string]any{"b": 1}}},
		{"nested list of lists", []any{[]any{1, 2}, []any{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := withBody(tt.body)
			roundTrip(t, in, in)
		})
	}
}

func TestMarshalWritesJSONNumbersAsTheirText(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want any
		text string
	}{
		{"int64", int64(5), 5, "json: 5\n"},
		{"integer", json.Number("100000"), 100000, "json: 100000\n"},
		{"negative integer", json.Number("-12"), -12, "json: -12\n"},
		{"negative zero integer", json.Number("-0"), 0, "json: 0\n"},
		{"uint64", json.Number("18446744073709551615"), uint64(18446744073709551615), "json: 18446744073709551615\n"},
		{"float", json.Number("0.1"), 0.1, "json: 0.1\n"},
		{"whole float", json.Number("100.0"), 100.0, "json: 100.0\n"},
		{"exponent", json.Number("1e3"), 1000.0, "json: 1e3\n"},
		{"capital exponent", json.Number("2.5E-3"), 0.0025, "json: 2.5E-3\n"},
		{"negative zero float", json.Number("-0.0"), math.Copysign(0, -1), "json: -0.0\n"},
		{"in an object", map[string]any{"amount": json.Number("150000"), "rate": json.Number("0.5")}, map[string]any{"amount": 150000, "rate": 0.5}, "        amount: 150000\n        rate: 0.5\n"},
		{"in a list", []any{json.Number("1"), json.Number("2.50")}, []any{1, 2.5}, "        - 1\n        - 2.50\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := roundTrip(t, withBody(tt.in), withBody(tt.want))
			if !strings.Contains(string(data), tt.text) {
				t.Errorf("output lacks %q\n%s", tt.text, data)
			}
		})
	}
}

func TestMarshalRefusesWhatParseCannotHold(t *testing.T) {
	tests := []struct {
		name string
		body any
		want []string
	}{
		{"NaN", math.NaN(), []string{"steps[0].request.json", "finite"}},
		{"infinity", math.Inf(1), []string{"request.json", "finite"}},
		{"NaN in an object", map[string]any{"a": []any{math.NaN()}}, []string{"request.json.a[0]", "finite"}},
		{"wide integer", json.Number("123456789012345678901234"), []string{"request.json", "too wide"}},
		{"float with no exact short text", json.Number("0.1000000000000000055511151231257827"), []string{"request.json", "exactly"}},
		{"float out of range", json.Number("1e400"), []string{"request.json", "range"}},
		{"not a number", json.Number("0x10"), []string{"request.json", "not a JSON number"}},
		{"invalid UTF-8", "a\xffb", []string{"request.json", "UTF-8"}},
		{"invalid UTF-8 key", map[string]any{"a\xff": 1}, []string{"request.json", "UTF-8"}},
		{"nil object", map[string]any(nil), []string{"request.json", "nil object"}},
		{"nil list", []any(nil), []string{"request.json", "nil list"}},
		{"unsupported type", map[string]string{"a": "b"}, []string{"request.json", "map[string]string"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := script.Marshal(withBody(tt.body))
			if err == nil {
				t.Fatal("Marshal accepted the body")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("err = %v, want it to contain %q", err, w)
				}
			}
		})
	}

	t.Run("invalid UTF-8 in a header value", func(t *testing.T) {
		s := withBody(nil)
		s.Steps[0].Request.Headers = map[string]string{"X-A": "a\xff"}
		if _, err := script.Marshal(s); err == nil || !strings.Contains(err.Error(), "headers.X-A") {
			t.Fatalf("err = %v, want one that names headers.X-A", err)
		}
	})
	t.Run("deeply nested", func(t *testing.T) {
		var v any = "x"
		for range 1000 {
			v = map[string]any{"a": v}
		}
		if _, err := script.Marshal(withBody(v)); err == nil {
			t.Fatal("Marshal accepted a body nested 1000 levels")
		}
	})
}

func TestMarshalSetsApiVersionAndKind(t *testing.T) {
	in := withBody(1)
	want := in
	in.APIVersion, in.Kind = "", ""
	roundTrip(t, in, want)

	for _, tt := range []struct {
		name string
		edit func(*domain.Script)
		want string
	}{
		{"other apiVersion", func(s *domain.Script) { s.APIVersion = "semar/v2" }, "apiVersion"},
		{"other kind", func(s *domain.Script) { s.Kind = "Scenario" }, "kind"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := withBody(1)
			tt.edit(&s)
			if _, err := script.Marshal(s); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want one that names %s", err, tt.want)
			}
		})
	}
}

func TestMarshalRoundTripsEveryRequestField(t *testing.T) {
	in := withBody(map[string]any{"a": 1})
	in.Steps = append(in.Steps,
		domain.ScriptStep{
			Step: "t",
			Request: &domain.APIRequest{
				Service: "orders", Method: "GET", Path: "/v1/orders/{{order_id}}",
				Query:   map[string]string{"z": "1", "a": "", "page size": "{{n}}", "yes": "no"},
				Headers: map[string]string{"X-Trace": "010", "Accept": "application/json", "Authorization": " Bearer x "},
			},
			Captures: []domain.Capture{{Name: "order_id", JSONPath: "$.data.id"}, {Name: "n", JSONPath: "$.n"}},
		},
		domain.ScriptStep{Step: "u"},
	)
	roundTrip(t, in, in)
}

func TestMarshalOmitsEmptyOptionalFields(t *testing.T) {
	in := withBody(nil)
	in.Steps[0].Request.Query = map[string]string{}
	in.Steps[0].Request.Headers = map[string]string{}
	in.Steps[0].Captures = []domain.Capture{}
	want := in
	want.Steps = []domain.ScriptStep{{Step: "s", Request: &domain.APIRequest{Service: "orders", Method: "POST", Path: "/v1/orders"}}}
	data := roundTrip(t, in, want)
	for _, absent := range []string{"query", "headers", "json", "captures"} {
		if strings.Contains(string(data), absent) {
			t.Errorf("output holds %q\n%s", absent, data)
		}
	}
}

func TestMarshalKeepsAnEmptyAndANilStepList(t *testing.T) {
	for name, steps := range map[string][]domain.ScriptStep{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			in := withBody(nil)
			in.Steps = steps
			roundTrip(t, in, in)
		})
	}
}

func TestMarshalWritesEveryMapInSortedOrder(t *testing.T) {
	in := withBody(map[string]any{"c": 1, "a": map[string]any{"z": 1, "y": 2}, "b": 2})
	in.Steps[0].Request.Headers = map[string]string{"B": "1", "A": "2", "C": "3"}
	first := roundTrip(t, in, in)
	for range 20 {
		again, err := script.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatalf("output differs between calls:\n%s\n%s", first, again)
		}
	}
	text := string(first)
	if strings.Index(text, "          y: 2") > strings.Index(text, "          z: 1") || strings.Index(text, "      a:") > strings.Index(text, "      b: 2") ||
		strings.Index(text, "        A:") > strings.Index(text, "        B:") {
		t.Errorf("keys are not sorted:\n%s", text)
	}
}

func TestMarshalRoundTripsTheExampleFiles(t *testing.T) {
	for _, path := range []string{
		"testdata/valid.yaml",
		"../../testdata/e2e/scripts/SHOP-482-01.yaml",
		"../replay/testdata/SHOP-482-01.script.yaml",
	} {
		t.Run(path, func(t *testing.T) {
			s, err := script.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			roundTrip(t, s, s)
		})
	}
}

func TestMarshalWritesReadableBlockYAML(t *testing.T) {
	s, err := script.Load("../../testdata/e2e/scripts/SHOP-482-01.yaml")
	if err != nil {
		t.Fatal(err)
	}
	data, err := script.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.HasPrefix(text, "apiVersion: semar/v1\nkind: Script\nscenario: SHOP-482-01\nexpectHash: \"8e44c6f1b96eebc0d6553cbdff28198a8fd28bbff9730ae2dcd094ac449ad121\"\nauthoredBy:\n  instance: hand\n  model: none\n  harness: none\nsteps:\n") {
		t.Errorf("the head of the file is not in struct order:\n%s", text)
	}
	for _, line := range []string{
		"  - step: create-order",
		"    request:",
		"      service: orders",
		"      method: POST",
		"      path: /v1/orders",
		"      json:",
		"        amount: 100000",
		"    captures:",
		"      - name: order_id",
		`        jsonpath: "$.data.id"`,
		`      path: "/v1/orders/{{order_id}}/refunds"`,
	} {
		if !strings.Contains(text, "\n"+line+"\n") {
			t.Errorf("output lacks the line %q\n%s", line, text)
		}
	}
	for _, bad := range []string{"{}", "[", "&", "*", "!!", "\t"} {
		if strings.Contains(text, bad) {
			t.Errorf("output holds %q\n%s", bad, text)
		}
	}
}

func FuzzMarshal(f *testing.F) {
	f.Add("hash", "svc", "/v1/{{id}}", "key", "value", "$.a", 0.1, int64(5), "1e3", uint8(0))
	f.Add("", "\x00", "/a b", "a: b", "#x", "$", 1e21, int64(-1), "100.0", uint8(1))
	f.Add("8e44", "s\nt", "", "", " x ", "$", math.Copysign(0, -1), int64(0), "0.1000000000000000055511151231257827", uint8(2))
	f.Add("x", "a\xff", "/", "~", "010", "$", math.NaN(), int64(1), "-0", uint8(3))
	f.Add("\n", " ", "/", "true", "\ta", "$", math.Inf(-1), int64(2), "123456789012345678901234", uint8(4))
	f.Fuzz(func(t *testing.T, hash, service, path, key, value, jsonpath string, fl float64, n int64, num string, shape uint8) {
		var body any
		exact := true
		switch shape % 7 {
		case 0:
			body = value
		case 1:
			body = fl
		case 2:
			body = n
		case 3:
			body = json.Number(num)
			exact = false
		case 4:
			body = map[string]any{key: []any{value, fl, nil, true, map[string]any{}}, value: int(n)}
		case 5:
			body = []any{map[string]any{key: map[string]any{value: fl}}, []any{}, value}
		default:
			body = nil
		}
		in := domain.Script{
			Scenario: "A-1", ExpectHash: domain.Hash(hash),
			AuthoredBy: domain.ProviderRef{Instance: service, Model: key, Harness: value},
			Steps: []domain.ScriptStep{{
				Step: "s",
				Request: &domain.APIRequest{
					Service: service, Method: "POST", Path: path,
					Query:   map[string]string{key: value},
					Headers: map[string]string{value: key},
					JSON:    body,
				},
				Captures: []domain.Capture{{Name: key, JSONPath: jsonpath}},
			}},
		}
		data, err := script.Marshal(in)
		if err != nil {
			refusable := !math.IsNaN(fl) && !math.IsInf(fl, 0) && shape%7 != 3
			for _, s := range []string{hash, service, path, key, value, jsonpath} {
				refusable = refusable && utf8.ValidString(s)
			}
			if refusable {
				t.Fatalf("Marshal refused a value that Parse can hold: %v", err)
			}
			return
		}
		got, err := script.Parse(data, "fuzz.yaml")
		if err != nil {
			t.Fatalf("Parse refused what Marshal wrote: %v\n%q", err, data)
		}
		if !exact {
			return
		}
		want := in
		want.APIVersion, want.Kind = "semar/v1", "Script"
		if i, ok := body.(int64); ok {
			r := *in.Steps[0].Request
			r.JSON = int(i)
			want.Steps = []domain.ScriptStep{{Step: "s", Request: &r, Captures: in.Steps[0].Captures}}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Parse(Marshal(x)) = %+v, want %+v\n%q", got, want, data)
		}
	})
}

func TestMarshalKeepsTheShapeHashAndTheStoredTime(t *testing.T) {
	s := withBody(nil)
	s.ShapeHash = "9a1f6b0c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c"
	s.StoredAt = time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	roundTrip(t, s, s)

	plain := roundTrip(t, withBody(nil), withBody(nil))
	if strings.Contains(string(plain), "shapeHash") || strings.Contains(string(plain), "storedAt") {
		t.Errorf("a script with neither field wrote one:\n%s", plain)
	}
}
