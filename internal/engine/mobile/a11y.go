package mobile

import (
	"fmt"
	"strings"

	"github.com/jancuk/semar/internal/domain"
)

// A11yKind names one accessibility problem found on a mobile screen.
type A11yKind string

const (
	// A11yMissingLabel is an interactive control with no accessible label (text, content-desc, or
	// hint on an editable/password field).
	A11yMissingLabel A11yKind = "missing_label"
	// A11ySmallTarget is an interactive control whose width or height is smaller than MinTouchTargetDP
	// and not clipped by the screen edge or a scrollable container.
	A11ySmallTarget A11yKind = "small_target"

	// MinTouchTargetDP is the Material / WCAG minimum touch target size in density-independent pixels.
	MinTouchTargetDP = 48

	defaultA11yLimit = 20
)

// A11yOptions tunes CheckAccessibility.
type A11yOptions struct {
	// Density is the screen density in dots per inch. Zero skips small-target checks.
	Density int
	// Limit bounds how many issues are returned. Zero uses defaultA11yLimit (20).
	Limit int
}

// A11yIssue is one accessibility problem found on a mobile UI tree.
type A11yIssue struct {
	Kind       A11yKind
	Node       *Node
	Class      string
	Role       string
	ResourceID string
	Label      string
	Bounds     Rect
	WidthDP    int
	HeightDP   int
}

type a11yKey struct {
	bounds Rect
	kind   A11yKind
}

// CheckAccessibility inspects the app window(s) of a finished UI tree for zero-model accessibility
// issues: unlabeled interactive controls and touch targets smaller than 48dp. Results are returned in
// deterministic pre-order traversal order, capped at opts.Limit (default 20).
func CheckAccessibility(t *Tree, hints WindowHints, opts A11yOptions) []A11yIssue {
	if t == nil || t.Root == nil {
		return nil
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultA11yLimit
	}
	ws := Windows(t, hints)
	anyPkg := false
	for _, w := range ws {
		if w.Package != "" {
			anyPkg = true
			break
		}
	}
	screen := screenRect(t)
	var screenArea int64
	if !screen.Empty() {
		screenArea = int64(screen.Width()) * int64(screen.Height())
	}
	seen := map[a11yKey]bool{}
	var issues []A11yIssue

	t.Walk(func(n *Node) bool {
		if len(issues) >= limit {
			return false
		}
		if anyPkg {
			w := WindowOf(n, ws)
			if w == nil || w.Kind != WindowApp {
				return true
			}
		}
		if !n.Enabled || !n.Visible || n.Bounds.Empty() || !isInteractive(n) {
			return true
		}
		w, h := n.Bounds.Width(), n.Bounds.Height()
		// Exclude large backdrop/root containers covering more than 40% of the screen.
		if screenArea > 0 && int64(w)*int64(h)*100 > screenArea*40 {
			return true
		}

		if !hasAccessibleLabel(n) {
			k := a11yKey{bounds: n.Bounds, kind: A11yMissingLabel}
			if !seen[k] {
				seen[k] = true
				issues = append(issues, A11yIssue{
					Kind:       A11yMissingLabel,
					Node:       n,
					Class:      n.Class,
					Role:       n.Role,
					ResourceID: n.ID,
					Bounds:     n.Bounds,
				})
				if len(issues) >= limit {
					return false
				}
			}
		}

		if opts.Density > 0 {
			widthDP := pxToDP(w, opts.Density)
			heightDP := pxToDP(h, opts.Density)
			wSmall := widthDP < MinTouchTargetDP
			hSmall := heightDP < MinTouchTargetDP
			if wSmall || hSmall {
				clippedH, clippedV := edgeClipped(n, screen)
				if (wSmall && !clippedH) || (hSmall && !clippedV) {
					k := a11yKey{bounds: n.Bounds, kind: A11ySmallTarget}
					if !seen[k] {
						seen[k] = true
						issues = append(issues, A11yIssue{
							Kind:       A11ySmallTarget,
							Node:       n,
							Class:      n.Class,
							Role:       n.Role,
							ResourceID: n.ID,
							Label:      n.AccName(),
							Bounds:     n.Bounds,
							WidthDP:    widthDP,
							HeightDP:   heightDP,
						})
						if len(issues) >= limit {
							return false
						}
					}
				}
			}
		}
		return true
	})

	return issues
}

func isInteractive(n *Node) bool {
	if n.Attrs == nil {
		return false
	}
	return n.Attrs["clickable"] == "true" || n.Attrs["long-clickable"] == "true" || n.Attrs["checkable"] == "true"
}

func hasAccessibleLabel(n *Node) bool {
	if strings.TrimSpace(n.Text) != "" || strings.TrimSpace(n.Desc) != "" {
		return true
	}
	if n.Password || n.Role == "textbox" || (n.Attrs != nil && n.Attrs["editable"] == "true") {
		return strings.TrimSpace(n.Hint) != ""
	}
	return false
}

func edgeClipped(n *Node, screen Rect) (horiz, vert bool) {
	if !screen.Empty() {
		if n.Bounds.Left <= screen.Left || n.Bounds.Right >= screen.Right {
			horiz = true
		}
		if n.Bounds.Top <= screen.Top || n.Bounds.Bottom >= screen.Bottom {
			vert = true
		}
	}
	for anc := n.Parent; anc != nil; anc = anc.Parent {
		if !anc.Scrollable || anc.Bounds.Empty() {
			continue
		}
		if n.Bounds.Left <= anc.Bounds.Left || n.Bounds.Right >= anc.Bounds.Right {
			horiz = true
		}
		if n.Bounds.Top <= anc.Bounds.Top || n.Bounds.Bottom >= anc.Bounds.Bottom {
			vert = true
		}
	}
	return horiz, vert
}

func pxToDP(px, density int) int {
	return (px*160 + density/2) / density
}

func formatRect(r Rect) string {
	return fmt.Sprintf("[%d,%d][%d,%d]", r.Left, r.Top, r.Right, r.Bottom)
}

func toDomainA11y(screen string, issues []A11yIssue) []domain.AccessibilityIssue {
	if len(issues) == 0 {
		return nil
	}
	out := make([]domain.AccessibilityIssue, len(issues))
	for i, iss := range issues {
		out[i] = domain.AccessibilityIssue{
			Screen:     screen,
			Kind:       string(iss.Kind),
			Class:      iss.Class,
			Role:       iss.Role,
			ResourceID: iss.ResourceID,
			Label:      iss.Label,
			Bounds:     formatRect(iss.Bounds),
			WidthDP:    iss.WidthDP,
			HeightDP:   iss.HeightDP,
		}
	}
	return out
}
