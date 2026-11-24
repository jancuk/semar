package mobilerec

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/script"
)

// readLimit is how many characters of an element's text a replay reads: the engine clips the text there,
// so a capture sees the same.
const readLimit = 500

var varName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// nameTaken says whether a variable name is used already, by a param, by a capture of a step, or by a
// value that a flow that the recording uses captured.
func (s *Session) nameTaken(name string) bool {
	if _, ok := s.vars[name]; ok {
		return true
	}
	if _, ok := s.params[name]; ok {
		return true
	}
	if slices.ContainsFunc(s.newParams, func(p domain.FlowParam) bool { return p.Name == name }) {
		return true
	}
	if r := s.o.Resume; r != nil {
		if _, ok := r.Flow.Param(name); ok {
			return true
		}
	}
	return slices.ContainsFunc(s.events, func(e *event) bool {
		return e.capture != nil && e.capture.name == name || slices.ContainsFunc(e.captures, func(c domain.Capture) bool { return c.Name == name })
	})
}

// fill gives a text with the values of its {{names}} in it, for the device. A name that no capture and no
// param gave is refused, so that a typo never reaches the device or a file.
func (s *Session) fill(text string) (string, error) {
	if !strings.Contains(text, "{{") {
		return text, nil
	}
	if err := s.checkRefs(text); err != nil {
		return "", err
	}
	m, err := script.RenderMobile(domain.MobileAction{Value: text}, s.scope())
	if err != nil {
		return "", fail("the text cannot be filled in: %v", err)
	}
	return m.Value, nil
}

// checkRefs refuses a {{name}} that nothing gave a value to.
func (s *Session) checkRefs(text string) error {
	scope := s.scope()
	for _, m := range refPattern.FindAllStringSubmatch(text, -1) {
		if _, ok := scope[m[1]]; !ok {
			return fail("the variable {{%s}} has no value yet: capture it first (capture N %s), or make it a param in a flow", m[1], m[1])
		}
	}
	return nil
}

// CaptureValue takes the text of an element, or the first group of a pattern on it, into the variable
// Name, for the steps after it: they write {{Name}} in a typed text or in the value of a check. It does
// nothing to the device. The value is kept in memory for this session and shown in the notice of the frame
// that it gives, and the files hold the name and the pattern only. It refuses a name that is used, a
// pattern that does not compile or does not match what the element shows now, and a value that is a secret.
// The second result is the value, for a preview.
func (s *Session) CaptureValue(ctx context.Context, r CaptureReq) (*Frame, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.liveCheck(); err != nil {
		return nil, "", err
	}
	if !varName.MatchString(r.Name) {
		return nil, "", fail("the name %q must start with a letter and hold letters, digits, and _", r.Name)
	}
	if s.nameTaken(r.Name) {
		return nil, "", fail("the name %s is used already by a param or a capture", r.Name)
	}
	if err := domain.CheckPattern(r.Pattern); err != nil {
		return nil, "", fail("%v", err)
	}
	if s.ov.Leaks(r.Pattern) {
		return nil, "", ErrSecretOnScreen
	}
	if r.Screen != "" {
		if err := checkName(r.Screen); err != nil {
			return nil, "", err
		}
	}
	if err := s.syncedLocked(); err != nil {
		return nil, "", err
	}
	tree, picked, err := s.resolve(ctx, r.Frame, r.Target)
	if err != nil {
		return nil, "", err
	}
	if s.leaks(picked) {
		return nil, "", ErrSecretOnScreen
	}
	text := picked.Text
	if rs := []rune(text); len(rs) > readLimit {
		text = string(rs[:readLimit])
	}
	if text == "" {
		return nil, "", fail("that element shows no text to capture")
	}
	c := domain.Capture{Name: r.Name, Pattern: r.Pattern}
	value, ok := c.Extract(text)
	if !ok {
		return nil, "", fail("the pattern %s does not match what the element shows: %s", quote(r.Pattern), quote(text))
	}
	if value == "" {
		return nil, "", fail("the pattern matches nothing: the value would be empty")
	}
	if s.ov.Leaks(value) {
		return nil, "", ErrSecretOnScreen
	}
	usable, _ := LocateAll(tree, picked, ForAssert, s.ov)
	best, alts, err := pickCaptureCandidate(usable, r.Candidate, picked.Text, value)
	if err != nil {
		return nil, "", err
	}
	screen := r.Screen
	if screen == "" {
		screen = s.screenName(tree)
	}
	ev := &event{
		id: s.stepID("capture-" + r.Name), screen: screen, hint: captureHint(picked, r.Name), purpose: ForAssert,
		capture: &captureSpec{name: r.Name, pattern: r.Pattern},
	}
	ev.choose(best, alts)
	ev.text = "Remember the text of " + ev.hint + " as " + r.Name
	if r.Pattern != "" {
		ev.text += " by the pattern " + quote(r.Pattern)
	}
	s.insertLocked(ev)
	s.vars[r.Name] = value
	s.seq++
	f := *s.frame
	f.ID = s.seq
	f.Notice = fmt.Sprintf("captured %s = %s: write {{%s}} in a later text or check", r.Name, quote(value), r.Name)
	s.notice = ""
	s.frame = &f
	return s.frame, value, nil
}

// captureHint is what a captured element is called by in the files: its short id, else the name of the
// capture. It never holds the text of the element, because that is the value that the replay reads.
func captureHint(n *mobile.Node, name string) string {
	if n.ID != "" {
		return shortID(n.ID)
	}
	return name
}

// holdsText says that a locator is made of one of the texts: the locator would then be saved with a value
// that changes between runs, and a file would hold what was captured. A locator that looks like a value
// that changes is refused too.
func holdsText(c Candidate, texts ...string) bool {
	if c.Dynamic {
		return true
	}
	locs := []domain.Locator{c.Locator}
	if c.Container != nil {
		locs = append(locs, c.Container.Locator)
	}
	for _, l := range locs {
		for _, f := range []string{l.TestID, l.Name, l.Text, l.CSS, l.ID, l.Desc, l.XPath, l.HasText} {
			for _, t := range texts {
				if t = strings.TrimSpace(t); t != "" && strings.Contains(f, t) {
					return true
				}
			}
		}
	}
	return c.Container != nil && slices.ContainsFunc(texts, func(t string) bool {
		t = strings.TrimSpace(t)
		return t != "" && strings.Contains(c.Container.Hint, t)
	})
}

// pickCaptureCandidate is pickCandidate for a step that reads the text of an element: a locator that
// holds the text, or the value, is never used or kept as another choice. 0 gives the best one that is
// safe, and a number that names one that is not safe is refused, so that the person picks another.
func pickCaptureCandidate(usable []Candidate, k int, texts ...string) (Candidate, []Candidate, error) {
	if k > 0 && k <= len(usable) && holdsText(usable[k-1], texts...) {
		return Candidate{}, nil, fail("locator %d holds the text that the capture reads, so a file would hold the value: pick another with N.K or repick", k)
	}
	if k == 0 {
		i := slices.IndexFunc(usable, func(c Candidate) bool { return !c.Explicit && !holdsText(c, texts...) })
		if i < 0 {
			if len(usable) == 0 {
				return Candidate{}, nil, ErrNoLocator
			}
			return Candidate{}, nil, ErrCaptureHoldsText
		}
		k = i + 1
	}
	best, alts, err := pickCandidate(usable, k)
	if err != nil {
		return best, alts, err
	}
	alts = slices.DeleteFunc(alts, func(c Candidate) bool { return holdsText(c, texts...) })
	return best, alts, nil
}

// flowParams are the params that the flow of the recording has: the ones of the file that was reopened,
// and the ones that MakeParam made.
func (s *Session) flowParams() []domain.FlowParam {
	var out []domain.FlowParam
	if r := s.o.Resume; r != nil && s.o.Kind == KindFlow {
		out = append(out, r.Flow.Params...)
	}
	return append(out, s.newParams...)
}

// MakeParam turns the text that the nth step types into the param name of the flow: the step then types
// {{name}}. The param gets the old text as its default, unless required is true, and then the caller must
// give it. The recording must be a flow, and the text must be one that was typed and not a secret. The
// result says which other steps type the same text, so that the person can decide to change them too.
func (s *Session) MakeParam(n int, name string, required bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.o.Kind != KindFlow {
		return "", fail("a scenario has no params: capture a value from the screen, or record a flow and make the param there")
	}
	if n < 1 || n > len(s.events) {
		return "", fmt.Errorf("there is no step %d: the recording has %d", n, len(s.events))
	}
	ev := s.events[n-1]
	switch {
	case ev.do != domain.MobileType || ev.use != "" || ev.isRead():
		return "", fail("step %d does not type a text: only the text of a type step becomes a param", n)
	case ev.secret != "":
		return "", fail("step %d types a secret: a secret is never a param, because a param may have a default in the file", n)
	case ev.value == "":
		return "", fail("step %d types no text", n)
	case strings.Contains(ev.value, "{{"):
		return "", fail("step %d types a variable already", n)
	}
	if !varName.MatchString(name) {
		return "", fail("the name %q must start with a letter and hold letters, digits, and _", name)
	}
	if s.nameTaken(name) {
		return "", fail("the name %s is used already by a param or a capture", name)
	}
	old := ev.value
	if s.ov.Leaks(old) {
		return "", ErrSecretOnScreen
	}
	p := domain.FlowParam{Name: name}
	if !required {
		p.Default = &old
	}
	s.newParams = append(s.newParams, p)
	s.params[name] = old
	ev.value = "{{" + name + "}}"
	ev.text = "Type " + quote(ev.value) + " in " + ev.hint
	var same []int
	for i, o := range s.events {
		if i != n-1 && o.do == domain.MobileType && o.value == old {
			same = append(same, i+1)
		}
	}
	if len(same) == 0 {
		return "", nil
	}
	nums := make([]string, len(same))
	for i, v := range same {
		nums[i] = fmt.Sprint(v)
	}
	return fmt.Sprintf("the steps %s type the same text: they still type it as it is", strings.Join(nums, ", ")), nil
}

// checkUse refuses a flow that a flow recording may not use: the flow is not there, its params are not
// given right, or the recording would use itself, through the flows that it uses, or nest too deep. The
// recording's own ID counts, though no file holds it yet. A scenario is checked when it is built.
func (s *Session) checkUse(own string, kind Kind, id string, with map[string]string) error {
	if kind != KindFlow {
		return nil
	}
	probe := domain.Flow{
		APIVersion: "semar/v1", Kind: "Flow", ID: own, Surface: domain.SurfaceAndroid, Service: s.o.Service, App: s.o.App,
		Steps: []domain.ScriptStep{{Step: "use", Use: id, With: with}},
	}
	// The values that the steps before captured, and the params, are known to the with of the step.
	known := map[string]bool{}
	for _, p := range s.flowParams() {
		known[p.Name] = true
		probe.Params = append(probe.Params, p)
	}
	for name := range s.vars {
		if !known[name] && varName.MatchString(name) {
			empty := ""
			probe.Params = append(probe.Params, domain.FlowParam{Name: name, Default: &empty})
		}
	}
	slices.SortFunc(probe.Params, func(a, b domain.FlowParam) int { return strings.Compare(a.Name, b.Name) })
	resolve := func(x string) (domain.Flow, error) {
		e, err := flow.Load(s.o.Repo, x)
		return e.Flow, err
	}
	if ps := flow.ValidateWith(probe, own, resolve); len(ps) > 0 {
		return fail("the flow %s cannot be used here: %s", id, ps[0].Message)
	}
	return nil
}
