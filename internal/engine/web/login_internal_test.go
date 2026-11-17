package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
		done <- login(context.Background(), LoginOptions{
			Home: home, Environment: "test", Account: "ana", URL: srv.URL, Hidden: true, Poll: 100 * time.Millisecond,
		}, closeWindow)
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
