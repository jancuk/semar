package yamlfile_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/yamlfile"
)

func TestDecode(t *testing.T) {
	type doc struct {
		A int `yaml:"a"`
	}
	cases := []struct {
		name string
		in   string
		want []string // substrings of the error. Empty means success
	}{
		{"valid", "a: 1\n", nil},
		{"unknown key names file line and key", "a: 1\nb: 2\n", []string{"f.yaml:2", "b"}},
		{"empty", "", []string{"f.yaml", "empty"}},
		{"two documents", "a: 1\n---\na: 2\n", []string{"f.yaml", "more than one"}},
		{"malformed", "a: [1\n", []string{"f.yaml"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var d doc
			err := yamlfile.Decode([]byte(c.in), "f.yaml", &d)
			if len(c.want) == 0 {
				if err != nil || d.A != 1 {
					t.Fatalf("got %+v, %v", d, err)
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

func TestJSONable(t *testing.T) {
	cases := []struct {
		name    string
		in      any
		want    any
		wantErr string
	}{
		{"scalar", 3, 3, ""},
		{"string map", map[string]any{"a": 1}, map[string]any{"a": 1}, ""},
		{"any map becomes string map", map[any]any{"a": 1}, map[string]any{"a": 1}, ""},
		{"nested in slice", []any{map[any]any{"a": []any{map[any]any{"b": 2}}}}, []any{map[string]any{"a": []any{map[string]any{"b": 2}}}}, ""},
		{"integer key", map[any]any{1: "x"}, nil, "mapping key must be a string"},
		{"integer key deep", []any{map[string]any{"a": map[any]any{1: "x"}}}, nil, "mapping key must be a string"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := yamlfile.JSONable(c.in)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("got error %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(c.want, got) {
				t.Fatalf("got %#v, want %#v", got, c.want)
			}
			if _, err := json.Marshal(got); err != nil {
				t.Fatalf("result is not encodable: %v", err)
			}
		})
	}
}
