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
	big := `{"id":9007199254740993,"near":9007199254740992,"same":9007199254740993,"two53":9007199254740992,"ids":[9007199254740993,9007199254740995,9007199254740994]}`
	nums := `{"one":1,"onef":1.0,"onee":1e0,"half":5e-1,"str":"1","huge":123456789012345678901234567890,"hugeSame":123456789012345678901234567890,"hugeNext":123456789012345678901234567891}`
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
		{"contains: redacted on a string that holds a marker", `{jsonpath: "$.t", contains: redacted}`, ok(200, `{"t":"a [redacted:ab12cd34]"}`), "not_evaluated", "the value is redacted", ""},
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
		{"equals null cannot be written", `{jsonpath: "$.error", equals: null}`, ok(200, body), "not_evaluated", "malformed", ""},
		{"big integer equals its neighbour", `{jsonpath: "$.id", equals: 9007199254740992}`, ok(200, big), "false", "", ""},
		{"big integer notEquals its neighbour", `{jsonpath: "$.id", notEquals: 9007199254740992}`, ok(200, big), "true", "", ""},
		{"big integer equals itself", `{jsonpath: "$.id", equals: 9007199254740993}`, ok(200, big), "true", "", ""},
		{"big integer equalsPath its neighbour", `{jsonpath: "$.id", equalsPath: "$.near"}`, ok(200, big), "false", "", ""},
		{"big integer equalsPath itself", `{jsonpath: "$.id", equalsPath: "$.same"}`, ok(200, big), "true", "", ""},
		{"contains in an array of big integers, absent", `{jsonpath: "$.ids", contains: 9007199254740992}`, ok(200, big), "false", "", ""},
		{"contains in an array of big integers, present", `{jsonpath: "$.ids", contains: 9007199254740995}`, ok(200, big), "true", "", ""},
		{"1 equals 1.0", `{jsonpath: "$.one", equals: 1.0}`, ok(200, nums), "true", "", ""},
		{"1.0 in the body equals 1", `{jsonpath: "$.onef", equals: 1}`, ok(200, nums), "true", "", ""},
		{"1e0 in the body equals 1", `{jsonpath: "$.onee", equals: 1}`, ok(200, nums), "true", "", ""},
		{"1e0 in the body equals 1.0", `{jsonpath: "$.onee", equals: 1.0}`, ok(200, nums), "true", "", ""},
		{"equalsPath 1 and 1.0 and 1e0", `{jsonpath: "$.one", equalsPath: "$.onee"}`, ok(200, nums), "true", "", ""},
		{"a fraction equals its exponent form", `{jsonpath: "$.half", equals: 0.5}`, ok(200, nums), "true", "", ""},
		{"30 digits equalsPath itself", `{jsonpath: "$.huge", equalsPath: "$.hugeSame"}`, ok(200, nums), "true", "", ""},
		{"30 digits equalsPath itself plus one", `{jsonpath: "$.huge", equalsPath: "$.hugeNext"}`, ok(200, nums), "false", "", ""},
		{"the string 1 is not the number 1", `{jsonpath: "$.str", equals: 1}`, ok(200, nums), "false", "", ""},
		{"the number 1 is not the string 1", `{jsonpath: "$.one", equals: "1"}`, ok(200, nums), "false", "", ""},
		{"the number 1 notEquals the string 1", `{jsonpath: "$.one", notEquals: "1"}`, ok(200, nums), "true", "", ""},
		{"equalsPath of a string and a number", `{jsonpath: "$.str", equalsPath: "$.one"}`, ok(200, nums), "false", "", ""},
		{"greater beyond 2^53", `{jsonpath: "$.id", greater: 9007199254740992}`, ok(200, big), "true", "", ""},
		{"greater at the bound", `{jsonpath: "$.two53", greater: 9007199254740992}`, ok(200, big), "false", "", ""},
		{"less beyond 2^53", `{jsonpath: "$.id", less: 9007199254740994}`, ok(200, big), "true", "", ""},
		{"less at the bound", `{jsonpath: "$.two53", less: 9007199254740992}`, ok(200, big), "false", "", ""},
		{"less above the bound", `{jsonpath: "$.ids[2]", less: 9007199254740994}`, ok(200, big), "false", "", ""},
		{"greater on a fraction", `{jsonpath: "$.half", greater: 0.4}`, ok(200, nums), "true", "", ""},
		{"greater with a bound that is not finite", `{jsonpath: "$.one", greater: .inf}`, ok(200, nums), "not_evaluated", "cannot be compared", ""},
		{"equals with an infinite expected value", `{jsonpath: "$.one", equals: .inf}`, ok(200, nums), "not_evaluated", "cannot be compared", ""},
		{"a string holding a marker in the middle", `{jsonpath: "$.t", equals: x}`, ok(200, `{"t":"token [redacted:ab12cd34] expired"}`), "not_evaluated", "the value is redacted", ""},
		{"notEquals on a string holding a marker", `{jsonpath: "$.t", notEquals: x}`, ok(200, `{"t":"token [redacted:ab12cd34] expired"}`), "not_evaluated", "the value is redacted", ""},
		{"greater on a string holding a marker", `{jsonpath: "$.t", greater: 1}`, ok(200, `{"t":"token [redacted:ab12cd34] expired"}`), "not_evaluated", "the value is redacted", ""},
		{"less on a string holding a marker", `{jsonpath: "$.t", less: 1}`, ok(200, `{"t":"token [redacted:ab12cd34] expired"}`), "not_evaluated", "the value is redacted", ""},
		{"equals on an array holding a marker", `{jsonpath: "$.a", equals: [x]}`, ok(200, `{"a":["x","[redacted:ab12cd34]"]}`), "not_evaluated", "the value is redacted", ""},
		{"contains on an array holding a marker", `{jsonpath: "$.a", contains: x}`, ok(200, `{"a":["y","a [redacted:ab12cd34]"]}`), "not_evaluated", "the value is redacted", ""},
		{"equals on an object holding a marker deep down", `{jsonpath: "$.a", equals: {b: {c: [1]}}}`, ok(200, `{"a":{"b":{"c":[1,{"d":"[redacted:ab12cd34]"}]}}}`), "not_evaluated", "the value is redacted", ""},
		{"equalsPath where only the first holds a marker", `{jsonpath: "$.t", equalsPath: "$.u"}`, ok(200, `{"t":"x [redacted:ab12cd34]","u":"x"}`), "not_evaluated", "the value is redacted", ""},
		{"equalsPath where only the second holds a marker", `{jsonpath: "$.u", equalsPath: "$.t"}`, ok(200, `{"t":"x [redacted:ab12cd34]","u":"x"}`), "not_evaluated", "the value is redacted", ""},
		{"equalsPath where an object holds a marker", `{jsonpath: "$.o", equalsPath: "$.p"}`, ok(200, `{"o":{"k":"[redacted:ab12cd34]"},"p":{"k":"v"}}`), "not_evaluated", "the value is redacted", ""},
		{"exists on a string holding a marker", `{jsonpath: "$.t", exists: true}`, ok(200, `{"t":"token [redacted:ab12cd34] expired"}`), "true", "", ""},
		{"count of values holding a marker", `{jsonpath: "$.a[*]", count: 2}`, ok(200, `{"a":["a [redacted:ab12cd34]","b"]}`), "true", "", ""},
		{"header holding a marker in the middle", `{header: X-A, equals: x}`, respond(200, "", false, map[string][]string{"X-A": {"a [redacted:ab12cd34] b"}}), "not_evaluated", "the value is redacted", ""},
		{"header contains on a value holding a marker", `{header: X-A, contains: redacted}`, respond(200, "", false, map[string][]string{"X-A": {"a [redacted:ab12cd34] b"}}), "not_evaluated", "the value is redacted", ""},
		{"header contains with a clean value that matches", `{header: X-A, contains: ok}`, respond(200, "", false, map[string][]string{"X-A": {"a [redacted:ab12cd34] b", "ok"}}), "true", "", ""},
		{"header equals with a clean value that does not match", `{header: X-A, equals: x}`, respond(200, "", false, map[string][]string{"X-A": {"[redacted:ab12cd34]", "y"}}), "not_evaluated", "the value is redacted", ""},
		{"header notEquals with a marker and a clean value", `{header: X-A, notEquals: x}`, respond(200, "", false, map[string][]string{"X-A": {"[redacted:ab12cd34]", "y"}}), "not_evaluated", "the value is redacted", ""},
		{"header notEquals with a marker and a matching value", `{header: X-A, notEquals: x}`, respond(200, "", false, map[string][]string{"X-A": {"[redacted:ab12cd34]", "x"}}), "not_evaluated", "the value is redacted", ""},
		{"header notEquals with a marker in the middle", `{header: X-A, notEquals: x}`, respond(200, "", false, map[string][]string{"X-A": {"a [redacted:ab12cd34]"}}), "not_evaluated", "the value is redacted", ""},
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
