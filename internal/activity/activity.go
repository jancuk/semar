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
	if a.Web != nil {
		return fromWebAction(a, class, evidencePath)
	}
	method, target, template, realPath := "", "", "", ""
	if a.Request != nil {
		method = strings.ToUpper(a.Request.Method)
		template = a.Request.PathTemplate
		realPath = pathOf(a.Request.URL)
		target = method + " " + realPath
	}
	verb, object := verbOf(class, method), objectOf(a, class, template, realPath)
	if a.Use.Pass == domain.PassCleanup && class != effects.Read {
		// A cleanup step undoes what a fixture made, whatever its method: a cancel is a POST.
		verb = domain.VerbDelete
		if o := cleanedObject(a, template, realPath); o != nil {
			object = o
		}
	}
	return domain.Activity{
		Seq:      a.Seq,
		At:       a.At,
		Actor:    a.By,
		Use:      a.Use,
		Surface:  a.Surface,
		Verb:     verb,
		Target:   target,
		Object:   object,
		Result:   resultOf(a),
		Why:      a.Why,
		TraceID:  a.TraceID,
		Evidence: evidencePath,
		Doc:      a.Doc,
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
		return u.EscapedPath()
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
		// The template lines up with the end of the real path; a service
		// base URL may put extra segments in front.
		real := strings.Split(strings.Trim(realPath, "/"), "/")
		if offset := len(real) - len(segs); offset >= 0 {
			obj.ID = real[offset+last+1]
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

// cleanedObject is the record that a cleanup path names: the last parameter of the template, with
// the noun before it. /v1/orders/{id}/cancel names an order, not a cancel. It is nil when the
// template has no parameter after a noun.
func cleanedObject(a domain.Action, template, realPath string) *domain.ObjectRef {
	segs := strings.Split(strings.Trim(template, "/"), "/")
	p := -1
	for i, s := range segs {
		if isParam(s) {
			p = i
		}
	}
	if p < 1 || a.Response == nil {
		return nil
	}
	obj := &domain.ObjectRef{Type: singular(segs[p-1])}
	real := strings.Split(strings.Trim(realPath, "/"), "/")
	if offset := len(real) - len(segs); offset >= 0 {
		obj.ID = real[offset+p]
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

// singular applies the rules in order: "ies" gives "y", "sses" loses "es",
// "ss" is unchanged, a final "s" is dropped. It is naive: "status" gives "statu".
func singular(word string) string {
	switch {
	case strings.HasSuffix(word, "ies"):
		return strings.TrimSuffix(word, "ies") + "y"
	case strings.HasSuffix(word, "sses"):
		return strings.TrimSuffix(word, "es")
	case strings.HasSuffix(word, "ss"):
		return word
	}
	return strings.TrimSuffix(word, "s")
}
