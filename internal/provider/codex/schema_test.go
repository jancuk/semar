package codex

import (
	"encoding/json"
	"testing"
)

func TestStrictSchema(t *testing.T) {
	cases := []struct {
		name   string
		schema string
		want   bool
	}{
		{"flat", `{"type":"object","additionalProperties":false,"required":["a"],"properties":{"a":{"type":"string"}}}`, true},
		{"nested and nullable", `{"type":"object","additionalProperties":false,"required":["list"],"properties":{"list":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["n","e"],"properties":{"n":{"type":["integer","null"]},"e":{"enum":["x","y"]}}}}}}`, true},
		{"anyOf", `{"type":"object","additionalProperties":false,"required":["v"],"properties":{"v":{"anyOf":[{"type":"string"},{"type":"null"}]}}}`, true},
		{"optional property", `{"type":"object","additionalProperties":false,"required":["a"],"properties":{"a":{"type":"string"},"b":{"type":"string"}}}`, false},
		{"open object", `{"type":"object","required":["a"],"properties":{"a":{"type":"string"}}}`, false},
		{"any value", `{"type":"object","additionalProperties":false,"required":["j"],"properties":{"j":{}}}`, false},
		{"object without properties inside", `{"type":"object","additionalProperties":false,"required":["q"],"properties":{"q":{"type":"object"}}}`, false},
		{"array without items", `{"type":"object","additionalProperties":false,"required":["l"],"properties":{"l":{"type":"array"}}}`, false},
		{"oneOf", `{"type":"object","additionalProperties":false,"required":["v"],"properties":{"v":{"oneOf":[{"type":"string"}]}}}`, false},
		{"root not an object", `{"type":"array","items":{"type":"string"}}`, false},
		{"broken", `{`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := strictSchema(json.RawMessage(c.schema)); got != c.want {
				t.Fatalf("strictSchema = %v, want %v", got, c.want)
			}
		})
	}
}

func TestTOMLString(t *testing.T) {
	cases := map[string]string{
		`/tmp/acme`:          `"/tmp/acme"`,
		`/tmp/a "b"\c`:       `"/tmp/a \"b\"\\c"`,
		"/tmp/line\nbreak":   `"/tmp/line\u000Abreak"`,
		"/tmp/orders-ü/data": `"/tmp/orders-ü/data"`,
	}
	for in, want := range cases {
		got, err := tomlString(in)
		if err != nil || got != want {
			t.Errorf("tomlString(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	if _, err := tomlString("/tmp/\xff"); err == nil {
		t.Error("invalid UTF-8 was accepted")
	}
}

func TestParseEventsWithoutAnyEvent(t *testing.T) {
	_, err := parseEvents([]byte("not json\n\n{\"no\":\"type\"}\n"), 0, nil)
	if err == nil || err.Error() == "" {
		t.Fatal("no error")
	}
}
