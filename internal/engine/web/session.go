package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

const (
	// quiet is how long the page must go with no request and no change of its tree to look settled.
	quiet = 250 * time.Millisecond
	poll  = 40 * time.Millisecond
)

// session is one Chrome on one profile, with one tab. An action holds mu while it runs, so two
// scenarios that share a profile never act on one page at the same time.
type session struct {
	ctx         context.Context
	cancel      context.CancelFunc
	allocCancel context.CancelFunc
	guard       *guard
	mu          sync.Mutex
}

// watchJS keeps the time of the last change of the page's tree. A page that has not changed for a
// short time, and has no request open, looks settled.
const watchJS = `(function(){
  if (window.__semarLast !== undefined) return;
  window.__semarLast = performance.now();
  const bump = () => { window.__semarLast = performance.now(); };
  new MutationObserver(bump).observe(document, { subtree: true, childList: true, attributes: true, characterData: true });
  window.addEventListener('load', bump);
})();`

func (e *Engine) session(name string) (*session, error) {
	account := accountName(name)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, errors.New("the browser was closed")
	}
	if s, ok := e.sessions[account]; ok {
		return s, nil
	}
	chrome, err := FindChrome(e.o.Chrome)
	if err != nil {
		return nil, err
	}
	dir, err := ProfileDir(e.o.Home, e.o.Environment, account)
	if err != nil {
		return nil, err
	}
	if err := ensureProfile(dir); err != nil {
		return nil, err
	}
	allocCtx, allocCancel := allocator(chrome, dir, e.o.Headed)
	ctx, cancel := chromedp.NewContext(allocCtx)
	s := &session{ctx: ctx, cancel: cancel, allocCancel: allocCancel, guard: newGuard(e.o.Policy, e.o.SpanID)}
	if err := s.start(); err != nil {
		s.close()
		return nil, fmt.Errorf("the browser could not be started on the profile of %s: %w; if a window of semar login is still open on this profile, close it", account, err)
	}
	e.sessions[account] = s
	return s, nil
}

func (s *session) start() error {
	if err := chromedp.Do(s.ctx); err != nil {
		return err
	}
	if err := chromedp.Listen(s.ctx, s.guard.onEvent); err != nil {
		return err
	}
	go s.guard.serve(s.ctx)
	steps := []func() error{
		func() error { _, err := chromedp.Call(s.ctx, page.Enable, page.EnableParams{}); return err },
		func() error { _, err := chromedp.Call(s.ctx, network.Enable, network.EnableParams{}); return err },
		func() error {
			_, err := chromedp.Call(s.ctx, fetch.Enable, fetch.EnableParams{Patterns: []*fetch.RequestPattern{{URLPattern: "*"}}})
			return err
		},
		func() error { _, err := chromedp.Call(s.ctx, accessibility.Enable, cdp.Empty{}); return err },
		func() error {
			_, err := chromedp.Call(s.ctx, page.AddScriptToEvaluateOnNewDocument, page.AddScriptToEvaluateOnNewDocumentParams{Source: watchJS})
			return err
		},
		func() error {
			_, err := chromedp.Call(s.ctx, runtime.Evaluate, runtime.EvaluateParams{Expression: watchJS})
			return err
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// close ends Chrome and waits for it, so that the profile is on the disk when it returns.
func (s *session) close() {
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	_ = chromedp.Cancel(ctx)
	s.cancel()
	s.allocCancel()
}

func (s *session) navigate(ctx context.Context, url string) error {
	if err := chromedp.Do(ctx, chromedp.Navigate(url)); err != nil {
		var le *chromedp.LoadError
		if errors.As(err, &le) {
			return fmt.Errorf("the page could not be opened: %s", le.ErrorText)
		}
		return err
	}
	return nil
}

func (s *session) location(ctx context.Context) (string, error) {
	return chromedp.Run(ctx, chromedp.Location())
}

func capturePNG(ctx context.Context) ([]byte, error) {
	res, err := chromedp.Call(ctx, page.CaptureScreenshot, page.CaptureScreenshotParams{Format: page.CaptureScreenshotFormatPng})
	if err != nil {
		return nil, err
	}
	return res.Data, nil
}

// settle waits until the document is loaded, no request is open, and neither the requests nor the
// tree of the page has changed for a short time, or until max has passed. A page that never settles
// is read as it is: a check then reads what a person would see.
func (s *session) settle(ctx context.Context, limit time.Duration) {
	settleUntil(ctx, limit, s.guard.idle)
}

// settleUntil is settle with the way to ask how long the requests have been still.
func settleUntil(ctx context.Context, limit time.Duration, idle func(time.Time) (time.Duration, int)) {
	deadline := time.Now().Add(limit)
	for ctx.Err() == nil {
		netQuiet, open := idle(time.Now())
		ready, domQuiet := pageState(ctx)
		if ready && open == 0 && netQuiet >= quiet && domQuiet >= quiet {
			return
		}
		if time.Now().After(deadline) {
			return
		}
		select {
		case <-ctx.Done():
		case <-time.After(poll):
		}
	}
}

// pageState says whether the document is complete, and how long ago its tree last changed. A page
// that cannot be asked, for instance in the middle of a navigation, is not ready.
func pageState(ctx context.Context) (ready bool, domQuiet time.Duration) {
	res, err := chromedp.Call(ctx, runtime.Evaluate, runtime.EvaluateParams{
		Expression:    `JSON.stringify([document.readyState === 'complete' ? 1 : 0, window.__semarLast === undefined ? 1e6 : performance.now() - window.__semarLast])`,
		ReturnByValue: ptr(true),
	})
	if err != nil || res.Result == nil || res.ExceptionDetails != nil {
		return false, 0
	}
	var text string
	if json.Unmarshal(res.Result.Value, &text) != nil {
		return false, 0
	}
	var state [2]float64
	if json.Unmarshal([]byte(text), &state) != nil {
		return false, 0
	}
	return state[0] == 1, time.Duration(state[1] * float64(time.Millisecond))
}
