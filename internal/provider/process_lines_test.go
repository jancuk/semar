package provider

import (
	"slices"
	"strings"
	"testing"
)

func TestLineSplitter(t *testing.T) {
	exact := strings.Repeat("a", MaxStdoutLine)
	over := strings.Repeat("b", MaxStdoutLine+1)
	cases := []struct {
		name   string
		chunks []string
		want   []string
	}{
		{"one line per chunk", []string{"a\n", "b\n"}, []string{"a", "b"}},
		{"several lines in one chunk", []string{"a\nb\nc\n"}, []string{"a", "b", "c"}},
		{"a line across chunks", []string{"ab", "c", "d\ne", "f\n"}, []string{"abcd", "ef"}},
		{"a carriage return is cut", []string{"a\r\nb\r", "\n"}, []string{"a", "b"}},
		{"empty lines are passed", []string{"\n\na\n"}, []string{"", "", "a"}},
		{"a last line with no ending is not passed", []string{"a\nb"}, []string{"a"}},
		{"a line of exactly the limit is passed", []string{exact + "\nz\n"}, []string{exact, "z"}},
		{"the limit across chunks", []string{exact[:10], exact[10:], "\nz\n"}, []string{exact, "z"}},
		{"a longer line is skipped", []string{over + "\nz\n"}, []string{"z"}},
		{"a longer line across chunks is skipped", []string{"x", over[:MaxStdoutLine], "yy", "y\nz\n"}, []string{"z"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			s := lineSplitter{fn: func(line []byte) { got = append(got, string(line)) }}
			for _, chunk := range c.chunks {
				s.feed([]byte(chunk))
			}
			if !slices.Equal(got, c.want) {
				t.Fatalf("got %d lines %.40q, want %d lines %.40q", len(got), got, len(c.want), c.want)
			}
			if cap(s.buf) > 2*MaxStdoutLine {
				t.Fatalf("the buffer grew to %d bytes", cap(s.buf))
			}
		})
	}
}

func TestLineWriterStopsAtTheCap(t *testing.T) {
	full := false
	var got []string
	w := &lineWriter{
		out:   &cappedBuffer{limit: 6, onFull: func() { full = true }},
		lines: lineSplitter{fn: func(line []byte) { got = append(got, string(line)) }},
	}
	for _, chunk := range []string{"a\n", "b\n", "c\nd\n", "e\n"} {
		if n, err := w.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	if !full || !slices.Equal(got, []string{"a", "b"}) || w.out.bytes() != nil {
		t.Fatalf("full %v, lines %q, kept %q", full, got, w.out.bytes())
	}
}
