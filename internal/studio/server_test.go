package studio_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
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
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/localhttp"
	"github.com/jancuk/semar/internal/mobilerec"
	"github.com/jancuk/semar/internal/recorder"
	"github.com/jancuk/semar/internal/redact"
	"github.com/jancuk/semar/internal/scenario"
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
		Recordable: func() ([]studio.Recording, error) {
			out := []studio.Recording{}
			if ids, err := flow.IDs(r.repo); err == nil {
				for _, id := range ids {
					out = append(out, studio.Recording{Kind: "flow", ID: id})
				}
			}
			return out, nil
		},
		NewSession: func(req studio.SessionRequest) (*mobilerec.Session, error) {
			var resume *mobilerec.Loaded
			if req.Resume {
				var err error
				if resume, err = r.loaded(req.Kind, req.ID); err != nil {
					return nil, err
				}
			}
			return mobilerec.New(mobilerec.Options{
				Repo: r.repo, Kind: req.Kind, ID: req.ID, Ticket: req.Ticket, Title: req.Title, Service: "shop-app", App: "shop-app",
				Open: open, Use: req.Use, UseWith: req.With, ConfirmClear: req.ConfirmClear, Resume: resume, ResumeAt: req.ResumeAt,
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

// loaded reads the files of a saved flow or scenario, as the command does to reopen one.
func (r *rig) loaded(kind mobilerec.Kind, id string) (*mobilerec.Loaded, error) {
	scr, err := screens.Load(r.repo, "shop-app")
	if err != nil {
		return nil, err
	}
	l := &mobilerec.Loaded{Screens: scr}
	if kind == mobilerec.KindFlow {
		entry, err := flow.Load(r.repo, id)
		if err != nil {
			return nil, err
		}
		l.Flow, l.Path = entry.Flow, flow.Path(id)
		return l, nil
	}
	var rel string
	_ = filepath.WalkDir(filepath.Join(r.repo, ".semar", "scenarios"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == id+".feature" {
			rel, _ = filepath.Rel(r.repo, p)
		}
		return nil
	})
	if rel == "" {
		return nil, fmt.Errorf("the scenario %s is not saved", id)
	}
	if l.Scenario, err = scenario.Load(filepath.Join(r.repo, rel)); err != nil {
		return nil, err
	}
	store, err := evidence.Open(filepath.Join(r.repo, ".semar"))
	if err != nil {
		return nil, err
	}
	var ok bool
	if l.Script, ok, err = store.Script(l.Scenario.ID, domain.ExpectHash(l.Scenario)); err != nil || !ok {
		return nil, fmt.Errorf("no stored script: %v", err)
	}
	l.Path = filepath.ToSlash(rel)
	return l, nil
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
		N       int    `json:"n"`
		ID      string `json:"id"`
		Text    string `json:"text"`
		Kind    string `json:"kind"`
		Screen  string `json:"screen"`
		Pending bool   `json:"pending"`
		Shared  bool   `json:"shared"`
		Locator string `json:"locator"`
	} `json:"steps"`
	Cursor       int      `json:"cursor"`
	Dirty        bool     `json:"dirty"`
	Common       int      `json:"common"`
	ScreenNames  []string `json:"screenNames"`
	Notice       string   `json:"notice"`
	NeedsSecrets []string `json:"needsSecrets"`
	NeedsParams  []string `json:"needsParams"`
	Captured     *struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"captured"`
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
		Diagnosis *struct {
			Kind     string `json:"kind"`
			Recorded string `json:"recorded"`
			Actual   string `json:"actual"`
		} `json:"diagnosis"`
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
	// The studio took the device back, so it says so, once.
	if strings.Count(f.Verify.Message, "The device is back at the start of the app.") != 1 || strings.Contains(f.Verify.Message, "could not be taken back") {
		t.Errorf("verify message = %q", f.Verify.Message)
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

// signInRecording records a flow on the sign-in screen: the launch, a literal email, a secret password, and
// the tap that signs in. It returns the frame after the last step.
func signInRecording(t *testing.T, c *client, kind string, id string) frameBody {
	t.Helper()
	f := c.call("POST", "/api/session/start", m{"kind": kind, "id": id}, 200)
	f = c.call("POST", "/api/session/act", m{"frameId": f.Frame.ID, "do": "type", "index": f.find(t, byID("email")), "value": "ana@example.com"}, 200)
	f = c.call("POST", "/api/session/act", m{"frameId": f.Frame.ID, "do": "type", "index": f.find(t, byID("password")), "secretEnv": "SHOP_PASSWORD", "ephemeralSecret": canary}, 200)
	return c.call("POST", "/api/session/act", m{"frameId": f.Frame.ID, "do": "tap", "index": f.find(t, byDesc("Sign in"))}, 200)
}

func TestStudioEditsTheTimeline(t *testing.T) {
	r := newRig(t)
	var verifies []string
	c, _ := start(t, r.deps(&verifies))
	f := signInRecording(t, c, "scenario", "")
	greeting := f.find(t, func(id, _ string) bool { return strings.HasSuffix(id, ":id/welcome") })
	f = c.call("POST", "/api/session/assert", m{"frameId": f.Frame.ID, "index": greeting, "kind": "equals"}, 200)
	if len(f.Steps) != 5 || f.Cursor != 5 || f.Dirty || f.Common != 0 || !contains(f.ScreenNames, "SignIn") || !contains(f.ScreenNames, "Home") {
		t.Fatalf("answer = steps %d cursor %d dirty %v common %d names %v", len(f.Steps), f.Cursor, f.Dirty, f.Common, f.ScreenNames)
	}
	if f.Steps[3].Locator == "" || f.Steps[0].Locator != "" {
		t.Errorf("a step with an element shows its locator, and the launch has none: %+v", f.Steps)
	}

	// The locators of the tap, and of a step that has no element.
	var locs struct {
		Locators []struct {
			K       int    `json:"k"`
			Locator string `json:"locator"`
			Current bool   `json:"current"`
		} `json:"locators"`
	}
	code, data := c.do("GET", "/api/timeline/3/locators", nil)
	if code != 200 || json.Unmarshal(data, &locs) != nil || len(locs.Locators) == 0 || !locs.Locators[0].Current || locs.Locators[0].K != 1 {
		t.Fatalf("locators = %d %s", code, data)
	}
	if code, data := c.do("GET", "/api/timeline/0/locators", nil); code != 400 || !bytes.Contains(data, []byte("no element")) {
		t.Errorf("locators of a launch = %d %s", code, data)
	}
	c.call("POST", "/api/timeline/3/locator", m{"choice": 1}, 200)
	if code, _ := c.do("POST", "/api/timeline/3/locator", m{"choice": 9}); code != 400 {
		t.Errorf("a locator that is not listed gave %d, want 400", code)
	}
	if code, _ := c.do("POST", "/api/timeline/3/locator", m{}); code != 400 {
		t.Errorf("a locator call with nothing in it gave %d, want 400", code)
	}

	// A step shared with every screen, and taken back. A locator is still refused in an edit.
	f = c.call("PATCH", "/api/timeline/3", m{"common": true}, 200)
	if !f.Steps[3].Shared {
		t.Errorf("the step is not shared: %+v", f.Steps[3])
	}
	f = c.call("PATCH", "/api/timeline/3", m{"common": false}, 200)
	if f.Steps[3].Shared {
		t.Errorf("the step is still shared")
	}
	for _, body := range []m{{"locator": m{"id": "x"}}, {"hasText": "Milk"}, {"index": 2}, {"within": "card"}} {
		if code, _ := c.do("PATCH", "/api/timeline/3", body); code != 400 {
			t.Errorf("an edit with %v gave %d, want 400", body, code)
		}
	}

	// Deleting the literal email, an action that the device did, leaves the session out of sync.
	f = c.call("DELETE", "/api/timeline/1", m{}, 200)
	if !f.Dirty || len(f.Steps) != 4 || f.Cursor != 4 {
		t.Fatalf("after the delete: dirty %v steps %d cursor %d", f.Dirty, len(f.Steps), f.Cursor)
	}
	for _, s := range f.Steps {
		if strings.Contains(s.Text, "ana@example.com") {
			t.Errorf("the deleted step is still there: %+v", s)
		}
	}
	for _, p := range []string{"/api/timeline/99", "/api/timeline/x"} {
		if code, _ := c.do("DELETE", p, m{}); code != 404 && code != 400 {
			t.Errorf("DELETE %s = %d", p, code)
		}
	}

	// Go here: the app is reset, the steps up to the 3rd are replayed (the launch, the password, the tap), and
	// the cursor is there. The secret was kept in the session, so nothing is asked.
	r.signed = false // the app of a real recording that signs in is reset with clear: it is signed out again
	f = c.call("POST", "/api/timeline/goto", m{"n": 3}, 200)
	if f.Dirty || f.Cursor != 3 || !f.Steps[3].Pending || f.Steps[2].Pending || f.Frame.Screen != "Home" {
		t.Fatalf("goto = cursor %d dirty %v pending %v screen %q", f.Cursor, f.Dirty, f.Steps[3].Pending, f.Frame.Screen)
	}
	// A new step goes at the cursor, before the check that was already there.
	f = c.call("POST", "/api/session/act", m{"frameId": f.Frame.ID, "do": "tap", "index": f.find(t, byDesc("Sign out"))}, 200)
	if len(f.Steps) != 5 || f.Steps[3].Kind != "tap" || f.Steps[4].Kind != "assert" || f.Cursor != 4 {
		t.Fatalf("the new step is not in the middle: %+v cursor %d", f.Steps, f.Cursor)
	}
	if code, _ := c.do("POST", "/api/timeline/goto", m{"n": 40}); code != 400 {
		t.Errorf("goto past the end gave %d, want 400", code)
	}
	f = c.call("DELETE", "/api/timeline/last", m{}, 200)
	if len(f.Steps) != 4 {
		t.Errorf("delete last left %d steps", len(f.Steps))
	}
	if strings.Contains(c.seen.String(), canary) {
		t.Errorf("the secret is in an answer")
	}
}

func TestStudioListsCandidatesAndTakesAChoice(t *testing.T) {
	r := newRig(t)
	var verifies []string
	c, _ := start(t, r.deps(&verifies))
	f := c.call("POST", "/api/session/start", m{}, 200)
	btn := f.find(t, byDesc("Sign in"))
	var list struct {
		Usable []struct {
			K       int    `json:"k"`
			Locator string `json:"locator"`
			Matches int    `json:"matches"`
		} `json:"usable"`
	}
	path := fmt.Sprintf("/api/session/candidates?frameId=%d&index=%d&do=tap", f.Frame.ID, btn)
	code, data := c.do("GET", path, nil)
	if code != 200 || json.Unmarshal(data, &list) != nil || len(list.Usable) == 0 || list.Usable[0].K != 1 {
		t.Fatalf("candidates = %d %s", code, data)
	}
	for _, p := range []string{"/api/session/candidates", "/api/session/candidates?frameId=1", fmt.Sprintf("/api/session/candidates?frameId=%d&index=999", f.Frame.ID)} {
		if code, _ := c.do("GET", p, nil); code != 400 {
			t.Errorf("GET %s = %d, want 400", p, code)
		}
	}
	// A choice that is not in the list is refused, and the screen is as it was.
	if code, data := c.do("POST", "/api/session/act", m{"frameId": f.Frame.ID, "do": "tap", "index": btn, "candidate": 99}); code != 400 || !bytes.Contains(data, []byte("no locator 99")) {
		t.Errorf("a candidate that is not listed = %d %s", code, data)
	}
	f = c.call("POST", "/api/session/act", m{"frameId": f.Frame.ID, "do": "tap", "index": btn, "candidate": 1}, 200)
	if f.Frame.Screen != "Home" {
		t.Errorf("the tap with the first candidate left the screen on %q", f.Frame.Screen)
	}
	greeting := f.find(t, func(id, _ string) bool { return strings.HasSuffix(id, ":id/welcome") })
	if code, data := c.do("POST", "/api/session/assert", m{"frameId": f.Frame.ID, "index": greeting, "kind": "exists", "candidate": 99}); code != 400 {
		t.Errorf("an assert with a candidate that is not listed = %d %s", code, data)
	}
	c.call("POST", "/api/session/assert", m{"frameId": f.Frame.ID, "index": greeting, "kind": "exists", "candidate": 1}, 200)

	// Repick: the device is at the step before the tap only when the cursor is there, so the call is refused here.
	if code, data := c.do("POST", "/api/timeline/1/locator", m{"frameId": f.Frame.ID, "element": greeting}); code != 409 || !bytes.Contains(data, []byte("goto")) {
		t.Errorf("a repick away from the step = %d %s", code, data)
	}
}

func TestStudioNamesScreensAndRenamesTheSteps(t *testing.T) {
	r := newRig(t)
	var verifies []string
	c, _ := start(t, r.deps(&verifies))
	f := c.call("POST", "/api/session/start", m{}, 200)
	f = c.call("POST", "/api/session/screen", m{"name": "Landing"}, 200)
	if f.Frame.Screen != "Landing" || !contains(f.ScreenNames, "Landing") {
		t.Fatalf("screen = %q %v", f.Frame.Screen, f.ScreenNames)
	}
	f = c.call("POST", "/api/session/act", m{"frameId": f.Frame.ID, "do": "tap", "index": f.find(t, byDesc("Sign in"))}, 200)
	var onLanding bool
	for _, s := range f.Steps {
		onLanding = onLanding || s.Screen == "Landing"
	}
	if !onLanding {
		t.Fatalf("no step is on Landing: %+v", f.Steps)
	}
	f = c.call("POST", "/api/session/screen", m{"name": "Welcome", "rename": true, "from": "Landing"}, 200)
	for _, s := range f.Steps {
		if s.Screen == "Landing" {
			t.Errorf("a step is still on Landing: %+v", s)
		}
	}
	if !contains(f.ScreenNames, "Welcome") {
		t.Errorf("screen names = %v", f.ScreenNames)
	}
	if code, _ := c.do("POST", "/api/session/screen", m{"name": "bad name!"}); code != 400 {
		t.Errorf("a bad screen name gave %d, want 400", code)
	}
	if code, _ := c.do("POST", "/api/session/screen", m{"name": "Same", "rename": true, "from": "Same"}); code != 400 {
		t.Errorf("a rename to the same name gave %d, want 400", code)
	}
}

func TestStudioCapturesAValueAndMakesAParam(t *testing.T) {
	r := newRig(t)
	var verifies []string
	c, _ := start(t, r.deps(&verifies))

	// A scenario: capture the first word after Hi, and a second capture of the same name is refused.
	f := signInRecording(t, c, "scenario", "")
	greeting := f.find(t, func(id, _ string) bool { return strings.HasSuffix(id, ":id/welcome") })
	f = c.call("POST", "/api/session/capture", m{"frameId": f.Frame.ID, "index": greeting, "name": "who", "pattern": "Hi (\\w+)"}, 200)
	if f.Captured == nil || f.Captured.Name != "who" || f.Captured.Value != "Ana" || f.Steps[len(f.Steps)-1].Kind != "capture" {
		t.Fatalf("capture = %+v steps %+v", f.Captured, f.Steps)
	}
	for _, body := range []m{
		{"frameId": f.Frame.ID, "index": greeting, "name": "who"},
		{"frameId": f.Frame.ID, "index": greeting, "name": "other", "pattern": "Bye (\\w+)"},
		{"frameId": f.Frame.ID, "index": greeting, "name": "bad name"},
		{"frameId": f.Frame.ID, "index": greeting, "name": "other", "pattern": "(["},
		{"frameId": f.Frame.ID, "name": "other"},
	} {
		if code, data := c.do("POST", "/api/session/capture", body); code != 400 {
			t.Errorf("capture %v = %d %s, want 400", body, code, data)
		}
	}
	// A scenario has no params.
	if code, data := c.do("PATCH", "/api/timeline/1", m{"param": "email"}); code != 400 || !bytes.Contains(data, []byte("no params")) {
		t.Errorf("a param in a scenario = %d %s", code, data)
	}

	// A flow: the email becomes a param with its text as the default, and the notice names nothing else.
	r.signed = false
	f = signInRecording(t, c, "flow", "login")
	if code, data := c.do("PATCH", "/api/timeline/1", m{"param": "email", "required": false}); code != 200 || !bytes.Contains(data, []byte("{{email}}")) {
		t.Fatalf("param = %d %s", code, data)
	}
	// A secret is never a param, a name that is used is refused, and a step with no text is refused.
	for _, c2 := range []struct {
		step int
		body m
	}{{2, m{"param": "pw"}}, {1, m{"param": "email"}}, {0, m{"param": "x"}}} {
		if code, data := c.do("PATCH", fmt.Sprintf("/api/timeline/%d", c2.step), c2.body); code != 400 {
			t.Errorf("param %v on step %d = %d %s, want 400", c2.body, c2.step, code, data)
		}
	}
	// A flow takes a check, and it can be saved.
	f = c.call("POST", "/api/session/refresh", m{}, 200)
	greeting = f.find(t, func(id, _ string) bool { return strings.HasSuffix(id, ":id/welcome") })
	f = c.call("POST", "/api/session/assert", m{"frameId": f.Frame.ID, "index": greeting, "kind": "equals"}, 200)
	if f.Steps[len(f.Steps)-1].Kind != "assert" {
		t.Fatalf("a flow took no check: %+v", f.Steps)
	}
	c.call("POST", "/api/finish", m{"kind": "flow", "id": "login"}, 200)
	text, _ := os.ReadFile(filepath.Join(r.repo, ".semar", "flows", "login.yaml"))
	if !strings.Contains(string(text), "{{email}}") || !strings.Contains(string(text), "expect") || strings.Contains(r.allFiles(), canary) {
		t.Errorf("the flow file is wrong or holds the secret:\n%s", text)
	}
}

func TestStudioReopensARecordingAndAsksForWhatNoFileHolds(t *testing.T) {
	r := newRig(t)
	var verifies []string
	d := r.deps(&verifies)
	c, _ := start(t, d)

	// A flow with a secret, a literal made a required param, and a check.
	signInRecording(t, c, "flow", "login")
	c.call("PATCH", "/api/timeline/1", m{"param": "email", "required": true}, 200)
	f := c.call("POST", "/api/session/refresh", m{}, 200)
	greeting := f.find(t, func(id, _ string) bool { return strings.HasSuffix(id, ":id/welcome") })
	f = c.call("POST", "/api/session/assert", m{"frameId": f.Frame.ID, "index": greeting, "kind": "equals"}, 200)
	steps := len(f.Steps)
	c.call("POST", "/api/finish", m{"kind": "flow", "id": "login"}, 200)

	// The state lists what can be reopened.
	var st struct {
		Recordings []struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
		} `json:"recordings"`
	}
	code, data := c.do("GET", "/api/state", nil)
	if code != 200 || json.Unmarshal(data, &st) != nil || len(st.Recordings) != 1 || st.Recordings[0].ID != "login" || st.Recordings[0].Kind != "flow" {
		t.Fatalf("state = %d %s", code, data)
	}

	// Reopen at step 1: only the launch is replayed, the steps are loaded, and the rest are pending.
	f = c.call("POST", "/api/session/start", m{"resume": m{"kind": "flow", "id": "login", "at": 1}}, 200)
	if f.Kind != "flow" || len(f.Steps) != steps || f.Cursor != 1 || f.Steps[1].Pending != true || f.Steps[0].Pending {
		t.Fatalf("reopened = kind %q steps %d cursor %d", f.Kind, len(f.Steps), f.Cursor)
	}
	if code, _ := c.do("POST", "/api/session/start", m{"resume": m{"kind": "flow", "id": "nope"}}); code != 400 {
		t.Errorf("reopening what is not saved gave %d, want 400", code)
	}
	if code, _ := c.do("POST", "/api/session/start", m{"resume": m{"kind": "flow", "id": "login"}, "useFlows": []string{"login"}}); code != 400 {
		t.Errorf("a reopen with a flow to start with gave %d, want 400", code)
	}
	// The failed start gave the device away, and the next one is a new recording: reopen again.
	c.call("POST", "/api/session/start", m{"resume": m{"kind": "flow", "id": "login", "at": 1}}, 200)

	// Replaying the steps that type the password needs it, and the param: 428 names them, and holds no value.
	typedBefore := len(r.d.Typed())
	code, data = c.do("POST", "/api/timeline/goto", m{"n": steps})
	var need frameBody
	_ = json.Unmarshal(data, &need)
	if code != http.StatusPreconditionRequired || !contains(need.NeedsSecrets, "SHOP_PASSWORD") || !contains(need.NeedsParams, "email") || bytes.Contains(data, []byte(canary)) {
		t.Fatalf("goto without the values = %d %s", code, data)
	}
	if typed := r.d.Typed(); len(typed) != typedBefore {
		t.Errorf("a text reached the device before the values were given: %q", typed)
	}
	f = c.call("POST", "/api/timeline/goto", m{"n": steps, "secrets": m{"SHOP_PASSWORD": canary}, "params": m{"email": "bo@example.com"}}, 200)
	if f.Cursor != steps || f.Dirty || f.Frame.Screen != "Home" {
		t.Fatalf("goto with the values = cursor %d dirty %v screen %q", f.Cursor, f.Dirty, f.Frame.Screen)
	}
	if typed := r.d.Typed()[typedBefore:]; !contains(typed, canary) || !contains(typed, "bo@example.com") {
		t.Errorf("the device was not given the values: %q", typed)
	}
	// A name that is no secret name, and a param that the flow lacks, are refused.
	if code, _ := c.do("POST", "/api/timeline/goto", m{"n": 1, "secrets": m{"bad name": "x"}}); code != 400 {
		t.Errorf("a bad secret name gave %d, want 400", code)
	}
	if code, _ := c.do("POST", "/api/timeline/goto", m{"n": 1, "params": m{"nope": "x"}}); code != 400 {
		t.Errorf("an unknown param gave %d, want 400", code)
	}
	if strings.Contains(c.seen.String(), canary) || strings.Contains(r.allFiles(), canary) || strings.Contains(r.sink.text(), canary) {
		t.Errorf("the secret is in an answer, a file, or the evidence")
	}

	// A flow records on top of another flow, now that a flow may use one.
	r.signed = false
	f = c.call("POST", "/api/session/start", m{"kind": "flow", "id": "nav"}, 200)
	f = c.call("POST", "/api/session/act", m{"frameId": f.Frame.ID, "do": "tap", "index": f.find(t, byDesc("Sign in"))}, 200)
	c.call("POST", "/api/finish", m{"kind": "flow", "id": "nav"}, 200)
	r.signed = false
	f = c.call("POST", "/api/session/start", m{"kind": "flow", "id": "outer", "useFlows": []string{"nav"}}, 200)
	if f.Kind != "flow" || len(f.Steps) == 0 || f.Steps[len(f.Steps)-1].Kind != "use" {
		t.Errorf("a flow that starts with a flow = %+v", f)
	}
}

func TestStudioReopensAScenarioAndSavesItAgain(t *testing.T) {
	r := newRig(t)
	var verifies []string
	c, _ := start(t, r.deps(&verifies))
	r.signed = true
	f := c.call("POST", "/api/session/start", m{"id": "SHOP-9"}, 200)
	greeting := f.find(t, func(id, _ string) bool { return strings.HasSuffix(id, ":id/welcome") })
	c.call("POST", "/api/session/assert", m{"frameId": f.Frame.ID, "index": greeting, "kind": "equals"}, 200)
	f = c.call("POST", "/api/finish", m{"kind": "scenario", "id": "SHOP-9", "title": "Greets"}, 200)
	feature := ".semar/scenarios/SHOP/SHOP-9.feature"
	if !contains(f.Files, feature) {
		t.Fatalf("files = %v", f.Files)
	}
	before, _ := os.ReadFile(filepath.Join(r.repo, filepath.FromSlash(feature)))

	r.signed = true
	f = c.call("POST", "/api/session/start", m{"resume": m{"kind": "scenario", "id": "SHOP-9"}}, 200)
	if f.Kind != "scenario" || len(f.Steps) != 3 || f.Cursor != 3 || f.Steps[2].Kind != "assert" {
		t.Fatalf("reopened = %+v cursor %d", f.Steps, f.Cursor)
	}
	// It saves again under its own name with no replace, and the files are as they were.
	c.call("POST", "/api/finish", m{"kind": "scenario", "id": "SHOP-9", "title": "Greets"}, 200)
	after, _ := os.ReadFile(filepath.Join(r.repo, filepath.FromSlash(feature)))
	if string(before) != string(after) {
		t.Errorf("a reopen and a save changed the feature:\n%s\n---\n%s", before, after)
	}
}

// The page names every path that the studio serves for editing, locators, screen names, and captures.
func TestStudioPageUsesTheEditingAPI(t *testing.T) {
	r := newRig(t)
	var verifies []string
	c, _ := start(t, r.deps(&verifies))
	res, err := http.Get(c.base + "/app.js")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	js := string(body)
	for _, want := range []string{
		"'/api/timeline/goto'", "'/api/timeline/' + i + '/locators'", "'/api/timeline/' + i + '/locator'", "'DELETE', '/api/timeline/' + i",
		"'DELETE', '/api/timeline/last'", "'/api/session/candidates?", "'/api/session/capture'", "'/api/session/screen'",
		"'PATCH', '/api/timeline/' + i", "resume:", "candidate", "rename: true", "common", "param", "required", "needsSecrets", "'mode-capture'",
		"recording here", "Go here", "Make parameter", "screenNames", "recordings", "'/api/session/start'",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js does not use %s", want)
		}
	}
	res, err = http.Get(c.base + "/")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	for _, id := range []string{"mode-capture", "open-pick", "open-at", "open-existing", "screen-names", "screen-rename", "cand-panel", "dlg-capture", "dlg-values", "start-kind"} {
		if !strings.Contains(string(page), `id="`+id+`"`) {
			t.Errorf("index.html has no %s", id)
		}
		if !strings.Contains(js, "$('"+id+"')") && id != "screen-names" && id != "start-kind" && id != "dlg-values" && id != "dlg-capture" {
			t.Errorf("app.js never uses %s", id)
		}
	}
}

func TestStudioAsksForTheValuesOfAReopenedRecordingBeforeItStarts(t *testing.T) {
	r := newRig(t)
	var verifies []string
	c, _ := start(t, r.deps(&verifies))
	signInRecording(t, c, "flow", "login")
	c.call("PATCH", "/api/timeline/1", m{"param": "email", "required": true}, 200)
	c.call("POST", "/api/finish", m{"kind": "flow", "id": "login"}, 200)

	// Reopening all of it needs the password and the param: 428 names them, and nothing was started.
	typedBefore := len(r.d.Typed())
	code, data := c.do("POST", "/api/session/start", m{"resume": m{"kind": "flow", "id": "login"}})
	var need frameBody
	_ = json.Unmarshal(data, &need)
	if code != http.StatusPreconditionRequired || !contains(need.NeedsSecrets, "SHOP_PASSWORD") || !contains(need.NeedsParams, "email") || bytes.Contains(data, []byte(canary)) {
		t.Fatalf("reopen without the values = %d %s", code, data)
	}
	if len(r.d.Typed()) != typedBefore {
		t.Errorf("a text reached the device before the values were given: %q", r.d.Typed())
	}
	r.signed = false
	f := c.call("POST", "/api/session/start", m{
		"resume": m{"kind": "flow", "id": "login"}, "secrets": m{"SHOP_PASSWORD": canary}, "params": m{"email": "bo@example.com"},
	}, 200)
	if f.Cursor != len(f.Steps) || f.Frame.Screen != "Home" {
		t.Errorf("reopened = cursor %d of %d, screen %q", f.Cursor, len(f.Steps), f.Frame.Screen)
	}
	if strings.Contains(c.seen.String(), canary) {
		t.Error("the secret is in an answer")
	}

	// A goto to a step that is not there is refused before any value is kept.
	if code, _ := c.do("POST", "/api/timeline/goto", m{"n": 0, "secrets": m{"SHOP_OTHER": "x"}}); code != 400 {
		t.Errorf("goto 0 gave %d, want 400", code)
	}
	// The dirty flag is always in the answer, so that the page never keeps a stale one.
	code, data = c.do("DELETE", "/api/timeline/1", m{})
	if code != 200 || !bytes.Contains(data, []byte(`"dirty":true`)) {
		t.Fatalf("delete = %d %s", code, data)
	}
	code, data = c.do("POST", "/api/timeline/goto", m{"n": 1})
	if code != 200 || !bytes.Contains(data, []byte(`"dirty":false`)) {
		t.Errorf("goto = %d, the answer lacks dirty:false: %.200s", code, data)
	}
	if code, _ := c.do("DELETE", "/api/timeline/99", m{}); code != 404 {
		t.Errorf("deleting a step that is not there gave %d, want 404", code)
	}
}

// Starting a flow without giving an ID up front uses the placeholder ID and lets the user name it on finish.
func TestStudioStartsAFlowWithoutAnIDAndSavesIt(t *testing.T) {
	r := newRig(t)
	var verifies []string
	c, _ := start(t, r.deps(&verifies))

	f := c.call("POST", "/api/session/start", m{"kind": "flow"}, 200)
	if f.Kind != "flow" || f.Frame == nil || f.Frame.Screen != "SignIn" {
		t.Fatalf("start flow without id = %+v", f)
	}
	f = c.call("POST", "/api/session/act", m{"frameId": f.Frame.ID, "do": "tap", "index": f.find(t, byDesc("Sign in"))}, 200)
	if f.Frame.Screen != "Home" {
		t.Fatalf("after tap screen = %q, want Home", f.Frame.Screen)
	}
	f = c.call("POST", "/api/finish", m{"kind": "flow", "id": "quick-login"}, 200)
	if !contains(f.Files, ".semar/flows/quick-login.yaml") || !r.exists(".semar/flows/quick-login.yaml") {
		t.Fatalf("finish flow = %+v", f.Files)
	}
}

// Recorded actions reuse the engine's settled tree and shrunk JPEG instead of settling or screenshotting twice,
// downscale large fresh captures, and still hide screenshots whenever a secret is visible on screen.
func TestStudioReusesEngineShotAndHidesSecrets(t *testing.T) {
	r := newRig(t)
	var verifies []string
	c, _ := start(t, r.deps(&verifies))

	// Start: 1 settle (2 Source calls) and 1 Screenshot call.
	f := c.call("POST", "/api/session/start", m{}, 200)
	if !strings.HasPrefix(f.Frame.Image, "data:image/jpeg;base64,") {
		t.Fatalf("start image = %q", f.Frame.Image)
	}
	if got := r.d.ScreenshotCalls(); got != 1 {
		t.Errorf("start Screenshot calls = %d, want 1", got)
	}

	// Tap: 1 resolve + 1 settle (3 Source calls total), 1 Screenshot call.
	srcBefore, shotBefore := r.d.SourceCalls(), r.d.ScreenshotCalls()
	f = c.call("POST", "/api/session/act", m{"frameId": f.Frame.ID, "do": "tap", "index": f.find(t, byDesc("Sign in"))}, 200)
	if got := r.d.SourceCalls() - srcBefore; got != 3 {
		t.Errorf("tap Source calls = %d, want 3 (1 resolve + 1 settle)", got)
	}
	if got := r.d.ScreenshotCalls() - shotBefore; got != 1 {
		t.Errorf("tap Screenshot calls = %d, want 1", got)
	}
	if !strings.HasPrefix(f.Frame.Image, "data:image/jpeg;base64,") {
		t.Errorf("tap image = %q", f.Frame.Image)
	}

	// Plain swipe: uses InitialTree (0 pre-action Source) + 1 settle (2 Source calls total), 1 Screenshot call.
	srcBefore, shotBefore = r.d.SourceCalls(), r.d.ScreenshotCalls()
	f = c.call("POST", "/api/session/act", m{"frameId": f.Frame.ID, "do": "swipe", "value": "up"}, 200)
	if got := r.d.SourceCalls() - srcBefore; got != 2 {
		t.Errorf("swipe Source calls = %d, want 2 (1 settle only)", got)
	}
	if got := r.d.ScreenshotCalls() - shotBefore; got != 1 {
		t.Errorf("swipe Screenshot calls = %d, want 1", got)
	}

	// Fresh refresh downscales a large PNG screenshot to at most 960px on its longest side.
	var bigBuf bytes.Buffer
	if err := png.Encode(&bigBuf, image.NewRGBA(image.Rect(0, 0, 1080, 2160))); err != nil {
		t.Fatal(err)
	}
	r.d.Shot = bigBuf.Bytes()
	f = c.call("POST", "/api/session/refresh", m{}, 200)
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(f.Frame.Image, "data:image/jpeg;base64,"))
	if err != nil {
		t.Fatalf("decode refresh jpeg: %v", err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode jpeg config: %v", err)
	}
	if max(cfg.Width, cfg.Height) > 960 {
		t.Errorf("refreshed image size = %dx%d, want longest side <= 960", cfg.Width, cfg.Height)
	}

	// Secret revealed directly by an action must hide the image and never serve the cached engine JPEG.
	r.signed = false
	r.d.Shot = tinyPNG()
	f = c.call("POST", "/api/session/reset", m{}, 200)
	f = c.call("POST", "/api/session/act", m{"frameId": f.Frame.ID, "do": "type", "index": f.find(t, byID("password")), "secretEnv": "SHOP_PASSWORD", "ephemeralSecret": canary}, 200)
	r.d.OnTouch = func(mobile.Gesture) {
		shown := signInScreen()
		shown.Walk(func(n *mobile.Node) bool {
			if strings.HasSuffix(n.ID, ":id/password") {
				n.Password, n.Text = false, canary
				n.Attrs["text"], n.Attrs["password"] = canary, ""
			}
			return true
		})
		r.d.Show(shown)
	}
	f = c.call("POST", "/api/session/act", m{"frameId": f.Frame.ID, "do": "tap", "index": f.find(t, byDesc("Sign in"))}, 200)
	if !f.Frame.ImageHidden || f.Frame.Image != "" {
		t.Errorf("secret revealed by action was not hidden: hidden=%v image=%q", f.Frame.ImageHidden, f.Frame.Image)
	}
	if strings.Contains(c.seen.String(), canary) {
		t.Error("the secret appeared in a studio response")
	}
}

func TestStudioDiagnosesAStaleStartAndSavesTheFix(t *testing.T) {
	r := newRig(t)
	var verifies []string
	d := r.deps(&verifies)
	verify := d.Verify
	d.Verify = func(ctx context.Context, sess *mobilerec.Session, saved *mobilerec.Saved) (studio.VerifyResult, error) {
		res, err := verify(ctx, sess, saved)
		text, _ := os.ReadFile(filepath.Join(r.repo, ".semar/flows/login.yaml"))
		if err == nil && !strings.Contains(string(text), "reset: clear") {
			res.Status, res.FailedStep = "failed", sess.Steps()[1].ID
		}
		return res, err
	}
	c, _ := start(t, d)

	f := c.call("POST", "/api/session/start", m{"kind": "flow", "id": "login"}, 200)
	f = c.call("POST", "/api/session/act", m{"frameId": f.Frame.ID, "do": "type", "index": f.find(t, byID("email")), "value": "ana@example.com"}, 200)
	c.call("POST", "/api/session/act", m{"frameId": f.Frame.ID, "do": "tap", "index": f.find(t, byDesc("Sign in"))}, 200)
	c.call("POST", "/api/finish", m{"kind": "flow", "id": "login"}, 200)

	f = c.call("POST", "/api/verify", m{}, 200)
	if f.Verify.Diagnosis == nil || f.Verify.Diagnosis.Kind != "stale-start" || f.Verify.Diagnosis.Actual != "Home" || f.Verify.Diagnosis.Recorded != "SignIn" {
		t.Fatalf("verify = %+v", f.Verify)
	}
	if !strings.Contains(f.Verify.Message, "The replay began on Home, not SignIn") {
		t.Errorf("the message does not carry the diagnosis: %q", f.Verify.Message)
	}

	c.call("POST", "/api/finish", m{"kind": "flow", "id": "login", "replace": true, "clearFirstLaunch": true}, 200)
	if text, _ := os.ReadFile(filepath.Join(r.repo, ".semar/flows/login.yaml")); !strings.Contains(string(text), "reset: clear") {
		t.Fatalf("the fix was not saved:\n%s", text)
	}
	if f = c.call("POST", "/api/verify", m{}, 200); f.Verify.Diagnosis != nil || f.Verify.Status != "completed" {
		t.Errorf("verify after the fix = %+v", f.Verify)
	}
}
