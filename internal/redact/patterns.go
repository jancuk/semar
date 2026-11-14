package redact

import (
	"encoding/base64"
	"regexp"
	"strings"
)

// A pattern finds a secret by its shape. valid, when set, rejects a match that
// only looks like one.
type pattern struct {
	re    *regexp.Regexp
	valid func(match string) bool
}

var (
	markerRE = regexp.MustCompile(`\[redacted:[0-9a-f]{8}\]`)
	builtins = []pattern{
		{re: regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*`)},
		// At least 16 characters, so that English such as "the bearer of" is left alone.
		{re: regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9\-._~+/]{16,}=*`)},
		{re: regexp.MustCompile(`(?i)\bbasic\s+[A-Za-z0-9+/]{8,}={0,2}`), valid: isBasicCredential},
	}
)

// isBasicCredential accepts base64 of "user:password" (RFC 7617), so that a
// phrase such as "basic information" is not taken for one.
func isBasicCredential(match string) bool {
	fields := strings.Fields(match)
	token := fields[len(fields)-1]
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
		if d, err := enc.DecodeString(token); err == nil {
			return strings.Contains(string(d), ":")
		}
	}
	return false
}

// patternSpans looks for patterns outside existing markers, so that a second
// pass finds nothing new.
func (s *scrubber) patternSpans(v view) []span {
	var spans []span
	scan := func(from, to int) {
		seg := v.text[from:to]
		for _, p := range s.r.patterns {
			for _, loc := range p.re.FindAllStringIndex(seg, -1) {
				if loc[1] == loc[0] || p.valid != nil && !p.valid(seg[loc[0]:loc[1]]) {
					continue
				}
				start, end := v.sourceSpan(from+loc[0], from+loc[1])
				spans = append(spans, span{start: start, end: end})
			}
		}
	}
	pos := 0
	for _, m := range markerRE.FindAllStringIndex(v.text, -1) {
		scan(pos, m[0])
		pos = m[1]
	}
	scan(pos, len(v.text))
	return spans
}
