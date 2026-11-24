package mobilemcp_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/mobilemcp"
)

const (
	kbPkg   = "com.aura.keyboard.debug"
	appPkg  = "com.aura.keyboard.fixture"
	testdir = "../engine/mobile/android/testdata"
)

// loadTree reads a screen that the Android engine tests share.
func loadTree(t *testing.T, name string) *mobile.Tree {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testdir, name))
	if err != nil {
		t.Fatal(err)
	}
	tree, err := android.ParseSource(data)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return tree
}

// retree makes a changed copy of a fixture screen: edit may change the nodes, and the digest is new.
func retree(t *testing.T, name, digest string, edit func(*mobile.Tree)) *mobile.Tree {
	t.Helper()
	tree := loadTree(t, name)
	if edit != nil {
		edit(tree)
	}
	tree.Finish()
	tree.Digest = digest
	return tree
}

// findNode gives the first node that the test picks.
func findNode(t *testing.T, tree *mobile.Tree, pick func(*mobile.Node) bool) *mobile.Node {
	t.Helper()
	var found *mobile.Node
	tree.Walk(func(n *mobile.Node) bool {
		if found == nil && pick(n) {
			found = n
		}
		return found == nil
	})
	if found == nil {
		t.Fatal("no such node in the screen")
	}
	return found
}

func keyDesc(desc string) func(*mobile.Node) bool {
	return func(n *mobile.Node) bool {
		return n.Package == kbPkg && n.Desc == desc && n.Class == "android.widget.Button"
	}
}

// reactiveDev is a fake device whose screen changes when it is touched.
type reactiveDev struct {
	*fakeDev
	onTouch func(g mobile.Gesture)
}

func (d *reactiveDev) Touch(ctx context.Context, g mobile.Gesture) error {
	err := d.fakeDev.Touch(ctx, g)
	if d.onTouch != nil {
		d.onTouch(g)
	}
	return err
}

// imeFixture is a Manager over a device that shows the fixture app and the keyboard of Aura.
func imeFixture(t *testing.T, opts ...fixOpt) *fixture {
	t.Helper()
	f := newFixture(t, opts...)
	f.dev.imePkg = kbPkg
	f.dev.setTree(retree(t, "ime_multiwindow.xml", "ime1", nil))
	return f
}

func touchedAt(d *fakeDev) []mobile.Point {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []mobile.Point
	for _, g := range d.touches {
		out = append(out, g.From)
	}
	return out
}

func TestTypeViaKeyboardTapsTheKeyOfEachCharacter(t *testing.T) {
	f := imeFixture(t)
	res := f.ok("mobile_type_keys", `{"text":"qwe a","via":"keyboard"}`)
	if got := textOf(res); !strings.Contains(got, "typed 5 characters") || !strings.Contains(got, kbPkg) {
		t.Errorf("result = %q", got)
	}
	// q, w, e are the first keys of the top row, 108 wide; a is the first of the second row, and the space
	// is mapped to the key that is labelled Space.
	want := []mobile.Point{{X: 54, Y: 1620}, {X: 162, Y: 1620}, {X: 270, Y: 1620}, {X: 540, Y: 1900}, {X: 108, Y: 1760}}
	got := touchedAt(f.dev)
	if len(got) != len(want) {
		t.Fatalf("touches = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("touch %d = %v, want %v", i, got[i], want[i])
		}
	}
	if f.dev.called("TypeFocused") != 0 {
		t.Error("the text was set in the field, not typed on the keyboard")
	}
}

func TestTypeViaKeyboardSubmitPressesEnterAndNewLineUsesTheEnterKey(t *testing.T) {
	f := imeFixture(t)
	f.ok("mobile_type_keys", `{"text":"q\n","via":"keyboard","submit":true}`)
	got := touchedAt(f.dev)
	if len(got) != 2 || got[1] != (mobile.Point{X: 990, Y: 1900}) {
		t.Errorf("touches = %v: a new line taps the Enter key", got)
	}
	if len(f.dev.keys) != 1 {
		t.Errorf("keys = %v, want the Enter after the text", f.dev.keys)
	}
}

func TestTypeViaKeyboardUppercaseWithShiftOffStopsAndSaysWhy(t *testing.T) {
	f := imeFixture(t)
	got := f.fails("mobile_type_keys", `{"text":"qQ","via":"keyboard"}`, "no key for 'Q' on "+kbPkg+": tap shift first")
	if !strings.Contains(got, "1 of 2 characters were typed") {
		t.Errorf("error = %q", got)
	}
	if n := len(touchedAt(f.dev)); n != 1 {
		t.Errorf("%d taps: the keys before the missing one are typed, nothing after it", n)
	}
}

func TestTypeViaKeyboardMissingKeyOnThisPage(t *testing.T) {
	f := imeFixture(t)
	f.fails("mobile_type_keys", `{"text":"1","via":"keyboard"}`, "no key for '1' on "+kbPkg+": this page of the keyboard has no such key")
}

func TestTypeViaKeyboardReadsTheScreenAgainAfterEachTap(t *testing.T) {
	var rd *reactiveDev
	var once sync.Once
	f := imeFixture(t, func(o *mobilemcp.Options, fx *fixture) {
		rd = &reactiveDev{fakeDev: fx.dev}
		o.Opener = func(context.Context, string) (mobilemcp.Device, func(context.Context) error, error) {
			return rd, func(context.Context) error { return nil }, nil
		}
	})
	// The first tap turns the keys to capital letters, as a shift that is on would.
	upper := retree(t, "ime_multiwindow.xml", "ime2", func(tree *mobile.Tree) {
		tree.Walk(func(n *mobile.Node) bool {
			if n.Package == kbPkg && len(n.Desc) == 1 {
				n.Desc = strings.ToUpper(n.Desc)
			}
			return true
		})
	})
	rd.onTouch = func(mobile.Gesture) { once.Do(func() { f.dev.setTree(upper) }) }
	f.ok("mobile_type_keys", `{"text":"qW","via":"keyboard"}`)
	if n := len(touchedAt(f.dev)); n != 2 {
		t.Errorf("%d taps, want 2: the second key was found on the layout that the first tap gave", n)
	}
}

func TestTypeViaKeyboardIsRefusedWithASecret(t *testing.T) {
	f := imeFixture(t)
	f.fails("mobile_type_keys", `{"secretEnv":"SHOP_PASSWORD","via":"keyboard"}`, "refused with secretEnv")
	got := f.fails("mobile_type_keys", `{"text":"x","secretEnv":"SHOP_PASSWORD","via":"keyboard"}`, "secretEnv")
	if strings.Contains(got, "s3cret") {
		t.Errorf("the refusal shows the secret: %q", got)
	}
	if f.opens.Load() != 0 || f.dev.called("Touch") != 0 {
		t.Errorf("the device was used: opens %d, touches %d", f.opens.Load(), f.dev.called("Touch"))
	}
}

func TestTypeViaKeyboardRefusals(t *testing.T) {
	f := imeFixture(t)
	f.fails("mobile_type_keys", `{"text":"a","via":"keyboard","replace":true}`, "replace is not available")
	f.fails("mobile_type_keys", `{"text":"a","via":"voice"}`, "use direct or keyboard")
	f.fails("mobile_type_keys", `{"via":"keyboard"}`, "give the text")
	f.fails("mobile_type_keys", `{"text":"`+strings.Repeat("a", 257)+`","via":"keyboard"}`, "at most 256")
}

func TestTypeDirectIsTheDefaultAndStillSetsTheText(t *testing.T) {
	f := imeFixture(t)
	f.ok("mobile_type_keys", `{"text":"hello"}`)
	f.ok("mobile_type_keys", `{"text":"again","via":"direct"}`)
	if f.dev.called("TypeFocused") != 2 || f.dev.called("Touch") != 0 {
		t.Errorf("calls = %v", f.dev.calls)
	}
}

func TestKeyFor(t *testing.T) {
	tree := loadTree(t, "ime_multiwindow.xml")
	hints := mobile.WindowHints{IME: kbPkg}
	for _, r := range []rune{'q', 'y', 'd', ' ', '\n'} {
		if _, pkg, err := mobilemcp.KeyFor(tree, hints, r); err != nil || pkg != kbPkg {
			t.Errorf("KeyFor(%q) = %q, %v", r, pkg, err)
		}
	}
	if _, _, err := mobilemcp.KeyFor(tree, mobile.WindowHints{}, 'q'); err == nil || !strings.Contains(err.Error(), "input method is not known") {
		t.Errorf("without the input method: %v", err)
	}
	// Two keys that show the same letter in different boxes cannot be told apart.
	dup := retree(t, "ime_multiwindow.xml", "dup", func(tr *mobile.Tree) {
		w := findNode(t, tr, keyDesc("w"))
		w.Desc = "q"
	})
	if _, _, err := mobilemcp.KeyFor(dup, hints, 'q'); err == nil || !strings.Contains(err.Error(), "2 keys show 'q'") {
		t.Errorf("ambiguous: %v", err)
	}
	// A keyboard that is not on the screen.
	gone := retree(t, "ime_multiwindow.xml", "gone", func(tr *mobile.Tree) { tr.Root.Children = tr.Root.Children[:1] })
	if _, _, err := mobilemcp.KeyFor(gone, hints, 'q'); err == nil || !strings.Contains(err.Error(), "is not shown") {
		t.Errorf("no keyboard: %v", err)
	}
}
