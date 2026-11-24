package mobilemcp_test

import (
	"bytes"
	"encoding/base64"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/mcp"
)

// imageOf decodes the first image of a result, and gives its text caption too.
func imageOf(t *testing.T, r mcp.Result) (w, h int, caption string) {
	t.Helper()
	for _, c := range r.Content {
		switch c.Type {
		case "image":
			if c.MimeType != "image/jpeg" {
				t.Errorf("mime = %s", c.MimeType)
			}
			raw, err := base64.StdEncoding.DecodeString(c.Data)
			if err != nil {
				t.Fatal(err)
			}
			img, err := jpeg.Decode(bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("not a JPEG: %v", err)
			}
			w, h = img.Bounds().Dx(), img.Bounds().Dy()
		case "text":
			caption = c.Text
		}
	}
	if w == 0 {
		t.Fatalf("no image in %+v", r)
	}
	return w, h, caption
}

func TestScreenshotSizeMath(t *testing.T) {
	f := newFixture(t)
	for _, c := range []struct {
		args      string
		w, h      int
		wantCapty string
	}{
		{``, 460, 1024, "image 460x1024 of screen 1080x2400: screen = image × 2.348; clip origin (0,0)"},
		{`{"maxSize":512}`, 230, 512, "image 230x512 of screen 1080x2400: screen = image × 4.696; clip origin (0,0)"},
		{`{"maxSize":4096}`, 1080, 2400, "image 1080x2400 of screen 1080x2400: screen = image × 1.000; clip origin (0,0)"},
		{`{"scale":0.25}`, 270, 600, "image 270x600 of screen 1080x2400: screen = image × 4.000; clip origin (0,0)"},
		{`{"scale":0.5}`, 460, 1024, "screen = image × 2.348"},
		{`{"scale":0.5,"maxSize":2000}`, 540, 1200, "image 540x1200 of screen 1080x2400: screen = image × 2.000; clip origin (0,0)"},
		{`{"clip":{"x":100,"y":200,"width":400,"height":600}}`, 400, 600, "image 400x600 of screen 1080x2400: screen = image × 1.000; clip origin (100,200)"},
		{`{"clip":{"x":0,"y":0,"width":1080,"height":1200},"maxSize":600}`, 540, 600, "image 540x600 of screen 1080x2400: screen = image × 2.000; clip origin (0,0)"},
		{`{"clip":{"x":40,"y":80,"width":200,"height":400},"scale":0.5}`, 100, 200, "image 100x200 of screen 1080x2400: screen = image × 2.000; clip origin (40,80)"},
	} {
		w, h, caption := imageOf(t, f.ok("mobile_take_screenshot", c.args))
		if w != c.w || h != c.h {
			t.Errorf("%s: %dx%d, want %dx%d", c.args, w, h, c.w, c.h)
		}
		if !strings.Contains(caption, c.wantCapty) {
			t.Errorf("%s: caption %q lacks %q", c.args, caption, c.wantCapty)
		}
	}
}

func TestScreenshotRefusals(t *testing.T) {
	f := newFixture(t)
	for args, want := range map[string]string{
		`{"clip":{"x":1000,"y":0,"width":200,"height":100}}`: "clip",
		`{"clip":{"x":0,"y":2300,"width":100,"height":200}}`: "clip",
		`{"clip":{"x":0,"y":0,"width":0,"height":100}}`:      "clip",
		`{"clip":{"x":-5,"y":0,"width":10,"height":10}}`:     "clip",
		`{"clip":{"x":5}}`:  "",
		`{"maxSize":10}`:    "maxSize",
		`{"maxSize":99999}`: "maxSize",
		`{"scale":1.5}`:     "scale",
		`{"scale":-1}`:      "scale",
	} {
		res := f.call("mobile_take_screenshot", args)
		if !res.IsError || (want != "" && !strings.Contains(textOf(res), want)) {
			t.Errorf("%s: %s", args, textOf(res))
		}
	}
	f.dev.err["Screenshot"] = mobile.ErrSecure
	f.fails("mobile_take_screenshot", "", "secure")
	f.dev.err["Screenshot"] = nil
	f.dev.shot = []byte("not a png")
	f.fails("mobile_take_screenshot", "", "not a PNG")
}

func TestScreenshotIsRefusedWhenASecretIsOnScreen(t *testing.T) {
	f := newFixture(t)
	f.scrub.Add("hunter2-password")
	f.dev.setTree(textTree("s1", "password: hunter2-password"))
	f.fails("mobile_take_screenshot", "", "secret value")
	f.fails("mobile_save_screenshot", `{"saveTo":"a.png"}`, "secret value")
	if f.dev.called("Screenshot") != 0 {
		t.Error("a screen with a secret was captured")
	}
	if _, err := os.Stat(filepath.Join(f.outDir, "a.png")); err == nil {
		t.Error("a file was saved")
	}
	// The secret can be in a description, a hint, an id, or any attribute too.
	f.dev.setTree(screenTree("s2", loginBox, true))
	f.dev.tree.Root.Children[0].Children[1].Attrs["extra"] = "has hunter2-password inside"
	f.fails("mobile_take_screenshot", "", "secret value")
	f.dev.setTree(screenTree("s3", loginBox, true))
	f.ok("mobile_take_screenshot", "")
}

func TestSaveScreenshot(t *testing.T) {
	f := newFixture(t)
	var saved struct {
		Path          string
		Bytes         int
		Width, Height int
	}
	decode(t, f.ok("mobile_save_screenshot", `{"saveTo":"shots/full.png"}`), &saved)
	b, err := os.ReadFile(saved.Path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("not a PNG: %v", err)
	}
	if img.Bounds().Dx() != 1080 || img.Bounds().Dy() != 2400 || saved.Width != 1080 || saved.Bytes != len(b) {
		t.Errorf("png %v, saved %+v", img.Bounds(), saved)
	}
	if !strings.HasPrefix(saved.Path, f.realOut) {
		t.Errorf("path %s is outside %s", saved.Path, f.realOut)
	}

	decode(t, f.ok("mobile_save_screenshot", `{"saveTo":"small.JPG","maxSize":300}`), &saved)
	b, _ = os.ReadFile(saved.Path)
	jimg, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil || jimg.Bounds().Dx() != 135 || jimg.Bounds().Dy() != 300 {
		t.Errorf("jpeg %v, %v", jimg.Bounds(), err)
	}
	decode(t, f.ok("mobile_save_screenshot", `{"saveTo":"half.jpeg","scale":0.5}`), &saved)
	if saved.Width != 540 || saved.Height != 1200 {
		t.Errorf("saved = %+v", saved)
	}
	// What is saved is a picture and never text: the secret check ran on the tree.
	f.fails("mobile_save_screenshot", `{}`, "saveTo")
}

func TestSaveScreenshotPathGuard(t *testing.T) {
	f := newFixture(t)
	outside := t.TempDir()
	linkDir := filepath.Join(f.outDir, "link")
	if err := os.Symlink(outside, linkDir); err != nil {
		t.Skip("no symlinks:", err)
	}
	victim := filepath.Join(outside, "victim.png")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(f.outDir, "target.png")); err != nil {
		t.Fatal(err)
	}
	for saveTo, want := range map[string]string{
		"../escape.png":                   "refused",
		"a/../../escape.png":              "refused",
		filepath.Join(outside, "abs.png"): "not inside",
		"link/new.png":                    "symbolic link",
		"target.png":                      "symbolic link",
		"shot.gif":                        "extension",
		"noext":                           "extension",
		"":                                "",
	} {
		res := f.call("mobile_save_screenshot", `{"saveTo":`+quote(saveTo)+`}`)
		if !res.IsError || (want != "" && !strings.Contains(textOf(res), want)) {
			t.Errorf("saveTo %q: %s", saveTo, textOf(res))
		}
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Errorf("a file outside was written: %q", b)
	}
	for _, p := range []string{filepath.Join(filepath.Dir(f.outDir), "escape.png"), filepath.Join(outside, "abs.png"), filepath.Join(outside, "new.png")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s exists", p)
		}
	}
	if f.dev.called("Screenshot") != 0 {
		t.Error("a refused path still took a screenshot")
	}
}

func quote(s string) string {
	b, _ := jsonMarshal(s)
	return string(b)
}
