package export

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// The longest text of each kind that an export writes. A report that a CI system shows must
// stay readable, and an observed value can be large.
const (
	maxMessage = 1000
	maxDetail  = 8000
)

// cut keeps the first max characters of s and says how many it left out.
func cut(s string, max int) string {
	if len(s) <= max { // a byte count at or below max cannot hold more than max characters
		return s
	}
	n := utf8.RuneCountInString(s)
	if n <= max {
		return s
	}
	i, seen := 0, 0
	for seen < max {
		_, w := utf8.DecodeRuneInString(s[i:])
		i += w
		seen++
	}
	return fmt.Sprintf("%s [cut: %d more characters]", s[:i], n-max)
}

func cutMessage(s string) string { return cut(s, maxMessage) }

func cutDetail(s string) string { return cut(s, maxDetail) }

// xmlText replaces every character that XML 1.0 forbids, and every byte that is not UTF-8,
// with U+FFFD. encoding/xml escapes the rest.
func xmlText(s string) string {
	ok := true
	for _, r := range s {
		if !xmlChar(r) {
			ok = false
			break
		}
	}
	if ok && utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if xmlChar(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(utf8.RuneError)
		}
	}
	return b.String()
}

// xmlChar is the Char production of XML 1.0. A byte that is not UTF-8 reads as U+FFFD, which is allowed.
func xmlChar(r rune) bool {
	return r == '\t' || r == '\n' || r == '\r' ||
		(r >= 0x20 && r <= 0xD7FF) ||
		(r >= 0xE000 && r <= 0xFFFD) ||
		(r >= 0x10000 && r <= 0x10FFFF)
}
