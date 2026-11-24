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

// tapGap is the time between the taps of a multi-tap, short enough for the system to see one gesture.
const tapGap = 60 * time.Millisecond

// Tap sends n taps of one finger at p: it moves to p, then goes down and up n times with a pause of 60 ms
// between the taps. Two taps are a double tap. A count under one is one tap.
func (s *Session) Tap(ctx context.Context, p Point, n int) error {
	_, _, err := s.c.call(ctx, http.MethodPost, s.path("/actions"), tapBody(p, n), false)
	return err
}

func tapBody(p Point, n int) map[string]any {
	n = max(n, 1)
	acts := []any{map[string]any{"type": "pointerMove", "duration": 0, "x": p.X, "y": p.Y, "origin": "viewport"}}
	for i := range n {
		if i > 0 {
			acts = append(acts, map[string]any{"type": "pause", "duration": int(tapGap / time.Millisecond)})
		}
		acts = append(acts, map[string]any{"type": "pointerDown", "button": 0}, map[string]any{"type": "pointerUp", "button": 0})
	}
	return map[string]any{"actions": []any{map[string]any{
		"type":       "pointer",
		"id":         "finger1",
		"parameters": map[string]any{"pointerType": "touch"},
		"actions":    acts,
	}}}
}
