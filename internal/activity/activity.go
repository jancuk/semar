// Package activity derives the activity log from recorded actions. Every
// function is pure: the same actions always give the same log.
package activity

import (
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
)

// FromAction takes the redacted action, so nothing it returns can hold a secret.
func FromAction(a domain.Action, class effects.Class, evidencePath string) domain.Activity {
	method, target, template, realPath := "", "", "", ""
	if a.Request != nil {
		method = strings.ToUpper(a.Request.Method)
		template = a.Request.PathTemplate
		realPath = pathOf(a.Request.URL)
		target = method + " " + realPath
	}
	return domain.Activity{
		Seq:      a.Seq,
		At:       a.At,
		Actor:    a.By,
		Use:      a.Use,
		Surface:  a.Surface,
		Verb:     verbOf(class, method),
		Target:   target,
		Object:   objectOf(a, class, template, realPath),
		Result:   resultOf(a),
		Why:      a.Why,
		TraceID:  a.TraceID,
		Evidence: evidencePath,
	}
}

func verbOf(class effects.Class, method string) domain.Verb {
	if class == effects.Read {
		return domain.VerbRead
	}
	switch method {
	case "POST":
		return domain.VerbCreate
	case "DELETE":
		return domain.VerbDelete
	default:
		return domain.VerbUpdate
	}
}

func resultOf(a domain.Action) string {
	switch {
	case a.Refused != "":
		return "refused: " + a.Refused
	case a.Response != nil:
		return strconv.Itoa(a.Response.Status)
	case a.Err != "":
		return "error: " + a.Err
	}
	return ""
}

func pathOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Path
	}
	path, _, _ := strings.Cut(raw, "?")
	return path
}

func objectOf(a domain.Action, class effects.Class, template, realPath string) *domain.ObjectRef {
	if a.Refused != "" || a.Response == nil {
		return nil
	}
	segs := strings.Split(strings.Trim(template, "/"), "/")
	last := -1
	for i, s := range segs {
		if s != "" && !isParam(s) {
			last = i
		}
	}
	if last < 0 {
		return nil
	}
	obj := &domain.ObjectRef{Type: singular(segs[last])}
	if last+1 < len(segs) && isParam(segs[last+1]) {
		real := strings.Split(strings.Trim(realPath, "/"), "/")
		if len(real) == len(segs) {
			obj.ID = real[last+1]
		}
		return obj
	}
	if class == effects.Read {
		return nil
	}
	if names := sortedKeys(a.Captures); len(names) > 0 {
		obj.ID = a.Captures[names[0]]
	}
	return obj
}

func isParam(seg string) bool { return strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") }

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// singular removes one final s. A word that ends in "sses" loses "es", so
// "addresses" gives "address".
func singular(word string) string {
	if strings.HasSuffix(word, "sses") {
		return strings.TrimSuffix(word, "es")
	}
	return strings.TrimSuffix(word, "s")
}
