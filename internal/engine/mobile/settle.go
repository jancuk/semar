package mobile

import (
	"context"
	"time"
)

const (
	settleFirst = 150 * time.Millisecond
	settleLast  = 500 * time.Millisecond
)

// settle reads the tree like Settle, with the clock and the sleep of the engine.
func (e *Engine) settle(ctx context.Context, d Driver, limit time.Duration) (*Tree, error) {
	return Settle(ctx, d, limit, e.o.Clock, e.o.Sleep)
}

// Settle reads the tree until two reads in a row are alike, which means that the screen is still, or
// until limit has passed. A screen that never settles is read as it is, so a check reads what a
// person would see. Two reads with the same digest give the first tree again, which has been parsed
// already. The delay between reads is 150 ms and grows to 500 ms, so a screen that settles in a moment
// costs two reads and a spinner costs few. A nil clock or sleep is the real one. The recorder and the
// engine both use it, so what a person sees while recording is what a replay reads.
func Settle(ctx context.Context, d Driver, limit time.Duration, clock func() time.Time, sleepFn func(context.Context, time.Duration) error) (*Tree, error) {
	if clock == nil {
		clock = time.Now
	}
	if sleepFn == nil {
		sleepFn = sleep
	}
	deadline := clock().Add(limit)
	prev, err := d.Source(ctx)
	if err != nil {
		return nil, err
	}
	delay := settleFirst
	for {
		if remaining := deadline.Sub(clock()); remaining <= 0 || ctx.Err() != nil {
			return prev, ctx.Err()
		} else if err := sleepFn(ctx, min(delay, remaining)); err != nil {
			return prev, err
		}
		next, err := d.Source(ctx)
		if err != nil {
			return prev, err
		}
		if next.Digest == prev.Digest && appVisible(next, d.About().Package) {
			return prev, nil
		}
		prev = next
		delay = min(delay*2, settleLast)
	}
}

// appVisible reports whether t has visible UI content belonging to pkg, or has no package tags at all
// (synthetic test trees). Right after launch, Android can still show the home launcher or an empty
// splash window for a moment: waiting avoids settling before the app draws its first screen.
func appVisible(t *Tree, pkg string) bool {
	if t == nil || pkg == "" {
		return true
	}
	sawAny := false
	matched := false
	t.Walk(func(n *Node) bool {
		if n.Package == "" {
			return true
		}
		sawAny = true
		if n.Package == pkg && n.Visible && !n.Bounds.Empty() && (n.Text != "" || n.Desc != "" || n.ID != "" || n.Role != "" || n.Attrs["clickable"] == "true") {
			matched = true
			return false
		}
		return true
	})
	return !sawAny || matched
}

// sleep waits for d, or for the context.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
