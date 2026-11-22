package webdriver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile/mobiletest"
	"github.com/jancuk/semar/internal/engine/mobile/webdriver"
)

func start(t *testing.T, o mobiletest.Options) (*mobiletest.Server, *webdriver.Session) {
	t.Helper()
	srv := mobiletest.NewServer(t, o)
	c := webdriver.New(srv.URL)
	if o.User != "" {
		c.User, c.Key = o.User, o.Key
	}
	s, err := c.NewSession(context.Background(), map[string]any{"platformName": "Android"})
	if err != nil {
		t.Fatal(err)
	}
	return srv, s
}

func TestStatus(t *testing.T) {
	for name, c := range map[string]struct {
		o    mobiletest.Options
		want bool
	}{"ready": {mobiletest.Options{}, true}, "not ready": {mobiletest.Options{NotReady: true}, false}, "no flag": {mobiletest.Options{NoStatusReady: true}, true}} {
		srv := mobiletest.NewServer(t, c.o)
		got, err := webdriver.New(srv.URL).Status(context.Background())
		if err != nil || got.Ready != c.want || got.Message != "fake server" {
			t.Errorf("%s: %+v, %v", name, got, err)
		}
	}
	// A server that is not there is an error, and it does not hold the URL.
	srv := mobiletest.NewServer(t, mobiletest.Options{})
	c := webdriver.New(srv.URL)
	srv.Close()
	if _, err := c.Status(context.Background()); err == nil || strings.Contains(err.Error(), srv.URL) {
		t.Errorf("error = %v", err)
	}
}

func TestNewSessionSendsAlwaysMatchAndReadsTheID(t *testing.T) {
	srv := mobiletest.NewServer(t, mobiletest.Options{})
	s, err := webdriver.New(srv.URL).NewSession(context.Background(), map[string]any{"platformName": "Android", "appium:x": 1})
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "sess-1" || srv.Sessions() != 1 {
		t.Errorf("id = %q, sessions %d", s.ID, srv.Sessions())
	}
	if srv.Capabilities()["platformName"] != "Android" {
		t.Errorf("caps = %v", srv.Capabilities())
	}
	var body struct {
		Capabilities struct {
			AlwaysMatch map[string]any   `json:"alwaysMatch"`
			FirstMatch  []map[string]any `json:"firstMatch"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(srv.Requests()[0].Body), &body); err != nil || len(body.Capabilities.FirstMatch) != 1 || len(body.Capabilities.FirstMatch[0]) != 0 {
		t.Errorf("body = %s (%v)", srv.Requests()[0].Body, err)
	}
	if len(s.Capabilities) == 0 {
		t.Error("the capabilities of the server were dropped")
	}
}

func TestALegacyServerGivesTheSessionIDAtTheTopAndElementsAsELEMENT(t *testing.T) {
	srv, s := start(t, mobiletest.Options{Legacy: true})
	if s.ID != "sess-1" {
		t.Errorf("id = %q", s.ID)
	}
	ctx := context.Background()
	// A field has the focus after a tap.
	if err := s.Touch(ctx, webdriver.Point{X: 5, Y: 5}, webdriver.Point{X: 5, Y: 5}, 0, 0); err != nil {
		t.Fatal(err)
	}
	id, err := s.Active(ctx)
	if err != nil || id != "e1" {
		t.Errorf("active = %q, %v", id, err)
	}
	_ = srv
}

func TestSourceScreenshotAndBack(t *testing.T) {
	srv, s := start(t, mobiletest.Options{})
	ctx := context.Background()
	srv.SetSource(mobiletest.Hierarchy(mobiletest.Widget("android.widget.Button", map[string]string{"text": "a<b&c"}, "[0,0][10,10]")))
	got, err := s.Source(ctx)
	if err != nil || !strings.Contains(got, `text="a&lt;b&amp;c"`) {
		t.Errorf("source = %q, %v", got, err)
	}
	shot, err := s.Screenshot(ctx)
	if err != nil || len(shot) < 8 || string(shot[1:4]) != "PNG" {
		t.Errorf("screenshot = %d bytes, %v", len(shot), err)
	}
	if err := s.Back(ctx); err != nil {
		t.Error(err)
	}
	if err := s.Delete(ctx); err != nil || srv.Deleted() != 1 {
		t.Errorf("delete: %v, %d", err, srv.Deleted())
	}
	// The session is gone as far as the fake knows only when it says so: a request with another id fails.
	other, _ := webdriver.New(srv.URL).Attach("sess-other")
	if _, err := other.Source(ctx); err == nil {
		t.Error("another session answered")
	} else {
		var e *webdriver.Error
		if !errors.As(err, &e) || e.Status != 404 || e.Code != "invalid session id" {
			t.Errorf("error = %v", err)
		}
	}
}

func TestTouchIsOneFingerMoveDownPauseMoveUp(t *testing.T) {
	srv, s := start(t, mobiletest.Options{})
	if err := s.Touch(context.Background(), webdriver.Point{X: 10, Y: 20}, webdriver.Point{X: 30, Y: 40}, 700*time.Millisecond, 300*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Actions []struct {
			Type       string         `json:"type"`
			ID         string         `json:"id"`
			Parameters map[string]any `json:"parameters"`
			Actions    []map[string]any
		} `json:"actions"`
	}
	req := srv.Requests()[len(srv.Requests())-1]
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil || len(body.Actions) != 1 {
		t.Fatalf("body = %s", req.Body)
	}
	a := body.Actions[0]
	if a.Type != "pointer" || a.Parameters["pointerType"] != "touch" || len(a.Actions) != 5 {
		t.Fatalf("pointer = %+v", a)
	}
	var kinds []string
	for _, x := range a.Actions {
		kinds = append(kinds, x["type"].(string))
	}
	if strings.Join(kinds, ",") != "pointerMove,pointerDown,pause,pointerMove,pointerUp" {
		t.Errorf("kinds = %v", kinds)
	}
	if a.Actions[2]["duration"] != float64(700) || a.Actions[3]["duration"] != float64(300) || a.Actions[3]["x"] != float64(30) || a.Actions[0]["y"] != float64(20) {
		t.Errorf("actions = %v", a.Actions)
	}
	touches := srv.Touches()
	if len(touches) != 1 || touches[0].From != [2]int{10, 20} || touches[0].To != [2]int{30, 40} || touches[0].Hold != 700 || touches[0].Move != 300 {
		t.Errorf("touches = %+v", touches)
	}
}

func TestTypingClearsThenSetsTheText(t *testing.T) {
	srv, s := start(t, mobiletest.Options{})
	ctx := context.Background()
	if _, err := s.Active(ctx); err == nil {
		t.Error("a field has the focus before any touch")
	}
	_ = s.Touch(ctx, webdriver.Point{X: 1, Y: 1}, webdriver.Point{X: 1, Y: 1}, 0, 0)
	id, err := s.Active(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Clear(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.SetValue(ctx, id, "ana@example.com"); err != nil {
		t.Fatal(err)
	}
	if got := srv.Typed(); len(got) != 1 || got[0] != "ana@example.com" {
		t.Errorf("typed = %v", got)
	}
	var sent map[string]any
	_ = json.Unmarshal([]byte(srv.Requests()[len(srv.Requests())-1].Body), &sent)
	if sent["text"] != "ana@example.com" {
		t.Errorf("body = %v", sent)
	}
	if err := s.SetValue(ctx, "../x", "t"); err == nil {
		t.Error("an element id with a slash was sent")
	}
	if err := s.Clear(ctx, "a b"); err == nil {
		t.Error("an element id with a space was sent")
	}
}

func TestSettingsKeycodeAndExecute(t *testing.T) {
	srv, s := start(t, mobiletest.Options{})
	ctx := context.Background()
	if err := s.Settings(ctx, map[string]any{"waitForIdleTimeout": 0, "waitForSelectorTimeout": 0}); err != nil {
		t.Fatal(err)
	}
	if got := srv.Settings(); got["waitForIdleTimeout"] != float64(0) || len(got) != 2 {
		t.Errorf("settings = %v", got)
	}
	if err := s.PressKeycode(ctx, 66); err != nil || len(srv.Keycodes()) != 1 || srv.Keycodes()[0] != 66 {
		t.Errorf("keycodes = %v, %v", srv.Keycodes(), err)
	}
	if _, err := s.Execute(ctx, "mobile: deepLink", map[string]any{"url": "shop://x", "package": "com.a.b"}); err != nil {
		t.Fatal(err)
	}
	got := srv.ExecutedScripts()
	if len(got) != 1 || got[0].Script != "mobile: deepLink" || got[0].Args["url"] != "shop://x" {
		t.Errorf("executed = %+v", got)
	}
	if _, err := s.Execute(ctx, "mobile: shake", nil); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("error = %v", err)
	}
	srv.OnExecute(func(script string, args map[string]any) (any, *mobiletest.Failure) { return "yes", nil })
	v, err := s.Execute(ctx, "mobile: pressKey", map[string]any{"keycode": 4})
	if err != nil || string(v) != `"yes"` {
		t.Errorf("value = %s, %v", v, err)
	}
}

func TestAServerWithNoExecuteAnswersAnError(t *testing.T) {
	_, s := start(t, mobiletest.Options{NoExecute: true})
	_, err := s.Execute(context.Background(), "mobile: deepLink", nil)
	var e *webdriver.Error
	if !errors.As(err, &e) || e.Status != 404 || e.Code != "unknown command" {
		t.Errorf("error = %v", err)
	}
}

func TestAnErrorIsTheFirstLineOfTheServerAndNeverTheStacktrace(t *testing.T) {
	srv, s := start(t, mobiletest.Options{})
	srv.Fail("GET /source", mobiletest.Failure{Status: 500, Code: "unknown error", Message: "The app crashed\nat com.x.Y(Y.java:1)"})
	_, err := s.Source(context.Background())
	var e *webdriver.Error
	if !errors.As(err, &e) || e.Status != 500 || e.Code != "unknown error" || e.Message != "The app crashed" {
		t.Fatalf("error = %#v", err)
	}
	if got := err.Error(); got != "the server answered 500 unknown error: The app crashed" {
		t.Errorf("text = %q", got)
	}
	if strings.Contains(err.Error(), "Y.java") || strings.Contains(err.Error(), "\n") {
		t.Errorf("the text holds a later line: %q", err)
	}
	// A message is cut short.
	srv.Fail("GET /source", mobiletest.Failure{Status: 500, Code: "x", Message: strings.Repeat("m", 1000)})
	_, err = s.Source(context.Background())
	if !errors.As(err, &e) || len(e.Message) > 310 {
		t.Errorf("message of %d bytes", len(e.Message))
	}
	// An error that comes with a status of 200 is an error too.
	srv.Heal("GET /source")
}

func TestATypedSecretIsNeverInAnError(t *testing.T) {
	const secret = "hunter2-very-secret"
	srv, s := start(t, mobiletest.Options{})
	srv.Fail("POST /element/e1/value", mobiletest.Failure{Status: 500, Code: "invalid element state", Message: "cannot set text " + secret})
	err := s.SetValue(context.Background(), "e1", secret)
	if err == nil {
		t.Fatal("no error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("the error holds the secret: %v", err)
	}
	var e *webdriver.Error
	if !errors.As(err, &e) || e.Code != "invalid element state" || e.Message != "" {
		t.Errorf("error = %#v", err)
	}
	// A transport error never holds the request either.
	srv.Close()
	err = s.SetValue(context.Background(), "e1", secret)
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), srv.URL) {
		t.Errorf("error = %v", err)
	}
}

func TestAnAnswerOverTheLimitIsRefused(t *testing.T) {
	srv, s := start(t, mobiletest.Options{})
	srv.SetSource(strings.Repeat("x", 5000))
	c := webdriver.New(srv.URL)
	c.MaxResponse = 1000
	big, _ := c.Attach("sess-1")
	_, err := big.Source(context.Background())
	if err == nil || !strings.Contains(err.Error(), "larger than 1000 bytes") {
		t.Errorf("error = %v", err)
	}
	// Under the limit it reads.
	c.MaxResponse = 6000
	if src, err := big.Source(context.Background()); err != nil || len(src) != 5000 {
		t.Errorf("source of %d bytes, %v", len(src), err)
	}
	_ = s
}

func TestAnAnswerThatIsNotJSONIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>proxy error</html>")) }))
	defer srv.Close()
	if _, err := webdriver.New(srv.URL).Status(context.Background()); err == nil || !strings.Contains(err.Error(), "did not answer with JSON") {
		t.Errorf("error = %v", err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		_, _ = w.Write([]byte("<html>bad gateway with a secret</html>"))
	}))
	defer bad.Close()
	_, err := webdriver.New(bad.URL).Status(context.Background())
	var e *webdriver.Error
	if !errors.As(err, &e) || e.Status != 502 || strings.Contains(err.Error(), "secret") {
		t.Errorf("error = %v", err)
	}
}

func TestASessionIDThatIsNotAnIDIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"a path":     `{"value":{"sessionId":"../x"}}`,
		"a space":    `{"value":{"sessionId":"a b"}}`,
		"none":       `{"value":{}}`,
		"top level":  `{"sessionId":"ok-1","value":{}}`,
		"no session": `{"value":null}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		s, err := webdriver.New(srv.URL).NewSession(context.Background(), nil)
		srv.Close()
		if (name == "top level") != (err == nil) {
			t.Errorf("%s: session %v, error %v", name, s, err)
		}
	}
	if _, err := webdriver.New("http://x").Attach("a/b"); err == nil {
		t.Error("Attach accepted a path")
	}
}

func TestAnElementIDOfAnotherKeyIsRead(t *testing.T) {
	for name, body := range map[string]struct {
		json string
		ok   bool
	}{
		"w3c":      {`{"value":{"element-6066-11e4-a52e-4f735466cecf":"abc"}}`, true},
		"legacy":   {`{"value":{"ELEMENT":"abc"}}`, true},
		"none":     {`{"value":{"other":"abc"}}`, false},
		"a path":   {`{"value":{"ELEMENT":"a/b"}}`, false},
		"a null":   {`{"value":null}`, false},
		"a string": {`{"value":"abc"}`, false},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/element/active") {
				_, _ = w.Write([]byte(body.json))
			}
		}))
		s, _ := webdriver.New(srv.URL).Attach("s1")
		id, err := s.Active(context.Background())
		srv.Close()
		if (err == nil) != body.ok || (body.ok && id != "abc") {
			t.Errorf("%s: id %q, error %v", name, id, err)
		}
	}
}

func TestACancelledContextStopsTheRequest(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := webdriver.New(srv.URL).Status(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Errorf("error = %v after %s", err, time.Since(start))
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	if _, err := webdriver.New(srv.URL).Status(ctx2); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v", err)
	}
}

func TestCredentialsAreSentAsBasicAuthAndRedirectsAreNotFollowed(t *testing.T) {
	srv, s := start(t, mobiletest.Options{User: "farm-user", Key: "farm-key"})
	if _, err := s.Source(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, r := range srv.Requests() {
		if r.User != "farm-user" {
			t.Errorf("request %s %s had user %q", r.Method, r.Path, r.User)
		}
	}
	// Wrong credentials: the error does not hold them.
	c := webdriver.New(srv.URL)
	c.User, c.Key = "farm-user", "wrong-key"
	_, err := c.Status(context.Background())
	var e *webdriver.Error
	if !errors.As(err, &e) || e.Status != 401 || strings.Contains(err.Error(), "wrong-key") {
		t.Errorf("error = %v", err)
	}
	// A redirect is an error and is not followed, so the credentials stay with the first host.
	hit := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer target.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redir.Close()
	c2 := webdriver.New(redir.URL)
	c2.User, c2.Key = "u", "k"
	if _, err := c2.Status(context.Background()); err == nil || hit {
		t.Errorf("error = %v, redirect followed = %v", err, hit)
	}
}

func TestALoopbackServerIsNeverReachedThroughAProxy(t *testing.T) {
	srv := mobiletest.NewServer(t, mobiletest.Options{})
	asked := false
	proxy := func(*http.Request) (*url.URL, error) {
		asked = true
		return url.Parse("http://127.0.0.1:1") // nothing listens there
	}
	c := &webdriver.Client{Base: srv.URL, HTTP: webdriver.NewHTTPClient(proxy)}
	if _, err := c.Status(context.Background()); err != nil || asked {
		t.Errorf("a loopback server was sent through the proxy: error %v, asked %v", err, asked)
	}
	// A host that is not on this machine does ask: the transport gives the proxy a say.
	req, _ := http.NewRequest("GET", "http://farm.example.com/status", nil)
	tr := webdriver.NewHTTPClient(proxy).Transport.(*http.Transport)
	if u, err := tr.Proxy(req); err != nil || u == nil || !asked {
		t.Errorf("proxy for a far host = %v, %v", u, err)
	}
	for _, host := range []string{"http://localhost:4723/", "http://[::1]:4723/", "http://127.0.0.2/", "http://a.localhost/"} {
		asked = false
		req, _ := http.NewRequest("GET", host, nil)
		if u, _ := tr.Proxy(req); u != nil || asked {
			t.Errorf("%s: proxy %v", host, u)
		}
	}
}
