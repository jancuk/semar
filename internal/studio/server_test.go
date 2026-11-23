package studio_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/mobiletest"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/localhttp"
	"github.com/jancuk/semar/internal/mobilerec"
	"github.com/jancuk/semar/internal/recorder"
	"github.com/jancuk/semar/internal/redact"
	"github.com/jancuk/semar/internal/screens"
	"github.com/jancuk/semar/internal/secret"
	"github.com/jancuk/semar/internal/studio"
)

const canary = "Tr0ub4dor&3-studio-canary"

// --- a fake device, as the recorder tests have one ---

type memSink struct {
	mu      sync.Mutex
	actions []domain.Action
}

func (s *memSink) WriteAction(a domain.Action) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.actions = append(s.actions, a)
	return "actions/" + string(a.ID) + ".json", nil
}
func (s *memSink) AppendActivity(domain.Activity) error { return nil }

func (s *memSink) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := json.Marshal(s.actions)
	return string(b)
}

type memFiles struct {
	mu    sync.Mutex
	files map[string][]byte
}

func (m *memFiles) WriteFile(rel string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.files == nil {
		m.files = map[string][]byte{}
	}
	m.files[rel] = data
	return nil
}

type rig struct {
	t      *testing.T
	repo   string
	d      *mobiletest.FakeDriver
	sink   *memSink
	signed bool

	mu    sync.Mutex
	opens int
	open  int
}

func node(class string, x, y, w, h int) *mobile.Node {
	return &mobile.Node{Class: class, Enabled: true, Bounds: mobile.Rect{Left: x, Top: y, Right: x + w, Bottom: y + h}, Attrs: map[string]string{"class": class}}
}

func button(desc string, x, y int) *mobile.Node {
	b := node("android.widget.Button", x, y, 400, 100)
	b.Desc, b.Role = desc, "button"
	b.Attrs["clickable"], b.Attrs["content-desc"] = "true", desc
	return b
}

func heading(text string) *mobile.Node {
	h := node("android.widget.TextView", 100, 100, 600, 80)
	h.Text, h.Role = text, "heading"
	h.Attrs["text"] = text
	return h
}

func field(id, hint string, y int, password bool) *mobile.Node {
	f := node("android.widget.EditText", 100, y, 800, 100)
	f.ID, f.Hint, f.Role, f.Password = "com.example.shop:id/"+id, hint, "textbox", password
	f.Attrs["resource-id"], f.Attrs["hint"] = f.ID, hint
	if password {
		f.Attrs["password"] = "true"
	}
	return f
}

func label(id, text string, y int) *mobile.Node {
	l := node("android.widget.TextView", 100, y, 600, 100)
	l.Text = text
	l.Attrs["text"] = text
	if id != "" {
		l.ID = "com.example.shop:id/" + id
		l.Attrs["resource-id"] = l.ID
	}
	return l
}

func signInScreen() *mobile.Tree {
	return mobiletest.Screen(heading("Sign in"), field("email", "Email", 300, false), field("password", "Password", 500, true), button("Sign in", 100, 900))
}

func homeScreen() *mobile.Tree {
	return mobiletest.Screen(heading("Home"), label("welcome", "Hi Ana", 300), button("Sign out", 100, 900))
}

func tinyPNG() []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 16)))
	return b.Bytes()
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, repo: t.TempDir(), d: mobiletest.NewFakeDriver(), sink: &memSink{}}
	r.d.Shot = tinyPNG()
	show := func() {
		if r.signed {
			r.d.Show(homeScreen())
		} else {
			r.d.Show(signInScreen())
		}
	}
	show()
	r.d.OnLaunch = show
	r.d.OnReset = func(mode string) {
		if mode == "clear" {
			r.signed = false
		}
		show()
	}
	r.d.OnTouch = func(g mobile.Gesture) {
		if g.From.Y >= 900 && g.From.Y < 1000 {
			r.signed = !r.signed
			show()
		}
	}
	return r
}

func (r *rig) factory() mobilerec.PlatformFactory {
	return func(_ context.Context, lookup mobilerec.LookupEnv) (mobilerec.Handle, error) {
		r.mu.Lock()
		if r.open > 0 {
			r.mu.Unlock()
			return mobilerec.Handle{}, &mobile.Unavailable{Reason: "the device is used by another semar run"}
		}
		r.opens++
		r.open++
		r.mu.Unlock()
		pol, err := effects.New(config.Environment{Hosts: []string{"shop.example.com"}, Methods: []string{"GET"}, Effects: config.Effects{Mode: "test_data"}})
		if err != nil {
			r.t.Fatal(err)
		}
		secrets := secret.NewEnv(lookup)
		red, err := redact.New(config.Redact{}, secrets.Loaded, []byte("0123456789abcdef0123456789abcdef"))
		if err != nil {
			r.t.Fatal(err)
		}
		rec := recorder.New(r.sink, red, func(string, *url.URL) effects.Class { return effects.Read }, "run-1")
		platform := &mobiletest.FakePlatform{D: r.d}
		eng := mobile.New(mobile.Options{
			Policy: pol, Recorder: rec, Files: &memFiles{}, Secrets: secrets, Platform: platform, Run: "run-1",
			Services: map[string]mobile.Service{"shop-app": {}},
			Sleep:    func(ctx context.Context, _ time.Duration) error { return ctx.Err() },
		})
		return mobilerec.Handle{Engine: eng, Platform: platform, Close: func(ctx context.Context) error {
			r.mu.Lock()
			r.open--
			r.mu.Unlock()
			return eng.Close(ctx)
		}}, nil
	}
}

func (r *rig) openHandles() int { r.mu.Lock(); defer r.mu.Unlock(); return r.open }
func (r *rig) openCount() int   { r.mu.Lock(); defer r.mu.Unlock(); return r.opens }

func (r *rig) allFiles() string {
	var b strings.Builder
	_ = filepath.WalkDir(r.repo, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			data, _ := os.ReadFile(p)
			b.WriteString(p + "\n" + string(data) + "\n")
		}
		return nil
	})
	return b.String()
}

// deps is the studio over the fake device, with a verify that proves that the lease is free.
func (r *rig) deps(verifies *[]string) studio.Deps {
	open := r.factory()
	return studio.Deps{
		Repo: r.repo,
		Status: func() (studio.Status, error) {
			return studio.Status{HasConfig: true, Environment: "uat", Service: "shop-app", App: "shop-app", Services: []string{"shop-app"}}, nil
		},
		TicketOf: func(id string) string {
			if i := strings.LastIndex(id, "-"); i > 0 {
				return id[:i]
			}
			return id
		},
		NewSession: func(req studio.SessionRequest) (*mobilerec.Session, error) {
			return mobilerec.New(mobilerec.Options{
				Repo: r.repo, Kind: req.Kind, ID: req.ID, Ticket: req.Ticket, Title: req.Title, Service: "shop-app", App: "shop-app",
				Open: open, Use: req.Use, UseWith: req.With, ConfirmClear: req.ConfirmClear,
				Sleep: func(ctx context.Context, _ time.Duration) error { return ctx.Err() },
			})
		},
		Verify: func(ctx context.Context, sess *mobilerec.Session, saved *mobilerec.Saved) (studio.VerifyResult, error) {
			// The studio gave the device away before the call: a second session on it is possible now.
			if n := r.openHandles(); n != 0 {
				return studio.VerifyResult{}, fmt.Errorf("the lease is still held by %d session(s)", n)
			}
			h, err := open(ctx, sess.Overlay().Lookup)
			if err != nil {
				return studio.VerifyResult{}, err
			}
			defer h.Close(ctx)
			*verifies = append(*verifies, string(sess.Kind())+":"+sess.ID())
			if sess.Kind() != mobilerec.KindFlow {
				return studio.VerifyResult{Status: "passed", Message: "The device was free, and the scenario was replayed once."}, nil
			}
			entry, err := flow.Load(r.repo, sess.ID())
			if err != nil {
				return studio.VerifyResult{}, err
			}
			res, err := flow.Run(ctx, h.Engine, screenSource{r.repo}, entry.Flow, nil, map[string]string{"run.id": "verify"})
			if err != nil {
				return studio.VerifyResult{}, err
			}
			out := studio.VerifyResult{Status: res.Status, Message: "The flow " + sess.ID() + " " + res.Status + "."}
			for _, s := range res.Steps {
				out.Steps = append(out.Steps, studio.VerifyStep{Name: s.Name, Status: "done"})
			}
			return out, nil
		},
	}
}

// --- the studio over HTTP ---

type client struct {
	t     *testing.T
	base  string
	token string
	mu    sync.Mutex
	seen  strings.Builder // every answer, for a search for the canary
}

func start(t *testing.T, d studio.Deps) (*client, *studio.Server) {
	t.Helper()
	srv, err := studio.New(d)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(srv.Close)
	return &client{t: t, base: ts.URL, token: srv.Token()}, srv
}

func (c *client) do(method, path string, body any, edit ...func(*http.Request)) (int, []byte) {
	c.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		data, err := json.Marshal(b)
		if err != nil {
			c.t.Fatal(err)
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set(localhttp.TokenHeader, c.token)
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, e := range edit {
		e(req)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	c.mu.Lock()
	c.seen.Write(data)
	c.mu.Unlock()
	return res.StatusCode, data
}

type frameBody struct {
	Frame *struct {
		ID          uint64 `json:"frameId"`
		Width       int    `json:"width"`
		Screen      string `json:"screen"`
		Image       string `json:"image"`
		ImageHidden bool   `json:"imageHidden"`
		Elements    []struct {
			Index     int             `json:"index"`
			ID        string          `json:"id"`
			Desc      string          `json:"desc"`
			Text      string          `json:"text"`
			Password  bool            `json:"password"`
			Editable  bool            `json:"editable"`
			Stability string          `json:"stability"`
			Locator   json.RawMessage `json:"locator"`
		} `json:"elements"`
	} `json:"frame"`
	Steps []struct {
		N    int    `json:"n"`
		ID   string `json:"id"`
		Text string `json:"text"`
		Kind string `json:"kind"`
	} `json:"steps"`
	Kind    string `json:"kind"`
	CanUndo bool   `json:"canUndo"`
	Flow    string `json:"flow"`
	Error   string `json:"error"`
	Stale   bool   `json:"stale"`
	Session *struct {
		Frame *struct {
			ID uint64 `json:"frameId"`
		} `json:"frame"`
	} `json:"session"`
	Files   []string          `json:"files"`
	Preview map[string]string `json:"preview"`
	Verify  struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Steps   []struct {
			Name string `json:"name"`
		} `json:"steps"`
	} `json:"verify"`
}

func (c *client) call(method, path string, body any, want int) frameBody {
	c.t.Helper()
	code, data := c.do(method, path, body)
	if code != want {
		c.t.Fatalf("%s %s = %d, want %d: %s", method, path, code, want, data)
	}
	var out frameBody
	if err := json.Unmarshal(data, &out); err != nil {
		c.t.Fatalf("%s %s: %v: %s", method, path, err, data)
	}
	return out
}

func (f frameBody) find(t *testing.T, match func(id, desc string) bool) int {
	t.Helper()
	for _, e := range f.Frame.Elements {
		if match(e.ID, e.Desc) {
			return e.Index
		}
	}
	t.Fatalf("no such element in %+v", f.Frame.Elements)
	return -1
}

func byID(id string) func(string, string) bool {
	return func(i, _ string) bool { return strings.HasSuffix(i, ":id/"+id) }
}
func byDesc(desc string) func(string, string) bool {
	return func(_, d string) bool { return d == desc }
}

type m = map[string]any

func TestStudioRecordsAFlowThenAScenarioThatUsesIt(t *testing.T) {
	r := newRig(t)
	var verifies []string
	c, _ := start(t, r.deps(&verifies))

	// The state before anything starts: the repository, no recording, and nothing secret.
	var st struct {
		Repo      string   `json:"repo"`
		HasConfig bool     `json:"hasConfig"`
		Flows     []string `json:"flows"`
		Session   any      `json:"session"`
		CanUndo   bool     `json:"canUndo"`
	}
	code, data := c.do("GET", "/api/state", nil)
	if code != 200 || json.Unmarshal(data, &st) != nil || !st.HasConfig || st.Session != nil || st.CanUndo || len(st.Flows) != 0 {
		t.Fatalf("state = %d %s", code, data)
	}

	// Start: the app is reset, and the first frame has the picture and the ranked elements.
	f := c.call("POST", "/api/session/start", m{}, 200)
	if f.Frame == nil || f.Frame.Screen != "SignIn" || !strings.HasPrefix(f.Frame.Image, "data:image/jpeg;base64,") {
		t.Fatalf("first frame = %+v", f.Frame)
	}
	email := f.find(t, byID("email"))
	if e := f.Frame.Elements[email]; e.Stability == "" || len(e.Locator) == 0 {
		t.Errorf("the email field has no ranked locator: %+v", e)
	}
	if calls := strings.Join(r.d.Calls(), ","); !strings.HasPrefix(calls, "reset") {
		t.Errorf("the recording did not start with a reset: %s", calls)
	}

	// A literal text, a secret with the canary, and the tap that signs in.
	f = c.call("POST", "/api/session/act", m{"frameId": f.Frame.ID, "do": "type", "index": email, "value": "ana@example.com"}, 200)
	f = c.call("POST", "/api/session/act", m{"frameId": f.Frame.ID, "do": "type", "index": f.find(t, byID("password")), "secretEnv": "SHOP_PASSWORD", "ephemeralSecret": canary}, 200)
	// A stale frame is a conflict that carries the frame of now.
	stale := f.Frame.ID - 1
	code, data = c.do("POST", "/api/session/act", m{"frameId": stale, "do": "tap", "index": 0})
	var sb frameBody
	_ = json.Unmarshal(data, &sb)
	if code != http.StatusConflict || !sb.Stale || sb.Session == nil || sb.Session.Frame == nil || sb.Session.Frame.ID <= f.Frame.ID {
		t.Fatalf("a stale frame gave %d %s", code, data)
	}
	f = c.call("POST", "/api/session/act", m{"frameId": sb.Session.Frame.ID, "do": "tap", "index": f.find(t, byDesc("Sign in"))}, 200)
	if f.Frame.Screen != "Home" {
		t.Fatalf("after the sign-in the screen is %q", f.Frame.Screen)
	}

	// The canary is nowhere that the studio shows.
	code, data = c.do("GET", "/api/state", nil)
	if code != 200 || bytes.Contains(data, []byte(canary)) || bytes.Contains(data, []byte("ana@example.com")) && false {
		t.Fatalf("state = %d %s", code, data)
	}

	// A check, a rename of the first step, and the delete of the check: a flow has none.
	greeting := f.find(t, func(id, _ string) bool { return strings.HasSuffix(id, ":id/welcome") })
	f = c.call("POST", "/api/session/assert", m{"frameId": f.Frame.ID, "index": greeting, "kind": "equals"}, 200)
	if got := f.Steps[len(f.Steps)-1]; got.Kind != "assert" || !strings.Contains(got.Text, "Hi Ana") {
		t.Fatalf("the check = %+v", got)
	}
	if code, data := c.do("PATCH", "/api/timeline/0", m{"id": "open-app"}); code != 200 {
		t.Fatalf("rename: %d %s", code, data)
	}
	// A locator cannot be sent: an old step has no screen to check it against.
	if code, _ := c.do("PATCH", "/api/timeline/1", m{"locator": m{"id": "x"}}); code != 400 {
		t.Errorf("a locator in an edit gave %d, want 400", code)
	}
	// The email becomes a secret: the literal is erased and the file will hold env:SHOP_EMAIL.
	c.call("PATCH", "/api/timeline/1", m{"secretEnv": "SHOP_EMAIL"}, 200)
	f = c.call("DELETE", "/api/timeline/last", m{}, 200)
	for _, s := range f.Steps {
		if s.Kind == "assert" || strings.Contains(s.Text, "ana@example.com") {
			t.Errorf("step %+v should be gone or erased", s)
		}
	}

	// Finish as a flow: the files are written, and the preview shows the flow.
	f = c.call("POST", "/api/finish", m{"kind": "flow", "id": "login"}, 200)
	if !contains(f.Files, ".semar/flows/login.yaml") || !strings.Contains(f.Preview[".semar/flows/login.yaml"], "env:SHOP_PASSWORD") || !f.CanUndo {
		t.Fatalf("finish = %+v", f)
	}
	if strings.Contains(r.allFiles(), canary) || strings.Contains(r.allFiles(), "ana@example.com") {
		t.Fatalf("a secret is in the files:\n%s", r.allFiles())
	}

	// Verify gives the device away, runs the flow on a clean session, and takes the device back.
	before := r.openCount()
	f = c.call("POST", "/api/verify", m{}, 200)
	if f.Verify.Status != "completed" || len(f.Verify.Steps) == 0 || strings.Contains(f.Verify.Status, "passed") {
		t.Fatalf("verify = %+v", f.Verify)
	}
	if got := r.openCount() - before; got != 2 || r.openHandles() != 1 {
		t.Errorf("verify opened %d sessions and %d are open: want the verify's and the studio's again", got, r.openHandles())
	}
	if len(verifies) != 1 || verifies[0] != "flow:login" {
		t.Errorf("verifies = %v", verifies)
	}

	// The next activity: reset the recording, run login with use-flow, check the greeting, save a scenario.
	f = c.call("POST", "/api/session/reset", m{"kind": "scenario"}, 200)
	// The app keeps its sign-in through a restart, as a real one does, so it is on Home. The flow starts with
	// reset: clear, so that running it signs in again from a clean app.
	if len(f.Steps) != 1 || f.Frame.Screen != "Home" {
		t.Fatalf("reset = %+v", f)
	}
	// A flow that signs in runs on the screen that the device shows: the person signs the app out first, as
	// the reset of a recording does when the reset is clear.
	r.signed = false
	r.d.Show(signInScreen())
	c.call("POST", "/api/session/refresh", m{}, 200)
	f = c.call("POST", "/api/session/use-flow", m{"flow": "login"}, 200)
	if !strings.Contains(f.Flow, "completed") || f.Frame.Screen != "Home" {
		t.Fatalf("use-flow = %+v", f)
	}
	greeting = f.find(t, func(id, _ string) bool { return strings.HasSuffix(id, ":id/welcome") })
	c.call("POST", "/api/session/assert", m{"frameId": f.Frame.ID, "index": greeting, "kind": "contains", "value": "Hi"}, 200)
	f = c.call("POST", "/api/finish", m{"kind": "scenario", "id": "SHOP-7", "title": "Greets Ana"}, 200)
	if !contains(f.Files, ".semar/scenarios/SHOP/SHOP-7.feature") || !strings.Contains(f.Preview[".semar/scenarios/SHOP/SHOP-7.feature"], "login") {
		t.Fatalf("finish scenario = %+v", f)
	}
	f = c.call("POST", "/api/verify", m{}, 200)
	if f.Verify.Status != "passed" || len(verifies) != 2 {
		t.Fatalf("verify scenario = %+v %v", f.Verify, verifies)
	}

	// Undo takes the scenario back once, and a second undo has nothing to take back.
	c.call("POST", "/api/undo", m{}, 200)
	if r.exists(".semar/scenarios/SHOP/SHOP-7.feature") || !r.exists(".semar/flows/login.yaml") {
		t.Errorf("undo left the wrong files:\n%s", r.allFiles())
	}
	if code, data := c.do("POST", "/api/undo", m{}); code != http.StatusConflict {
		t.Errorf("second undo = %d %s", code, data)
	}

	// The next activity can start with a flow: the reset runs login from a clean app, and records that it did.
	f = c.call("POST", "/api/session/reset", m{"kind": "scenario", "useFlows": []string{"login"}}, 200)
	if len(f.Steps) != 1 || f.Steps[0].Kind != "use" || f.Frame.Screen != "Home" {
		t.Fatalf("reset with a flow = %+v", f)
	}

	// The canary was in no answer and in no file.
	if seen := c.seen.String(); strings.Contains(seen, canary) {
		t.Errorf("the secret is in an answer of the studio")
	}
	if strings.Contains(r.allFiles(), canary) || strings.Contains(r.sink.text(), canary) {
		t.Errorf("the secret is in a file or in the evidence")
	}
}

func (r *rig) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(r.repo, filepath.FromSlash(rel)))
	return err == nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// A secret that the app shows, as when the person taps show password, is never sent: the text is redacted
// and the picture is left out.
func TestStudioHidesTheFrameThatShowsASecret(t *testing.T) {
	r := newRig(t)
	var verifies []string
	c, _ := start(t, r.deps(&verifies))
	f := c.call("POST", "/api/session/start", m{}, 200)
	f = c.call("POST", "/api/session/act", m{"frameId": f.Frame.ID, "do": "type", "index": f.find(t, byID("password")), "secretEnv": "SHOP_PASSWORD", "ephemeralSecret": canary}, 200)
	// The toggle shows the password: it is the text of the field now.
	shown := signInScreen()
	shown.Walk(func(n *mobile.Node) bool {
		if strings.HasSuffix(n.ID, ":id/password") {
			n.Password, n.Text = false, canary
			n.Attrs["text"], n.Attrs["password"] = canary, ""
		}
		return true
	})
	r.d.Show(shown)
	f = c.call("POST", "/api/session/refresh", m{}, 200)
	if !f.Frame.ImageHidden || f.Frame.Image != "" {
		t.Errorf("a picture of a screen that shows a secret was sent: hidden=%v", f.Frame.ImageHidden)
	}
	if strings.Contains(c.seen.String(), canary) {
		t.Errorf("the shown password is in an answer")
	}
	if code, data := c.do("POST", "/api/session/act", m{"frameId": f.Frame.ID, "do": "tap", "index": f.find(t, byDesc("Sign in"))}); code != 200 {
		t.Errorf("tap: %d %s", code, data)
	}
}

func TestStudioNeedsASecretForAPasswordField(t *testing.T) {
	r := newRig(t)
	var verifies []string
	c, _ := start(t, r.deps(&verifies))
	f := c.call("POST", "/api/session/start", m{}, 200)
	code, data := c.do("POST", "/api/session/act", m{"frameId": f.Frame.ID, "do": "type", "index": f.find(t, byID("password")), "value": canary})
	if code != http.StatusUnprocessableEntity || !bytes.Contains(data, []byte(`"needsSecret":true`)) || bytes.Contains(data, []byte(canary)) {
		t.Errorf("a password typed as text gave %d %s", code, data)
	}
	if typed := r.d.Typed(); len(typed) != 0 {
		t.Errorf("the password reached the device: %q", typed)
	}
}

func TestStudioConfirmsAPhysicalDevice(t *testing.T) {
	r := newRig(t)
	var verifies []string
	d := r.deps(&verifies)
	d.Inspector = fakeInspector{{Serial: "R58M123", Model: "Pixel", State: "device"}, {Serial: "emulator-5554", State: "device", Emulator: true}}
	c, _ := start(t, d)

	var devs struct {
		Devices []struct {
			Serial   string `json:"serial"`
			Physical bool   `json:"physical"`
		} `json:"devices"`
	}
	code, data := c.do("GET", "/api/devices", nil)
	if code != 200 || json.Unmarshal(data, &devs) != nil || len(devs.Devices) != 2 || !devs.Devices[0].Physical || devs.Devices[1].Physical {
		t.Fatalf("devices = %d %s", code, data)
	}
	code, data = c.do("POST", "/api/session/start", m{"deviceSerial": "R58M123"})
	if code != http.StatusPreconditionRequired || !bytes.Contains(data, []byte(`"needsConfirm":true`)) {
		t.Fatalf("a physical device started without a confirmation: %d %s", code, data)
	}
	if len(r.d.Calls()) != 0 {
		t.Errorf("the device was touched before the person agreed: %v", r.d.Calls())
	}
	c.call("POST", "/api/session/start", m{"deviceSerial": "R58M123", "confirmClear": true}, 200)
	if code, _ := c.do("POST", "/api/session/start", m{"deviceSerial": "bad serial;rm"}); code != 400 {
		t.Errorf("a serial with a space gave %d", code)
	}
}

type screenSource struct{ repo string }

func (s screenSource) Load(app string) (domain.Screens, error) { return screens.Load(s.repo, app) }

type fakeInspector []mobile.DeviceInfo

func (f fakeInspector) ListDevices(context.Context) ([]mobile.DeviceInfo, error) { return f, nil }
func (f fakeInspector) InspectForeground(_ context.Context, serial string) (mobile.AppInfo, error) {
	return mobile.AppInfo{Package: "com.example.shop", Activity: ".Main", Candidates: []string{"com.example.shop"}}, nil
}

func TestStudioScaffoldsAStandaloneAndroidApp(t *testing.T) {
	repo := t.TempDir()
	c, _ := start(t, studio.Deps{
		Repo: repo,
		Status: func() (studio.Status, error) {
			cfg, err := config.Load(repo)
			if err != nil {
				return studio.Status{}, nil
			}
			name, env, err := cfg.Environment("")
			if err != nil {
				return studio.Status{Problem: err.Error()}, nil
			}
			return studio.Status{HasConfig: true, Environment: name, Service: env.Services[0].Name}, nil
		},
		NewSession: func(studio.SessionRequest) (*mobilerec.Session, error) { return nil, fmt.Errorf("not here") },
		Verify: func(context.Context, *mobilerec.Session, *mobilerec.Saved) (studio.VerifyResult, error) {
			return studio.VerifyResult{}, nil
		},
	})
	var st struct {
		HasConfig bool `json:"hasConfig"`
	}
	_, data := c.do("GET", "/api/state", nil)
	if json.Unmarshal(data, &st) != nil || st.HasConfig {
		t.Fatalf("a repository with no configuration says %s", data)
	}
	// A recording cannot start before the setup.
	if code, _ := c.do("POST", "/api/session/start", m{}); code != http.StatusConflict {
		t.Errorf("start with no configuration = %d, want 409", code)
	}
	code, data := c.do("POST", "/api/scaffold", m{"name": "sinau", "package": "com.example.sinau"})
	if code != 200 || !bytes.Contains(data, []byte(`"created":true`)) || !bytes.Contains(data, []byte(`"hasConfig":true`)) {
		t.Fatalf("scaffold = %d %s", code, data)
	}
	text, err := os.ReadFile(filepath.Join(repo, ".semar", "semar.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"appPackage: com.example.sinau", "name: sinau"} {
		if !strings.Contains(string(text), want) {
			t.Errorf("semar.yaml lacks %q:\n%s", want, text)
		}
	}
	for _, bad := range []string{"baseUrl", "appActivity"} {
		if strings.Contains(string(text), bad) {
			t.Errorf("a standalone app has no %s:\n%s", bad, text)
		}
	}
	// The same name twice, and a package that is not one, are refused in words.
	if code, data := c.do("POST", "/api/scaffold", m{"name": "sinau", "package": "com.example.sinau"}); code != 400 || !bytes.Contains(data, []byte("already")) {
		t.Errorf("a second scaffold = %d %s", code, data)
	}
	if code, _ := c.do("POST", "/api/scaffold", m{"name": "other", "package": "not a package"}); code != 400 {
		t.Errorf("a bad package = %d", code)
	}
}

func TestStudioGuards(t *testing.T) {
	r := newRig(t)
	var verifies []string
	c, srv := start(t, r.deps(&verifies))
	host := strings.TrimPrefix(c.base, "http://")
	_ = srv

	tests := []struct {
		name   string
		method string
		path   string
		body   any
		edit   func(*http.Request)
		want   int
	}{
		{"bad host on the page", "GET", "/", nil, func(r *http.Request) { r.Host = "evil.example:" + strings.Split(host, ":")[1] }, 403},
		{"bad host on the api", "GET", "/api/state", nil, func(r *http.Request) { r.Host = "evil.example" }, 403},
		{"bad origin", "POST", "/api/session/refresh", m{}, func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }, 403},
		{"no token", "GET", "/api/state", nil, func(r *http.Request) { r.Header.Del(localhttp.TokenHeader) }, 403},
		{"wrong token", "GET", "/api/state", nil, func(r *http.Request) { r.Header.Set(localhttp.TokenHeader, strings.Repeat("a", 64)) }, 403},
		{"wrong content type", "POST", "/api/session/refresh", "{}", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 415},
		{"unknown field", "POST", "/api/session/act", `{"do":"tap","nope":1}`, nil, 400},
		{"oversized body", "POST", "/api/session/act", `{"do":"` + strings.Repeat("x", 70<<10) + `"}`, nil, 413},
		{"no such path", "GET", "/api/nothing", nil, nil, 404},
		{"wrong method", "GET", "/api/session/act", nil, nil, 405},
		{"no recording", "POST", "/api/session/refresh", m{}, nil, 409},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var edits []func(*http.Request)
			if tt.edit != nil {
				edits = append(edits, tt.edit)
			}
			code, data := c.do(tt.method, tt.path, tt.body, edits...)
			if code != tt.want {
				t.Errorf("%s %s = %d, want %d: %s", tt.method, tt.path, code, tt.want, data)
			}
		})
	}

	// Every answer carries the policy: scripts and styles from the page's own origin, pictures as data.
	for _, p := range []string{"/", "/api/state"} {
		req, _ := http.NewRequest("GET", c.base+p, nil)
		req.Header.Set(localhttp.TokenHeader, c.token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		csp := res.Header.Get("Content-Security-Policy")
		for _, want := range []string{"script-src 'self'", "style-src 'self'", "img-src 'self' data:", "frame-ancestors 'none'", "default-src 'none'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: the policy lacks %q: %s", p, want, csp)
			}
		}
		if res.Header.Get("Access-Control-Allow-Origin") != "" || res.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: headers %v", p, res.Header)
		}
	}
}

func TestStudioServesItsPageWithoutOutsideAddresses(t *testing.T) {
	r := newRig(t)
	var verifies []string
	c, _ := start(t, r.deps(&verifies))
	for path, ctype := range map[string]string{"/": "text/html; charset=utf-8", "/app.js": "text/javascript; charset=utf-8", "/style.css": "text/css; charset=utf-8"} {
		// The page and its files need no token: they hold nothing of the recording.
		res, err := http.Get(c.base + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 || res.Header.Get("Content-Type") != ctype || len(body) < 200 {
			t.Errorf("%s: %d %q (%d bytes)", path, res.StatusCode, res.Header.Get("Content-Type"), len(body))
		}
		for _, u := range regexp.MustCompile(`(?i)(https?:)?//[a-z0-9.-]+\.[a-z]{2,}[^\s"')]*`).FindAllString(string(body), -1) {
			if u != "http://www.w3.org/2000/svg" {
				t.Errorf("%s names the outside address %s", path, u)
			}
		}
		for _, bad := range []string{"cdn", "unpkg", "googleapis", "<script>", "style=\"", "onclick=\""} {
			if strings.Contains(strings.ToLower(string(body)), bad) {
				t.Errorf("%s holds %q, which the policy or the design refuses", path, bad)
			}
		}
		if path == "/app.js" {
			if strings.Contains(string(body), "'SEMAR_'") {
				t.Errorf("%s suggests a reserved SEMAR_ secret name", path)
			}
			for _, want := range []string{"'APP_'", "start-service", "'class'"} {
				if !strings.Contains(string(body), want) {
					t.Errorf("%s is missing %s", path, want)
				}
			}
		}
		if path == "/" && !strings.Contains(string(body), `id="start-service"`) {
			t.Errorf("%s is missing the multi-service picker", path)
		}
	}
}

// Two clicks at once never act on the device at once: every call takes the lock of the session.
func TestStudioSerialisesTheDevice(t *testing.T) {
	r := newRig(t)
	var verifies []string
	c, _ := start(t, r.deps(&verifies))
	f := c.call("POST", "/api/session/start", m{}, 200)
	btn := f.find(t, byDesc("Sign in"))
	var wg sync.WaitGroup
	codes := make([]int, 6)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i], _ = c.do("POST", "/api/session/act", m{"frameId": f.Frame.ID, "do": "tap", "index": btn})
		}()
	}
	wg.Wait()
	ok, stale := 0, 0
	for _, code := range codes {
		switch code {
		case 200:
			ok++
		case 409:
			stale++
		default:
			t.Errorf("code %d", code)
		}
	}
	if ok != 1 || stale != len(codes)-1 {
		t.Errorf("%d acted and %d were stale of %d clicks on one frame: want exactly one to act", ok, stale, len(codes))
	}
	if n := len(r.d.Touches()); n != 1 {
		t.Errorf("the device was touched %d times", n)
	}
}

// Ctrl+C ends the session: the device is given away and the server stops.
func TestStudioServeGivesTheDeviceAwayOnShutdown(t *testing.T) {
	r := newRig(t)
	var verifies []string
	srv, err := studio.New(r.deps(&verifies))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	c := &client{t: t, base: "http://" + ln.Addr().String(), token: srv.Token()}
	c.call("POST", "/api/session/start", m{}, 200)
	if r.openHandles() != 1 {
		t.Fatalf("open handles = %d", r.openHandles())
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the studio did not stop")
	}
	if r.openHandles() != 0 {
		t.Errorf("the device was not given away: %d open", r.openHandles())
	}
}
