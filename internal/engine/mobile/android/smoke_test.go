package android_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/userconfig"
)

// TestSmokeAndroidSettings runs the platform against a real emulator and the Settings app. It is opt in:
//
//	SEMAR_ANDROID_SMOKE=1 go test -run Smoke -v ./internal/engine/mobile/android
//
// It needs adb, one running emulator, and the two UiAutomator2 server APKs in <semar home>/android/. It
// changes nothing on the emulator but a search that it types and leaves.
func TestSmokeAndroidSettings(t *testing.T) {
	if os.Getenv("SEMAR_ANDROID_SMOKE") != "1" {
		t.Skip("set SEMAR_ANDROID_SMOKE=1 to run the Android smoke test")
	}
	home, err := userconfig.Home(os.LookupEnv)
	if err != nil {
		t.Skipf("no Semar home: %v", err)
	}
	for _, name := range []string{android.ServerAPKName, android.ServerTestAPKName} {
		if _, err := os.Stat(filepath.Join(home, "android", name)); err != nil {
			t.Skipf("the server APK %s is not in %s: %v", name, filepath.Join(home, "android"), err)
		}
	}
	adbBin, err := android.FindADB(os.LookupEnv, "")
	if err != nil {
		t.Skipf("no adb: %v", err)
	}
	var run android.ExecRunner
	ctx := context.Background()
	out, err := run.Output(ctx, adbBin, "devices", "-l")
	if err != nil {
		t.Skipf("adb devices: %v", err)
	}
	if _, un := android.ChooseDevice(android.ParseDevices(string(out)), os.Getenv("ANDROID_SERIAL")); un != nil {
		t.Skipf("no emulator to use: %s", un.Reason)
	}

	p := android.New(android.Options{
		Home: home, RepoRoot: t.TempDir(),
		Apps: map[string]android.App{"settings": {Package: "com.android.settings", Reset: "restart"}},
	})
	closed := false
	closeOnce := func() {
		if !closed {
			closed = true
			if err := p.Close(ctx); err != nil {
				t.Errorf("Close: %v", err)
			}
		}
	}
	defer closeOnce()

	start := time.Now()
	d, err := p.Driver(ctx, "settings")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Logf("the platform started in %s on %s (Android %s)", time.Since(start).Round(time.Millisecond), d.About().Device, d.About().Version)

	if err := d.Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := d.Launch(ctx); err != nil {
		t.Fatalf("launch: %v", err)
	}
	time.Sleep(2 * time.Second)

	tree, err := d.Source(ctx)
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	if tree.Count() < 5 {
		t.Fatalf("the Settings screen has %d nodes", tree.Count())
	}
	t.Logf("the screen is %dx%d with %d nodes", tree.Width, tree.Height, tree.Count())

	// Read an element: the first text that a person can read.
	var firstText, scroller *mobile.Node
	tree.Walk(func(n *mobile.Node) bool {
		if firstText == nil && n.Visible && n.Role == "text" && n.Text != "" {
			firstText = n
		}
		if scroller == nil && n.Visible && n.Scrollable && n.Bounds.Height() > tree.Height/3 {
			scroller = n
		}
		return true
	})
	if firstText == nil {
		t.Fatal("the Settings screen shows no text")
	}
	found, err := mobile.Find(tree, domain.Locator{Text: firstText.Text}, nil)
	if err != nil || len(found) == 0 {
		t.Fatalf("an element that was read is not found by its text %q: %v", firstText.Text, err)
	}
	t.Logf("read %q", firstText.Text)

	// Scroll: a swipe over the list changes the screen.
	if scroller != nil {
		c := scroller.Bounds
		mid := (c.Left + c.Right) / 2
		g := mobile.Gesture{From: mobile.Point{X: mid, Y: c.Top + c.Height()*4/5}, To: mobile.Point{X: mid, Y: c.Top + c.Height()/5}, Move: 300 * time.Millisecond}
		if err := d.Touch(ctx, g); err != nil {
			t.Fatalf("swipe: %v", err)
		}
		time.Sleep(time.Second)
		after, err := d.Source(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if after.Digest == tree.Digest {
			t.Log("the screen did not change after the swipe (the list may be short)")
		}
		tree = after
	} else {
		t.Log("no scrollable list on this screen")
	}

	// Type in the search: tap the search bar, then type in the field that has the focus.
	searchLocators := []domain.Locator{
		{Text: "Search settings"}, {Desc: "Search settings"}, {Desc: "Search"}, {Role: "textbox"}, {XPath: "//*[contains(@resource-id,'search')]"},
	}
	var search *mobile.Node
	for _, loc := range searchLocators {
		if nodes, err := mobile.Find(tree, loc, nil); err == nil {
			for _, n := range nodes {
				if n.Visible {
					search = n
					break
				}
			}
		}
		if search != nil {
			break
		}
	}
	if search == nil {
		// The list scrolled the search bar away: go back to the top.
		if err := d.Reset(ctx); err != nil {
			t.Fatal(err)
		}
		_ = d.Launch(ctx)
		time.Sleep(2 * time.Second)
		if tree, err = d.Source(ctx); err != nil {
			t.Fatal(err)
		}
		for _, loc := range searchLocators {
			if nodes, _ := mobile.Find(tree, loc, nil); len(nodes) > 0 && nodes[0].Visible {
				search = nodes[0]
				break
			}
		}
	}
	if search == nil {
		t.Fatalf("the search bar of Settings was not found among %d nodes", tree.Count())
	}
	at := search.Bounds.Center()
	if err := d.Touch(ctx, mobile.Gesture{From: at, To: at}); err != nil {
		t.Fatalf("tap on the search: %v", err)
	}
	time.Sleep(time.Second)
	if err := d.Type(ctx, "wifi"); err != nil {
		t.Fatalf("type: %v", err)
	}
	time.Sleep(time.Second)
	typed, err := d.Source(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var sawTyped bool
	typed.Walk(func(n *mobile.Node) bool {
		if strings.EqualFold(n.Text, "wifi") {
			sawTyped = true
		}
		return true
	})
	if !sawTyped {
		t.Error("the text that was typed is not on the screen")
	}

	// A screenshot.
	shot, err := d.Screenshot(ctx)
	if err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	jpg, err := mobile.Shrink(shot)
	if err != nil || len(jpg) == 0 {
		t.Fatalf("the screenshot was not read: %v", err)
	}
	t.Logf("screenshot: %d bytes of PNG, %d of JPEG", len(shot), len(jpg))

	// Leave the keyboard and the search as they were found.
	_ = d.Key(ctx, mobile.KeyBack)
	_ = d.Key(ctx, mobile.KeyHome)

	closeOnce()
	forwards, err := run.Output(ctx, adbBin, "forward", "--list")
	if err != nil {
		t.Fatalf("adb forward --list: %v", err)
	}
	if strings.Contains(string(forwards), "tcp:6790") {
		t.Errorf("a forward of the server was left behind:\n%s", forwards)
	}
}
