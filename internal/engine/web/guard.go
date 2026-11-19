package web

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/hostrule"
)

// guard decides, for every request that the page makes, whether the browser may send it, and keeps the
// count of requests that are still open, for the wait until the page has settled.
type guard struct {
	policy *effects.Policy
	span   func() string // 16 lower-case hex characters

	mu      sync.Mutex
	trace   string
	open    map[network.RequestID]bool
	lastNet time.Time
	blocked map[string]bool
	paused  chan *fetch.EventRequestPaused
	dialogs chan struct{}
}

func newGuard(policy *effects.Policy, span func() string) *guard {
	return &guard{
		policy:  policy,
		span:    span,
		open:    map[network.RequestID]bool{},
		blocked: map[string]bool{},
		paused:  make(chan *fetch.EventRequestPaused, 1024),
		dialogs: make(chan struct{}, 16),
	}
}

// begin starts an action: the requests of the page now carry this trace, and the hosts that were
// blocked are counted again from nothing.
func (g *guard) begin(trace string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.trace = trace
	g.blocked = map[string]bool{}
}

// blockedHosts lists the hosts that the browser refused to reach since begin, in order. A path is
// never listed: it can hold a value.
func (g *guard) blockedHosts() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Sorted(maps.Keys(g.blocked))
}

// idle reports how long ago a request last started or ended, and how many are open.
func (g *guard) idle(now time.Time) (quiet time.Duration, open int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.lastNet.IsZero() {
		return time.Hour, len(g.open)
	}
	return now.Sub(g.lastNet), len(g.open)
}

// onEvent runs in the loop that reads the messages of the tab. It must return fast and run no command.
func (g *guard) onEvent(ev any) {
	switch e := ev.(type) {
	case *network.EventRequestWillBeSent:
		if tracked(e.Type) {
			g.mu.Lock()
			g.open[e.RequestID] = true
			g.lastNet = time.Now()
			g.mu.Unlock()
		}
	case *network.EventLoadingFinished:
		g.done(e.RequestID)
	case *network.EventLoadingFailed:
		g.done(e.RequestID)
	case *fetch.EventRequestPaused:
		select {
		case g.paused <- e:
		default:
			// The queue is full, so the request is failed in the loop that serves it: no request is sent unjudged.
			go func() { g.paused <- e }()
		}
	case *page.EventJavascriptDialogOpening:
		select {
		case g.dialogs <- struct{}{}:
		default:
		}
	}
}

// tracked is true for the requests that a page waits for before it looks settled. A stream or a socket
// stays open for good, so it is not one of them.
func tracked(t network.ResourceType) bool {
	switch t {
	case network.ResourceTypeDocument, network.ResourceTypeScript, network.ResourceTypeStylesheet,
		network.ResourceTypeImage, network.ResourceTypeFont, network.ResourceTypeXHR, network.ResourceTypeFetch:
		return true
	}
	return false
}

func (g *guard) done(id network.RequestID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.open[id] {
		delete(g.open, id)
		g.lastNet = time.Now()
	}
}

// serve answers every paused request until ctx ends. It runs in its own goroutine, because it runs
// commands, and the loop of the tab must not.
func (g *guard) serve(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-g.paused:
			go g.answer(ctx, ev)
		case <-g.dialogs:
			// A dialog stops the page until it is answered. A step never accepts what a page asks, so it is dismissed.
			go func() {
				_, _ = chromedp.Call(ctx, page.HandleJavaScriptDialog, page.HandleJavaScriptDialogParams{Accept: false})
			}()
		}
	}
}

// requestVerdict is why the page may not send a request, or "" when it may. host is the hosts entry
// that would allow the request, and it is set only when the host is the reason: a request that the
// deny list or the read-only mode stops has no host to add.
func (g *guard) requestVerdict(method, raw string) (host string, refused string) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "the address of the request does not parse"
	}
	switch u.Scheme {
	case "data", "blob", "about":
		return "", ""
	}
	if err := g.policy.AdmitPage(effects.Request{Method: method, URL: u}); err != nil {
		var ref *effects.Refusal
		if errors.As(err, &ref) && ref.Kind == effects.HostNotAllowed {
			host = hostrule.Entry(u)
		}
		return host, err.Error()
	}
	return "", ""
}

func (g *guard) answer(ctx context.Context, ev *fetch.EventRequestPaused) {
	host, refused := g.requestVerdict(ev.Request.Method, ev.Request.URL)
	if refused != "" {
		if host != "" {
			g.mu.Lock()
			g.blocked[host] = true
			g.mu.Unlock()
		}
		_, _ = chromedp.Call(ctx, fetch.FailRequest, fetch.FailRequestParams{
			RequestID: ev.RequestID, ErrorReason: network.ErrorReasonBlockedByClient,
		})
		return
	}
	g.mu.Lock()
	trace := g.trace
	g.mu.Unlock()
	params := fetch.ContinueRequestParams{RequestID: ev.RequestID}
	if trace != "" {
		params.Headers = traceHeaders(ev.Request.Headers, trace, g.span())
	}
	if _, err := chromedp.Call(ctx, fetch.ContinueRequest, params); err != nil && ctx.Err() == nil {
		// A request that cannot be continued is failed, and never left to be sent as it was.
		_, _ = chromedp.Call(ctx, fetch.FailRequest, fetch.FailRequestParams{
			RequestID: ev.RequestID, ErrorReason: network.ErrorReasonFailed,
		})
	}
}

// traceHeaders is the header list of a request with the W3C traceparent of the action. The command
// replaces the whole list, so every header that the page set is kept.
func traceHeaders(in network.Headers, trace, span string) []*fetch.HeaderEntry {
	out := make([]*fetch.HeaderEntry, 0, len(in)+1)
	for _, name := range slices.Sorted(maps.Keys(in)) {
		if strings.EqualFold(name, "traceparent") {
			continue
		}
		out = append(out, &fetch.HeaderEntry{Name: name, Value: fmt.Sprint(in[name])})
	}
	return append(out, &fetch.HeaderEntry{Name: "traceparent", Value: "00-" + trace + "-" + span + "-01"})
}
