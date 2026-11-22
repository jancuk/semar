package xpathlite_test

import (
	"testing"

	"github.com/jancuk/semar/internal/xpathlite"
)

// FuzzCompile feeds any text to the reader and runs what it accepts on a tree. Neither may panic, and
// a text that Compile accepts must be accepted again as the same source.
func FuzzCompile(f *testing.F) {
	for _, s := range []string{
		"//android.widget.Button[@text='Pay' and @enabled='true']",
		"/hierarchy/*/android.widget.LinearLayout[last()]",
		"//*[contains(@content-desc,'x')]",
		"//*[starts-with(@resource-id,\"a\")][2]",
		"//a[1", "//", "[[[", "//a[@b='c' and", "", "/", "//a//b//c//d[last()]", "\x00//a", "//é[@é='é']",
	} {
		f.Add(s)
	}
	root := sample()
	f.Fuzz(func(t *testing.T, src string) {
		e, err := xpathlite.Compile(src)
		if err != nil {
			if e != nil {
				t.Fatal("an error with an expression")
			}
			return
		}
		if e.String() != src {
			t.Fatalf("String = %q, want %q", e.String(), src)
		}
		got := xpathlite.Select(e, root)
		seen := map[*node]bool{}
		for _, n := range got {
			if seen[n] {
				t.Fatalf("%q selected a node twice", src)
			}
			seen[n] = true
		}
		if err := xpathlite.Validate(src); err != nil {
			t.Fatalf("Validate refuses what Compile accepts: %v", err)
		}
	})
}
