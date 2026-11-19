// Package webinput sends the input of a person to a Chrome tab through the browser protocol: the
// mouse, the keyboard, and the drag. The engine of a replay, the explorer of a plan, and the tests of
// the recorder all send input here, so that what a page and the recorder hear is the same real input
// whoever sends it.
package webinput

import (
	"context"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

// Click presses and lets go of the left button at a point of the page.
func Click(tab context.Context, x, y float64) error {
	return chromedp.Do(tab, chromedp.MouseClickXY(x, y))
}

// OptionClick is a click with the Option key (Alt) held down.
func OptionClick(tab context.Context, x, y float64) error {
	for _, typ := range []input.DispatchMouseEventType{chromedp.MousePressed, chromedp.MouseReleased} {
		err := chromedp.Do(tab, chromedp.MouseEvent(typ, x, y, func(p *input.DispatchMouseEventParams) {
			p.Button, p.ClickCount, p.Modifiers = input.MouseButtonLeft, 1, int64(chromedp.ModifierAlt)
		}))
		if err != nil {
			return err
		}
	}
	return nil
}

// Drag presses at one point, moves in steps to another, and lets go.
func Drag(tab context.Context, fromX, fromY, toX, toY float64) error {
	return chromedp.Do(tab, chromedp.DragAndDropXY(fromX, fromY, toX, toY))
}

// Keys sends key events: a character, or a key of the kb package such as kb.Enter.
func Keys(tab context.Context, keys string) error {
	return chromedp.Do(tab, chromedp.KeyEvent(keys))
}

// Hover moves the pointer to a point of the page, with no button held.
func Hover(tab context.Context, x, y float64) error {
	return chromedp.Do(tab, chromedp.MouseEvent(chromedp.MouseMoved, x, y))
}
