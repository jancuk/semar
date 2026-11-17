package openapi_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/openapi"
)

const swaggerJSON = `{
  "swagger": "2.0",
  "basePath": "/v1/",
  "paths": {
    "/orders": {"get": {"responses": {"200": {}, "default": {}}}, "post": {"responses": {"201": {}}}},
    "/orders/{id}": {"get": {"responses": {"200": {}, "404": {}}}, "parameters": [], "x-extra": 1}
  }
}`

const swaggerYAML = `swagger: "2.0"
basePath: /v1/
paths:
  /orders:
    get:
      responses:
        200: {description: ok}
        default: {description: err}
    post:
      responses:
        "201": {description: created}
  /orders/{id}:
    parameters: []
    x-extra: 1
    get:
      responses:
        200: {description: ok}
        404: {description: none}
`

const oas3JSON = `{
  "openapi": "3.0.3",
  "servers": [
    {"url": "https://api.example.com/v1/"},
    {"url": "/v2"},
    {"url": "https://{env}.example.com/{version}"},
    {"url": "https://example.com"},
    {"url": "/"},
    {"url": "/v1"}
  ],
  "paths": {
    "/orders": {"get": {"responses": {"200": {}, "default": {}}}, "post": {"responses": {"201": {}}}},
    "/orders/{id}": {"get": {"responses": {"200": {}, "404": {}}}}
  }
}`

const oas3YAML = `openapi: 3.1.0
servers:
  - url: https://api.example.com/v1/
  - url: /v2
  - url: "https://{env}.example.com/{version}"
  - url: https://example.com
  - url: /
  - url: /v1
paths:
  /orders:
    summary: orders
    get:
      responses:
        200: {description: ok}
        default: {description: err}
    post:
      responses:
        201: {description: created}
  /orders/{id}:
    get:
      responses:
        200: {description: ok}
        404: {description: none}
`

func TestParse(t *testing.T) {
	ops := []openapi.Operation{
		{Method: "GET", Path: "/orders", Statuses: []string{"200", "default"}},
		{Method: "POST", Path: "/orders", Statuses: []string{"201"}},
		{Method: "GET", Path: "/orders/{id}", Statuses: []string{"200", "404"}},
	}
	withLines := func(lines ...int) []openapi.Operation {
		out := make([]openapi.Operation, len(ops))
		copy(out, ops)
		for i, l := range lines {
			out[i].Line = l
		}
		return out
	}
	oasPrefixes := []string{"/v1", "/v2", "/{version}"}
	tests := []struct {
		name string
		in   string
		want openapi.Document
	}{
		{"swagger 2 json", swaggerJSON, openapi.Document{Version: "2.0", Prefixes: []string{"/v1"}, Operations: ops}},
		{"swagger 2 yaml", swaggerYAML, openapi.Document{Version: "2.0", Prefixes: []string{"/v1"}, Operations: withLines(5, 9, 15)}},
		{"openapi 3.0 json", oas3JSON, openapi.Document{Version: "3.0.3", Prefixes: oasPrefixes, Operations: ops}},
		{"openapi 3.1 yaml", oas3YAML, openapi.Document{Version: "3.1.0", Prefixes: oasPrefixes, Operations: withLines(12, 16, 20)}},
		{"openapi 3.1 without paths", "openapi: 3.1.0\n", openapi.Document{Version: "3.1.0"}},
		{"null paths", "openapi: 3.1.0\npaths:\n", openapi.Document{Version: "3.1.0"}},
		{"json with leading space and no paths", "\n  {\"openapi\": \"3.1.0\"}", openapi.Document{Version: "3.1.0"}},
		{"ref path item is skipped", "openapi: 3.0.0\npaths:\n  /a:\n    $ref: other.yaml\n  /b:\n    get: {}\n",
			openapi.Document{Version: "3.0.0", Operations: []openapi.Operation{{Method: "GET", Path: "/b", Line: 6}}}},
		{"ref path item in json", `{"openapi":"3.0.0","paths":{"/a":{"$ref":"x.json"}}}`, openapi.Document{Version: "3.0.0"}},
		{"unknown methods and keys are ignored", "openapi: 3.0.0\npaths:\n  /a:\n    summary: s\n    GET: {}\n    parameters: [{name: id}]\n    trace:\n      responses: {200: {}}\n",
			openapi.Document{Version: "3.0.0", Operations: []openapi.Operation{{Method: "TRACE", Path: "/a", Statuses: []string{"200"}, Line: 7}}}},
		{"alias for responses", "openapi: 3.0.0\npaths:\n  /a:\n    get:\n      responses: &r\n        200: {}\n    put:\n      responses: *r\n",
			openapi.Document{Version: "3.0.0", Operations: []openapi.Operation{
				{Method: "GET", Path: "/a", Statuses: []string{"200"}, Line: 4},
				{Method: "PUT", Path: "/a", Statuses: []string{"200"}, Line: 7}}}},
		{"relative server without a slash is skipped", "openapi: 3.0.0\nservers:\n  - url: v1\n  - url: \"\"\n  - {}\n  - 4\n", openapi.Document{Version: "3.0.0"}},
		{"server with query", "openapi: 3.0.0\nservers:\n  - url: https://example.com/v3?x=1\n", openapi.Document{Version: "3.0.0", Prefixes: []string{"/v3"}}},
		{"basePath root", "swagger: '2.0'\nbasePath: /\n", openapi.Document{Version: "2.0"}},
		{"utf-8 byte order mark", "\xef\xbb\xbfopenapi: 3.0.0\n", openapi.Document{Version: "3.0.0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := openapi.Parse([]byte(tt.in), "docs/openapi")
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			// The index is an unexported detail of a parsed document, and the details of an operation
			// have their own tests.
			for i := range got.Operations {
				op := &got.Operations[i]
				op.Key, op.Params, op.Body, op.Responses = "", nil, nil, nil
			}
			if got.Version != tt.want.Version || !reflect.DeepEqual(got.Prefixes, tt.want.Prefixes) || !reflect.DeepEqual(got.Operations, tt.want.Operations) {
				t.Errorf("Parse =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

func TestParseRefusals(t *testing.T) {
	manyOps := func(n int) string {
		var sb strings.Builder
		sb.WriteString("openapi: 3.0.0\npaths:\n")
		for i := 0; i < n; i++ {
			sb.WriteString("  /p" + strings.Repeat("a", i%7) + string(rune('a'+i%26)) + itoa(i) + ":\n    get: {}\n")
		}
		return sb.String()
	}
	// 200 path items that all alias one responses mapping of 20,000 keys.
	var bomb strings.Builder
	bomb.WriteString("openapi: 3.0.0\npaths:\n  /p0:\n    get:\n      responses: &r\n")
	for i := 0; i < 20000; i++ {
		bomb.WriteString("        c" + itoa(i) + ": {}\n")
	}
	for i := 1; i <= 200; i++ {
		bomb.WriteString("  /p" + itoa(i) + ":\n    get:\n      responses: *r\n")
	}
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "docs/api.yaml"},
		{"yaml too large", strings.Repeat(" ", 2<<20+1), "larger than 2 MiB, the limit for YAML"},
		{"json too large", "{" + strings.Repeat(" ", 5<<20), "larger than 5 MiB, the limit for JSON"},
		{"scalar", "hello", "not a mapping"},
		{"list", "- a\n- b\n", "not a mapping"},
		{"no version key", "info: {}\npaths: {}\n", "\"openapi\" or \"swagger\""},
		{"version is a mapping", "openapi: {a: b}\n", "\"openapi\" or \"swagger\""},
		{"json no version", `{"info": {}}`, "\"openapi\" or \"swagger\""},
		{"json version is a number", `{"openapi": 3}`, "\"openapi\" or \"swagger\""},
		{"json broken", `{"openapi": `, "docs/api.yaml"},
		{"yaml broken", "openapi: [\n", "docs/api.yaml"},
		{"two yaml documents", "openapi: 3.0.0\n---\nopenapi: 3.0.0\n", "more than one"},
		{"paths is a list", "openapi: 3.0.0\npaths: [a]\n", "\"paths\" must be a mapping"},
		{"paths is a string in json", `{"openapi":"3.0.0","paths":"x"}`, "\"paths\" must be a mapping"},
		{"too many operations yaml", manyOps(5001), "more than 5000 operations"},
		{"repeated aliases", bomb.String(), "too large or too repetitive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := openapi.Parse([]byte(tt.in), "docs/api.yaml")
			if err == nil {
				t.Fatal("Parse succeeded, want an error")
			}
			msg := err.Error()
			if !strings.Contains(msg, tt.want) || !strings.HasPrefix(msg, "docs/api.yaml") {
				t.Errorf("error = %q, want it to name the file and contain %q", msg, tt.want)
			}
			if msg != strings.ToLower(msg[:1])+msg[1:] || strings.HasSuffix(msg, ".") {
				t.Errorf("error = %q, want lower case and no final period", msg)
			}
		})
	}

	t.Run("5000 operations are allowed", func(t *testing.T) {
		d, err := openapi.Parse([]byte(manyOps(5000)), "x")
		if err != nil || len(d.Operations) != 5000 {
			t.Fatalf("got %d operations, err %v", len(d.Operations), err)
		}
	})
	t.Run("too many operations json", func(t *testing.T) {
		var sb strings.Builder
		sb.WriteString(`{"openapi":"3.0.0","paths":{`)
		for i := 0; i < 5001; i++ {
			if i > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(`"/p` + itoa(i) + `":{"get":{}}`)
		}
		sb.WriteString("}}")
		if _, err := openapi.Parse([]byte(sb.String()), "x"); err == nil || !strings.Contains(err.Error(), "more than 5000") {
			t.Fatalf("err = %v", err)
		}
	})
}

func itoa(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{digits[i%10]}, b...)
	}
	return string(b)
}

func TestLookup(t *testing.T) {
	d, err := openapi.Parse([]byte(oas3YAML), "x")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, method, route string
		found, known        bool
		methods             []string
	}{
		{"as written", "GET", "/orders", true, true, []string{"GET", "POST"}},
		{"parameter name differs", "GET", "/orders/{orderId}", true, true, []string{"GET"}},
		{"concrete parameter is a different route", "GET", "/orders/42", false, false, nil},
		{"trailing slash on the route", "GET", "/orders/", true, true, []string{"GET", "POST"}},
		{"under a prefix", "POST", "/v1/orders", true, true, []string{"GET", "POST"}},
		{"under another prefix", "GET", "/v2/orders/{x}", true, true, []string{"GET"}},
		{"under a prefix with a variable", "GET", "/{v}/orders", true, true, []string{"GET", "POST"}},
		{"method in lower case", "post", "/orders", true, true, []string{"GET", "POST"}},
		{"route known, method not", "DELETE", "/orders/{id}", false, true, []string{"GET"}},
		{"route unknown", "GET", "/invoices", false, false, nil},
		{"route is case sensitive", "GET", "/Orders", false, false, nil},
		{"unknown prefix", "GET", "/v9/orders", false, false, nil},
		{"empty route", "GET", "", false, false, nil},
		{"root", "GET", "/", false, false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := d.Lookup(tt.method, tt.route)
			if m.Found != tt.found || m.PathKnown != tt.known || !reflect.DeepEqual(m.Methods, tt.methods) {
				t.Errorf("Lookup = %+v, want found=%v known=%v methods=%v", m, tt.found, tt.known, tt.methods)
			}
			if m.Found && !strings.EqualFold(m.Operation.Method, tt.method) {
				t.Errorf("operation = %+v", m.Operation)
			}
		})
	}

	t.Run("root path with a prefix", func(t *testing.T) {
		d, err := openapi.Parse([]byte("openapi: 3.0.0\nservers: [{url: /v1}]\npaths:\n  /:\n    get: {}\n"), "x")
		if err != nil {
			t.Fatal(err)
		}
		for _, route := range []string{"/", "/v1", "/v1/"} {
			if m := d.Lookup("GET", route); !m.Found {
				t.Errorf("route %q not found", route)
			}
		}
	})
	t.Run("zero document", func(t *testing.T) {
		if m := (openapi.Document{}).Lookup("GET", "/"); m.Found || m.PathKnown {
			t.Errorf("Lookup = %+v", m)
		}
	})
}

func TestDeclares(t *testing.T) {
	tests := []struct {
		name     string
		statuses []string
		status   int
		want     bool
	}{
		{"exact", []string{"200", "404"}, 404, true},
		{"missing", []string{"200"}, 404, false},
		{"upper range", []string{"4XX"}, 404, true},
		{"lower range", []string{"4xx"}, 418, true},
		{"other range", []string{"5XX"}, 404, false},
		{"default", []string{"default"}, 500, true},
		{"none", nil, 200, false},
		{"zero is in no range", []string{"0XX"}, 0, false},
		{"out of range code", []string{"9XX"}, 999, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (openapi.Operation{Statuses: tt.statuses}).Declares(tt.status); got != tt.want {
				t.Errorf("Declares(%d) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{
		swaggerJSON, swaggerYAML, oas3JSON, oas3YAML,
		"",
		"{",
		`{"openapi":"3.0.0","paths":{"/a/{b":{"get":{"responses":{"200":{}}}}}}`,
		"openapi: 3.0.0\nservers: [{url: '{a}://{b}/{c}'}]\npaths:\n  /a: &a\n    get: *a\n",
		"openapi: &a [*a]\n",
		"a: &a [*a, *a]\nb: &b [*a, *a]\nopenapi: 3\npaths: *b\n",
	} {
		f.Add([]byte(s), "GET", "/orders/{id}")
	}
	f.Fuzz(func(t *testing.T, data []byte, method, route string) {
		d, err := openapi.Parse(data, "f")
		if err != nil {
			return
		}
		m := d.Lookup(method, route)
		for _, op := range d.Operations {
			_ = op.Declares(200)
		}
		_ = m
	})
}

func TestParseReadsTheOperationIDAndSummary(t *testing.T) {
	docs := map[string]string{
		"json": `{"openapi": "3.0.3", "paths": {"/orders/{id}/refunds": {"post": {"operationId": "refundOrder", "summary": "Refund an order", "responses": {"201": {}}}, "get": {"responses": {"200": {}}}}}}`,
		"yaml": "openapi: 3.0.3\npaths:\n  /orders/{id}/refunds:\n    post:\n      operationId: refundOrder\n      summary: Refund an order\n      responses:\n        \"201\": {description: ok}\n    get:\n      responses:\n        \"200\": {description: ok}\n",
	}
	for name, src := range docs {
		t.Run(name, func(t *testing.T) {
			doc, err := openapi.Parse([]byte(src), name)
			if err != nil {
				t.Fatal(err)
			}
			if len(doc.Operations) != 2 {
				t.Fatalf("operations = %+v", doc.Operations)
			}
			get, post := doc.Operations[0], doc.Operations[1]
			if get.Method != "GET" || get.ID != "" || get.Summary != "" {
				t.Errorf("get = %+v", get)
			}
			if post.Method != "POST" || post.ID != "refundOrder" || post.Summary != "Refund an order" {
				t.Errorf("post = %+v", post)
			}
		})
	}
}
