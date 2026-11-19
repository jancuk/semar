package web

import (
	"context"
	"net/url"
	"slices"
	"sync"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"github.com/jancuk/semar/internal/hostrule"
)

// hostWatch notes the hosts that the pages of a window ask for and that the environment does not
// list. It blocks nothing. It holds a host and never a path, because a path can hold a value.
type hostWatch struct {
	allowed []string

	mu       sync.Mutex
	noted    map[string]bool
	attached map[target.ID]context.CancelFunc
	own      target.ID
	opened   chan *target.Info
	stop     chan struct{}
	stopOnce sync.Once
}

// newHostWatch is a watch for an environment that lists the hosts allowed. A nil watch notes nothing.
func newHostWatch(allowed []string) *hostWatch {
	return &hostWatch{allowed: allowed, noted: map[string]bool{}, attached: map[target.ID]context.CancelFunc{},
		opened: make(chan *target.Info, 64), stop: make(chan struct{})}
}

// watch listens to the first page, which tab is, and turns its network events on before it goes
// anywhere.
func (w *hostWatch) watch(tab context.Context) error {
	if c := chromedp.FromContext(tab); c != nil && c.Target != nil {
		w.own = c.Target.TargetID
	}
	if err := chromedp.Listen(tab, w.onEvent); err != nil {
		return err
	}
	// A page that another page opens starts to load at once, so the browser tells of it as it is made,
	// and the look at the targets that the wait does is only the second net.
	if err := chromedp.ListenBrowser(tab, w.onBrowserEvent); err != nil {
		return err
	}
	go func() {
		for {
			select {
			case <-w.stop:
				return
			case <-tab.Done():
				return
			case info := <-w.opened:
				w.attach(tab, info)
			}
		}
	}()
	_, err := chromedp.Call(tab, network.Enable, network.EnableParams{})
	return err
}

func (w *hostWatch) onBrowserEvent(ev any) {
	created, ok := ev.(*target.EventTargetCreated)
	if !ok || created.TargetInfo == nil || (created.TargetInfo.Type != "page" && created.TargetInfo.Type != "iframe") {
		return
	}
	select {
	case w.opened <- created.TargetInfo:
	default:
	}
}

// earlyRequests is every address that a page has asked for since it began: the page that a script opens
// has sent some requests before Semar is attached to it.
const earlyRequests = `[location.href, ...performance.getEntriesByType('navigation').map(e => e.name), ...performance.getEntriesByType('resource').map(e => e.name)]`

// attach starts to watch a page or a frame of the browser that tab belongs to, once. It does not wait
// for the page, so a page that does not answer cannot hold up the look for the window.
func (w *hostWatch) attach(tab context.Context, info *target.Info) {
	if w == nil || info.TargetID == w.own {
		return
	}
	w.mu.Lock()
	if _, done := w.attached[info.TargetID]; done {
		w.mu.Unlock()
		return
	}
	c, cancel := chromedp.NewContext(tab, chromedp.WithTargetID(info.TargetID), chromedp.WithDetachOnCancel(), chromedp.WithListener(w.onEvent))
	w.attached[info.TargetID] = cancel
	w.mu.Unlock()
	go func() {
		if _, err := chromedp.Call(c, network.Enable, network.EnableParams{}); err != nil {
			return
		}
		urls, err := chromedp.Run(c, chromedp.Evaluate[[]string](earlyRequests))
		if err != nil {
			return
		}
		for _, raw := range urls {
			w.note(raw)
		}
	}()
}

// release lets go of the pages that attach took.
func (w *hostWatch) release() {
	if w == nil {
		return
	}
	w.stopOnce.Do(func() { close(w.stop) })
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, cancel := range w.attached {
		cancel()
	}
}

// onEvent runs in the loop that reads the messages of a page. It must return fast and run no command.
func (w *hostWatch) onEvent(ev any) {
	if e, ok := ev.(*network.EventRequestWillBeSent); ok && e.Request != nil {
		w.note(e.Request.URL)
	}
}

func (w *hostWatch) note(raw string) {
	u, err := url.Parse(raw)
	if err != nil || hostrule.Allowed(u, w.allowed) {
		return
	}
	entry := hostrule.Entry(u)
	if entry == "" {
		return
	}
	w.mu.Lock()
	w.noted[entry] = true
	w.mu.Unlock()
}

// hosts are the noted hosts, sorted.
func (w *hostWatch) hosts() []string {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.noted))
	for h := range w.noted {
		out = append(out, h)
	}
	slices.Sort(out)
	return out
}
