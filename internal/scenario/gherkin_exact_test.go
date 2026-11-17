package scenario_test

import (
	"math"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/scenario"
)

// thenFile is a file whose one Then line, on line 7, is sentence.
func thenFile(sentence string) []byte {
	return []byte("Feature: F\n\n  @id:A-1 @ticket:T-1 @surface:api\n  Scenario: S\n    When do it\n    # step: s\n    Then " + sentence + "\n    # diff\n")
}

func TestParseRefusesAValueThatIsNotExact(t *testing.T) {
	tests := []struct {
		name     string
		sentence string
		want     []string
	}{
		{"wide float", `$.a does not equal 12345678901234567891.0`, []string{"12345678901234567891.0", "fewer digits"}},
		{"wide integer", `$.a does not equal 123456789012345678901234`, []string{"123456789012345678901234", "fewer digits"}},
		{"long fraction", `$.a equals 0.1000000000000000055511151231257827`, []string{"fewer digits"}},
		{"integer that a float bound rounds", `$.a is greater than 9007199254740993`, []string{"9007199254740993", "fewer digits"}},
		{"less bound", `$.a is less than 12345678901234567891`, []string{"fewer digits"}},
		{"contains", `$.a contains 123456789012345678901234`, []string{"fewer digits"}},
		{"inside a list", `$.a equals [1, 9007199254740993.0]`, []string{"fewer digits"}},
		{"inside an object", `$.a equals {"k": {"m": 123456789012345678901234}}`, []string{"fewer digits"}},
		{"too large for a float", `$.a does not equal 1e400`, []string{"outside the range"}},
		{"a bound too large", `$.a is greater than 1e400`, []string{"outside the range"}},
		{"a status too large", `the status is 99999999999999999999`, []string{"too large"}},
		{"a count too large", `$.a has 99999999999999999999 items`, []string{"too large"}},
		{"a bound that is not a number", `$.a is less than ten`, []string{`"ten" is not a number`}},
		{"not a JSON value", `$.a equals yes`, []string{`"yes" is not a JSON value`, "double quotes"}},
		{"single quotes", `$.a equals 'x'`, []string{"not a JSON value"}},
		{"two values", `$.a equals 1 2`, []string{"not one JSON value"}},
		{"null", `$.a equals null`, []string{"null cannot be checked", "exists"}},
		{"a path that is not a path", `$.a equals the value at b`, []string{`"b" is not a JSON path`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := scenario.Parse(thenFile(tt.sentence), "s.feature")
			if err == nil {
				t.Fatal("Parse accepted the value")
			}
			for _, want := range append([]string{"s.feature:7:"}, tt.want...) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v, want it to contain %q", err, want)
				}
			}
		})
	}
}

func TestParseKeepsAnExactValue(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	i := func(v int) *int { return &v }
	tests := []struct {
		name     string
		sentence string
		want     domain.Check
	}{
		{"2^53", "$.a equals 9007199254740992", domain.Check{JSONPath: "$.a", Equals: 9007199254740992}},
		{"largest uint64", "$.a equals 18446744073709551615", domain.Check{JSONPath: "$.a", Equals: uint64(18446744073709551615)}},
		{"half", "$.a equals 0.5", domain.Check{JSONPath: "$.a", Equals: 0.5}},
		{"tenth", "$.a equals 0.1", domain.Check{JSONPath: "$.a", Equals: 0.1}},
		{"small exponent", "$.a equals 2.5E-3", domain.Check{JSONPath: "$.a", Equals: 0.0025}},
		{"exponent", "$.a equals 1e3", domain.Check{JSONPath: "$.a", Equals: 1000.0}},
		{"whole float", "$.a equals 5.0", domain.Check{JSONPath: "$.a", Equals: 5.0}},
		{"negative zero", "$.a equals -0.0", domain.Check{JSONPath: "$.a", Equals: math.Copysign(0, -1)}},
		{"integer", "$.a does not equal 100000", domain.Check{JSONPath: "$.a", NotEquals: 100000}},
		{"quoted digits", `$.a equals "12345678901234567891"`, domain.Check{JSONPath: "$.a", Equals: "12345678901234567891"}},
		{"numbers in a list", `$.a contains [1, 2.5, "x"]`, domain.Check{JSONPath: "$.a", Contains: []any{1, 2.5, "x"}}},
		{"bound of 2^53", "$.a is greater than 9007199254740992", domain.Check{JSONPath: "$.a", Greater: f(9007199254740992)}},
		{"bound of a half", "$.a is less than 0.5", domain.Check{JSONPath: "$.a", Less: f(0.5)}},
		{"bound of a tenth", "$.a is greater than 0.1", domain.Check{JSONPath: "$.a", Greater: f(0.1)}},
		{"status", "the status is 422", domain.Check{Status: i(422)}},
		{"count", "$.a has 3 items", domain.Check{JSONPath: "$.a", Count: i(3)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := scenario.Parse(thenFile(tt.sentence), "s.feature")
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

// A number operand is either refused or held exactly as its text says.
func FuzzExactValue(f *testing.F) {
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
		sentence := "$.a equals " + text
		if bound {
			sentence = "$.a is greater than " + text
		}
		s, err := scenario.Parse(thenFile(sentence), "fuzz.feature")
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
			t.Fatalf("%q was kept as %#v, which is not a number", text, got)
		}
		if r.Cmp(want) != 0 {
			t.Fatalf("%q was kept as %#v, which is %s", text, got, r.RatString())
		}
	})
}
