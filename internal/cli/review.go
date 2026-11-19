package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/confirm"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/journey"
	"github.com/jancuk/semar/internal/obligation"
	"github.com/jancuk/semar/internal/screens"
)

type reviewFlags struct {
	yes bool
	env string
	dir string
}

func newReviewCommand(o options) *cobra.Command {
	var f reviewFlags
	cmd := &cobra.Command{
		Use:   "review [id...]",
		Short: "Look at everything that needs a person, and approve it one item at a time",
		Long: "Show what needs a person, one item at a time: the fixtures that are not approved, the screens files that are not approved, the scenarios that need a confirmation, grouped by use case, and the journeys.\n" +
			"Answer y to approve an item, n to skip it, d for its details, and q to stop. An ID narrows the list: a scenario, a ticket, a fixture, a journey, or the app of a screens file.\n" +
			"--yes approves every item with no question. With no terminal and no --yes, the items are listed and nothing is approved.\n" +
			"Exit 0 when nothing is left to review, and 2 when an item was skipped or the review stopped.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReview(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o, f, args)
		},
	}
	cmd.Flags().BoolVar(&f.yes, "yes", false, "approve every item without asking")
	cmd.Flags().StringVar(&f.env, "env", "", "the environment whose auth profiles the legs of a journey use; needed when there are several")
	addDirFlag(cmd, &f.dir)
	return cmd
}

// approver is who approves, and when. The email is the user.email of git, and it can be empty.
type approver struct {
	name, email string
	at          time.Time
}

// reviewItem is one thing for a person to look at and approve.
type reviewItem struct {
	kind, id string
	summary  string
	show     func(w io.Writer, n, total int)
	details  func(w io.Writer)
	approve  func(store *confirm.Local, a approver) error
	// drop retires the item instead of approving it. Only an edge case has it: a person who finds a
	// case not worth keeping drops it, and a later plan does not draft it again.
	drop func(w io.Writer, o options, dir string) error
}

func runReview(ctx context.Context, stdout, stderr io.Writer, o options, f reviewFlags, args []string) error {
	for _, a := range args {
		if err := idcheck.Check("ID", a); err != nil {
			return err
		}
	}
	items, notes, err := collectReview(stderr, f, args)
	if err != nil {
		return err
	}
	for _, n := range notes {
		fmt.Fprintln(stdout, n)
	}
	if len(items) == 0 {
		if len(notes) == 0 {
			fmt.Fprintln(stdout, "nothing needs review")
		}
		return nil
	}

	if !f.yes && !o.Terminal() {
		fmt.Fprintf(stdout, "%s need review:\n", plural(len(items), "item"))
		for _, it := range items {
			fmt.Fprintf(stdout, "  %s\n", it.summary)
		}
		fmt.Fprintln(stderr, "semar: nothing was approved: the input is not a terminal, so pass --yes to approve")
		return exitError{ExitUndecided}
	}
	left, err := approveItems(ctx, stdout, o, f, newLineReader(o.Stdin), items)
	if err != nil {
		return err
	}
	if left > 0 {
		return exitError{ExitUndecided}
	}
	return nil
}

// approveItems asks about each item in turn, or approves them all with --yes, says how many were
// approved, and returns how many are left. semar review and semar run share it.
func approveItems(ctx context.Context, stdout io.Writer, o options, f reviewFlags, in *lineReader, items []reviewItem) (int, error) {
	name, err := o.User()
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(name) == "" {
		return 0, errors.New("the approver has no name; set git config user.name")
	}
	who := approver{name: name, email: o.Email(f.dir)}
	store, err := evidence.Open(semarDir(f.dir))
	if err != nil {
		return 0, err
	}
	approvals := confirm.NewLocal(store.Root())
	approve := func(it reviewItem) error {
		who.at = o.Clock().UTC()
		if err := it.approve(approvals, who); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "approved %s as %s (%s)\n", it.id, terminalSafe(oneLine(who.name)), localIdentity)
		return nil
	}

	approved, skipped, dropped, stopped := 0, 0, 0, false
loop:
	for i, it := range items {
		it.show(stdout, i+1, len(items))
		if f.yes {
			if err := approve(it); err != nil {
				return 0, err
			}
			approved++
			continue
		}
		for {
			menu := "[y] approve  [n] skip  [d] details  [q] stop"
			if it.drop != nil {
				menu = "[y] approve  [n] skip  [x] drop  [d] details  [q] stop"
			}
			fmt.Fprintf(stdout, "\n  %s\n> ", menu)
			line, ok := in.read(ctx)
			if !ok {
				fmt.Fprintln(stdout)
				stopped = true
				break loop
			}
			switch strings.ToLower(strings.TrimSpace(line)) {
			case "y", "yes":
				if err := approve(it); err != nil {
					return 0, err
				}
				approved++
			case "n", "no":
				skipped++
			case "x", "drop":
				if it.drop == nil {
					fmt.Fprintln(stdout, "answer y, n, d, or q")
					continue
				}
				if err := it.drop(stdout, o, f.dir); err != nil {
					return 0, err
				}
				dropped++
			case "d", "details":
				it.details(stdout)
				continue
			case "q", "quit":
				stopped = true
				break loop
			default:
				fmt.Fprintln(stdout, "answer y, n, d, or q")
				continue
			}
			fmt.Fprintln(stdout)
			break
		}
	}
	left := len(items) - approved - dropped
	fmt.Fprintf(stdout, "approved %d of %s", approved, plural(len(items), "item"))
	if dropped > 0 {
		fmt.Fprintf(stdout, ", dropped %d", dropped)
	}
	if left > 0 {
		fmt.Fprintf(stdout, "; %d left", left)
		if skipped > 0 || stopped {
			fmt.Fprint(stdout, ": run semar review again to come back to them")
		}
	}
	fmt.Fprintln(stdout)
	return left, nil
}

// picker says which objects the IDs on the command line name. With no ID, every object is named.
type picker struct {
	ids []string
	hit map[string]bool
}

func (p *picker) all() bool { return len(p.ids) == 0 }

// wants is true when the object is named by one of its keys, or when no ID was given. An object that
// is named counts as found, whether or not it needs a review.
func (p *picker) wants(keys ...string) bool {
	if p.all() {
		return true
	}
	named := false
	for _, k := range keys {
		if slices.Contains(p.ids, k) {
			p.hit[k] = true
			named = true
		}
	}
	return named
}

func collectReview(stderr io.Writer, f reviewFlags, args []string) ([]reviewItem, []string, error) {
	cfg, err := loadConfigOrNone(f.dir)
	if err != nil {
		return nil, nil, err
	}
	p := &picker{ids: args, hit: map[string]bool{}}
	approvals := confirm.NewLocal(semarDir(f.dir))
	var notes []string
	var items []reviewItem
	var cataloguePtr *fixture.Catalogue
	var catalogueErr error
	loadCatalogue := func() (fixture.Catalogue, error) {
		if cataloguePtr == nil && catalogueErr == nil {
			c, err := fixture.Load(f.dir, nil)
			cataloguePtr, catalogueErr = &c, err
		}
		if catalogueErr != nil {
			return fixture.Catalogue{}, catalogueErr
		}
		return *cataloguePtr, nil
	}

	apps, err := screens.List(f.dir)
	if err != nil {
		return nil, nil, err
	}
	for _, app := range apps {
		if !p.wants(app) {
			continue
		}
		scr, err := screens.Load(f.dir, app)
		if err != nil {
			if p.all() {
				fmt.Fprintf(stderr, "semar: the screens of %s: %s; they are not in this review\n", terminalSafe(app), terminalSafe(oneLine(err.Error())))
				continue
			}
			return nil, nil, err
		}
		h := domain.ScreensHash(scr)
		if h == "" {
			return nil, nil, fmt.Errorf("the screens of %s cannot be hashed, so they cannot be approved", app)
		}
		done, err := approvals.ScreensApproved(app, h)
		if err != nil {
			return nil, nil, err
		}
		if done {
			if !p.all() {
				notes = append(notes, fmt.Sprintf("the screens of %s are already approved for hash %s", app, h))
			}
			continue
		}
		before, ok, err := approvals.LatestScreens(app)
		if err != nil {
			return nil, nil, err
		}
		var prev *confirm.ScreensApproval
		if ok {
			prev = &before
		}
		items = append(items, screensItem(scr, h, prev))
	}

	scs, err := loadAllScenarios(f.dir)
	if err != nil {
		return nil, nil, err
	}
	trust := cfg.Policy.Trust.Confirm
	uses, _, _ := obligation.Load(f.dir)
	approvedFixture := func(id domain.FixtureID) string {
		cat, err := loadCatalogue()
		if err != nil {
			return "not found"
		}
		fx, ok := cat.Get(id)
		if !ok {
			return "not found"
		}
		if done, err := approvals.FixtureApproved(id, domain.FixtureHash(fx)); err == nil && done {
			return "approved"
		}
		return "not approved: semar review " + string(id)
	}
	var todo []unconfirmed
	for _, sc := range scs {
		if !p.wants(string(sc.ID), sc.Ticket) {
			continue
		}
		u := needsConfirmation(f.dir, sc, trust)
		if len(u.lines) == 0 {
			if !p.all() && slices.Contains(p.ids, string(sc.ID)) {
				notes = append(notes, fmt.Sprintf("no expected result of scenario %s needs a confirmation", sc.ID))
			}
			continue
		}
		if why := unconfirmedWhy(approvals, trust, sc, ""); why == "" {
			if !p.all() && slices.Contains(p.ids, string(sc.ID)) {
				notes = append(notes, fmt.Sprintf("scenario %s is already confirmed", sc.ID))
			}
			continue
		}
		u.file, u.gherkin = scenarioText(f.dir, sc)
		todo = append(todo, u)
	}
	items = append(items, scenarioItems(f.dir, uses, todo, approvedFixture, scenarioViewer(f.dir))...)

	js, problems, err := loadJourneys(f.dir)
	if err != nil {
		return nil, nil, err
	}
	if p.all() {
		for _, pr := range problems {
			fmt.Fprintf(stderr, "semar: journey file %s; it is not in this review\n", problemLine(pr))
		}
	}
	var chosen []domain.Journey
	for _, j := range js {
		if p.wants(string(j.ID)) {
			chosen = append(chosen, j)
		}
	}
	if len(chosen) > 0 {
		_, env, err := cfg.Environment(f.env)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: the legs of a journey act as the accounts of an environment, so name it with --env", err)
		}
		env = withServiceAuth(cfg, env)
		for _, j := range chosen {
			if it, ok := journeyItem(f.dir, approvals, trust, env, j); ok {
				items = append(items, it)
			} else if !p.all() {
				notes = append(notes, fmt.Sprintf("no expected result of journey %s needs a confirmation", j.ID))
			}
		}
	}

	// The fixtures are read when the review is of everything, or when an ID is still unmatched. A
	// review of one scenario never reads them, so a problem there cannot stop it.
	unmatched := slices.ContainsFunc(args, func(a string) bool { return !p.hit[a] })
	var fixtureItems []reviewItem
	if p.all() || unmatched {
		cat, err := loadCatalogue()
		if err != nil {
			return nil, nil, err
		}
		if p.all() {
			for _, pr := range cat.Problems {
				fmt.Fprintf(stderr, "semar: fixture file %s: %s; it is not in this review\n", terminalSafe(pr.File), terminalSafe(oneLine(problemText(pr))))
			}
		}
		for _, fx := range cat.Fixtures {
			if !p.wants(string(fx.ID)) {
				continue
			}
			h := domain.FixtureHash(fx)
			if h == "" {
				return nil, nil, fmt.Errorf("fixture %s cannot be hashed, so it cannot be approved", fx.ID)
			}
			done, err := approvals.FixtureApproved(fx.ID, h)
			if err != nil {
				return nil, nil, err
			}
			if done {
				if !p.all() {
					notes = append(notes, fmt.Sprintf("fixture %s %s is already approved for hash %s", fx.ID, fx.Version, h))
				}
				continue
			}
			fixtureItems = append(fixtureItems, fixtureItem(fx, h))
		}
	}
	items = append(fixtureItems, items...)

	for _, a := range args {
		if p.hit[a] {
			continue
		}
		if _, err := fixture.LoadOne(f.dir, domain.FixtureID(a), nil); err != nil && !strings.Contains(err.Error(), "was not found") && !strings.Contains(err.Error(), "invalid fixture ID") {
			return nil, nil, err
		}
		for _, pr := range problems {
			if strings.HasPrefix(pr.File, journeysRel+"/"+a+".") {
				return nil, nil, fmt.Errorf("journey %s is not valid, so it cannot be reviewed: %s", a, problemLine(pr))
			}
		}
		return nil, nil, fmt.Errorf("%s is not a scenario, a ticket, a fixture, a journey, or the app of a screens file", terminalSafe(oneLine(a)))
	}
	return items, notes, nil
}

func fixtureItem(fx domain.Fixture, h domain.Hash) reviewItem {
	return reviewItem{
		kind: "fixture", id: "fixture " + string(fx.ID),
		summary: fmt.Sprintf("fixture %s %s", fx.ID, fx.Version),
		show: func(w io.Writer, n, total int) {
			fmt.Fprintf(w, "\n%d of %d · fixture %s %s (owner %s)\n", n, total, fx.ID, fx.Version, terminalSafe(oneLine(fx.Owner)))
			fmt.Fprintf(w, "  %s\n", oneLine(terminalSafe(fx.Description)))
			sends, removes := fixtureRequests(fx)
			fmt.Fprintln(w, "  Sends:")
			for _, l := range sends {
				fmt.Fprintf(w, "    %s\n", oneLine(terminalSafe(l)))
			}
			if len(removes) > 0 {
				fmt.Fprintln(w, "  Removes afterwards:")
				for _, l := range removes {
					fmt.Fprintf(w, "    %s\n", oneLine(terminalSafe(l)))
				}
			}
		},
		details: func(w io.Writer) {
			for _, l := range fixtureLines(fx, h) {
				fmt.Fprintln(w, oneLine(terminalSafe(l)))
			}
		},
		approve: func(store *confirm.Local, a approver) error {
			return store.PutFixture(confirm.FixtureApproval{Fixture: fx.ID, Version: fx.Version, Hash: h, User: a.name, Email: a.email, Identity: localIdentity, At: a.at})
		},
	}
}

// fixtureRequests lists every request of a fixture in full, apart from what the fixture removes
// afterwards, so that a person sees what a fixture writes before approving it.
func fixtureRequests(fx domain.Fixture) (sends, removes []string) {
	for _, phase := range []struct {
		name  string
		steps []domain.FixtureStep
		into  *[]string
	}{{domain.PhaseFind, fx.Find, &sends}, {domain.PhaseCreate, fx.Create, &sends}, {domain.PhaseCleanup, fx.Cleanup, &removes}} {
		for i, st := range phase.steps {
			*phase.into = append(*phase.into, fixtureStepLine(phase.name, i+1, st))
		}
	}
	return sends, removes
}

func screensItem(scr domain.Screens, h domain.Hash, before *confirm.ScreensApproval) reviewItem {
	return reviewItem{
		kind: "screens", id: "the screens of " + scr.App,
		summary: "screens " + scr.App,
		show: func(w io.Writer, n, total int) {
			fmt.Fprintf(w, "\n%d of %d · the screens of %s\n", n, total, terminalSafe(oneLine(scr.App)))
			for _, sn := range scr.ScreenNames() {
				fmt.Fprintf(w, "  %s: %s\n", oneLine(terminalSafe(sn)), plural(len(scr.ElementNames(sn)), "element"))
			}
			lines := screensLines(scr, h, before)
			for i, l := range lines {
				if strings.HasPrefix(l, "  since the approval") || strings.HasPrefix(l, "  these screens were never approved") {
					for _, tail := range lines[i:] {
						fmt.Fprintln(w, oneLine(terminalSafe(tail)))
					}
					break
				}
			}
		},
		details: func(w io.Writer) {
			for _, l := range screensLines(scr, h, before) {
				fmt.Fprintln(w, oneLine(terminalSafe(l)))
			}
		},
		approve: func(store *confirm.Local, a approver) error {
			return store.PutScreens(confirm.ScreensApproval{App: scr.App, Hash: h, User: a.name, Email: a.email, Identity: localIdentity, At: a.at, Locators: scr.Screens})
		},
	}
}

// scenarioItems groups the scenarios that need a confirmation by the use case that covers them. A
// scenario that no use case covers is an item of its own.
func scenarioItems(dir string, uses []obligation.Located, todo []unconfirmed, fixtureState func(domain.FixtureID) string, view func(domain.Scenario) scenarioView) []reviewItem {
	groups := map[string][]unconfirmed{}
	var order []string
	for _, u := range todo {
		key := "scenario " + string(u.sc.ID)
		for _, l := range uses {
			if slices.Contains(l.Covers, u.sc.ID) {
				key = l.ID
				break
			}
		}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], u)
	}
	// The use cases come in the order of their files, and the scenarios that no use case covers last.
	rank := func(key string) int {
		for i, l := range uses {
			if l.ID == key {
				return i
			}
		}
		return len(uses)
	}
	slices.SortStableFunc(order, func(a, b string) int { return rank(a) - rank(b) })

	var out []reviewItem
	for _, key := range order {
		us := groups[key]
		var use *obligation.Located
		for i := range uses {
			if uses[i].ID == key {
				use = &uses[i]
			}
		}
		ids := make([]string, len(us))
		for i, u := range us {
			ids[i] = string(u.sc.ID)
		}
		out = append(out, reviewItem{
			kind: "scenario", id: strings.Join(ids, ", "),
			summary: "scenario " + strings.Join(ids, ", "),
			show: func(w io.Writer, n, total int) {
				showScenarioItem(w, dir, n, total, use, us, fixtureState, view)
			},
			details: func(w io.Writer) {
				for _, u := range us {
					if u.gherkin != "" {
						fmt.Fprintf(w, "%s as Gherkin, with the source and cite of each expected result under it:\n\n", u.file)
						for _, line := range strings.Split(strings.TrimRight(u.gherkin, "\n"), "\n") {
							fmt.Fprintf(w, "    %s\n", line)
						}
						fmt.Fprintln(w)
					}
					fmt.Fprintf(w, "Scenario %s has %d expected results that a person must confirm:\n", u.sc.ID, len(u.lines))
					for _, line := range u.lines {
						fmt.Fprintf(w, "  - %s\n", line)
					}
					view(u.sc).printExchanges(w, u.sc)
				}
			},
			approve: func(store *confirm.Local, a approver) error {
				for _, u := range us {
					if err := putConfirmation(store, a, u); err != nil {
						return err
					}
					if err := coverEdgeCase(dir, uses, u.sc); err != nil {
						return err
					}
				}
				return nil
			},
			drop: dropOf(us),
		})
	}
	return out
}

// coverEdgeCase makes an approved edge case one more test of every use case that its base covers. Until a
// person approves it, no use case holds it, so it cannot change the state of one.
func coverEdgeCase(dir string, uses []obligation.Located, sc domain.Scenario) error {
	if sc.Edge == nil {
		return nil
	}
	for _, l := range uses {
		if !slices.Contains(l.Covers, sc.Edge.Base) || slices.Contains(l.Covers, sc.ID) {
			continue
		}
		err := obligation.Edit(dir, l.ID, func(ob *obligation.Obligation) error {
			ob.Covers = append(ob.Covers, sc.ID)
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// dropOf is the drop of an item that holds one edge case, and nil for any other item.
func dropOf(us []unconfirmed) func(io.Writer, options, string) error {
	if len(us) != 1 || us[0].sc.Edge == nil {
		return nil
	}
	id := us[0].sc.ID
	return func(w io.Writer, o options, dir string) error {
		return retireScenario(w, o, rmFlags{reason: "dropped in the review", dir: dir}, string(id))
	}
}

func putConfirmation(store *confirm.Local, a approver, u unconfirmed) error {
	return store.Put(confirm.Record{
		Scenario:   u.sc.ID,
		ExpectHash: domain.ExpectHash(u.sc),
		User:       a.name,
		Email:      a.email,
		Identity:   localIdentity,
		At:         a.at,
		Sources:    slices.Compact(slices.Sorted(slices.Values(u.sources))),
	})
}

func fixturesOf(sc domain.Scenario) []domain.FixtureID {
	var ids []domain.FixtureID
	for _, g := range sc.Given {
		if !slices.Contains(ids, g.Fixture) {
			ids = append(ids, g.Fixture)
		}
	}
	return ids
}

func surfaceWord(s domain.Surface) string {
	switch s {
	case domain.SurfaceWeb:
		return "web"
	case domain.SurfaceAndroid:
		return "Android"
	}
	return "API"
}

func accountOf(sc domain.Scenario) string {
	if sc.AuthProfile == "" {
		return "default"
	}
	return terminalSafe(oneLine(sc.AuthProfile))
}

// journeyItem is a journey whose legs have expected results that nobody confirmed. ok is false when
// none has.
func journeyItem(dir string, store *confirm.Local, trust []domain.Source, env config.Environment, j domain.Journey) (reviewItem, bool) {
	var todo []unconfirmed
	for _, l := range j.Legs {
		sc, _ := journey.LegScenario(j, l.ID, legAuth(env, j, l.Service))
		if unconfirmedWhy(store, trust, sc, j.ID) == "" {
			continue
		}
		u := needsConfirmation(dir, sc, trust)
		if len(u.lines) == 0 {
			continue
		}
		u.label = fmt.Sprintf("Leg %s of journey %s (scenario %s)", l.ID, j.ID, sc.ID)
		if as := journey.ActedAs(sc); len(as) > 0 {
			u.label += ", acts as " + terminalSafe(strings.Join(as, ", "))
		}
		todo = append(todo, u)
	}
	if len(todo) == 0 {
		return reviewItem{}, false
	}
	return reviewItem{
		kind: "journey", id: "journey " + string(j.ID),
		summary: fmt.Sprintf("journey %s (%s)", j.ID, plural(len(todo), "leg")),
		show: func(w io.Writer, n, total int) {
			fmt.Fprintf(w, "\n%d of %d · journey %s, %s with expected results to confirm\n", n, total, terminalSafe(oneLine(string(j.ID))), plural(len(todo), "leg"))
			if j.Title != "" {
				fmt.Fprintf(w, "  %q\n", terminalSafe(oneLine(j.Title)))
			}
			for _, u := range todo {
				fmt.Fprintf(w, "\n  %s\n", terminalSafe(oneLine(u.label)))
				for _, line := range u.lines {
					fmt.Fprintf(w, "    - %s\n", strings.ReplaceAll(line, "\n", "\n    "))
				}
			}
		},
		details: func(w io.Writer) {
			for _, u := range todo {
				fmt.Fprintf(w, "%s has %d expected results that a person must confirm:\n", u.label, len(u.lines))
				for _, line := range u.lines {
					fmt.Fprintf(w, "  - %s\n", line)
				}
			}
		},
		approve: func(store *confirm.Local, a approver) error {
			for _, u := range todo {
				if err := putConfirmation(store, a, u); err != nil {
					return err
				}
			}
			return nil
		},
	}, true
}
