package mcp

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestDecodeArgs(t *testing.T) {
	type A struct {
		Text string `json:"text"`
		N    int    `json:"n"`
		On   bool   `json:"on"`
	}
	for _, c := range []struct {
		name, in string
		want     A
		problem  string // substring of the error, "" for none
	}{
		{"ok", `{"text":"a","n":3,"on":true}`, A{"a", 3, true}, ""},
		{"empty", ``, A{}, ""},
		{"null", `null`, A{}, ""},
		{"empty object", `{}`, A{}, ""},
		{"unknown key", `{"text":"a","extra":1}`, A{}, `unknown argument "extra"`},
		{"wrong type", `{"n":"x"}`, A{}, `the argument "n" must be an integer, not string`},
		{"wrong type bool", `{"on":"yes"}`, A{}, `the argument "on" must be a boolean`},
		{"not an object", `[1]`, A{}, "one JSON object"},
		{"string", `"x"`, A{}, "one JSON object"},
		{"two values", `{"n":1}{"n":2}`, A{}, "more than one"},
		{"broken", `{"n":`, A{}, "not valid JSON"},
	} {
		var got A
		err := DecodeArgs(json.RawMessage(c.in), &got)
		switch {
		case c.problem == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.problem != "" && (err == nil || !strings.Contains(err.Error(), c.problem)):
			t.Errorf("%s: err = %v, want %q", c.name, err, c.problem)
		case c.problem == "" && got != c.want:
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestStringMapDecodesArrayAndObject(t *testing.T) {
	want := StringMap{"a": "1", "b": "two", "c": "true"}
	for name, in := range map[string]string{
		"array":         `[{"name":"a","value":"1"},{"name":"b","value":"two"},{"name":"c","value":"true"}]`,
		"object":        `{"a":"1","b":"two","c":"true"}`,
		"array scalars": `[{"name":"a","value":1},{"name":"b","value":"two"},{"name":"c","value":true}]`,
		"object scalar": `{"a":1,"b":"two","c":true}`,
	} {
		var m StringMap
		if err := json.Unmarshal([]byte(in), &m); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !reflect.DeepEqual(m, want) {
			t.Errorf("%s: got %v", name, m)
		}
	}
	var empty struct {
		With StringMap `json:"with"`
	}
	for _, in := range []string{`{"with":[]}`, `{"with":{}}`, `{"with":null}`, `{}`} {
		if err := json.Unmarshal([]byte(in), &empty); err != nil || len(empty.With) != 0 {
			t.Errorf("%s: %v %v", in, empty.With, err)
		}
	}
}

func TestStringMapRejects(t *testing.T) {
	for name, in := range map[string]string{
		"duplicate":     `[{"name":"a","value":"1"},{"name":"a","value":"2"}]`,
		"empty name":    `[{"name":"","value":"1"}]`,
		"no name":       `[{"value":"1"}]`,
		"no value":      `[{"name":"a"}]`,
		"extra key":     `[{"name":"a","value":"1","x":1}]`,
		"item not obj":  `["a"]`,
		"object value":  `{"a":{"b":1}}`,
		"null value":    `{"a":null}`,
		"empty key":     `{"":"x"}`,
		"scalar":        `"a=b"`,
		"number":        `5`,
		"array value":   `[{"name":"a","value":[1]}]`,
		"name not text": `[{"name":3,"value":"x"}]`,
	} {
		var m StringMap
		if err := json.Unmarshal([]byte(in), &m); err == nil {
			t.Errorf("%s: accepted %s as %v", name, in, m)
		}
	}
}

func TestStringMapInsideDecodeArgs(t *testing.T) {
	var a struct {
		With StringMap `json:"with"`
	}
	if err := DecodeArgs(json.RawMessage(`{"with":[{"name":"k","value":"v"}]}`), &a); err != nil || a.With["k"] != "v" {
		t.Errorf("array: %v %v", a.With, err)
	}
	if err := DecodeArgs(json.RawMessage(`{"with":{"k":"v2"}}`), &a); err != nil || a.With["k"] != "v2" {
		t.Errorf("object: %v %v", a.With, err)
	}
	err := DecodeArgs(json.RawMessage(`{"with":[{"name":"k","value":"v","oops":1}]}`), &a)
	if err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("unknown key in a pair: %v", err)
	}
}

func TestStringMapMarshalsAsSortedArray(t *testing.T) {
	data, err := json.Marshal(StringMap{"b": "2", "a": "1"})
	if err != nil || string(data) != `[{"name":"a","value":"1"},{"name":"b","value":"2"}]` {
		t.Errorf("%s %v", data, err)
	}
	var back StringMap
	if err := json.Unmarshal(data, &back); err != nil || !reflect.DeepEqual(back, StringMap{"a": "1", "b": "2"}) {
		t.Errorf("round trip: %v %v", back, err)
	}
}
