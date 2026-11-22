package mobile

import (
	"context"
	"time"
)

const (
	settleFirst = 150 * time.Millisecond
	settleLast  = 500 * time.Millisecond
)

// settle reads the tree until two reads in a row are alike, which means that the screen is still, or
// until limit has passed. A screen that never settles is read as it is, so a check reads what a
// person would see. Two reads with the same digest give the first tree again, which has been parsed
// already. The delay between reads is 150 ms and grows to 500 ms, so a screen that settles in a moment
// costs two reads and a spinner costs few.
func (e *Engine) settle(ctx context.Context, d Driver, limit time.Duration) (*Tree, error) {
	deadline := e.o.Clock().Add(limit)
	prev, err := d.Source(ctx)
	if err != nil {
		return nil, err
	}
	delay := settleFirst
	for {
		if remaining := deadline.Sub(e.o.Clock()); remaining <= 0 || ctx.Err() != nil {
			return prev, ctx.Err()
		} else if err := e.o.Sleep(ctx, min(delay, remaining)); err != nil {
			return prev, err
		}
		next, err := d.Source(ctx)
		if err != nil {
			return prev, err
		}
		if next.Digest == prev.Digest {
			return prev, nil
		}
		prev = next
		delay = min(delay*2, settleLast)
	}
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
