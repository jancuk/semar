package export

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCutMarker(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"short", "abc", 5, "abc"},
		{"exact", "abcde", 5, "abcde"},
		{"one over", "abcdef", 5, "abcde [cut: 1 more characters]"},
		{"counts characters, not bytes", "ééééééé", 3, "ééé [cut: 4 more characters]"},
		{"empty", "", 5, ""},
		{"many", strings.Repeat("x", 1123), maxMessage, strings.Repeat("x", maxMessage) + " [cut: 123 more characters]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cut(tt.in, tt.max); got != tt.want {
				t.Errorf("cut(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
			}
		})
	}
	if got := cutDetail(strings.Repeat("y", maxDetail+5)); !strings.HasSuffix(got, "[cut: 5 more characters]") {
		t.Errorf("cutDetail did not cut at %d: ends %q", maxDetail, got[len(got)-30:])
	}
	if got := cutMessage(strings.Repeat("y", maxDetail)); utf8.RuneCountInString(got) > maxMessage+40 {
		t.Errorf("cutMessage left %d characters", utf8.RuneCountInString(got))
	}
}

func TestCutKeepsValidUTF8(t *testing.T) {
	s := strings.Repeat("\U0001F600", 10)
	if got := cut(s, 3); !utf8.ValidString(got) || !strings.HasPrefix(got, strings.Repeat("\U0001F600", 3)+" [cut: 7") {
		t.Errorf("cut = %q", got)
	}
}

func TestXMLText(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain", "refund ok", "refund ok"},
		{"keeps tab newline return", "a\tb\nc\rd", "a\tb\nc\rd"},
		{"escape", "a\x1bb", "a�b"},
		{"nul", "a\x00b", "a�b"},
		{"c0 and del range", "\x01\x08\x0b\x0c\x0e\x1f", "������"},
		{"surrogate half in utf-8", "a\xed\xa0\x80b", "a���b"},
		{"not utf-8", "a\xffb", "a�b"},
		{"not characters", "a￾b￿", "a�b�"},
		{"keeps astral", "a\U0001F600", "a\U0001F600"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := xmlText(tt.in)
			if got != tt.want {
				t.Errorf("xmlText(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("xmlText(%q) is not UTF-8", tt.in)
			}
		})
	}
}
