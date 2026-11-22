package webdriver

import (
	"context"
	"net/http"
	"time"
)

// Point is a place on the screen, in pixels.
type Point struct{ X, Y int }

// Touch sends one finger: it moves to from, goes down, waits for hold, moves to to, and goes up:
// POST /session/{id}/actions. With from equal to to it is a tap, with a hold it is a long press, and
// with another to and a move time it is a swipe.
func (s *Session) Touch(ctx context.Context, from, to Point, hold, move time.Duration) error {
	_, _, err := s.c.call(ctx, http.MethodPost, s.path("/actions"), touchBody(from, to, hold, move), false)
	return err
}

func touchBody(from, to Point, hold, move time.Duration) map[string]any {
	ms := func(d time.Duration) int { return int(d / time.Millisecond) }
	return map[string]any{"actions": []any{map[string]any{
		"type":       "pointer",
		"id":         "finger1",
		"parameters": map[string]any{"pointerType": "touch"},
		"actions": []any{
			map[string]any{"type": "pointerMove", "duration": 0, "x": from.X, "y": from.Y, "origin": "viewport"},
			map[string]any{"type": "pointerDown", "button": 0},
			map[string]any{"type": "pause", "duration": ms(hold)},
			map[string]any{"type": "pointerMove", "duration": ms(move), "x": to.X, "y": to.Y, "origin": "viewport"},
			map[string]any{"type": "pointerUp", "button": 0},
		},
	}}}
}
