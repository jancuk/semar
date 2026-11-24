package mobilemcp_test

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/mobiletest"
	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/mobilemcp"
	"github.com/jancuk/semar/internal/mobilerec"
	"github.com/jancuk/semar/internal/recorder"
	"github.com/jancuk/semar/internal/redact"
	"github.com/jancuk/semar/internal/secret"
)

const recSecret = "s3cret-Pa55word"

type recSink struct{}

func (recSink) WriteAction(a domain.Action) (string, error) { return "actions/" + string(a.ID), nil }
func (recSink) AppendActivity(domain.Activity) error        { return nil }

type recFiles struct{}

func (recFiles) WriteFile(string, []byte) error { return nil }

func recNode(class string, x, y, w, h int) *mobile.Node {
	return &mobile.Node{Class: class, Enabled: true, Bounds: mobile.Rect{Left: x, Top: y, Right: x + w, Bottom: y + h}, Attrs: map[string]string{"class": class}}
}

func recButton(desc string, x, y int) *mobile.Node {
	b := recNode("android.widget.Button", x, y, 400, 100)
	b.Desc, b.Role = desc, "button"
	b.Attrs["clickable"], b.Attrs["content-desc"] = "true", desc
	return b
}

func recField(id, hint string, y int, password, focused bool) *mobile.Node {
	f := recNode("android.widget.EditText", 100, y, 800, 100)
	f.ID, f.Hint, f.Role, f.Password, f.Focused = "com.example.shop:id/"+id, hint, "textbox", password, focused
	f.Attrs["resource-id"], f.Attrs["hint"] = f.ID, hint
	if password {
		f.Attrs["password"] = "true"
	}
	return f
}

func recLabel(id, text string, y int) *mobile.Node {
	l := recNode("android.widget.TextView", 100, y, 600, 100)
	l.Text = text
	l.Attrs["text"] = text
	l.ID = "com.example.shop:id/" + id
	l.Attrs["resource-id"] = l.ID
	return l
}

func recHeading(text string) *mobile.Node {
	h := recNode("android.widget.TextView", 100, 100, 600, 80)
	h.Text, h.Role = text, "heading"
	h.Attrs["text"] = text
	return h
}

func signIn(focusPassword bool) *mobile.Tree {
	return mobiletest.Screen(recHeading("Sign in"), recField("email", "Email", 300, false, !focusPassword), recField("password", "Password", 500, true, focusPassword), recButton("Sign in", 100, 900))
}

func home() *mobile.Tree {
	return mobiletest.Screen(recHeading("Home"), recLabel("welcome", "Hi Ana", 300), recButton("Sign out", 100, 1100))
}

// fakeBackend is a RecordBackend over the fake driver: what the command builds from the studio.
type fakeBackend struct {
	t    *testing.T
	repo string
	d    *mobiletest.FakeDriver

	mu       sync.Mutex
	opens    int
	open     int
	reqs     []mobilemcp.RecordRequest
	verified int
	ran      []string
	runs     func() mobilemcp.RunResult
}

func newBackend(t *testing.T) *fakeBackend {
	b := &fakeBackend{t: t, repo: t.TempDir(), d: mobiletest.NewFakeDriver()}
	b.d.Show(signIn(false))
	b.d.OnLaunch = func() { b.d.Show(signIn(false)) }
	b.d.OnReset = func(string) { b.d.Show(signIn(false)) }
	b.d.OnTouch = func(g mobile.Gesture) {
		if g.From.Y >= 900 && g.From.Y < 1000 {
			b.d.Show(home())
		}
	}
	return b
}

func (b *fakeBackend) handles() int { b.mu.Lock(); defer b.mu.Unlock(); return b.open }

func (b *fakeBackend) Start(_ context.Context, r mobilemcp.RecordRequest) (*mobilerec.Session, error) {
	b.mu.Lock()
	b.reqs = append(b.reqs, r)
	b.mu.Unlock()
	author := domain.ProviderRef{Instance: "mcp", Model: "agent", Harness: "mcp"}
	lookup := func(name string) (string, bool) {
		if name == "SHOP_PASSWORD" {
			return recSecret, true
		}
		return "", false
	}
	factory := func(_ context.Context, look mobilerec.LookupEnv) (mobilerec.Handle, error) {
		b.mu.Lock()
		b.opens++
		b.open++
		b.mu.Unlock()
		pol, err := effects.New(config.Environment{Hosts: []string{"shop.example.com"}, Methods: []string{"GET"}, Effects: config.Effects{Mode: "test_data"}})
		if err != nil {
			b.t.Fatal(err)
		}
		secrets := secret.NewEnv(look)
		red, err := redact.New(config.Redact{}, secrets.Loaded, []byte("0123456789abcdef0123456789abcdef"))
		if err != nil {
			b.t.Fatal(err)
		}
		rec := recorder.New(recSink{}, red, func(string, *url.URL) effects.Class { return effects.Read }, "run-1")
		platform := &mobiletest.FakePlatform{D: b.d}
		eng := mobile.New(mobile.Options{
			Policy: pol, Recorder: rec, Files: recFiles{}, Secrets: secrets, Platform: platform, Run: "run-1",
			Services: map[string]mobile.Service{"shop-app": {}},
			Sleep:    func(ctx context.Context, _ time.Duration) error { return ctx.Err() },
		})
		return mobilerec.Handle{Engine: eng, Platform: platform, Close: func(ctx context.Context) error {
			b.mu.Lock()
			b.open--
			b.mu.Unlock()
			return eng.Close(ctx)
		}}, nil
	}
	return mobilerec.New(mobilerec.Options{
		Repo: b.repo, Kind: r.Kind, ID: r.ID, Ticket: r.Ticket, Title: r.Title, Service: "shop-app", App: "shop-app",
		Open: factory, Lookup: lookup, Author: &author, Reset: r.Reset, ConfirmClear: r.ConfirmClear,
		Sleep: func(ctx context.Context, _ time.Duration) error { return ctx.Err() },
	})
}

func (b *fakeBackend) Verify(context.Context, *mobilerec.Session, *mobilerec.Saved) (mobilemcp.VerifyResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.verified++
	if b.open != 0 {
		b.t.Errorf("the verify ran while %d device sessions were open", b.open)
	}
	return mobilemcp.VerifyResult{Status: "passed", Message: "Replayed once: passed."}, nil
}

func (b *fakeBackend) RunScenario(_ context.Context, id string) (mobilemcp.RunResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ran = append(b.ran, id)
	if b.runs != nil {
		return b.runs(), nil
	}
	return mobilemcp.RunResult{Outcome: "passed"}, nil
}

func (b *fakeBackend) List(context.Context) (mobilemcp.Catalog, error) {
	return mobilemcp.Catalog{Services: []string{"shop-app"}}, nil
}

func (b *fakeBackend) read(rel string) string {
	b.t.Helper()
	data, err := os.ReadFile(filepath.Join(b.repo, filepath.FromSlash(rel)))
	if err != nil {
		b.t.Fatal(err)
	}
	return string(data)
}

func (b *fakeBackend) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(b.repo, filepath.FromSlash(rel)))
	return err == nil
}

// recFixture is the fixture of the live tools with the record tools added.
func recFixture(t *testing.T, opts ...fixOpt) (*fixture, *fakeBackend) {
	t.Helper()
	f := newFixture(t, opts...)
	b := newBackend(t)
	f.m.SetRecordBackend(b)
	for _, tool := range f.m.RecordTools() {
		f.tools[tool.Name] = tool
	}
	return f, b
}

// ref finds the ref of the line of the screen that has the text.
func ref(t *testing.T, text, want string) string {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, want) && strings.HasPrefix(line, "e") {
			return strings.Fields(line)[0]
		}
	}
	t.Fatalf("no element with %q in:\n%s", want, text)
	return ""
}

func TestRecordToolsAreRegisteredOnlyWithABackendAndPassTheSchemaCheck(t *testing.T) {
	bare := newFixture(t)
	if got := bare.m.RecordTools(); len(got) != 0 {
		t.Errorf("no backend, but %d record tools", len(got))
	}
	f, _ := recFixture(t)
	var names []string
	for _, tool := range f.m.RecordTools() {
		names = append(names, tool.Name)
		if err := mcp.ValidatePortableSchema(tool.Input); err != nil {
			t.Errorf("%s: %v", tool.Name, err)
		}
		if tool.Surface != "mobile" || tool.Annotations.ReadOnly == nil || tool.Write == *tool.Annotations.ReadOnly {
			t.Errorf("%s: surface %q, write %v", tool.Name, tool.Surface, tool.Write)
		}
	}
	want := []string{"semar_list", "semar_record_start", "semar_record_assert", "semar_record_capture", "semar_record_screen",
		"semar_record_steps", "semar_record_delete_step", "semar_record_save", "semar_record_undo", "semar_record_stop", "semar_run_scenario"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("tools = %v", names)
	}
	for _, n := range names {
		if n == "semar_record_use_flow" {
			t.Error("semar_record_use_flow is dropped: semar_run_flow takes its place")
		}
	}
}

func TestRecordASignInAndSaveItWithAVerify(t *testing.T) {
	f, b := recFixture(t)
	// The live tools open the device first, as an agent that looked around would.
	f.ok("mobile_get_screen_size", "")
	if f.opens.Load() != 1 {
		t.Fatalf("live opens = %d", f.opens.Load())
	}

	res := f.ok("semar_record_start", `{"kind":"scenario","id":"AGENT-1","ticket":"SHOP-1"}`)
	if f.closes.Load() != 1 || b.handles() != 1 {
		t.Errorf("the live device was not given away for the recording: closes=%d recording handles=%d", f.closes.Load(), b.handles())
	}
	screen := textOf(res)
	if !f.m.Recording() || f.m.ActiveSession() == nil {
		t.Fatal("the Manager does not say a recording goes on")
	}
	if req := b.reqs[0]; req.Serial != "emulator-5554" || req.Kind != mobilerec.KindScenario || req.Ticket != "SHOP-1" {
		t.Errorf("request = %+v", req)
	}
	f.fails("semar_record_start", `{"kind":"flow","id":"again"}`, "is going on")

	// The live device is not reopened by a tool that would use it.
	if got := f.fails("mobile_get_device_logs", "", "while recording"); strings.Contains(got, "opened") {
		t.Error(got)
	}
	if f.opens.Load() != 1 {
		t.Errorf("a tool opened the live device during the recording: %d", f.opens.Load())
	}

	// A plain text into the password field is refused with a hint to use secretEnv.
	pw := ref(t, screen, "Password")
	f.fails("mobile_type_keys", `{"text":"hunter2","ref":"`+pw+`"}`, "secretEnv")
	res = f.ok("mobile_type_keys", `{"secretEnv":"SHOP_PASSWORD","ref":"`+pw+`"}`)
	if got := textOf(res); strings.Contains(got, recSecret) {
		t.Fatalf("the secret is in the result: %s", got)
	}
	if !strings.Contains(textOf(res), "recorded step") {
		t.Errorf("the result does not say what was recorded: %s", textOf(res))
	}
	if typed := b.d.Typed(); len(typed) != 1 || typed[0] != recSecret {
		t.Errorf("the device was typed %v", typed)
	}

	res = f.ok("mobile_click_on_screen_at_coordinates", `{"ref":"`+ref(t, textOf(res), `button  "Sign in"`)+`"}`)
	home := textOf(res)
	f.ok("semar_record_screen", `{"name":"Home"}`)
	res = f.ok("mobile_list_elements_on_screen", "")
	f.ok("semar_record_assert", `{"ref":"`+ref(t, textOf(res), "Hi Ana")+`","kind":"equals","value":"Hi Ana"}`)
	_ = home

	var steps struct {
		Steps   []mobilerec.Step `json:"steps"`
		Unsaved bool             `json:"unsaved"`
	}
	decode(t, f.ok("semar_record_steps", ""), &steps)
	if len(steps.Steps) != 4 || !steps.Unsaved {
		t.Fatalf("steps = %+v", steps)
	}

	f.fails("semar_record_stop", "", "not saved")
	var saved struct {
		Files  []string `json:"files"`
		Checks int      `json:"checks"`
		Verify struct {
			Status string `json:"status"`
		} `json:"verify"`
	}
	decode(t, f.ok("semar_record_save", ""), &saved)
	if saved.Verify.Status != "passed" || b.verified != 1 || saved.Checks != 1 {
		t.Errorf("save = %+v, verified %d", saved, b.verified)
	}
	if !f.m.Recording() || b.handles() != 1 {
		t.Errorf("the session must stay open after the verify: recording=%v handles=%d", f.m.Recording(), b.handles())
	}
	feature := b.read(".semar/scenarios/SHOP-1/AGENT-1.feature")
	if !strings.Contains(feature, "# baseline, by model") {
		t.Errorf("feature:\n%s", feature)
	}
	if !b.exists(".semar/screens/shop-app.yaml") {
		t.Error("no screens file")
	}
	for _, rel := range saved.Files {
		if strings.Contains(b.read(rel), recSecret) {
			t.Errorf("the secret is in %s", rel)
		}
	}
	if !strings.Contains(strings.Join(saved.Files, " "), "AGENT-1") {
		t.Errorf("files = %v", saved.Files)
	}

	// The recording goes on after the verify, and a stop after a save needs no discard.
	f.ok("semar_record_undo", "")
	if b.exists(".semar/screens/shop-app.yaml") {
		t.Error("undo left the screens file")
	}
	f.ok("semar_record_stop", `{"discard":true}`)
	if f.m.Recording() || b.handles() != 0 {
		t.Errorf("the recording was not ended: recording=%v handles=%d", f.m.Recording(), b.handles())
	}
	// The next live tool reopens the device.
	f.ok("mobile_get_screen_size", "")
	if f.opens.Load() != 2 {
		t.Errorf("live opens = %d, want 2", f.opens.Load())
	}
}

func TestRecordRoutingOfCoordinatesAndKeys(t *testing.T) {
	f, b := recFixture(t)
	f.ok("semar_record_start", `{"kind":"scenario","id":"S-1"}`)

	// A point on the button is found again as the button, and it is a step.
	res := f.ok("mobile_click_on_screen_at_coordinates", `{"x":300,"y":950}`)
	if !strings.Contains(textOf(res), "recorded step") || !strings.Contains(textOf(res), "Hi Ana") {
		t.Errorf("tap by point: %s", textOf(res))
	}
	// A point on nothing is an error that is no step.
	if got := f.fails("mobile_click_on_screen_at_coordinates", `{"x":1,"y":1}`, "a ref"); !strings.Contains(got, "mobile_list_elements_on_screen") {
		t.Errorf("the error does not point to the list: %s", got)
	}
	// Keys: the recordable ones are steps, the others are refused.
	f.ok("mobile_press_button", `{"button":"back"}`)
	f.fails("mobile_press_button", `{"button":"VOLUME_UP"}`, "cannot be recorded")
	// A swipe with no point is a step of the screen.
	res = f.ok("mobile_swipe_on_screen", `{"direction":"up"}`)
	if !strings.Contains(textOf(res), "distance is not stored") {
		t.Errorf("swipe: %s", textOf(res))
	}
	res = f.ok("mobile_long_press_on_screen_at_coordinates", `{"x":300,"y":1150,"duration":900}`)
	if !strings.Contains(textOf(res), "700 ms") {
		t.Errorf("long press: %s", textOf(res))
	}
	if keys := b.d.Keys(); len(keys) != 1 || keys[0] != mobile.KeyBack {
		t.Errorf("keys = %v", keys)
	}

	// Non-recordable tools are refused, and so are the ones that need a second session on the device.
	for name, args := range map[string]string{
		"mobile_set_orientation":        `{"orientation":"landscape"}`,
		"mobile_fold_device":            `{"posture":"folded"}`,
		"mobile_set_location":           `{"latitude":1,"longitude":2}`,
		"mobile_clipboard":              `{"text":"x"}`,
		"mobile_terminate_app":          `{"packageName":"com.example.shop"}`,
		"mobile_install_app":            `{"path":"a.apk"}`,
		"mobile_uninstall_app":          `{"packageName":"com.example.shop","confirm":true}`,
		"mobile_clear_app_data":         `{"packageName":"com.example.shop","confirm":true}`,
		"mobile_use_device":             `{"device":"emulator-5556"}`,
		"mobile_launch_app":             `{"packageName":"com.example.shop"}`,
		"mobile_start_screen_recording": `{}`,
	} {
		if got := f.fails(name, args, ""); !strings.Contains(got, "recording") {
			t.Errorf("%s: %s", name, got)
		}
	}
	f.ok("mobile_get_screen_size", "")
	if f.opens.Load() != 0 {
		t.Errorf("a refused tool opened the live device: %d", f.opens.Load())
	}

	// A batch routes each step through the recording.
	before := len(f.m.ActiveSession().Steps())
	res = f.ok("mobile_batch_commands", `{"steps":[{"name":"mobile_press_button","arguments":{"button":"HOME"}},{"name":"mobile_press_button","arguments":{"button":"TAB"}}],"listElementsAtEnd":true}`)
	if got := len(f.m.ActiveSession().Steps()); got != before+2 {
		t.Errorf("steps %d -> %d\n%s", before, got, textOf(res))
	}
	if !strings.Contains(textOf(res), "elements on the screen:") {
		t.Errorf("batch: %s", textOf(res))
	}
}

func TestRecordStaleRefComesBackWithAFreshListing(t *testing.T) {
	f, b := recFixture(t)
	res := f.ok("semar_record_start", `{"kind":"scenario","id":"S-2"}`)
	button := ref(t, textOf(res), `button  "Sign in"`)
	// The app changes the screen on its own, between the listing and the tap.
	b.d.Show(home())
	got := f.fails("mobile_click_on_screen_at_coordinates", `{"ref":"`+button+`"}`, "the screen changed")
	if !strings.Contains(got, "Hi Ana") {
		t.Errorf("the error does not show the screen now:\n%s", got)
	}
	var steps struct {
		Steps []mobilerec.Step `json:"steps"`
	}
	decode(t, f.ok("semar_record_steps", ""), &steps)
	if len(steps.Steps) != 1 {
		t.Errorf("a stale tap was recorded: %+v", steps.Steps)
	}
	// The refs of the fresh listing are valid.
	f.ok("mobile_click_on_screen_at_coordinates", `{"ref":"`+ref(t, got, "Sign out")+`"}`)
}

func TestRecordStopNeedsDiscardForUnsavedStepsAndCloseCleansUp(t *testing.T) {
	f, b := recFixture(t)
	res := f.ok("semar_record_start", `{"kind":"flow","id":"login"}`)
	f.ok("mobile_click_on_screen_at_coordinates", `{"ref":"`+ref(t, textOf(res), `button  "Sign in"`)+`"}`)
	f.fails("semar_record_stop", "", "discard")
	if !f.m.Recording() {
		t.Fatal("a refused stop ended the recording")
	}
	if err := f.m.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.m.Recording() || b.handles() != 0 {
		t.Errorf("Close left the recording: recording=%v handles=%d", f.m.Recording(), b.handles())
	}
	f.fails("semar_record_steps", "", "no recording")
}

func TestRunScenarioNeedsNoRecordingAndFreesTheLiveDevice(t *testing.T) {
	f, b := recFixture(t)
	b.runs = func() mobilemcp.RunResult {
		return mobilemcp.RunResult{Outcome: "failed", Reasons: []string{"step check: Hi Bob is not Hi Ana"}, Report: ".semar/runs/r1/report.html"}
	}
	f.ok("mobile_get_screen_size", "")
	var got mobilemcp.RunResult
	decode(t, f.ok("semar_run_scenario", `{"id":"S-1"}`), &got)
	if got.Outcome != "failed" || len(got.Reasons) != 1 || got.Report == "" {
		t.Errorf("result = %+v", got)
	}
	if f.closes.Load() != 1 || len(b.ran) != 1 || b.ran[0] != "S-1" {
		t.Errorf("closes=%d ran=%v", f.closes.Load(), b.ran)
	}
	f.ok("semar_record_start", `{"kind":"flow","id":"x"}`)
	f.fails("semar_run_scenario", `{"id":"S-1"}`, "recording is going on")
}

func TestRecordToolsAreRefusedInObserveMode(t *testing.T) {
	f, _ := recFixture(t, func(o *mobilemcp.Options, _ *fixture) {
		o.Guard = mcpkit.Guard{Observe: true, Reason: "--read-only was given"}
	})
	for _, name := range []string{"semar_record_start", "semar_record_assert", "semar_record_save", "semar_record_undo", "semar_record_stop", "semar_run_scenario"} {
		f.fails(name, `{"kind":"flow","id":"x","name":"n","n":1}`, "only observes")
	}
	f.ok("semar_list", "")
	var catalog struct {
		Catalog mobilemcp.Catalog `json:"catalog"`
	}
	decode(t, f.ok("semar_list", ""), &catalog)
	if len(catalog.Catalog.Services) != 1 {
		t.Errorf("catalog = %+v", catalog)
	}
}

func TestRecordedSecretIsScrubbedFromEveryResult(t *testing.T) {
	var calls atomic.Int32
	f, _ := recFixture(t, func(o *mobilemcp.Options, _ *fixture) { calls.Add(1) })
	res := f.ok("semar_record_start", `{"kind":"scenario","id":"S-3"}`)
	f.ok("mobile_type_keys", `{"secretEnv":"SHOP_PASSWORD","ref":"`+ref(t, textOf(res), "Password")+`"}`)
	raw, _ := json.Marshal(f.call("mobile_list_elements_on_screen", `{"format":"json"}`))
	if strings.Contains(string(raw), recSecret) {
		t.Errorf("the secret is in the listing: %s", raw)
	}
	f.fails("mobile_type_keys", `{"secretEnv":"NOT_SET_ANYWHERE","ref":"e1"}`, "is not set")
}

func TestRecordAllowsReadingTheClipboardButNotSettingIt(t *testing.T) {
	f, b := recFixture(t)
	b.d.Clip = "copied during the recording"
	f.ok("semar_record_start", `{"kind":"scenario","id":"S-1"}`)
	before := len(f.m.ActiveSession().Steps())
	if got := textOf(f.ok("mobile_clipboard", "")); !strings.Contains(got, "copied during the recording") {
		t.Errorf("read = %s", got)
	}
	if got := len(f.m.ActiveSession().Steps()); got != before {
		t.Errorf("a clipboard read is a step: %d -> %d", before, got)
	}
	if got := f.fails("mobile_clipboard", `{"text":"x"}`, ""); !strings.Contains(got, "recording") {
		t.Errorf("a write in a recording: %s", got)
	}
	if f.opens.Load() != 0 {
		t.Errorf("a clipboard read opened the live device: %d", f.opens.Load())
	}
}

func TestRecordStartNamesTheClientAsTheAuthor(t *testing.T) {
	for _, tc := range []struct{ client, model string }{{"claude-code", "claude-code"}, {"", "mcp"}} {
		f, b := recFixture(t)
		tool := f.tools["semar_record_start"]
		res, err := tool.Handler(context.Background(), mcp.Call{
			Name: tool.Name, Args: json.RawMessage(`{"kind":"scenario","id":"S-1"}`), Client: mcp.ClientInfo{Name: tc.client},
			Progress: func(float64, float64, string) {},
		})
		if err != nil || res.IsError {
			t.Fatalf("%v %s", err, textOf(res))
		}
		if got := b.reqs[0].Author; got != (domain.ProviderRef{Instance: "agent", Harness: "mcp", Model: tc.model}) {
			t.Errorf("client %q: author = %+v", tc.client, got)
		}
	}
}

func TestAnEmptyClipboardReadWhileRecordingSaysWhy(t *testing.T) {
	f, _ := recFixture(t)
	f.ok("semar_record_start", `{"kind":"scenario","id":"S-1"}`)
	got := textOf(f.ok("mobile_clipboard", ""))
	if !strings.Contains(got, `"note"`) || !strings.Contains(got, "Android 10") {
		t.Errorf("an empty read = %s", got)
	}
}

func TestCaptureOfALabelWithOnlyItsTextTellsAnAgentWhatToDo(t *testing.T) {
	f, b := recFixture(t)
	f.ok("semar_record_start", `{"kind":"scenario","id":"S-1"}`)
	label := recNode("android.widget.TextView", 100, 300, 600, 100)
	label.Text = "Reset code 884213"
	label.Attrs["text"] = label.Text
	b.d.Show(mobiletest.Screen(recHeading("Reset"), label))
	res := f.ok("mobile_list_elements_on_screen", "")
	got := f.fails("semar_record_capture", `{"ref":"`+ref(t, textOf(res), "Reset code")+`","name":"code"}`, "")
	if !strings.Contains(got, "Semantics(identifier") || strings.Contains(got, "N.K") {
		t.Errorf("the message = %s", got)
	}
}

func TestChecksDoNotRepeatTheListingAndRefsStayValid(t *testing.T) {
	f, _ := recFixture(t)
	res := f.ok("semar_record_start", `{"kind":"scenario","id":"S-1"}`)
	listing := textOf(f.ok("mobile_list_elements_on_screen", ""))
	signInRef := ref(t, listing, `button  "Sign in"`)
	emailRef := ref(t, listing, "Email")
	_ = res

	got := textOf(f.ok("semar_record_assert", `{"ref":"`+emailRef+`","kind":"exists"}`))
	if !strings.Contains(got, "recorded step") || !strings.Contains(got, "stay valid") || strings.Contains(got, "Password") {
		t.Errorf("assert answer:\n%s", got)
	}
	got = textOf(f.ok("semar_record_screen", `{"name":"Login"}`))
	if !strings.Contains(got, "the screen is called Login") || !strings.Contains(got, "stay valid") || strings.Contains(got, "Password") {
		t.Errorf("screen answer:\n%s", got)
	}
	// The refs of the earlier listing still work: the tap is recorded and the screen did not go stale.
	got = textOf(f.ok("mobile_click_on_screen_at_coordinates", `{"ref":"`+signInRef+`"}`))
	if !strings.Contains(got, "recorded step") {
		t.Errorf("tap answer:\n%s", got)
	}
}

func TestListingWaitsForARecordedWebViewToBeFilled(t *testing.T) {
	sl := &sleeps{}
	f, b := recFixture(t, sl.opt)
	f.ok("semar_record_start", `{"kind":"scenario","id":"S-1"}`)
	empty := loadTree(t, "webview_empty.xml")
	empty.Finish()
	loaded := loadTree(t, "webview_loaded.xml")
	loaded.Finish()
	// The script is indexed by the reads so far: the next three reads are empty, empty, loaded.
	script := make([]*mobile.Tree, b.d.SourceCalls(), b.d.SourceCalls()+3)
	for i := range script {
		script[i] = empty
	}
	b.d.Sources = append(script, empty, empty, loaded)
	got := textOf(f.ok("mobile_list_elements_on_screen", ""))
	if !strings.Contains(got, "Continue") {
		t.Errorf("the listing is not the loaded one:\n%s", got)
	}
	// A read of the session settles on two equal readings, so the two empty ones are one Refresh.
	if n, sum := sl.total(); n != 1 || sum != 400*time.Millisecond {
		t.Errorf("%d waits of %v in all, want one of 400 ms", n, sum)
	}
}
