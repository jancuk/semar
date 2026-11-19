package script_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/scenario"
	"github.com/jancuk/semar/internal/script"
)

func loadPair(t *testing.T) (domain.Scenario, domain.Script) {
	t.Helper()
	sc, err := scenario.Load("../scenario/testdata/valid.feature")
	if err != nil {
		t.Fatal(err)
	}
	s, err := script.Load("testdata/valid.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return sc, s
}

func TestFixtureHashMatchesScenario(t *testing.T) {
	sc, s := loadPair(t)
	if got := domain.ExpectHash(sc); got != s.ExpectHash {
		t.Fatalf("internal/scenario/testdata/valid.feature now hashes to %s: set expectHash in internal/script/testdata/valid.yaml to it", got)
	}
}

func TestParse(t *testing.T) {
	head := "apiVersion: semar/v1\nkind: Script\nscenario: S\nexpectHash: x\nauthoredBy: { instance: a, model: b, harness: c }\n"
	cases := []struct {
		name string
		in   string
		want []string // substrings of the error. Empty means success
	}{
		{"valid", head + "steps:\n  - step: a\n    request: { service: s, method: GET, path: /x }\n", nil},
		{"unknown key", head + "steps:\n  - step: a\n    bogus: 1\n", []string{"s.yaml:8", "bogus"}},
		{"two documents", head + "steps: []\n---\nkind: Script\n", []string{"s.yaml", "more than one"}},
		{"empty", "", []string{"s.yaml", "empty"}},
		{"exact numbers in json", head + "steps:\n  - step: a\n    request: { service: s, method: POST, path: /x, json: { a: 9007199254740992, b: 0.1, c: [1, 2.5, 18446744073709551615] } }\n", nil},
		{"wide integer in json", head + "steps:\n  - step: a\n    request: { service: s, method: POST, path: /x, json: { amount: 123456789012345678901234 } }\n", []string{"s.yaml:8", "json", "fewer digits"}},
		{"wide float in a json list", head + "steps:\n  - step: a\n    request: { service: s, method: POST, path: /x, json: { ids: [1, 12345678901234567891.0] } }\n", []string{"s.yaml:8", "json", "fewer digits"}},
		{"infinity in json", head + "steps:\n  - step: a\n    request: { service: s, method: POST, path: /x, json: { limit: .inf } }\n", []string{"s.yaml:8", "json", "finite"}},
		{"non-string key in json", head + "steps:\n  - step: a\n    request: { service: s, method: POST, path: /x, json: { 1: a } }\n", []string{"s.yaml", "steps[0]", "mapping key must be a string"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := script.Parse([]byte(c.in), "s.yaml")
			if len(c.want) == 0 {
				if err != nil {
					t.Fatal(err)
				}
				if s.Steps[0].Request.Path != "/x" {
					t.Fatalf("got %+v", s)
				}
				return
			}
			if err == nil {
				t.Fatal("want an error")
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Fatalf("error %q lacks %q", err, w)
				}
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := script.Load("testdata/missing.yaml"); err == nil || !strings.Contains(err.Error(), "testdata/missing.yaml") {
		t.Fatalf("got %v", err)
	}
}

func TestParseJSONBodyIsEncodable(t *testing.T) {
	_, s := loadPair(t)
	m, ok := s.Steps[0].Request.JSON.(map[string]any)
	if !ok || m["amount"] == nil {
		t.Fatalf("json body is %#v", s.Steps[0].Request.JSON)
	}
}

// withoutHint removes the hint of the first step and keeps the script's hash in step with the scenario.
func withoutHint(sc *domain.Scenario, s *domain.Script) {
	sc.Steps[0].Hint = nil
	s.ExpectHash = domain.ExpectHash(*sc)
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name   string
		change func(*domain.Scenario, *domain.Script)
		want   string // substring of one Problem.Message. Empty means no problem
	}{
		{"valid", func(*domain.Scenario, *domain.Script) {}, ""},
		{"other hash", func(_ *domain.Scenario, s *domain.Script) { s.ExpectHash = "0000" }, "the script was written for other expected results"},
		{"script without hash", func(_ *domain.Scenario, s *domain.Script) { s.ExpectHash = "" }, "the script has no expectation hash"},
		{"scenario without hash", func(sc *domain.Scenario, s *domain.Script) {
			sc.Steps[1].Expect[1].Check.Equals = func() {}
			s.ExpectHash = ""
		}, "the scenario has no expectation hash"},
		{"wrong apiVersion", func(_ *domain.Scenario, s *domain.Script) { s.APIVersion = "semar/v2" }, "apiVersion must be semar/v1"},
		{"wrong kind", func(_ *domain.Scenario, s *domain.Script) { s.Kind = "Scenario" }, "kind must be Script"},
		{"other scenario", func(_ *domain.Scenario, s *domain.Script) { s.Scenario = "OTHER-1" }, "the script is for scenario OTHER-1"},
		{"setup step left out", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps = s.Steps[1:]
			s.Steps[0].Request.Path = "/v1/orders/1/refunds"
			s.Steps[1].Request.Path = "/v1/orders/1"
		}, ""},
		{"step with no request", func(_ *domain.Scenario, s *domain.Script) { s.Steps = s.Steps[:2] }, "step order-unchanged has an expected result and no request"},
		{"two requests for one step", func(_ *domain.Scenario, s *domain.Script) { s.Steps = append(s.Steps, s.Steps[1]) }, "step refund-too-much is bound more than once"},
		{"unknown step", func(_ *domain.Scenario, s *domain.Script) { s.Steps[2].Step = "nope" }, "step nope is not in the scenario"},
		{"other method", func(_ *domain.Scenario, s *domain.Script) { s.Steps[1].Request.Method = "PUT" }, "the request is PUT and the step expects POST"},
		{"other path", func(_ *domain.Scenario, s *domain.Script) { s.Steps[1].Request.Path = "/v1/orders/{{order_id}}/cancel" }, "does not match /v1/orders/{id}/refunds"},
		{"setup step with other method", func(_ *domain.Scenario, s *domain.Script) { s.Steps[0].Request.Method = "PUT" }, "the request is PUT and the step expects POST"},
		{"out of order", func(_ *domain.Scenario, s *domain.Script) { s.Steps[1], s.Steps[2] = s.Steps[2], s.Steps[1] }, "steps are not in the scenario's order"},
		{"variable never captured", func(_ *domain.Scenario, s *domain.Script) { s.Steps[0].Captures = nil }, "order_id is used before any step captures it"},
		{"variable captured by the same step only", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].Captures = nil
			s.Steps[1].Captures = []domain.Capture{{Name: "order_id", JSONPath: "$.id"}}
		}, "order_id is used before any step captures it"},
		{"variable used in a header", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].Request.Headers = map[string]string{"X-A": "{{nope}}"}
		}, "nope is used before any step captures it"},
		{"variable used in a json body", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].Request.JSON = map[string]any{"a": []any{"x{{nope}}"}}
		}, "nope is used before any step captures it"},
		{"readback of another object", func(_ *domain.Scenario, s *domain.Script) { s.Steps[2].Request.Path = "/v1/orders/999" }, "does not use a value from an earlier step"},
		{"empty service", func(_ *domain.Scenario, s *domain.Script) { s.Steps[0].Request.Service = "" }, "the request has no service"},
		{"empty method", func(_ *domain.Scenario, s *domain.Script) { s.Steps[0].Request.Method = "" }, "the request has no method"},
		{"unknown method", func(_ *domain.Scenario, s *domain.Script) { s.Steps[0].Request.Method = "get" }, "method get is not one of"},
		{"path without slash", func(_ *domain.Scenario, s *domain.Script) { s.Steps[0].Request.Path = "v1/orders" }, "path v1/orders must start with /"},
		{"capture without name", func(_ *domain.Scenario, s *domain.Script) { s.Steps[0].Captures[0].Name = "" }, "a capture has no name"},
		{"capture with bad name", func(_ *domain.Scenario, s *domain.Script) { s.Steps[0].Captures[0].Name = "a b" }, "capture name a b is not a valid variable name"},
		{"capture with bad jsonpath", func(_ *domain.Scenario, s *domain.Script) { s.Steps[0].Captures[0].JSONPath = "$.[" }, "jsonpath does not parse"},
		{"percent before a variable", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[1].Request.Path = "/v1/orders/%{{order_id}}/refunds"
		}, "a variable directly follows a percent sign"},
		{"percent and a hex digit before a variable", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[1].Request.Path = "/v1/orders/%2{{order_id}}/refunds"
		}, "a variable directly follows a percent sign"},
		{"percent and an upper case hex digit before a variable", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[1].Request.Path = "/v1/orders/%F{{order_id}}/refunds"
		}, "a variable directly follows a percent sign"},
		{"percent and two hex digits before a variable", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[1].Request.Path = "/v1/orders/%2f{{order_id}}/refunds"
		}, "holds an encoded slash or backslash"},
		{"raw backslash in a path", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].Request.Path = `/v1\orders`
		}, "holds a backslash"},
		{"encoded percent sign in a path", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[1].Request.Path = "/v1/orders/%25{{order_id}}/refunds"
		}, "holds an encoded percent sign"},
		{"spaced variable in a path", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[1].Request.Path = "/v1/orders/{{ order_id }}/refunds"
		}, "malformed variable"},
		{"open brace pair in a path", func(_ *domain.Scenario, s *domain.Script) { s.Steps[1].Request.Path = "/v1/orders/{{/refunds" }, "malformed variable"},
		{"dotted variable with an empty part in a query", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].Request.Query = map[string]string{"a": "{{a..b}}"}
		}, "malformed variable"},
		{"malformed variable in a header", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].Request.Headers = map[string]string{"A": "{{ x }}"}
		}, "malformed variable"},
		{"an empty header name", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].Request.Headers = map[string]string{"": "1"}
		}, `header name "" is not a valid HTTP header name`},
		{"a header name with a space", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].Request.Headers = map[string]string{"X A": "1"}
		}, `header name "X A" is not a valid HTTP header name`},
		{"a header name with a colon", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].Request.Headers = map[string]string{"X-A:": "1"}
		}, "not a valid HTTP header name"},
		{"a header name with a non-ASCII letter", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].Request.Headers = map[string]string{"X-Ä": "1"}
		}, "not a valid HTTP header name"},
		{"a header value with a line feed", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].Request.Headers = map[string]string{"X-A": "a\nX-B: b"}
		}, "the value of header \"X-A\" holds a control character"},
		{"a header value with a carriage return around a variable", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[2].Request.Headers = map[string]string{"X-A": "{{order_id}}\r"}
		}, "holds a control character"},
		{"a header value with a nul", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].Request.Headers = map[string]string{"X-A": "a\x00"}
		}, "holds a control character"},
		{"a header value with a del", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].Request.Headers = map[string]string{"X-A": "a\x7f"}
		}, "holds a control character"},
		{"a header value with an escape", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].Request.Headers = map[string]string{"X-A": "\x1b[31m"}
		}, "holds a control character"},
		{"a header value with a vertical tab", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].Request.Headers = map[string]string{"X-A": "a\vb"}
		}, "holds a control character"},
		{"a header value with a tab", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[2].Request.Headers = map[string]string{"X-A": "a\tb", "X-B": "\t{{order_id}}"}
		}, ""},
		{"a valid header", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[2].Request.Headers = map[string]string{"X-Request-For": "order {{order_id}}", "Accept": "application/json", "If-None-Match": `"x"`}
		}, ""},
		{"a malformed variable names its key", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].Request.Headers = map[string]string{"X-A": "{{ x }}"}
			s.Steps[0].Request.Query = map[string]string{"page": "{{a..b}}"}
		}, "header X-A: "},
		{"a malformed variable in a query names its key", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].Request.Query = map[string]string{"page": "{{a..b}}"}
		}, "query page: "},
		{"a malformed variable in a json body names its place", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].Request.JSON = map[string]any{"a": []any{"x", "{{"}}
		}, "json.a[1]: "},
		{"malformed variable in a json string", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].Request.JSON = map[string]any{"a": []any{map[string]any{"b": "{{"}}}
		}, "malformed variable"},
		{"capture with no request", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[2].Request = nil
			s.Steps[2].Captures = []domain.Capture{{Name: "later", JSONPath: "$.id"}}
		}, "a capture needs a request"},
		{"dot segment without a hint", func(sc *domain.Scenario, s *domain.Script) {
			withoutHint(sc, s)
			s.Steps[0].Request.Path = "/v1/../orders"
		}, "holds a dot segment"},
		{"encoded dot segment without a hint", func(sc *domain.Scenario, s *domain.Script) {
			withoutHint(sc, s)
			s.Steps[0].Request.Path = "/v1/%2e%2E/orders"
		}, "holds a dot segment"},
		{"empty segment without a hint", func(sc *domain.Scenario, s *domain.Script) {
			withoutHint(sc, s)
			s.Steps[0].Request.Path = "/v1//orders"
		}, "holds an empty segment"},
		{"question mark without a hint", func(sc *domain.Scenario, s *domain.Script) {
			withoutHint(sc, s)
			s.Steps[0].Request.Path = "/v1/orders?a=1"
		}, "holds ?"},
		{"fragment without a hint", func(sc *domain.Scenario, s *domain.Script) {
			withoutHint(sc, s)
			s.Steps[0].Request.Path = "/v1/orders#a"
		}, "holds #"},
		{"trailing slash without a hint", func(sc *domain.Scenario, s *domain.Script) {
			withoutHint(sc, s)
			s.Steps[0].Request.Path = "/v1/orders/"
		}, ""},
		{"duplicate capture across steps", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[1].Captures = []domain.Capture{{Name: "order_id", JSONPath: "$.id"}}
		}, "capture order_id is defined more than once"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sc, s := loadPair(t)
			c.change(&sc, &s)
			ps := script.Validate(sc, s)
			if c.want == "" {
				if len(ps) != 0 {
					t.Fatalf("want no problem, got %+v", ps)
				}
				return
			}
			for _, p := range ps {
				if strings.Contains(p.Message, c.want) {
					return
				}
			}
			t.Fatalf("want a problem with %q, got %+v", c.want, ps)
		})
	}
}

func TestValidateRefusesTheHeadersThatTheClientOrTheEngineSets(t *testing.T) {
	for _, name := range []string{"Host", "host", "Content-Length", "TRANSFER-ENCODING", "Connection", "Upgrade", "TE", "te", "Trailer", "Keep-Alive", "Proxy-Connection"} {
		t.Run(name, func(t *testing.T) {
			sc, s := loadPair(t)
			s.Steps[0].Request.Headers = map[string]string{name: "x"}
			ps := script.Validate(sc, s)
			if len(ps) != 1 || ps[0].Step != s.Steps[0].Step || !strings.Contains(ps[0].Message, "set by the HTTP client") {
				t.Fatalf("got %+v", ps)
			}
		})
	}
	for _, name := range []string{"traceparent", "Baggage", "user-agent", "X-Semar-Run"} {
		t.Run(name, func(t *testing.T) {
			sc, s := loadPair(t)
			s.Steps[0].Request.Headers = map[string]string{name: "x"}
			ps := script.Validate(sc, s)
			if len(ps) != 1 || !strings.Contains(ps[0].Message, "set by Semar on every request") {
				t.Fatalf("got %+v", ps)
			}
		})
	}
}

func TestValidateNamesTheStep(t *testing.T) {
	sc, s := loadPair(t)
	s.Steps[1].Request.Method = "PUT"
	ps := script.Validate(sc, s)
	if len(ps) != 1 || ps[0].Step != "refund-too-much" {
		t.Fatalf("got %+v", ps)
	}
}

func TestMatchPath(t *testing.T) {
	cases := []struct {
		hint, actual string
		want         bool
	}{
		{"/v1/orders", "/v1/orders", true},
		{"/v1/orders/{id}", "/v1/orders/{{order_id}}", true},
		{"/v1/orders/{id}/refunds", "/v1/orders/81234/refunds", true},
		{"/v1/orders/{id}", "/v1/orders/81234/", true},
		{"/v1/orders/", "/v1/orders", true},
		{"/v1/orders/{id}", "/v1/orders/order-{{n}}", true},
		{"/", "/", true},
		{"/v1/orders/{id}", "/v1/orders", false},
		{"/v1/orders/{id}", "/v1/orders/1/refunds", false},
		{"/v1/orders", "/v1/Orders", false},
		{"/v1/orders/{id}", "/v1/orders/../admin", false},
		{"/v1/orders/{id}", "/v1/orders/..", false},
		{"/v1/orders/{id}", "/v1/orders/.", false},
		{"/v1/orders/{id}", "/v1/orders/%2e%2e", false},
		{"/v1/orders/{id}", "/v1/orders/%2E", false},
		{"/v1/orders/{id}", "/v1/./orders/1", false},
		{"/v1/orders/{id}", "/v1//orders", false},
		{"/v1/orders/{id}", "/v1/orders//", false},
		{"/v1/orders/{id}", "v1/orders/1", false},
		{"/v1/orders/{id}", "/v1/orders/1?x=2", false},
		{"/v1/orders/{id}", "/v1/orders/1#a", false},
		{"/v1/orders/{id}", "/v1/orders/{{a}}/{{b}}", false},
		{"/v1/orders", "/v1/{{x}}", false},
		{"/v1/orders/{id}", "", false},
		{"/", "//", false},
		{"v1/orders", "v1/orders", false},
	}
	for _, c := range cases {
		t.Run(c.hint+" vs "+c.actual, func(t *testing.T) {
			if got := script.MatchPath(c.hint, c.actual); got != c.want {
				t.Fatalf("MatchPath(%q, %q) = %v, want %v", c.hint, c.actual, got, c.want)
			}
		})
	}
}

func TestRender(t *testing.T) {
	base := map[string]string{"order_id": "81234", "amount": "150000", "token": "abc"}
	cases := []struct {
		name    string
		vars    map[string]string
		req     domain.APIRequest
		want    domain.APIRequest
		wantErr string
	}{
		{
			name: "path query header and json",
			vars: base,
			req: domain.APIRequest{
				Service: "orders", Method: "POST", Path: "/v1/orders/{{order_id}}/refunds",
				Query:   map[string]string{"a": "{{order_id}}", "b": "plain"},
				Headers: map[string]string{"X-Token": "Bearer {{token}}"},
				JSON:    map[string]any{"amount": "{{amount}}", "list": []any{"x-{{order_id}}", 1.0, nil}, "nested": map[string]any{"id": "{{order_id}}"}},
			},
			want: domain.APIRequest{
				Service: "orders", Method: "POST", Path: "/v1/orders/81234/refunds",
				Query:   map[string]string{"a": "81234", "b": "plain"},
				Headers: map[string]string{"X-Token": "Bearer abc"},
				JSON:    map[string]any{"amount": "150000", "list": []any{"x-81234", 1.0, nil}, "nested": map[string]any{"id": "81234"}},
			},
		},
		{name: "no variables", vars: base, req: domain.APIRequest{Path: "/v1/orders"}, want: domain.APIRequest{Path: "/v1/orders"}},
		{name: "unknown variable in path", vars: base, req: domain.APIRequest{Path: "/v1/{{nope}}"}, wantErr: "nope"},
		{name: "unknown variable in query", vars: base, req: domain.APIRequest{Path: "/x", Query: map[string]string{"a": "{{nope}}"}}, wantErr: "nope"},
		{name: "unknown variable in header", vars: base, req: domain.APIRequest{Path: "/x", Headers: map[string]string{"a": "{{nope}}"}}, wantErr: "nope"},
		{name: "unknown variable in deep json", vars: base, req: domain.APIRequest{Path: "/x", JSON: map[string]any{"a": []any{map[string]any{"b": "{{nope}}"}}}}, wantErr: "nope"},
		{name: "unclosed variable", vars: base, req: domain.APIRequest{Path: "/v1/{{order_id"}, wantErr: "malformed"},
		{name: "slash in path value", vars: map[string]string{"x": "1/refunds"}, req: domain.APIRequest{Path: "/v1/orders/{{x}}"}, wantErr: "x"},
		{name: "encoded dot from a split percent", vars: map[string]string{"x": "e"}, req: domain.APIRequest{Path: "/v1/orders/%2{{x}}"}, wantErr: "/v1/orders/%2"},
		{name: "encoded slash from a lone percent", vars: map[string]string{"x": "2f"}, req: domain.APIRequest{Path: "/v1/orders/%{{x}}"}, wantErr: "/v1/orders/%"},
		{name: "encoded backslash upper case", vars: map[string]string{"x": "5C"}, req: domain.APIRequest{Path: "/v1/orders/%{{x}}"}, wantErr: "/v1/orders/%"},
		{name: "encoded double dot from percent", vars: map[string]string{"x": "2e"}, req: domain.APIRequest{Path: "/v1/%{{x}}%2e"}, wantErr: "/v1/%"},
		{name: "raw backslash in the template", vars: base, req: domain.APIRequest{Path: `/v1\{{order_id}}`}, wantErr: "holds a backslash"},
		{name: "raw backslash as a whole segment", vars: base, req: domain.APIRequest{Path: `/v1/\/{{order_id}}`}, wantErr: "holds a backslash"},
		{name: "encoded percent then an encoded dot", vars: map[string]string{"x": "2e"}, req: domain.APIRequest{Path: "/v1/%25{{x}}"}, wantErr: "holds an encoded percent sign"},
		{name: "encoded percent alone", vars: base, req: domain.APIRequest{Path: "/v1/100%25/{{order_id}}"}, wantErr: "holds an encoded percent sign"},
		{name: "other escapes are kept", vars: base, req: domain.APIRequest{Path: "/v1/a%20b/{{order_id}}"}, want: domain.APIRequest{Path: "/v1/a%20b/81234"}},
		{name: "dot segment in the template", vars: base, req: domain.APIRequest{Path: "/v1/../{{order_id}}"}, wantErr: "/v1/../"},
		{name: "empty segment in the template", vars: base, req: domain.APIRequest{Path: "/v1//{{order_id}}"}, wantErr: "/v1//"},
		{name: "question mark in the template", vars: base, req: domain.APIRequest{Path: "/v1/x?{{order_id}}"}, wantErr: "/v1/x?"},
		{name: "fragment in the template", vars: base, req: domain.APIRequest{Path: "/v1/x#{{order_id}}"}, wantErr: "/v1/x#"},
		{name: "path without slash", vars: base, req: domain.APIRequest{Path: "v1/{{order_id}}"}, wantErr: "v1/"},
		{name: "trailing slash is kept", vars: base, req: domain.APIRequest{Path: "/v1/{{order_id}}/"}, want: domain.APIRequest{Path: "/v1/81234/"}},
		{name: "value with dots and tilde", vars: map[string]string{"x": "a..b.c_d-e~f"}, req: domain.APIRequest{Path: "/v1/{{x}}"}, want: domain.APIRequest{Path: "/v1/a..b.c_d-e~f"}},
		{name: "header with a tab", vars: map[string]string{"x": "a\tb"}, req: domain.APIRequest{Path: "/x", Headers: map[string]string{"A": "{{x}}"}}, want: domain.APIRequest{Path: "/x", Headers: map[string]string{"A": "a\tb"}}},
		{name: "header with a vertical tab", vars: map[string]string{"x": "a\vb"}, req: domain.APIRequest{Path: "/x", Headers: map[string]string{"A": "{{x}}"}}, wantErr: "x"},
		{name: "header with a nul", vars: map[string]string{"x": "a\x00b"}, req: domain.APIRequest{Path: "/x", Headers: map[string]string{"A": "{{x}}"}}, wantErr: "x"},
		{name: "header with a del", vars: map[string]string{"x": "a\x7fb"}, req: domain.APIRequest{Path: "/x", Headers: map[string]string{"A": "{{x}}"}}, wantErr: "x"},
		{name: "spaced variable", vars: base, req: domain.APIRequest{Path: "/x/{{ order_id }}"}, wantErr: "malformed"},
		{name: "dotted variable with an empty part", vars: base, req: domain.APIRequest{Path: "/x", Query: map[string]string{"a": "{{a..b}}"}}, wantErr: "malformed"},
		{name: "dotted variable ending in a dot", vars: base, req: domain.APIRequest{Path: "/x", Query: map[string]string{"a": "{{a.}}"}}, wantErr: "malformed"},
		{name: "variable starting with underscore", vars: map[string]string{"_a": "1"}, req: domain.APIRequest{Path: "/x/{{_a}}"}, wantErr: "malformed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := script.Render(c.req, c.vars)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("got error %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %#v, want %#v", got, c.want)
			}
		})
	}
}

func TestRenderPathValueIsOneSegment(t *testing.T) {
	for _, val := range []string{"1/refunds", "1?x=2", "1#frag", "..", ".", "", "1%2f2", `1\2`, "{{other}}", "{", "a\nb", "a b", "caf\u00e9", "a+b", "a:b", "a;b", "%", "a%2eb", "e\u0301"} {
		t.Run(val, func(t *testing.T) {
			_, err := script.Render(domain.APIRequest{Path: "/v1/orders/{{id}}"}, map[string]string{"id": val, "other": "x"})
			if err == nil {
				t.Fatalf("value %q was accepted in a path", val)
			}
			if !strings.Contains(err.Error(), "id") {
				t.Fatalf("error %q does not name the variable", err)
			}
		})
	}
}

func TestRenderPathValueErrorDoesNotPrintTheValue(t *testing.T) {
	for _, val := range []string{"secret/value", "secret value", "s3cr3t%2f"} {
		t.Run(val, func(t *testing.T) {
			_, err := script.Render(domain.APIRequest{Path: "/v1/orders/{{id}}"}, map[string]string{"id": val})
			if err == nil || !strings.Contains(err.Error(), "id") || strings.Contains(err.Error(), "secret") {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestRenderValuesOutsideThePathMayHoldSlashes(t *testing.T) {
	got, err := script.Render(domain.APIRequest{
		Path:    "/x",
		Query:   map[string]string{"next": "{{u}}"},
		JSON:    map[string]any{"u": "{{u}}"},
		Headers: map[string]string{"Referer": "{{u}}"},
	}, map[string]string{"u": "/a/b?c#d"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Query["next"] != "/a/b?c#d" || got.JSON.(map[string]any)["u"] != "/a/b?c#d" || got.Headers["Referer"] != "/a/b?c#d" {
		t.Fatalf("got %#v", got)
	}
}

func TestRenderHeaderValueRefusesLineBreak(t *testing.T) {
	_, err := script.Render(domain.APIRequest{Path: "/x", Headers: map[string]string{"A": "{{v}}"}}, map[string]string{"v": "a\r\nB: c"})
	if err == nil || !strings.Contains(err.Error(), "v") {
		t.Fatalf("got %v", err)
	}
}

func TestRenderDoesNotChangeItsInput(t *testing.T) {
	req := domain.APIRequest{
		Path:    "/v1/{{a}}",
		Query:   map[string]string{"q": "{{a}}"},
		Headers: map[string]string{"h": "{{a}}"},
		JSON:    map[string]any{"k": []any{"{{a}}", map[string]any{"n": "{{a}}"}}},
	}
	want := domain.APIRequest{
		Path:    "/v1/{{a}}",
		Query:   map[string]string{"q": "{{a}}"},
		Headers: map[string]string{"h": "{{a}}"},
		JSON:    map[string]any{"k": []any{"{{a}}", map[string]any{"n": "{{a}}"}}},
	}
	if _, err := script.Render(req, map[string]string{"a": "1"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(req, want) {
		t.Fatalf("input changed: %#v", req)
	}
}

func TestRenderValueIsNotRenderedAgain(t *testing.T) {
	got, err := script.Render(domain.APIRequest{Path: "/x", Query: map[string]string{"q": "{{a}}"}}, map[string]string{"a": "{{b}}", "b": "boom"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Query["q"] != "{{b}}" {
		t.Fatalf("got %q", got.Query["q"])
	}
}

func TestRenderDottedVariable(t *testing.T) {
	vars := map[string]string{"order.id": "o-7", "run.id": "r-1"}
	got, err := script.Render(domain.APIRequest{
		Path:  "/v1/orders/{{order.id}}",
		Query: map[string]string{"ref": "semar-{{run.id}}"},
		JSON:  map[string]any{"orderId": "{{order.id}}"},
	}, vars)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "/v1/orders/o-7" || got.Query["ref"] != "semar-r-1" || got.JSON.(map[string]any)["orderId"] != "o-7" {
		t.Fatalf("got %#v", got)
	}

	for _, val := range []string{"a/b", "..", "", "x?y", `a\b`, "s3cr3t/value"} {
		_, err := script.Render(domain.APIRequest{Path: "/v1/orders/{{order.id}}"}, map[string]string{"order.id": val})
		if err == nil {
			t.Fatalf("value %q was accepted in a path", val)
		}
		if !strings.Contains(err.Error(), "order.id") || strings.Contains(err.Error(), "s3cr3t") || (val != "" && strings.Contains(err.Error(), val)) {
			t.Fatalf("error %q must name the variable and not the value %q", err, val)
		}
	}

	if _, err := script.Render(domain.APIRequest{Path: "/v1/{{order.nope}}"}, vars); err == nil || !strings.Contains(err.Error(), "unknown variable order.nope") {
		t.Fatalf("got %v", err)
	}
}

// withGiven gives the valid pair a given entry and a request that reads its output.
func withGiven(t *testing.T) (domain.Scenario, domain.Script) {
	t.Helper()
	sc, s := loadPair(t)
	sc.Given = []domain.Given{{Fixture: "order.paid", As: "order"}}
	s.ExpectHash = domain.ExpectHash(sc)
	s.Steps[0].Captures = nil
	s.Steps[1].Request.Path = "/v1/orders/{{order.id}}/refunds"
	s.Steps[2].Request.Path = "/v1/orders/{{order.id}}"
	return sc, s
}

func messages(ps []script.Problem) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Message)
	}
	return out
}

func TestValidateWithAliasCountsAsCapture(t *testing.T) {
	sc, s := withGiven(t)
	// The first step reads the alias before any step has captured anything.
	s.Steps[0].Request.Path = "/v1/orders"
	s.Steps[0].Request.Query = map[string]string{"ref": "{{order.id}}"}
	for _, outputs := range []map[string][]string{nil, {"order": {"id"}}} {
		if ps := script.ValidateWith(sc, s, outputs); len(ps) != 0 {
			t.Fatalf("outputs %v: got %+v", outputs, ps)
		}
	}
	if ps := script.Validate(sc, s); len(ps) != 0 {
		t.Fatalf("Validate must accept an alias of a declared given entry: %+v", ps)
	}
}

func TestValidateWithOutputs(t *testing.T) {
	cases := []struct {
		name    string
		change  func(*domain.Scenario, *domain.Script)
		outputs map[string][]string
		want    string // substring of one message. Empty means no problem
	}{
		{"known output", func(*domain.Scenario, *domain.Script) {}, map[string][]string{"order": {"id", "total"}}, ""},
		{"unknown output", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[2].Request.Path = "/v1/orders/{{order.total}}"
		}, map[string][]string{"order": {"id"}}, "order.total is not an output of fixture order.paid; its outputs are id"},
		{"unknown output with no outputs", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[2].Request.Path = "/v1/orders/{{order.total}}"
		}, map[string][]string{}, "order.total is not an output of fixture order.paid; it has no known outputs"},
		{"unknown alias", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[2].Request.Path = "/v1/orders/{{customer.id}}"
		}, map[string][]string{"order": {"id"}}, "no given entry has as customer"},
		{"unknown alias with nil outputs", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[2].Request.Path = "/v1/orders/{{customer.id}}"
		}, nil, "no given entry has as customer"},
		{"alias used with no given entries", func(sc *domain.Scenario, s *domain.Script) {
			sc.Given = nil
			s.ExpectHash = domain.ExpectHash(*sc)
		}, nil, "no given entry has as order"},
		{"run.id without a fixture", func(sc *domain.Scenario, s *domain.Script) {
			sc.Given = nil
			s.ExpectHash = domain.ExpectHash(*sc)
			s.Steps[1].Request.Path = "/v1/orders/{{order_id}}/refunds"
			s.Steps[2].Request.Path = "/v1/orders/{{order_id}}"
			s.Steps[0].Captures = []domain.Capture{{Name: "order_id", JSONPath: "$.data.id"}}
			s.Steps[2].Request.Query = map[string]string{"ref": "semar-{{run.id}}"}
		}, nil, ""},
		{"data in a script", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[2].Request.Query = map[string]string{"c": "{{data.customers.default}}"}
		}, nil, "data.customers.default cannot be used in a script"},
		{"secret in a script", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[2].Request.Headers = map[string]string{"X-Key": "{{secret.TOKEN}}"}
		}, nil, "a script cannot use a secret"},
		{"dotted name with a nested part", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[2].Request.Query = map[string]string{"c": "{{order.id.x}}"}
		}, nil, "order.id.x is not an output of fixture order.paid"},
		{"a capture cannot take a dotted name", func(_ *domain.Scenario, s *domain.Script) {
			s.Steps[0].Captures = []domain.Capture{{Name: "order.id", JSONPath: "$.id"}}
		}, nil, "capture name order.id is not a valid variable name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sc, s := withGiven(t)
			c.change(&sc, &s)
			ps := script.ValidateWith(sc, s, c.outputs)
			if c.want == "" {
				if len(ps) != 0 {
					t.Fatalf("want no problem, got %+v", ps)
				}
				return
			}
			for _, m := range messages(ps) {
				if strings.Contains(m, c.want) {
					return
				}
			}
			t.Fatalf("want a problem with %q, got %v", c.want, messages(ps))
		})
	}
}

func TestValidateWithoutGivenIsValidate(t *testing.T) {
	sc, s := loadPair(t)
	s.Steps[0].Captures = nil
	if !reflect.DeepEqual(script.Validate(sc, s), script.ValidateWith(sc, s, nil)) {
		t.Fatal("Validate must equal ValidateWith with nil outputs")
	}
}

func TestRequestProblems(t *testing.T) {
	known := func(name string) bool { return name == "id" || name == "run.id" }
	cases := []struct {
		name string
		req  domain.APIRequest
		want []string // substrings, one per problem, in order. Empty means no problem
	}{
		{"valid", domain.APIRequest{Service: "orders", Method: "GET", Path: "/v1/orders/{{id}}", Query: map[string]string{"ref": "semar-{{run.id}}"}}, nil},
		{"no service", domain.APIRequest{Method: "GET", Path: "/x"}, []string{"the request has no service"}},
		{"unknown method", domain.APIRequest{Service: "s", Method: "get", Path: "/x"}, []string{"method get is not one of"}},
		{"path without slash", domain.APIRequest{Service: "s", Method: "GET", Path: "x"}, []string{"path x must start with /"}},
		{"dot segment", domain.APIRequest{Service: "s", Method: "GET", Path: "/a/../b"}, []string{"holds a dot segment"}},
		{"bad header name", domain.APIRequest{Service: "s", Method: "GET", Path: "/x", Headers: map[string]string{"X A": "1"}}, []string{"not a valid HTTP header name"}},
		{"engine header", domain.APIRequest{Service: "s", Method: "GET", Path: "/x", Headers: map[string]string{"traceparent": "1"}}, []string{"set by Semar on every request"}},
		{"malformed variable", domain.APIRequest{Service: "s", Method: "GET", Path: "/x", Query: map[string]string{"a": "{{a."}}, []string{"query a: malformed variable"}},
		{"unknown variable names its place", domain.APIRequest{Service: "s", Method: "POST", Path: "/x/{{order}}", JSON: map[string]any{"k": []any{"{{nope}}"}}},
			[]string{"path: {{order}} is not a known variable", "json.k[0]: {{nope}} is not a known variable"}},
		{"secret variable", domain.APIRequest{Service: "s", Method: "GET", Path: "/x", Headers: map[string]string{"Authorization": "{{secret.T}}"}}, []string{"header Authorization: {{secret.T}} is not a known variable"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := script.RequestProblems(c.req, known)
			if len(got) != len(c.want) {
				t.Fatalf("got %q, want %d problems %q", got, len(c.want), c.want)
			}
			for i, w := range c.want {
				if !strings.Contains(got[i], w) {
					t.Errorf("problem %d is %q, want %q in it", i, got[i], w)
				}
			}
		})
	}
}

func TestValidateOperandVariables(t *testing.T) {
	cases := []struct {
		name    string
		operand string
		outputs map[string][]string
		want    string // substring of one message. Empty means no problem
	}{
		{"an earlier capture", "{{order_id}}", nil, ""},
		{"an output of a given entry", "{{order.id}}", map[string][]string{"order": {"id"}}, ""},
		{"run.id", "semar-{{run.id}}", nil, ""},
		{"a plain string", "no variables", nil, ""},
		{"an unknown capture", "{{nope}}", nil, "expect[0].check.equals: nope is used before any step captures it"},
		{"an unknown output", "{{order.total}}", map[string][]string{"order": {"id"}}, "order.total is not an output of fixture order.paid"},
		{"an unknown alias", "{{board.id}}", map[string][]string{"order": {"id"}}, "no given entry has as board"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sc, s := withGiven(t)
			sc.Steps[2].Expect[0].Check.Equals = c.operand
			s.ExpectHash = domain.ExpectHash(sc)
			s.Steps[0].Captures = []domain.Capture{{Name: "order_id", JSONPath: "$.data.id"}}
			ps := script.ValidateWith(sc, s, c.outputs)
			if c.want == "" {
				if len(ps) != 0 {
					t.Fatalf("want no problem, got %+v", ps)
				}
				return
			}
			for _, m := range messages(ps) {
				if strings.Contains(m, c.want) {
					return
				}
			}
			t.Fatalf("want a problem with %q, got %v", c.want, messages(ps))
		})
	}
}

func TestValidateShapeWithIgnoresTheExpectationHash(t *testing.T) {
	sc, s := loadPair(t)
	s.ShapeHash = domain.ShapeHash(sc)
	s.ExpectHash = "another"
	if ps := script.ValidateShapeWith(sc, s, nil); len(ps) != 0 {
		t.Errorf("a script of the same shape has problems: %+v", ps)
	}
	s.ShapeHash = "other"
	ps := script.ValidateShapeWith(sc, s, nil)
	if len(ps) != 1 || ps[0].Message != "the script was written for other steps" {
		t.Errorf("problems = %+v", ps)
	}
	s.ShapeHash = domain.ShapeHash(sc)
	s.Steps = append(s.Steps, domain.ScriptStep{Step: "nope"})
	if ps := script.ValidateShapeWith(sc, s, nil); len(ps) == 0 {
		t.Error("a structural problem went unreported")
	}
}
