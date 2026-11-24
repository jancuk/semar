package mobile_test

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/engine/mobile"
)

func jpegSize(t *testing.T, data []byte) image.Rectangle {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not a JPEG: %v", err)
	}
	return img.Bounds()
}

func TestShrinkToScalesToTheExactMaxSide(t *testing.T) {
	out, err := mobile.ShrinkTo(pngOf(1080, 2400), 1024, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b := jpegSize(t, out); b.Dx() != 460 || b.Dy() != 1024 {
		t.Errorf("size = %v, want 460x1024", b)
	}
	// 0 means the default of 1280.
	out, err = mobile.ShrinkTo(pngOf(1080, 2400), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b := jpegSize(t, out); b.Dy() != 1280 {
		t.Errorf("default size = %v", b)
	}
}

func TestShrinkToNeverEnlarges(t *testing.T) {
	out, err := mobile.ShrinkTo(pngOf(300, 200), 1024, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b := jpegSize(t, out); b.Dx() != 300 || b.Dy() != 200 {
		t.Errorf("size = %v", b)
	}
}

func TestShrinkToCropsBeforeScaling(t *testing.T) {
	// Left half red, right half blue. A clip of the right half must be all blue.
	img := image.NewRGBA(image.Rect(0, 0, 400, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 400; x++ {
			c := color.RGBA{R: 255, A: 255}
			if x >= 200 {
				c = color.RGBA{B: 255, A: 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	out, err := mobile.ShrinkTo(b.Bytes(), 100, &mobile.Rect{Left: 200, Top: 0, Right: 400, Bottom: 100})
	if err != nil {
		t.Fatal(err)
	}
	got, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if bb := got.Bounds(); bb.Dx() != 100 || bb.Dy() != 50 {
		t.Fatalf("size = %v, want 100x50", bb)
	}
	r, _, bl, _ := got.At(50, 25).RGBA()
	if r>>8 > 30 || bl>>8 < 220 {
		t.Errorf("the clip is not all blue: r=%d b=%d", r>>8, bl>>8)
	}
}

func TestShrinkToRefusesAClipOutsideTheImage(t *testing.T) {
	for _, c := range []mobile.Rect{
		{Left: -1, Top: 0, Right: 10, Bottom: 10},
		{Left: 0, Top: 0, Right: 301, Bottom: 10},
		{Left: 0, Top: 0, Right: 10, Bottom: 201},
		{Left: 10, Top: 10, Right: 10, Bottom: 20},
		{Left: 20, Top: 20, Right: 10, Bottom: 10},
	} {
		if _, err := mobile.ShrinkTo(pngOf(300, 200), 1024, &c); err == nil || !strings.Contains(err.Error(), "clip") {
			t.Errorf("clip %v: error = %v", c, err)
		}
	}
}

func TestShrinkToKeepsTheDecodeGuards(t *testing.T) {
	if _, err := mobile.ShrinkTo(bigHeader(9000, 100), 1024, nil); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("error = %v", err)
	}
	if _, err := mobile.ShrinkTo(bigHeader(8192, 8192), 1024, nil); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("error = %v", err)
	}
	if _, err := mobile.ShrinkTo([]byte("junk"), 1024, nil); err == nil {
		t.Error("junk")
	}
}

func TestRenderReportsTheMappingAndTheScale(t *testing.T) {
	s, err := mobile.Render(pngOf(1080, 2400), mobile.ShotOptions{MaxSide: 1200})
	if err != nil {
		t.Fatal(err)
	}
	if s.Width != 540 || s.Height != 1200 || s.ScreenWidth != 1080 || s.ScreenHeight != 2400 {
		t.Errorf("shot = %+v", s)
	}
	if s.Ratio != 2 {
		t.Errorf("ratio = %v", s.Ratio)
	}
	// scale 0.5 of a clip, with the origin kept.
	clip := mobile.Rect{Left: 100, Top: 200, Right: 500, Bottom: 600}
	s, err = mobile.Render(pngOf(1080, 2400), mobile.ShotOptions{Scale: 0.5, Clip: &clip})
	if err != nil {
		t.Fatal(err)
	}
	if s.Width != 200 || s.Height != 200 || s.Origin != (mobile.Point{X: 100, Y: 200}) || s.Ratio != 2 {
		t.Errorf("shot = %+v", s)
	}
	for _, bad := range []float64{-0.1, 1.5} {
		if _, err := mobile.Render(pngOf(100, 100), mobile.ShotOptions{Scale: bad}); err == nil {
			t.Errorf("scale %v was accepted", bad)
		}
	}
}

func TestRenderWritesAPNGThatKeepsThePixels(t *testing.T) {
	s, err := mobile.Render(pngOf(300, 200), mobile.ShotOptions{PNG: true})
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(s.Data))
	if err != nil {
		t.Fatalf("not a PNG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 300 || b.Dy() != 200 {
		t.Errorf("size = %v", b)
	}
}
