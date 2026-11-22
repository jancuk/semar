package mobile_test

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
)

func TestShrinkKeepsASmallImageAndMakesAJPEG(t *testing.T) {
	out, err := mobile.Shrink(pngOf(300, 200))
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("not a JPEG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 300 || b.Dy() != 200 {
		t.Errorf("size = %v", b)
	}
}

func TestShrinkReducesByAWholeFactorToAtMost1280(t *testing.T) {
	for _, c := range []struct{ w, h, wantW, wantH int }{
		{1080, 2400, 540, 1200},  // longest 2400: factor 2
		{1280, 800, 1280, 800},   // already within
		{1281, 100, 640, 50},     // just over: factor 2
		{2560, 1440, 1280, 720},  // factor 2
		{3000, 1000, 1000, 333},  // factor 3
		{4000, 4000, 1000, 1000}, // factor 4
	} {
		out, err := mobile.Shrink(pngOf(c.w, c.h))
		if err != nil {
			t.Fatalf("%dx%d: %v", c.w, c.h, err)
		}
		img, err := jpeg.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		if b := img.Bounds(); b.Dx() != c.wantW || b.Dy() != c.wantH || max(b.Dx(), b.Dy()) > 1280 {
			t.Errorf("%dx%d became %v, want %dx%d", c.w, c.h, b, c.wantW, c.wantH)
		}
	}
}

func TestShrinkTakesTheMeanOfTheBox(t *testing.T) {
	// A 2560x2 image: the left half is black and the right half is white, in 2x2 boxes.
	img := image.NewGray(image.Rect(0, 0, 2560, 2))
	for y := 0; y < 2; y++ {
		for x := 1280; x < 2560; x++ {
			img.Pix[y*img.Stride+x] = 255
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	out, err := mobile.Shrink(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	got, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	r1, _, _, _ := got.At(10, 0).RGBA()
	r2, _, _, _ := got.At(1270, 0).RGBA()
	if r1>>8 > 20 || r2>>8 < 235 {
		t.Errorf("left %d, right %d", r1>>8, r2>>8)
	}
}

func TestShrinkRefusesWhatIsNotAPNGOrIsTooLarge(t *testing.T) {
	if _, err := mobile.Shrink([]byte("not a png")); err == nil {
		t.Error("garbage")
	}
	if _, err := mobile.Shrink(nil); err == nil {
		t.Error("nothing")
	}
	// A PNG header that claims 9000 pixels is refused before the pixels are allocated.
	big := bigHeader(9000, 100)
	if _, err := mobile.Shrink(big); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("error = %v", err)
	}
	if _, err := mobile.Shrink(bigHeader(8192, 8192)); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("error = %v", err)
	}
	// Truncated pixel data is an error and not a panic.
	good := pngOf(100, 100)
	if _, err := mobile.Shrink(good[:len(good)/2]); err == nil {
		t.Error("a truncated image")
	}
}

// bigHeader is a PNG that declares a size and holds no pixels: the decoder must refuse by the header.
func bigHeader(w, h int) []byte {
	var b bytes.Buffer
	b.Write([]byte("\x89PNG\r\n\x1a\n"))
	chunk := func(kind string, data []byte) {
		n := len(data)
		b.Write([]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)})
		b.WriteString(kind)
		b.Write(data)
		crc := crc32png(append([]byte(kind), data...))
		b.Write([]byte{byte(crc >> 24), byte(crc >> 16), byte(crc >> 8), byte(crc)})
	}
	ihdr := []byte{byte(w >> 24), byte(w >> 16), byte(w >> 8), byte(w), byte(h >> 24), byte(h >> 16), byte(h >> 8), byte(h), 8, 2, 0, 0, 0}
	chunk("IHDR", ihdr)
	chunk("IEND", nil)
	return b.Bytes()
}

func crc32png(data []byte) uint32 {
	crc := ^uint32(0)
	for _, c := range data {
		crc ^= uint32(c)
		for i := 0; i < 8; i++ {
			if crc&1 == 1 {
				crc = crc>>1 ^ 0xedb88320
			} else {
				crc >>= 1
			}
		}
	}
	return ^crc
}

func TestAScreenshotIsStoredBelowTheRunDirectory(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.d.shot = pngOf(1080, 2400)
	res := r.do(call("start", domain.MobileAction{Service: "shop-app", Do: domain.MobileLaunch}, nil))
	if res.Action.Mobile.Screenshot != "screenshots/AND-1/0001-start.jpg" {
		t.Fatalf("screenshot = %q", res.Action.Mobile.Screenshot)
	}
	data := r.files.files["screenshots/AND-1/0001-start.jpg"]
	if img, err := jpeg.Decode(bytes.NewReader(data)); err != nil || img.Bounds().Dy() != 1200 {
		t.Errorf("stored = %d bytes, err %v", len(data), err)
	}
	r.do(call("again", domain.MobileAction{Service: "shop-app", Do: domain.MobileLaunch}, nil))
	if _, ok := r.files.files["screenshots/AND-1/0002-again.jpg"]; !ok {
		t.Errorf("files = %v", keys(r.files.files))
	}
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestASecureScreenGivesNoScreenshotAndNeverFailsTheStep(t *testing.T) {
	for name, err := range map[string]error{"secure": mobile.ErrSecure, "wrapped": errors.Join(mobile.ErrSecure, errors.New("x")), "other": errors.New("screencap failed")} {
		r := newRig(t, rigOptions{})
		r.d.shotErr = err
		res := r.do(call("start", domain.MobileAction{Service: "shop-app", Do: domain.MobileLaunch}, nil))
		if res.Action.Mobile.Screenshot != "" || res.Action.Err != "" || len(r.files.files) != 0 {
			t.Errorf("%s: screenshot %q, err %q", name, res.Action.Mobile.Screenshot, res.Action.Err)
		}
	}
}

func TestABadScreenshotOrAFileErrorNeverFailsTheStep(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.d.shot = []byte("not a png")
	res := r.do(call("start", domain.MobileAction{Service: "shop-app", Do: domain.MobileLaunch}, nil))
	if res.Action.Mobile.Screenshot != "" || res.Action.Err != "" {
		t.Errorf("action = %+v", res.Action)
	}
	r2 := newRig(t, rigOptions{})
	r2.d.shot = pngOf(100, 100)
	r2.files.err = errors.New("disk full")
	res = r2.do(call("start", domain.MobileAction{Service: "shop-app", Do: domain.MobileLaunch}, nil))
	if res.Action.Mobile.Screenshot != "" || res.Action.Err != "" {
		t.Errorf("action = %+v", res.Action)
	}
	// A run with no place for files takes none.
	r3 := newRig(t, rigOptions{}, func(o *mobile.Options) { o.Files = nil })
	r3.d.shot = pngOf(100, 100)
	res = r3.do(call("start", domain.MobileAction{Service: "shop-app", Do: domain.MobileLaunch}, nil))
	if res.Action.Mobile.Screenshot != "" || res.Action.Err != "" {
		t.Errorf("action = %+v", res.Action)
	}
}
