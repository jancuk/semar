package mobile_test

import (
	"testing"

	"github.com/jancuk/semar/internal/engine/mobile"
)

func windowTree() (*mobile.Tree, map[string]*mobile.Node) {
	leaf := func(pkg, text string) *mobile.Node {
		return &mobile.Node{Class: "android.widget.TextView", Package: pkg, Text: text, Bounds: mobile.Rect{Right: 10, Bottom: 10}, Enabled: true}
	}
	named := map[string]*mobile.Node{
		"app": leaf("com.shop", "Sign in"), "ime": leaf("com.kb", "q"), "bar": leaf("com.android.systemui", "12:00"),
		"float": leaf("com.chat.head", "chat"),
	}
	wrap := func(pkg string, kids ...*mobile.Node) *mobile.Node {
		return &mobile.Node{Class: "android.widget.FrameLayout", Package: pkg, Bounds: mobile.Rect{Right: 1080, Bottom: 2400}, Children: kids}
	}
	root := &mobile.Node{Class: "hierarchy", Bounds: mobile.Rect{Right: 1080, Bottom: 2400}, Children: []*mobile.Node{
		wrap("com.shop", named["app"]), wrap("com.kb", named["ime"]), wrap("com.android.systemui", named["bar"]), wrap("com.chat.head", named["float"]),
	}}
	t := &mobile.Tree{Root: root, Width: 1080, Height: 2400}
	t.Finish()
	return t, named
}

func TestWindowsClassifiesByPackage(t *testing.T) {
	tree, _ := windowTree()
	ws := mobile.Windows(tree, mobile.WindowHints{App: "com.shop", IME: "com.kb"})
	want := []mobile.WindowKind{mobile.WindowApp, mobile.WindowKeyboard, mobile.WindowSystem, mobile.WindowOverlay}
	if len(ws) != len(want) {
		t.Fatalf("%d windows", len(ws))
	}
	for i, w := range ws {
		if w.Kind != want[i] || w.Index != i {
			t.Errorf("window %d = %+v, want %s", i, w, want[i])
		}
	}
	if ws[1].Package != "com.kb" || ws[3].Package != "com.chat.head" {
		t.Errorf("packages: %+v", ws)
	}
}

func TestWindowsGuessesTheAppWithoutAHint(t *testing.T) {
	tree, _ := windowTree()
	ws := mobile.Windows(tree, mobile.WindowHints{IME: "com.kb"})
	if ws[0].Kind != mobile.WindowApp || ws[1].Kind != mobile.WindowKeyboard || ws[3].Kind != mobile.WindowOverlay {
		t.Errorf("kinds = %v %v %v %v", ws[0].Kind, ws[1].Kind, ws[2].Kind, ws[3].Kind)
	}
}

func TestWindowsTakesThePackageFromAnInnerNode(t *testing.T) {
	inner := &mobile.Node{Class: "x", Package: "com.shop", Bounds: mobile.Rect{Right: 5, Bottom: 5}}
	top := &mobile.Node{Class: "FrameLayout", Bounds: mobile.Rect{Right: 5, Bottom: 5}, Children: []*mobile.Node{inner}}
	tree := &mobile.Tree{Root: &mobile.Node{Class: "hierarchy", Children: []*mobile.Node{top}}, Width: 5, Height: 5}
	tree.Finish()
	ws := mobile.Windows(tree, mobile.WindowHints{App: "com.shop"})
	if len(ws) != 1 || ws[0].Package != "com.shop" || ws[0].Kind != mobile.WindowApp {
		t.Errorf("ws = %+v", ws)
	}
}

func TestWindowsOfAFlatTreeIsOneWindow(t *testing.T) {
	tree := &mobile.Tree{Root: &mobile.Node{Class: "hierarchy", Package: "com.shop"}}
	tree.Finish()
	ws := mobile.Windows(tree, mobile.WindowHints{App: "com.shop"})
	if len(ws) != 1 || ws[0].Root != tree.Root {
		t.Errorf("ws = %+v", ws)
	}
	if mobile.Windows(nil, mobile.WindowHints{}) != nil || mobile.Windows(&mobile.Tree{}, mobile.WindowHints{}) != nil {
		t.Error("a nil or empty tree has windows")
	}
}

func TestWindowOfFollowsTheParents(t *testing.T) {
	tree, named := windowTree()
	ws := mobile.Windows(tree, mobile.WindowHints{App: "com.shop", IME: "com.kb"})
	for name, want := range map[string]int{"app": 0, "ime": 1, "bar": 2, "float": 3} {
		w := mobile.WindowOf(named[name], ws)
		if w == nil || w.Index != want {
			t.Errorf("%s: window = %+v, want %d", name, w, want)
		}
	}
	if w := mobile.WindowOf(tree.Root, ws); w != nil {
		t.Errorf("the root is in window %d", w.Index)
	}
	if mobile.WindowOf(&mobile.Node{}, ws) != nil || mobile.WindowOf(nil, ws) != nil {
		t.Error("a stray node has a window")
	}
}
