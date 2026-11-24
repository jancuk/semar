package mobilerec_test

import (
	"testing"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/mobilerec"
)

func listTree() *mobile.Tree {
	btn := func(pkg, id, text string) *mobile.Node {
		return &mobile.Node{Class: "android.widget.Button", Role: "button", Package: pkg, ID: id, Text: text, Enabled: true,
			Bounds: mobile.Rect{Left: 0, Top: 0, Right: 100, Bottom: 50}, Attrs: map[string]string{"clickable": "true", "text": text}}
	}
	win := func(pkg string, kids ...*mobile.Node) *mobile.Node {
		return &mobile.Node{Class: "android.widget.FrameLayout", Package: pkg, Enabled: true, Bounds: mobile.Rect{Right: 1080, Bottom: 2400}, Children: kids}
	}
	field := &mobile.Node{Class: "android.widget.EditText", Role: "textbox", Package: "com.shop", Focused: true, Enabled: true,
		Hint: "Email", Bounds: mobile.Rect{Left: 0, Top: 100, Right: 500, Bottom: 160}, Attrs: map[string]string{"editable": "true"}}
	root := &mobile.Node{Class: "hierarchy", Bounds: mobile.Rect{Right: 1080, Bottom: 2400}, Children: []*mobile.Node{
		win("com.shop", btn("com.shop", "login", "Sign in"), field),
		win("com.kb", btn("com.kb", "", "q")),
		win("com.android.systemui", btn("com.android.systemui", "clock", "12:00")),
	}}
	t := &mobile.Tree{Root: root, Width: 1080, Height: 2400}
	t.Finish()
	return t
}

func TestListElementsFillsWindowPackageAndFocus(t *testing.T) {
	els, nodes := mobilerec.ListElements(listTree(), mobile.WindowHints{App: "com.shop", IME: "com.kb"})
	if len(els) != 3 || len(nodes) != 3 {
		t.Fatalf("%d elements, %d nodes: %+v", len(els), len(nodes), els)
	}
	for i, e := range els {
		if e.Index != i {
			t.Errorf("element %d has index %d", i, e.Index)
		}
	}
	if e := els[0]; e.Window != "app" || e.Package != "com.shop" || e.ID != "login" || e.Text != "Sign in" || !e.Clickable {
		t.Errorf("first = %+v", e)
	}
	if e := els[1]; !e.Editable || !e.Focused || e.Hint != "Email" || e.Window != "app" {
		t.Errorf("field = %+v", e)
	}
	if e := els[2]; e.Window != "keyboard" || e.Package != "com.kb" {
		t.Errorf("key = %+v", e)
	}
}

func TestListElementsKeepsTheSystemUIFilter(t *testing.T) {
	els, _ := mobilerec.ListElements(listTree(), mobile.WindowHints{App: "com.android.systemui"})
	found := false
	for _, e := range els {
		if e.ID == "clock" {
			found = true
		}
	}
	if !found {
		t.Error("the system UI is the app, so its elements are kept")
	}
	if els, nodes := mobilerec.ListElements(nil, mobile.WindowHints{}); els != nil || nodes != nil {
		t.Error("a nil tree lists elements")
	}
}

func TestIdentityIgnoresTextButNotPlace(t *testing.T) {
	a := &mobile.Node{Class: "c", ID: "id", Desc: "d", Hint: "h", Text: "one", Bounds: mobile.Rect{Right: 5, Bottom: 5}}
	b := *a
	b.Text = "two"
	if mobilerec.Identity(a) != mobilerec.Identity(&b) {
		t.Error("the text changed the identity")
	}
	b.Bounds.Right = 6
	if mobilerec.Identity(a) == mobilerec.Identity(&b) {
		t.Error("a move kept the identity")
	}
}
