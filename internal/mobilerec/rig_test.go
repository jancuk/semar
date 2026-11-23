package mobilerec_test

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/mobiletest"
	"github.com/jancuk/semar/internal/mobilerec"
	"github.com/jancuk/semar/internal/recorder"
	"github.com/jancuk/semar/internal/redact"
	"github.com/jancuk/semar/internal/secret"
)

const canary = "Tr0ub4dor&3-canary"

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

// rig is a repository, a fake device, and a factory that opens it, with a count of the open handles.
type rig struct {
	t      *testing.T
	repo   string
	d      *mobiletest.FakeDriver
	sink   *memSink
	signed bool

	mu      sync.Mutex
	opens   int
	open    int
	lookups []mobilerec.LookupEnv
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

func screen(kids ...*mobile.Node) *mobile.Tree { return mobiletest.Screen(kids...) }

func signInScreen() *mobile.Tree {
	return screen(heading("Sign in"), field("email", "Email", 300, false), field("password", "Password", 500, true), button("Sign in", 100, 900))
}

func homeScreen() *mobile.Tree {
	return screen(heading("Home"), label("welcome", "Hi Ana", 300), button("Sign out", 100, 900))
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, repo: t.TempDir(), d: mobiletest.NewFakeDriver(), sink: &memSink{}}
	r.d.Show(signInScreen())
	showStart := func() {
		if r.signed {
			r.d.Show(homeScreen())
		} else {
			r.d.Show(signInScreen())
		}
	}
	r.d.OnLaunch = showStart
	r.d.OnReset = func(mode string) {
		if mode == "clear" {
			r.signed = false
		}
		showStart()
	}
	r.d.OnTouch = func(g mobile.Gesture) {
		if g.From.Y >= 900 && g.From.Y < 1000 {
			r.signed = !r.signed
			showStart()
		}
	}
	return r
}

// factory opens the fake device, as the command line opens the Android platform.
func (r *rig) factory() mobilerec.PlatformFactory {
	return func(_ context.Context, lookup mobilerec.LookupEnv) (mobilerec.Handle, error) {
		r.mu.Lock()
		r.opens++
		r.open++
		r.lookups = append(r.lookups, lookup)
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

func (r *rig) session(kind mobilerec.Kind, id string, tune ...func(*mobilerec.Options)) *mobilerec.Session {
	r.t.Helper()
	o := mobilerec.Options{
		Repo: r.repo, Kind: kind, ID: id, Ticket: "SHOP-1", Service: "shop-app", App: "shop-app", Open: r.factory(),
		Sleep: func(ctx context.Context, _ time.Duration) error { return ctx.Err() },
	}
	for _, f := range tune {
		f(&o)
	}
	s, err := mobilerec.New(o)
	if err != nil {
		r.t.Fatal(err)
	}
	return s
}

func (r *rig) read(rel string) string {
	r.t.Helper()
	b, err := os.ReadFile(filepath.Join(r.repo, filepath.FromSlash(rel)))
	if err != nil {
		r.t.Fatal(err)
	}
	return string(b)
}

func (r *rig) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(r.repo, filepath.FromSlash(rel)))
	return err == nil
}

// allFiles reads every file below the repository, for a search for a secret.
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

// pick finds the element of a frame that a test names.
func pick(t *testing.T, f *mobilerec.Frame, match func(mobilerec.Element) bool) mobilerec.Target {
	t.Helper()
	for _, e := range f.Elements {
		if match(e) {
			return mobilerec.Target{Index: e.Index}
		}
	}
	t.Fatalf("no such element in %+v", f.Elements)
	return mobilerec.Target{}
}

func byID(id string) func(mobilerec.Element) bool {
	return func(e mobilerec.Element) bool { return strings.HasSuffix(e.ID, ":id/"+id) }
}

func byDesc(desc string) func(mobilerec.Element) bool {
	return func(e mobilerec.Element) bool { return e.Desc == desc }
}

// recordLogin signs in on the fake device and returns the frame at the end.
func recordLogin(t *testing.T, r *rig, s *mobilerec.Session) *mobilerec.Frame {
	t.Helper()
	ctx := context.Background()
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if f, err = s.Act(ctx, mobilerec.Act{Do: "type", Frame: f.ID, Target: ptr(pick(t, f, byID("email"))), Value: "ana@example.com"}); err != nil {
		t.Fatal(err)
	}
	if f, err = s.Act(ctx, mobilerec.Act{Do: "type", Frame: f.ID, Target: ptr(pick(t, f, byID("password"))), Value: canary, Secret: "SHOP_PASSWORD"}); err != nil {
		t.Fatal(err)
	}
	if f, err = s.Act(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: ptr(pick(t, f, byDesc("Sign in")))}); err != nil {
		t.Fatal(err)
	}
	return f
}

func ptr[T any](v T) *T { return &v }

func (r *rig) write(rel, text string) {
	r.t.Helper()
	p := filepath.Join(r.repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		r.t.Fatal(err)
	}
}
