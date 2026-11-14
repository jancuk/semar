package report_test

import (
	"strings"
	"testing"
	"unicode"

	"github.com/jancuk/semar/internal/report"
)

func FuzzEscape(f *testing.F) {
	for _, seed := range []string{
		"", "plain", "<script>", "a|b", "a`b", `a\|b`, `\`, "x\r\ny", "\u202e", "http:\u200b//x", "www\u200b.x", "$x$", "_a_",
		"\\\\|", "a\u2028b", "\xff\xfe", "[l](http://e)", "&lt;",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := []rune(report.Escape(in))
		escaped := false
		for i, r := range out {
			switch {
			case r == '<' || r == '>':
				t.Fatalf("Escape(%q) = %q holds %q", in, string(out), r)
			case r == '\n' || r == '\r':
				t.Fatalf("Escape(%q) = %q holds a line break", in, string(out))
			case unicode.Is(unicode.Cf, r):
				t.Fatalf("Escape(%q) = %q holds the format character %U", in, string(out), r)
			case !escaped && (r == '|' || r == '`'):
				t.Fatalf("Escape(%q) = %q holds an unescaped %q", in, string(out), r)
			case !escaped && r == ':' && i+2 < len(out) && out[i+1] == '/' && out[i+2] == '/':
				t.Fatalf("Escape(%q) = %q holds an unescaped scheme separator", in, string(out))
			case !escaped && r == '.' && i >= 3 && strings.EqualFold(string(out[i-3:i]), "www"):
				t.Fatalf("Escape(%q) = %q holds an unescaped www.", in, string(out))
			}
			escaped = !escaped && r == '\\'
		}
		if escaped {
			t.Fatalf("Escape(%q) = %q ends in a backslash that escapes what follows it", in, string(out))
		}
	})
}
