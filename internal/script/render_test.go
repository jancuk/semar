package script

import (
	"reflect"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func manyKeys() domain.APIRequest {
	return domain.APIRequest{
		Service: "orders",
		Method:  "GET",
		Path:    "/v1/orders",
		Query:   map[string]string{"page": "{{p", "a": "{{x", "z": "{{y", "m": "ok"},
		Headers: map[string]string{"X-B": "{{b", "X-A": "{{a", "X-C": "{{c"},
		JSON:    map[string]any{"b": []any{"{{n"}, "a": map[string]any{"y": "{{y", "x": "{{x"}},
	}
}

func TestRequestProblemsComeInAFixedOrder(t *testing.T) {
	want := []string{
		`query a: malformed variable in "{{x"`,
		`query page: malformed variable in "{{p"`,
		`query z: malformed variable in "{{y"`,
		`header X-A: malformed variable in "{{a"`,
		`header X-B: malformed variable in "{{b"`,
		`header X-C: malformed variable in "{{c"`,
		`json.a.x: malformed variable in "{{x"`,
		`json.a.y: malformed variable in "{{y"`,
		`json.b[0]: malformed variable in "{{n"`,
	}
	req := manyKeys()
	ss := domain.ScriptStep{Step: "s1", Request: &req}
	first := validateRequest(ss, nil, false, nil)
	for range 50 {
		if got := templateProblems(req); !reflect.DeepEqual(got, want) {
			t.Fatalf("templateProblems = %q\nwant %q", got, want)
		}
		if got := validateRequest(ss, nil, false, nil); !reflect.DeepEqual(got, first) {
			t.Fatalf("validateRequest = %v\nfirst run %v", got, first)
		}
	}
}

func TestRenderFailsOnTheFirstKey(t *testing.T) {
	cases := []struct {
		name string
		req  domain.APIRequest
		want string
	}{
		{"query", domain.APIRequest{Path: "/x", Query: map[string]string{"page": "{{p}}", "a": "{{q}}", "z": "{{r}}"}}, "query a: unknown variable q"},
		{"header", domain.APIRequest{Path: "/x", Headers: map[string]string{"X-B": "{{p}}", "X-A": "{{q}}"}}, "header X-A: unknown variable q"},
		{"json", domain.APIRequest{Path: "/x", JSON: map[string]any{"b": "{{p}}", "a": []any{"ok", "{{q}}"}}}, "json.a[1]: unknown variable q"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for range 50 {
				_, err := Render(c.req, map[string]string{})
				if err == nil || err.Error() != c.want {
					t.Fatalf("got %v, want %q", err, c.want)
				}
			}
		})
	}
}
