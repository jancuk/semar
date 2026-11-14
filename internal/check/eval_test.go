package check_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/yamlfile"
)

func respond(status int, body string, truncated bool, headers map[string][]string) domain.Action {
	return domain.Action{Response: &domain.HTTPResponse{
		Status:    status,
		Headers:   headers,
		Body:      domain.Body(body),
		Truncated: truncated,
	}}
}

func ok(status int, body string) domain.Action {
	return respond(status, body, false, map[string][]string{"Content-Type": {"application/json"}})
}

func cut(status int, body string) domain.Action { return respond(status, body, true, nil) }

func refused(why string) domain.Action { return domain.Action{Refused: why} }

func failed(err string) domain.Action { return domain.Action{Err: err} }

func yamlCheck(t *testing.T, src string) domain.Check {
	t.Helper()
	var c domain.Check
	if err := yamlfile.Decode([]byte(src), "check.yaml", &c); err != nil {
		t.Fatalf("decode check %q: %v", src, err)
	}
	return c
}

func TestEval(t *testing.T) {
	body := `{"data":{"id":"o-1","refunded_total":0,"total":100000,"items":[{"q":1},{"q":2}],"tags":["a","b"],"note":"paid in full"},"error":null}`
	deep := `{"a":{"b":{"c":{"d":[{"k":"x","v":1},{"k":"y","v":2},{"k":"z","v":3}]}}}}`
	cases := []struct {
		name     string
		check    string
		act      domain.Action
		want     domain.CheckState
		detail   string
		expected string
	}{
		{"status equal", "status: 200", ok(200, body), "true", "", "status 200"},
		{"status differs", "status: 422", ok(200, body), "false", "", "status 422"},
		{"equals a number, 0 and 0.0", `{jsonpath: "$.data.refunded_total", equals: 0.0}`, ok(200, body), "true", "", ""},
		{"equals an integer against a float", `{jsonpath: "$.data.total", equals: 100000}`, ok(200, body), "true", "", ""},
		{"equals a string", `{jsonpath: "$.data.id", equals: o-1}`, ok(200, body), "true", "", "$.data.id equals o-1"},
		{"equals differs", `{jsonpath: "$.data.id", equals: o-9}`, ok(200, body), "false", "", ""},
		{"exists on a null value", `{jsonpath: "$.error", exists: true}`, ok(200, body), "true", "", ""},
		{"notEquals", `{jsonpath: "$.data.id", notEquals: o-2}`, ok(200, body), "true", "", ""},
		{"notEquals on equal", `{jsonpath: "$.data.id", notEquals: o-1}`, ok(200, body), "false", "", ""},
		{"equals an object", `{jsonpath: "$.data.items[0]", equals: {q: 1}}`, ok(200, body), "true", "", ""},
		{"contains in a string", `{jsonpath: "$.data.note", contains: paid}`, ok(200, body), "true", "", ""},
		{"contains a non-string in a string", `{jsonpath: "$.data.note", contains: 5}`, ok(200, body), "false", "needs a string", ""},
		{"contains in an array", `{jsonpath: "$.data.tags", contains: b}`, ok(200, body), "true", "", ""},
		{"contains missing from an array", `{jsonpath: "$.data.tags", contains: c}`, ok(200, body), "false", "", ""},
		{"contains on a number", `{jsonpath: "$.data.total", contains: 1}`, ok(200, body), "false", "needs a string or an array", ""},
		{"exists true", `{jsonpath: "$.data.id", exists: true}`, ok(200, body), "true", "", ""},
		{"exists true on a missing path", `{jsonpath: "$.data.nope", exists: true}`, ok(200, body), "false", "", ""},
		{"exists false on a missing path", `{jsonpath: "$.data.nope", exists: false}`, ok(200, body), "true", "", ""},
		{"equals on a missing path", `{jsonpath: "$.data.nope", equals: 1}`, ok(200, body), "false", "no value at $.data.nope", ""},
		{"equals on several values", `{jsonpath: "$.data.items[*].q", equals: 1}`, ok(200, body), "false", "2 values at $.data.items[*].q", ""},
		{"greater", `{jsonpath: "$.data.total", greater: 99999}`, ok(200, body), "true", "", ""},
		{"greater on a string", `{jsonpath: "$.data.id", greater: 1}`, ok(200, body), "false", "needs a number", ""},
		{"less", `{jsonpath: "$.data.total", less: 100000}`, ok(200, body), "false", "", ""},
		{"count", `{jsonpath: "$.data.items[*]", count: 2}`, ok(200, body), "true", "", ""},
		{"count differs", `{jsonpath: "$.data.items[*]", count: 3}`, ok(200, body), "false", "", ""},
		{"count of none", `{jsonpath: "$.data.nope", count: 0}`, ok(200, body), "true", "", ""},
		{"equalsPath differs", `{jsonpath: "$.data.total", equalsPath: "$.data.refunded_total"}`, ok(200, body), "false", "", ""},
		{"equalsPath equal", `{jsonpath: "$.data.items[0].q", equalsPath: "$.data.items[0].q"}`, ok(200, body), "true", "", ""},
		{"equalsPath with a missing second path", `{jsonpath: "$.data.total", equalsPath: "$.data.nope"}`, ok(200, body), "false", "no value at $.data.nope", ""},
		{"equalsPath with several values at the second path", `{jsonpath: "$.data.total", equalsPath: "$.data.items[*].q"}`, ok(200, body), "false", "2 values at $.data.items[*].q", ""},
		{"equalsPath with an invalid second path", `{jsonpath: "$.data.total", equalsPath: "$["}`, ok(200, body), "not_evaluated", "invalid JSONPath", ""},
		{"deeply nested with a filter", `{jsonpath: "$.a.b.c.d[?@.v > 1 && @.k != 'z'].k", equals: y}`, ok(200, deep), "true", "", ""},
		{"deeply nested filter selecting several", `{jsonpath: "$.a.b.c.d[?@.v > 1].k", count: 2}`, ok(200, deep), "true", "", ""},
		{"jsonpath that does not parse", `{jsonpath: "$..[", equals: 1}`, ok(200, body), "not_evaluated", "invalid JSONPath", ""},
		{"header", `{header: Content-Type, equals: application/json}`, ok(200, body), "true", "", "header Content-Type equals application/json"},
		{"header in another case", `{header: content-TYPE, equals: application/json}`, ok(200, body), "true", "", ""},
		{"header differs", `{header: Content-Type, equals: text/plain}`, ok(200, body), "false", "", ""},
		{"header exists", `{header: Content-Type, exists: true}`, ok(200, body), "true", "", ""},
		{"header exists on a missing header", `{header: X-Nope, exists: true}`, ok(200, body), "false", "", ""},
		{"header does not exist", `{header: X-Nope, exists: false}`, ok(200, body), "true", "", ""},
		{"header equals on a missing header", `{header: X-Nope, equals: a}`, ok(200, body), "false", "no header X-Nope", ""},
		{"header contains", `{header: Content-Type, contains: json}`, ok(200, body), "true", "", ""},
		{"header notEquals", `{header: Content-Type, notEquals: text/plain}`, ok(200, body), "true", "", ""},
		{"header with two values, one equal", `{header: Set-Cookie, equals: b=2}`, respond(200, "", false, map[string][]string{"Set-Cookie": {"a=1", "b=2"}}), "true", "", ""},
		{"header with two values, none equal", `{header: Set-Cookie, equals: c=3}`, respond(200, "", false, map[string][]string{"Set-Cookie": {"a=1", "b=2"}}), "false", "", ""},
		{"header value is redacted", `{header: Authorization, equals: x}`, respond(200, "", false, map[string][]string{"Authorization": {"[redacted:ab12cd34]"}}), "not_evaluated", "the value is redacted", ""},
		{"header with a redacted and a matching value", `{header: X-A, equals: x}`, respond(200, "", false, map[string][]string{"X-A": {"[redacted:ab12cd34]", "x"}}), "true", "", ""},
		{"header greater is not supported", `{header: Content-Length, greater: 1}`, respond(200, "", false, map[string][]string{"Content-Length": {"5"}}), "not_evaluated", "not supported on a header", ""},
		{"body is not JSON", `{jsonpath: "$.a", equals: 1}`, ok(200, "<html>"), "not_evaluated", "the response body is not JSON", ""},
		{"body is empty", `{jsonpath: "$.a", exists: false}`, ok(204, ""), "not_evaluated", "the response body is not JSON", ""},
		{"body was cut", `{jsonpath: "$.a", equals: 1}`, cut(200, `{"a":`), "not_evaluated", "the response body was cut at the size limit", ""},
		{"body was cut but parses", `{jsonpath: "$.a", equals: 1}`, cut(200, `{"a":1}`), "not_evaluated", "the response body was cut at the size limit", ""},
		{"value is redacted", `{jsonpath: "$.t", equals: x}`, ok(200, `{"t":"[redacted:ab12cd34]"}`), "not_evaluated", "the value is redacted", ""},
		{"value only contains a marker", `{jsonpath: "$.t", contains: redacted}`, ok(200, `{"t":"a [redacted:ab12cd34]"}`), "true", "", ""},
		{"redacted value still exists", `{jsonpath: "$.t", exists: true}`, ok(200, `{"t":"[redacted:ab12cd34]"}`), "true", "", ""},
		{"equalsPath on a redacted value", `{jsonpath: "$.t", equalsPath: "$.u"}`, ok(200, `{"t":"[redacted:ab12cd34]","u":"[redacted:ab12cd34]"}`), "not_evaluated", "the value is redacted", ""},
		{"request was refused", "status: 200", refused("read_only"), "not_evaluated", "no response was recorded", ""},
		{"request failed", "status: 200", failed("connection reset"), "not_evaluated", "no response was recorded", ""},
		{"request refused, jsonpath check", `{jsonpath: "$.a", exists: true}`, refused("read_only"), "not_evaluated", "no response was recorded", ""},
		{"request failed, header check", `{header: X, exists: true}`, failed("reset"), "not_evaluated", "no response was recorded", ""},
		{"check names nothing", "{}", ok(200, body), "not_evaluated", "malformed", ""},
		{"check names two kinds", `{status: 200, header: X, exists: true}`, ok(200, body), "not_evaluated", "malformed", ""},
		{"jsonpath without an operator", `{jsonpath: "$.a"}`, ok(200, body), "not_evaluated", "malformed", ""},
		{"jsonpath with two operators", `{jsonpath: "$.data.id", equals: o-1, exists: true}`, ok(200, body), "not_evaluated", "malformed", ""},
		{"status with an operator", `{status: 200, equals: 1}`, ok(200, body), "not_evaluated", "malformed", ""},
		{"header without an operator", `{header: X}`, ok(200, body), "not_evaluated", "malformed", ""},
		{"expected value that JSON cannot hold", `{jsonpath: "$.data.id", equals: .nan}`, ok(200, body), "not_evaluated", "cannot be compared", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := yamlCheck(t, tc.check)
			got := check.Eval(c, tc.act)
			if got.State != tc.want {
				t.Fatalf("state = %q, want %q (detail %q)", got.State, tc.want, got.Detail)
			}
			if !strings.Contains(got.Detail, tc.detail) {
				t.Errorf("detail = %q, want it to contain %q", got.Detail, tc.detail)
			}
			if tc.detail == "" && got.Detail != "" && tc.want != "false" {
				t.Errorf("unexpected detail %q", got.Detail)
			}
			if tc.expected != "" && got.Expected != tc.expected {
				t.Errorf("expected = %q, want %q", got.Expected, tc.expected)
			}
		})
	}
}

func TestEvalTextForms(t *testing.T) {
	body := `{"error":{"code":"REFUND_EXCEEDS_TOTAL"},"long":"` + strings.Repeat("é", 500) + `"}`
	t.Run("expected and observed of a JSONPath equals", func(t *testing.T) {
		got := check.Eval(yamlCheck(t, `{jsonpath: "$.error.code", equals: REFUND_EXCEEDS_TOTAL}`), ok(422, body))
		if got.Expected != "$.error.code equals REFUND_EXCEEDS_TOTAL" || got.Observed != "REFUND_EXCEEDS_TOTAL" {
			t.Errorf("got expected %q, observed %q", got.Expected, got.Observed)
		}
	})
	t.Run("observed status", func(t *testing.T) {
		got := check.Eval(yamlCheck(t, "status: 422"), ok(200, body))
		if got.Observed != "status 200" {
			t.Errorf("observed = %q", got.Observed)
		}
	})
	t.Run("observed is cut at 200 characters", func(t *testing.T) {
		got := check.Eval(yamlCheck(t, `{jsonpath: "$.long", equals: x}`), ok(200, body))
		if n := len([]rune(got.Observed)); n != 200 || !strings.HasSuffix(got.Observed, "...") {
			t.Errorf("observed has %d characters and ends %q", n, got.Observed[len(got.Observed)-6:])
		}
	})
}

func TestEvalDoesNotChangeItsArguments(t *testing.T) {
	c := yamlCheck(t, `{jsonpath: "$.a", equals: {k: [1, 2]}}`)
	a := ok(200, `{"a":{"k":[1,2]}}`)
	before := string(a.Response.Body)
	for range 2 {
		if got := check.Eval(c, a); got.State != domain.CheckTrue {
			t.Fatalf("state = %q", got.State)
		}
	}
	if string(a.Response.Body) != before {
		t.Errorf("body changed to %q", a.Response.Body)
	}
}

func TestInvariants(t *testing.T) {
	expect := func(status int) domain.Step {
		return domain.Step{Expect: []domain.Expectation{{Check: domain.Check{Status: &status}}}}
	}
	cases := []struct {
		name string
		step domain.Step
		act  domain.Action
		want domain.CheckState
	}{
		{"500 with no expected 5xx", domain.Step{}, ok(500, ""), "false"},
		{"500 on a step that expects 500", expect(500), ok(500, ""), "true"},
		{"503 on a step that expects 500", expect(500), ok(503, ""), "false"},
		{"500 on a step that expects 422", expect(422), ok(500, ""), "false"},
		{"422", domain.Step{}, ok(422, ""), "true"},
		{"499", domain.Step{}, ok(499, ""), "true"},
		{"refused", domain.Step{}, refused("read_only"), "not_evaluated"},
		{"transport error", domain.Step{}, failed("reset"), "not_evaluated"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := check.Invariants(tc.step, tc.act)
			if len(got) != 1 {
				t.Fatalf("got %d results, want 1", len(got))
			}
			if got[0].State != tc.want || got[0].Expected != "no server error" {
				t.Errorf("got state %q expected %q, want state %q", got[0].State, got[0].Expected, tc.want)
			}
		})
	}
}

func FuzzEval(f *testing.F) {
	f.Add("$.a", "1", `{"a":1}`, uint8(0))
	f.Add("$..[?@.v > 1]", `"x"`, `{"a":[{"v":2}]}`, uint8(3))
	f.Add("$[", "null", `[`, uint8(7))
	f.Add("$.a", `[1]`, `{"a":"[redacted:ab12cd34]"}`, uint8(1))
	f.Fuzz(func(t *testing.T, path, value, body string, sel uint8) {
		var v any = value
		if err := json.Unmarshal([]byte(value), &v); err != nil {
			v = value
		}
		f64 := float64(len(value))
		n := len(path)
		c := domain.Check{JSONPath: path}
		switch sel % 8 {
		case 0:
			c.Equals = v
		case 1:
			c.NotEquals = v
		case 2:
			c.Contains = v
		case 3:
			e := len(value)%2 == 0
			c.Exists = &e
		case 4:
			c.Greater = &f64
		case 5:
			c.Less = &f64
		case 6:
			c.Count = &n
		case 7:
			c.EqualsPath = value
		}
		got := check.Eval(c, ok(200, body))
		switch got.State {
		case domain.CheckTrue, domain.CheckFalse, domain.CheckNotEvaluated:
		default:
			t.Fatalf("state %q", got.State)
		}
	})
}
