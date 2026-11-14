package yamlfile_test

import (
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
