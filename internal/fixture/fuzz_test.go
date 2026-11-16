package fixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v4"
)

// FuzzParse: Parse never panics, and a fixture that Parse and Validate accept still passes both
// after it is written as YAML and read again.
func FuzzParse(f *testing.F) {
	seed, err := os.ReadFile(filepath.Join("testdata", exampleName))
	if err != nil {
		f.Fatal(err)
	}
	s := string(seed)
	for _, text := range []string{
		s,
		"",
		"apiVersion: semar/v1\nkind: Fixture\n",
		strings.Replace(s, "amount: 1000", "amount: 1e3", 1),
		strings.Replace(s, "amount: 1000", "amount: 2097152", 1),
		strings.Replace(s, "amount: 1000", "amount: [1, {a: 2.5}, null]", 1),
		strings.Replace(s, "equals: paid", "equals: 2.5", 1),
		strings.Replace(s, "equals: paid", "equals: [1]", 1),
		strings.Replace(s, "timeout: 30s", "timeout: 99h", 1),
		strings.Replace(s, "outputs: [id]", "outputs: [id]\ngiven: []", 1),
		strings.Replace(s, "{{id}}", "{{secret.TOKEN}}", 1),
		strings.Replace(s, "{{id}}", "{{a.b.c}}", 1),
		strings.Replace(s, "{{customer}}", "{{customer", 1),
		strings.Replace(s, "/v1/orders", "/v1/%2e%2e/orders", 1),
		"a: &a [*a]\n",
		"a: &a {b: *a}\n",
		strings.Repeat("[", 500) + strings.Repeat("]", 500),
		"apiVersion: [1\n",
		"\x00\xff",
	} {
		f.Add([]byte(text))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fx, err := Parse(data, "f.yaml")
		if err != nil {
			return
		}
		if len(Validate(fx, nil)) != 0 {
			return
		}
		out, err := yaml.Marshal(fx)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		again, err := Parse(out, "f.yaml")
		if err != nil {
			t.Fatalf("an accepted fixture does not parse after a marshal: %v\n%s", err, out)
		}
		if ps := Validate(again, nil); len(ps) != 0 {
			t.Fatalf("an accepted fixture has problems after a marshal: %v\n%s", ps, out)
		}
	})
}
