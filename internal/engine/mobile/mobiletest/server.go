// Package mobiletest holds the fakes that the tests of the mobile engine, the Android platform, and the
// command line share: a scriptable W3C WebDriver server over HTTP, and a fake adb program. They run
// with no emulator and no device.
package mobiletest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

// Request is one request that the server got.
type Request struct {
	Method string
	Path   string
	// Body is what the client sent. A test may look for a secret in it, and the engine's evidence must
	// hold none.
	Body string
	// User is the user name of the basic authentication, or "".
	User string
}

// Failure makes one endpoint answer with an error.
type Failure struct {
	Status  int
	Code    string
	Message string
}

// Options changes how the server speaks.
type Options struct {
	// Legacy makes it answer as a JSONWP server: ELEMENT and a top-level sessionId.
	Legacy bool
	// NoExecute makes execute/sync an unknown command, as the bare UiAutomator2 server has it.
	NoExecute bool
	// NotReady makes /status say that the server is not ready.
	NotReady bool
	// User and Key, when set, are the credentials that the server requires.
	User, Key string
	// NoStatusReady leaves the ready flag out of /status.
	NoStatusReady bool
}

// Server is a fake W3C WebDriver server.
type Server struct {
	// URL is the address of the server, http://127.0.0.1:port.
	URL string

	t   testing.TB
	srv *httptest.Server
	o   Options

	mu        sync.Mutex
	source    string
	shot      []byte
	reqs      []Request
	failures  map[string]Failure
	sessionID string
	sessions  int
	deleted   int
	caps      map[string]any
	field     string
	typed     []string
	taps      [][2]int
	keycodes  []int
	executed  []Executed
	settings  map[string]any

	onTap     func(x, y int)
	onTouch   func(Touch)
	touches   []Touch
	onType    func(text string)
	onKeycode func(code int)
	onExecute func(script string, args map[string]any) (any, *Failure)
}

// Touch is one finger that a client sent: where it went down, where it came up, and for how long.
type Touch struct {
	From, To   [2]int
	Hold, Move int // milliseconds
}

// Executed is one script that a client ran.
type Executed struct {
	Script string
	Args   map[string]any
}

// NewServer starts a server that is closed when the test ends. It shows an empty screen until the test
// sets one.
func NewServer(t testing.TB, o Options) *Server {
	t.Helper()
	s := &Server{t: t, o: o, failures: map[string]Failure{}, source: Hierarchy(), sessionID: "sess-1", settings: map[string]any{}}
	s.shot = tinyPNG()
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	s.URL = s.srv.URL
	t.Cleanup(s.srv.Close)
	return s
}

// Close stops the server before the test ends.
func (s *Server) Close() { s.srv.Close() }

// SetSource sets the XML that /source gives.
func (s *Server) SetSource(xml string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.source = xml
}

// SetScreenshot sets the PNG that /screenshot gives.
func (s *Server) SetScreenshot(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shot = data
}

// OnTap runs fn for every touch that ends where it began, with the place. fn may call SetSource.
func (s *Server) OnTap(fn func(x, y int)) { s.mu.Lock(); s.onTap = fn; s.mu.Unlock() }

// OnTouch runs fn for every touch, a swipe included. fn may call SetSource.
func (s *Server) OnTouch(fn func(Touch)) { s.mu.Lock(); s.onTouch = fn; s.mu.Unlock() }

// Touches are the touches so far, in order.
func (s *Server) Touches() []Touch { s.mu.Lock(); defer s.mu.Unlock(); return slices.Clone(s.touches) }

// OnType runs fn with the text that a client typed in the focused field.
func (s *Server) OnType(fn func(text string)) { s.mu.Lock(); s.onType = fn; s.mu.Unlock() }

// OnKeycode runs fn for every key code.
func (s *Server) OnKeycode(fn func(code int)) { s.mu.Lock(); s.onKeycode = fn; s.mu.Unlock() }

// OnExecute runs fn for every script. It gives the value to answer with, or a failure.
func (s *Server) OnExecute(fn func(script string, args map[string]any) (any, *Failure)) {
	s.mu.Lock()
	s.onExecute = fn
	s.mu.Unlock()
}

// Fail makes the next requests of a kind answer with an error. key is "METHOD /suffix", where suffix is
// what follows /session/{id}, such as "POST /actions" or "GET /source", or "POST /session".
func (s *Server) Fail(key string, f Failure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[key] = f
}

// Heal removes a failure.
func (s *Server) Heal(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failures, key)
}

// Requests gives every request so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.reqs)
}

// Calls gives the requests as "METHOD /suffix", with the session id left out.
func (s *Server) Calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.reqs))
	for i, r := range s.reqs {
		out[i] = r.Method + " " + strings.Replace(r.Path, "/session/"+s.sessionID, "", 1)
	}
	return out
}

// Count is how many requests of a kind there were, in the form that Calls gives.
func (s *Server) Count(call string) int {
	n := 0
	for _, c := range s.Calls() {
		if c == call {
			n++
		}
	}
	return n
}

// Sessions is how many sessions were started, and Deleted how many were ended.
func (s *Server) Sessions() int { s.mu.Lock(); defer s.mu.Unlock(); return s.sessions }
func (s *Server) Deleted() int  { s.mu.Lock(); defer s.mu.Unlock(); return s.deleted }

// Capabilities are the alwaysMatch capabilities of the last session that was started.
func (s *Server) Capabilities() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.caps
}

// Typed is what the clients typed, one entry for each value request.
func (s *Server) Typed() []string { s.mu.Lock(); defer s.mu.Unlock(); return slices.Clone(s.typed) }

// Taps are the places that were touched.
func (s *Server) Taps() [][2]int { s.mu.Lock(); defer s.mu.Unlock(); return slices.Clone(s.taps) }

// Keycodes are the key codes that were pressed.
func (s *Server) Keycodes() []int { s.mu.Lock(); defer s.mu.Unlock(); return slices.Clone(s.keycodes) }

// ExecutedScripts are the scripts that were run, in order.
func (s *Server) ExecutedScripts() []Executed {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.executed)
}

// Settings are the settings that a client set.
func (s *Server) Settings() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]any{}
	for k, v := range s.settings {
		out[k] = v
	}
	return out
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	user, key, hasAuth := r.BasicAuth()
	req := Request{Method: r.Method, Path: r.URL.Path, Body: string(body)}
	if hasAuth {
		req.User = user
	}
	s.mu.Lock()
	s.reqs = append(s.reqs, req)
	s.mu.Unlock()

	if s.o.User != "" && (!hasAuth || user != s.o.User || key != s.o.Key) {
		fail(w, 401, "unauthorized", "the credentials are wrong")
		return
	}
	path := r.URL.Path
	suffix := ""
	switch {
	case path == "/status" && r.Method == http.MethodGet:
		s.status(w)
		return
	case path == "/session" && r.Method == http.MethodPost:
		if f, ok := s.failure("POST /session"); ok {
			fail(w, f.Status, f.Code, f.Message)
			return
		}
		s.newSession(w, body)
		return
	}
	rest, ok := strings.CutPrefix(path, "/session/")
	if !ok {
		fail(w, 404, "unknown command", "unknown command")
		return
	}
	id, suffix, _ := strings.Cut(rest, "/")
	if suffix != "" {
		suffix = "/" + suffix
	}
	if id != s.sessionID {
		fail(w, 404, "invalid session id", "no such session")
		return
	}
	if f, ok := s.failure(r.Method + " " + suffix); ok {
		fail(w, f.Status, f.Code, f.Message)
		return
	}
	switch {
	case r.Method == http.MethodDelete && suffix == "":
		s.mu.Lock()
		s.deleted++
		s.mu.Unlock()
		reply(w, nil)
	case r.Method == http.MethodGet && suffix == "/source":
		s.mu.Lock()
		src := s.source
		s.mu.Unlock()
		reply(w, src)
	case r.Method == http.MethodGet && suffix == "/screenshot":
		s.mu.Lock()
		shot := s.shot
		s.mu.Unlock()
		reply(w, base64.StdEncoding.EncodeToString(shot))
	case r.Method == http.MethodPost && suffix == "/actions":
		s.actions(w, body)
	case r.Method == http.MethodGet && suffix == "/element/active":
		s.mu.Lock()
		has := s.field != ""
		s.mu.Unlock()
		if !has {
			fail(w, 404, "no such element", "no element has the focus")
			return
		}
		reply(w, s.elementRef("e1"))
	case r.Method == http.MethodPost && strings.HasPrefix(suffix, "/element/") && strings.HasSuffix(suffix, "/clear"):
		s.mu.Lock()
		s.field = "cleared"
		s.mu.Unlock()
		reply(w, nil)
	case r.Method == http.MethodPost && strings.HasPrefix(suffix, "/element/") && strings.HasSuffix(suffix, "/value"):
		s.value(w, body)
	case r.Method == http.MethodPost && suffix == "/back":
		reply(w, nil)
	case r.Method == http.MethodPost && suffix == "/appium/settings":
		var in struct {
			Settings map[string]any `json:"settings"`
		}
		_ = json.Unmarshal(body, &in)
		s.mu.Lock()
		for k, v := range in.Settings {
			s.settings[k] = v
		}
		s.mu.Unlock()
		reply(w, nil)
	case r.Method == http.MethodPost && suffix == "/appium/device/press_keycode":
		var in struct {
			Keycode int `json:"keycode"`
		}
		_ = json.Unmarshal(body, &in)
		s.mu.Lock()
		s.keycodes = append(s.keycodes, in.Keycode)
		fn := s.onKeycode
		s.mu.Unlock()
		if fn != nil {
			fn(in.Keycode)
		}
		reply(w, nil)
	case r.Method == http.MethodPost && suffix == "/execute/sync":
		s.execute(w, body)
	default:
		fail(w, 404, "unknown command", "unknown command")
	}
}

func (s *Server) failure(key string) (Failure, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.failures[key]
	return f, ok
}

func (s *Server) status(w http.ResponseWriter) {
	v := map[string]any{"message": "fake server"}
	if !s.o.NoStatusReady {
		v["ready"] = !s.o.NotReady
	}
	reply(w, v)
}

func (s *Server) newSession(w http.ResponseWriter, body []byte) {
	var in struct {
		Capabilities struct {
			AlwaysMatch map[string]any `json:"alwaysMatch"`
			FirstMatch  []any          `json:"firstMatch"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		fail(w, 400, "invalid argument", "the capabilities do not parse")
		return
	}
	s.mu.Lock()
	s.sessions++
	s.caps = in.Capabilities.AlwaysMatch
	s.mu.Unlock()
	if s.o.Legacy {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"sessionId": s.sessionID, "status": 0, "value": map[string]any{"platformName": "Android"}})
		return
	}
	reply(w, map[string]any{"sessionId": s.sessionID, "capabilities": in.Capabilities.AlwaysMatch})
}

func (s *Server) elementRef(id string) map[string]any {
	if s.o.Legacy {
		return map[string]any{"ELEMENT": id}
	}
	return map[string]any{"element-6066-11e4-a52e-4f735466cecf": id}
}

func (s *Server) actions(w http.ResponseWriter, body []byte) {
	var in struct {
		Actions []struct {
			Actions []struct {
				Type     string `json:"type"`
				X        int    `json:"x"`
				Y        int    `json:"y"`
				Duration int    `json:"duration"`
			} `json:"actions"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(body, &in); err != nil || len(in.Actions) != 1 {
		fail(w, 400, "invalid argument", "the actions do not parse")
		return
	}
	var moves [][2]int
	touch := Touch{}
	for _, a := range in.Actions[0].Actions {
		switch a.Type {
		case "pointerMove":
			moves = append(moves, [2]int{a.X, a.Y})
			touch.Move = a.Duration
		case "pause":
			touch.Hold = a.Duration
		}
	}
	if len(moves) != 2 {
		fail(w, 400, "invalid argument", "a touch is a move, a down, a pause, a move, and an up")
		return
	}
	touch.From, touch.To = moves[0], moves[1]
	s.mu.Lock()
	s.touches = append(s.touches, touch)
	fnTouch, fnTap := s.onTouch, s.onTap
	if moves[0] == moves[1] {
		s.taps = append(s.taps, moves[1])
		s.field = "focused"
	}
	s.mu.Unlock()
	if fnTouch != nil {
		fnTouch(touch)
	}
	if moves[0] == moves[1] && fnTap != nil {
		fnTap(moves[1][0], moves[1][1])
	}
	reply(w, nil)
}

func (s *Server) value(w http.ResponseWriter, body []byte) {
	var in struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		fail(w, 400, "invalid argument", "the text does not parse")
		return
	}
	s.mu.Lock()
	s.typed = append(s.typed, in.Text)
	fn := s.onType
	s.mu.Unlock()
	if fn != nil {
		fn(in.Text)
	}
	reply(w, nil)
}

func (s *Server) execute(w http.ResponseWriter, body []byte) {
	if s.o.NoExecute {
		fail(w, 404, "unknown command", "unknown command: POST /execute/sync")
		return
	}
	var in struct {
		Script string           `json:"script"`
		Args   []map[string]any `json:"args"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		fail(w, 400, "invalid argument", "the script does not parse")
		return
	}
	args := map[string]any{}
	if len(in.Args) > 0 {
		args = in.Args[0]
	}
	known := []string{"mobile: deepLink", "mobile: pressKey", "mobile: activateApp", "mobile: terminateApp", "mobile: clearApp", "mobile: hideKeyboard"}
	if !slices.Contains(known, in.Script) {
		fail(w, 400, "unknown command", fmt.Sprintf("Unknown mobile command %q", in.Script))
		return
	}
	s.mu.Lock()
	s.executed = append(s.executed, Executed{Script: in.Script, Args: args})
	fn := s.onExecute
	s.mu.Unlock()
	if fn != nil {
		v, f := fn(in.Script, args)
		if f != nil {
			fail(w, f.Status, f.Code, f.Message)
			return
		}
		reply(w, v)
		return
	}
	reply(w, nil)
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"value": v})
}

func fail(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"value": map[string]any{"error": code, "message": message, "stacktrace": "at fake.server(1)\nat other(2)"}})
}

func tinyPNG() []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 16)))
	return b.Bytes()
}

// El is one element of an Android hierarchy in the form of the UiAutomator2 source: the class is the tag,
// and the attributes are written in sorted order.
func El(class string, attrs map[string]string, kids ...string) string {
	var b strings.Builder
	b.WriteString("<" + class)
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, " %s=\"%s\"", k, html.EscapeString(attrs[k]))
	}
	if len(kids) == 0 {
		b.WriteString("/>")
		return b.String()
	}
	b.WriteString(">")
	for _, k := range kids {
		b.WriteString(k)
	}
	b.WriteString("</" + class + ">")
	return b.String()
}

// Hierarchy wraps elements in a hierarchy of a 1080 by 2400 screen.
func Hierarchy(kids ...string) string {
	return `<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>` + El("hierarchy", map[string]string{"index": "0", "class": "hierarchy", "rotation": "0", "width": "1080", "height": "2400"}, kids...)
}

// Widget is an element with the attributes of a widget that is shown: it has bounds, is enabled and
// displayed. The bounds are [x1,y1][x2,y2].
func Widget(class string, attrs map[string]string, bounds string, kids ...string) string {
	a := map[string]string{"class": class, "bounds": bounds, "enabled": "true", "displayed": "true", "index": "0", "package": "com.example.shop",
		"checkable": "false", "checked": "false", "clickable": "false", "focusable": "false", "focused": "false", "long-clickable": "false",
		"password": "false", "scrollable": "false", "selected": "false", "text": "", "content-desc": "", "resource-id": ""}
	for k, v := range attrs {
		a[k] = v
	}
	return El(class, a, kids...)
}
