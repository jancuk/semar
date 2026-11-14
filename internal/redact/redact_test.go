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

const secretValue = "s3cret-value"

func secrets() []string { return []string{secretValue, "abc"} }

func newRedactor(t *testing.T, cfg config.Redact) *redact.Redactor {
	t.Helper()
	r, err := redact.New(cfg, secrets)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func sampleAction() domain.Action {
	return domain.Action{
		Request: &domain.HTTPRequest{
			URL: "https://api.uat.example.com/v1/x?token=s3cret-value",
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
	if want := "https://api.uat.example.com/v1/x?token=" + redact.Marker(secretValue); out.Request.URL != want {
		t.Errorf("URL = %q, want %q", out.Request.URL, want)
	}
	if loc := out.Response.Headers["Location"][0]; !strings.HasPrefix(loc, "/next?t=[redacted:") {
		t.Errorf("Location = %q", loc)
	}

	var body struct{ Data map[string]string }
	if err := json.Unmarshal(out.Response.Body, &body); err != nil {
		t.Fatalf("response body is not JSON: %v", err)
	}
	if got, want := body.Data["national_id"], redact.Marker("3201"); got != want {
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
	if strings.Contains(string(out.Request.Body), secretValue) {
		t.Errorf("request body leaks: %s", out.Request.Body)
	}
	for k, v := range out.Captures {
		if k == "t" && !redact.IsMarker(v) {
			t.Errorf("capture t = %q", v)
		}
	}
	if out.Captures["plain"] != "x" {
		t.Errorf("capture plain = %q", out.Captures["plain"])
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
	in.Response.Body = []byte(`{"data":{"national_id":"3201","count":42,"obj":{"a":1},"jwt":"eyJhbGciOi.eyJzdWIiOi.c2ln","x":"id-77 Bearer tok123"}}`)
	once := r.Action(in)
	twice := r.Action(once)
	if !reflect.DeepEqual(once, twice) {
		a, _ := json.Marshal(once)
		b, _ := json.Marshal(twice)
		t.Fatalf("second pass changed the action\nonce:  %s\ntwice: %s", a, b)
	}
}

func TestJSONPathValues(t *testing.T) {
	cases := []struct {
		name, body, path string
		want             string
	}{
		{"string", `{"a":"3201"}`, "$.a", `{"a":"` + redact.Marker("3201") + `"}`},
		{"number", `{"a":42}`, "$.a", `{"a":"` + redact.Marker("42") + `"}`},
		{"object", `{"a":{"b":1}}`, "$.a", `{"a":"` + redact.Marker(`{"b":1}`) + `"}`},
		{"null", `{"a":null}`, "$.a", `{"a":"` + redact.Marker("null") + `"}`},
		{"wildcard in array", `{"a":[1,2]}`, "$.a[*]", `{"a":["` + redact.Marker("1") + `","` + redact.Marker("2") + `"]}`},
		{"root", `{"a":1}`, "$", `"` + redact.Marker(`{"a":1}`) + `"`},
		{"no match", `{"a":1}`, "$.zzz", `{"a":1}`},
		{"nested paths", `{"a":{"b":1}}`, "$..*", `{"a":"` + redact.Marker(`{"b":1}`) + `"}`},
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
		want string
	}{
		{"bad jsonpath", config.Redact{JSONPaths: []string{"$.ok", "$[?"}}, `$[?`},
		{"bad pattern", config.Redact{Patterns: []string{`(unclosed`}}, `(unclosed`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := redact.New(c.cfg, secrets)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to name %q", err, c.want)
			}
		})
	}
}

func TestSecretForms(t *testing.T) {
	r, err := redact.New(config.Redact{}, func() []string { return []string{"", "short", "p@ss word/+=1"} })
	if err != nil {
		t.Fatal(err)
	}
	const s = "p@ss word/+=1"
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
	r, err := redact.New(config.Redact{}, func() []string { return []string{secretValue, `","b":"x`} })
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

func TestMarker(t *testing.T) {
	m := redact.Marker("3201")
	if !redact.IsMarker(m) || len(m) != len("[redacted:]")+8 {
		t.Errorf("Marker = %q", m)
	}
	if m != redact.Marker("3201") || m == redact.Marker("3202") {
		t.Error("Marker must be stable and depend on the value")
	}
	for _, s := range []string{"", "x" + m, m + "x", "[redacted:ABCDEF12]", "[redacted:abc]"} {
		if redact.IsMarker(s) {
			t.Errorf("IsMarker(%q) = true", s)
		}
	}
}
