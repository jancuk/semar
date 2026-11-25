package mobile_test

import (
	"context"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
)

func a11yNode(pkg, class, role, id, text, desc, hint string, bounds mobile.Rect, attrs map[string]string, kids ...*mobile.Node) *mobile.Node {
	if attrs == nil {
		attrs = map[string]string{}
	}
	return &mobile.Node{
		Package: pkg, Class: class, Role: role, ID: id, Text: text, Desc: desc, Hint: hint,
		Enabled: true, Bounds: bounds, Attrs: attrs, Children: kids,
	}
}

func a11yTree(wins ...*mobile.Node) *mobile.Tree {
	root := &mobile.Node{Class: "hierarchy", Enabled: true, Bounds: mobile.Rect{Right: 1080, Bottom: 2400}, Children: wins}
	t := &mobile.Tree{Root: root, Width: 1080, Height: 2400}
	t.Finish()
	return t
}

func TestCheckAccessibilityCleanScreenHasNoIssues(t *testing.T) {
	// At 320 dpi, 48dp = 96px. A 200x120 button is 100x60dp.
	app := a11yNode("com.shop", "android.widget.FrameLayout", "", "", "", "", "", mobile.Rect{Right: 1080, Bottom: 2400}, nil,
		a11yNode("com.shop", "android.widget.Button", "button", "com.shop:id/ok", "Continue", "", "", mobile.Rect{Left: 100, Top: 400, Right: 300, Bottom: 520}, map[string]string{"clickable": "true"}),
		a11yNode("com.shop", "android.widget.EditText", "textbox", "com.shop:id/pw", "", "", "Password", mobile.Rect{Left: 100, Top: 600, Right: 900, Bottom: 720}, map[string]string{"clickable": "true", "editable": "true"}),
		a11yNode("com.shop", "android.widget.TextView", "text", "", "", "", "", mobile.Rect{Left: 100, Top: 800, Right: 120, Bottom: 820}, nil), // non-interactive
	)
	app.Children[1].Password = true
	tree := a11yTree(app)

	if got := mobile.CheckAccessibility(tree, mobile.WindowHints{App: "com.shop"}, mobile.A11yOptions{Density: 320}); len(got) != 0 {
		t.Errorf("clean screen produced issues: %+v", got)
	}
}

func TestCheckAccessibilityMissingLabelAndSmallTarget(t *testing.T) {
	// Density 320 dpi => 48dp = 96px.
	unlabeled := a11yNode("com.shop", "android.widget.ImageButton", "button", "com.shop:id/icon", "", "", "", mobile.Rect{Left: 100, Top: 200, Right: 220, Bottom: 320}, map[string]string{"clickable": "true"})
	small := a11yNode("com.shop", "android.widget.Button", "button", "com.shop:id/close", "", "Close", "", mobile.Rect{Left: 400, Top: 200, Right: 464, Bottom: 264}, map[string]string{"clickable": "true"}) // 64x64px = 32x32dp
	disabled := a11yNode("com.shop", "android.widget.ImageButton", "button", "com.shop:id/off", "", "", "", mobile.Rect{Left: 500, Top: 200, Right: 540, Bottom: 240}, map[string]string{"clickable": "true"})
	disabled.Enabled = false
	backdrop := a11yNode("com.shop", "android.widget.FrameLayout", "", "com.shop:id/bg", "", "", "", mobile.Rect{Left: 0, Top: 0, Right: 1080, Bottom: 2400}, map[string]string{"clickable": "true"})

	// Wrapper + child with identical bounds should deduplicate.
	dupChild := a11yNode("com.shop", "android.widget.ImageView", "img", "", "", "", "", mobile.Rect{Left: 200, Top: 500, Right: 260, Bottom: 560}, map[string]string{"clickable": "true"})
	dupWrap := a11yNode("com.shop", "android.widget.FrameLayout", "", "com.shop:id/dup", "", "", "", mobile.Rect{Left: 200, Top: 500, Right: 260, Bottom: 560}, map[string]string{"clickable": "true"}, dupChild)

	// Keyboard and system windows must be ignored.
	kb := a11yNode("com.kb", "android.widget.FrameLayout", "", "", "", "", "", mobile.Rect{Top: 1800, Right: 1080, Bottom: 2400}, nil,
		a11yNode("com.kb", "android.widget.Button", "button", "", "", "", "", mobile.Rect{Left: 10, Top: 1850, Right: 50, Bottom: 1890}, map[string]string{"clickable": "true"}),
	)
	sys := a11yNode("com.android.systemui", "android.widget.FrameLayout", "", "", "", "", "", mobile.Rect{Right: 1080, Bottom: 80}, nil,
		a11yNode("com.android.systemui", "android.widget.ImageView", "", "", "", "", "", mobile.Rect{Left: 10, Top: 10, Right: 40, Bottom: 40}, map[string]string{"clickable": "true"}),
	)

	app := a11yNode("com.shop", "android.widget.FrameLayout", "", "", "", "", "", mobile.Rect{Right: 1080, Bottom: 2400}, nil,
		backdrop, unlabeled, small, disabled, dupWrap,
	)
	tree := a11yTree(app, kb, sys)

	got := mobile.CheckAccessibility(tree, mobile.WindowHints{App: "com.shop", IME: "com.kb"}, mobile.A11yOptions{Density: 320})
	// Expect:
	// 1. unlabeled: missing_label (120x120px = 60x60dp, not small)
	// 2. small: small_target (32x32dp, labeled "Close")
	// 3. dupWrap: missing_label + small_target (30x30dp), deduplicated against dupChild
	if len(got) != 4 {
		t.Fatalf("got %d issues, want 4: %+v", len(got), got)
	}
	if got[0].Kind != mobile.A11yMissingLabel || got[0].ResourceID != "com.shop:id/icon" {
		t.Errorf("issue 0 = %+v", got[0])
	}
	if got[1].Kind != mobile.A11ySmallTarget || got[1].Label != "Close" || got[1].WidthDP != 32 || got[1].HeightDP != 32 {
		t.Errorf("issue 1 = %+v", got[1])
	}
	if got[2].Kind != mobile.A11yMissingLabel || got[2].ResourceID != "com.shop:id/dup" {
		t.Errorf("issue 2 = %+v", got[2])
	}
	if got[3].Kind != mobile.A11ySmallTarget || got[3].WidthDP != 30 || got[3].HeightDP != 30 {
		t.Errorf("issue 3 = %+v", got[3])
	}

	// Density 0 skips small_target checks, keeping only missing_label.
	noDensity := mobile.CheckAccessibility(tree, mobile.WindowHints{App: "com.shop", IME: "com.kb"}, mobile.A11yOptions{Density: 0})
	if len(noDensity) != 2 {
		t.Fatalf("density=0 gave %d issues, want 2: %+v", len(noDensity), noDensity)
	}
	for _, iss := range noDensity {
		if iss.Kind != mobile.A11yMissingLabel {
			t.Errorf("unexpected kind with density=0: %+v", iss)
		}
	}
}

func TestCheckAccessibilityEdgeClippingAndLimit(t *testing.T) {
	// Density 320 dpi => 48dp = 96px.
	// Clipped vertically at screen bottom (height 40px < 96px, width 200px >= 96px): skip!
	clippedBottom := a11yNode("com.shop", "android.widget.Button", "button", "", "Row", "", "", mobile.Rect{Left: 100, Top: 2360, Right: 300, Bottom: 2400}, map[string]string{"clickable": "true"})
	// Clipped horizontally inside a scrollable container (width 40px < 96px, height 120px >= 96px): skip!
	scrollItem := a11yNode("com.shop", "android.widget.Button", "button", "", "Chip", "", "", mobile.Rect{Left: 960, Top: 400, Right: 1000, Bottom: 520}, map[string]string{"clickable": "true"})
	scroller := a11yNode("com.shop", "androidx.recyclerview.widget.RecyclerView", "list", "", "", "", "", mobile.Rect{Left: 100, Top: 300, Right: 1000, Bottom: 600}, nil, scrollItem)
	scroller.Scrollable = true
	// Clipped horizontally at screen edge, BUT ALSO too short vertically (not clipped vertically): MUST flag!
	clippedHShortV := a11yNode("com.shop", "android.widget.Button", "button", "", "Tiny", "", "", mobile.Rect{Left: 0, Top: 800, Right: 40, Bottom: 840}, map[string]string{"clickable": "true"})

	app := a11yNode("com.shop", "android.widget.FrameLayout", "", "", "", "", "", mobile.Rect{Right: 1080, Bottom: 2400}, nil,
		clippedBottom, scroller, clippedHShortV,
	)
	tree := a11yTree(app)

	got := mobile.CheckAccessibility(tree, mobile.WindowHints{App: "com.shop"}, mobile.A11yOptions{Density: 320})
	if len(got) != 1 || got[0].Label != "Tiny" {
		t.Errorf("got = %+v, want only Tiny", got)
	}

	// Limit caps the result deterministically.
	var many []*mobile.Node
	for i := 0; i < 10; i++ {
		many = append(many, a11yNode("com.shop", "android.widget.ImageButton", "button", "", "", "", "", mobile.Rect{Left: 100, Top: 100 + i*150, Right: 220, Bottom: 220 + i*150}, map[string]string{"clickable": "true"}))
	}
	capped := mobile.CheckAccessibility(a11yTree(a11yNode("com.shop", "android.widget.FrameLayout", "", "", "", "", "", mobile.Rect{Right: 1080, Bottom: 2400}, nil, many...)),
		mobile.WindowHints{App: "com.shop"}, mobile.A11yOptions{Density: 320, Limit: 3})
	if len(capped) != 3 {
		t.Errorf("capped len = %d, want 3", len(capped))
	}
}

func TestCheckAccessibility48dpRoundingConsistencyAt411DPI(t *testing.T) {
	// At 411 dpi, 48dp = 48 * 411 / 160 = 123.3px -> Android lays out 123px (rounds to 48dp -> must pass).
	// 122px rounds to 47dp -> must be flagged and report 47x47dp (never "48x48dp, min 48dp").
	okBtn := a11yNode("com.shop", "android.widget.Button", "button", "com.shop:id/ok48", "OK", "", "", mobile.Rect{Left: 100, Top: 200, Right: 223, Bottom: 323}, map[string]string{"clickable": "true"})
	smallBtn := a11yNode("com.shop", "android.widget.Button", "button", "com.shop:id/small47", "Small", "", "", mobile.Rect{Left: 400, Top: 200, Right: 522, Bottom: 322}, map[string]string{"clickable": "true"})
	tree := a11yTree(a11yNode("com.shop", "android.widget.FrameLayout", "", "", "", "", "", mobile.Rect{Right: 1080, Bottom: 2400}, nil, okBtn, smallBtn))

	got := mobile.CheckAccessibility(tree, mobile.WindowHints{App: "com.shop"}, mobile.A11yOptions{Density: 411})
	if len(got) != 1 {
		t.Fatalf("got %d issues, want 1: %+v", len(got), got)
	}
	if got[0].Kind != mobile.A11ySmallTarget || got[0].ResourceID != "com.shop:id/small47" || got[0].WidthDP != 47 || got[0].HeightDP != 47 {
		t.Errorf("issue = %+v, want small47 47x47dp", got[0])
	}
	if got[0].Node != smallBtn {
		t.Errorf("issue.Node = %p, want %p", got[0].Node, smallBtn)
	}
}

type densityPlatform struct {
	*fakePlatform
	density int
}

func (p *densityPlatform) ScreenDensity(context.Context) int { return p.density }

func TestEngineAttachesAccessibilityFindingsWithNoExtraSourceCall(t *testing.T) {
	r := newRig(t, rigOptions{}, func(o *mobile.Options) {
		o.Platform = &densityPlatform{fakePlatform: o.Platform.(*fakePlatform), density: 320}
		o.AccessibilityCheck = true
	})
	badBtn := a11yNode("com.example.shop", "android.widget.ImageButton", "button", "com.example.shop:id/tiny", "", "", "", mobile.Rect{Left: 100, Top: 400, Right: 160, Bottom: 460}, map[string]string{"clickable": "true"})
	r.d.screen = a11yTree(a11yNode("com.example.shop", "android.widget.FrameLayout", "", "", "", "", "", mobile.Rect{Right: 1080, Bottom: 2400}, nil, badBtn))

	res := r.do(call("launch", domain.MobileAction{Service: "shop-app", App: "shop-app", Screen: "Home", Do: domain.MobileLaunch}, nil))
	if res.Action.Err != "" {
		t.Fatalf("unexpected err: %s", res.Action.Err)
	}
	if len(res.Action.Mobile.Accessibility) != 2 {
		t.Fatalf("accessibility = %+v, want 2 issues (missing_label + small_target)", res.Action.Mobile.Accessibility)
	}
	if res.Action.Mobile.Accessibility[0].Screen != "Home" || res.Action.Mobile.Accessibility[0].Kind != "missing_label" {
		t.Errorf("first = %+v", res.Action.Mobile.Accessibility[0])
	}
	if res.Action.Mobile.Accessibility[1].Kind != "small_target" || res.Action.Mobile.Accessibility[1].WidthDP != 30 {
		t.Errorf("second = %+v", res.Action.Mobile.Accessibility[1])
	}
	// settle reads twice (to confirm equal digest); accessibility check must add zero extra Source calls.
	if r.d.sourceCalls != 2 {
		t.Errorf("sourceCalls = %d, want 2 (no extra read for accessibility)", r.d.sourceCalls)
	}

	// With AccessibilityCheck off, nothing is attached.
	off := newRig(t, rigOptions{}, func(o *mobile.Options) {
		o.Platform = &densityPlatform{fakePlatform: &fakePlatform{d: r.d}, density: 320}
		o.AccessibilityCheck = false
	})
	offRes := off.do(call("launch", domain.MobileAction{Service: "shop-app", App: "shop-app", Screen: "Home", Do: domain.MobileLaunch}, nil))
	if len(offRes.Action.Mobile.Accessibility) != 0 {
		t.Errorf("off mode recorded issues: %+v", offRes.Action.Mobile.Accessibility)
	}
}
