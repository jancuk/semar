package web

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/secret"
)

// LoginOptions say which profile a person signs in to, and where.
type LoginOptions struct {
	Chrome      string // "" finds Chrome
	Home        string
	Environment string
	Account     string // "" is default
	URL         string // the page to open: the base URL of the service
	// Hosts are the hosts that the environment lists, in the form of a hosts entry. The window blocks
	// nothing: Login only notes the hosts of the requests that these do not allow.
	Hosts []string
	// Hidden runs the browser with no window. A person cannot sign in to it. It is for a test.
	Hidden bool
	// Poll is how often Login looks for the pages of the window. The default is 200 milliseconds.
	Poll time.Duration
	// AfterOpen runs when the window is open, with its first page. A test stands there where the
	// person stands, and closes the pages as they would.
	AfterOpen func(tab context.Context)
}

// Login opens a Chrome window on the profile of the account at the URL, so that a person can sign in
// with whatever the application asks for. It returns when the last page of the window is closed, or
// Chrome is quit, and then Chrome has written the profile to the disk. It never uses the profile that
// a person browses with. It gives the hosts that the pages asked for and that Hosts does not allow,
// sorted and with no path, so that a person can add them to the environment.
func Login(ctx context.Context, o LoginOptions) ([]string, error) {
	chrome, err := FindChrome(o.Chrome)
	if err != nil {
		return nil, err
	}
	dir, err := ProfileDir(o.Home, o.Environment, o.Account)
	if err != nil {
		return nil, err
	}
	if err := ensureProfile(dir); err != nil {
		return nil, err
	}
	allocCtx, allocCancel := allocator(chrome, dir, !o.Hidden)
	defer allocCancel()
	tab, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	if err := chromedp.Do(tab); err != nil {
		return nil, fmt.Errorf("the browser could not be started on the profile %s: %w; if another window of this profile is open, close it", dir, err)
	}
	seen := newHostWatch(o.Hosts)
	if err := seen.watch(tab); err != nil {
		closeBrowser(tab)
		return nil, fmt.Errorf("the browser could not be started on the profile %s: %w", dir, err)
	}
	// The page may take long to load, or never: the window is the person's, so a failed load is
	// not an error here. They see it, and they can try again in the window.
	go func() { _ = chromedp.Do(tab, chromedp.Navigate(o.URL)) }()
	if o.AfterOpen != nil {
		go o.AfterOpen(tab)
	}

	err = waitWindowClosed(ctx, tab, o.Poll, false, seen)
	return seen.hosts(), err
}

// waitWindowClosed returns when the person has closed the last page of the browser, or quit it. A
// page that a person opens in the window, or a page that opens one, keeps the wait going, however
// many of the earlier pages are closed. It gives ctx.Err() when ctx ends first, after it has asked
// the browser to quit. The pages are looked at every poll, which is 200 milliseconds when poll is
// zero. A page that is open already when the wait starts is told by open: a page that closes before
// the first look is then still seen to close. With a watch, the wait also notes the requests of every
// page that it sees.
//
// When no page is left, Chrome is ended as session.close ends it, and not left to be killed: Chrome
// writes its profile when it exits by itself, and on macOS it stays alive with no window.
func waitWindowClosed(ctx context.Context, tab context.Context, poll time.Duration, open bool, seen *hostWatch) error {
	every := poll
	if every <= 0 {
		every = 200 * time.Millisecond
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	defer seen.release()
	opened := open
	failed := 0
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
			// The browser is gone when the person quit it. A browser that is still there and does not
			// answer is asked to quit after a few tries, so that no Chrome is left with no window.
			if browserGone(tab) {
				return nil
			}
			if failed++; failed >= 5 {
				closeBrowser(tab)
				return nil
			}
			continue
		}
		failed = 0
		pages := 0
		for _, t := range res.TargetInfos {
			switch t.Type {
			case "page":
				pages++
				seen.attach(tab, t)
			case "iframe":
				seen.attach(tab, t)
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

// browserGone says whether the browser of the tab has exited or lost its connection.
func browserGone(tab context.Context) bool {
	if tab.Err() != nil {
		return true
	}
	c := chromedp.FromContext(tab)
	if c == nil || c.Browser == nil {
		return true
	}
	select {
	case <-c.Browser.LostConnection:
		return true
	default:
		return false
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

// SignInOptions say which account signs in, to which page, and with what.
type SignInOptions struct {
	Chrome      string // "" finds Chrome
	Home        string
	Environment string
	Account     string // "" is default
	URL         string // the page to open: the base URL of the service
	Who         config.WebSignIn
	Secrets     secret.Resolver
	// Hidden runs the browser with no window.
	Hidden bool
}

// SignIn opens the page in a browser on the profile of the account and signs in to its form as a step
// does, then ends the browser, so that what the application keeps in the profile is on the disk. It
// says whether the page showed a sign-in form. A sign-in that does not finish is an error.
func SignIn(ctx context.Context, o SignInOptions) (bool, error) {
	account := accountName(o.Account)
	rec, err := StartRecorder(ctx, RecorderOptions{
		Chrome: o.Chrome, Home: o.Home, Environment: o.Environment, Account: o.Account, Hidden: o.Hidden,
		SignIn: &RecorderSignIn{Account: account, Who: o.Who, Secrets: o.Secrets},
	})
	if err != nil {
		return false, err
	}
	defer rec.Finish()
	if err := rec.Open(o.URL); err != nil {
		return false, err
	}
	_, signed := rec.SignedIn()
	return signed, nil
}
