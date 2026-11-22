package mobile

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
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
// the old ones. It never decodes an image with a side over 8192 pixels.
func Shrink(data []byte) ([]byte, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("the screenshot is not a PNG: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, errors.New("the screenshot has no size")
	}
	if cfg.Width > maxDecodeSide || cfg.Height > maxDecodeSide || cfg.Width*cfg.Height > maxDecodePixels {
		return nil, fmt.Errorf("the screenshot is too large: %dx%d", cfg.Width, cfg.Height)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("the screenshot could not be read: %w", err)
	}
	if f := factor(cfg.Width, cfg.Height); f > 1 {
		img = boxDown(img, f)
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, fmt.Errorf("the screenshot could not be written: %w", err)
	}
	return out.Bytes(), nil
}

// factor is the smallest whole number that brings the longest side to at most maxShotSide.
func factor(w, h int) int {
	longest := max(w, h)
	return (longest + maxShotSide - 1) / maxShotSide
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
