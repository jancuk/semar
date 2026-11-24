package mobilemcp

import (
	"context"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/mobilerec"
)

const (
	// flutterRetries is how many times a screen that is an empty Flutter view is read again.
	flutterRetries = 3
	// flutterInterval is the wait between two reads of such a screen.
	flutterInterval = 400 * time.Millisecond
	// launchWarmBudget bounds the wait after a launch: the first frame of a debug build is slow.
	launchWarmBudget = 10 * time.Second
)

// flutterViewClasses are the classes of the view that a Flutter app draws in.
var flutterViewClasses = []string{"io.flutter.embedding.android.FlutterView", "io.flutter.view.FlutterView"}

func sleepFor(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// flutterViews lists the Flutter views of the tree.
func flutterViews(t *mobile.Tree) []*mobile.Node {
	if t == nil || t.Root == nil {
		return nil
	}
	var out []*mobile.Node
	t.Walk(func(n *mobile.Node) bool {
		for _, c := range flutterViewClasses {
			if n.Class == c {
				out = append(out, n)
				break
			}
		}
		return true
	})
	return out
}

// isFlutter says whether the tree shows a Flutter view.
func isFlutter(t *mobile.Tree) bool { return len(flutterViews(t)) > 0 }

// flutterEmpty says whether the tree has a Flutter view with nothing inside it that a person could act
// on. Flutter builds its semantics lazily, so a first read can be one view with no children.
func flutterEmpty(t *mobile.Tree, hints mobile.WindowHints) bool {
	views := flutterViews(t)
	if len(views) == 0 {
		return false
	}
	_, nodes := mobilerec.ListElements(t, hints)
	for _, v := range views {
		for _, n := range nodes {
			if descendantOf(n, v) {
				return false
			}
		}
	}
	return true
}

func descendantOf(n, anc *mobile.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p == anc {
			return true
		}
	}
	return false
}

// emptyFlutter is flutterEmpty with the hints that the device gives. It asks the device for them only
// when the tree has a Flutter view at all.
func (m *Manager) emptyFlutter(ctx context.Context, dev Device, t *mobile.Tree) bool {
	if !isFlutter(t) {
		return false
	}
	return flutterEmpty(t, m.hints(ctx, dev, t))
}

// emptyDocument says whether the list has a WebView that shows nothing yet: an element of the role
// document in the app window that covers a tenth of the screen or more, with no element after it (the
// list is in document order, so its children come next) inside its bounds. w and h are the size of the
// screen.
func emptyDocument(els []mobilerec.Element, w, h int) bool {
	if w <= 0 || h <= 0 {
		return false
	}
	for i, d := range els {
		if d.Role != "document" || (d.Window != "" && d.Window != string(mobile.WindowApp)) {
			continue
		}
		if d.Bounds.Width()*d.Bounds.Height() < w*h/10 {
			continue
		}
		filled := false
		for _, e := range els[i+1:] {
			if (e.Window == "" || e.Window == string(mobile.WindowApp)) && inside(e.Bounds, d.Bounds) {
				filled = true
				break
			}
		}
		if !filled {
			return true
		}
	}
	return false
}

// inside says whether the box a lies within the box b.
func inside(a, b mobile.Rect) bool {
	return a.Left >= b.Left && a.Top >= b.Top && a.Right <= b.Right && a.Bottom <= b.Bottom
}

// emptyWebView is emptyDocument for a tree. It walks the tree for a node of the role document first, and
// builds the list of elements only when there is one.
func (m *Manager) emptyWebView(ctx context.Context, dev Device, t *mobile.Tree) bool {
	if t == nil || t.Root == nil {
		return false
	}
	found := false
	t.Walk(func(n *mobile.Node) bool {
		if n.Role == "document" {
			found = true
		}
		return !found
	})
	if !found {
		return false
	}
	els, _ := mobilerec.ListElements(t, m.hints(ctx, dev, t))
	return emptyDocument(els, t.Width, t.Height)
}

// sourceWarm reads the screen, and reads it again, up to three times 400 ms apart, while it is a
// Flutter view or a WebView with nothing in it. Any other screen is read once.
func (m *Manager) sourceWarm(ctx context.Context, dev Device) (*mobile.Tree, error) {
	t, err := dev.Source(ctx)
	if err != nil {
		return nil, err
	}
	for i := 0; i < flutterRetries && (m.emptyFlutter(ctx, dev, t) || m.emptyWebView(ctx, dev, t)); i++ {
		if err := m.sleep(ctx, flutterInterval); err != nil {
			return nil, err
		}
		if t, err = dev.Source(ctx); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// warmAfterLaunch waits, for at most 10 seconds in all, until a Flutter app that was just started shows
// something. It gives how long it waited. It reads the screen as a courtesy and never fails: an error
// of the device or a cancelled context ends the wait.
//
// The 10 seconds are wall-clock time: the clock of the Manager measures them, and a context with a
// deadline cuts a read that is still running at the end of them. A dump of the UI tree can take seconds
// on a busy device, so counting the pauses alone would let the loop hold the lock of the Manager for far
// longer.
func (m *Manager) warmAfterLaunch(ctx context.Context, dev Device) time.Duration {
	ctx, cancel := context.WithTimeout(ctx, launchWarmBudget)
	defer cancel()
	start := m.clock()
	var slept time.Duration
	elapsed := func() time.Duration { return max(m.clock().Sub(start), slept) }
	for {
		t, err := dev.Source(ctx)
		if err != nil || !m.emptyFlutter(ctx, dev, t) || elapsed() >= launchWarmBudget {
			return elapsed()
		}
		if err := m.sleep(ctx, flutterInterval); err != nil {
			return elapsed()
		}
		slept += flutterInterval
	}
}
