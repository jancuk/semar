package mobile

// WindowKind says what a window of the screen is.
type WindowKind string

const (
	// WindowApp is the window of the app in front.
	WindowApp WindowKind = "app"
	// WindowKeyboard is the window of the input method.
	WindowKeyboard WindowKind = "keyboard"
	// WindowSystem is a window of the system: the status bar, the navigation bar, a system dialog.
	WindowSystem WindowKind = "system"
	// WindowOverlay is any other window: an app that draws over the others, a floating button.
	WindowOverlay WindowKind = "overlay"
)

// Window is one top-level window of the screen. With enableMultiWindows, the server gives each window as
// a child of the root of the hierarchy.
type Window struct {
	// Index is the place of the window among the children of the root.
	Index   int
	Package string
	Kind    WindowKind
	Root    *Node
	Bounds  Rect
}

// WindowHints are what the caller knows about the device, which the tree does not say.
type WindowHints struct {
	// App is the package of the app in front, or "".
	App string
	// IME is the package of the input method, or "".
	IME string
}

// Windows lists the top-level windows of the tree, in document order. A tree whose root has no child
// gives one window, the root itself, so that a caller never has to special-case it. The kind of a window
// follows from its package: the app in front is app, the input method is keyboard, com.android.systemui
// and android are system, and anything else is overlay. With no App hint, the first window that is not
// the keyboard or the system is the app.
func Windows(t *Tree, h WindowHints) []Window {
	if t == nil || t.Root == nil {
		return nil
	}
	tops := t.Root.Children
	if len(tops) == 0 {
		tops = []*Node{t.Root}
	}
	ws := make([]Window, 0, len(tops))
	for i, n := range tops {
		ws = append(ws, Window{Index: i, Package: windowPackage(n), Root: n, Bounds: n.Bounds})
	}
	app := h.App
	if app == "" {
		for _, w := range ws {
			if w.Package != "" && !isSystemPackage(w.Package) && (h.IME == "" || w.Package != h.IME) {
				app = w.Package
				break
			}
		}
	}
	for i := range ws {
		switch p := ws[i].Package; {
		case p != "" && p == app:
			ws[i].Kind = WindowApp
		case p != "" && p == h.IME:
			ws[i].Kind = WindowKeyboard
		case isSystemPackage(p):
			ws[i].Kind = WindowSystem
		default:
			ws[i].Kind = WindowOverlay
		}
	}
	return ws
}

func isSystemPackage(p string) bool { return p == "com.android.systemui" || p == "android" }

// windowPackage is the package of a window: its own, else that of the first node inside it that has one.
func windowPackage(n *Node) string {
	if n.Package != "" {
		return n.Package
	}
	var found string
	var walk func(*Node) bool
	walk = func(x *Node) bool {
		if x.Package != "" {
			found = x.Package
			return false
		}
		for _, k := range x.Children {
			if !walk(k) {
				return false
			}
		}
		return true
	}
	walk(n)
	return found
}

// WindowOf gives the window that holds the node, or nil when none does. It follows the parents of the
// node, so the tree must have been finished.
func WindowOf(n *Node, ws []Window) *Window {
	for p := n; p != nil; p = p.Parent {
		for i := range ws {
			if ws[i].Root == p {
				return &ws[i]
			}
		}
	}
	return nil
}
