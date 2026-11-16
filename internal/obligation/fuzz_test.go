package obligation_test

import (
	"testing"

	"github.com/jancuk/semar/internal/obligation"
)

// A file that Parse accepts must give the same problems after Marshal and Parse, apart from
// the lines.
func FuzzParse(f *testing.F) {
	for _, name := range []string{"example.yaml", "hand.yaml"} {
		data, err := readFile(name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Add([]byte(""))
	f.Add([]byte("obligations: [unclosed\n"))
	f.Add([]byte("a: 1\n---\nb: 2\n"))
	f.Add([]byte(head + "obligations:\n  - {id: g/a, title: \"\\e[2J\", source: diff, covers: [a], gap: x}\n"))
	f.Add([]byte(head + "obligations:\n  - &a {id: g/a, title: T, source: diff}\n  - *a\n"))
	f.Add([]byte("\x00\xff: !!binary ~\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		a, err := obligation.Parse(data, "fuzz.yaml")
		if err != nil {
			return
		}
		want := keys(obligation.Validate(a, "fuzz.yaml"))
		out, err := obligation.Marshal(a)
		if err != nil {
			return // text that YAML cannot carry, such as invalid UTF-8
		}
		b, err := obligation.Parse(out, "fuzz.yaml")
		if err != nil {
			t.Fatalf("Marshal wrote a file that Parse refuses: %v\n%s", err, out)
		}
		got := keys(obligation.Validate(b, "fuzz.yaml"))
		if len(got) != len(want) {
			t.Fatalf("problems changed: %v, then %v", want, got)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("problem %d changed: %q, then %q", i, want[i], got[i])
			}
		}
	})
}

func keys(ps []obligation.Problem) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Path + ": " + p.Message
	}
	return out
}
