package web

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// LoginOptions say which profile a person signs in to, and where.
type LoginOptions struct {
	Chrome      string // "" finds Chrome
	Home        string
	Environment string
	Account     string // "" is default
	URL         string // the page to open: the base URL of the service
	// Hidden runs the browser with no window. A person cannot sign in to it. It is for a test.
	Hidden bool
	// Poll is how often Login looks for the window. The default is half a second.
	Poll time.Duration
}

// Login opens a Chrome window on the profile of the account at the URL, so that a person can sign in
// with whatever the application asks for. It returns when the window is closed, and then Chrome has
// written the profile to the disk. It never uses the profile that a person browses with.
func Login(ctx context.Context, o LoginOptions) error { return login(ctx, o, nil) }

// login is Login with a hook that runs when the window is open: a test closes the window there, as a person would.
func login(ctx context.Context, o LoginOptions, afterOpen func(tab context.Context)) error {
	chrome, err := FindChrome(o.Chrome)
	if err != nil {
		return err
	}
	dir, err := ProfileDir(o.Home, o.Environment, o.Account)
	if err != nil {
		return err
	}
	if err := ensureProfile(dir); err != nil {
		return err
	}
	allocCtx, allocCancel := allocator(chrome, dir, !o.Hidden)
	defer allocCancel()
	tab, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	if err := chromedp.Do(tab); err != nil {
		return fmt.Errorf("the browser could not be started on the profile %s: %w; if another window of this profile is open, close it", dir, err)
	}
	// The page may take long to load, or never: the window is the person's, so a failed load is
	// not an error here. They see it, and they can try again in the window.
	go func() { _ = chromedp.Do(tab, chromedp.Navigate(o.URL)) }()
	if afterOpen != nil {
		go afterOpen(tab)
	}

	return waitWindowClosed(ctx, tab, o.Poll)
}

// waitWindowClosed returns when the person has closed the last window of the browser, or quit it.
// It gives ctx.Err() when ctx ends first, after it has asked the browser to quit. A window is
// looked for every poll, which is half a second when poll is zero.
func waitWindowClosed(ctx context.Context, tab context.Context, poll time.Duration) error {
	every := poll
	if every <= 0 {
		every = 500 * time.Millisecond
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	opened := false
	for {
		select {
		case <-ctx.Done():
			closeBrowser(tab)
			return ctx.Err()
		case <-tab.Done():
			return nil
		case <-tick.C:
		}
		res, err := chromedp.CallBrowser(tab, target.GetTargets, target.GetTargetsParams{})
		if err != nil {
			// The browser is gone: the person quit it.
			return nil
		}
		pages := 0
		for _, t := range res.TargetInfos {
			if t.Type == "page" {
				pages++
			}
		}
		if pages > 0 {
			opened = true
		}
		if pages == 0 && opened {
			closeBrowser(tab)
			return nil
		}
	}
}

// closeBrowser asks Chrome to quit and waits a little, so that the profile is written.
func closeBrowser(tab context.Context) {
	ctx, cancel := context.WithTimeout(tab, 10*time.Second)
	defer cancel()
	if err := chromedp.Cancel(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return
	}
}
