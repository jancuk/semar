package redact_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/redact"
)

const (
	secretValue  = "s3cret-value"
	otherSecret  = "p@ss word/+=1"
	numberSecret = "123456"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

func secrets() []string { return []string{secretValue, "abc"} }

func newRedactor(t *testing.T, cfg config.Redact) *redact.Redactor {
	t.Helper()
	r, err := redact.New(cfg, secrets, testKey)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func sampleAction() domain.Action {
	return domain.Action{
		Request: &domain.HTTPRequest{
			URL:          "https://api.uat.example.com/v1/x?token=s3cret-value",
			PathTemplate: "/v1/{s3cret-value}",
			Headers: map[string][]string{
				"Authorization":       {"Bearer s3cret-value"},
				"Cookie":              {"a=1"},
				"Proxy-Authorization": {"Basic eA=="},
				"X-Api-Key":           {"k"},
				"Accept":              {"application/json"},
			},
			Body: json.RawMessage(`{"password":"s3cret-value","n":1}`),
		},
		Response: &domain.HTTPResponse{
			Headers: map[string][]string{"Set-Cookie": {"sid=1"}, "Location": {"/next?t=s3cret%2Dvalue"}},
			Body:    []byte(`{"data":{"national_id":"3201","note":"abc","jwt":"eyJhbGciOi.eyJzdWIiOi.c2ln","b64":"czNjcmV0LXZhbHVl"}}`),
		},
		Captures: map[string]string{"t": "s3cret-value", "plain": "x"},
		Err:      "dial failed for s3cret-value",
		Refused:  "not allowed: s3cret-value",
		Why:      "sign in with s3cret-value",
	}
}

func TestAction(t *testing.T) {
	r := newRedactor(t, config.Redact{JSONPaths: []string{"$.data.national_id"}, Headers: []string{"X-Api-Key"}})
	in := sampleAction()
	out := r.Action(in)

	if !reflect.DeepEqual(in, sampleAction()) {
		t.Fatalf("input was modified: %+v", in)
	}

	for _, h := range []string{"Authorization", "Cookie", "Proxy-Authorization", "X-Api-Key"} {
		v := out.Request.Headers[h]
		if len(v) != 1 || !redact.IsMarker(v[0]) {
			t.Errorf("request header %s = %q, want a marker", h, v)
		}
	}
	if v := out.Response.Headers["Set-Cookie"]; len(v) != 1 || !redact.IsMarker(v[0]) {
		t.Errorf("Set-Cookie = %q, want a marker", v)
	}
	if got := out.Request.Headers["Accept"]; !reflect.DeepEqual(got, []string{"application/json"}) {
		t.Errorf("Accept = %q, want unchanged", got)
	}
	m := r.Marker(secretValue)
	strs := []struct{ name, got, want string }{
		{"URL", out.Request.URL, "https://api.uat.example.com/v1/x?token=" + m},
		{"PathTemplate", out.Request.PathTemplate, "/v1/{" + m + "}"},
		{"Location", out.Response.Headers["Location"][0], "/next?t=" + m},
		{"Err", out.Err, "dial failed for " + m},
		{"Refused", out.Refused, "not allowed: " + m},
		{"Why", out.Why, "sign in with " + m},
		{"capture t", out.Captures["t"], m},
		{"capture plain", out.Captures["plain"], "x"},
		{"request body", string(out.Request.Body), `{"password":"` + m + `","n":1}`},
	}
	for _, s := range strs {
		t.Run(s.name, func(t *testing.T) {
			if s.got != s.want {
				t.Errorf("got %q, want %q", s.got, s.want)
			}
		})
	}

	var body struct{ Data map[string]string }
	if err := json.Unmarshal(out.Response.Body, &body); err != nil {
		t.Fatalf("response body is not JSON: %v", err)
	}
	if got, want := body.Data["national_id"], r.Marker("3201"); got != want {
		t.Errorf("national_id = %q, want %q", got, want)
	}
	if body.Data["note"] != "abc" {
		t.Errorf("note = %q, want abc", body.Data["note"])
	}
	for _, k := range []string{"jwt", "b64"} {
		if !redact.IsMarker(body.Data[k]) {
			t.Errorf("%s = %q, want a marker", k, body.Data[k])
		}
	}

	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{secretValue, "s3cret%2Dvalue", "czNjcmV0LXZhbHVl"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("%q survived in %s", leak, raw)
		}
	}
}

func TestActionCopiesEverything(t *testing.T) {
	r := newRedactor(t, config.Redact{})
	in := sampleAction()
	out := r.Action(in)

	out.Request.Headers["Accept"][0] = "changed"
	out.Request.Body[0] = 'X'
	out.Response.Body[0] = 'X'
	out.Captures["plain"] = "changed"
	if !reflect.DeepEqual(in, sampleAction()) {
		t.Fatalf("mutating the output changed the input: %+v", in)
	}
}

func TestActionIdempotent(t *testing.T) {
	r := newRedactor(t, config.Redact{
		JSONPaths: []string{"$.data.national_id", "$.data.count", "$.data.obj"},
		Headers:   []string{"X-Api-Key"},
		Patterns:  []string{`id-\d+`},
	})
	in := sampleAction()
	in.Response.Body = []byte(`{"data":{"national_id":"3201","count":42,"obj":{"a":1},"jwt":"eyJhbGciOi.eyJzdWIiOi.c2ln","x":"id-77 Bearer tok123tok123tok123tok"}}`)
	once := r.Action(in)
	twice := r.Action(once)
	if !reflect.DeepEqual(once, twice) {
		a, _ := json.Marshal(once)
		b, _ := json.Marshal(twice)
		t.Fatalf("second pass changed the action\nonce:  %s\ntwice: %s", a, b)
	}
}

// TestOnlySecretSpanChanges covers rulings A and B: only the source bytes that
// encode a secret change, whatever else the string holds.
func TestOnlySecretSpanChanges(t *testing.T) {
	r, err := redact.New(config.Redact{}, func() []string { return []string{secretValue, otherSecret, numberSecret} }, testKey)
	if err != nil {
		t.Fatal(err)
	}
	m, m2 := r.Marker(secretValue), r.Marker(otherSecret)
	q := func(s string) string { return `"` + r.Marker(s) + `"` }
	errField := func(s string) domain.Action { return domain.Action{Err: s} }
	errOf := func(a domain.Action) string { return a.Err }
	location := func(s string) domain.Action {
		return domain.Action{Response: &domain.HTTPResponse{Headers: map[string][]string{"Location": {s}}}}
	}
	locationOf := func(a domain.Action) string { return a.Response.Headers["Location"][0] }
	body := func(s string) domain.Action { return domain.Action{Response: &domain.HTTPResponse{Body: []byte(s)}} }
	bodyOf := func(a domain.Action) string { return string(a.Response.Body) }
	url := func(s string) domain.Action { return domain.Action{Request: &domain.HTTPRequest{URL: s}} }
	urlOf := func(a domain.Action) string { return a.Request.URL }

	cases := []struct {
		name     string
		place    func(string) domain.Action
		get      func(domain.Action) string
		in, want string
	}{
		{"nested query stays encoded", url, urlOf,
			"https://h/?next=https%3A%2F%2Fy%2F%3Fa%3D1%26b%3D2&token=s3cret-value",
			"https://h/?next=https%3A%2F%2Fy%2F%3Fa%3D1%26b%3D2&token=" + m},
		{"stray percent before", errField, errOf, "50% done; t=s3cret%2Dvalue", "50% done; t=" + m},
		{"stray percent after", location, locationOf, "/next?t=s3cret%2Dvalue&d=100%", "/next?t=" + m + "&d=100%"},
		{"html body", body, bodyOf, `<a href="/x?t=s3cret%2Dvalue">100% off</a>`, `<a href="/x?t=` + m + `">100% off</a>`},
		{"json string value", body, bodyOf, `{"a":"s3cret%2dvalue 5%"}`, `{"a":"` + m + ` 5%"}`},
		{"plus as space", errField, errOf, "q=p%40ss+word%2f%2b%3d1", "q=" + m2},
		{"json object key", body, bodyOf, `{"s3cret%2Dvalue":1}`, `{"` + m + `":1}`},
		{"double encoded", errField, errOf, "x=s3cret%252Dvalue&y=%2", "x=" + m + "&y=%2"},
		{"json escape", body, bodyOf, `{"a": "s3cret-value\n", "b" : 1}`, `{"a": "` + m + `\n", "b" : 1}`},
		{"json layout kept", body, bodyOf, "{\n  \"z\": \"s3cret-value\",\n  \"a\": [1, 2]\n}", "{\n  \"z\": \"" + m + "\",\n  \"a\": [1, 2]\n}"},
		{"secret inside a json number", body, bodyOf,
			`{"id":1700000123456,"name":"keep me","items":[1,2,3]}`,
			`{"id":` + q("1700000123456") + `,"name":"keep me","items":[1,2,3]}`},
		{"json number is the secret", body, bodyOf, `{"pin":123456}`, `{"pin":` + q("123456") + `}`},
		{"json number with sign and exponent", body, bodyOf, "[ -123456.5e+3 ,true,null]", "[ " + q("-123456.5e+3") + " ,true,null]"},
		{"secret inside a number outside json", errField, errOf, "id 1700000123456", "id 1700000" + r.Marker(numberSecret)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.get(r.Action(c.place(c.in))); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestPatterns(t *testing.T) {
	r := newRedactor(t, config.Redact{})
	m := r.Marker
	cases := []struct{ name, in, want string }{
		{"english bearer", "the bearer of this card", "the bearer of this card"},
		{"short bearer token", "Bearer tok123", "Bearer tok123"},
		{"bearer token", "Bearer abcdefghij0123456789xyz", m("Bearer abcdefghij0123456789xyz")},
		{"basic credential", "Basic dXNlcjpzM2NyZXQtdmFsdWU=", m("Basic dXNlcjpzM2NyZXQtdmFsdWU=")},
		{"english basic", "basic information about the user", "basic information about the user"},
		{"jwt", "t=eyJhbGciOi.eyJzdWIiOi.c2ln;", "t=" + m("eyJhbGciOi.eyJzdWIiOi.c2ln") + ";"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := r.Action(domain.Action{Request: &domain.HTTPRequest{Headers: map[string][]string{"X-Upstream-Auth": {c.in}}}})
			if got := out.Request.Headers["X-Upstream-Auth"][0]; got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestJSONPathValues(t *testing.T) {
	m := newRedactor(t, config.Redact{}).Marker
	cases := []struct {
		name, body, path string
		want             string
	}{
		{"string", `{"a":"3201"}`, "$.a", `{"a":"` + m("3201") + `"}`},
		{"number", `{"a":42}`, "$.a", `{"a":"` + m("42") + `"}`},
		{"object", `{"a":{"b":1}}`, "$.a", `{"a":"` + m(`{"b":1}`) + `"}`},
		{"null", `{"a":null}`, "$.a", `{"a":"` + m("null") + `"}`},
		{"wildcard in array", `{"a":[1,2]}`, "$.a[*]", `{"a":["` + m("1") + `","` + m("2") + `"]}`},
		{"root", `{"a":1}`, "$", `"` + m(`{"a":1}`) + `"`},
		{"no match", `{"a":1}`, "$.zzz", `{"a":1}`},
		{"nested paths", `{"a":{"b":1}}`, "$..*", `{"a":"` + m(`{"b":1}`) + `"}`},
		{"layout kept", "{ \"a\" : 42 ,\n \"b\": 1 }", "$.a", "{ \"a\" : \"" + m("42") + "\" ,\n \"b\": 1 }"},
		{"escaped key", `{"name":"x"}`, "$.name", `{"name":"` + m("x") + `"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRedactor(t, config.Redact{JSONPaths: []string{c.path}})
			out := r.Action(domain.Action{Response: &domain.HTTPResponse{Body: []byte(c.body)}})
			if got := string(out.Response.Body); got != c.want {
				t.Errorf("body = %s, want %s", got, c.want)
			}
		})
	}
}

func TestNewErrors(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.Redact
		key  []byte
		want string
	}{
		{"bad jsonpath", config.Redact{JSONPaths: []string{"$.ok", "$[?"}}, testKey, `$[?`},
		{"bad pattern", config.Redact{Patterns: []string{`(unclosed`}}, testKey, `(unclosed`},
		{"short key", config.Redact{}, testKey[:15], "key"},
		{"no key", config.Redact{}, nil, "key"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := redact.New(c.cfg, secrets, c.key)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to name %q", err, c.want)
			}
		})
	}
}

func TestSecretForms(t *testing.T) {
	r, err := redact.New(config.Redact{}, func() []string { return []string{"", "short", otherSecret} }, testKey)
	if err != nil {
		t.Fatal(err)
	}
	const s = otherSecret
	cases := []struct{ name, in string }{
		{"as is", "x" + s + "y"},
		{"query escaped", "?q=" + "p%40ss+word%2F%2B%3D1"},
		{"path escaped", "/p%40ss%20word%2F+=1"},
		{"lowercase percent", "?q=p%40ss%20word%2f%2b%3d1"},
		{"std base64", "cEBzcyB3b3JkLys9MQ=="},
		{"std base64 unpadded", "cEBzcyB3b3JkLys9MQ"},
		{"url base64", "cEBzcyB3b3JkLys9MQ=="},
		{"double encoded", "p%2540ss%2520word%252F%252B%253D1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := r.Action(domain.Action{Err: c.in})
			if !strings.Contains(out.Err, "[redacted:") {
				t.Errorf("Err = %q, want a marker", out.Err)
			}
			for _, leak := range []string{s, "cEBzcyB3b3JkLys9MQ", "p%40ss"} {
				if strings.Contains(out.Err, leak) {
					t.Errorf("%q survived in %q", leak, out.Err)
				}
			}
		})
	}
	if out := r.Action(domain.Action{Err: "short and empty stay"}); out.Err != "short and empty stay" {
		t.Errorf("short secret was searched: %q", out.Err)
	}
}

func TestJSONBodiesStayValid(t *testing.T) {
	cases := []struct{ name, body string }{
		{"secret in value", `{"a":"x s3cret-value y"}`},
		{"secret in key", `{"s3cret-value":1}`},
		{"secret spans a quote", `{"a":"v","b":"x"}`},
		{"escaped quote in secret", `{"a":"q\"s3cret-value"}`},
		{"top level string", `"s3cret-value"`},
		{"array", `["s3cret-value",1,null]`},
		{"json escaped secret", `{"a":"s3cret-value"}`},
	}
	r, err := redact.New(config.Redact{}, func() []string { return []string{secretValue, `","b":"x`} }, testKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := r.Action(domain.Action{Response: &domain.HTTPResponse{Body: []byte(c.body)}})
			if !json.Valid(out.Response.Body) {
				t.Fatalf("body is no longer JSON: %s", out.Response.Body)
			}
			if strings.Contains(string(out.Response.Body), secretValue) || strings.Contains(string(out.Response.Body), `","b":"x`) {
				t.Errorf("secret survived: %s", out.Response.Body)
			}
		})
	}
}

// TestWholeBodyFallback lists every known case in which a JSON body is replaced
// by one marker, and shows that ordinary bodies are edited in place instead.
func TestWholeBodyFallback(t *testing.T) {
	const crossing, escapeShaped = `1,"abc`, "nxxxxx"
	r0, err := redact.New(config.Redact{}, func() []string { return nil }, testKey)
	if err != nil {
		t.Fatal(err)
	}
	m, q := r0.Marker, func(s string) string { return `"` + r0.Marker(s) + `"` }
	cases := []struct {
		name, body, path string
		whole            bool
		want             string
	}{
		{name: "duplicate key with a JSONPath", body: `{"data":{"national_id":"3201"},"data":{}}`, path: "$.data.national_id", whole: true},
		{name: "duplicate key deep inside, with a JSONPath", body: `{"a":[{"k":1,"k":2}]}`, path: "$.zzz", whole: true},
		{name: "keys that become duplicates, with a JSONPath", body: `{"s3cret-value":1,"s3cret%2Dvalue":2}`, path: "$.zzz", whole: true},
		{name: "secret crosses the JSON structure", body: `[1,"abc"]`, whole: true},
		{name: "secret only in raw escape text", body: `{"a":"\nxxxxx"}`, whole: true},

		{name: "duplicate key without a JSONPath", body: `{"k":"s3cret-value","k":2}`, want: `{"k":"` + m(secretValue) + `","k":2}`},
		{name: "secret in a string", body: `{"a":"x s3cret-value y","n":1}`, want: `{"a":"x ` + m(secretValue) + ` y","n":1}`},
		{name: "secret in a key", body: `{"s3cret-value":{"b":true}}`, want: `{"` + m(secretValue) + `":{"b":true}}`},
		{name: "secret in a number", body: `{"id":1700000123456,"name":"keep me","items":[1,2,3]}`, want: `{"id":` + q("1700000123456") + `,"name":"keep me","items":[1,2,3]}`},
		{name: "secret in a nested array", body: `{"a":[[1,"s3cret-value"],[123456,null]]}`, want: `{"a":[[1,"` + m(secretValue) + `"],[` + q(numberSecret) + `,null]]}`},
		{name: "number at a JSONPath that holds a secret", body: `{"pin":123456,"n":7}`, path: "$.pin", want: `{"pin":` + q(numberSecret) + `,"n":7}`},
		{name: "distinct keys with a JSONPath", body: `{"data":{"national_id":"3201","note":"s3cret-value"}}`, path: "$.data.national_id", want: `{"data":{"national_id":` + q("3201") + `,"note":"` + m(secretValue) + `"}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := config.Redact{}
			if c.path != "" {
				cfg.JSONPaths = []string{c.path}
			}
			r, err := redact.New(cfg, func() []string { return []string{secretValue, numberSecret, crossing, escapeShaped} }, testKey)
			if err != nil {
				t.Fatal(err)
			}
			whole := q(c.body)
			got := string(r.Action(domain.Action{Response: &domain.HTTPResponse{Body: []byte(c.body)}}).Response.Body)
			switch {
			case c.whole && got != whole:
				t.Errorf("got %s, want the whole body replaced: %s", got, whole)
			case !c.whole && got == whole:
				t.Errorf("the whole body was replaced: %s", got)
			case !c.whole && got != c.want:
				t.Errorf("got  %s\nwant %s", got, c.want)
			}
		})
	}
}

func TestNamesRedacted(t *testing.T) {
	r := newRedactor(t, config.Redact{})
	m := r.Marker(secretValue)
	cases := []struct {
		name string
		in   domain.Action
		want domain.Action
	}{
		{
			name: "request header name",
			in:   domain.Action{Request: &domain.HTTPRequest{Headers: map[string][]string{"X-s3cret-value": {"v"}, "Accept": {"a"}}}},
			want: domain.Action{Request: &domain.HTTPRequest{Headers: map[string][]string{"X-" + m: {"v"}, "Accept": {"a"}}}},
		},
		{
			name: "response header name, encoded",
			in:   domain.Action{Response: &domain.HTTPResponse{Headers: map[string][]string{"X-s3cret%2Dvalue-Id": {"1"}}}},
			want: domain.Action{Response: &domain.HTTPResponse{Headers: map[string][]string{"X-" + m + "-Id": {"1"}}}},
		},
		{
			name: "header names that collide keep every value",
			in: domain.Action{Request: &domain.HTTPRequest{Headers: map[string][]string{
				"X-s3cret-value":   {"b", "c"},
				"X-s3cret%2Dvalue": {"a"},
				"X-" + m:           {"d s3cret-value"},
			}}},
			want: domain.Action{Request: &domain.HTTPRequest{Headers: map[string][]string{"X-" + m: {"d " + m, "a", "b", "c"}}}},
		},
		{
			name: "empty header values stay empty, not nil",
			in:   domain.Action{Request: &domain.HTTPRequest{Headers: map[string][]string{"X-s3cret-value": {}}}},
			want: domain.Action{Request: &domain.HTTPRequest{Headers: map[string][]string{"X-" + m: {}}}},
		},
		{
			name: "capture key",
			in:   domain.Action{Captures: map[string]string{"tok-s3cret-value": "v", "plain": "x"}},
			want: domain.Action{Captures: map[string]string{"tok-" + m: "v", "plain": "x"}},
		},
		{
			name: "capture keys that collide keep every value",
			in:   domain.Action{Captures: map[string]string{"s3cret-value": "b", "s3cret%2Dvalue": "a"}},
			want: domain.Action{Captures: map[string]string{m: "a, b"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := r.Action(c.in)
			if !reflect.DeepEqual(out, c.want) {
				a, _ := json.Marshal(out)
				b, _ := json.Marshal(c.want)
				t.Errorf("got  %s\nwant %s", a, b)
			}
			if again := r.Action(out); !reflect.DeepEqual(again, out) {
				t.Errorf("second pass changed the action")
			}
		})
	}
}

func TestMarker(t *testing.T) {
	r := newRedactor(t, config.Redact{})
	otherKey := []byte("fedcba9876543210fedcba9876543210")
	other, err := redact.New(config.Redact{}, secrets, otherKey)
	if err != nil {
		t.Fatal(err)
	}
	m := r.Marker("3201")
	cases := []struct {
		name string
		ok   bool
	}{
		{"has the marker format", redact.IsMarker(m) && len(m) == len("[redacted:]")+8},
		{"same redactor, same value, same marker", m == r.Marker("3201")},
		{"depends on the value", m != r.Marker("3202")},
		{"depends on the key", m != other.Marker("3201")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("marker %q", m)
			}
		})
	}
	for _, s := range []string{"", "x" + m, m + "x", "[redacted:ABCDEF12]", "[redacted:abc]"} {
		t.Run("not a marker "+s, func(t *testing.T) {
			if redact.IsMarker(s) {
				t.Errorf("IsMarker(%q) = true", s)
			}
		})
	}
}
