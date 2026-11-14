package scenario_test

import (
	"math"
	"math/big"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/scenario"
)

// withCheck returns a scenario whose one check, on line 9, is {<check>}.
func withCheck(check string) []byte {
	return []byte("apiVersion: semar/v1\nkind: Scenario\nid: A-1\nticket: T-1\nsurface: api\nsteps:\n  - id: s\n    expect:\n" +
		"      - check: {" + check + "}\n        source: diff\n")
}

func TestParseRefusesAnOperandThatIsNotExact(t *testing.T) {
	tests := []struct {
		name  string
		data  []byte
		line  string
		key   string
		extra string
	}{
		{"wide float", withCheck("jsonpath: $.a, notEquals: 12345678901234567891.0"), ":9:", "notEquals", "fewer digits"},
		{"wide integer", withCheck("jsonpath: $.a, notEquals: 123456789012345678901234"), ":9:", "notEquals", "fewer digits"},
		{"long fraction", withCheck("jsonpath: $.a, equals: 0.1000000000000000055511151231257827"), ":9:", "equals", "fewer digits"},
		{"integer that a float bound rounds", withCheck("jsonpath: $.a, greater: 9007199254740993"), ":9:", "greater", "fewer digits"},
		{"less bound", withCheck("jsonpath: $.a, less: 12345678901234567891"), ":9:", "less", "fewer digits"},
		{"contains", withCheck("jsonpath: $.a, contains: 123456789012345678901234"), ":9:", "contains", "fewer digits"},
		{"inside a list", withCheck("jsonpath: $.a, equals: [1, 9007199254740993.0]"), ":9:", "equals", "fewer digits"},
		{"inside a mapping", withCheck("jsonpath: $.a, equals: {k: {m: 123456789012345678901234}}"), ":9:", "equals", "fewer digits"},
		{"leading zero read as octal", withCheck("jsonpath: $.a, equals: 010"), ":9:", "equals", "fewer digits"},
		{"status read as octal", withCheck("status: 0310"), ":9:", "status", "fewer digits"},
		{"number that YAML reads as a string", withCheck("jsonpath: $.a, notEquals: 1e400"), ":9:", "notEquals", "string"},
		{"infinity", withCheck("jsonpath: $.a, equals: .inf"), ":9:", "equals", "finite"},
		{"negative infinity bound", withCheck("jsonpath: $.a, less: -.inf"), ":9:", "less", "finite"},
		{"not a number", withCheck("jsonpath: $.a, notEquals: .nan"), ":9:", "notEquals", "finite"},
		{"tagged float", withCheck("jsonpath: $.a, equals: !!float 9007199254740993"), ":9:", "equals", "fewer digits"},
		{
			"through an alias",
			[]byte("apiVersion: semar/v1\nkind: Scenario\nid: A-1\ntitle: &n 12345678901234567891.0\nticket: T-1\nsurface: api\nsteps:\n  - id: s\n    expect:\n" +
				"      - check: {jsonpath: $.a, notEquals: *n}\n        source: diff\n"),
			":4:", "notEquals", "fewer digits",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := scenario.Parse(tt.data, "s.yaml")
			if err == nil {
				t.Fatal("Parse accepted the operand")
			}
			msg := err.Error()
			for _, want := range []string{"s.yaml" + tt.line, tt.key, tt.extra} {
				if !strings.Contains(msg, want) {
					t.Errorf("err = %v, want it to contain %q", err, want)
				}
			}
		})
	}
}

func TestParseKeepsAnExactOperand(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	i := func(v int) *int { return &v }
	tests := []struct {
		name  string
		check string
		want  domain.Check
	}{
		{"2^53", "jsonpath: $.a, equals: 9007199254740992", domain.Check{JSONPath: "$.a", Equals: 9007199254740992}},
		{"largest uint64", "jsonpath: $.a, equals: 18446744073709551615", domain.Check{JSONPath: "$.a", Equals: uint64(18446744073709551615)}},
		{"half", "jsonpath: $.a, equals: 0.5", domain.Check{JSONPath: "$.a", Equals: 0.5}},
		{"tenth", "jsonpath: $.a, equals: 0.1", domain.Check{JSONPath: "$.a", Equals: 0.1}},
		{"small exponent", "jsonpath: $.a, equals: 2.5E-3", domain.Check{JSONPath: "$.a", Equals: 0.0025}},
		{"exponent", "jsonpath: $.a, equals: 1e3", domain.Check{JSONPath: "$.a", Equals: 1000.0}},
		{"negative zero", "jsonpath: $.a, equals: -0", domain.Check{JSONPath: "$.a", Equals: math.Copysign(0, -1)}},
		{"integer", "jsonpath: $.a, notEquals: 100000", domain.Check{JSONPath: "$.a", NotEquals: 100000}},
		{"quoted digits", `jsonpath: $.a, equals: "12345678901234567891"`, domain.Check{JSONPath: "$.a", Equals: "12345678901234567891"}},
		{"number in a list", "jsonpath: $.a, contains: [1, 2.5, x]", domain.Check{JSONPath: "$.a", Contains: []any{1, 2.5, "x"}}},
		{"underscores", "jsonpath: $.a, equals: 1_000", domain.Check{JSONPath: "$.a", Equals: 1000}},
		{"hex", "jsonpath: $.a, equals: 0x1F", domain.Check{JSONPath: "$.a", Equals: 31}},
		{"octal with a prefix", "jsonpath: $.a, equals: 0o17", domain.Check{JSONPath: "$.a", Equals: 15}},
		{"bound of 2^53", "jsonpath: $.a, greater: 9007199254740992", domain.Check{JSONPath: "$.a", Greater: f(9007199254740992)}},
		{"bound of a half", "jsonpath: $.a, less: 0.5", domain.Check{JSONPath: "$.a", Less: f(0.5)}},
		{"bound of a tenth", "jsonpath: $.a, greater: 0.1", domain.Check{JSONPath: "$.a", Greater: f(0.1)}},
		{"status", "status: 422", domain.Check{Status: i(422)}},
		{"count", "jsonpath: $.a, count: 3", domain.Check{JSONPath: "$.a", Count: i(3)}},
		{"word", "jsonpath: $.a, equals: REFUND_EXCEEDS_TOTAL", domain.Check{JSONPath: "$.a", Equals: "REFUND_EXCEEDS_TOTAL"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := scenario.Parse(withCheck(tt.check), "s.yaml")
			if err != nil {
				t.Fatal(err)
			}
			got := s.Steps[0].Expect[0].Check
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("check = %#v, want %#v", got, tt.want)
			}
			if tt.name == "negative zero" && !math.Signbit(got.Equals.(float64)) {
				t.Errorf("equals = %v, want negative zero", got.Equals)
			}
		})
	}
}

// decimalText is the plain decimal form that the fuzz oracle reads with math/big alone.
var decimalText = regexp.MustCompile(`^-?[0-9]{1,40}(\.[0-9]{1,40})?([eE][-+]?[0-9]{1,3})?$`)

// A number operand is either refused or held exactly as its text says. A float64 is read the
// way internal/check reads it: as the shortest decimal that gives the same float64.
func FuzzExactOperand(f *testing.F) {
	for _, s := range []string{
		"0", "-0", "1", "0.5", "1e3", "100000", "010", "9007199254740992", "9007199254740993",
		"18446744073709551615", "18446744073709551616", "12345678901234567891.0",
		"0.1000000000000000055511151231257827", "1e400", "1e-400", "4.9e-324", "2.5E-3",
	} {
		f.Add(s, false)
		f.Add(s, true)
	}
	f.Fuzz(func(t *testing.T, text string, bound bool) {
		if !decimalText.MatchString(text) {
			return
		}
		want, ok := new(big.Rat).SetString(text)
		if !ok {
			t.Fatalf("the oracle cannot read %q", text)
		}
		key := "equals"
		if bound {
			key = "greater"
		}
		s, err := scenario.Parse(withCheck("jsonpath: $.a, "+key+": "+text), "fuzz.yaml")
		if err != nil {
			return
		}
		c := s.Steps[0].Expect[0].Check
		var got any = c.Equals
		if bound {
			got = *c.Greater
		}
		r, ok := ratOf(got)
		if !ok {
			t.Fatalf("%s: %q was kept as %#v, which is not a number", key, text, got)
		}
		if r.Cmp(want) != 0 {
			t.Fatalf("%s: %q was kept as %#v, which is %s", key, text, got, r.RatString())
		}
	})
}

func ratOf(v any) (*big.Rat, bool) {
	switch x := v.(type) {
	case int:
		return new(big.Rat).SetInt64(int64(x)), true
	case int64:
		return new(big.Rat).SetInt64(x), true
	case uint64:
		return new(big.Rat).SetInt(new(big.Int).SetUint64(x)), true
	case float64:
		if math.IsInf(x, 0) || math.IsNaN(x) {
			return nil, false
		}
		r, ok := new(big.Rat).SetString(strconv.FormatFloat(x, 'g', -1, 64))
		return r, ok
	default:
		return nil, false
	}
}
