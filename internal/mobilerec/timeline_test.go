package mobilerec_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/mobilerec"
	"github.com/jancuk/semar/internal/scenario"
	"github.com/jancuk/semar/internal/screens"
)

// recordSignOut records a scenario on the signed-in device: a start, a check of the greeting, a tap on Sign out,
// and a check that the email field is there. It returns the session at the end, saved or not.
func recordSignOut(t *testing.T, r *rig, id string, tune ...func(*mobilerec.Options)) *mobilerec.Session {
	t.Helper()
	ctx := context.Background()
	r.signed = true
	s := r.session(mobilerec.KindScenario, id, tune...)
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if f, err = s.Assert(ctx, mobilerec.Assert{Frame: f.ID, Target: pick(t, f, byID("welcome")), Kind: mobilerec.AssertEquals}); err != nil {
		t.Fatal(err)
	}
	if f, err = s.Act(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: ptr(pick(t, f, byDesc("Sign out")))}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Assert(ctx, mobilerec.Assert{Frame: f.ID, Target: pick(t, f, byID("email")), Kind: mobilerec.AssertExists}); err != nil {
		t.Fatal(err)
	}
	return s
}

// loadSaved reads the files of a saved scenario or flow, as the command does to reopen one.
func loadSaved(t *testing.T, r *rig, kind mobilerec.Kind, id, rel string) *mobilerec.Loaded {
	t.Helper()
	scr, err := screens.Load(r.repo, "shop-app")
	if err != nil {
		t.Fatal(err)
	}
	l := &mobilerec.Loaded{Screens: scr, Path: rel}
	if kind == mobilerec.KindFlow {
		entry, err := flow.Load(r.repo, id)
		if err != nil {
			t.Fatal(err)
		}
		l.Flow = entry.Flow
		return l
	}
	if l.Scenario, err = scenario.Load(filepath.Join(r.repo, filepath.FromSlash(rel))); err != nil {
		t.Fatal(err)
	}
	store, err := evidence.Open(filepath.Join(r.repo, ".semar"))
	if err != nil {
		t.Fatal(err)
	}
	var ok bool
	if l.Script, ok, err = store.Script(l.Scenario.ID, domain.ExpectHash(l.Scenario)); err != nil || !ok {
		t.Fatalf("script: %v %v", ok, err)
	}
	return l
}

func TestDeleteAMiddleCheckAndAMiddleAction(t *testing.T) {
	r := newRig(t)
	s := recordSignOut(t, r, "TASK-D")
	if got := len(s.Steps()); got != 4 {
		t.Fatalf("steps = %+v", s.Steps())
	}
	// Step 2 is the first check: it did nothing on the device, so nothing is out of sync.
	if err := s.DeleteStep(2); err != nil {
		t.Fatal(err)
	}
	if s.Dirty() || len(s.Steps()) != 3 || s.Cursor() != 3 {
		t.Errorf("after a check: dirty %v, steps %d, cursor %d", s.Dirty(), len(s.Steps()), s.Cursor())
	}
	if _, err := s.Build(); err != nil {
		t.Fatal(err)
	}
	// Step 2 is now the tap, which the device did: the session is out of sync, and it says so.
	if err := s.DeleteStep(2); err != nil {
		t.Fatal(err)
	}
	if !s.Dirty() || s.Cursor() != 2 {
		t.Errorf("after an action: dirty %v, cursor %d", s.Dirty(), s.Cursor())
	}
	d, err := s.Build()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(d.Notes, " "), "was deleted") {
		t.Errorf("notes = %v", d.Notes)
	}
	if err := s.DeleteStep(9); err == nil || !strings.Contains(err.Error(), "there is no step 9") {
		t.Errorf("delete 9 = %v", err)
	}
	if err := s.DeleteLastStep(); err != nil || len(s.Steps()) != 1 {
		t.Errorf("delete last = %v, steps %+v", err, s.Steps())
	}
}

func TestGotoReplaysTheFirstStepsAndInsertsAtTheCursor(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	s := recordSignOut(t, r, "TASK-G")
	taps := len(r.d.Touches())
	if taps != 1 {
		t.Fatalf("touches = %d", taps)
	}
	r.signed = true // the app starts signed in, as it did
	f, err := s.Goto(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	// The start and the tap ran again, and the checks did not run on the device.
	if got := len(r.d.Touches()) - taps; got != 1 {
		t.Errorf("goto touched %d times, want 1", got)
	}
	if s.Cursor() != 3 || f.Screen != "SignIn" {
		t.Errorf("cursor %d, screen %s", s.Cursor(), f.Screen)
	}
	if !s.Steps()[3].Pending || s.Steps()[2].Pending {
		t.Errorf("steps = %+v", s.Steps())
	}
	// A new step goes at the cursor, before the old last check.
	if _, err := s.Act(ctx, mobilerec.Act{Do: "type", Frame: f.ID, Target: ptr(pick(t, f, byID("email"))), Value: "ana@example.com"}); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, st := range s.Steps() {
		kinds = append(kinds, st.Kind)
	}
	if strings.Join(kinds, ",") != "launch,assert,tap,type,assert" || s.Cursor() != 4 {
		t.Errorf("kinds = %v, cursor %d", kinds, s.Cursor())
	}
	if _, err := s.Build(); err != nil {
		t.Fatal(err)
	}
	// Goto 0 and past the end are refused.
	if _, err := s.Goto(ctx, 0); err == nil {
		t.Error("goto 0 worked")
	}
	if _, err := s.Goto(ctx, 9); err == nil || !strings.Contains(err.Error(), "the recording has 5") {
		t.Errorf("goto 9 = %v", err)
	}
}

func TestGotoStopsAtTheFirstStepThatFails(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	s := recordSignOut(t, r, "TASK-F")
	r.signed = false // the app starts signed out now: there is no Sign out button
	f, err := s.Goto(ctx, 4)
	if err == nil || !strings.Contains(err.Error(), "step 3") {
		t.Fatalf("goto = %v", err)
	}
	if f == nil || f.Screen != "SignIn" {
		t.Errorf("frame = %+v", f)
	}
	if s.Cursor() != 2 {
		t.Errorf("the cursor is %d, want 2: the last step that went through", s.Cursor())
	}
}

func TestAPositionIsUsedOnlyWhenThePersonPicksIt(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	three := screen(heading("Items"), button("Delete", 100, 300), button("Delete", 100, 500), button("Delete", 100, 700))
	r.d.Show(three)
	r.d.OnLaunch, r.d.OnReset = func() { r.d.Show(three) }, func(string) { r.d.Show(three) }
	s := r.session(mobilerec.KindScenario, "TASK-P")
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second := mobilerec.Target{Index: pick(t, f, byDesc("Delete")).Index + 1}
	var warned bool
	for _, e := range f.Elements {
		warned = warned || strings.Contains(e.Warning, "only a position")
	}
	if !warned {
		t.Errorf("no element warns that only a position finds it: %+v", f.Elements)
	}
	if _, err := s.Act(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: &second}); !errors.Is(err, mobilerec.ErrNoLocator) {
		t.Fatalf("tap without a choice = %v, want ErrNoLocator", err)
	} else if !strings.Contains(err.Error(), "loc N") {
		t.Errorf("the error does not say how to pick: %v", err)
	}
	list, err := s.Candidates(ctx, f.ID, second, "tap")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Usable) != 1 || !list.Usable[0].Explicit || !strings.Contains(list.Usable[0].Locator, "index: 2") {
		t.Fatalf("usable = %+v", list.Usable)
	}
	if len(list.Refused) == 0 || list.Refused[0].Matches != 3 || !strings.Contains(list.Refused[0].Note, "3 matches (refused)") {
		t.Errorf("refused = %+v", list.Refused)
	}
	if _, err := s.Act(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: &second, Candidate: 1}); err != nil {
		t.Fatal(err)
	}
	if got := r.d.Touches(); len(got) != 1 || got[0].From.Y < 500 || got[0].From.Y > 600 {
		t.Errorf("touches = %+v, want the second button", got)
	}
	if _, err := s.Act(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: &second, Candidate: 7}); err == nil {
		t.Error("a locator that does not exist was accepted")
	}
}

func TestChangeALocatorWithoutTouchingTheScreensEntry(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	tree, dels := cardsScreen([]string{"Milk", "Eggs", "Bread"}, "")
	_ = dels
	r.d.Show(tree)
	r.d.OnLaunch, r.d.OnReset = func() { r.d.Show(tree) }, func(string) { r.d.Show(tree) }
	r.write(".semar/screens/shop-app.yaml", "apiVersion: semar/v1\nkind: Screens\napp: shop-app\nscreens:\n  Screen1:\n    delete:\n      desc: Delete\n      within: card\n    card: { class: android.view.ViewGroup, hasText: Milk }\n")
	before := r.read(".semar/screens/shop-app.yaml")
	s := r.session(mobilerec.KindScenario, "TASK-L")
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The best locator of the second Delete is scoped by the text of its card.
	var deletes []int
	for _, e := range f.Elements {
		if e.Desc == "Delete" {
			deletes = append(deletes, e.Index)
		}
	}
	eggs := mobilerec.Target{Index: deletes[1]}
	if f, err = s.Act(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: &eggs}); err != nil {
		t.Fatal(err)
	}
	locs, err := s.Locators(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) == 0 || !locs[0].Current || !strings.Contains(locs[0].Locator, "within") {
		t.Fatalf("locators = %+v", locs)
	}
	if _, err := s.Assert(ctx, mobilerec.Assert{Frame: f.ID, Target: mobilerec.Target{Index: 0}, Kind: mobilerec.AssertExists}); err != nil {
		t.Fatal(err)
	}
	d, err := s.Build()
	if err != nil {
		t.Fatal(err)
	}
	// The element does not take the name of the entry of the file, which has another locator.
	got := d.Script.Steps[1].Mobile.Element
	if got == "delete" {
		t.Errorf("the step reuses delete, which finds Milk's button")
	}
	if _, err := s.Save(mobilerec.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	after := r.read(".semar/screens/shop-app.yaml")
	if !strings.HasPrefix(after, strings.TrimSuffix(before, "\n")) {
		t.Errorf("the screens file lost what it had:\n%s", after)
	}
	if err := s.SetLocator(2, 1); err != nil {
		t.Errorf("choosing the locator in use = %v", err)
	}
	if err := s.SetLocator(1, 2); err == nil {
		t.Error("a launch has no locator")
	}
}

func TestRepickPointsAStepAtAnotherElement(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.signed = true
	s := r.session(mobilerec.KindFlow, "repick")
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Tap Sign out, then try to repick it while the device is after it: refused.
	if f, err = s.Act(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: ptr(pick(t, f, byDesc("Sign out")))}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Repick(ctx, 2, f.ID, pick(t, f, byDesc("Sign in")), 0); err == nil || !strings.Contains(err.Error(), "goto 1") {
		t.Fatalf("repick after the step = %v", err)
	}
	r.signed = true
	if f, err = s.Goto(ctx, 1); err != nil {
		t.Fatal(err)
	}
	// A step that taps takes any element; a type step takes only a field.
	if _, err := s.Repick(ctx, 2, f.ID, pick(t, f, byID("welcome")), 0); err == nil {
		t.Log("a tap on a label has a locator")
	}
	if _, err := s.Repick(ctx, 2, f.ID, pick(t, f, byDesc("Sign out")), 0); err != nil {
		t.Fatal(err)
	}
	steps := s.Steps()
	if steps[1].Locator == "" {
		t.Errorf("steps = %+v", steps)
	}
	if err := s.DeleteStep(1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Repick(ctx, 1, f.ID, pick(t, f, byDesc("Sign out")), 0); err == nil {
		t.Error("repick worked on a dirty session")
	}
}

func TestReopenASavedScenarioAndSaveItAgainUnchanged(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	first := recordSignOut(t, r, "TASK-O")
	sv, err := first.Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	screensBefore := r.read(".semar/screens/shop-app.yaml")
	featureBefore := r.read(sv.ScenarioPath)
	loaded := loadSaved(t, r, mobilerec.KindScenario, "TASK-O", sv.ScenarioPath)

	r.signed = true
	s := r.session(mobilerec.KindScenario, "TASK-O", func(o *mobilerec.Options) { o.Resume = loaded })
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Six steps: the start, a wait for the greeting that holds its check, the tap, and a wait that holds the last.
	if s.Cursor() != 6 || len(s.Steps()) != 6 || f.Screen != "SignIn" {
		t.Errorf("cursor %d, steps %d, screen %s", s.Cursor(), len(s.Steps()), f.Screen)
	}
	d, err := s.Build()
	if err != nil {
		t.Fatal(err)
	}
	if d.Script.ExpectHash != loaded.Script.ExpectHash || d.Script.ExpectHash != sv.Draft.Script.ExpectHash {
		t.Errorf("the expect hash changed: %s, was %s", d.Script.ExpectHash, loaded.Script.ExpectHash)
	}
	if len(d.Added) != 0 {
		t.Errorf("an unchanged recording adds elements: %+v", d.Added)
	}
	sv2, err := s.Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sv2.ScenarioPath != sv.ScenarioPath {
		t.Errorf("path = %s, want %s", sv2.ScenarioPath, sv.ScenarioPath)
	}
	if got := r.read(".semar/screens/shop-app.yaml"); got != screensBefore {
		t.Errorf("the screens file changed:\n%s", got)
	}
	if got := r.read(sv2.ScenarioPath); got != featureBefore {
		t.Errorf("the feature changed:\n%s\nwas\n%s", got, featureBefore)
	}
	again := loadSaved(t, r, mobilerec.KindScenario, "TASK-O", sv.ScenarioPath)
	if !slices.EqualFunc(again.Script.Steps, loaded.Script.Steps, func(a, b domain.ScriptStep) bool { return a.Step == b.Step && *a.Mobile == *b.Mobile }) {
		t.Errorf("the script changed:\n%+v\nwas\n%+v", again.Script.Steps, loaded.Script.Steps)
	}
	if slices.ContainsFunc(sv2.Files, func(f string) bool { return f == sv2.ScreensPath }) {
		t.Error("the save wrote the screens file")
	}
}

func TestReopenAtAStepReplaysOnlyThatMany(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	sv, err := recordSignOut(t, r, "TASK-A").Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	loaded := loadSaved(t, r, mobilerec.KindScenario, "TASK-A", sv.ScenarioPath)
	taps := len(r.d.Touches())
	r.signed = true
	s := r.session(mobilerec.KindScenario, "TASK-A", func(o *mobilerec.Options) { o.Resume, o.ResumeAt = loaded, 2 })
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.Cursor() != 2 || len(r.d.Touches()) != taps || f.Screen != "Home" {
		t.Errorf("cursor %d, touches %d, screen %s", s.Cursor(), len(r.d.Touches())-taps, f.Screen)
	}
	// Record the tap again at the cursor: it goes before the old tap, and the old check stays after.
	if _, err := s.Act(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: ptr(pick(t, f, byDesc("Sign out")))}); err != nil {
		t.Fatal(err)
	}
	if got := len(s.Steps()); got != 7 || s.Cursor() != 3 {
		t.Errorf("steps %d, cursor %d", got, s.Cursor())
	}
	_, err = mobilerec.New(mobilerec.Options{
		Repo: r.repo, Kind: mobilerec.KindScenario, ID: "TASK-A", Ticket: "SHOP-1", Service: "shop-app", Open: r.factory(), Resume: loaded, ResumeAt: 9,
	})
	if err == nil || !strings.Contains(err.Error(), "there is no step 9") {
		t.Errorf("a step past the end: %v", err)
	}
}

func TestAReopenedCheckKeepsItsSourceAndBy(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	sv, err := recordSignOut(t, r, "TASK-M").Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	loaded := loadSaved(t, r, mobilerec.KindScenario, "TASK-M", sv.ScenarioPath)
	for i := range loaded.Scenario.Steps {
		for j := range loaded.Scenario.Steps[i].Expect {
			ex := &loaded.Scenario.Steps[i].Expect[j]
			ex.By, ex.Source, ex.Cite = "model", domain.SourceTicket, "SHOP-1"
		}
	}
	loaded.Script.ExpectHash = domain.ExpectHash(loaded.Scenario)
	r.signed = true
	s := r.session(mobilerec.KindScenario, "TASK-M", func(o *mobilerec.Options) { o.Resume = loaded })
	if _, err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	d, err := s.Build()
	if err != nil {
		t.Fatal(err)
	}
	var seen int
	for _, st := range d.Scenario.Steps {
		for _, ex := range st.Expect {
			seen++
			if ex.By != "model" || ex.Source != domain.SourceTicket || ex.Cite != "SHOP-1" {
				t.Errorf("expectation = %+v", ex)
			}
		}
	}
	if seen != 2 || d.Script.ExpectHash != domain.ExpectHash(loaded.Scenario) {
		t.Errorf("seen %d expectations, hash %s want %s", seen, d.Script.ExpectHash, domain.ExpectHash(loaded.Scenario))
	}
}

func TestReopeningRefusesAnAPIStep(t *testing.T) {
	r := newRig(t)
	sv, err := recordSignOut(t, r, "TASK-X").Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	loaded := loadSaved(t, r, mobilerec.KindScenario, "TASK-X", sv.ScenarioPath)
	loaded.Script.Steps[1] = domain.ScriptStep{Step: loaded.Script.Steps[1].Step, Request: &domain.APIRequest{Service: "shop", Method: "GET", Path: "/x"}}
	_, err = mobilerec.New(mobilerec.Options{
		Repo: r.repo, Kind: mobilerec.KindScenario, ID: "TASK-X", Ticket: "SHOP-1", Service: "shop-app", Open: r.factory(), Resume: loaded,
	})
	if err == nil || !strings.Contains(err.Error(), "only steps on a device can be reopened") {
		t.Errorf("err = %v", err)
	}
}

func TestReopenAFlowThatTypesASecret(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	s := r.session(mobilerec.KindFlow, "login")
	recordLogin(t, r, s)
	sv, err := s.Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	flowBefore, screensBefore := r.read(sv.FlowPath), r.read(".semar/screens/shop-app.yaml")
	loaded := loadSaved(t, r, mobilerec.KindFlow, "login", sv.FlowPath)

	r.signed = false
	re := r.session(mobilerec.KindFlow, "login", func(o *mobilerec.Options) { o.Resume = loaded })
	f, err := re.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// No file holds the password, so the session cannot replay until it is given one.
	if re.Cursor() != 0 || !strings.Contains(f.Notice, "SHOP_PASSWORD") {
		t.Errorf("cursor %d, notice %q", re.Cursor(), f.Notice)
	}
	if got := re.MissingSecrets(0); len(got) != 1 || got[0] != "SHOP_PASSWORD" {
		t.Errorf("missing = %v", got)
	}
	if _, err := re.Act(ctx, mobilerec.Act{Do: "press", Value: "back"}); !errors.Is(err, mobilerec.ErrNotSynced) {
		t.Errorf("act before goto = %v", err)
	}
	if _, err := re.Goto(ctx, 4); !errors.Is(err, mobilerec.ErrNeedsValues) {
		t.Fatalf("goto without the secret = %v", err)
	}
	if err := re.SetSecret("SHOP_PASSWORD", canary); err != nil {
		t.Fatal(err)
	}
	if f, err = re.Goto(ctx, len(re.Steps())); err != nil {
		t.Fatal(err)
	}
	if f.Screen != "Home" {
		t.Errorf("screen = %s", f.Screen)
	}
	if _, err := re.Save(mobilerec.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := r.read(sv.FlowPath); got != flowBefore {
		t.Errorf("the flow changed:\n%s\nwas\n%s", got, flowBefore)
	}
	if got := r.read(".semar/screens/shop-app.yaml"); got != screensBefore {
		t.Errorf("the screens file changed:\n%s", got)
	}
	if strings.Contains(r.allFiles(), canary) {
		t.Error("the secret is in a file")
	}
}

func TestAScreenNameHoldsForTheNextScreenWithTheSameHeading(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	s := r.session(mobilerec.KindFlow, "names")
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if f.Screen != "SignIn" {
		t.Fatalf("screen = %s", f.Screen)
	}
	if f, err = s.SetScreen("Login"); err != nil {
		t.Fatal(err)
	}
	if f, err = s.Act(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: ptr(pick(t, f, byDesc("Sign in")))}); err != nil {
		t.Fatal(err)
	}
	if f.Screen != "Home" {
		t.Fatalf("screen = %s", f.Screen)
	}
	if f, err = s.Act(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: ptr(pick(t, f, byDesc("Sign out")))}); err != nil {
		t.Fatal(err)
	}
	if f.Screen != "Login" {
		t.Errorf("the screen is %s, want the name that the person gave", f.Screen)
	}
	if names := s.ScreenNames(); !slices.Contains(names, "Login") || !slices.Contains(names, "Home") {
		t.Errorf("names = %v", names)
	}
	// Renaming moves the recorded steps, and a later screen takes the new name.
	if f, err = s.RenameScreen("Login", "Auth"); err != nil {
		t.Fatal(err)
	}
	if f.Screen != "Auth" {
		t.Errorf("screen = %s", f.Screen)
	}
	d, err := s.Build()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range d.Added {
		if a.Screen == "Login" {
			t.Errorf("an element is still added to Login: %+v", a)
		}
	}
	if _, err := s.RenameScreen("Nowhere", "Else"); err == nil {
		t.Error("a screen that nothing is on was renamed")
	}
	if names := s.ScreenNames(); slices.Contains(names, "Else") || slices.Contains(names, "Nowhere") {
		t.Errorf("a refused rename left a name behind: %v", names)
	}
}

func TestTheScreenOfTheFileIsGuessedFromItsElements(t *testing.T) {
	r := newRig(t)
	r.write(".semar/screens/shop-app.yaml", `apiVersion: semar/v1
kind: Screens
app: shop-app
screens:
  Auth:
    email: { id: email }
    password: { id: password }
    go: { desc: Sign in }
  Home:
    greeting: { id: welcome }
`)
	s := r.session(mobilerec.KindFlow, "guess")
	f, err := s.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f.Screen != "Auth" {
		t.Errorf("screen = %s, want Auth from its elements, and not SignIn from its heading", f.Screen)
	}
}

func TestACommonElementIsSharedAndReused(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.signed = true
	s := r.session(mobilerec.KindFlow, "chrome")
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Act(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: ptr(pick(t, f, byDesc("Sign out")))}); err != nil {
		t.Fatal(err)
	}
	on := true
	if err := s.EditStep(2, mobilerec.Edit{Common: &on}); err != nil {
		t.Fatal(err)
	}
	if err := s.EditStep(1, mobilerec.Edit{Common: &on}); err == nil {
		t.Error("a launch was shared")
	}
	if !s.Steps()[1].Shared {
		t.Errorf("steps = %+v", s.Steps())
	}
	sv, err := s.Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	text := r.read(sv.ScreensPath)
	if !strings.Contains(text, "common:") || !strings.Contains(text, "sign-out") {
		t.Fatalf("screens:\n%s", text)
	}
	// Another recording of the same button, on any screen, uses the common name and adds nothing.
	r.signed = true
	again := r.session(mobilerec.KindFlow, "chrome-2")
	f, err = again.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = again.Act(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: ptr(pick(t, f, byDesc("Sign out")))}); err != nil {
		t.Fatal(err)
	}
	d, err := again.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Added) != 0 || d.Flow.Steps[1].Mobile.Element != "sign-out" {
		t.Errorf("added %+v, element %q", d.Added, d.Flow.Steps[1].Mobile.Element)
	}
	// The shared step goes back to its screen when it is not shared.
	off := false
	if err := s.EditStep(2, mobilerec.Edit{Common: &off}); err != nil {
		t.Fatal(err)
	}
	if s.CommonCount() != 1 {
		t.Errorf("common = %d", s.CommonCount())
	}
}

func TestChooseAnotherLocatorOfARecordedStep(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.write(".semar/screens/shop-app.yaml", "apiVersion: semar/v1\nkind: Screens\napp: shop-app\nscreens:\n  SignIn:\n    sign-in: { desc: Sign in }\n")
	before := r.read(".semar/screens/shop-app.yaml")
	s := r.session(mobilerec.KindFlow, "other")
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Act(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: ptr(pick(t, f, byDesc("Sign in")))}); err != nil {
		t.Fatal(err)
	}
	locs, err := s.Locators(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) < 2 || !locs[0].Current || locs[0].Locator != "{ desc: \"Sign in\" }" {
		t.Fatalf("locators = %+v", locs)
	}
	if err := s.SetLocator(2, 2); err != nil {
		t.Fatal(err)
	}
	now, _ := s.Locators(2)
	if now[0].Locator != locs[1].Locator || now[1].Locator != locs[0].Locator {
		t.Errorf("after the change: %+v", now)
	}
	d, err := s.Build()
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Flow.Steps[1].Mobile.Element; got == "sign-in" {
		t.Errorf("the step keeps the entry of the old locator: %s", got)
	}
	if _, err := s.Save(mobilerec.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	if after := r.read(".semar/screens/shop-app.yaml"); !strings.Contains(after, "sign-in: { desc: Sign in }") || !strings.HasPrefix(after, before) {
		t.Errorf("the entry of the file changed:\n%s", after)
	}
	if err := s.SetLocator(2, 9); err == nil {
		t.Error("locator 9 exists")
	}
}

func TestReopenKeepsTheTicketOfTheFile(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	first := recordSignOut(t, r, "TASK-T", func(o *mobilerec.Options) { o.Ticket = "SHOP-9" })
	sv, err := first.Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sv.ScenarioPath, "SHOP-9") {
		t.Fatalf("path = %s", sv.ScenarioPath)
	}
	before := r.read(sv.ScenarioPath)
	loaded := loadSaved(t, r, mobilerec.KindScenario, "TASK-T", sv.ScenarioPath)

	r.signed = true
	// A surface that works the ticket out from the ID gives another one: the file keeps its own.
	s := r.session(mobilerec.KindScenario, "TASK-T", func(o *mobilerec.Options) { o.Resume, o.Ticket = loaded, "TASK" })
	if _, err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	sv2, err := s.Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sv2.ScenarioPath != sv.ScenarioPath {
		t.Errorf("path = %s, want %s", sv2.ScenarioPath, sv.ScenarioPath)
	}
	if got := r.read(sv2.ScenarioPath); got != before {
		t.Errorf("the feature changed:\n%s\nwas\n%s", got, before)
	}
}

func TestReopenAsksBeforeAFlowInsideAFlowClearsAPhysicalDevice(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	// inner launches with no reset of its own: it resets as the service does. outer only uses inner.
	inner := r.session(mobilerec.KindFlow, "inner")
	f, err := inner.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inner.Assert(ctx, mobilerec.Assert{Frame: f.ID, Target: pick(t, f, byID("email")), Kind: mobilerec.AssertExists}); err != nil {
		t.Fatal(err)
	}
	if _, err := inner.Save(mobilerec.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.read(".semar/flows/inner.yaml"), "reset:") {
		t.Fatalf("the launch of inner names a reset:\n%s", r.read(".semar/flows/inner.yaml"))
	}
	outer := r.session(mobilerec.KindFlow, "outer", func(o *mobilerec.Options) { o.Use = "inner" })
	if f, err = outer.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := outer.Assert(ctx, mobilerec.Assert{Frame: f.ID, Target: pick(t, f, byID("email")), Kind: mobilerec.AssertExists}); err != nil {
		t.Fatal(err)
	}
	sv, err := outer.Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	loaded := loadSaved(t, r, mobilerec.KindFlow, "outer", sv.FlowPath)

	r.d.Info.Device = "R58M123ABC"
	re := r.session(mobilerec.KindFlow, "outer", func(o *mobilerec.Options) { o.Resume, o.Reset = loaded, "clear" })
	if _, err := re.Start(ctx); !errors.Is(err, mobilerec.ErrNeedsConfirm) {
		t.Fatalf("reopen of a flow that clears through another = %v, want ErrNeedsConfirm", err)
	}
	ok := r.session(mobilerec.KindFlow, "outer", func(o *mobilerec.Options) { o.Resume, o.Reset, o.ConfirmClear = loaded, "clear", true })
	if _, err := ok.Start(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestGoingToAStepAgainAndAgainNeverUsesTheBudgetUp(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	s := recordSignOut(t, r, "TASK-G")
	// The engine of a run allows 200 actions by default: more replays than that must still work.
	for i := 0; i < 250; i++ {
		if _, err := s.Goto(ctx, 1); err != nil {
			t.Fatalf("goto %d: %v", i+1, err)
		}
	}
}

func TestDeletingACaptureLeavesTheSessionClean(t *testing.T) {
	r := ordersRig(t)
	ctx := context.Background()
	s, f := startOrders(t, r, mobilerec.KindFlow, "cap-del")
	if _, _, err := s.CaptureValue(ctx, mobilerec.CaptureReq{Frame: f.ID, Target: pick(t, f, byID("ref")), Name: "ref"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteLastStep(); err != nil {
		t.Fatal(err)
	}
	if s.Dirty() {
		t.Error("deleting a capture, which does nothing on the device, made the session dirty")
	}
}

func TestReopenKeepsTheServiceOfAStepOfAFlow(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	s := r.session(mobilerec.KindFlow, "svc")
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Assert(ctx, mobilerec.Assert{Frame: f.ID, Target: pick(t, f, byID("email")), Kind: mobilerec.AssertExists}); err != nil {
		t.Fatal(err)
	}
	sv, err := s.Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	text := r.read(sv.FlowPath)
	if !strings.Contains(text, "do: launch") {
		t.Fatalf("flow:\n%s", text)
	}
	// A person gave the launch its own service and app.
	r.write(sv.FlowPath, strings.Replace(text, "do: launch", "do: launch\n      service: shop-app\n      app: shop-app", 1))
	loaded := loadSaved(t, r, mobilerec.KindFlow, "svc", sv.FlowPath)
	re := r.session(mobilerec.KindFlow, "svc", func(o *mobilerec.Options) { o.Resume = loaded })
	if _, err := re.Start(ctx); err != nil {
		t.Fatal(err)
	}
	sv2, err := re.Save(mobilerec.SaveOptions{Replace: true})
	if err != nil {
		t.Fatal(err)
	}
	// The steps of the file name the service and the app of the step, whatever the order of the keys.
	got := r.read(sv2.FlowPath)
	if i := strings.Index(got, "wait-email"); i < 0 || strings.Count(got[:i], "service: shop-app") != 2 || strings.Count(got[:i], "app: shop-app") != 2 {
		t.Errorf("the launch lost its service or its app:\n%s", got)
	}
}
