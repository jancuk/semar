package mobile

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
)

const (
	// maxDecodeSide is the largest side of a screenshot that is decoded. A device that is bigger, or a
	// server that sends a hostile image, is refused before the pixels are allocated.
	maxDecodeSide = 8192
	// maxDecodePixels bounds the memory of the decoded image: 4 bytes a pixel.
	maxDecodePixels = 32 << 20
	// maxShotSide is the longest side of the screenshot that is stored.
	maxShotSide = 1280
	jpegQuality = 80
)

// Shrink turns a PNG into a JPEG whose longest side is at most 1280 pixels. A larger image is reduced
// by the whole factor that brings it within the limit, and each new pixel is the mean of a box of
// the old ones. It never decodes an image with a side over 8192 pixels. The evidence of a run uses it,
// so its output stays the same; ShrinkTo is the general form that the tool server uses.
func Shrink(data []byte) ([]byte, error) {
	s, err := render(data, renderOptions{maxSide: maxShotSide, whole: true, quality: jpegQuality})
	return s.Data, err
}

// ShrinkTo turns a PNG into a JPEG (quality 75) whose longest side is at most maxSide pixels (1280 when
// maxSide is 0 or less). With clip, the image is cropped to that box of the screen first, and then scaled,
// so that the pixels of the box are all that is left. The clip must lie inside the decoded image. It keeps
// the decode guards of Shrink: an image with a side over 8192 pixels, or over 32 million pixels, is
// refused before it is decoded.
func ShrinkTo(data []byte, maxSide int, clip *Rect) ([]byte, error) {
	s, err := Render(data, ShotOptions{MaxSide: maxSide, Clip: clip})
	return s.Data, err
}

// ShotOptions say how Render makes a picture of a screenshot.
type ShotOptions struct {
	// MaxSide is the most that the longest side of the result may be. 0 or less means 1280.
	MaxSide int
	// Scale multiplies the size, after the clip and before MaxSide caps it. 0 means 1, and it must not be
	// above 1: a screenshot is never enlarged.
	Scale float64
	// Clip is the box of the screen to keep, or nil for all of it.
	Clip *Rect
	// PNG writes a PNG that keeps every pixel of the scaled image, instead of a JPEG.
	PNG bool
}

// Shot is a picture of a screenshot and how it maps to the screen.
type Shot struct {
	Data []byte
	// Width and Height are the size of the picture.
	Width, Height int
	// ScreenWidth and ScreenHeight are the size of the whole screenshot.
	ScreenWidth, ScreenHeight int
	// Origin is the top left corner of the part of the screen that the picture shows: (0, 0) with no clip.
	Origin Point
	// Ratio is how many screen pixels one pixel of the picture covers, at least 1.
	Ratio float64
}

// Render crops, scales, and encodes a PNG screenshot as ShotOptions say, and tells how the picture maps
// to the screen, so that a caller can turn a point of the picture into a point on the screen.
func Render(data []byte, o ShotOptions) (Shot, error) {
	if o.Scale < 0 || o.Scale > 1 {
		return Shot{}, fmt.Errorf("the scale %v is not between 0 and 1", o.Scale)
	}
	return render(data, renderOptions{maxSide: o.MaxSide, scale: o.Scale, clip: o.Clip, png: o.PNG, quality: 75})
}

type renderOptions struct {
	maxSide int
	scale   float64
	clip    *Rect
	// whole reduces by a whole factor, as Shrink always did.
	whole   bool
	png     bool
	quality int
}

func render(data []byte, o renderOptions) (Shot, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Shot{}, fmt.Errorf("the screenshot is not a PNG: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return Shot{}, errors.New("the screenshot has no size")
	}
	if cfg.Width > maxDecodeSide || cfg.Height > maxDecodeSide || cfg.Width*cfg.Height > maxDecodePixels {
		return Shot{}, fmt.Errorf("the screenshot is too large: %dx%d", cfg.Width, cfg.Height)
	}
	box := Rect{0, 0, cfg.Width, cfg.Height}
	if o.clip != nil {
		c := *o.clip
		if c.Empty() || c.Left < 0 || c.Top < 0 || c.Right > cfg.Width || c.Bottom > cfg.Height {
			return Shot{}, fmt.Errorf("the clip [%d,%d][%d,%d] is not inside the screen of %dx%d", c.Left, c.Top, c.Right, c.Bottom, cfg.Width, cfg.Height)
		}
		box = c
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return Shot{}, fmt.Errorf("the screenshot could not be read: %w", err)
	}
	if o.clip != nil {
		img = crop(img, box)
	}
	w, h := box.Width(), box.Height()
	maxSide := o.maxSide
	if maxSide <= 0 {
		maxSide = maxShotSide
	}
	if o.scale > 0 && o.scale < 1 {
		img = resize(img, max(int(float64(w)*o.scale+0.5), 1), max(int(float64(h)*o.scale+0.5), 1))
		w, h = img.Bounds().Dx(), img.Bounds().Dy()
	}
	if longest := max(w, h); longest > maxSide {
		if o.whole {
			f := (longest + maxSide - 1) / maxSide
			img = boxDown(img, f)
		} else {
			img = resize(img, max(w*maxSide/longest, 1), max(h*maxSide/longest, 1))
		}
	}
	shot := Shot{Width: img.Bounds().Dx(), Height: img.Bounds().Dy(), ScreenWidth: cfg.Width, ScreenHeight: cfg.Height, Origin: Point{box.Left, box.Top}}
	shot.Ratio = float64(box.Width()) / float64(shot.Width)
	var out bytes.Buffer
	if o.png {
		err = png.Encode(&out, img)
	} else {
		err = jpeg.Encode(&out, img, &jpeg.Options{Quality: o.quality})
	}
	if err != nil {
		return Shot{}, fmt.Errorf("the screenshot could not be written: %w", err)
	}
	shot.Data = out.Bytes()
	return shot, nil
}

// crop copies a box of the image into a new image whose origin is (0, 0).
func crop(src image.Image, box Rect) image.Image {
	b := src.Bounds()
	r := image.Rect(b.Min.X+box.Left, b.Min.Y+box.Top, b.Min.X+box.Right, b.Min.Y+box.Bottom)
	dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(dst, dst.Bounds(), src, r.Min, draw.Src)
	return dst
}

// resize makes an image of w by h pixels, and each new pixel is the mean of the box of old pixels that it
// covers. It only makes an image smaller, or the same size.
func resize(src image.Image, w, h int) image.Image {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if w >= sw && h >= sh {
		return src
	}
	w, h = min(w, sw), min(h, sh)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if rgba, ok := src.(*image.RGBA); ok {
		resizeRGBA(rgba, dst)
		return dst
	}
	for y := 0; y < h; y++ {
		y0, y1 := y*sh/h, max((y+1)*sh/h, y*sh/h+1)
		for x := 0; x < w; x++ {
			x0, x1 := x*sw/w, max((x+1)*sw/w, x*sw/w+1)
			var r, g, bl, a, n uint32
			for sy := y0; sy < y1 && sy < sh; sy++ {
				for sx := x0; sx < x1 && sx < sw; sx++ {
					pr, pg, pb, pa := src.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
					r, g, bl, a, n = r+pr, g+pg, bl+pb, a+pa, n+1
				}
			}
			if n == 0 {
				continue
			}
			dst.SetRGBA(x, y, color.RGBA{R: uint8((r / n) >> 8), G: uint8((g / n) >> 8), B: uint8((bl / n) >> 8), A: uint8((a / n) >> 8)})
		}
	}
	return dst
}

// boxDown reduces the image by an integer factor. A pixel of the result is the mean of f by f pixels.
func boxDown(src image.Image, f int) image.Image {
	b := src.Bounds()
	w, h := max(b.Dx()/f, 1), max(b.Dy()/f, 1)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var r, g, bl, a, n uint32
			for dy := 0; dy < f; dy++ {
				for dx := 0; dx < f; dx++ {
					sx, sy := b.Min.X+x*f+dx, b.Min.Y+y*f+dy
					if sx >= b.Max.X || sy >= b.Max.Y {
						continue
					}
					pr, pg, pb, pa := src.At(sx, sy).RGBA()
					r, g, bl, a, n = r+pr, g+pg, bl+pb, a+pa, n+1
				}
			}
			if n == 0 {
				continue
			}
			dst.SetRGBA(x, y, color.RGBA{R: uint8((r / n) >> 8), G: uint8((g / n) >> 8), B: uint8((bl / n) >> 8), A: uint8((a / n) >> 8)})
		}
	}
	return dst
}

// resizeRGBA is resize for the common case of an RGBA source, which reads the pixels directly.
func resizeRGBA(src, dst *image.RGBA) {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	w, h := dst.Bounds().Dx(), dst.Bounds().Dy()
	for y := 0; y < h; y++ {
		y0, y1 := y*sh/h, max((y+1)*sh/h, y*sh/h+1)
		for x := 0; x < w; x++ {
			x0, x1 := x*sw/w, max((x+1)*sw/w, x*sw/w+1)
			var r, g, b, a, n uint32
			for sy := y0; sy < y1 && sy < sh; sy++ {
				i := src.PixOffset(src.Rect.Min.X+x0, src.Rect.Min.Y+sy)
				for sx := x0; sx < x1 && sx < sw; sx++ {
					r, g, b, a, n = r+uint32(src.Pix[i]), g+uint32(src.Pix[i+1]), b+uint32(src.Pix[i+2]), a+uint32(src.Pix[i+3]), n+1
					i += 4
				}
			}
			if n == 0 {
				continue
			}
			o := dst.PixOffset(x, y)
			dst.Pix[o], dst.Pix[o+1], dst.Pix[o+2], dst.Pix[o+3] = uint8(r/n), uint8(g/n), uint8(b/n), uint8(a/n)
		}
	}
}
