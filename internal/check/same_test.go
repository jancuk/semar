package check_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/check"
)

func TestSameDecimals(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		equal  bool
	}{
		{"one thousand in three spellings", []string{"1000", "1000.0", "1e3"}, true},
		{"upper case exponent", []string{"1000", "1E3", "10e2"}, true},
		{"a fraction and its exponent form", []string{"0.5", "5e-1", "0.50"}, true},
		{"negative zero equals zero", []string{"-0", "0", "0.0"}, true},
		{"negative values", []string{"-12.50", "-1.25e1"}, true},
		{"explicit plus in the exponent", []string{"1e+3", "1000"}, true},
		{"30 digits are exact", []string{"123456789012345678901234567890", "123456789012345678901234567890.0"}, true},
		{"integers above 2^53 that differ by one", []string{"9007199254740993", "9007199254740992"}, false},
		{"the same integers above 2^53", []string{"9007199254740993", "9007199254740993.0"}, true},
		{"the middle one differs", []string{"1000", "999", "1000"}, false},
		{"the last one differs", []string{"1000", "1000", "1000.01"}, false},
		{"a value that differs only in a far exponent digit", []string{"1e400", "1e401"}, false},
		{"a large exponent equals itself", []string{"1e400", "10e399"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			equal, ok := check.Same(tt.values)
			if !ok {
				t.Fatalf("Same(%q) ok = false, want true", tt.values)
			}
			if equal != tt.equal {
				t.Fatalf("Same(%q) equal = %v, want %v", tt.values, equal, tt.equal)
			}
		})
	}
}

// An exponent that no memory can hold must not be allocated, and cannot be called equal.
func TestSameHugeExponent(t *testing.T) {
	for _, v := range [][]string{
		{"1e999999999", "1e999999999"},
		{"1e999999999999999999999999", "1"},
		{"1e-999999999", "0"},
	} {
		if equal, ok := check.Same(v); ok || equal {
			t.Errorf("Same(%q) = %v, %v, want false, false", v, equal, ok)
		}
	}
}

func TestSameText(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		equal  bool
	}{
		{"equal strings", []string{"paid", "paid", "paid"}, true},
		{"equal strings that differ in case", []string{"paid", "PAID"}, false},
		{"a trailing space", []string{"paid", "paid "}, false},
		{"two empty strings", []string{"", ""}, true},
		{"an empty string and zero", []string{"", "0"}, false},
		{"a number and a word", []string{"1000", "one thousand"}, false},
		{"a number and text that starts like one", []string{"1000", "1000abc"}, false},
		{"a decimal and its text with a leading space", []string{"1000", " 1000"}, false},
		{"null is text", []string{"null", "null"}, true},
		{"null is not zero", []string{"null", "0"}, false},
		{"a fraction is text, not a decimal", []string{"1/2", "0.5"}, false},
		{"the same fraction is equal text", []string{"1/2", "1/2"}, true},
		{"hex is text, not a decimal", []string{"0x10", "16"}, false},
		{"NaN is text", []string{"NaN", "NaN"}, true},
		{"NaN is not Inf", []string{"NaN", "Inf"}, false},
		{"a leading zero is text", []string{"0100", "100"}, false},
		{"a plus sign is text", []string{"+5", "5"}, false},
		{"a digit group is text", []string{"1_000", "1000"}, false},
		{"a decimal and a different word", []string{"1e3", "1e3x"}, false},
		{"a unicode digit is text", []string{"١٠٠٠", "1000"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			equal, ok := check.Same(tt.values)
			if !ok {
				t.Fatalf("Same(%q) ok = false, want true", tt.values)
			}
			if equal != tt.equal {
				t.Fatalf("Same(%q) equal = %v, want %v", tt.values, equal, tt.equal)
			}
		})
	}
}

func TestSameMarker(t *testing.T) {
	const marker = "[redacted:0a1b2c3d]"
	tests := []struct {
		name   string
		values []string
	}{
		{"the marker alone", []string{marker, marker}},
		{"a marker against a number", []string{"1000", marker}},
		{"a marker inside text", []string{"token " + marker, "token " + marker}},
		{"a marker in the last value only", []string{"a", "b", "c", marker}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			equal, ok := check.Same(tt.values)
			if ok || equal {
				t.Fatalf("Same(%q) = %v, %v, want false, false", tt.values, equal, ok)
			}
		})
	}
}

func TestSameNeedsTwoValues(t *testing.T) {
	for _, v := range [][]string{nil, {}, {"1000"}} {
		if equal, ok := check.Same(v); ok || equal {
			t.Errorf("Same(%q) = %v, %v, want false, false", v, equal, ok)
		}
	}
}

func TestSameLongTextIsNotCut(t *testing.T) {
	a := strings.Repeat("x", 5000)
	if equal, ok := check.Same([]string{a, a + "y"}); !ok || equal {
		t.Fatalf("got %v, %v", equal, ok)
	}
}
