package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/confirm"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/api"
	"github.com/jancuk/semar/internal/engine/web"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/record"
	"github.com/jancuk/semar/internal/scenario"
	"github.com/jancuk/semar/internal/screens"
	"github.com/jancuk/semar/internal/script"
	"github.com/jancuk/semar/internal/secret"
)

type recordTestFlags struct {
	as        string
	service   string
	app       string
	open      string
	given     []string
	ticket    string
	covers    string
	secretEnv string
	replace   bool
	noReplay  bool
	dir       string
}

func newRecordCommand(o options) *cobra.Command {
	var f recordTestFlags
	cmd := &cobra.Command{
		Use:   "record <scenario id> [environment] [--open <path>] [--given <fixture>[ as <alias>]]",
		Short: "Record a web test by doing the flow once in Chrome",
		Long: "Open a Chrome window on Semar's own profile of the environment and the account, and record what you do until you close it. Semar then writes the screens file, the scenario, and the script, and replays the scenario once.\n" +
			"Click, type, press Enter or Escape, and drag as a user does. Hold Option and click what must be true: that is a check, it does nothing on the page, and the text that the element shows becomes the expected result. A recording with no check writes nothing.\n" +
			"A password field is never read: the test types the value of an environment variable, named with --secret-env.\n" +
			"Nothing that record writes is approved. The screens file and the scenario go through a review.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			env := ""
			if len(args) == 2 {
				env = args[1]
			}
			return recordTest(cmd.Context(), cmd, o, f, args[0], env)
		},
	}
	cmd.Flags().StringVar(&f.as, "as", "", "the account whose browser profile and session the recording uses: an auth profile of semar.yaml")
	cmd.Flags().StringVar(&f.service, "service", "", "the service to record against, when the environment has several")
	cmd.Flags().StringVar(&f.app, "app", "", "the screens file to add to, .semar/screens/<app>.yaml; the default is the name of the service")
	cmd.Flags().StringVar(&f.open, "open", "", "the first page, a path below the service; it may use {{variables}} of the fixtures")
	cmd.Flags().StringArrayVar(&f.given, "given", nil, "make a fixture before the window opens, as <fixture> or <fixture> as <alias>; repeat for more")
	cmd.Flags().StringVar(&f.ticket, "ticket", "", "the ticket of the scenario; the default is the group of the ID, as SHOP-482 for SHOP-482-07")
	cmd.Flags().StringVar(&f.covers, "covers", "", "the use case that the scenario covers: print the command that records the link")
	cmd.Flags().StringVar(&f.secretEnv, "secret-env", "", "the environment variable that holds the password that you type")
	cmd.Flags().BoolVar(&f.replace, "replace", false, "write over the scenario of this ID if there is one")
	cmd.Flags().BoolVar(&f.noReplay, "no-replay", false, "do not replay the scenario once after it is written")
	addDirFlag(cmd, &f.dir)
	return cmd
}

// recordingHooks let a test stand where the person stands: it hides the window and sends the input.
// The window is closed when drive returns. The command is the same code either way.
type recordingHooks struct {
	hidden bool
	drive  func(tab context.Context)
}

type recordingKey struct{}

func withRecording(ctx context.Context, h recordingHooks) context.Context {
	return context.WithValue(ctx, recordingKey{}, h)
}

func recordingOf(ctx context.Context) recordingHooks {
	h, _ := ctx.Value(recordingKey{}).(recordingHooks)
	return h
}

// trustedScreensKey carries the app whose screens a run treats as approved, with no approval stored.
// Only semar record sets it, for the replay of what it has just written.
type trustedScreensKey struct{}

func withTrustedScreens(ctx context.Context, app string) context.Context {
	return context.WithValue(ctx, trustedScreensKey{}, app)
}

func trustedScreens(ctx context.Context) string {
	app, _ := ctx.Value(trustedScreensKey{}).(string)
	return app
}

// trustedExpectationsKey carries the scenario whose expectations a run treats as confirmed, with no
// confirmation stored. Only a plan sets it, for the replay of the test that a flow has just written: that
// replay says whether the page meets what the criterion says, which a person has not read yet.
type trustedExpectationsKey struct{}

func withTrustedExpectations(ctx context.Context, id domain.ScenarioID) context.Context {
	return context.WithValue(ctx, trustedExpectationsKey{}, id)
}

func trustedExpectations(ctx context.Context) domain.ScenarioID {
	id, _ := ctx.Value(trustedExpectationsKey{}).(domain.ScenarioID)
	return id
}

// trustingConfirm confirms every source of one scenario and leaves every other scenario to the store.
type trustingConfirm struct {
	confirm.Store
	id domain.ScenarioID
}

func (t trustingConfirm) Get(id domain.ScenarioID, h domain.Hash) (confirm.Record, bool, error) {
	if id != t.id {
		return t.Store.Get(id, h)
	}
	return confirm.Record{
		Scenario: id, ExpectHash: h, User: "plan", Identity: "this replay only",
		Sources: []domain.Source{domain.SourceTicket, domain.SourceTestcase, domain.SourceDiff, domain.SourceBaseline},
	}, true, nil
}

func recordTest(ctx context.Context, cmd *cobra.Command, o options, f recordTestFlags, id, envArg string) error {
	out := cmd.OutOrStdout()
	if err := idcheck.Scenario(id); err != nil {
		return err
	}
	ticket := f.ticket
	if ticket == "" {
		ticket = groupOf(id)
	}
	if err := idcheck.Check("ticket", ticket); err != nil {
		return err
	}
	cfg, err := config.Load(f.dir)
	if err != nil {
		return err
	}
	envName, env, err := cfg.Environment(envArg)
	if err != nil {
		return err
	}
	switch {
	case env.Kind == config.KindProduction:
		return fmt.Errorf("the environment %q is a production environment, and a recording clicks and types, so semar record does not run there", envName)
	case env.Effects.Mode == "read_only":
		return fmt.Errorf("the environment %q is read only, and a recording clicks and types, so it needs an environment that allows test data", envName)
	}
	env = withServiceAuth(cfg, env)
	svc, err := recordService(env, f.service)
	if err != nil {
		return err
	}
	account, authProfile, err := recordAccount(cfg, svc, f.as)
	if err != nil {
		return err
	}
	if len(f.given) > 0 && authProfile == "" && len(cfg.AuthProfiles) > 0 {
		return fmt.Errorf("the fixtures are made with an account: name one with --as, %s", strings.Join(slices.Sorted(maps.Keys(cfg.AuthProfiles)), ", "))
	}
	app := f.app
	if app == "" {
		app = svc.Name
	}
	if err := idcheck.Check("app name", app); err != nil {
		return err
	}
	if f.secretEnv != "" {
		if err := record.CheckSecretEnv(f.secretEnv); err != nil {
			return err
		}
	}
	replace, err := existingScenario(f.dir, id, f.replace)
	if err != nil {
		return err
	}
	if _, err := web.FindChrome(env.Web.Chrome); err != nil {
		if text, ok := web.ChromeNotFoundText(err); ok {
			return errors.New(text)
		}
		return err
	}
	home, err := o.home()
	if err != nil {
		return err
	}
	given, err := parseGiven(f.given)
	if err != nil {
		return err
	}

	values, release, err := makeFixtures(ctx, out, cmd.ErrOrStderr(), o, f.dir, cfg, envName, env, id, authProfile, given)
	if err != nil {
		return err
	}
	defer release()

	first, err := firstPage(svc.BaseURL, f.open, values.vars)
	if err != nil {
		return err
	}
	hooks := recordingOf(ctx)
	rec, err := startRecording(ctx, o, cfg, recordingWindow{Home: home, Environment: envName, Chrome: env.Web.Chrome, Account: account, Hidden: hooks.hidden}, first)
	if err != nil {
		return fmt.Errorf("nothing was recorded: %w", err)
	}
	defer rec.Finish()
	if who, ok := rec.SignedIn(); ok {
		fmt.Fprintf(out, "Signed in as %s.\n", who)
	}
	fmt.Fprintf(out, "A Chrome window is open at %s\n  Do the flow. Hold Option and click what must be true. Close the window to finish.\n", first)
	if hooks.drive != nil {
		go func() {
			defer rec.CloseWindow()
			hooks.drive(rec.Context())
		}()
	}
	if err := rec.Wait(ctx); err != nil {
		return fmt.Errorf("the recording was interrupted, and nothing was written: %w", err)
	}
	events := rec.Finish()
	release()

	opts := record.Options{
		ID: domain.ScenarioID(id), Ticket: ticket, App: app, Service: svc.Name, BaseURL: svc.BaseURL,
		AuthProfile: authProfile, Given: given, Values: record.NewValues(values.vars),
		SecretEnv: f.secretEnv, Date: o.Clock(),
	}
	if opts.Existing, err = screens.Load(f.dir, app); err != nil && !errors.Is(err, screens.ErrNotFound) {
		return err
	}
	draft, err := record.Build(events, opts)
	if errors.Is(err, record.ErrSecretName) && o.Terminal() {
		fmt.Fprint(out, "\nA password was typed. The name of the environment variable that holds the password: ")
		name, _ := bufio.NewReader(o.Stdin).ReadString('\n')
		opts.SecretEnv = strings.TrimSpace(name)
		draft, err = record.Build(events, opts)
	}
	if errors.Is(err, record.ErrNoCheck) {
		fmt.Fprintf(out, "\nRecorded %s and no check.\n", plural(countActions(events), "action"))
		return fmt.Errorf("nothing was written: %w", err)
	}
	if err != nil {
		return err
	}
	written, err := persistDraft(f.dir, draft, app, ticket, replace)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "\nRecorded %s and %s.\n", plural(draft.Actions, "action"), plural(draft.Checks, "check"))
	for _, n := range draft.Notes {
		fmt.Fprintf(out, "Note: %s.\n", terminalSafe(n))
	}
	fmt.Fprintf(out, "Wrote %s (%s, %s)\n      %s\n      the script of %s\n",
		written.ScreensPath, plural(written.Screens, "screen"), plural(written.Elements, "element"), written.ScenarioPath, id)
	if !f.noReplay {
		replayOnce(ctx, out, o, f, envName, app, id)
	}
	fmt.Fprintf(out, "\nNext: semar review %s\n", id)
	if f.covers != "" {
		fmt.Fprintf(out, "      semar check %s --by %s\n", f.covers, id)
	}
	return nil
}

// groupOf is the ticket of an ID that ends in a number: SHOP-482 for SHOP-482-07. Any other ID is its own group.
func groupOf(id string) string {
	i := strings.LastIndex(id, "-")
	if i <= 0 || i == len(id)-1 || strings.Trim(id[i+1:], "0123456789") != "" {
		return id
	}
	return id[:i]
}

func countActions(events []record.Event) int {
	n := 0
	for _, e := range events {
		if e.Kind != record.KindCheck {
			n++
		}
	}
	return n
}

func recordService(env config.Environment, name string) (config.Service, error) {
	switch {
	case name != "":
		i := slices.IndexFunc(env.Services, func(s config.Service) bool { return s.Name == name })
		if i < 0 {
			return config.Service{}, fmt.Errorf("the environment has no service %s", name)
		}
		return env.Services[i], nil
	case len(env.Services) == 1:
		return env.Services[0], nil
	case len(env.Services) == 0:
		return config.Service{}, errors.New("the environment has no service to record against")
	}
	names := make([]string, len(env.Services))
	for i, s := range env.Services {
		names[i] = s.Name
	}
	return config.Service{}, fmt.Errorf("choose the service to record against with --service: %s", strings.Join(names, ", "))
}

// recordAccount is the account whose Chrome profile the recording uses, and the auth profile that the
// scenario then names. An account that the configuration lacks is the profile named default, and the
// scenario sends no session then.
func recordAccount(cfg config.Config, svc config.Service, as string) (account, authProfile string, err error) {
	account = as
	switch {
	case account != "":
		if _, ok := cfg.AuthProfiles[account]; !ok {
			return "", "", fmt.Errorf("account %s is not in the configuration", account)
		}
	case svc.AuthProfile != "":
		account = svc.AuthProfile
	default:
		account = "default"
	}
	if _, ok := cfg.AuthProfiles[account]; ok {
		authProfile = account
	}
	return account, authProfile, nil
}

// existingScenario gives the files that a scenario of this ID has, relative to the repository, and
// refuses when there are some and the person did not say that they may be replaced.
func existingScenario(repo, id string, replace bool) ([]string, error) {
	files, err := scenarioFiles(repo)
	if err != nil {
		return nil, err
	}
	have := files[domain.ScenarioID(id)]
	if len(have) == 0 {
		return nil, nil
	}
	rel := make([]string, len(have))
	for i, p := range have {
		r, err := filepath.Rel(repo, p)
		if err != nil {
			r = p
		}
		rel[i] = filepath.ToSlash(r)
	}
	if !replace {
		return nil, fmt.Errorf("scenario %s exists already: %s; use --replace to record it again", id, strings.Join(rel, ", "))
	}
	return rel, nil
}

// parseGiven reads --given <fixture>[ as <alias>]. The alias is the first word of the fixture ID when
// there is none.
func parseGiven(specs []string) ([]domain.Given, error) {
	var out []domain.Given
	for _, spec := range specs {
		fx, alias, has := strings.Cut(spec, " as ")
		fx, alias = strings.TrimSpace(fx), strings.TrimSpace(alias)
		if fx == "" || (has && alias == "") {
			return nil, fmt.Errorf("--given %q must be <fixture> or <fixture> as <alias>", spec)
		}
		if !has {
			alias = strings.ReplaceAll(strings.SplitN(fx, ".", 2)[0], "-", "_")
		}
		out = append(out, domain.Given{Fixture: domain.FixtureID(fx), As: alias})
	}
	return out, nil
}

// firstPage is the address that the window opens: the base URL of the service and the path, with the
// outputs of the fixtures put in.
func firstPage(base, path string, vars map[string]string) (string, error) {
	if path == "" {
		return base, nil
	}
	w, err := script.RenderWeb(domain.WebAction{Do: domain.WebOpen, Path: path}, vars)
	if err != nil {
		return "", fmt.Errorf("--open %s: %w", path, err)
	}
	head, tail := script.SplitWebPath(w.Path)
	u, err := api.JoinURL(base, head, nil)
	if err != nil {
		return "", fmt.Errorf("--open %s: %w", path, err)
	}
	if tail == "" {
		return u.String(), nil
	}
	u2, err := url.Parse(u.String() + tail)
	if err != nil {
		return "", fmt.Errorf("--open %s: %w", path, err)
	}
	return u2.String(), nil
}

// fixtureValues are what the fixtures of a recording gave, by variable name, with the run ID.
type fixtureValues struct{ vars map[string]string }

// makeFixtures makes the fixtures of the recording before the window opens. The function that it
// returns cleans them up, and may be called more than once.
func makeFixtures(ctx context.Context, out, errw io.Writer, o options, dir string, cfg config.Config, envName string, env config.Environment, id, authProfile string, given []domain.Given) (fixtureValues, func(), error) {
	none := func() {}
	if len(given) == 0 {
		return fixtureValues{vars: map[string]string{}}, none, nil
	}
	cat, err := fixture.Load(dir, allServices(cfg), fixtureAccounts(cfg))
	if err != nil {
		return fixtureValues{}, none, err
	}
	for _, p := range cat.Problems {
		fmt.Fprintf(errw, "semar: fixture file %s: %s; this recording does not use it\n", terminalSafe(p.File), terminalSafe(oneLine(problemText(p))))
	}
	needs := sessionNeeds(cfg, env, []domain.Scenario{{ID: domain.ScenarioID(id), AuthProfile: authProfile, Given: given}}, &cat)
	rg, err := newEnvRig(ctx, o, dir, cfg, env, nil, needs)
	if err != nil {
		return fixtureValues{}, none, err
	}
	resolver, err := rg.resolver(o, envName, env, cat)
	if err != nil {
		return fixtureValues{}, none, err
	}
	res, err := resolver.Resolve(ctx, fixture.Request{Owner: domain.ScenarioID(id), Given: slices.Clone(given), AuthProfile: authProfile, Pass: domain.PassSetup})
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		for _, rec := range resolver.Release(cctx, res) {
			if rec.Cleanup == fixture.CleanupFailed {
				fmt.Fprintf(errw, "semar: the fixture %s was not cleaned up: %s\n", rec.Fixture, terminalSafe(oneLine(strings.Join(rec.Left, "; "))))
			}
		}
		_ = rg.run.Close(domain.Run{ID: rg.id, Environment: envName, EnvironmentKind: env.Kind, Purpose: domain.PurposeRecording, StartedAt: o.Clock(), EndedAt: o.Clock()})
	}
	switch {
	case err != nil:
		release()
		return fixtureValues{}, none, fmt.Errorf("the fixtures could not be made: %w", err)
	case len(res.Blocked) > 0:
		release()
		return fixtureValues{}, none, fmt.Errorf("the fixtures could not be made: %s", terminalSafe(oneLine(strings.Join(res.Blocked, "; "))))
	}
	for _, g := range given {
		fmt.Fprintf(out, "Made the fixture %s.\n", g.Fixture)
	}
	vars := maps.Clone(res.Vars)
	vars["run.id"] = string(rg.id)
	return fixtureValues{vars: vars}, release, nil
}

// replayed is how the replay of a written scenario ended.
type replayed struct {
	result *domain.ScenarioResult // nil when the scenario did not finish
	report string
	stderr string
	err    error
}

// replayScenario runs the scenario that was just written, with no window, as semar run does, with the
// screens of its app treated as approved for this one run, and, when trustExpectations is set, the
// expectations of the scenario treated as confirmed for it.
func replayScenario(ctx context.Context, o options, dir, envName, app, id string, trustExpectations bool) replayed {
	var r replayed
	var stderr bytes.Buffer
	flags := runFlags{
		scenarios: []string{id}, env: envName, dir: dir,
		done: func(run domain.Run, path string) {
			if len(run.Results) == 1 {
				r.result, r.report = &run.Results[0], path
			}
		},
	}
	ctx = withTrustedScreens(ctx, app)
	if trustExpectations {
		ctx = withTrustedExpectations(ctx, domain.ScenarioID(id))
	}
	r.err = runDaily(ctx, io.Discard, &stderr, o, flags)
	r.stderr = stderr.String()
	return r
}

// replayOnce runs the scenario that was just written and says how it ended. A scenario that does not
// replay stays written, so that the person can fix a step or record again.
func replayOnce(ctx context.Context, out io.Writer, o options, f recordTestFlags, envName, app, id string) {
	r := replayScenario(ctx, o, f.dir, envName, app, id, false)
	var exit exitError
	switch {
	case r.result == nil && r.err != nil && !errors.As(r.err, &exit):
		fmt.Fprintf(out, "Replayed once: it could not run: %s\n", terminalSafe(oneLine(r.err.Error())))
	case r.result == nil:
		fmt.Fprintf(out, "Replayed once: it did not finish.\n%s", terminalSafe(r.stderr))
	case r.result.Outcome == domain.OutcomePassed:
		fmt.Fprintln(out, "Replayed once: passed.")
	default:
		fmt.Fprintf(out, "Replayed once: %s.\n", r.result.Outcome)
		for _, rs := range r.result.Reasons {
			step := ""
			if rs.Step != "" {
				step = "step " + string(rs.Step) + ": "
			}
			fmt.Fprintf(out, "  %s%s\n", step, terminalSafe(oneLine(rs.Text)))
		}
		fmt.Fprintf(out, "  The files are written. Fix the step in the file, or record it again with --replace.\n  report: %s\n", r.report)
	}
}

// recordingWindow is what a recorder needs to open: the profile that it browses on.
type recordingWindow struct {
	Home, Environment, Chrome, Account string
	Hidden                             bool
	Policy                             *effects.Policy
}

// startRecording opens the recorder, signs in when the account has a web sign-in, and goes to the first
// page. The recording starts there. The person of semar record and the model of a plan use it alike.
func startRecording(ctx context.Context, o options, cfg config.Config, w recordingWindow, first string) (*web.Recorder, error) {
	ro := web.RecorderOptions{Chrome: w.Chrome, Home: w.Home, Environment: w.Environment, Account: w.Account, Hidden: w.Hidden, Policy: w.Policy}
	if who := cfg.AuthProfiles[w.Account].Web; who != nil {
		ro.SignIn = &web.RecorderSignIn{Account: w.Account, Who: *who, Secrets: secret.NewEnv(o.LookupEnv)}
	}
	rec, err := web.StartRecorder(ctx, ro)
	if err != nil {
		return nil, err
	}
	if err := rec.Open(first); err != nil {
		rec.Finish()
		return nil, err
	}
	return rec, nil
}

// checkDraft refuses a draft that makes a scenario or a script that is not valid, in the words that
// semar record prints, before anything is written.
func checkDraft(draft record.Draft) error {
	if ps := scenario.Validate(draft.Scenario, scenario.FromFile); len(ps) > 0 {
		return fmt.Errorf("nothing was written: the recording makes a scenario that is not valid: %s: %s", ps[0].Path, terminalSafe(oneLine(ps[0].Message)))
	}
	if ps := script.Validate(draft.Scenario, draft.Script); len(ps) > 0 {
		msgs := make([]string, len(ps))
		for i, p := range ps {
			msgs[i] = terminalSafe(oneLine(p.Message))
			if p.Step != "" {
				msgs[i] = "step " + string(p.Step) + ": " + msgs[i]
			}
		}
		return fmt.Errorf("nothing was written: the recording makes a script that does not fit its scenario: %s", strings.Join(msgs, "; "))
	}
	return nil
}

// persistDraft checks a draft and writes its screens, its scenario, and its script. A script that does
// not fit its scenario is stored by neither, and the files are put back.
func persistDraft(dir string, draft record.Draft, app, ticket string, replace []string) (record.Written, error) {
	if err := checkDraft(draft); err != nil {
		return record.Written{}, err
	}
	written, err := record.Write(dir, draft, record.WriteOptions{App: app, Subject: ticket, Replace: replace})
	if err != nil {
		return record.Written{}, err
	}
	_, _, problems, err := storeScript(dir, draft.Script)
	if err == nil && len(problems) > 0 {
		err = fmt.Errorf("the script of the recording does not fit its scenario: %s", strings.Join(problems, "; "))
	}
	if err != nil {
		if undoErr := written.Undo(); undoErr != nil {
			err = errors.Join(err, undoErr)
		}
		return record.Written{}, err
	}
	return written, nil
}
