// Package studio is the local web page that records an app on a device: semar record --ui. It is a thin
// front end of mobilerec.Session, the API that the terminal shares. It serves on the loopback address
// only, behind the checks of internal/localhttp, and it holds no secret: the one that a person types
// lives in the overlay of the session, in memory, and is in no answer, no error, and no file.
package studio

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/localhttp"
	"github.com/jancuk/semar/internal/mobilerec"
	"github.com/jancuk/semar/internal/screens"
)

const (
	maxBody      = 64 << 10
	closeTimeout = 15 * time.Second
	inspectLimit = 10 * time.Second
	previewLimit = 16 << 10
	// defaultID is what a recording is called until the person names it at the end.
	defaultID = "recording"
)

// SessionRequest is what a new recording needs from the command that knows the repository.
type SessionRequest struct {
	Kind    mobilerec.Kind
	ID      string
	Ticket  string
	Title   string
	Service string
	// Serial is the device to record on. Empty is the device that adb gives.
	Serial string
	// Use is a flow that the scenario starts with, with its params.
	Use     string
	With    map[string]string
	Replace bool
	// ConfirmClear says that the person agreed to reset: clear on a physical device.
	ConfirmClear bool
	// Resume reopens the saved flow or scenario named by Kind and ID: its files are loaded, and the steps
	// are replayed up to ResumeAt (0 is all of them) when the session starts.
	Resume   bool
	ResumeAt int
}

// Recording is a saved flow or scenario that the studio can reopen.
type Recording struct {
	// Kind is flow or scenario.
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
}

// Status is what the repository says about recording: whether there is a configuration, and which app.
type Status struct {
	HasConfig   bool     `json:"hasConfig"`
	Environment string   `json:"env,omitempty"`
	Service     string   `json:"service,omitempty"`
	App         string   `json:"app,omitempty"`
	Services    []string `json:"services,omitempty"`
	// Problem says in plain words why the repository cannot record yet. It has no secret in it.
	Problem string `json:"problem,omitempty"`
}

// VerifyStep is what one step of a verify did.
type VerifyStep struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// VerifyResult is how a verify ended. A flow completes and never passes: it has no check.
type VerifyResult struct {
	// Status is completed, passed, failed, refused, or the outcome of the scenario.
	Status  string       `json:"status"`
	Message string       `json:"message"`
	Steps   []VerifyStep `json:"steps,omitempty"`
	Report  string       `json:"report,omitempty"`
}

// Deps are what the studio takes from the command. Tests replace each of them.
type Deps struct {
	// Repo is the repository that recordings are written to.
	Repo string
	// Status reads the configuration again each time, so that a guided setup is seen at once.
	Status func() (Status, error)
	// NewSession makes a session that has not started. The command decides the environment, the service,
	// and the way to open the device.
	NewSession func(SessionRequest) (*mobilerec.Session, error)
	// Verify runs what the save wrote on a clean device session. The studio has given the device away
	// before it is called, and takes it back after, so the callback opens its own.
	Verify func(ctx context.Context, sess *mobilerec.Session, saved *mobilerec.Saved) (VerifyResult, error)
	// Inspector lists the devices and the app in front. It is optional.
	Inspector mobile.AppInspector
	// Recordable lists the saved flows and the Android scenarios that can be reopened. It is optional.
	Recordable func() ([]Recording, error)
	// TicketOf is the default ticket of a scenario ID. It is optional.
	TicketOf func(id string) string
	// Live makes the studio a live one: a person watches an agent drive the device, takes it over, and
	// hands it back. With only Live set (no Repo, Status, NewSession, or Verify) the studio serves the
	// live page and nothing else. With both, the recording calls answer 409 while the agent owns the device.
	Live LiveHost
}

// Server is the studio.
type Server struct {
	d       Deps
	token   string
	handler http.Handler
	base    context.Context

	// mu serialises every call that reads or changes the session, so that two clicks never act on the
	// device at once.
	mu       sync.Mutex
	sess     *mobilerec.Session
	service  string
	physical bool
}

// New makes a studio with a new random token.
func New(d Deps) (*Server, error) {
	switch {
	case d.Live != nil && d.Repo == "" && d.Status == nil && d.NewSession == nil && d.Verify == nil:
		// A live-only studio: it serves the live page and has no recording to start.
	case d.Repo == "":
		return nil, errors.New("the studio needs the repository to record in")
	case d.Status == nil, d.NewSession == nil, d.Verify == nil:
		return nil, errors.New("the studio is missing one of its dependencies")
	}
	token, err := localhttp.NewToken()
	if err != nil {
		return nil, fmt.Errorf("make the studio token: %w", err)
	}
	s := &Server{d: d, token: token, base: context.Background()}
	s.handler = s.routes()
	return s, nil
}

// Token is the secret that the printed address carries.
func (s *Server) Token() string { return s.token }

// Handler is the whole studio, behind its checks.
func (s *Server) Handler() http.Handler { return s.handler }

// Serve answers on ln until ctx ends, then gives the device away and drops the secrets.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.base = ctx
	return localhttp.Serve(ctx, ln, s.handler, s.Close)
}

// Close ends the session, so that the device is free, and drops its secrets. It is safe to call twice.
func (s *Server) Close() {
	s.mu.Lock()
	sess := s.sess
	s.sess = nil
	s.mu.Unlock()
	if sess == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	_ = sess.Close(ctx)
	sess.Wipe()
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/{$}", localhttp.Allow(map[string]http.HandlerFunc{"GET": page("index.html")}))
	mux.Handle("/app.js", localhttp.Allow(map[string]http.HandlerFunc{"GET": page("app.js")}))
	mux.Handle("/style.css", localhttp.Allow(map[string]http.HandlerFunc{"GET": page("style.css")}))
	mux.Handle("/live.js", localhttp.Allow(map[string]http.HandlerFunc{"GET": page("live.js")}))

	api := http.NewServeMux()
	if s.d.Live != nil {
		s.liveRoutes(func(path string, methods map[string]http.HandlerFunc) { api.Handle(path, localhttp.Allow(methods)) })
	}
	if s.d.Status != nil && s.d.NewSession != nil && s.d.Verify != nil {
		s.recordingRoutes(api)
	}
	api.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		localhttp.Fail(w, http.StatusNotFound, "There is no such API path.")
	})
	mux.Handle("/api/", localhttp.GuardAPI("studio", "semar record --ui", s.token, maxBody, api))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not found.", http.StatusNotFound)
	})
	return localhttp.GuardAll("studio", localhttp.CSP, mux)
}

// recordingRoutes registers the calls that record. With a live host, each one that acts on the device or
// changes the recording answers 409 while the agent owns the device.
func (s *Server) recordingRoutes(api *http.ServeMux) {
	handle := func(path string, methods map[string]http.HandlerFunc) {
		guarded := make(map[string]http.HandlerFunc, len(methods))
		for m, h := range methods {
			guarded[m] = s.withHuman(h)
		}
		api.Handle(path, localhttp.Allow(guarded))
	}
	// What only reads the repository and the device list is open to the page at any time.
	open := func(path string, methods map[string]http.HandlerFunc) { api.Handle(path, localhttp.Allow(methods)) }
	open("/api/state", map[string]http.HandlerFunc{"GET": s.getState})
	open("/api/devices", map[string]http.HandlerFunc{"GET": s.getDevices})
	handle("/api/scaffold", map[string]http.HandlerFunc{"POST": s.postScaffold})
	handle("/api/session/start", map[string]http.HandlerFunc{"POST": s.postStart})
	handle("/api/session/refresh", map[string]http.HandlerFunc{"POST": s.postRefresh})
	handle("/api/session/peek", map[string]http.HandlerFunc{"POST": s.postPeek})
	handle("/api/session/act", map[string]http.HandlerFunc{"POST": s.postAct})
	handle("/api/session/assert", map[string]http.HandlerFunc{"POST": s.postAssert})
	handle("/api/session/screen", map[string]http.HandlerFunc{"POST": s.postScreen})
	handle("/api/session/use-flow", map[string]http.HandlerFunc{"POST": s.postUseFlow})
	handle("/api/session/reset", map[string]http.HandlerFunc{"POST": s.postReset})
	handle("/api/session/candidates", map[string]http.HandlerFunc{"GET": s.getCandidates})
	handle("/api/session/capture", map[string]http.HandlerFunc{"POST": s.postCapture})
	handle("/api/timeline/last", map[string]http.HandlerFunc{"DELETE": s.deleteLast})
	handle("/api/timeline/goto", map[string]http.HandlerFunc{"POST": s.postGoto})
	handle("/api/timeline/{index}", map[string]http.HandlerFunc{"PATCH": s.patchStep, "DELETE": s.deleteStep})
	handle("/api/timeline/{index}/locators", map[string]http.HandlerFunc{"GET": s.getLocators})
	handle("/api/timeline/{index}/locator", map[string]http.HandlerFunc{"POST": s.postLocator})
	handle("/api/finish", map[string]http.HandlerFunc{"POST": s.postFinish})
	handle("/api/verify", map[string]http.HandlerFunc{"POST": s.postVerify})
	handle("/api/undo", map[string]http.HandlerFunc{"POST": s.postUndo})
}

// stateReply is what GET /api/state says. It never holds a secret, a value that was typed, or a picture.
type stateReply struct {
	Repo      string   `json:"repo"`
	HasConfig bool     `json:"hasConfig"`
	Env       string   `json:"env,omitempty"`
	Service   string   `json:"service,omitempty"`
	App       string   `json:"app,omitempty"`
	Services  []string `json:"services,omitempty"`
	Problem   string   `json:"problem,omitempty"`
	Flows     []string `json:"flows"`
	// Recordings are the saved flows and Android scenarios that can be reopened.
	Recordings []Recording     `json:"recordings"`
	Screens    []screensReply  `json:"screens"`
	Session    *sessionSummary `json:"session,omitempty"`
	CanUndo    bool            `json:"canUndo"`
	Physical   bool            `json:"physicalDevice"`
}

type screensReply struct {
	App     string         `json:"app"`
	Screens map[string]int `json:"screens"`
}

type sessionSummary struct {
	Kind    mobilerec.Kind   `json:"kind"`
	ID      string           `json:"id"`
	Device  string           `json:"device,omitempty"`
	FrameID uint64           `json:"frameId,omitempty"`
	Steps   []mobilerec.Step `json:"steps"`
	// Cursor, Dirty, Common, and ScreenNames are as in the answer of a call.
	Cursor      int      `json:"cursor"`
	Dirty       bool     `json:"dirty,omitempty"`
	Common      int      `json:"common"`
	ScreenNames []string `json:"screenNames"`
}

func (s *Server) getState(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.d.Status()
	if err != nil {
		st = Status{Problem: oneLine(err.Error())}
	}
	out := stateReply{
		Repo: filepath.Base(s.d.Repo), HasConfig: st.HasConfig, Env: st.Environment, Service: st.Service, App: st.App,
		Services: st.Services, Problem: st.Problem, Flows: []string{}, Recordings: []Recording{}, Screens: []screensReply{},
	}
	if s.d.Recordable != nil {
		if list, err := s.d.Recordable(); err == nil && list != nil {
			out.Recordings = list
		}
	}
	if ids, err := flow.IDs(s.d.Repo); err == nil && ids != nil {
		out.Flows = ids
	}
	if apps, err := screens.List(s.d.Repo); err == nil {
		for _, app := range apps {
			sc, err := screens.Load(s.d.Repo, app)
			if err != nil {
				continue
			}
			row := screensReply{App: app, Screens: map[string]int{}}
			for name, els := range sc.Screens {
				row.Screens[name] = len(els)
			}
			out.Screens = append(out.Screens, row)
		}
	}
	if sess := s.sess; sess != nil {
		if out.Service == "" && s.service != "" {
			out.Service = s.service
		}
		sum := &sessionSummary{
			Kind: sess.Kind(), ID: sess.ID(), Device: sess.Device(), Steps: sess.Steps(),
			Cursor: sess.Cursor(), Dirty: sess.Dirty(), Common: sess.CommonCount(), ScreenNames: sess.ScreenNames(),
		}
		if f := sess.Frame(); f != nil {
			sum.FrameID = f.ID
		}
		out.Session, out.CanUndo, out.Physical = sum, sess.Last() != nil, s.physical
	}
	reply(w, http.StatusOK, out)
}

var serialPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

type deviceReply struct {
	Serial     string      `json:"serial"`
	Model      string      `json:"model,omitempty"`
	State      string      `json:"state"`
	Physical   bool        `json:"physical"`
	Foreground *foreground `json:"foreground,omitempty"`
}

type foreground struct {
	Package    string   `json:"package,omitempty"`
	Activity   string   `json:"activity,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
}

func (s *Server) getDevices(w http.ResponseWriter, r *http.Request) {
	out := struct {
		Available bool          `json:"available"`
		Devices   []deviceReply `json:"devices"`
		Error     string        `json:"error,omitempty"`
	}{Devices: []deviceReply{}}
	if s.d.Inspector == nil {
		reply(w, http.StatusOK, out)
		return
	}
	out.Available = true
	ctx, cancel := context.WithTimeout(s.base, inspectLimit)
	defer cancel()
	list, err := s.d.Inspector.ListDevices(ctx)
	if err != nil {
		out.Error = oneLine(err.Error())
		reply(w, http.StatusOK, out)
		return
	}
	for i, d := range list {
		row := deviceReply{Serial: d.Serial, Model: d.Model, State: d.State, Physical: !d.Emulator}
		if d.State == "device" && i < 8 && serialPattern.MatchString(d.Serial) {
			if app, err := s.d.Inspector.InspectForeground(ctx, d.Serial); err == nil {
				row.Foreground = &foreground{Package: app.Package, Activity: app.Activity, Candidates: app.Candidates}
			}
		}
		out.Devices = append(out.Devices, row)
	}
	reply(w, http.StatusOK, out)
}

// isPhysical says whether the device that a recording will use is a physical one, as far as adb says. A
// serial that is not listed, or no adb, is not a reason to refuse: the session reports what it finds.
func (s *Server) isPhysical(ctx context.Context, serial string) bool {
	if s.d.Inspector == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, inspectLimit)
	defer cancel()
	list, err := s.d.Inspector.ListDevices(ctx)
	if err != nil {
		return false
	}
	ready := list[:0:0]
	for _, d := range list {
		if d.State == "device" {
			ready = append(ready, d)
		}
	}
	if serial != "" {
		if i := slices.IndexFunc(ready, func(d mobile.DeviceInfo) bool { return d.Serial == serial }); i >= 0 {
			return !ready[i].Emulator
		}
		return false
	}
	return len(ready) == 1 && !ready[0].Emulator
}

type scaffoldRequest struct {
	Environment string `json:"environment"`
	Name        string `json:"name"`
	Package     string `json:"package"`
	BaseURL     string `json:"baseUrl"`
	APK         string `json:"apk"`
	Reset       string `json:"reset"`
}

func (s *Server) postScaffold(w http.ResponseWriter, r *http.Request) {
	var req scaffoldRequest
	if !decode(w, r, &req) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, created, err := config.WriteAndroidService(s.d.Repo, config.AndroidService{
		Environment: req.Environment, Name: req.Name, Package: req.Package, BaseURL: req.BaseURL, APK: req.APK, Reset: req.Reset,
	})
	if err != nil {
		localhttp.Fail(w, http.StatusBadRequest, oneLine(err.Error()))
		return
	}
	rel, rerr := filepath.Rel(s.d.Repo, path)
	if rerr != nil {
		rel = path
	}
	st, serr := s.d.Status()
	if serr != nil {
		st = Status{Problem: oneLine(serr.Error())}
	}
	reply(w, http.StatusOK, struct {
		Path    string `json:"path"`
		Created bool   `json:"created"`
		Status  Status `json:"status"`
	}{filepath.ToSlash(rel), created, st})
}
