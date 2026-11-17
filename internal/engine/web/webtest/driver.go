// Package webtest finds an element of a page by its selector and sends input to it with webinput, as
// a person uses their hands. A test of the recorder uses it, so that what the recorder hears is real
// input and not a call into the page.
package webtest

import (
	"context"
	"fmt"

	"github.com/chromedp/chromedp"

	"github.com/jancuk/semar/internal/engine/web/webinput"
)

// Driver sends input to one tab.
type Driver struct {
	Tab context.Context
}

// Ready says whether the page of the tab is loaded and has the recorder in it.
func (d Driver) Ready() bool {
	ok, err := chromedp.Run(d.Tab, chromedp.Evaluate[bool](`document.readyState === 'complete' && !!window.__semarRec`))
	return err == nil && ok
}

func (d Driver) center(sel string) (float64, float64, error) {
	pt, err := chromedp.Run(d.Tab, chromedp.Evaluate[[2]float64](fmt.Sprintf(
		`(() => { const e = document.querySelector(%q); e.scrollIntoView({block: 'center'}); const b = e.getBoundingClientRect(); return [b.x + b.width / 2, b.y + b.height / 2]; })()`, sel)))
	if err != nil {
		return 0, 0, fmt.Errorf("find %s: %w", sel, err)
	}
	return pt[0], pt[1], nil
}

// Click presses and lets go of the left button on the middle of the element.
func (d Driver) Click(sel string) error {
	x, y, err := d.center(sel)
	if err != nil {
		return err
	}
	return webinput.Click(d.Tab, x, y)
}

// OptionClick is a click with the Option key held down.
func (d Driver) OptionClick(sel string) error {
	x, y, err := d.center(sel)
	if err != nil {
		return err
	}
	return webinput.OptionClick(d.Tab, x, y)
}

// Type clicks a field to focus it and sends one key event for each character.
func (d Driver) Type(sel, text string) error {
	if err := d.Click(sel); err != nil {
		return err
	}
	return d.Keys(text)
}

// Keys sends key events: a character, or a key of the kb package such as kb.Enter.
func (d Driver) Keys(keys string) error { return webinput.Keys(d.Tab, keys) }

// Drag presses on one element, moves in steps to another, and lets go.
func (d Driver) Drag(from, to string) error {
	fx, fy, err := d.center(from)
	if err != nil {
		return err
	}
	tx, ty, err := d.center(to)
	if err != nil {
		return err
	}
	return webinput.Drag(d.Tab, fx, fy, tx, ty)
}
