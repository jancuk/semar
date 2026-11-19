package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/web"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/pagelist"
	"github.com/jancuk/semar/internal/record"
	"github.com/jancuk/semar/internal/report"
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
	// builds declare the commit that each service with no build probe runs, for the replay of a test:
	// the plan does not judge a build, so it declares the head of the change.
	builds []string
}

// newWebExplorer settles who a flow signs in as, where it starts, and which screens file it adds to. as
// is the account of the plan, and the account of the service when it is empty.
func newWebExplorer(o options, dir string, cfg config.Config, envName string, env config.Environment, svc config.Service, as, head string) (*webExplorer, error) {
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
	var builds []string
	if head != "" {
		for _, s := range env.Services {
			if s.Build == nil {
				builds = append(builds, s.Name+"="+head)
			}
		}
	}
	return &webExplorer{
		o: o, dir: dir, cfg: cfg, envName: envName, env: env, svc: svc, home: home,
		account: account, authProfile: authProfile, app: svc.Name, first: first, builds: builds,
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
	fx.x = x
	end, err := drive(ctx, br)
	if err != nil {
		return plan.WebOutcome{}, err
	}
	blocked := rec.BlockedHosts()
	events := rec.Finish()
	fx.release()
	if end.Manual != "" {
		return plan.WebOutcome{Manual: end.Manual, End: end, Blocked: blocked}, nil
	}
	out, err := w.write(ctx, req, end, events, fx)
	out.End = end
	out.Blocked = blocked
	return out, err
}

// write builds the test from what the browser heard, writes it as record does, and replays it once. The
// replay must end as the recording predicts: passed when every check met its claim, failed when one did
// not. A test that ends otherwise is removed, because a wrong test costs more than a manual check.
func (w *webExplorer) write(ctx context.Context, req plan.WebRequest, end plan.FlowEnd, events []record.Event, fx *flowFixtures) (plan.WebOutcome, error) {
	claims := make([]record.Claim, len(end.Checks))
	for i, c := range end.Checks {
		if c.Check != nil {
			claims[i] = record.Claim{Check: c.Check, Cite: req.Cite}
			continue
		}
		expect, err := fx.x.Expand(c.Expect)
		if err != nil {
			return manual("the check of %s cannot be filled in: %s", c.Ref, terminalSafe(oneLine(err.Error()))), nil
		}
		claims[i] = record.Claim{Expect: expect, Cite: req.Cite}
	}
	vars := fx.vars()
	opts := record.Options{
		ID: req.ID, Ticket: req.Ticket, App: w.app, Service: w.svc.Name, BaseURL: w.svc.BaseURL,
		AuthProfile: w.authProfile, Given: fx.given, Values: record.NewValues(vars), Date: w.o.Clock(), Claims: claims,
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
	written, err := persistDraft(w.dir, draft, w.app, valueOr(req.Subject, req.Ticket), nil)
	if err != nil {
		return plan.WebOutcome{}, err
	}
	remove := func() error { return w.remove(written, req.ID) }
	r := replayScenario(ctx, w.o, w.dir, w.envName, w.app, string(req.ID), w.builds, true)
	if ctx.Err() != nil {
		_ = remove()
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
		_ = remove()
		return manual("the replay of the written test did not end as the recording predicts, %s was expected and %s, so the test was removed", want, why), nil
	}
	t := &plan.WebTest{
		Scenario: draft.Scenario, ScenarioPath: written.ScenarioPath, ScreensPath: written.ScreensPath,
		Replay: string(r.result.Outcome), Cite: req.Cite, Remove: remove,
	}
	for _, f := range draft.FailsNow {
		t.FailsNow = append(t.FailsNow, plan.FailsNow{Element: f.Element, Expect: f.Expect, Shown: f.Shown, Cite: f.Cite, Rule: f.Rule})
	}
	return plan.WebOutcome{Test: t}, nil
}

// remove puts the repository back as it was before the test was written.
func (w *webExplorer) remove(written record.Written, id domain.ScenarioID) error {
	err := written.Undo()
	store, serr := evidence.Open(semarDir(w.dir))
	if serr == nil {
		_, serr = store.RemoveScripts(id)
	}
	return errors.Join(err, serr)
}

// flowFixtures are the fixtures that one flow made. The browser makes them when the model names them in
// its first answer.
type flowFixtures struct {
	w     *webExplorer
	id    string
	given []domain.Given
	got   fixtureValues
	undo  func()
	// x is the browser of the flow, which types the ID of the run for {{run.id}} and the day or the time
	// for a date variable.
	x *web.Explorer
}

// release cleans the fixtures up. It may be called more than once.
func (f *flowFixtures) release() {
	if f.undo != nil {
		f.undo()
		f.undo = nil
	}
}

// vars are the values that the fixtures gave, the ID of the run, and the date variables that the flow
// typed or expected, which the test keeps as variables.
func (f *flowFixtures) vars() map[string]string {
	vars := map[string]string{}
	maps.Copy(vars, f.got.vars)
	if f.x != nil {
		maps.Copy(vars, f.x.Variables())
		if f.x.RunID != "" {
			vars["run.id"] = f.x.RunID
		}
	}
	return vars
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
	l, err := b.x.List(ctx)
	if err != nil {
		return l, err
	}
	return ownElements(l, b.x.RunID), nil
}

// ownElements leaves out each element that names another run: what an earlier flow or run left on the
// page. A model that sees such a record uses it, and its test then needs a record that is gone later.
func ownElements(l pagelist.List, own string) pagelist.List {
	l.Elements = slices.DeleteFunc(slices.Clone(l.Elements), func(e pagelist.Element) bool {
		_, other := domain.RunIDIn(e.Name+" "+e.Value, domain.RunID(own))
		return other
	})
	return l
}

func (b *flowBrowser) Act(ctx context.Context, a plan.Action) (string, error) {
	switch a.Kind {
	case plan.ActionClick:
		return "", b.x.Click(ctx, a.Ref)
	case plan.ActionFill:
		return "", b.x.Fill(ctx, a.Ref, a.Text)
	case plan.ActionSelect:
		return "", b.x.Select(ctx, a.Ref, a.Text)
	case plan.ActionHover:
		return "", b.x.Hover(ctx, a.Ref)
	case plan.ActionTick:
		return "", b.x.SetChecked(ctx, a.Ref, true)
	case plan.ActionUntick:
		return "", b.x.SetChecked(ctx, a.Ref, false)
	case plan.ActionPress:
		return "", b.x.Press(ctx, a.Ref, a.Key)
	case plan.ActionDrag:
		return "", b.x.Drag(ctx, a.Ref, a.To)
	case plan.ActionCheck:
		return b.x.Check(ctx, a.Ref)
	case plan.ActionAbsent, plan.ActionCount, plan.ActionState, plan.ActionAttribute, plan.ActionPage:
		return b.x.Assert(ctx, a.Claim())
	}
	return "", fmt.Errorf("the action %q is not one that a flow does", a.Kind)
}

func (b *flowBrowser) Prepare(ctx context.Context, checks []plan.Action) {
	claims := make([]domain.Check, len(checks))
	for i, a := range checks {
		claims[i] = a.Claim()
	}
	b.x.Prepare(ctx, claims)
}

func (b *flowBrowser) Compare(ctx context.Context, a plan.Action) (string, bool, error) {
	if a.Kind != plan.ActionCheck {
		r, err := b.x.Test(ctx, a.Claim())
		return r.Shown, r.Held, err
	}
	shown, err := b.x.Shows(ctx, a.Ref)
	if err != nil {
		return "", false, err
	}
	want, err := b.x.Expand(a.Expect)
	if err != nil {
		return "", false, err
	}
	return shown, shown == want, nil
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

// planWeb decides whether a plan explores its web criteria, and builds the explorer. A recording clicks
// and types, so the rules of where semar record runs hold here too: a test environment that allows test
// data, and a Chrome. A service has a web page when its start page answers text/html. When there is no
// explorer, the second value says why in one line, and each web criterion stays a manual check.
func planWeb(ctx context.Context, o options, f planFlags, cfg config.Config, envName string, env config.Environment, profile, head string) (plan.WebExplorer, string) {
	switch {
	case f.noWeb:
		return nil, "turned off with --no-web"
	case env.Kind == config.KindProduction:
		return nil, envName + " is a production environment, and a flow clicks and types"
	}
	policy, err := effects.New(env)
	switch {
	case err != nil:
		return nil, oneLine(err.Error())
	case policy.ReadOnly():
		return nil, envName + " allows only reads, and a flow clicks and types"
	}
	if _, err := web.FindChrome(env.Web.Chrome); err != nil {
		if text, ok := web.ChromeNotFoundText(err); ok {
			return nil, oneLine(text)
		}
		return nil, oneLine(err.Error())
	}
	env = withServiceAuth(cfg, env)
	var tried []string
	for _, svc := range env.Services {
		page, err := firstPage(svc.BaseURL, svc.WebStart(), nil)
		if err != nil {
			tried = append(tried, err.Error())
			continue
		}
		if !answersHTML(ctx, o.HTTP, page) {
			tried = append(tried, "GET "+page+" did not answer text/html")
			continue
		}
		x, err := newWebExplorer(o, f.dir, cfg, envName, env, svc, profile, head)
		if err != nil {
			return nil, oneLine(err.Error())
		}
		return x, ""
	}
	if len(tried) == 0 {
		return nil, "the environment has no service"
	}
	return nil, "no service has a web page: " + strings.Join(tried, "; ")
}

// answersHTML is one GET of a page, which reads nothing of its body.
func answersHTML(ctx context.Context, client *http.Client, page string) bool {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, page, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Accept", "text/html")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	kind, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	return err == nil && kind == "text/html" && resp.StatusCode < 500
}

// printWebPart is the web part of a plan in a few lines: what was explored, written, and left for a
// person with the reason, the turns, and the cost.
func printWebPart(w io.Writer, envName string, out plan.Output) {
	if out.Pipeline == nil || out.Pipeline.Web.Explored == 0 {
		return
	}
	st := out.Pipeline.Web
	criteria := "criteria"
	if st.Explored == 1 {
		criteria = "criterion"
	}
	written := plural(st.Written, "test") + " written"
	if st.FailsNow == 1 {
		written += " (1 fails on this build)"
	} else if st.FailsNow > 1 {
		written += fmt.Sprintf(" (%d fail on this build)", st.FailsNow)
	}
	var cost float64
	for _, u := range out.Pipeline.WebUses {
		cost += u.Result.CostUSD
	}
	line := fmt.Sprintf("web: %d %s explored, %s, %d left for a person, %s", st.Explored, criteria, written, st.Manual, plural(st.Turns, "turn"))
	if cost > 0 {
		line += fmt.Sprintf(", $%.2f", cost)
	}
	fmt.Fprintln(w, line)
	var blocked []report.BlockedHost
	for _, o := range out.Outcomes {
		if o.Web == nil {
			continue
		}
		for _, h := range o.Web.Blocked {
			blocked = report.AddBlocked(blocked, h, o.Packet.Cite)
		}
	}
	report.WriteBlocked(w, envName, "criterion", blocked)
	for _, o := range out.Outcomes {
		if o.Web != nil && o.Web.Test == nil {
			fmt.Fprintf(w, "web: left for a person: %s: %s\n", terminalSafe(o.Packet.Cite), terminalSafe(oneLine(o.Web.Manual)))
		}
	}
	for _, t := range out.WebTests {
		for _, f := range t.FailsNow {
			if f.Rule != "" {
				fmt.Fprintf(w, "web: %s fails now: the test expects the %s, and the page shows %s\n", t.Scenario.ID, terminalSafe(f.Rule), terminalSafe(f.Shown))
				continue
			}
			fmt.Fprintf(w, "web: %s fails now: %s should show %q and the page shows %q\n", t.Scenario.ID, terminalSafe(f.Element), terminalSafe(f.Expect), terminalSafe(f.Shown))
		}
	}
}

// removeWebTests takes the web tests of a plan that was not written back out of the repository, the
// newest first, because each one put the screens file back as it was before its own write.
func removeWebTests(stderr io.Writer, tests []plan.WebTest) {
	for _, t := range slices.Backward(tests) {
		if t.Remove == nil {
			continue
		}
		if err := t.Remove(); err != nil {
			fmt.Fprintf(stderr, "semar: the web test %s could not be removed: %s; remove %s by hand\n", t.Scenario.ID, terminalSafe(oneLine(err.Error())), terminalSafe(t.ScenarioPath))
		}
	}
}
