package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

func TestLoginReturnsWhenTheWindowCloses(t *testing.T) {
	if _, err := FindChrome(""); err != nil {
		t.Skip("skipped: " + err.Error())
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "<!doctype html><title>Sign in</title><p>sign in here</p>")
	}))
	defer srv.Close()
	home := t.TempDir()
	closeWindow := func(tab context.Context) {
		time.Sleep(1500 * time.Millisecond)
		res, err := chromedp.CallBrowser(tab, target.GetTargets, target.GetTargetsParams{})
		if err != nil {
			return
		}
		for _, info := range res.TargetInfos {
			if info.Type == "page" {
				_, _ = chromedp.CallBrowser(tab, target.CloseTarget, target.CloseTargetParams{TargetID: info.TargetID})
			}
		}
	}
	done := make(chan error, 1)
	go func() {
		_, err := Login(context.Background(), LoginOptions{
			Home: home, Environment: "test", Account: "ana", URL: srv.URL, Hidden: true, Poll: 100 * time.Millisecond, AfterOpen: closeWindow,
		})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(40 * time.Second):
		t.Fatal("Login did not return after the window was closed")
	}
	if _, err := os.Stat(filepath.Join(home, "browser", "test", "ana")); err != nil {
		t.Errorf("the profile of the account was not made: %v", err)
	}
}

func chromeOrSkip(t *testing.T) {
	t.Helper()
	if _, err := FindChrome(""); err != nil {
		t.Skip("skipped: " + err.Error())
	}
}

// pagesOf lists the page targets of the browser of tab.
func pagesOf(t *testing.T, tab context.Context) []*target.Info {
	t.Helper()
	res, err := chromedp.CallBrowser(tab, target.GetTargets, target.GetTargetsParams{})
	if err != nil {
		t.Errorf("the targets could not be listed: %v", err)
		return nil
	}
	var pages []*target.Info
	for _, info := range res.TargetInfos {
		if info.Type == "page" {
			pages = append(pages, info)
		}
	}
	return pages
}

func closePage(t *testing.T, tab context.Context, info *target.Info) {
	t.Helper()
	if _, err := chromedp.CallBrowser(tab, target.CloseTarget, target.CloseTargetParams{TargetID: info.TargetID}); err != nil {
		t.Errorf("the page could not be closed: %v", err)
	}
}

// openLogin runs login in the background with a window that is hidden, and says when it returns.
func openLogin(t *testing.T, home, url string, hosts []string, ended *atomic.Bool, afterOpen func(tab context.Context)) <-chan loginEnd {
	t.Helper()
	done := make(chan loginEnd, 1)
	go func() {
		seen, err := Login(context.Background(), LoginOptions{
			Home: home, Environment: "test", Account: "ana", URL: url, Hosts: hosts, Hidden: true, Poll: 100 * time.Millisecond, AfterOpen: afterOpen,
		})
		ended.Store(true)
		done <- loginEnd{seen, err}
	}()
	return done
}

type loginEnd struct {
	seen []string
	err  error
}

func TestLoginStaysOpenWhileAnotherPageIsOpenAndEndsWhenTheLastOneCloses(t *testing.T) {
	chromeOrSkip(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "<!doctype html><title>Sign in</title><p>sign in here</p>")
	}))
	defer srv.Close()
	var ended atomic.Bool
	drive := func(tab context.Context) {
		time.Sleep(1500 * time.Millisecond)
		first := pagesOf(t, tab)
		if len(first) != 1 {
			t.Errorf("%d pages are open, want 1", len(first))
			return
		}
		if _, err := chromedp.CallBrowser(tab, target.CreateTarget, target.CreateTargetParams{URL: srv.URL + "/second"}); err != nil {
			t.Errorf("the second page could not be opened: %v", err)
			return
		}
		time.Sleep(500 * time.Millisecond)
		closePage(t, tab, first[0])
		time.Sleep(2 * time.Second)
		if ended.Load() {
			t.Error("login ended while the second page was still open")
		}
		rest := pagesOf(t, tab)
		if len(rest) != 1 {
			t.Errorf("%d pages are open after the first closed, want 1", len(rest))
			return
		}
		closePage(t, tab, rest[0])
	}
	done := openLogin(t, t.TempDir(), srv.URL, []string{srv.Listener.Addr().String()}, &ended, drive)
	select {
	case end := <-done:
		if end.err != nil {
			t.Fatal(end.err)
		}
	case <-time.After(40 * time.Second):
		t.Fatal("login did not end when the last page closed")
	}
}

func TestLoginEndsWhenTheBrowserIsQuit(t *testing.T) {
	chromeOrSkip(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "<!doctype html><p>x</p>") }))
	defer srv.Close()
	quit := func(tab context.Context) {
		time.Sleep(1500 * time.Millisecond)
		// Cmd+Q ends the process, and the program never gets to say goodbye.
		if err := chromedp.FromContext(tab).Browser.Process().Kill(); err != nil {
			t.Errorf("Chrome could not be ended: %v", err)
		}
	}
	done := openLogin(t, t.TempDir(), srv.URL, nil, &atomic.Bool{}, quit)
	select {
	case end := <-done:
		if end.err != nil {
			t.Fatal(end.err)
		}
	case <-time.After(40 * time.Second):
		t.Fatal("login did not end when the browser was quit")
	}
}

func TestLoginNotesTheHostsOfEveryPageThatTheEnvironmentDoesNotList(t *testing.T) {
	chromeOrSkip(t)
	var mu sync.Mutex
	asked := map[string]int{}
	other := func(name string) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			asked[name]++
			mu.Unlock()
			w.Header().Set("Content-Type", "image/gif")
			fmt.Fprint(w, "GIF89a")
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	analytics, cdn, listed := other("analytics"), other("cdn"), other("listed")
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/second":
			fmt.Fprintf(w, `<!doctype html><title>Second</title><img src=%q>`, cdn.URL+"/pixel.gif?token=abc123")
		default:
			fmt.Fprintf(w, `<!doctype html><title>Sign in</title><img src=%q><img src=%q><script>fetch(%q, {mode: 'no-cors'})</script>`,
				analytics.URL+"/a.gif", listed.URL+"/ok.gif", analytics.URL+"/b")
		}
	}))
	defer site.Close()
	hosts := []string{site.Listener.Addr().String(), listed.Listener.Addr().String()}
	drive := func(tab context.Context) {
		time.Sleep(1500 * time.Millisecond)
		if _, err := chromedp.CallBrowser(tab, target.CreateTarget, target.CreateTargetParams{URL: site.URL + "/second"}); err != nil {
			t.Errorf("the second page could not be opened: %v", err)
		}
		time.Sleep(2 * time.Second)
		for _, p := range pagesOf(t, tab) {
			closePage(t, tab, p)
		}
	}
	done := openLogin(t, t.TempDir(), site.URL, hosts, &atomic.Bool{}, drive)
	select {
	case end := <-done:
		if end.err != nil {
			t.Fatal(end.err)
		}
		want := []string{cdn.Listener.Addr().String(), analytics.Listener.Addr().String()}
		slices.Sort(want)
		if !slices.Equal(end.seen, want) {
			t.Errorf("hosts = %v, want %v", end.seen, want)
		}
		for _, h := range end.seen {
			if strings.Contains(h, "abc123") || strings.Contains(h, "/") {
				t.Errorf("a host holds more than a host: %q", h)
			}
		}
	case <-time.After(40 * time.Second):
		t.Fatal("login did not end")
	}
	mu.Lock()
	defer mu.Unlock()
	if asked["analytics"] == 0 || asked["cdn"] == 0 || asked["listed"] == 0 {
		t.Errorf("the window blocked a request: %v", asked)
	}
}
