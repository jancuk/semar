package script_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/script"
)

func validPath(p string) bool {
	if !strings.HasPrefix(p, "/") {
		return false
	}
	body := strings.TrimSuffix(p[1:], "/")
	if body == "" {
		return p == "/"
	}
	for _, seg := range strings.Split(body, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, "?#") || strings.Contains(strings.ToLower(seg), "%2e") {
			return false
		}
	}
	return true
}

func hasDotDotSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

func FuzzMatchPath(f *testing.F) {
	seeds := [][2]string{
		{"/v1/orders/{id}", "/v1/orders/{{order_id}}"},
		{"/v1/orders/{id}", "/v1/orders/../admin"},
		{"/v1/orders", "/v1/orders/"},
		{"/", "//"},
		{"{", "}"},
		{"/{id}/{id}", "/a/b"},
		{"", ""},
	}
	for _, s := range seeds {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, hint, actual string) {
		got := script.MatchPath(hint, actual)
		if got && hasDotDotSegment(actual) {
			t.Fatalf("MatchPath(%q, %q) matched a path with a .. segment", hint, actual)
		}
		if !strings.ContainsAny(hint, "{}") && validPath(hint) && !script.MatchPath(hint, hint) {
			t.Fatalf("hint %q does not match itself", hint)
		}
	})
}

func FuzzRender(f *testing.F) {
	f.Add("/v1/orders/{{id}}", "a", "1", "x{{id}}")
	f.Add("/v1/{{", "id", "..", "{{")
	f.Add("/{{{{id}}}}", "id", "{{id}}", "}}")
	f.Add("/{{a}}", "a", "x/y", "")
	f.Add("{{{id}}", "0", "{", "0")
	f.Add("/v1/orders/%2{{id}}", "x", "e", "")
	f.Add("/v1/orders/%{{id}}", "x", "2f", "")
	f.Fuzz(func(t *testing.T, path, name, value, other string) {
		req := domain.APIRequest{
			Path:    path,
			Query:   map[string]string{"q": other},
			Headers: map[string]string{"h": other},
			JSON:    map[string]any{"k": []any{other, map[string]any{"n": other}}},
		}
		got, err := script.Render(req, map[string]string{name: value, "id": value})
		if err != nil {
			return
		}
		if strings.Contains(got.Path, "{{") {
			t.Fatalf("rendered path %q holds {{", got.Path)
		}
		if strings.Count(got.Path, "/") != strings.Count(path, "/") {
			t.Fatalf("template %q rendered as %q with another number of segments", path, got.Path)
		}
		for _, seg := range strings.Split(got.Path, "/") {
			dec, derr := url.PathUnescape(seg)
			if derr != nil {
				dec = strings.ReplaceAll(strings.ToLower(seg), "%2e", ".")
			}
			if dec == "." || dec == ".." {
				t.Fatalf("template %q rendered as %q with a dot segment", path, got.Path)
			}
		}
	})
}
