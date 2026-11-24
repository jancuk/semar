package mobilemcp_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/engine/mobile"
)

func TestKeyboardKeysAreListedUnderTheirWindowAndTheirRefSurvivesATextChange(t *testing.T) {
	f := imeFixture(t)
	text := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if !strings.Contains(text, "window keyboard "+kbPkg) {
		t.Fatalf("no keyboard window:\n%s", text)
	}
	w := ref(t, text, "id=key_w")
	// Typing changed the field: its text and the digest of the screen are new, the keys are the same.
	f.dev.setTree(retree(t, "ime_multiwindow.xml", "ime-typed", func(tr *mobile.Tree) {
		field := findNode(t, tr, func(n *mobile.Node) bool { return strings.HasSuffix(n.ID, ":id/field") })
		field.Text = "qwerty"
		field.Attrs["text"] = "qwerty"
	}))
	res := textOf(f.ok("mobile_click_on_screen_at_coordinates", `{"ref":"`+w+`"}`))
	if !strings.Contains(res, "found again at (162,1620)") {
		t.Errorf("result = %q", res)
	}
}

func TestAKeyPreviewPopupDoesNotTakeTheRefOfTheKey(t *testing.T) {
	f := imeFixture(t)
	text := textOf(f.ok("mobile_list_elements_on_screen", ""))
	w := ref(t, text, "id=key_w")
	// The popup is a second node of the keyboard package that shows the same letter, above the key.
	f.dev.setTree(retree(t, "ime_multiwindow.xml", "ime-popup", func(tr *mobile.Tree) {
		key := findNode(t, tr, keyDesc("w"))
		popup := &mobile.Node{
			Class: key.Class, Role: key.Role, Package: kbPkg, ID: key.ID, Desc: "w", Text: "w", Enabled: true,
			Bounds: mobile.Rect{Left: 98, Top: 1400, Right: 226, Bottom: 1540}, Attrs: map[string]string{"clickable": "true", "text": "w"},
		}
		key.Parent.Children = append(key.Parent.Children, popup)
	}))
	f.ok("mobile_click_on_screen_at_coordinates", `{"ref":"`+w+`"}`)
	if g := touchedAt(f.dev); g[len(g)-1] != (mobile.Point{X: 162, Y: 1620}) {
		t.Errorf("tapped %v: the popup took the place of the key", g)
	}
}

func TestAKeyThatMovedALittleIsFoundByItsBoxAndALostOneIsStale(t *testing.T) {
	f := imeFixture(t)
	text := textOf(f.ok("mobile_list_elements_on_screen", ""))
	w := ref(t, text, "id=key_w")
	// The layout moved 10 px, and a popup of the same letter is far away: the nearest box is the key.
	f.dev.setTree(retree(t, "ime_multiwindow.xml", "ime-shifted", func(tr *mobile.Tree) {
		key := findNode(t, tr, keyDesc("w"))
		key.Bounds = mobile.Rect{Left: 108, Top: 1570, Right: 216, Bottom: 1690}
		popup := *key
		popup.Bounds = mobile.Rect{Left: 600, Top: 1300, Right: 700, Bottom: 1400}
		key.Parent.Children = append(key.Parent.Children, &popup)
	}))
	res := textOf(f.ok("mobile_click_on_screen_at_coordinates", `{"ref":"`+w+`"}`))
	if !strings.Contains(res, "found again at (162,1630)") {
		t.Errorf("result = %q", res)
	}
	// With the key gone from the keyboard, the ref is stale and the answer shows the screen as it is now.
	f.dev.setTree(retree(t, "ime_multiwindow.xml", "ime-gone", func(tr *mobile.Tree) {
		key := findNode(t, tr, keyDesc("w"))
		kids := key.Parent.Children[:0]
		for _, k := range key.Parent.Children {
			if k != key {
				kids = append(kids, k)
			}
		}
		key.Parent.Children = kids
	}))
	got := f.fails("mobile_click_on_screen_at_coordinates", `{"ref":"`+w+`"}`, "stale")
	if !strings.Contains(got, "window keyboard "+kbPkg) {
		t.Errorf("the stale error does not show the screen:\n%s", got)
	}
}

// canvasKeyboard is the fixture with the keys taken out: the keyboard draws them on a canvas.
func canvasKeyboard(t *testing.T, digest string) *mobile.Tree {
	return retree(t, "ime_multiwindow.xml", digest, func(tr *mobile.Tree) {
		for _, w := range tr.Root.Children {
			if w.Package == kbPkg {
				w.Children = nil
			}
		}
	})
}

func TestACanvasKeyboardIsOnePseudoElementThatIsTappedByCoordinates(t *testing.T) {
	f := imeFixture(t)
	f.dev.setTree(canvasKeyboard(t, "canvas1"))
	text := textOf(f.ok("mobile_list_elements_on_screen", ""))
	want := "keyboard  " + kbPkg + "  bounds=[0,1500][1080,2400]  (no keys exposed: tap by coordinates)"
	if !strings.Contains(text, want) {
		t.Fatalf("listing lacks %q:\n%s", want, text)
	}
	if strings.Count(text, "no keys exposed") != 1 {
		t.Errorf("more than one pseudo-element:\n%s", text)
	}
	kb := ref(t, text, "no keys exposed")
	// After typing, the screen changed, and the keyboard is found again by its window.
	f.dev.setTree(canvasKeyboard(t, "canvas2"))
	f.ok("mobile_click_on_screen_at_coordinates", `{"ref":"`+kb+`"}`)
	if g := touchedAt(f.dev); g[len(g)-1] != (mobile.Point{X: 540, Y: 1950}) {
		t.Errorf("tapped %v", g)
	}
	var js struct {
		Elements []struct {
			Ref   string `json:"ref"`
			Class string `json:"class"`
		} `json:"elements"`
	}
	decode(t, f.ok("mobile_list_elements_on_screen", `{"format":"json"}`), &js)
	found := false
	for _, e := range js.Elements {
		found = found || e.Ref == kb
	}
	if !found {
		t.Errorf("the json listing has no %s", kb)
	}
}

func TestAKeyboardWithKeysHasNoPseudoElement(t *testing.T) {
	f := imeFixture(t)
	if text := textOf(f.ok("mobile_list_elements_on_screen", "")); strings.Contains(text, "no keys exposed") {
		t.Errorf("listing:\n%s", text)
	}
}

func TestBackThatClosesTheKeyboardSaysSo(t *testing.T) {
	f := imeFixture(t)
	got := textOf(f.ok("mobile_press_button", `{"button":"back"}`))
	if !strings.Contains(got, "BACK closed the keyboard "+kbPkg) || !strings.Contains(got, "did not go back") {
		t.Errorf("result = %q", got)
	}
	if got := textOf(f.ok("mobile_press_button", `{"button":"HOME"}`)); got != "pressed HOME" {
		t.Errorf("result = %q", got)
	}
	f.dev.imeErr = errBoom
	if got := textOf(f.ok("mobile_press_button", `{"button":"BACK"}`)); got != "pressed BACK" {
		t.Errorf("when the keyboard cannot be read: %q", got)
	}
}

func TestFlutterListingCollapsesDescriptionsAndSaysWhatIsMissing(t *testing.T) {
	f := newFixture(t)
	f.dev.setTree(retree(t, "flutter_sinau.xml", "sinau", nil))
	text := textOf(f.ok("mobile_list_elements_on_screen", ""))
	for _, want := range []string{
		`"Selamat datang, Guru Ana"`,
		`"Kelas 7A Matematika Senin 07.00"`,
		"note: this is a Flutter app and its elements have no resource-id",
		"note: list is scrollable: swipe to see more",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("listing lacks %q:\n%s", want, text)
		}
	}
	// Every element is one line: a line that starts a description never ends before the element does.
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "e") && !strings.HasSuffix(line, "]") {
			t.Errorf("an element line is cut: %q", line)
		}
	}
}

func TestFlutterNotesAreNotAddedToOtherApps(t *testing.T) {
	f := imeFixture(t)
	text := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if strings.Contains(text, "Flutter") || strings.Contains(text, "swipe to see more") {
		t.Errorf("listing:\n%s", text)
	}
	// A Flutter app whose elements have identifiers gets no stability note.
	f.dev.setTree(retree(t, "flutter_sinau.xml", "sinau-ids", func(tr *mobile.Tree) {
		tr.Walk(func(n *mobile.Node) bool {
			if n.Package == "dev.sinau.sinau_mobile" && n.ID == "" {
				n.ID = "sinau:id/x"
			}
			return true
		})
	}))
	if text := textOf(f.ok("mobile_list_elements_on_screen", "")); strings.Contains(text, "no resource-id") {
		t.Errorf("listing:\n%s", text)
	}
}
