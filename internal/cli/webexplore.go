package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/web"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/pagelist"
	"github.com/jancuk/semar/internal/record"
	"github.com/jancuk/semar/internal/screens"
	"github.com/jancuk/semar/internal/stage/plan"
)

// webExplorer writes the web tests of a plan. It is semar record with a model in the seat of the person:
// the same recorder, the same sign-in, the same files, and the same replay. Only who drives differs.
type webExplorer struct {
	o       options
	dir     string
	cfg     config.Config
	envName string
	env     config.Environment
	svc     config.Service
	home    string

	account, authProfile string
	app                  string
	first                string // the page that every flow starts at
}

// newWebExplorer settles who a flow signs in as, where it starts, and which screens file it adds to. as
// is the account of the plan, and the account of the service when it is empty.
func newWebExplorer(o options, dir string, cfg config.Config, envName string, env config.Environment, svc config.Service, as string) (*webExplorer, error) {
	account, authProfile, err := recordAccount(cfg, svc, as)
	if err != nil {
		return nil, err
	}
	home, err := o.home()
	if err != nil {
		return nil, err
	}
	first, err := firstPage(svc.BaseURL, svc.WebStart(), nil)
	if err != nil {
		return nil, err
	}
	return &webExplorer{
		o: o, dir: dir, cfg: cfg, envName: envName, env: env, svc: svc, home: home,
		account: account, authProfile: authProfile, app: svc.Name, first: first,
	}, nil
}

func manual(format string, args ...any) plan.WebOutcome {
	return plan.WebOutcome{Manual: fmt.Sprintf(format, args...)}
}

// Explore plays one flow. A failure of the browser, of the sign-in, or of the fixtures ends the flow as
// a manual check with its reason, and a failure of the plan, such as a cancel, is an error.
func (w *webExplorer) Explore(ctx context.Context, req plan.WebRequest, drive plan.FlowDriver) (plan.WebOutcome, error) {
	policy, err := effects.New(w.env)
	if err != nil {
		return plan.WebOutcome{}, err
	}
	if _, err := existingScenario(w.dir, string(req.ID), false); err != nil {
		return plan.WebOutcome{}, err
	}
	fx := &flowFixtures{w: w, id: string(req.ID)}
	defer fx.release()
	rec, err := startRecording(ctx, w.o, w.cfg, recordingWindow{
		Home: w.home, Environment: w.envName, Chrome: w.env.Web.Chrome, Account: w.account, Hidden: true, Policy: policy,
	}, w.first)
	if err != nil {
		if ctx.Err() != nil {
			return plan.WebOutcome{}, ctx.Err()
		}
		return manual("the browser could not be opened: %s", terminalSafe(oneLine(err.Error()))), nil
	}
	defer rec.Finish()
	x := web.NewExplorer(rec)
	x.RunID = string(w.o.RunID())
	br := &flowBrowser{w: w, rec: rec, x: x, fx: fx}
	end, err := drive(ctx, br)
	if err != nil {
		return plan.WebOutcome{}, err
	}
	events := rec.Finish()
	fx.release()
	if end.Manual != "" {
		return plan.WebOutcome{Manual: end.Manual, End: end}, nil
	}
	out, err := w.write(ctx, req, end, events, fx)
	out.End = end
	return out, err
}

// write builds the test from what the browser heard, writes it as record does, and replays it once. The
// replay must end as the recording predicts: passed when every check met its claim, failed when one did
// not. A test that ends otherwise is removed, because a wrong test costs more than a manual check.
func (w *webExplorer) write(ctx context.Context, req plan.WebRequest, end plan.FlowEnd, events []record.Event, fx *flowFixtures) (plan.WebOutcome, error) {
	claims := make([]record.Claim, len(end.Checks))
	for i, c := range end.Checks {
		claims[i] = record.Claim{Expect: c.Expect, Cite: req.Cite}
	}
	opts := record.Options{
		ID: req.ID, Ticket: req.Ticket, App: w.app, Service: w.svc.Name, BaseURL: w.svc.BaseURL,
		AuthProfile: w.authProfile, Given: fx.given, Values: record.NewValues(fx.vars()), Date: w.o.Clock(), Claims: claims,
	}
	var err error
	if opts.Existing, err = screens.Load(w.dir, w.app); err != nil && !errors.Is(err, screens.ErrNotFound) {
		return plan.WebOutcome{}, err
	}
	draft, err := record.Build(events, opts)
	switch {
	case errors.Is(err, record.ErrNoCheck):
		return manual("the browser heard no check, so nothing proves the criterion"), nil
	case errors.Is(err, record.ErrSecretName):
		return manual("the model typed into a password field, which a test cannot keep"), nil
	case err != nil:
		return manual("the recording could not be made into a test: %s", terminalSafe(oneLine(err.Error()))), nil
	}
	draft.Scenario.Title = oneLine(end.Done.Title)
	draft.Scenario.Intent = oneLine(end.Done.Intent)
	draft.Script.ExpectHash = domain.ExpectHash(draft.Scenario)
	if err := checkDraft(draft); err != nil {
		return manual("%s", strings.TrimPrefix(err.Error(), "nothing was written: ")), nil
	}
	written, err := persistDraft(w.dir, draft, w.app, req.Ticket, nil)
	if err != nil {
		return plan.WebOutcome{}, err
	}
	r := replayScenario(ctx, w.o, w.dir, w.envName, w.app, string(req.ID), true)
	if ctx.Err() != nil {
		w.remove(written, req.ID)
		return plan.WebOutcome{}, ctx.Err()
	}
	want := domain.OutcomePassed
	if len(draft.FailsNow) > 0 {
		want = domain.OutcomeFailed
	}
	if r.result == nil || r.result.Outcome != want {
		why := "it did not finish"
		if r.result != nil {
			why = "it ended " + string(r.result.Outcome)
			if len(r.result.Reasons) > 0 {
				why += ": " + terminalSafe(oneLine(r.result.Reasons[0].Text))
			}
		} else if r.err != nil {
			why = "it could not run: " + terminalSafe(oneLine(r.err.Error()))
		}
		w.remove(written, req.ID)
		return manual("the replay of the written test did not end as the recording predicts, %s was expected and %s, so the test was removed", want, why), nil
	}
	t := &plan.WebTest{
		Scenario: draft.Scenario, ScenarioPath: written.ScenarioPath, ScreensPath: written.ScreensPath,
		Replay: string(r.result.Outcome), Cite: req.Cite,
	}
	for _, f := range draft.FailsNow {
		t.FailsNow = append(t.FailsNow, plan.FailsNow{Element: f.Element, Expect: f.Expect, Shown: f.Shown, Cite: f.Cite})
	}
	return plan.WebOutcome{Test: t}, nil
}

// remove puts the repository back as it was before the test was written.
func (w *webExplorer) remove(written record.Written, id domain.ScenarioID) {
	_ = written.Undo()
	if store, err := evidence.Open(semarDir(w.dir)); err == nil {
		_, _ = store.RemoveScripts(id)
	}
}

// flowFixtures are the fixtures that one flow made. The browser makes them when the model names them in
// its first answer.
type flowFixtures struct {
	w     *webExplorer
	id    string
	given []domain.Given
	got   fixtureValues
	undo  func()
}

// release cleans the fixtures up. It may be called more than once.
func (f *flowFixtures) release() {
	if f.undo != nil {
		f.undo()
		f.undo = nil
	}
}

// vars are the values that the fixtures gave, and the ID of the run, which the test keeps as variables.
func (f *flowFixtures) vars() map[string]string {
	if f.got.vars == nil {
		return map[string]string{}
	}
	return f.got.vars
}

// make makes the fixtures the model named, as semar record --given does.
func (f *flowFixtures) make(ctx context.Context, given []plan.AnswerGiven) ([]plan.GivenOutput, error) {
	for _, g := range given {
		dg := domain.Given{Fixture: domain.FixtureID(g.Fixture), As: g.As}
		if len(g.With) > 0 {
			dg.With = map[string]string{}
		}
		for _, w := range g.With {
			v, _ := w.Value.(string)
			dg.With[w.Name] = v
		}
		f.given = append(f.given, dg)
	}
	w := f.w
	values, release, err := makeFixtures(ctx, io.Discard, io.Discard, w.o, w.dir, w.cfg, w.envName, w.env, f.id, w.authProfile, f.given)
	if err != nil {
		return nil, err
	}
	f.got, f.undo = values, release
	var out []plan.GivenOutput
	for _, key := range sortedKeys(values.vars) {
		alias, name, ok := strings.Cut(key, ".")
		if !ok || alias == "run" {
			continue
		}
		out = append(out, plan.GivenOutput{Alias: alias, Name: name, Value: values.vars[key]})
	}
	return out, nil
}

// flowBrowser is the browser of one flow as the plan sees it.
type flowBrowser struct {
	w   *webExplorer
	rec *web.Recorder
	x   *web.Explorer
	fx  *flowFixtures
}

func (b *flowBrowser) List(ctx context.Context) (pagelist.List, error) {
	b.rec.Settle(ctx)
	return b.x.List(ctx)
}

func (b *flowBrowser) Act(ctx context.Context, a plan.Action) (string, error) {
	switch a.Kind {
	case plan.ActionClick:
		return "", b.x.Click(ctx, a.Ref)
	case plan.ActionFill:
		return "", b.x.Fill(ctx, a.Ref, a.Text)
	case plan.ActionPress:
		return "", b.x.Press(ctx, a.Ref, a.Key)
	case plan.ActionDrag:
		return "", b.x.Drag(ctx, a.Ref, a.To)
	case plan.ActionCheck:
		return b.x.Check(ctx, a.Ref)
	}
	return "", fmt.Errorf("the action %q is not one that a flow does", a.Kind)
}

// Given makes the fixtures and loads the first page again, so that the page shows their data and the
// recording starts there.
func (b *flowBrowser) Given(ctx context.Context, given []plan.AnswerGiven) ([]plan.GivenOutput, error) {
	out, err := b.fx.make(ctx, given)
	if err != nil {
		return nil, err
	}
	if id := b.fx.vars()["run.id"]; id != "" {
		b.x.RunID = id
	}
	if err := b.rec.Reopen(b.w.first); err != nil {
		return nil, err
	}
	return out, nil
}

func sortedKeys(m map[string]string) []string { return slices.Sorted(maps.Keys(m)) }
