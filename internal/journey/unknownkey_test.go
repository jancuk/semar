package journey

import "testing"

func TestParseNamesAnUnknownKeyInPlainWords(t *testing.T) {
	cases := []struct{ name, text, want string }{
		{"top level", "apiVersion: semar/v1\nkind: Journey\nid: a\nschedule: daily\n", "j.yaml:4: unknown key schedule"},
		{"in a leg", "kind: Journey\nlegs:\n  - id: a\n    asx: bob\n", "j.yaml:4: unknown key asx in legs"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(c.text), "j.yaml")
			if err == nil || err.Error() != c.want {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}
