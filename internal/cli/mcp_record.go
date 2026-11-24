package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/mobilemcp"
	"github.com/jancuk/semar/internal/mobilerec"
	"github.com/jancuk/semar/internal/studio"
)

// mcpAuthor is who an agent that records over MCP is, in the script that it writes, when its client did
// not give a name.
var mcpAuthor = mcpkit.AgentAuthor("")

func init() {
	registerMCPMobileExtra(func(w *mcpWiring) (mcpSurfaceResult, error) {
		// A recording writes into a repository and acts on an app of one of its environments, so it needs
		// both. Standalone mode, and a repository with no environment bound, have only the live tools.
		if !w.RepoMode || w.EnvName == "" {
			return mcpSurfaceResult{}, nil
		}
		w.Mobile.SetRecordBackend(&mcpRecordBackend{w: w})
		return mcpSurfaceResult{Tools: w.Mobile.RecordTools()}, nil
	})
}

// mcpRecordBackend is the mobilemcp.RecordBackend of the command: the session and the verify of the studio,
// and the replay of semar run.
type mcpRecordBackend struct {
	w *mcpWiring

	mu  sync.Mutex
	cur *studioBackend // the backend of the latest session, which its verify reads
}

var _ mobilemcp.RecordBackend = (*mcpRecordBackend)(nil)

func (r *mcpRecordBackend) backend(service, reset string, author *domain.ProviderRef) *studioBackend {
	return &studioBackend{
		o: r.w.Opts, f: recordTestFlags{dir: r.w.Dir, service: service}, envArg: r.w.EnvName, author: author, reset: reset,
	}
}

func (r *mcpRecordBackend) Start(_ context.Context, req mobilemcp.RecordRequest) (*mobilerec.Session, error) {
	author := req.Author
	if author == (domain.ProviderRef{}) {
		author = mcpAuthor
	}
	b := r.backend(req.Service, req.Reset, &author)
	ticket := req.Ticket
	if ticket == "" && req.Kind == mobilerec.KindScenario {
		ticket = groupOf(req.ID)
	}
	sess, err := b.newSession(studio.SessionRequest{
		Kind: req.Kind, ID: req.ID, Ticket: ticket, Title: req.Title, Service: req.Service, Serial: req.Serial,
		Use: req.Use, With: req.With, Replace: req.Replace, ConfirmClear: req.ConfirmClear,
	})
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.cur = b
	r.mu.Unlock()
	return sess, nil
}

func (r *mcpRecordBackend) Verify(ctx context.Context, s *mobilerec.Session, saved *mobilerec.Saved) (mobilemcp.VerifyResult, error) {
	r.mu.Lock()
	b := r.cur
	r.mu.Unlock()
	if b == nil {
		return mobilemcp.VerifyResult{}, errors.New("there is no recording to verify")
	}
	res, err := b.verify(ctx, s, saved)
	if err != nil {
		return mobilemcp.VerifyResult{}, err
	}
	out := mobilemcp.VerifyResult{Status: res.Status, Message: res.Message, Report: res.Report}
	for _, st := range res.Steps {
		out.Steps = append(out.Steps, mobilemcp.VerifyStep{Name: st.Name, Status: st.Status, Detail: st.Detail})
	}
	return out, nil
}

// RunScenario replays a saved scenario as semar run does. The outcome is the verdict of the code, with no
// model in the loop.
func (r *mcpRecordBackend) RunScenario(ctx context.Context, id string) (mobilemcp.RunResult, error) {
	w := r.w
	clean := func(s string) string { return w.Scrubber.Text(terminalSafe(oneLine(s))) }
	rep := replayScenario(ctx, w.Opts, w.Dir, w.EnvName, id, recordVerifyBuilds(w.Dir, w.EnvName))
	var exit exitError
	switch {
	case rep.result == nil && rep.err != nil && !errors.As(rep.err, &exit):
		return mobilemcp.RunResult{}, fmt.Errorf("the scenario could not run: %s", clean(rep.err.Error()))
	case rep.result == nil:
		return mobilemcp.RunResult{}, fmt.Errorf("the scenario did not finish: %s", clean(rep.stderr))
	}
	out := mobilemcp.RunResult{Outcome: string(rep.result.Outcome), Report: rep.report}
	for _, rs := range rep.result.Reasons {
		text := clean(rs.Text)
		if rs.Step != "" {
			text = "step " + string(rs.Step) + ": " + text
		}
		out.Reasons = append(out.Reasons, text)
	}
	return out, nil
}

func (r *mcpRecordBackend) List(context.Context) (mobilemcp.Catalog, error) {
	b := r.backend("", "", nil)
	st, err := b.status()
	if err != nil {
		return mobilemcp.Catalog{}, err
	}
	cat := mobilemcp.Catalog{Environment: st.Environment, Services: st.Services}
	if st.Problem != "" && len(st.Services) == 0 {
		return cat, errors.New(st.Problem)
	}
	items, err := b.recordable()
	if err != nil {
		return mobilemcp.Catalog{}, err
	}
	for _, it := range items {
		row := mobilemcp.CatalogItem{ID: it.ID, Title: it.Title}
		if strings.EqualFold(it.Kind, string(mobilerec.KindFlow)) {
			cat.Flows = append(cat.Flows, row)
		} else {
			cat.Scenarios = append(cat.Scenarios, row)
		}
	}
	return cat, nil
}
