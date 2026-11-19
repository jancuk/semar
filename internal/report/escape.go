package report

import (
	"fmt"
	"strings"
	"unicode"
)

const maxShown = 200

// escape makes text from a service or a scenario file safe inside one line of Markdown.
// Format characters (category Cf, such as the bidi controls and the zero-width space) are
// dropped first, so that they cannot hide a scheme or a marker from the rules below.
// A colon after a scheme and the dot of "www." are escaped so that no renderer makes a link
// of them, and "$" and "@" are escaped so that no renderer makes math or a mention of them.
func escape(s string) string {
	var b strings.Builder
	rs := make([]rune, 0, len(s))
	for _, r := range s {
		if !unicode.Is(unicode.Cf, r) {
			rs = append(rs, r)
		}
	}
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
		case strings.ContainsRune("\\`*_[]|~#$@", r):
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == ':' && i+2 < len(rs) && rs[i+1] == '/' && rs[i+2] == '/':
			b.WriteString(`\:`)
		case r == '.' && i >= 3 && strings.EqualFold(string(rs[i-3:i]), "www"):
			b.WriteString(`\.`)
		case r == '\r' && i+1 < len(rs) && rs[i+1] == '\n':
		case r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029':
			b.WriteString("↵")
		case unicode.IsControl(r):
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// cut keeps the first max characters of s and says how many it left out.
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

// shown cuts a value and escapes it. The cut happens before the escape, so the mark stays readable.
func shown(s string) string {
	head, more := cut(s, maxShown)
	out := escape(head)
	if more > 0 {
		out += cutMark(more)
	}
	return out
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// viewer fills a view with text. The Markdown report escapes every text for one line of Markdown, and
// the HTML report keeps it as it is, because html/template escapes it when it is written.
type viewer struct{ show func(string) string }

var markdownViewer = viewer{show: shown}

// rawViewer cuts a text like the Markdown report does and leaves it unescaped.
var rawViewer = viewer{show: func(s string) string {
	head, more := cut(s, maxShown)
	if more > 0 {
		head += cutMark(more)
	}
	return plainLine(head)
}}

// plainLine keeps text on one line and drops format characters, as escape does.
func plainLine(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Cf, r):
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
