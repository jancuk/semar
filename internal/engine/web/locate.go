package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/cdp/jsonv2"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/jancuk/semar/internal/domain"
)

const (
	objectGroup = "semar"
	maxText     = 500
	maxMatches  = 200
	maxWithin   = 16
)

// inspectJS reads what a check needs of one element: whether a person could see it, and its text. A
// field takes its value, and a password field takes none. root is optional, and says whether the
// element is inside it.
const inspectJS = `function(root, names){
  const el = this;
  const cs = getComputedStyle(el);
  let visible = cs.visibility !== 'hidden' && cs.display !== 'none' && el.getClientRects().length > 0;
  if (visible) { const b = el.getBoundingClientRect(); visible = b.width > 0 && b.height > 0 && cs.opacity !== '0'; }
  let t = '';
  const tag = el.tagName;
  if (tag === 'INPUT' || tag === 'TEXTAREA') { t = el.type === 'password' ? '' : (el.value || ''); }
  else if (tag === 'SELECT') { t = Array.from(el.selectedOptions).map(o => o.text).join(', '); }
  else { t = visible ? (el.innerText || '') : (el.textContent || ''); }
  t = t.replace(/\s+/g, ' ').trim();
  const at = {};
  for (const n of (names || [])) {
    if (!el.hasAttribute(n)) continue;
    if (n.toLowerCase() === 'value' && tag === 'INPUT' && el.type === 'password') continue;
    at[n] = el.getAttribute(n);
  }
  const aria = el.getAttribute('aria-disabled');
  return {
    v: visible, t: t, in: root ? root.contains(el) : true, tag: tag.toLowerCase(), tid: el.getAttribute('data-testid') || '', at: at,
    en: !el.matches(':disabled') && aria !== 'true',
    ch: el.checked === true || el.getAttribute('aria-checked') === 'true',
  };
}`

// searchJS finds elements by test ID, by css, or by exact visible text, below this (a document or an
// element). The text of an element is its own visible text, and an element whose child has the same
// text is left out, so that only the innermost one is found.
const searchJS = `function(kind, arg){
  const root = this;
  const norm = s => (s || '').replace(/\s+/g, ' ').trim();
  const vis = el => { const cs = getComputedStyle(el); return cs.visibility !== 'hidden' && cs.display !== 'none' && el.getClientRects().length > 0; };
  if (kind === 'testid') return Array.from(root.querySelectorAll('[data-testid=' + JSON.stringify(arg) + ']'));
  if (kind === 'css') return Array.from(root.querySelectorAll(arg));
  const want = norm(arg), out = [];
  for (const el of root.querySelectorAll('*')) {
    if (['SCRIPT', 'STYLE', 'NOSCRIPT', 'TEMPLATE'].includes(el.tagName) || !vis(el)) continue;
    if (norm(el.innerText) !== want) continue;
    let deeper = false;
    for (const c of el.children) { if (vis(c) && norm(c.innerText) === want) { deeper = true; break; } }
    if (!deeper) out.push(el);
  }
  return out;
}`

type resolved struct {
	state domain.ElementState
	id    cdp.BackendNodeID
	// others names up to maxNamed of the elements that a locator matched when it matched several.
	others []domain.Candidate
}

// maxNamed is how many of the elements that one locator matched are named for a person.
const maxNamed = 3

type axEntry struct {
	id         cdp.BackendNodeID
	role, name string
}

// reader reads elements of one page for one action. It finds each locator, follows within, and keeps
// the accessibility tree, so that the elements of a step are read from one picture of the page.
type reader struct {
	ctx  context.Context
	locs map[string]domain.Locator
	ax   []axEntry
	axOK bool
	doc  runtime.RemoteObjectID
	memo map[string]resolved
	// attrs holds the attributes that checks read, by the name of the element.
	attrs map[string][]string
}

func newReader(ctx context.Context, locs map[string]domain.Locator) *reader {
	return &reader{ctx: ctx, locs: locs, memo: map[string]resolved{}}
}

// release lets the page free the handles that the reader made.
func (r *reader) release() {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), 2*time.Second)
	defer cancel()
	_, _ = chromedp.Call(ctx, runtime.ReleaseObjectGroup, runtime.ReleaseObjectGroupParams{ObjectGroup: objectGroup})
}

// errLocator is an error of the locator or of the page, as opposed to an element that is not there.
type errLocator struct{ msg string }

func (e errLocator) Error() string { return e.msg }

// resolve finds the element of a name. An element that is not there is a state with Found false and
// no error. An error means that the page could not be read.
func (r *reader) resolve(name string) (resolved, error) {
	return r.resolveDepth(name, 0)
}

func (r *reader) resolveDepth(name string, depth int) (resolved, error) {
	if res, ok := r.memo[name]; ok {
		return res, nil
	}
	loc, ok := r.locs[name]
	if !ok {
		return resolved{}, errLocator{fmt.Sprintf("the locator of element %s was not given to the engine", name)}
	}
	if depth > maxWithin {
		return resolved{}, errLocator{fmt.Sprintf("element %s is inside too many other elements", name)}
	}
	res := resolved{state: domain.ElementState{Locator: &loc}}
	var scope runtime.RemoteObjectID
	if loc.Within != "" {
		outer, err := r.resolveDepth(loc.Within, depth+1)
		if err != nil {
			return resolved{}, err
		}
		if !outer.state.Found {
			// Nothing is inside an element that is not there.
			r.memo[name] = res
			return res, nil
		}
		if scope, err = r.handle(outer.id); err != nil {
			return resolved{}, err
		}
	}
	ids, err := r.find(loc, scope)
	if err != nil {
		return resolved{}, fmt.Errorf("element %s: %w", name, err)
	}
	if len(ids) > maxMatches {
		ids = ids[:maxMatches]
	}
	var seen []cdp.BackendNodeID
	var matched []match
	for _, id := range ids {
		info, err := r.inspect(id, scope, r.attrs[name])
		if err != nil {
			return resolved{}, err
		}
		if !info.In {
			continue
		}
		res.state.Count++
		matched = append(matched, match{id, info})
		if info.Visible {
			res.state.Visible = true
			seen = append(seen, id)
			res.state.Text = clip(info.Text)
		}
	}
	if len(seen) == 1 {
		res.state.Found, res.id = true, seen[0]
	} else {
		res.state.Text = ""
	}
	switch len(matched) {
	case 1:
		only := matched[0].info
		res.state.Enabled, res.state.Checked = only.Enabled, only.Checked
		if len(only.Attrs) > 0 {
			res.state.Attrs = map[string]string{}
			for k, v := range only.Attrs {
				res.state.Attrs[k] = clip(v)
			}
		}
	case 0:
	default:
		res.others = r.name(name, matched)
	}
	r.memo[name] = res
	return res, nil
}

func clip(s string) string {
	if r := []rune(s); len(r) > maxText {
		return string(r[:maxText])
	}
	return s
}

// find gives the elements that the first strategy of the locator that matches anything gives, and
// none when no strategy matches. The order is the order of the TRD: test ID, role and name, text, css.
func (r *reader) find(loc domain.Locator, scope runtime.RemoteObjectID) ([]cdp.BackendNodeID, error) {
	for _, s := range loc.Strategies() {
		var ids []cdp.BackendNodeID
		var err error
		switch s {
		case "testId":
			ids, err = r.search("testid", loc.TestID, scope)
		case "role":
			ids, err = r.byRole(loc.Role, loc.Name)
		case "text":
			ids, err = r.search("text", loc.Text, scope)
		case "css":
			ids, err = r.search("css", loc.CSS, scope)
		}
		if err != nil {
			return nil, err
		}
		if len(ids) > 0 {
			return ids, nil
		}
	}
	return nil, nil
}

// byRole matches the accessibility tree: the role, when there is one, and the accessible name, when
// there is one, exactly after the spaces are collapsed.
func (r *reader) byRole(role, name string) ([]cdp.BackendNodeID, error) {
	tree, err := r.tree()
	if err != nil {
		return nil, err
	}
	var ids []cdp.BackendNodeID
	for _, n := range tree {
		if role != "" && !strings.EqualFold(n.role, role) {
			continue
		}
		if name != "" && n.name != name {
			continue
		}
		ids = append(ids, n.id)
	}
	return ids, nil
}

// tree is the accessibility tree of the page, without the nodes that the tree ignores.
func (r *reader) tree() ([]axEntry, error) {
	if r.axOK {
		return r.ax, nil
	}
	res, err := chromedp.Call(r.ctx, accessibility.GetFullAXTree, accessibility.GetFullAXTreeParams{})
	if err != nil {
		return nil, fmt.Errorf("the accessibility tree could not be read: %w", err)
	}
	for _, n := range res.Nodes {
		if n.Ignored || n.BackendDOMNodeID == 0 {
			continue
		}
		r.ax = append(r.ax, axEntry{id: n.BackendDOMNodeID, role: axString(n.Role), name: normSpace(axString(n.Name))})
	}
	r.axOK = true
	return r.ax, nil
}

func axString(v *accessibility.Value) string {
	if v == nil {
		return ""
	}
	var s string
	if err := json.Unmarshal(v.Value, &s); err != nil {
		return ""
	}
	return s
}

func normSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// Tree gives the accessibility tree for the repair of an element that was not found.
func (r *reader) axNodes() ([]AXNode, error) {
	tree, err := r.tree()
	if err != nil {
		return nil, err
	}
	out := make([]AXNode, len(tree))
	for i, n := range tree {
		out[i] = AXNode{Role: n.role, Name: n.name}
	}
	return out, nil
}

func (r *reader) document() (runtime.RemoteObjectID, error) {
	if r.doc != "" {
		return r.doc, nil
	}
	res, err := chromedp.Call(r.ctx, runtime.Evaluate, runtime.EvaluateParams{Expression: "document", ObjectGroup: objectGroup})
	if err != nil {
		return "", err
	}
	if res.Result == nil || res.Result.ObjectID == "" {
		return "", errors.New("the page has no document")
	}
	r.doc = res.Result.ObjectID
	return r.doc, nil
}

// search runs searchJS below the scope, or below the document, and gives the backend IDs of the elements.
func (r *reader) search(kind, arg string, scope runtime.RemoteObjectID) ([]cdp.BackendNodeID, error) {
	if scope == "" {
		var err error
		if scope, err = r.document(); err != nil {
			return nil, err
		}
	}
	res, err := chromedp.Call(r.ctx, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: searchJS,
		ObjectID:            scope,
		Arguments:           []*runtime.CallArgument{{Value: jsonv2.Value(strconv.Quote(kind))}, {Value: quote(arg)}},
		ObjectGroup:         objectGroup,
	})
	if err != nil {
		return nil, err
	}
	if res.ExceptionDetails != nil {
		return nil, errLocator{"the locator could not be used: " + exceptionText(res.ExceptionDetails)}
	}
	if res.Result == nil || res.Result.ObjectID == "" {
		return nil, nil
	}
	return r.elementsOf(res.Result.ObjectID)
}

// elementsOf gives the backend IDs of the elements that a page array holds.
func (r *reader) elementsOf(array runtime.RemoteObjectID) ([]cdp.BackendNodeID, error) {
	props, err := chromedp.Call(r.ctx, runtime.GetProperties, runtime.GetPropertiesParams{ObjectID: array, OwnProperties: ptr(true)})
	if err != nil {
		return nil, err
	}
	var ids []cdp.BackendNodeID
	for _, p := range props.Result {
		if p.Value == nil || p.Value.ObjectID == "" {
			continue
		}
		if _, err := strconv.Atoi(p.Name); err != nil {
			continue
		}
		d, err := chromedp.Call(r.ctx, dom.DescribeNode, dom.DescribeNodeParams{ObjectID: p.Value.ObjectID})
		if err != nil {
			return nil, err
		}
		if d.Node != nil {
			ids = append(ids, d.Node.BackendNodeID)
		}
	}
	return ids, nil
}

func exceptionText(d *runtime.ExceptionDetails) string {
	if d.Exception != nil && d.Exception.Description != "" {
		first, _, _ := strings.Cut(d.Exception.Description, "\n")
		return first
	}
	return d.Text
}

func quote(s string) jsonv2.Value {
	b, _ := json.Marshal(s)
	return jsonv2.Value(b)
}

func ptr[T any](v T) *T { return &v }

// handle gives the page object of a node, which the page functions run on.
func (r *reader) handle(id cdp.BackendNodeID) (runtime.RemoteObjectID, error) {
	res, err := chromedp.Call(r.ctx, dom.ResolveNode, dom.ResolveNodeParams{BackendNodeID: id, ObjectGroup: objectGroup})
	if err != nil {
		return "", err
	}
	if res.Object == nil || res.Object.ObjectID == "" {
		return "", errors.New("the element is no longer on the page")
	}
	return res.Object.ObjectID, nil
}

type inspected struct {
	Visible bool              `json:"v"`
	Text    string            `json:"t"`
	In      bool              `json:"in"`
	Tag     string            `json:"tag"`
	TestID  string            `json:"tid"`
	Attrs   map[string]string `json:"at"`
	Enabled bool              `json:"en"`
	Checked bool              `json:"ch"`
}

type match struct {
	id   cdp.BackendNodeID
	info inspected
}

// name proposes a locator for each of the first elements that a locator matched, as the repair
// proposes one for an element that is gone: a test ID, a role with a name, or a text.
func (r *reader) name(element string, matched []match) []domain.Candidate {
	tree, _ := r.tree()
	byID := map[cdp.BackendNodeID]axEntry{}
	for _, n := range tree {
		byID[n.id] = n
	}
	why := fmt.Sprintf("one of the %d elements that the locator matches", len(matched))
	var out []domain.Candidate
	for _, m := range matched {
		if len(out) == maxNamed {
			break
		}
		var loc domain.Locator
		switch ax := byID[m.id]; {
		case m.info.TestID != "":
			loc = domain.Locator{TestID: m.info.TestID}
		case meaningful(ax.role) && ax.name != "":
			loc = domain.Locator{Role: ax.role, Name: ax.name}
		case m.info.Text != "" && len([]rune(m.info.Text)) <= 60:
			loc = domain.Locator{Text: m.info.Text}
		default:
			loc = domain.Locator{CSS: m.info.Tag}
		}
		out = append(out, domain.Candidate{Element: element, Locator: loc, Why: why})
	}
	return out
}

func (r *reader) inspect(id cdp.BackendNodeID, scope runtime.RemoteObjectID, attrs []string) (inspected, error) {
	h, err := r.handle(id)
	if err != nil {
		// An element that went away while it was read is not on the page.
		return inspected{}, nil
	}
	call := runtime.CallFunctionOnParams{FunctionDeclaration: inspectJS, ObjectID: h, ReturnByValue: ptr(true)}
	names, _ := json.Marshal(attrs)
	if attrs == nil {
		names = []byte("null")
	}
	if scope != "" {
		call.Arguments = []*runtime.CallArgument{{ObjectID: scope}, {Value: names}}
	} else {
		call.Arguments = []*runtime.CallArgument{{Value: []byte("null")}, {Value: names}}
	}
	res, err := chromedp.Call(r.ctx, runtime.CallFunctionOn, call)
	if err != nil {
		return inspected{}, err
	}
	if res.ExceptionDetails != nil || res.Result == nil {
		return inspected{}, nil
	}
	var out inspected
	if err := json.Unmarshal(res.Result.Value, &out); err != nil {
		return inspected{}, nil
	}
	return out, nil
}
