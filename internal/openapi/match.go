package openapi

import (
	"slices"
	"strings"
)

// MatchPath finds the operation that answers a request with a real path, such as /orders/42. A literal
// segment beats a {name} segment, so /orders/new wins over /orders/{id}. The path may be written
// with or without a prefix of the document.
func (d Document) MatchPath(method, path string, alsoTry ...string) Match {
	var best Match
	bestScore := -1
	cands := append([]string{path}, alsoTry...)
	for _, op := range d.Operations {
		for _, cand := range cands {
			score, ok := d.fits(op.Path, cand)
			if !ok {
				continue
			}
			best.PathKnown = true
			if !slices.Contains(best.Methods, op.Method) {
				best.Methods = append(best.Methods, op.Method)
			}
			if strings.EqualFold(op.Method, method) && score > bestScore {
				best.Found, best.Operation, bestScore = true, op, score
			}
			break
		}
	}
	slices.Sort(best.Methods)
	return best
}

// fits reports whether the real path is the template, as it is or under a prefix of the
// document, and how many literal segments agree.
func (d Document) fits(template, path string) (int, bool) {
	if n, ok := segmentsFit(template, path); ok {
		return n, true
	}
	for _, p := range d.Prefixes {
		if n, ok := segmentsFit(p+template, path); ok {
			return n, true
		}
	}
	return 0, false
}

func segmentsFit(template, path string) (int, bool) {
	t := strings.Split(strings.Trim(template, "/"), "/")
	p := strings.Split(strings.Trim(path, "/"), "/")
	if len(t) != len(p) {
		return 0, false
	}
	literal := 0
	for i := range t {
		if strings.Contains(t[i], "{") {
			if p[i] == "" {
				return 0, false
			}
			continue
		}
		if t[i] != p[i] {
			return 0, false
		}
		literal++
	}
	return literal, true
}
