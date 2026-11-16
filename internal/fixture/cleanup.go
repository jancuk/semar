package fixture

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/domain"
)

// cleanupLimit bounds the cleanup of every fixture of one owner, so a stalled service still lets
// the run end (D15).
const cleanupLimit = 2 * time.Minute

// Release runs the cleanup of every entry that create resolved, in reverse order of the entries,
// and then releases every lease. It runs on a context that the caller's cancel does not stop, so
// an interrupt still removes what a fixture made. It never returns an error: a failure is in the
// records. It is safe with a nil Resolution and more than once: a later call sends nothing.
func (r *Resolver) Release(ctx context.Context, res *Resolution) []domain.FixtureRecord {
	if res == nil {
		return nil
	}
	res.once.Do(func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupLimit)
		defer cancel()
		for _, e := range slices.Backward(res.entries) {
			r.cleanup(cctx, e)
		}
		for _, e := range res.entries {
			for _, release := range e.release {
				release()
			}
		}
	})
	return records(res.entries)
}

// cleanup sends each cleanup step once, as written. A step that fails is recorded and never
// sent again, and the record says what may still exist (C1 to C5).
func (r *Resolver) cleanup(ctx context.Context, e *entry) {
	if !e.made {
		return
	}
	f := e.fixture
	noun, _, _ := strings.Cut(string(f.ID), ".")
	what := fmt.Sprintf("%s created by fixture %s (as %s)", noun, f.ID, e.given.As)
	if len(f.Cleanup) == 0 {
		e.rec.Cleanup = CleanupNone
		e.rec.Left = append(e.rec.Left, what+" is kept: the fixture declares no cleanup")
		return
	}
	e.rec.Cleanup = CleanupDone
	for i, st := range f.Cleanup {
		o, err := r.runStep(ctx, e, domain.PassCleanup, domain.PhaseCleanup, i, e.vars)
		text := o.failure
		switch {
		case err != nil:
			text = "could not be sent or recorded"
		case o.renderErr != nil && strings.Contains(o.renderErr.Error(), "unknown variable"):
			text = "was not sent: it needs a value that create did not capture"
		case o.renderErr != nil:
			text = "was not sent: it " + text
		}
		if text != "" {
			e.rec.Cleanup = CleanupFailed
			e.rec.Left = append(e.rec.Left, fmt.Sprintf("%s may still exist: %s %s", what, stepLabel(domain.PhaseCleanup, i, st), text))
		}
	}
}
