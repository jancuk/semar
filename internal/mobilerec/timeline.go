package mobilerec

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/script"
)

// ErrNeedsValues is the answer to a replay that needs a secret or a param that the session does not hold.
// The error is a *NeedsValues, which names them. The values are asked of the person, kept in memory, and
// never written.
var ErrNeedsValues = errors.New("a secret or a param is needed first")

// NeedsValues says which secrets and which params a replay of the steps lacks.
type NeedsValues struct {
	Secrets []string
	Params  []string
}

func (e *NeedsValues) Error() string {
	var parts []string
	if len(e.Secrets) > 0 {
		parts = append(parts, "the secret "+strings.Join(e.Secrets, ", "))
	}
	if len(e.Params) > 0 {
		parts = append(parts, "the param "+strings.Join(e.Params, ", "))
	}
	return ErrNeedsValues.Error() + ": " + strings.Join(parts, " and ")
}
func (e *NeedsValues) Is(t error) bool { return t == ErrNeedsValues }

// insertLocked puts a step at the cursor, and moves the cursor after it.
func (s *Session) insertLocked(ev *event) {
	if s.at > len(s.events) {
		s.at = len(s.events)
	}
	s.events = slices.Insert(s.events, s.at, ev)
	s.at++
}

// syncedLocked refuses a new step when the device is not at any step of a reopened recording.
func (s *Session) syncedLocked() error {
	if s.at == 0 && len(s.events) > 0 {
		return ErrNotSynced
	}
	return nil
}

// Cursor is the number of steps that the device has been taken through. A new step goes after them.
func (s *Session) Cursor() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.at
}

// Dirty says that a step that the device had done was deleted: the device is not where the steps say. A
// goto takes it back.
func (s *Session) Dirty() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dirty
}

// Steps lists the recorded steps, for the timeline. The text has no secret in it.
func (s *Session) Steps() []Step {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Step, len(s.events))
	for i, e := range s.events {
		out[i] = Step{N: i + 1, ID: e.id, Text: s.ov.Scrub(e.text), Screen: e.screen, Kind: e.kind(), Pending: i >= s.at, Shared: e.shared}
		if e.element() {
			out[i].Locator, out[i].Stability = s.ov.Scrub(locatorText(e.locator, e.container)), e.cand.Stability
		}
	}
	return out
}

// locatorText is how a locator reads to a person: a container is called by its hint.
func locatorText(loc domain.Locator, c *Container) string {
	if c != nil {
		loc.Within = c.Hint
	}
	return loc.String()
}

// DeleteLastStep takes the last step off the recording.
func (s *Session) DeleteLastStep() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteStepLocked(len(s.events))
}

// DeleteStep takes the nth step off the recording. A check goes with no effect on the device. A step that
// the device had done, before the cursor, leaves the device where the later steps do not expect it: the
// session is then dirty, and a goto takes the device through the steps that are left.
func (s *Session) DeleteStep(n int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteStepLocked(n)
}

func (s *Session) deleteStepLocked(n int) error {
	if len(s.events) == 0 {
		return errors.New("there is no step to delete")
	}
	if n < 1 || n > len(s.events) {
		return fmt.Errorf("there is no step %d: the recording has %d", n, len(s.events))
	}
	ev := s.events[n-1]
	s.events = slices.Delete(s.events, n-1, n)
	if ev.capture != nil {
		delete(s.vars, ev.capture.name)
	}
	if n-1 < s.at {
		s.at--
		if !ev.isRead() {
			s.dirty = true
			s.notice = "the device still shows what the deleted step did: use goto to take it through the steps that are left"
		}
	}
	return nil
}

// refsOf lists the {{names}} that a step uses.
var refPattern = regexp.MustCompile(`\{\{\s*([A-Za-z][A-Za-z0-9_.]*)\s*\}\}`)

func refsOf(ev *event) []string {
	texts := []string{ev.value, ev.link}
	for _, v := range ev.with {
		texts = append(texts, v)
	}
	for _, l := range ev.locs {
		for _, v := range l.Fields() {
			texts = append(texts, v)
		}
	}
	var out []string
	for _, t := range texts {
		for _, m := range refPattern.FindAllStringSubmatch(t, -1) {
			if !slices.Contains(out, m[1]) {
				out = append(out, m[1])
			}
		}
	}
	return out
}

// limit is how many steps a count asks for: 0 or less is all of them.
func (s *Session) limit(upTo int) int {
	if upTo <= 0 || upTo > len(s.events) {
		return len(s.events)
	}
	return upTo
}

// MissingSecrets lists the secrets that the first upTo steps type, and that the session does not hold: a
// reopened recording does not know them, because no file holds a secret. upTo of 0 is all the steps. Give
// each with SetSecret.
func (s *Session) MissingSecrets(upTo int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.missingSecretsLocked(upTo)
}

func (s *Session) missingSecretsLocked(upTo int) []string {
	var names []string
	var add func(secret string)
	add = func(secret string) {
		name, ok := strings.CutPrefix(secret, "env:")
		if !ok || slices.Contains(names, name) {
			return
		}
		if _, held := s.ov.Lookup(name); !held {
			names = append(names, name)
		}
	}
	var fromFlow func(id string, depth int)
	fromFlow = func(id string, depth int) {
		entry, err := flow.Load(s.o.Repo, id)
		if err != nil || depth > flow.MaxDepth {
			return
		}
		for _, st := range entry.Flow.Steps {
			if st.Mobile != nil {
				add(st.Mobile.Secret)
			}
			if st.Use != "" {
				fromFlow(st.Use, depth+1)
			}
		}
	}
	for _, ev := range s.events[:s.limit(upTo)] {
		add(ev.secret)
		if ev.use != "" {
			fromFlow(ev.use, 1)
		}
	}
	slices.Sort(names)
	return names
}

// SetSecret gives the value of a secret that a reopened recording types. It is kept in memory for this
// session, as a secret that was typed in it is, and it is written nowhere.
func (s *Session) SetSecret(name, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := CheckSecretEnv(name); err != nil {
		return err
	}
	if value == "" {
		return errors.New("a secret needs a value")
	}
	s.ov.SetSecret(name, value)
	return nil
}

// MissingParams lists the params of the reopened flow that the first upTo steps use, and that have no
// default and no value yet. upTo of 0 is all the steps. Give each with SetParam.
func (s *Session) MissingParams(upTo int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.missingParamsLocked(upTo)
}

func (s *Session) missingParamsLocked(upTo int) []string {
	if s.o.Resume == nil || s.o.Kind != KindFlow {
		return nil
	}
	var names []string
	for _, ev := range s.events[:s.limit(upTo)] {
		for _, r := range refsOf(ev) {
			p, ok := s.o.Resume.Flow.Param(r)
			if _, given := s.params[r]; ok && p.Required() && !given && !slices.Contains(names, r) {
				names = append(names, r)
			}
		}
	}
	slices.Sort(names)
	return names
}

// SetParam gives the value of a param of the reopened flow, for the steps that use it. The file keeps
// {{name}}.
func (s *Session) SetParam(name, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.o.Resume == nil || s.o.Kind != KindFlow {
		return errors.New("only a reopened flow has params")
	}
	if _, ok := s.o.Resume.Flow.Param(name); !ok {
		return fmt.Errorf("the flow has no param %s", name)
	}
	if s.ov.Leaks(value) {
		return ErrSecretOnScreen
	}
	s.params[name] = value
	return nil
}

// scope is the variables that the steps see while they replay.
func (s *Session) scope() map[string]string {
	v := map[string]string{"run.id": "recording"}
	maps.Copy(v, s.params)
	maps.Copy(v, s.vars)
	return v
}

// needsClear says whether the first n steps reset with clear, which a physical device must be asked about.
// A launch that names no reset resets as the service does, and a flow that is used is looked into, and the
// flows that it uses, as deep as flows nest.
func (s *Session) needsClearLocked(n int) bool {
	for _, ev := range s.events[:n] {
		switch {
		case ev.do == domain.MobileLaunch:
			r := ev.reset
			if r == "" {
				r = s.o.Reset
			}
			if r == "clear" {
				return true
			}
		case ev.use != "":
			if s.flowClears(ev.use, 1) {
				return true
			}
		}
	}
	return false
}

// flowClears says whether the flow id, or a flow that it uses, launches the app with a reset of clear.
func (s *Session) flowClears(id string, depth int) bool {
	if depth > flow.MaxDepth {
		return false
	}
	entry, err := flow.Load(s.o.Repo, id)
	if err != nil {
		return false
	}
	for _, st := range entry.Flow.Steps {
		switch {
		case st.Mobile != nil && st.Mobile.Do == domain.MobileLaunch:
			r := st.Mobile.Reset
			if r == "" {
				r = s.o.Reset
			}
			if r == "clear" {
				return true
			}
		case st.Use != "":
			if s.flowClears(st.Use, depth+1) {
				return true
			}
		}
	}
	return false
}

// Goto takes the device through the first n steps again, from the reset of the first one, so that the
// device is where the recording was after step n. The checks are skipped: nothing on the device is
// changed by them. A new step is then recorded after step n. It stops at the first step that does not go
// through, and leaves the cursor at the last one that did, with the frame and the error. A physical device
// that a clear would wipe gives ErrNeedsConfirm, and a secret or a param that the session lacks gives
// ErrNeedsValues.
func (s *Session) Goto(ctx context.Context, n int) (*Frame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.liveCheck(); err != nil {
		return nil, err
	}
	return s.gotoLocked(ctx, n)
}

func (s *Session) gotoLocked(ctx context.Context, n int) (*Frame, error) {
	if n < 1 || n > len(s.events) {
		return nil, fmt.Errorf("there is no step %d: the recording has %d", n, len(s.events))
	}
	if first := s.events[0]; first.do != domain.MobileLaunch && first.use == "" {
		return nil, errors.New("the recording does not start the app: its first step must be the launch, or a flow")
	}
	if s.device.Device != "" && !s.emulatorLocked() && !s.o.ConfirmClear && s.needsClearLocked(n) {
		return nil, ErrNeedsConfirm
	}
	if secrets, params := s.missingSecretsLocked(n), s.missingParamsLocked(n); len(secrets) > 0 || len(params) > 0 {
		return nil, &NeedsValues{Secrets: secrets, Params: params}
	}
	s.newReplayLocked()
	s.vars = map[string]string{}
	var failure error
	good := n
	for i := 0; i < n; i++ {
		ev := s.events[i]
		if ev.isAssert() {
			continue
		}
		if err := s.replayLocked(ctx, ev); err != nil {
			good, failure = i, fmt.Errorf("step %d (%s) did not go through: %w", i+1, ev.id, err)
			if ev.use != "" {
				s.dirty = true
			}
			break
		}
	}
	s.at, s.screen = good, ""
	if failure == nil {
		s.dirty = false
	}
	f, err := s.refreshLocked(ctx)
	switch {
	case failure != nil && err == nil:
		return f, failure
	case failure != nil:
		return nil, failure
	}
	return f, err
}

// replayLocked sends one recorded step to the device, as it was sent when it was recorded.
func (s *Session) replayLocked(ctx context.Context, ev *event) error {
	vars := s.scope()
	if ev.use != "" {
		entry, err := flow.Load(s.o.Repo, ev.use)
		if err != nil {
			return s.wrap(err)
		}
		res, err := flow.RunWith(ctx, s.h.Engine, screenSource{s.o.Repo}, entry.Flow, ev.with, vars, flow.Options{
			By: domain.ActorPilot, KeepState: true, Flows: flow.Loader(s.o.Repo).Flow,
			Use: func(name string) domain.UseCase { return s.use(ev.id + "." + name) },
		})
		if err != nil {
			return s.wrap(err)
		}
		if res.Status != flow.StatusCompleted {
			return fail("the flow %s did not complete: %s", ev.use, s.ov.Scrub(res.Error))
		}
		maps.Copy(s.vars, res.Captured)
		return nil
	}
	m := domain.MobileAction{Do: ev.do, Value: ev.value, Secret: ev.secret, Link: ev.link, Screen: ev.screen, Element: ev.elem, Within: ev.within}
	if ev.to != nil {
		m.To = ev.to.elem
	}
	caps := ev.captures
	if ev.capture != nil {
		// A capture reads the screen as it is after the step before it: it waits for the element and reads it.
		m = domain.MobileAction{Do: domain.MobileWait, Screen: ev.screen, Element: ev.elem}
		caps = []domain.Capture{{Name: ev.capture.name, Element: ev.elem, Pattern: ev.capture.pattern}}
	}
	if ev.do == domain.MobileLaunch {
		m.Reset, m.Screen = ev.reset, ""
	}
	m, err := script.RenderMobile(m, vars)
	if err != nil {
		return err
	}
	locs := make(map[string]domain.Locator, len(ev.locs))
	for name, l := range ev.locs {
		if locs[name], err = script.RenderLocator(l, vars); err != nil {
			return err
		}
	}
	res, err := s.runCall(ctx, ev.id, m, locs, caps, nil)
	if err != nil {
		return err
	}
	maps.Copy(s.vars, res.Captured)
	return nil
}

// pickCandidate chooses a locator from the usable ones. 0 is the best one that is not a position: a
// position is used only when a person chooses it, by its number.
func pickCandidate(usable []Candidate, k int) (best Candidate, alts []Candidate, err error) {
	if len(usable) == 0 {
		return Candidate{}, nil, ErrNoLocator
	}
	i := k - 1
	if k == 0 {
		i = slices.IndexFunc(usable, func(c Candidate) bool { return !c.Explicit })
		if i < 0 {
			return Candidate{}, nil, ErrNoLocator
		}
	}
	if i < 0 || i >= len(usable) {
		return Candidate{}, nil, fail("there is no locator %d: pick one from 1 to %d", k, len(usable))
	}
	alts = slices.Concat(usable[:i:i], usable[i+1:])
	return usable[i], alts, nil
}

// LocatorInfo is one way to find an element, as a person reads it.
type LocatorInfo struct {
	// K is the number to choose it by, from 1. It is 0 for a locator that is refused.
	K         int    `json:"k"`
	Locator   string `json:"locator"`
	Tier      string `json:"tier,omitempty"`
	Stability string `json:"stability,omitempty"`
	// Matches is how many elements the locator finds, for one that finds several and is refused.
	Matches int    `json:"matches,omitempty"`
	Note    string `json:"note,omitempty"`
	// Explicit is true for a position, which is used only when it is chosen.
	Explicit bool `json:"explicit,omitempty"`
	// Current is true for the locator that the step uses now.
	Current bool `json:"current,omitempty"`
}

// CandidateList is what finds an element: the locators that find it alone, best first, and the ones that
// find it among several, which a replay refuses.
type CandidateList struct {
	Usable  []LocatorInfo `json:"usable"`
	Refused []LocatorInfo `json:"refused,omitempty"`
}

func (s *Session) infoOf(k int, c Candidate) LocatorInfo {
	return LocatorInfo{
		K: k, Locator: s.ov.Scrub(locatorText(c.Locator, c.Container)), Tier: c.Tier, Stability: c.Stability,
		Matches: c.Matches, Note: s.ov.Scrub(c.Note), Explicit: c.Explicit,
	}
}

func purposeOf(do string) Purpose {
	switch domain.MobileOp(do) {
	case domain.MobileTap, domain.MobileLongTap, domain.MobileDoubleTap:
		return ForTap
	case domain.MobileType:
		return ForType
	}
	return ForAssert
}

// Candidates lists the locators that find the element of a frame, for the operation do (tap, longPress,
// doubleTap, type, or any other, which reads the element, a drag included). The Kth usable one is what Act.Candidate chooses.
func (s *Session) Candidates(ctx context.Context, frame uint64, t Target, do string) (*CandidateList, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.liveCheck(); err != nil {
		return nil, err
	}
	p := purposeOf(do)
	tree, picked, err := s.resolve(ctx, frame, t)
	if err != nil {
		return nil, err
	}
	target := targetFor(picked, p)
	if p == ForType && !Editable(target) {
		return nil, fail("that element takes no text")
	}
	if s.leaks(target) {
		return nil, ErrSecretOnScreen
	}
	usable, amb := LocateAll(tree, target, p, s.ov)
	out := &CandidateList{}
	for i, c := range usable {
		out.Usable = append(out.Usable, s.infoOf(i+1, c))
	}
	for _, c := range amb {
		info := s.infoOf(0, c)
		info.Note = fmt.Sprintf("%d matches (refused)", c.Matches)
		out.Refused = append(out.Refused, info)
	}
	return out, nil
}

// optionsLocked is the current locator of a step, then the others that it could have.
func optionsOf(ev *event) []Candidate {
	cur := ev.cand
	cur.Locator, cur.Container = ev.locator, ev.container
	if cur.Tier == "" {
		cur.Tier = TierXPath
		switch {
		case ev.container != nil:
			cur.Tier = TierWithin
		case ev.locator.ID != "":
			cur.Tier = TierID
		case ev.locator.Desc != "":
			cur.Tier = TierDesc
		case ev.locator.Role != "":
			cur.Tier = TierRole
		case ev.locator.Text != "":
			cur.Tier = TierText
		}
	}
	return append([]Candidate{cur}, ev.alts...)
}

// Locators lists the locators of the nth step: the one that it uses is number 1, and the others that were
// found when it was recorded follow. A step that was loaded from files has the one that its file names.
func (s *Session) Locators(n int) ([]LocatorInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ev, err := s.elementStep(n)
	if err != nil {
		return nil, err
	}
	var out []LocatorInfo
	for i, c := range optionsOf(ev) {
		info := s.infoOf(i+1, c)
		info.Current = i == 0
		out = append(out, info)
	}
	return out, nil
}

func (s *Session) elementStep(n int) (*event, error) {
	if n < 1 || n > len(s.events) {
		return nil, fmt.Errorf("there is no step %d: the recording has %d", n, len(s.events))
	}
	ev := s.events[n-1]
	if !ev.element() {
		return nil, fmt.Errorf("step %d has no element, so it has no locator", n)
	}
	return ev, nil
}

// SetLocator makes the Kth locator of Locators the one that the nth step uses. It changes no entry of the
// screens file: the element gets a new name when the recording is saved. It works on the locators that were
// found when the step was recorded or repicked; for another element, use Repick.
func (s *Session) SetLocator(n, k int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ev, err := s.elementStep(n)
	if err != nil {
		return err
	}
	opts := optionsOf(ev)
	if k < 1 || k > len(opts) {
		return fmt.Errorf("step %d has %d locators: pick one from 1 to %d", n, len(opts), len(opts))
	}
	if len(opts) == 1 {
		return fmt.Errorf("step %d has no other locator: repick its element with repick", n)
	}
	if k == 1 {
		return nil
	}
	chosen := opts[k-1]
	ev.choose(chosen, slices.Concat(opts[:k-1:k-1], opts[k:]))
	return nil
}

// Repick points the nth step at another element: the one of the frame, which shows the screen as it is
// before step n. The device must be there: the cursor must be at step n-1, and no step may have been
// deleted since a goto. The element must suit what the step does (a step that types needs a field), and a
// locator that finds several elements is refused. candidate is as in Act. The step keeps its ID and text.
func (s *Session) Repick(ctx context.Context, n int, frame uint64, t Target, candidate int) (*Frame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.liveCheck(); err != nil {
		return nil, err
	}
	ev, err := s.elementStep(n)
	if err != nil {
		return nil, err
	}
	if s.at != n-1 || s.dirty {
		return nil, fmt.Errorf("%w: use goto %d first", ErrNotAtStep, n-1)
	}
	p := ev.purpose
	tree, picked, err := s.resolve(ctx, frame, t)
	if err != nil {
		return nil, err
	}
	target := targetFor(picked, p)
	if p == ForType && !Editable(target) {
		return nil, fail("that element takes no text")
	}
	if s.leaks(target) {
		return nil, ErrSecretOnScreen
	}
	usable, _ := LocateAll(tree, target, p, s.ov)
	if ev.capture != nil {
		// A capture keeps its hint, and no locator of it holds the text that it reads or the value.
		best, alts, err := pickCaptureCandidate(usable, candidate, target.Text, s.vars[ev.capture.name])
		if err != nil {
			return nil, err
		}
		ev.choose(best, alts)
		ev.hint = captureHint(target, ev.capture.name)
		return s.frame, nil
	}
	best, alts, err := pickCandidate(usable, candidate)
	if err != nil {
		return nil, err
	}
	ev.purpose = p
	ev.choose(best, alts)
	ev.hint = HintOf(target)
	return s.frame, nil
}
