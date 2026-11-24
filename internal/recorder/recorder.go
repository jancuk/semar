// Package recorder is the one place an action is stored. It redacts first, so
// no unredacted action is ever written or returned.
package recorder

import (
	"fmt"
	"net/url"
	"sync"

	"github.com/jancuk/semar/internal/activity"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/redact"
)

type Sink interface {
	WriteAction(a domain.Action) (path string, err error) // path is relative to the run directory
	AppendActivity(e domain.Activity) error
}

type Recorder struct {
	mu       sync.Mutex
	sink     Sink
	red      *redact.Redactor
	classify func(method string, u *url.URL) effects.Class
	run      domain.RunID
	seq      int
}

func New(sink Sink, red *redact.Redactor, classify func(method string, u *url.URL) effects.Class, run domain.RunID) *Recorder {
	return &Recorder{sink: sink, red: red, classify: classify, run: run}
}

// Redact removes the secrets from a text that is stored beside the actions, such as the report of a crash.
func (r *Recorder) Redact(text string) string { return r.red.Text(text) }

// Record sets Seq and ID, redacts, stores the action, appends its activity entry, and returns the redacted action.
// A Seq whose sink call failed is not reused: the action file may already exist under it.
func (r *Recorder) Record(a domain.Action) (domain.Action, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	a.Seq = r.seq
	a.ID = domain.ActionID(fmt.Sprintf("a-%04d", r.seq))
	a.Schema = 1
	a.Run = r.run

	class := effects.Read
	if a.Request != nil {
		u, err := url.Parse(a.Request.URL)
		if err != nil {
			u = &url.URL{}
		}
		class = r.classify(a.Request.Method, u)
	}
	if a.Web != nil && domain.WebOp(a.Web.Op).Writes() {
		class = effects.Write
	}
	if a.Mobile != nil && domain.MobileOp(a.Mobile.Op).Writes() {
		class = effects.Write
	}
	red := r.red.Action(a)
	path, err := r.sink.WriteAction(red)
	if err != nil {
		return domain.Action{}, fmt.Errorf("store action %s: %w", red.ID, err)
	}
	if err := r.sink.AppendActivity(activity.FromAction(red, class, path)); err != nil {
		return domain.Action{}, fmt.Errorf("append activity %s: %w", red.ID, err)
	}
	return red, nil
}
