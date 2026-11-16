package redact_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/theory/jsonpath"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/redact"
)

func responseWith(body string, caps map[string]string) domain.Action {
	return domain.Action{Response: &domain.HTTPResponse{Body: []byte(body)}, Captures: caps}
}

// A capture that holds a value which a configured JSONPath removed from the body
// must show the marker that the body shows. A capture of anything else stays.
func TestCapturesOfRedactedPaths(t *testing.T) {
	m := newRedactor(t, config.Redact{}).Marker
	cases := []struct {
		name  string
		paths []string
		body  string
		caps  map[string]string
		want  map[string]string
	}{
		{"string", []string{"$.data.ssn"}, `{"data":{"ssn":"3201-77"}}`,
			map[string]string{"x": "3201-77"}, map[string]string{"x": m("3201-77")}},
		{"integer", []string{"$.data.limit"}, `{"data":{"limit":1000}}`,
			map[string]string{"x": "1000"}, map[string]string{"x": m("1000")}},
		{"decimal form", []string{"$.data.limit"}, `{"data":{"limit":1000.0}}`,
			map[string]string{"x": "1000.0"}, map[string]string{"x": m("1000.0")}},
		{"capture of the stored form only", []string{"$.data.limit"}, `{"data":{"limit":1000.0}}`,
			map[string]string{"x": "1000"}, map[string]string{"x": "1000"}},
		{"exponent form", []string{"$.n"}, `{"n":1e3}`,
			map[string]string{"x": "1e3"}, map[string]string{"x": m("1e3")}},
		{"boolean", []string{"$.flag"}, `{"flag":true}`,
			map[string]string{"x": "true"}, map[string]string{"x": m("true")}},
		{"object, compact text", []string{"$.a"}, `{"a":{"b":1,"c":"d"}}`,
			map[string]string{"x": `{"b":1,"c":"d"}`}, map[string]string{"x": m(`{"b":1,"c":"d"}`)}},
		{"object, source text with layout", []string{"$.a"}, `{"a": { "b": 1 }}`,
			map[string]string{"x": `{ "b": 1 }`}, map[string]string{"x": m(`{"b":1}`)}},
		{"array", []string{"$.a"}, `{"a":[1,"two"]}`,
			map[string]string{"x": `[1,"two"]`}, map[string]string{"x": m(`[1,"two"]`)}},
		{"scalar inside a redacted object", []string{"$.a"}, `{"a":{"b":{"c":"inner-1","d":7}}}`,
			map[string]string{"x": "inner-1", "y": "7"}, map[string]string{"x": m("inner-1"), "y": m("7")}},
		{"scalar inside a redacted array", []string{"$.a"}, `{"a":["in-0",{"b":"in-1"}]}`,
			map[string]string{"x": "in-0", "y": "in-1"}, map[string]string{"x": m("in-0"), "y": m("in-1")}},
		{"nested path", []string{"$.a.b.c"}, `{"a":{"b":{"c":"deep-value"}}}`,
			map[string]string{"x": "deep-value"}, map[string]string{"x": m("deep-value")}},
		{"wildcard", []string{"$.users[*].ssn"}, `{"users":[{"ssn":"111-1"},{"ssn":"222-2"},{"ssn":333}]}`,
			map[string]string{"a": "111-1", "b": "222-2", "c": "333", "d": "444"},
			map[string]string{"a": m("111-1"), "b": m("222-2"), "c": m("333"), "d": "444"}},
		{"several paths", []string{"$.a", "$.b"}, `{"a":"one-1","b":"two-2","c":"three-3"}`,
			map[string]string{"a": "one-1", "b": "two-2", "c": "three-3"},
			map[string]string{"a": m("one-1"), "b": m("two-2"), "c": "three-3"}},
		{"path not redacted", []string{"$.data.ssn"}, `{"data":{"ssn":"3201","id":"order-9"}}`,
			map[string]string{"id": "order-9"}, map[string]string{"id": "order-9"}},
		{"no path matches", []string{"$.zzz"}, `{"data":{"id":"order-9"}}`,
			map[string]string{"id": "order-9"}, map[string]string{"id": "order-9"}},
		// The same text under a name that no path redacts is masked too. Telling
		// the two apart would need the capture's source path, which the Action
		// does not hold, and a mask on a harmless value costs a check while a
		// leak costs the secret.
		{"coincidence is masked", []string{"$.data.ssn"}, `{"data":{"ssn":"4242","id":"4242"}}`,
			map[string]string{"id": "4242"}, map[string]string{"id": m("4242")}},
		{"no paths configured", nil, `{"data":{"ssn":"3201"}}`,
			map[string]string{"x": "3201"}, map[string]string{"x": "3201"}},
		{"already a marker", []string{"$.a"}, `{"a":"` + m("v") + `"}`,
			map[string]string{"x": m("v")}, map[string]string{"x": m("v")}},
		{"body that is not JSON", []string{"$.a"}, `a=3201`,
			map[string]string{"x": "3201"}, map[string]string{"x": "3201"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRedactor(t, config.Redact{JSONPaths: c.paths})
			out := r.Action(responseWith(c.body, c.caps))
			if !reflect.DeepEqual(out.Captures, c.want) {
				t.Errorf("captures = %v, want %v\nbody = %s", out.Captures, c.want, out.Response.Body)
			}
		})
	}
}

func TestCapturesOfRedactedRequestBody(t *testing.T) {
	r := newRedactor(t, config.Redact{JSONPaths: []string{"$.password"}})
	m := r.Marker
	in := domain.Action{
		Request:  &domain.HTTPRequest{Body: []byte(`{"user":"u1","password":"hunter-22"}`)},
		Response: &domain.HTTPResponse{Body: []byte(`{"id":"u1"}`)},
		Captures: map[string]string{"pw": "hunter-22", "id": "u1"},
	}
	out := r.Action(in)
	if got, want := out.Captures, map[string]string{"pw": m("hunter-22"), "id": "u1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("captures = %v, want %v", got, want)
	}
}

// A body that the redactor replaces as a whole still removed the values at the
// paths, so a capture of one of them is masked.
func TestCapturesWhenBodyIsReplacedWhole(t *testing.T) {
	r := newRedactor(t, config.Redact{JSONPaths: []string{"$.a"}})
	// A repeated key makes the paths unreliable, so the whole body is replaced.
	out := r.Action(responseWith(`{"a":"first-1","a":"last-2"}`, map[string]string{"x": "last-2"}))
	if !redact.IsMarker(string(out.Response.Body[1 : len(out.Response.Body)-1])) {
		t.Fatalf("body = %s, want a whole-body marker", out.Response.Body)
	}
	if got := out.Captures["x"]; got != r.Marker("last-2") {
		t.Errorf("capture = %q, want %q", got, r.Marker("last-2"))
	}
}

// Keys that collide after redaction are joined, and each value is checked on its own.
func TestCapturesJoinedAfterMasking(t *testing.T) {
	r := newRedactor(t, config.Redact{JSONPaths: []string{"$.a"}})
	out := r.Action(responseWith(`{"a":"raw-value"}`, map[string]string{"k": "raw-value", "k\x00": "other"}))
	for k, v := range out.Captures {
		if strings.Contains(v, "raw-value") {
			t.Errorf("capture %q = %q holds the raw value", k, v)
		}
	}
}

func TestCapturesDeterministic(t *testing.T) {
	r := newRedactor(t, config.Redact{JSONPaths: []string{"$.users[*].ssn", "$..n"}})
	in := responseWith(`{"users":[{"ssn":"1-1","n":2},{"ssn":"2-2"}]}`,
		map[string]string{"a": "1-1", "b": "2-2", "c": "2", "d": "free"})
	first := r.Action(in)
	for range 20 {
		if got := r.Action(in); !reflect.DeepEqual(got, first) {
			t.Fatalf("redaction differs between runs:\n%+v\n%+v", first, got)
		}
	}
	if again := r.Action(first); !reflect.DeepEqual(again, first) {
		t.Fatalf("second pass changed the action:\n%+v\n%+v", first, again)
	}
	// The caller's action is not touched: later steps use the raw captures.
	if in.Captures["a"] != "1-1" {
		t.Errorf("input captures changed: %v", in.Captures)
	}
}

// rawForms lists the text a capture stores for each scalar that the paths select.
func rawForms(t *testing.T, body string, paths []string) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(body))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil
	}
	var out []string
	for _, p := range paths {
		path, err := jsonpath.Parse(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range path.Select(doc) {
			switch v := n.(type) {
			case string:
				out = append(out, v)
			case json.Number:
				out = append(out, v.String())
			case bool:
				if v {
					out = append(out, "true")
				} else {
					out = append(out, "false")
				}
			}
		}
	}
	return out
}

func TestNoRedactedValueSurvivesInAction(t *testing.T) {
	paths := [][]string{
		{"$.data.ssn"},
		{"$.list[*]"},
		{"$..ssn"},
		{"$.data.ssn", "$.list[1]", "$.n"},
	}
	bodies := []string{
		`{"data":{"ssn":"RAW-ssn-1"},"list":["RAW-l0","RAW-l1"],"n":1000}`,
		`{"data":{"ssn":"RAW-ssn-1"},"list":["RAW-l0","RAW-l1"],"n":1000.0}`,
		`{"data":{"ssn":"RAW-ssn-1","deep":{"ssn":"RAW-deep"}},"list":[],"n":-0.5e2}`,
	}
	for _, ps := range paths {
		for _, b := range bodies {
			caps := map[string]string{}
			for i, f := range rawForms(t, b, ps) {
				caps[string(rune('a'+i))] = f
			}
			r, err := redact.New(config.Redact{JSONPaths: ps}, secrets, testKey)
			if err != nil {
				t.Fatal(err)
			}
			in := responseWith(b, caps)
			in.Request = &domain.HTTPRequest{Body: []byte(b)}
			out := r.Action(in)
			for k, v := range out.Captures {
				if !redact.IsMarker(v) {
					t.Errorf("paths %v body %s: capture %s = %q, want a marker", ps, b, k, v)
				}
			}
		}
	}
}

// FuzzRedactedValueAbsent places one value at every path that is configured
// and checks that it is nowhere in the serialized Action, captures included.
func FuzzRedactedValueAbsent(f *testing.F) {
	f.Add("ssn-3201", uint8(0))
	f.Add("1000", uint8(1))
	f.Add(`quote " and \ slash`, uint8(2))
	f.Add("{\"b\":1}", uint8(3))
	f.Add("héllo ", uint8(1))
	f.Add("", uint8(2))
	f.Fuzz(func(t *testing.T, v string, set uint8) {
		if !utf8.ValidString(v) || v == "" {
			t.Skip()
		}
		// The prefix makes the value unlike any key, marker, or secret form, so its
		// presence in the output can only be a leak.
		raw := "RAW<" + v + ">"
		enc, _ := json.Marshal(raw)
		body := `{"data":{"ssn":` + string(enc) + `,"deep":{"ssn":` + string(enc) + `}},"list":[` + string(enc) + `,` + string(enc) + `]}`
		pathSets := [][]string{
			{"$.data.ssn", "$.data.deep.ssn", "$.list[0]", "$.list[1]"},
			{"$..ssn", "$.list[*]"},
			{"$..*"},
			{"$.data", "$.list"},
		}
		r, err := redact.New(config.Redact{JSONPaths: pathSets[int(set)%len(pathSets)]}, secrets, testKey)
		if err != nil {
			t.Fatal(err)
		}
		in := domain.Action{
			Request:  &domain.HTTPRequest{Body: []byte(body)},
			Response: &domain.HTTPResponse{Body: []byte(body)},
			Captures: map[string]string{"ssn": raw, "list0": raw},
		}
		out := r.Action(in)
		blob, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range append(texts(out), string(blob)) {
			if strings.Contains(s, raw) || strings.Contains(s, string(enc)) {
				t.Fatalf("value %q survived in %q", raw, s)
			}
		}
		if again := r.Action(out); !reflect.DeepEqual(again, out) {
			t.Fatalf("second pass changed the action")
		}
		if out2 := r.Action(in); !reflect.DeepEqual(out2, out) {
			t.Fatalf("redaction is not deterministic")
		}
	})
}
