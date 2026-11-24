package webmcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/pagelist"
	"github.com/jancuk/semar/internal/record"
	"github.com/jancuk/semar/internal/takeover"
	"github.com/jancuk/semar/internal/webmcp"
)

const (
	base       = "https://shop.test"
	pwdSecret  = "hunter2-very-secret"
	secretName = "SHOP_PASSWORD"
)

// fakeBrowser is a window that records what it was asked to do.
type fakeBrowser struct {
	mu     sync.Mutex
	list   pagelist.List
	events []record.Event
	calls  []string
	shows  map[string]string
	// pageText is put into the list of the next call, to show a value on the page.
	closed int
	loc    string
	failOn string
}

func newBrowser() *fakeBrowser {
	return &fakeBrowser{
		loc:   base + "/",
		shows: map[string]string{"e1": "Orders", "e4": "Refunded 5.00"},
		list: pagelist.Build(base+"/", "Shop", []pagelist.Item{
			{Index: 0, Role: "heading", Name: "Orders", Acts: false},
			{Index: 1, Role: "textbox", Name: "Email", Acts: true},
			{Index: 2, Role: "textbox", Name: "Password", Acts: true, Secret: true},
			{Index: 3, Role: "button", Name: "Refund", Acts: true},
			{Index: 4, Role: "checkbox", Name: "Notify", Acts: true, State: "unchecked"},
			{Index: 5, Role: "combobox", Name: "Reason", Acts: true, Options: []string{"Damaged", "Late"}},
		}),
	}
}

func (b *fakeBrowser) rec(call string, ev ...record.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, call)
	if b.failOn == call {
		return errors.New("the page said no")
	}
	b.events = append(b.events, ev...)
	return nil
}

func (b *fakeBrowser) List(context.Context) (pagelist.List, error) {
	_ = b.rec("list")
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.list, nil
}
func (b *fakeBrowser) Click(_ context.Context, ref string) error {
	return b.rec("click "+ref, record.Event{Kind: record.KindClick})
}
func (b *fakeBrowser) Fill(_ context.Context, ref, text string) error {
	return b.rec("fill "+ref+" "+text, record.Event{Kind: record.KindFill, Value: text})
}
func (b *fakeBrowser) Select(_ context.Context, ref, label string) error {
	return b.rec("select "+ref+" "+label, record.Event{Kind: record.KindSelect})
}
func (b *fakeBrowser) SetChecked(_ context.Context, ref string, on bool) error {
	return b.rec(fmt.Sprintf("check %s %v", ref, on), record.Event{Kind: record.KindTick})
}
func (b *fakeBrowser) Press(_ context.Context, ref, key string) error {
	return b.rec("press "+ref+" "+key, record.Event{Kind: record.KindPress})
}
func (b *fakeBrowser) Hover(_ context.Context, ref string) error {
	return b.rec("hover "+ref, record.Event{Kind: record.KindHover})
}
func (b *fakeBrowser) Drag(_ context.Context, from, to string) error {
	return b.rec("drag "+from+" "+to, record.Event{Kind: record.KindDrag})
}
func (b *fakeBrowser) Shows(_ context.Context, ref string) (string, error) {
	return b.shows[ref], b.rec("shows " + ref)
}
func (b *fakeBrowser) Check(_ context.Context, ref string) (string, error) {
	return b.shows[ref], b.rec("assert "+ref, record.Event{Kind: record.KindCheck, Value: b.shows[ref]})
}
func (b *fakeBrowser) Open(_ context.Context, u string) error {
	return b.rec("open "+u, record.Event{Kind: record.KindOpen, Page: record.Page{URL: u}})
}
func (b *fakeBrowser) Screenshot(context.Context) ([]byte, error) {
	return []byte("\xff\xd8jpeg"), b.rec("screenshot")
}
func (b *fakeBrowser) Location(context.Context) (string, error) { return b.loc, nil }
func (b *fakeBrowser) Mark() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.events)
}
func (b *fakeBrowser) EventsFrom(i int) []record.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	if i >= len(b.events) {
		return nil
	}
	return append([]record.Event(nil), b.events[i:]...)
}
func (b *fakeBrowser) Vars() map[string]string { return map[string]string{"run.id": "r1"} }
func (b *fakeBrowser) Blocked() []string       { return nil }
func (b *fakeBrowser) Close() []record.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed++
	return append([]record.Event(nil), b.events...)
}

func (b *fakeBrowser) did(call string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.calls {
		if c == call || strings.HasPrefix(c, call+" ") {
			return true
		}
	}
	return false
}

type fakeBackend struct {
	mu       sync.Mutex
	br       *fakeBrowser
	opens    []webmcp.OpenRequest
	saved    []webmcp.SaveRequest
	events   [][]record.Event
	saveErr  error
	openErr  error
	savedVar map[string]string
}

func (f *fakeBackend) Open(_ context.Context, r webmcp.OpenRequest) (webmcp.Browser, webmcp.OpenInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.openErr != nil {
		return nil, webmcp.OpenInfo{}, f.openErr
	}
	f.opens = append(f.opens, r)
	f.br.events = []record.Event{{Kind: record.KindOpen, Page: record.Page{URL: r.URL}}}
	return f.br, webmcp.OpenInfo{Service: r.Service, Account: "support", URL: r.URL}, nil
}

func (f *fakeBackend) Save(_ context.Context, r webmcp.SaveRequest, ev []record.Event, vars map[string]string) (webmcp.SaveResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved, f.events, f.savedVar = append(f.saved, r), append(f.events, ev), vars
	if f.saveErr != nil {
		return webmcp.SaveResult{}, f.saveErr
	}
	return webmcp.SaveResult{ScenarioPath: ".semar/scenarios/" + r.ID + ".yaml", ScreensPath: ".semar/screens/shop.yaml", Actions: 2, Checks: 1, Replay: "passed"}, nil
}

type rig struct {
	t     *testing.T
	m     *webmcp.Manager
	be    *fakeBackend
	br    *fakeBrowser
	scrub *mcpkit.Scrubber
	out   *mcpkit.Outputs
	env   map[string]string
	tools map[string]mcp.Tool
}

func newRig(t *testing.T, guard mcpkit.Guard, tweak ...func(*webmcp.Options)) *rig {
	t.Helper()
	br := newBrowser()
	be := &fakeBackend{br: br}
	out, err := mcpkit.NewOutputs(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	pol, err := effects.New(config.Environment{Hosts: []string{"shop.test"}, Methods: []string{"GET", "POST"}, Effects: config.Effects{Mode: "test_data"}})
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, be: be, br: br, scrub: mcpkit.NewScrubber(nil), out: out, env: map[string]string{secretName: pwdSecret}}
	o := webmcp.Options{
		Backend: be, Services: []webmcp.Service{{Name: "shop", BaseURL: base, Start: "/"}}, Hosts: []string{"shop.test"}, Policy: pol,
		Guard: guard, Scrub: r.scrub, Outputs: out,
		LookupEnv: func(n string) (string, bool) { v, ok := r.env[n]; return v, ok },
	}
	for _, f := range tweak {
		f(&o)
	}
	r.m = webmcp.New(o)
	r.tools = map[string]mcp.Tool{}
	for _, tool := range r.m.Tools() {
		r.tools[tool.Name] = tool
	}
	return r
}

// call runs a tool and gives its text, and whether it failed.
func (r *rig) call(name string, args any) (string, bool) {
	r.t.Helper()
	tool, ok := r.tools[name]
	if !ok {
		r.t.Fatalf("no tool %s", name)
	}
	raw, _ := json.Marshal(args)
	res, err := tool.Handler(context.Background(), mcp.Call{Name: name, Args: raw, Progress: func(float64, float64, string) {}})
	if err != nil {
		r.t.Fatalf("%s: %v", name, err)
	}
	var sb strings.Builder
	for _, c := range res.Content {
		sb.WriteString(c.Text)
	}
	return sb.String(), res.IsError
}

func (r *rig) ok(name string, args any) string {
	r.t.Helper()
	text, failed := r.call(name, args)
	if failed {
		r.t.Fatalf("%s failed: %s", name, text)
	}
	return text
}

func (r *rig) refused(name string, args any, want string) {
	r.t.Helper()
	text, failed := r.call(name, args)
	if !failed {
		r.t.Fatalf("%s should be refused, gave %s", name, text)
	}
	if want != "" && !strings.Contains(text, want) {
		r.t.Fatalf("%s: %q does not mention %q", name, text, want)
	}
}

var wantTools = []string{
	"web_open", "web_list_elements", "web_click", "web_fill", "web_select", "web_set_checked", "web_press",
	"web_hover", "web_drag", "web_read", "web_assert", "web_screenshot", "web_save", "web_close",
}

func TestToolsAreTheExpectedSetAndPortable(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	var names []string
	for _, tool := range r.m.Tools() {
		names = append(names, tool.Name)
		if err := mcp.ValidatePortableSchema(tool.Input); err != nil {
			t.Errorf("%s: %v", tool.Name, err)
		}
		if tool.Surface != "web" {
			t.Errorf("%s: surface %q", tool.Name, tool.Surface)
		}
		if tool.Annotations.ReadOnly == nil || tool.Write == *tool.Annotations.ReadOnly {
			t.Errorf("%s: Write=%v with readOnly=%v", tool.Name, tool.Write, tool.Annotations.ReadOnly)
		}
	}
	if !reflect.DeepEqual(names, wantTools) {
		t.Errorf("tools = %v", names)
	}
	if _, err := mcp.NewServer(mcp.Info{Name: "t", Version: "1"}, r.m.Tools(), nil); err != nil {
		t.Fatalf("the server refuses the tools: %v", err)
	}
}

func TestHappyPathOfEveryTool(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	text := r.ok("web_open", map[string]any{"path": "/orders/42", "account": "support"})
	if !strings.Contains(text, "e4 button \"Refund\"") {
		t.Fatalf("the list is not in the answer: %s", text)
	}
	if got := r.be.opens[0]; got.URL != base+"/orders/42" || got.Service != "shop" || got.Account != "support" || got.ReadOnly {
		t.Fatalf("open request %+v", got)
	}
	var list struct {
		Elements []struct{ Ref, Role string }
	}
	if err := json.Unmarshal([]byte(r.ok("web_list_elements", map[string]any{"format": "json"})), &list); err != nil || len(list.Elements) != 6 {
		t.Fatalf("json list: %v %+v", err, list)
	}
	steps := []struct {
		tool string
		args any
		call string
	}{
		{"web_fill", map[string]any{"ref": "e2", "text": "ana@example.com"}, "fill e2 ana@example.com"},
		{"web_click", map[string]any{"ref": "e4"}, "click e4"},
		{"web_select", map[string]any{"ref": "e6", "label": "Late"}, "select e6 Late"},
		{"web_set_checked", map[string]any{"ref": "e5", "checked": true}, "check e5 true"},
		{"web_press", map[string]any{"key": "Enter", "ref": "e2"}, "press e2 Enter"},
		{"web_hover", map[string]any{"ref": "e4"}, "hover e4"},
		{"web_drag", map[string]any{"fromRef": "e4", "toRef": "e5"}, "drag e4 e5"},
		{"web_assert", map[string]any{"ref": "e4"}, "assert e4"},
	}
	for _, s := range steps {
		r.ok(s.tool, s.args)
		if !r.br.did(strings.Fields(s.call)[0] + " " + strings.Fields(s.call)[1]) {
			t.Errorf("%s did not reach the browser as %q: %v", s.tool, s.call, r.br.calls)
		}
	}
	// No recording of web_read.
	n := len(r.br.events)
	if got := r.ok("web_read", map[string]any{"ref": "e1"}); got != "Orders" {
		t.Errorf("web_read = %q", got)
	}
	if len(r.br.events) != n {
		t.Error("web_read recorded an event")
	}
	// A move, by path, is a step of the same window.
	r.ok("web_open", map[string]any{"url": base + "/orders"})
	if !r.br.did("open "+base+"/orders") || len(r.be.opens) != 1 {
		t.Errorf("the second open must move the window: %v opens=%d", r.br.calls, len(r.be.opens))
	}
	img, failed := r.call("web_screenshot", map[string]any{"saveTo": "page.jpg"})
	if failed || !strings.Contains(img, "saved to") {
		t.Fatalf("screenshot: %v %s", failed, img)
	}
	if data, err := os.ReadFile(filepath.Join(r.out.Root(), "page.jpg")); err != nil || !strings.HasPrefix(string(data), "\xff\xd8") {
		t.Errorf("the file is not saved: %v", err)
	}
	out := r.ok("web_save", map[string]any{"id": "SHOP-482-07", "ticket": "SHOP-482", "title": "Refund"})
	if !strings.Contains(out, `"replay":"passed"`) || r.br.closed != 1 {
		t.Fatalf("save: %s closed=%d", out, r.br.closed)
	}
	if got := r.be.saved[0]; got.ID != "SHOP-482-07" || !got.Verify || got.Ticket != "SHOP-482" || got.Info.Account != "support" {
		t.Errorf("save request %+v", got)
	}
	if r.be.savedVar["run.id"] != "r1" || len(r.be.events[0]) == 0 {
		t.Errorf("vars %v events %d", r.be.savedVar, len(r.be.events[0]))
	}
	if r.m.Active() {
		t.Error("the browser stays open after a save")
	}
	r.refused("web_click", map[string]any{"ref": "e4"}, "web_open")
}

func TestSaveFailureClosesTheBrowserAndSaysWhy(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	r.ok("web_open", nil)
	r.ok("web_list_elements", nil)
	r.ok("web_assert", map[string]any{"ref": "e1"})
	r.be.saveErr = errors.New("the replay of the written test did not end as the recording predicts, so the test was removed")
	r.refused("web_save", map[string]any{"id": "SHOP-1-01", "verify": false}, "was removed")
	if r.be.saved[0].Verify {
		t.Error("verify:false was not passed on")
	}
	if r.m.Active() {
		t.Error("the browser stays open")
	}
	r.refused("web_save", map[string]any{"id": "../x"}, "")
}

func TestOpenChecksTheAddressBeforeTheBrowserMoves(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	for _, args := range []map[string]any{
		{"url": "https://evil.test/"},
		{"url": "https://user:pw@shop.test/"},
		{"url": "file:///etc/passwd"},
		{"path": "/a", "url": base + "/b"},
		{"service": "nope"},
	} {
		r.refused("web_open", args, "")
	}
	if len(r.be.opens) != 0 || r.m.Active() {
		t.Fatalf("a refused address started the browser: %+v", r.be.opens)
	}
	r.ok("web_open", nil)
	r.refused("web_open", map[string]any{"url": "https://evil.test/"}, "")
	if r.br.did("open https://evil.test/") {
		t.Error("the browser moved to a host that is not listed")
	}
}

func TestObserveModeAllowsOnlyOpenListReadAndScreenshot(t *testing.T) {
	r := newRig(t, mcpkit.Guard{Observe: true, Reason: "the environment is read only"})
	r.ok("web_open", map[string]any{"path": "/orders"})
	if !r.be.opens[0].ReadOnly {
		t.Fatal("the browser must be started read only while the server observes")
	}
	r.ok("web_list_elements", nil)
	r.ok("web_read", map[string]any{"ref": "e1"})
	r.ok("web_screenshot", nil)
	r.ok("web_open", map[string]any{"path": "/orders/1"})
	before := len(r.br.calls)
	refused := map[string]any{
		"web_click": map[string]any{"ref": "e4"}, "web_fill": map[string]any{"ref": "e2", "text": "x"},
		"web_select": map[string]any{"ref": "e6", "label": "Late"}, "web_set_checked": map[string]any{"ref": "e5", "checked": true},
		"web_press": map[string]any{"key": "Enter"}, "web_hover": map[string]any{"ref": "e4"},
		"web_drag": map[string]any{"fromRef": "e4", "toRef": "e5"}, "web_assert": map[string]any{"ref": "e1"},
		"web_save": map[string]any{"id": "SHOP-1-01"},
	}
	for name, args := range refused {
		r.refused(name, args, "only observes")
	}
	// Nothing but the listing of the checks above reached the browser.
	if len(r.br.calls) != before || len(r.be.saved) != 0 {
		t.Fatalf("a refused tool reached the browser: %v", r.br.calls[before:])
	}
	// The only tools that Write are the ones the matrix refuses.
	for name, tool := range r.tools {
		_, isRefused := refused[name]
		if tool.Write != isRefused && name != "web_close" {
			t.Errorf("%s: Write=%v", name, tool.Write)
		}
	}
	r.ok("web_close", nil)
	if r.m.Active() {
		t.Error("web_close must still end the browser while the server observes")
	}
}

func TestPasswordFieldRefusesPlainTextAndTakesSecretEnv(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	r.ok("web_open", nil)
	r.refused("web_fill", map[string]any{"ref": "e3", "text": "plain"}, "secretEnv")
	if r.br.did("fill e3") {
		t.Fatal("plain text reached the password field")
	}
	r.refused("web_fill", map[string]any{"ref": "e2", "secretEnv": secretName}, "not a password field")
	r.refused("web_fill", map[string]any{"ref": "e3", "secretEnv": "NOPE_NOT_SET"}, "not set")
	r.refused("web_fill", map[string]any{"ref": "e3", "secretEnv": "bad name"}, "")
	r.refused("web_fill", map[string]any{"ref": "e3", "text": "a", "secretEnv": secretName}, "not both")
	out := r.ok("web_fill", map[string]any{"ref": "e3", "secretEnv": secretName})
	if strings.Contains(out, pwdSecret) || !strings.Contains(out, secretName) {
		t.Fatalf("answer: %s", out)
	}
	// The value is typed, once, and a second variable is refused.
	if !r.br.did("fill e3 " + pwdSecret) {
		t.Error("the value was not typed")
	}
	r.env["OTHER"] = "another-secret"
	r.refused("web_fill", map[string]any{"ref": "e3", "secretEnv": "OTHER"}, "one secret")
	r.refused("web_read", map[string]any{"ref": "e3"}, "never read")
	r.refused("web_assert", map[string]any{"ref": "e3"}, "never read")
	// A text that holds the secret is refused too.
	r.refused("web_fill", map[string]any{"ref": "e2", "text": "x " + pwdSecret}, "secret value")
}

func TestSecretEnvNeverLeaksIntoAnyOutput(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	r.ok("web_open", nil)
	var seen []string
	collect := func(s string) { seen = append(seen, s) }
	collect(r.ok("web_fill", map[string]any{"ref": "e3", "secretEnv": secretName}))
	// The page echoes the secret everywhere that a text can come from.
	r.br.mu.Lock()
	r.br.list = pagelist.Build(base+"/", "Hello "+pwdSecret, []pagelist.Item{
		{Index: 0, Role: "heading", Name: "Hi " + pwdSecret, Value: pwdSecret, Acts: true},
		{Index: 1, Role: "textbox", Name: "Password", Secret: true, Acts: true},
	})
	r.br.shows["e1"] = "your password is " + pwdSecret
	r.br.mu.Unlock()
	collect(r.ok("web_list_elements", nil))
	collect(r.ok("web_list_elements", map[string]any{"format": "json"}))
	collect(r.ok("web_read", map[string]any{"ref": "e1"}))
	collect(r.ok("web_assert", map[string]any{"ref": "e1"}))
	collect(r.ok("web_open", map[string]any{"url": base + "/?q=" + pwdSecret}))
	for _, name := range []string{"web_click", "web_hover"} {
		collect(r.ok(name, map[string]any{"ref": "e1"}))
	}
	for _, text := range []string{
		mustText(r, "web_fill", map[string]any{"ref": "e1", "text": "x"}),
		mustText(r, "web_select", map[string]any{"ref": "e9", "label": pwdSecret}),
		mustText(r, "web_fill", map[string]any{"ref": "e2", "text": pwdSecret}),
		mustText(r, "web_save", map[string]any{"id": "bad id " + pwdSecret}),
	} {
		collect(text)
	}
	for i, s := range seen {
		if strings.Contains(s, pwdSecret) {
			t.Errorf("output %d leaks the secret: %s", i, s)
		}
	}
	if !strings.Contains(strings.Join(seen, "\n"), "redacted") && !strings.Contains(strings.Join(seen, "\n"), "[") {
		t.Errorf("nothing was scrubbed: %v", seen)
	}
}

func mustText(r *rig, name string, args any) string {
	text, _ := r.call(name, args)
	return text
}

func TestScreenshotIsRefusedWhileAKnownSecretIsOnThePage(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	r.ok("web_open", nil)
	r.ok("web_fill", map[string]any{"ref": "e3", "secretEnv": secretName})
	r.ok("web_screenshot", nil)
	r.br.mu.Lock()
	r.br.list = pagelist.Build(base+"/", "Shop", []pagelist.Item{{Index: 0, Role: "heading", Name: "token " + pwdSecret, Acts: true}})
	r.br.mu.Unlock()
	r.br.calls = nil
	r.refused("web_screenshot", map[string]any{"saveTo": "x.jpg"}, "secret")
	if r.br.did("screenshot") {
		t.Error("a picture was taken of a page that shows a secret")
	}
	if _, err := os.Stat(filepath.Join(r.out.Root(), "x.jpg")); err == nil {
		t.Error("the picture was saved")
	}
	r.refused("web_screenshot", map[string]any{"saveTo": "../x.jpg"}, "saveTo")
	r.refused("web_screenshot", map[string]any{"saveTo": "x.png"}, "saveTo")
}

func TestCloseNeedsDiscardForAnUnsavedRecording(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	r.text("no browser was open", "web_close", nil)
	r.ok("web_open", nil)
	// Moves alone are nothing to lose.
	r.ok("web_open", map[string]any{"path": "/orders"})
	r.ok("web_list_elements", nil)
	r.ok("web_click", map[string]any{"ref": "e4"})
	r.refused("web_close", nil, "web_save")
	if !r.m.Active() || r.br.closed != 0 {
		t.Fatal("the browser closed with unsaved steps")
	}
	r.ok("web_close", map[string]any{"discard": true})
	if r.m.Active() || r.br.closed != 1 {
		t.Fatalf("discard did not close: closed=%d", r.br.closed)
	}
	// After a move only, close needs no flag.
	r.ok("web_open", nil)
	r.ok("web_close", nil)
}

func (r *rig) text(want, name string, args any) {
	r.t.Helper()
	if got := r.ok(name, args); !strings.Contains(got, want) {
		r.t.Fatalf("%s = %q, want %q", name, got, want)
	}
}

func TestInvalidateListMakesTheNextActionListFirst(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	r.ok("web_open", nil)
	r.ok("web_click", map[string]any{"ref": "e4"})
	r.m.InvalidateList()
	r.refused("web_click", map[string]any{"ref": "e4"}, "web_list_elements")
	r.refused("web_fill", map[string]any{"ref": "e2", "text": "x"}, "web_list_elements")
	r.ok("web_list_elements", nil)
	r.ok("web_click", map[string]any{"ref": "e4"})
	r.refused("web_click", map[string]any{"ref": "e99"}, "not in the last list")
	r.refused("web_click", map[string]any{"ref": "button"}, "not a ref")
}

func TestBrowserFailureIsAnErrorTheModelCanRead(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	r.ok("web_open", nil)
	r.br.failOn = "click e4"
	r.refused("web_click", map[string]any{"ref": "e4"}, "page said no")
	r.refused("web_click", map[string]any{"ref": "e4", "extra": 1}, "unknown argument")
	r.refused("web_press", map[string]any{"key": "Tab"}, "Enter or Escape")
	r.refused("web_set_checked", map[string]any{"ref": "e5"}, "checked")
}

func TestJourneyHelpers(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	if _, ok := r.m.Mark(); ok || r.m.EventsFrom(0) != nil || len(r.m.Vars()) != 0 {
		t.Fatal("helpers must be empty without a window")
	}
	if _, err := r.m.Location(context.Background()); err == nil {
		t.Fatal("location without a window")
	}
	r.ok("web_open", nil)
	r.ok("web_list_elements", nil)
	mark, ok := r.m.Mark()
	if !ok || mark != 1 {
		t.Fatalf("mark = %d %v", mark, ok)
	}
	r.ok("web_click", map[string]any{"ref": "e4"})
	ev := r.m.EventsFrom(mark)
	if len(ev) != 1 || ev[0].Kind != record.KindClick {
		t.Fatalf("events from the mark: %+v", ev)
	}
	ev[0].Kind = "changed"
	if r.m.EventsFrom(mark)[0].Kind != record.KindClick {
		t.Error("EventsFrom gave the events themselves, not a copy")
	}
	if loc, err := r.m.Location(context.Background()); err != nil || loc != base+"/" {
		t.Errorf("location %q %v", loc, err)
	}
	if r.m.Info().Service != "shop" || r.m.Vars()["run.id"] != "r1" {
		t.Errorf("info %+v vars %v", r.m.Info(), r.m.Vars())
	}
	if err := r.m.Close(context.Background()); err != nil || r.m.Active() || r.br.closed != 1 {
		t.Fatalf("close: %v active=%v closed=%d", err, r.m.Active(), r.br.closed)
	}
	if err := r.m.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentCallsAreSerialized(t *testing.T) {
	r := newRig(t, mcpkit.Guard{})
	r.ok("web_open", nil)
	r.ok("web_list_elements", nil)
	var wg sync.WaitGroup
	for i := range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch i % 6 {
			case 0:
				r.call("web_list_elements", nil)
			case 1:
				r.call("web_click", map[string]any{"ref": "e4"})
			case 2:
				r.call("web_read", map[string]any{"ref": "e1"})
			case 3:
				r.m.InvalidateList()
			case 4:
				r.m.Mark()
				r.m.EventsFrom(0)
			case 5:
				r.call("web_screenshot", nil)
			}
		}()
	}
	wg.Wait()
	r.call("web_close", map[string]any{"discard": true})
}

func TestOpenAndCloseAreRefusedWhileAPersonHoldsControlEvenWhenObserving(t *testing.T) {
	for _, guard := range []mcpkit.Guard{{}, {Observe: true, Reason: "the environment is read only"}} {
		b := takeover.New(nil)
		r := newRig(t, guard, func(o *webmcp.Options) { o.Baton = b })
		r.ok("web_open", map[string]any{"path": "/orders"}) // the agent drives: admitted, observing or not
		opens := len(r.be.opens)

		b.Yield("a captcha")
		for name, args := range map[string]any{"web_open": map[string]any{"path": "/orders/1"}, "web_close": map[string]any{"discard": true}} {
			tool := r.tools[name]
			raw, _ := json.Marshal(args)
			res, err := tool.Handler(context.Background(), mcp.Call{Name: name, Args: raw})
			if err != nil || !res.IsError {
				t.Fatalf("%s while a person drives: %v %+v", name, err, res)
			}
			m, _ := res.Structured.(map[string]any)
			if m["status"] != "human_in_control" || m["reason"] != "a captcha" {
				t.Errorf("%s: structured = %v", name, res.Structured)
			}
		}
		if len(r.be.opens) != opens || !r.m.Active() {
			t.Error("a refused call moved the browser")
		}
		// Reads still work.
		r.ok("web_list_elements", nil)

		b.Resume("done")
		r.ok("web_close", map[string]any{"discard": true})
		if r.m.Active() {
			t.Error("web_close did not close the browser after the hand-back")
		}
	}
}
