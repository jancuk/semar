package android_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/android"
)

// FuzzParseSource feeds any bytes to the parser of the screen source. It must never panic, must keep
// to its limits, and must give a tree that finished.
func FuzzParseSource(f *testing.F) {
	files, _ := filepath.Glob(filepath.Join("testdata", "*.xml"))
	for _, name := range files {
		data, err := os.ReadFile(name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	for _, s := range []string{
		"", "<", "<a", "<a>", "<a/>", "</a>", "<a b=\"", `<a bounds="[1,2][3,4]">`, `<a bounds="[999999999,1][2,3]"/>`, "<?xml ?>", "<!DOCTYPE a [<!ENTITY x \"y\">]><a>&x;</a>",
		"<a><b><c/></b></a>", "\x00\x01\x02", `<a text="&#0;"/>`, `<a text="&amp;&lt;&gt;&quot;"/>`, "<a:b xmlns:a=\"u\"/>",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		tree, err := android.ParseSource(data)
		if err != nil {
			if tree != nil {
				t.Fatal("an error with a tree")
			}
			return
		}
		if tree.Root == nil || tree.Digest == "" {
			t.Fatalf("a tree with no root or no digest: %+v", tree)
		}
		if n := tree.Count(); n > 20000 {
			t.Fatalf("%d nodes", n)
		}
		tree.Walk(func(n *mobile.Node) bool {
			if n.Text != "" && n.Password {
				t.Fatal("a password field shows text")
			}
			for _, k := range n.Children {
				if k.Parent != n {
					t.Fatal("a child that is not linked to its parent")
				}
			}
			return true
		})
	})
}
