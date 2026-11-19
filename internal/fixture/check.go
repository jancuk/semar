package fixture

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
)

// CheckResult is what one health check of a fixture did.
type CheckResult struct {
	// Record has the fixture, version, hash, environment, run, the three phases, Healthy, and
	// Reason. The caller adds At and Builds. It is stored only when Checked is true.
	Record HealthRecord
	// Checked is false when the check could not judge the fixture: a
	// parameter has no value, the policy refuses a step, or the environment is read only and
	// the fixture has create steps. Note says why. A record of that would block scenarios for a
	// reason that is not the fixture's health.
	Checked bool
	Note    string
	Left    []string // what may still exist after cleanup
	Actions []domain.ActionID
}

// Check runs the find steps of a fixture, then its create steps also when find gave a record, so
// that create is checked, and then the cleanup of what create made. It sends only what the
// effects policy admits. Its own last health record does not
// block it, so a fixture that failed once can be checked again. No lease is taken: create makes a
// record that no other run knows. It returns an error only for a recorder failure or a cancelled
// context, after it has cleaned up what it made.
func (r *Resolver) Check(ctx context.Context, id domain.FixtureID, authProfile string) (CheckResult, error) {
	e := r.newEntry("", authProfile, domain.PassSetup, domain.Given{Fixture: id, As: "check"})
	e.check = true
	res := CheckResult{Record: HealthRecord{
		Fixture: id, Environment: r.d.Environment, Run: r.d.Run,
		Find: "none", Create: "none", Cleanup: CleanupNotNeeded,
	}}
	if blocked := r.prepare(e); len(blocked) > 0 {
		res.Note = strings.Join(blocked, "; ")
		return res, nil
	}
	f := e.fixture
	rec := &res.Record
	rec.Version, rec.Hash = e.rec.Version, e.rec.Hash

	var fd found
	if len(f.Find) > 0 {
		var err error
		if fd, err = r.find(ctx, e); err != nil {
			return r.finishCheck(ctx, e, res), err
		}
		switch {
		case fd.block != "":
			rec.Find = "failed"
			rec.Create = "not_tried"
			rec.Reason = fd.block
			res.Checked = true
			return r.finishCheck(ctx, e, res), nil
		case fd.ok:
			rec.Find = "found"
		case fd.problem != "":
			rec.Find = "failed"
		default:
			rec.Find = "nothing"
		}
	}

	switch {
	case len(f.Create) == 0:
		res.Checked = true
		rec.Healthy = fd.ok
		switch {
		case fd.problem != "":
			rec.Reason = fd.problem
		case !fd.ok:
			rec.Reason = fmt.Sprintf("fixture %s: find gave nothing and the fixture has no create steps", f.ID)
		}
		return r.finishCheck(ctx, e, res), nil
	case r.d.Policy != nil && r.d.Policy.ReadOnly():
		rec.Create = "not_tried"
		res.Note = fmt.Sprintf("this environment is read-only: fixture %s has create steps, and a check cannot send them", f.ID)
		return r.finishCheck(ctx, e, res), nil
	}

	res.Checked = true
	blocked, err := r.create(ctx, e, map[string]string{})
	if err != nil {
		return r.finishCheck(ctx, e, res), err
	}
	if len(blocked) > 0 {
		rec.Create, rec.Reason = "failed", blocked[0]
	} else {
		rec.Create, rec.Healthy = "made", true
	}
	return r.finishCheck(ctx, e, res), nil
}

// finishCheck removes what create made, through the same cleanup as a scenario, and copies the
// outcome of it into the result.
func (r *Resolver) finishCheck(ctx context.Context, e *entry, res CheckResult) CheckResult {
	out := r.Release(ctx, &Resolution{entries: []*entry{e}})[0]
	res.Record.Cleanup = out.Cleanup
	res.Left = slices.Clone(out.Left)
	res.Actions = slices.Clone(out.Actions)
	return res
}
