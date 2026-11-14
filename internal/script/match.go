package script

import "strings"

// segments splits a request path. ok is false for a path that can never match:
// no leading slash, an empty, "." or ".." segment (also percent-encoded), or a query or fragment.
func segments(p string) (segs []string, ok bool) {
	if !strings.HasPrefix(p, "/") || strings.ContainsAny(p, "?#") {
		return nil, false
	}
	body := p[1:]
	if body == "" {
		return nil, true
	}
	segs = strings.Split(body, "/")
	if len(segs) > 1 && segs[len(segs)-1] == "" {
		segs = segs[:len(segs)-1]
	}
	for _, s := range segs {
		switch strings.ReplaceAll(strings.ToLower(s), "%2e", ".") {
		case "", ".", "..":
			return nil, false
		}
	}
	return segs, true
}

func isParam(seg string) bool {
	return len(seg) > 2 && seg[0] == '{' && seg[len(seg)-1] == '}' && !strings.ContainsAny(seg[1:len(seg)-1], "{}")
}

// MatchPath reports whether an actual request path fits a hint template.
// A segment such as {id} in the hint matches exactly one segment of the actual path,
// and a segment with a {{variable}} counts as one segment. All other segments must be equal.
func MatchPath(hint, actual string) bool {
	h, ok := segments(hint)
	if !ok {
		return false
	}
	a, ok := segments(actual)
	if !ok || len(h) != len(a) {
		return false
	}
	for i := range h {
		if !isParam(h[i]) && h[i] != a[i] {
			return false
		}
	}
	return true
}
