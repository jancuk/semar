// Package mcpkit holds what every tool server of Semar shares: the guard that refuses writes while the
// server only observes, the scrubber that keeps secrets out of results, and the folder that outputs go
// to. The mobile, web, API, journey, and control servers use it, so that they refuse and scrub the same
// way.
package mcpkit

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jancuk/semar/internal/mcp"
)

// Surface says which family of tools a tool belongs to.
type Surface string

const (
	SurfaceMobile  Surface = "mobile"
	SurfaceWeb     Surface = "web"
	SurfaceAPI     Surface = "api"
	SurfaceJourney Surface = "journey"
	SurfaceControl Surface = "control"
)

func (s Surface) valid() bool {
	switch s {
	case SurfaceMobile, SurfaceWeb, SurfaceAPI, SurfaceJourney, SurfaceControl:
		return true
	}
	return false
}

// Guard decides whether a tool that changes something may run. Observe is set when the environment that
// the server is bound to is production, when the effects mode is read_only, or with --read-only. Reason
// says which, in words for the model.
type Guard struct {
	Observe bool
	Reason  string
}

// ErrObserve is what Allow wraps when it refuses. A caller tests it with errors.Is.
var ErrObserve = errors.New("the server only observes")

// Allow returns nil when a tool of the surface may run, and an error that says why when it may not. A
// tool that only reads is always allowed. The text is the same for every surface.
func (g Guard) Allow(s Surface, write bool) error {
	if !s.valid() {
		return fmt.Errorf("the surface %q is not known", string(s))
	}
	if !write || !g.Observe {
		return nil
	}
	why := strings.TrimSpace(g.Reason)
	if why == "" {
		why = "it was started to observe"
	}
	return fmt.Errorf("%w: %s, so a %s tool that changes something is refused. Use a tool that only reads, or start the server without observe mode", ErrObserve, why, s)
}

// Middleware refuses, for every tool that has a surface and changes something, with the text of Allow.
// A tool with no surface passes.
func (g Guard) Middleware() mcp.Middleware {
	return func(t mcp.Tool, next mcp.Handler) mcp.Handler {
		if t.Surface == "" || !t.Write {
			return next
		}
		return func(ctx context.Context, call mcp.Call) (mcp.Result, error) {
			if err := g.Allow(Surface(t.Surface), t.Write); err != nil {
				return mcp.ErrorResult(err.Error()), nil
			}
			return next(ctx, call)
		}
	}
}
