package report

import (
	"fmt"
	"strings"
	"unicode"
)

const maxShown = 200

// Escape makes text from a service or a scenario file safe inside one line of Markdown.
func Escape(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '&':
			b.WriteString("&amp;")
		case r == '_' && i > 0 && i+1 < len(rs) && isWordRune(rs[i-1]) && isWordRune(rs[i+1]):
			b.WriteRune(r)
		case strings.ContainsRune("\\`*_[]|~#", r):
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\r' && i+1 < len(rs) && rs[i+1] == '\n':
		case r == '\n' || r == '\r' || r == ' ' || r == ' ':
			b.WriteString("↵")
		case unicode.IsControl(r):
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Cut keeps the first max characters of s and marks the cut.
func Cut(s string, max int) string {
	head, more := cut(s, max)
	if more == 0 {
		return head
	}
	return head + cutMark(more)
}

func cut(s string, max int) (string, int) {
	rs := []rune(s)
	if len(rs) <= max {
		return s, 0
	}
	return string(rs[:max]), len(rs) - max
}

func cutMark(more int) string {
	return fmt.Sprintf("… [cut: %d more characters]", more)
}

// shown escapes a value and cuts it. The cut happens before the escape, so the mark stays readable.
func shown(s string) string {
	head, more := cut(s, maxShown)
	out := Escape(head)
	if more > 0 {
		out += cutMark(more)
	}
	return out
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
