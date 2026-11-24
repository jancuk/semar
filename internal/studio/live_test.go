package studio_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/localhttp"
	"github.com/jancuk/semar/internal/mobilerec"
	"github.com/jancuk/semar/internal/studio"
	"github.com/jancuk/semar/internal/takeover"
)

const typedCanary = "hunter2-typed-canary"

// fakeLive is a LiveHost over a real baton, so that a long poll, a pause, and a hand-back behave as they
// do in the command.
type fakeLive struct {
	b *takeover.Baton

	mu       sync.Mutex
	waits    []time.Duration
	acts     []studio.LiveAct
	typed    []string
	secret   bool
	observe  bool
	recorded []bool
}

func newFakeLive() *fakeLive { return &fakeLive{b: takeover.New(nil)} }

func (f *fakeLive) view(st takeover.State, evs []takeover.Event) studio.LiveState {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := studio.LiveState{
		Version: st.Version, Owner: st.Owner.String(), Pending: st.Pending, Asked: st.Asked, Reason: st.Reason, Note: st.Note,
		Since: st.Since, Mode: "live", RecordHuman: st.RecordHuman, Width: 1080, Height: 2400, Secret: f.secret, Observe: f.observe,
		Windows: []studio.LiveWindow{{Kind: "app", Package: "com.shop", Elements: []studio.LiveElement{{Index: 0, Kind: "button", Text: "Sign in", Right: 400, Bottom: 100, Enabled: true, Clickable: true}}}},
		Steps:   []studio.LiveStep{},
	}
	for _, e := range evs {
		out.Events = append(out.Events, studio.LiveEvent{Seq: e.Seq, At: e.At, Who: e.Who.String(), Tool: e.Tool, Surface: e.Surface, OK: e.OK, MS: e.MS, Summary: e.Summary})
	}
	return out
}

func (f *fakeLive) State(ctx context.Context, since uint64, wait time.Duration) (studio.LiveState, error) {
	f.mu.Lock()
	f.waits = append(f.waits, wait)
	f.mu.Unlock()
	st, evs := f.b.Watch(ctx, since, wait)
	return f.view(st, evs), nil
}

func (f *fakeLive) Screen(context.Context) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.secret {
		return nil, &studio.LiveError{Status: http.StatusForbidden, Msg: "the screen shows a secret value, so its picture is hidden"}
	}
	return []byte{0xff, 0xd8, 0xff, 0xe0, 'j', 'p', 'g'}, nil
}

func (f *fakeLive) Pause(ctx context.Context, reason string) error {
	return f.b.Pause(ctx, reason, time.Second)
}

func (f *fakeLive) Resume(_ context.Context, note string) error { f.b.Resume(note); return nil }

func (f *fakeLive) Act(ctx context.Context, a studio.LiveAct) (studio.LiveState, error) {
	f.mu.Lock()
	observe := f.observe
	f.mu.Unlock()
	_, leave, err := f.b.Enter(ctx, takeover.Human, true)
	if err != nil {
		return studio.LiveState{}, studio.ErrAgentOwns
	}
	defer leave(true, "")
	if observe {
		return studio.LiveState{}, &studio.LiveError{Status: http.StatusForbidden, Msg: "this server only observes"}
	}
	summary := a.Do
	if a.Text != nil {
		f.mu.Lock()
		f.typed = append(f.typed, *a.Text)
		f.mu.Unlock()
		summary = fmt.Sprintf("[typed %d chars]", len([]rune(*a.Text)))
	}
	f.mu.Lock()
	f.acts = append(f.acts, a)
	f.mu.Unlock()
	f.b.RecordEvent(takeover.Event{Who: takeover.Human, Tool: "human " + a.Do, Surface: "mobile", OK: true, Summary: summary})
	return f.State(ctx, 0, 0)
}

func (f *fakeLive) SetRecordHuman(on bool) error {
	f.b.SetRecordHuman(on)
	f.mu.Lock()
	f.recorded = append(f.recorded, on)
	f.mu.Unlock()
	return nil
}

func (f *fakeLive) actCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.acts) }

func startLive(t *testing.T) (*client, *fakeLive) {
	t.Helper()
	host := newFakeLive()
	c, _ := start(t, studio.Deps{Live: host})
	return c, host
}

func decodeLive(t *testing.T, data []byte) studio.LiveState {
	t.Helper()
	var st studio.LiveState
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("not a state: %v\n%s", err, data)
	}
	return st
}

func TestLiveOnlyStudioServesTheLivePageAndNothingElse(t *testing.T) {
	c, _ := startLive(t)
	for _, path := range []string{"/", "/live.js", "/app.js", "/style.css"} {
		res, err := http.Get(c.base + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 || len(body) == 0 {
			t.Errorf("GET %s = %d", path, res.StatusCode)
		}
		if path == "/" && !strings.Contains(string(body), `src="live.js"`) {
			t.Error("the page does not load live.js")
		}
	}
	for _, path := range []string{"/api/state", "/api/session/refresh", "/api/devices"} {
		code, _ := c.do("GET", path, nil)
		if code != 404 {
			t.Errorf("GET %s = %d, want 404 in a live-only studio", path, code)
		}
	}
	code, body := c.do("GET", "/api/live/state", nil)
	if code != 200 || decodeLive(t, body).Owner != "agent" {
		t.Errorf("state = %d %s", code, body)
	}
}

func TestStudioNeedsTheRepositoryUnlessItIsLiveOnly(t *testing.T) {
	if _, err := studio.New(studio.Deps{}); err == nil {
		t.Error("a studio with nothing to do was made")
	}
	if _, err := studio.New(studio.Deps{Live: newFakeLive(), Repo: t.TempDir()}); err == nil {
		t.Error("a studio with a repository and no way to record was made")
	}
}

func TestLiveRoutesNeedTheToken(t *testing.T) {
	c, host := startLive(t)
	routes := []struct{ method, path, body string }{
		{"GET", "/api/live/state", ""}, {"GET", "/api/live/screen", ""},
		{"POST", "/api/live/pause", "{}"}, {"POST", "/api/live/resume", "{}"},
		{"POST", "/api/live/act", `{"do":"tap","x":1,"y":1}`}, {"POST", "/api/live/record", `{"on":true}`},
	}
	for _, r := range routes {
		for name, edit := range map[string]func(*http.Request){
			"no token":    func(q *http.Request) { q.Header.Del(localhttp.TokenHeader) },
			"wrong token": func(q *http.Request) { q.Header.Set(localhttp.TokenHeader, strings.Repeat("a", 64)) },
			"bearer only": func(q *http.Request) {
				q.Header.Del(localhttp.TokenHeader)
				q.Header.Set("Authorization", "Bearer "+c.token)
			},
			"other origin": func(q *http.Request) {
				q.Header.Set("Origin", "http://evil.example")
			},
			"other host": func(q *http.Request) { q.Host = "evil.example" },
		} {
			var body any
			if r.body != "" {
				body = r.body
			}
			code, _ := c.do(r.method, r.path, body, edit)
			if code != http.StatusForbidden {
				t.Errorf("%s %s with %s = %d, want 403", r.method, r.path, name, code)
			}
		}
	}
	// A change must be JSON.
	code, _ := c.do("POST", "/api/live/pause", "{}", func(q *http.Request) { q.Header.Set("Content-Type", "text/plain") })
	if code != http.StatusUnsupportedMediaType {
		t.Errorf("a text body = %d, want 415", code)
	}
	if host.actCount() != 0 {
		t.Error("a refused request reached the host")
	}
	if host.b.State().Owner != takeover.Agent {
		t.Error("a refused request moved the baton")
	}
}

func TestLongPollWakesOnAVersionChangeAndIsCapped(t *testing.T) {
	c, host := startLive(t)
	_, body := c.do("GET", "/api/live/state", nil)
	v := decodeLive(t, body).Version

	type answer struct {
		st studio.LiveState
		at time.Duration
	}
	got := make(chan answer, 1)
	started := time.Now()
	go func() {
		_, body := c.do("GET", fmt.Sprintf("/api/live/state?since=%d&wait=20", v), nil)
		got <- answer{decodeLive(t, body), time.Since(started)}
	}()
	time.Sleep(150 * time.Millisecond)
	select {
	case <-got:
		t.Fatal("the long poll returned before anything changed")
	default:
	}
	host.b.RecordEvent(takeover.Event{Who: takeover.Agent, Tool: "mobile_list_elements_on_screen", Surface: "mobile", OK: true, MS: 12})
	select {
	case a := <-got:
		if a.at > 5*time.Second {
			t.Errorf("the poll woke after %v", a.at)
		}
		if a.st.Version <= v || len(a.st.Events) != 1 || a.st.Events[0].Tool != "mobile_list_elements_on_screen" || a.st.Events[0].Who != "agent" {
			t.Errorf("state = %+v", a.st)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the long poll did not wake on a change")
	}

	// A wait past 25 seconds is cut to 25, and a poll with a newer version than the host has returns on time.
	c.do("GET", "/api/live/state?since=0&wait=999", nil)
	host.mu.Lock()
	last := host.waits[len(host.waits)-1]
	host.mu.Unlock()
	if last != 25*time.Second {
		t.Errorf("wait = %v, want 25s", last)
	}
	_, body = c.do("GET", "/api/live/state?wait=0", nil)
	begin := time.Now()
	_, body = c.do("GET", fmt.Sprintf("/api/live/state?since=%d&wait=0.2", decodeLive(t, body).Version), nil)
	if d := time.Since(begin); d < 150*time.Millisecond || d > 3*time.Second {
		t.Errorf("a poll with nothing new took %v", d)
	}

	for _, bad := range []string{"since=x", "since=-1", "wait=x", "wait=-3", "wait=NaN", "wait=Inf", "wait=-Inf", "wait=%2BInf"} {
		if code, _ := c.do("GET", "/api/live/state?"+bad, nil); code != 400 {
			t.Errorf("%s = %d, want 400", bad, code)
		}
	}
}

func TestPollingIsTheHeartbeat(t *testing.T) {
	c, host := startLive(t)
	host.b.SetHeartbeatTimeout(300 * time.Millisecond)
	if code, _ := c.do("POST", "/api/live/pause", "{}"); code != 200 {
		t.Fatalf("pause = %d", code)
	}
	// A page that keeps polling keeps control.
	for i := 0; i < 4; i++ {
		time.Sleep(120 * time.Millisecond)
		_, body := c.do("GET", "/api/live/state?wait=0", nil)
		if decodeLive(t, body).Owner != "human" {
			t.Fatal("control went back while the studio polled")
		}
	}
	// A page that went away loses it, with a note that says so.
	time.Sleep(700 * time.Millisecond)
	st := host.b.State()
	if st.Owner != takeover.Agent || st.Note != "the studio went away" {
		t.Errorf("state = %+v", st)
	}
}

func TestActIsRefusedWhileTheAgentOwnsTheDevice(t *testing.T) {
	c, host := startLive(t)
	for _, body := range []string{`{"do":"tap","x":10,"y":20}`, `{"do":"type","text":"` + typedCanary + `"}`, `{"do":"press","key":"BACK"}`} {
		code, data := c.do("POST", "/api/live/act", body)
		if code != http.StatusConflict || !strings.Contains(string(data), "press Pause") {
			t.Errorf("act %s = %d %s, want 409 with a hint", body, code, data)
		}
	}
	if host.actCount() != 0 {
		t.Error("an act of a person reached the device while the agent drove")
	}

	// Pause, act, resume.
	code, data := c.do("POST", "/api/live/pause", map[string]string{"reason": "look at the cart"})
	st := decodeLive(t, data)
	if code != 200 || st.Owner != "human" || st.Reason != "look at the cart" {
		t.Fatalf("pause = %d %+v", code, st)
	}
	if code, data = c.do("POST", "/api/live/act", `{"do":"tap","x":10,"y":20}`); code != 200 {
		t.Fatalf("act = %d %s", code, data)
	}
	if host.actCount() != 1 {
		t.Errorf("acts = %d", host.actCount())
	}
	code, data = c.do("POST", "/api/live/resume", map[string]string{"note": "entered the code"})
	st = decodeLive(t, data)
	if code != 200 || st.Owner != "agent" || st.Note != "entered the code" {
		t.Fatalf("resume = %d %+v", code, st)
	}
	if code, _ = c.do("POST", "/api/live/act", `{"do":"tap","x":1,"y":1}`); code != http.StatusConflict {
		t.Errorf("act after the hand-back = %d, want 409", code)
	}
}

func TestRecordingCallsAnswer409WhileTheAgentOwnsTheDevice(t *testing.T) {
	host := newFakeLive()
	c, _ := start(t, studio.Deps{
		Live: host, Repo: t.TempDir(),
		Status: func() (studio.Status, error) { return studio.Status{HasConfig: true}, nil },
		NewSession: func(studio.SessionRequest) (*mobilerec.Session, error) {
			return nil, errors.New("no device in this test")
		},
		Verify: func(context.Context, *mobilerec.Session, *mobilerec.Saved) (studio.VerifyResult, error) {
			return studio.VerifyResult{}, nil
		},
	})
	calls := []struct{ method, path, body string }{
		{"POST", "/api/session/start", `{"kind":"scenario"}`},
		{"POST", "/api/session/refresh", `{}`},
		{"POST", "/api/session/act", `{"do":"tap","x":1,"y":1}`},
		{"POST", "/api/session/assert", `{}`},
		{"POST", "/api/session/use-flow", `{}`},
		{"POST", "/api/session/reset", `{}`},
		{"POST", "/api/finish", `{}`},
		{"POST", "/api/verify", `{}`},
		{"POST", "/api/undo", `{}`},
		{"POST", "/api/timeline/goto", `{}`},
		{"DELETE", "/api/timeline/last", `{}`},
		{"DELETE", "/api/timeline/1", `{}`},
	}
	for _, r := range calls {
		var body any
		if r.body != "" {
			body = r.body
		}
		code, data := c.do(r.method, r.path, body)
		if code != http.StatusConflict || !strings.Contains(string(data), "the agent owns the device: press Pause") {
			t.Errorf("%s %s = %d %s, want 409", r.method, r.path, code, data)
		}
	}
	// What only reads still answers.
	if code, _ := c.do("GET", "/api/state", nil); code != 200 {
		t.Errorf("GET /api/state = %d", code)
	}
	// Once the person holds control, the call reaches the recording and fails there, not with a 409.
	if code, _ := c.do("POST", "/api/live/pause", "{}"); code != 200 {
		t.Fatal("pause")
	}
	if _, data := c.do("POST", "/api/session/refresh", `{}`); strings.Contains(string(data), "the agent owns the device") {
		t.Errorf("the refresh was refused as the agent's while the person holds control: %s", data)
	}
}

func TestScreenIsRefusedOnASecretScreen(t *testing.T) {
	c, host := startLive(t)
	req, _ := http.NewRequest("GET", c.base+"/api/live/screen", nil)
	req.Header.Set(localhttp.TokenHeader, c.token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/jpeg" || !bytes.HasPrefix(data, []byte{0xff, 0xd8}) {
		t.Errorf("screen = %d %q % x", res.StatusCode, res.Header.Get("Content-Type"), data)
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", res.Header.Get("Cache-Control"))
	}
	host.mu.Lock()
	host.secret = true
	host.mu.Unlock()
	code, body := c.do("GET", "/api/live/screen", nil)
	if code != http.StatusForbidden || bytes.HasPrefix(body, []byte{0xff, 0xd8}) {
		t.Errorf("screen of a secret = %d %q", code, body)
	}
	_, state := c.do("GET", "/api/live/state", nil)
	if !decodeLive(t, state).Secret {
		t.Error("the state does not say that the screen is secret")
	}
}

func TestTypedTextIsInNoAnswerAndTheFeedSaysOnlyHowMany(t *testing.T) {
	c, host := startLive(t)
	if code, _ := c.do("POST", "/api/live/pause", "{}"); code != 200 {
		t.Fatal("pause")
	}
	code, body := c.do("POST", "/api/live/act", map[string]any{"do": "type", "text": typedCanary, "submit": true})
	if code != 200 {
		t.Fatalf("act = %d %s", code, body)
	}
	st := decodeLive(t, body)
	var line string
	for _, e := range st.Events {
		if e.Who == "human" {
			line = e.Summary
		}
	}
	if want := fmt.Sprintf("[typed %d chars]", len(typedCanary)); line != want {
		t.Errorf("the feed says %q, want %q", line, want)
	}
	c.do("GET", "/api/live/state?since=0&wait=0", nil)
	c.do("POST", "/api/live/record", map[string]bool{"on": false})
	c.do("POST", "/api/live/resume", map[string]string{"note": "done"})
	c.mu.Lock()
	seen := c.seen.String()
	c.mu.Unlock()
	if strings.Contains(seen, typedCanary) {
		t.Errorf("the typed text is in an answer of the studio:\n%s", seen)
	}
	host.mu.Lock()
	typed := append([]string(nil), host.typed...)
	host.mu.Unlock()
	if len(typed) != 1 || typed[0] != typedCanary {
		t.Errorf("the host got %v", typed)
	}
}

func TestObserveModeLetsAPersonPauseButNotAct(t *testing.T) {
	c, host := startLive(t)
	host.mu.Lock()
	host.observe = true
	host.mu.Unlock()
	if code, _ := c.do("POST", "/api/live/pause", "{}"); code != 200 {
		t.Fatalf("pause = %d", code)
	}
	code, data := c.do("POST", "/api/live/act", `{"do":"tap","x":1,"y":1}`)
	if code != http.StatusForbidden || !strings.Contains(string(data), "observes") {
		t.Errorf("act in observe mode = %d %s", code, data)
	}
	if host.actCount() != 0 {
		t.Error("an act reached the device in observe mode")
	}
	if code, _ := c.do("POST", "/api/live/resume", `{}`); code != 200 {
		t.Error("resume")
	}
}

func TestLiveRequestsAreValidated(t *testing.T) {
	c, host := startLive(t)
	cases := []struct{ name, path, body string }{
		{"an operation that is not known", "/api/live/act", `{"do":"teleport"}`},
		{"no operation", "/api/live/act", `{}`},
		{"an unknown field", "/api/live/act", `{"do":"tap","evil":true}`},
		{"text that is too long", "/api/live/act", `{"do":"type","text":"` + strings.Repeat("a", 4097) + `"}`},
		{"a reason that is too long", "/api/live/pause", `{"reason":"` + strings.Repeat("a", 201) + `"}`},
		{"a note that is too long", "/api/live/resume", `{"note":"` + strings.Repeat("a", 501) + `"}`},
		{"a record call with no value", "/api/live/record", `{}`},
		{"two values", "/api/live/record", `{"on":true}{"on":false}`},
	}
	for _, tc := range cases {
		if code, data := c.do("POST", tc.path, tc.body); code != 400 {
			t.Errorf("%s: %d %s, want 400", tc.name, code, data)
		}
	}
	if host.b.State().Owner != takeover.Agent || host.actCount() != 0 {
		t.Error("a bad request had an effect")
	}
	if code, _ := c.do("POST", "/api/live/record", `{"on":false}`); code != 200 {
		t.Error("record")
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if len(host.recorded) != 1 || host.recorded[0] {
		t.Errorf("record calls = %v", host.recorded)
	}
}

func TestTheLivePageIsServedBehindTheSameChecks(t *testing.T) {
	c, _ := startLive(t)
	req, _ := http.NewRequest("GET", c.base+"/live.js", nil)
	req.Host = "evil.example"
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("live.js to another Host = %d", res.StatusCode)
	}
	ts := httptest.NewServer(http.NotFoundHandler())
	ts.Close()
}
