package mobilemcp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/mcp"
)

const (
	defaultMaxSize = 1024
	minMaxSize     = 32
	maxMaxSize     = 8192
)

// clipArgs is the box of the screen that a screenshot keeps, in screen pixels.
type clipArgs struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

func (c *clipArgs) rect() (*mobile.Rect, error) {
	if c == nil {
		return nil, nil
	}
	if c.X < 0 || c.Y < 0 || c.Width <= 0 || c.Height <= 0 {
		return nil, errors.New("the clip needs x and y of 0 or more, and a width and a height above 0")
	}
	return &mobile.Rect{Left: c.X, Top: c.Y, Right: c.X + c.Width, Bottom: c.Y + c.Height}, nil
}

var clipSchema = mcp.Object(map[string]mcp.Schema{
	"x":      mcp.Int("Left edge of the box, in screen pixels.").With("minimum", 0),
	"y":      mcp.Int("Top edge of the box, in screen pixels.").With("minimum", 0),
	"width":  mcp.Int("Width of the box, in screen pixels.").With("minimum", 1),
	"height": mcp.Int("Height of the box, in screen pixels.").With("minimum", 1),
}).Required("x", "y", "width", "height")

// shotRequest is what a screenshot tool asks for.
type shotRequest struct {
	MaxSize int     // 0: the default
	Scale   float64 // 0: none
	Clip    *clipArgs
	PNG     bool
}

func (r shotRequest) check() (*mobile.Rect, error) {
	if r.MaxSize != 0 && (r.MaxSize < minMaxSize || r.MaxSize > maxMaxSize) {
		return nil, fmt.Errorf("maxSize must be %d to %d", minMaxSize, maxMaxSize)
	}
	if r.Scale < 0 || r.Scale > 1 {
		return nil, errors.New("scale must be above 0 and at most 1")
	}
	return r.Clip.rect()
}

// treeLeaks says whether the screen shows a value that the scrubber knows. The pixels of a picture cannot
// be scrubbed, so a screen that shows one is not captured.
func (m *Manager) treeLeaks(t *mobile.Tree) bool {
	leaks := false
	t.Walk(func(n *mobile.Node) bool {
		for _, v := range []string{n.ID, n.Desc, n.Text, n.Hint} {
			if m.scrub.Leaks(v) {
				leaks = true
				return false
			}
		}
		for _, v := range n.Attrs {
			if m.scrub.Leaks(v) {
				leaks = true
				return false
			}
		}
		return true
	})
	return leaks
}

// capture takes a screenshot and makes the picture. It refuses a screen that shows a secret.
func (m *Manager) capture(ctx context.Context, dev Device, r shotRequest) (mobile.Shot, error) {
	clip, err := r.check()
	if err != nil {
		return mobile.Shot{}, err
	}
	t, err := dev.Source(ctx)
	if err != nil {
		return mobile.Shot{}, err
	}
	if m.treeLeaks(t) {
		return mobile.Shot{}, errors.New("the screen shows a secret value: a picture of it would show the value. Hide it, and take the screenshot again")
	}
	data, err := dev.Screenshot(ctx)
	if err != nil {
		return mobile.Shot{}, err
	}
	max := r.MaxSize
	if max == 0 {
		max = defaultMaxSize
	}
	return mobile.Render(data, mobile.ShotOptions{MaxSide: max, Scale: r.Scale, Clip: clip, PNG: r.PNG})
}

// caption says how the picture maps to the screen, so that the model can turn a point of the picture into
// a point to tap.
func caption(s mobile.Shot) string {
	return fmt.Sprintf("image %dx%d of screen %dx%d: screen = image × %.3f; clip origin (%d,%d)",
		s.Width, s.Height, s.ScreenWidth, s.ScreenHeight, s.Ratio, s.Origin.X, s.Origin.Y)
}

// imageResult is the picture as image content with its caption.
func imageResult(s mobile.Shot) mcp.Result {
	return mcp.ImageResult(base64.StdEncoding.EncodeToString(s.Data), "image/jpeg", caption(s))
}
