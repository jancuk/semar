package domain_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func TestCaptureExtract(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		text    string
		want    string
		ok      bool
	}{
		{"no pattern keeps the text", "", "Order #A1", "Order #A1", true},
		{"the first group", `Order #([A-Z0-9]+)`, "Your Order #A1B2 is ready", "A1B2", true},
		{"no group gives the match", `[A-Z][0-9]+`, "Order #A12 ready", "A12", true},
		{"no match is missing", `Order #([0-9]+)`, "Hello", "", false},
		{"an empty group is a value", `x(\d*)`, "x", "", true},
		{"a pattern that does not compile is missing", `([a-z`, "abc", "", false},
		{"a long pattern is missing", strings.Repeat("a", domain.MaxPatternLen+1), "a", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := domain.Capture{Name: "x", Element: "e", Pattern: tt.pattern}.Extract(tt.text)
			if got != tt.want || ok != tt.ok {
				t.Errorf("Extract(%q) = %q, %v, want %q, %v", tt.text, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestCheckPattern(t *testing.T) {
	if err := domain.CheckPattern(""); err != nil {
		t.Errorf("empty: %v", err)
	}
	if err := domain.CheckPattern(`#(\d+)`); err != nil {
		t.Errorf("good: %v", err)
	}
	if err := domain.CheckPattern(`(`); err == nil || !strings.Contains(err.Error(), "regular expression") {
		t.Errorf("bad: %v", err)
	}
	if err := domain.CheckPattern(strings.Repeat("a", 201)); err == nil || !strings.Contains(err.Error(), "200") {
		t.Errorf("long: %v", err)
	}
}
