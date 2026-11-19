package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/record"
)

const (
	// assertWait is how long a check of the explorer waits for the page to meet it before it says that
	// it does not. A run waits web.timeout, and an explorer that waits that long on a wrong check would
	// stall a flow.
	assertWait = 2 * time.Second
	assertPoll = 100 * time.Millisecond
)

// probe is what Prepare read of one element of a list: where to find it again, and the words that name
// it, or why it could not be read.
type probe struct {
	el  record.Element
	err error
}

// Reading is how a check of the explorer came out on the page: whether it holds, and what the page
// showed when it does not.
type Reading struct {
	Held  bool
	Shown string
}

const describeJS = `function(purpose){ return window.__semarDescribe ? window.__semarDescribe(this, purpose) : null; }`

// Prepare reads where each element that a check names can be found again, while it is on the page. A check
// that an element is not there, or is hidden, names an element that an action of the same turn removes,
// and nothing can be read of it afterwards. The elements are those of the last list. A check that counts
// names the elements that look like its own.
func (x *Explorer) Prepare(ctx context.Context, checks []domain.Check) {
	known := map[string]probe{}
	for _, c := range checks {
		if c.Element == "" {
			continue
		}
		known[knownKey(c)] = x.describeRef(ctx, c)
	}
	x.mu.Lock()
	x.known = known
	x.mu.Unlock()
}

// knownKey tells apart the two readings of one reference: the element, and the group that it belongs to.
func knownKey(c domain.Check) string {
	if c.Count != nil {
		return c.Element + " group"
	}
	return c.Element
}

func (x *Explorer) describeRef(ctx context.Context, c domain.Check) probe {
	if err := ctx.Err(); err != nil {
		return probe{err: err}
	}
	t, err := x.locate(c.Element, true)
	if err != nil {
		return probe{err: err}
	}
	defer t.release()
	purpose := "act"
	if c.Count != nil {
		purpose = "group"
	}
	el, err := x.describe(t, purpose)
	return probe{el: el, err: err}
}

// describe asks the recorder in the page for the locator of the element and the words that name it.
func (x *Explorer) describe(t found, purpose string) (record.Element, error) {
	arg, err := json.Marshal(purpose)
	if err != nil {
		return record.Element{}, err
	}
	res, err := chromedp.Call(x.rec.tab, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: describeJS, ObjectID: t.obj, ReturnByValue: ptr(true), Arguments: []*runtime.CallArgument{{Value: arg}},
	})
	if err != nil {
		return record.Element{}, err
	}
	var el record.Element
	if res.Result == nil || json.Unmarshal(res.Result.Value, &el) != nil || el.Locator.Empty() {
		return record.Element{}, fmt.Errorf("%s cannot be found again by a locator", t.name)
	}
	return el, nil
}

// refuseMany refuses a check of the text of an element whose locator, as a run would use it, matches
// several elements: the check could hold for one and not for the others, so a run would not pass it.
// The handles that the read makes are freed with those of the element.
func (x *Explorer) refuseMany(t found) error {
	el, err := x.describe(t, "check")
	if err != nil {
		return nil // the recorder cannot name it, and the check is refused where it is heard
	}
	const name = "element"
	rd := newReader(x.rec.tab, map[string]domain.Locator{name: el.Locator})
	res, err := rd.resolve(name)
	if err != nil || res.state.Count < 2 {
		return nil
	}
	return fmt.Errorf("%s: %s does not name one element, so name another element of the list for this check",
		check.ManyReason(res.state.Count, candidateNames(res.others)), t.name)
}

// Test decides a check of a form that reads more than a text, on the page as it is now, and records
// nothing. The check names its element by a reference that Prepare read. An error says why the check
// cannot be decided, such as a locator that matches several elements.
func (x *Explorer) Test(ctx context.Context, c domain.Check) (Reading, error) {
	r, _, err := x.decide(ctx, c)
	return r, err
}

// Assert is Test, and records the check, so that the test that the flow writes holds it. The recording
// keeps the verdict, so that a check that the page does not meet is written as a test that fails now.
func (x *Explorer) Assert(ctx context.Context, c domain.Check) (string, error) {
	r, el, err := x.decide(ctx, c)
	if err != nil {
		return "", err
	}
	url, title, err := x.pageNow()
	if err != nil {
		return "", err
	}
	ev := record.Event{Kind: record.KindCheck, Page: record.Page{URL: url, Title: title}, Probe: &record.Probe{Held: r.Held, Shown: r.Shown}}
	if c.Element != "" {
		ev.Target = &el
	}
	x.rec.add(ev)
	return r.Shown, nil
}

// needsOne says whether a check is decided only when its locator picks out one element. A check that
// the element is not there, how many there are, or that it is hidden does not.
func needsOne(c domain.Check) bool {
	return c.Element != "" && c.Exists == nil && c.Count == nil && c.State != "hidden"
}

func (x *Explorer) decide(ctx context.Context, c domain.Check) (Reading, record.Element, error) {
	if err := ctx.Err(); err != nil {
		return Reading{}, record.Element{}, err
	}
	var p probe
	if c.Element != "" {
		x.mu.Lock()
		var ok bool
		p, ok = x.known[knownKey(c)]
		x.mu.Unlock()
		if !ok {
			return Reading{}, record.Element{}, fmt.Errorf("%s was not prepared: use a reference of the list", c.Element)
		}
		if p.err != nil {
			return Reading{}, record.Element{}, p.err
		}
	}
	var err error
	for _, operand := range []*any{&c.Equals, &c.NotEquals, &c.Contains} {
		if s, ok := (*operand).(string); ok {
			if *operand, err = x.Expand(s); err != nil {
				return Reading{}, record.Element{}, err
			}
		}
	}
	deadline := time.Now().Add(assertWait)
	settled := false
	for {
		res, st, repair, err := x.readCheck(ctx, c, p)
		if err != nil {
			return Reading{}, record.Element{}, err
		}
		if needsOne(c) && st.Count > 1 {
			return Reading{}, record.Element{}, fmt.Errorf("%s: %s does not name one element, so name another element of the list for this check",
				check.ManyReason(st.Count, candidateNames(repair)), c.Element)
		}
		held := res.State == domain.CheckTrue
		// A check that something is not there holds on a page that has not drawn it yet, so it is read
		// again when the page is quiet.
		if held && !settled && absence(c) {
			settled = true
			x.rec.Settle(ctx)
			continue
		}
		if held || time.Now().After(deadline) || ctx.Err() != nil {
			if res.State == domain.CheckNotEvaluated {
				return Reading{}, record.Element{}, errors.New(oneLineText(res.Detail))
			}
			return Reading{Held: held, Shown: shownWords(c, st, res)}, p.el, nil
		}
		select {
		case <-ctx.Done():
		case <-time.After(assertPoll):
		}
	}
}

func candidateNames(repair []domain.Candidate) string {
	var out []string
	for _, cand := range repair {
		out = append(out, cand.Locator.String())
	}
	return strings.Join(out, ", ")
}

func oneLineText(s string) string { return strings.Join(strings.Fields(s), " ") }

// readCheck reads the page as a step of a run does, and decides the check with the code that a run uses.
func (x *Explorer) readCheck(ctx context.Context, c domain.Check, p probe) (check.Result, domain.ElementState, []domain.Candidate, error) {
	w := &domain.WebRecord{Op: "check"}
	var st domain.ElementState
	var repair []domain.Candidate
	if c.Element != "" {
		rd := newReader(x.rec.tab, map[string]domain.Locator{c.Element: p.el.Locator})
		defer rd.release()
		if c.Attribute != "" {
			rd.attrs = map[string][]string{c.Element: {c.Attribute}}
		}
		res, err := rd.resolve(c.Element)
		if err != nil {
			return check.Result{}, st, nil, err
		}
		st, repair = res.state, res.others
		st.Presence = c.PresenceOnly()
		w.Elements = map[string]domain.ElementState{c.Element: st}
		w.Repair = res.others
	}
	url, title, err := x.pageNow()
	if err != nil {
		return check.Result{}, st, nil, err
	}
	w.URL, w.Title = url, title
	return check.Eval(c, domain.Action{Surface: domain.SurfaceWeb, Web: w}), st, repair, nil
}

// pageNow gives the address and the title of the page as the recorder reports them.
func (x *Explorer) pageNow() (url, title string, err error) {
	text, err := chromedp.Run(x.rec.tab, chromedp.Evaluate[string](`JSON.stringify({url: location.href, title: document.title})`))
	if err != nil {
		return "", "", fmt.Errorf("the page could not be read: %w", err)
	}
	var got struct{ URL, Title string }
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		return "", "", errors.New("the page could not be read")
	}
	return got.URL, got.Title, nil
}

// shownWords says what the page showed, in the words of the check that it did not meet.
func shownWords(c domain.Check, st domain.ElementState, res check.Result) string {
	switch {
	case c.Page != "", c.Attribute != "":
		if strings.HasPrefix(res.Observed, "no attribute") {
			return res.Observed
		}
		return strconv.Quote(res.Observed)
	case c.State != "" && st.Count == 1:
		return stateWords(st)
	}
	return res.Observed
}

func stateWords(st domain.ElementState) string {
	words := []string{"hidden", "enabled", "not checked"}
	if st.Visible {
		words[0] = "shown"
	}
	if !st.Enabled {
		words[1] = "disabled"
	}
	if st.Checked {
		words[2] = "checked"
	}
	return strings.Join(words, ", ")
}
