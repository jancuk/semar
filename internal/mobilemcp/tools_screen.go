package mobilemcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jancuk/semar/internal/mcp"
)

func (m *Manager) screenTools() []*toolDef {
	sizeSchema := mcp.Int("The longest side of the picture in pixels. The default is 1024.").With("minimum", minMaxSize).With("maximum", maxMaxSize)
	scaleSchema := mcp.Number("A factor above 0 and at most 1 that makes the picture smaller, applied before maxSize.").With("minimum", 0).With("maximum", 1)
	return []*toolDef{
		{
			name: "mobile_take_screenshot", title: "Take a screenshot", readOnly: true,
			desc: "Take a screenshot as a JPEG image. A text block says how the image maps to the screen: screen = image × factor, plus the origin of the clip. Refused when the screen shows a secret value.",
			schema: mcp.Object(map[string]mcp.Schema{
				"maxSize": sizeSchema,
				"scale":   scaleSchema,
				"clip":    clipSchema,
			}),
			run: m.takeScreenshot,
		},
		{
			name: "mobile_save_screenshot", title: "Save a screenshot", readOnly: true,
			desc: "Take a screenshot and save it as a .png, .jpg, or .jpeg file in the output folder of the server. A path is relative to that folder. Refused when the screen shows a secret value.",
			schema: mcp.Object(map[string]mcp.Schema{
				"saveTo":  mcp.String("File name in the output folder, ending in .png, .jpg, or .jpeg."),
				"maxSize": mcp.Int("The longest side of the picture in pixels. The default is the full size.").With("minimum", minMaxSize).With("maximum", maxMaxSize),
				"scale":   scaleSchema,
			}).Required("saveTo"),
			run: m.saveScreenshot,
		},
		{
			name: "mobile_list_elements_on_screen", title: "List elements on screen", readOnly: true,
			desc: "List what is on the screen that you can act on, grouped by window (app, keyboard, system, overlay). Each element has a ref such as e3 that the tap, type, and swipe tools accept instead of x and y; a ref stays valid while the screen is unchanged, and is found again when it only moved. Passwords are never shown.",
			schema: mcp.Object(map[string]mcp.Schema{
				"format": mcp.Enum("text is a line per element (the default). json is the same facts as JSON.", "text", "json"),
				"raw":    mcp.Bool("Give the raw tree of the screen as JSON instead, with no refs."),
			}),
			run: m.listElements,
		},
	}
}

func (m *Manager) takeScreenshot(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		MaxSize int       `json:"maxSize"`
		Scale   float64   `json:"scale"`
		Clip    *clipArgs `json:"clip"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	shot, err := m.capture(ctx, dev, shotRequest{MaxSize: a.MaxSize, Scale: a.Scale, Clip: a.Clip})
	if err != nil {
		return mcp.Result{}, err
	}
	return imageResult(shot), nil
}

func (m *Manager) saveScreenshot(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		SaveTo  string  `json:"saveTo"`
		MaxSize int     `json:"maxSize"`
		Scale   float64 `json:"scale"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	if m.out == nil {
		return mcp.Result{}, errors.New("this server has no output folder, so it cannot save files")
	}
	exts := []string{".png", ".jpg", ".jpeg"}
	path, err := m.out.Resolve(a.SaveTo, exts)
	if err != nil {
		return mcp.Result{}, fmt.Errorf("saveTo is refused: %w", err)
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	req := shotRequest{MaxSize: a.MaxSize, Scale: a.Scale, PNG: strings.HasSuffix(strings.ToLower(path), ".png")}
	if req.MaxSize == 0 {
		req.MaxSize = maxMaxSize
	}
	shot, err := m.capture(ctx, dev, req)
	if err != nil {
		return mcp.Result{}, err
	}
	saved, err := m.out.Write(path, exts, shot.Data)
	if err != nil {
		return mcp.Result{}, err
	}
	return textJSON(map[string]any{
		"path": saved, "bytes": len(shot.Data), "width": shot.Width, "height": shot.Height,
		"screenWidth": shot.ScreenWidth, "screenHeight": shot.ScreenHeight,
	})
}

func (m *Manager) listElements(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Format string `json:"format"`
		Raw    bool   `json:"raw"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	format := strings.ToLower(strings.TrimSpace(a.Format))
	if format != "" && format != "text" && format != "json" {
		return mcp.Result{}, errors.New("format must be text or json")
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	if a.Raw {
		t, err := dev.Source(ctx)
		if err != nil {
			return mcp.Result{}, err
		}
		b, err := m.rawTree(t)
		if err != nil {
			return mcp.Result{}, err
		}
		return mcp.TextResult(string(b)), nil
	}
	l, err := m.list(ctx, dev)
	if err != nil {
		return mcp.Result{}, err
	}
	if format == "json" {
		b, err := l.json(m)
		if err != nil {
			return mcp.Result{}, err
		}
		return mcp.TextResult(string(b)), nil
	}
	return mcp.TextResult(l.text(m)), nil
}
