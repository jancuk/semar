package studio

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/localhttp"
)

// The live studio is the page that a person opens while an agent drives a device: they can see what the
// agent does, press Pause to take over, act on the device by hand, and hand it back. The studio never
// touches the device itself. It talks to a LiveHost, which the command that serves the agent provides, so
// that this package imports neither the tool server nor the command line.

const (
	// maxLiveWait is the longest that a state request waits for a change. A request is also the signal that
	// the studio is alive.
	maxLiveWait = 25 * time.Second
	// maxReason and maxNote bound what a person writes when they take over and hand back.
	maxReason = 200
	maxNote   = 500
	// maxLiveText bounds the text that a person types in one go.
	maxLiveText = 4096
)

// LiveHost is what the live studio needs of the command. Every text that it gives is already scrubbed.
type LiveHost interface {
	// State waits until the state is newer than since or wait has passed (0: no wait), and gives it. It is
	// the heartbeat of the studio.
	State(ctx context.Context, since uint64, wait time.Duration) (LiveState, error)
	// Screen is a picture of the device as a JPEG. A screen that shows a secret is refused.
	Screen(ctx context.Context) ([]byte, error)
	// Pause takes control for the person. It waits for the agent's call in flight, then returns.
	Pause(ctx context.Context, reason string) error
	// Resume hands control back to the agent, with a note.
	Resume(ctx context.Context, note string) error
	// Act does one thing by hand. It is refused while the agent holds control, and in observe mode.
	Act(ctx context.Context, a LiveAct) (LiveState, error)
	// SetRecordHuman says whether a recording keeps what the person does.
	SetRecordHuman(on bool) error
}

// LiveError is an error with the HTTP status that the studio answers it with. A host returns one for what
// the person can understand and fix: the agent has control (409), the screen is secret or the server only
// observes (403), the agent is busy (503).
type LiveError struct {
	Status int
	Msg    string
}

func (e *LiveError) Error() string { return e.Msg }

// ErrAgentOwns is the answer to a recording call while the agent holds control.
var ErrAgentOwns = &LiveError{Status: http.StatusConflict, Msg: "the agent owns the device: press Pause"}

// LiveElement is an element of the screen.
type LiveElement struct {
	Index     int    `json:"index"`
	Kind      string `json:"kind"`
	Text      string `json:"text,omitempty"`
	Desc      string `json:"desc,omitempty"`
	ID        string `json:"id,omitempty"`
	Hint      string `json:"hint,omitempty"`
	Left      int    `json:"left"`
	Top       int    `json:"top"`
	Right     int    `json:"right"`
	Bottom    int    `json:"bottom"`
	Clickable bool   `json:"clickable,omitempty"`
	Editable  bool   `json:"editable,omitempty"`
	Password  bool   `json:"password,omitempty"`
	Enabled   bool   `json:"enabled"`
	Checked   bool   `json:"checked,omitempty"`
	Focused   bool   `json:"focused,omitempty"`
}

// LiveWindow groups the elements of one window: app, keyboard, system, or overlay.
type LiveWindow struct {
	Kind     string        `json:"kind"`
	Package  string        `json:"package,omitempty"`
	Elements []LiveElement `json:"elements"`
}

// LiveStep is a step of the recording, for the timeline. Who is human for a step that the person did.
type LiveStep struct {
	N       int    `json:"n"`
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Text    string `json:"text"`
	Who     string `json:"who,omitempty"`
	Pending bool   `json:"pending,omitempty"`
}

// LiveEvent is a line of the call feed: a call of the agent, or something that the person did. Summary
// never holds an argument, and a text that a person typed is only "[typed N chars]".
type LiveEvent struct {
	Seq     uint64    `json:"seq"`
	At      time.Time `json:"at"`
	Who     string    `json:"who"`
	Tool    string    `json:"tool"`
	Surface string    `json:"surface,omitempty"`
	OK      bool      `json:"ok"`
	MS      int64     `json:"ms,omitempty"`
	Summary string    `json:"summary,omitempty"`
}

// LiveState is everything that the page draws.
type LiveState struct {
	// Version grows with every change. The page passes the last one it saw.
	Version uint64 `json:"version"`
	// Owner is agent or human. Pending says that a pause is waiting for the agent's call in flight.
	Owner   string `json:"owner"`
	Pending bool   `json:"pending,omitempty"`
	// Asked is why the agent asked for help, when it did. Reason is why the person holds control.
	Asked  string    `json:"asked,omitempty"`
	Reason string    `json:"reason,omitempty"`
	Note   string    `json:"note,omitempty"`
	Since  time.Time `json:"since"`
	// Mode is live or record. RecordHuman says that the person's actions are steps of the recording, and Gap
	// that some are not, so that the recording cannot be saved yet.
	Mode        string `json:"mode"`
	RecordHuman bool   `json:"recordHuman"`
	Gap         bool   `json:"gap,omitempty"`
	GapAt       int    `json:"gapAt,omitempty"`
	// Observe says that the server only observes: the person may pause and look, and nothing more.
	Observe       bool   `json:"observe,omitempty"`
	ObserveReason string `json:"observeReason,omitempty"`

	Width    int  `json:"width"`
	Height   int  `json:"height"`
	Secret   bool `json:"secret,omitempty"`
	NoDevice bool `json:"noDevice,omitempty"`
	// Busy says that the agent held the device when the screen was read, so Windows may be the last reading.
	Busy    bool         `json:"busy,omitempty"`
	Windows []LiveWindow `json:"windows"`
	Steps   []LiveStep   `json:"steps"`
	Events  []LiveEvent  `json:"events"`
}

// LiveAct is one thing that the person does by hand.
type LiveAct struct {
	// Do is tap, doubleTap, longPress, swipe, type, or press.
	Do string `json:"do"`
	// Ref names an element of the last state (e3 is its Index 2). X and Y are screen pixels, and X2 and Y2
	// end a swipe.
	Ref       string   `json:"ref,omitempty"`
	X         *float64 `json:"x,omitempty"`
	Y         *float64 `json:"y,omitempty"`
	X2        *float64 `json:"x2,omitempty"`
	Y2        *float64 `json:"y2,omitempty"`
	Direction string   `json:"direction,omitempty"`
	Hold      int      `json:"hold,omitempty"`
	// Text is typed. SecretEnv names a variable of the server whose value is typed instead.
	Text      *string `json:"text,omitempty"`
	SecretEnv string  `json:"secretEnv,omitempty"`
	Replace   bool    `json:"replace,omitempty"`
	Submit    bool    `json:"submit,omitempty"`
	// Key is the button of a press: BACK, HOME, ENTER, and so on.
	Key string `json:"key,omitempty"`
}

var liveActs = map[string]bool{"tap": true, "doubletap": true, "longpress": true, "swipe": true, "type": true, "press": true}

// liveRoutes registers the live API on api, behind the same token as every other call.
func (s *Server) liveRoutes(handle func(string, map[string]http.HandlerFunc)) {
	handle("/api/live/state", map[string]http.HandlerFunc{"GET": s.getLiveState})
	handle("/api/live/screen", map[string]http.HandlerFunc{"GET": s.getLiveScreen})
	handle("/api/live/pause", map[string]http.HandlerFunc{"POST": s.postLivePause})
	handle("/api/live/resume", map[string]http.HandlerFunc{"POST": s.postLiveResume})
	handle("/api/live/act", map[string]http.HandlerFunc{"POST": s.postLiveAct})
	handle("/api/live/record", map[string]http.HandlerFunc{"POST": s.postLiveRecord})
}

// withHuman wraps a recording call. While the agent owns the device it answers 409 and does nothing, so
// that a click on the old page cannot act on a device that the agent is driving.
func (s *Server) withHuman(h http.HandlerFunc) http.HandlerFunc {
	if s.d.Live == nil {
		return h
	}
	return func(w http.ResponseWriter, r *http.Request) {
		st, err := s.d.Live.State(r.Context(), 0, 0)
		if err != nil {
			s.liveFailure(w, err)
			return
		}
		if st.Owner != "human" {
			s.liveFailure(w, ErrAgentOwns)
			return
		}
		h(w, r)
	}
}

// liveFailure answers an error of the host. A LiveError says its own status. Any other is a failure of the
// host, and says so without the detail that it may not be safe to show.
func (s *Server) liveFailure(w http.ResponseWriter, err error) {
	var le *LiveError
	switch {
	case errors.As(err, &le):
		localhttp.Fail(w, le.Status, le.Msg)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		localhttp.Fail(w, http.StatusServiceUnavailable, "The request ended before the studio could answer.")
	default:
		localhttp.Fail(w, http.StatusBadRequest, oneLine(err.Error()))
	}
}

func (s *Server) getLiveState(w http.ResponseWriter, r *http.Request) {
	var since uint64
	if v := r.URL.Query().Get("since"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			localhttp.Fail(w, http.StatusBadRequest, "since is a number: the version that the page saw last.")
			return
		}
		since = n
	}
	var wait time.Duration
	if v := r.URL.Query().Get("wait"); v != "" {
		sec, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(sec) || math.IsInf(sec, 0) || sec < 0 {
			localhttp.Fail(w, http.StatusBadRequest, "wait is a number of seconds, 0 or more.")
			return
		}
		wait = time.Duration(min(sec, maxLiveWait.Seconds()) * float64(time.Second))
	}
	st, err := s.d.Live.State(r.Context(), since, wait)
	if err != nil {
		s.liveFailure(w, err)
		return
	}
	reply(w, http.StatusOK, st)
}

func (s *Server) getLiveScreen(w http.ResponseWriter, r *http.Request) {
	data, err := s.d.Live.Screen(r.Context())
	if err != nil {
		s.liveFailure(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// stateAfter answers a call that changed something with the state that it left.
func (s *Server) stateAfter(w http.ResponseWriter, r *http.Request) {
	st, err := s.d.Live.State(r.Context(), 0, 0)
	if err != nil {
		s.liveFailure(w, err)
		return
	}
	reply(w, http.StatusOK, st)
}

func (s *Server) postLivePause(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Reason string `json:"reason"`
	}
	if !localhttp.Decode(w, r, &req, true, maxBody) {
		return
	}
	reason := oneLine(req.Reason)
	if len([]rune(reason)) > maxReason {
		localhttp.Fail(w, http.StatusBadRequest, "The reason is longer than 200 characters.")
		return
	}
	if reason == "" {
		reason = "paused from the studio"
	}
	if err := s.d.Live.Pause(r.Context(), reason); err != nil {
		s.liveFailure(w, err)
		return
	}
	s.stateAfter(w, r)
}

func (s *Server) postLiveResume(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Note string `json:"note"`
	}
	if !localhttp.Decode(w, r, &req, true, maxBody) {
		return
	}
	note := oneLine(req.Note)
	if len([]rune(note)) > maxNote {
		localhttp.Fail(w, http.StatusBadRequest, "The note is longer than 500 characters.")
		return
	}
	if err := s.d.Live.Resume(r.Context(), note); err != nil {
		s.liveFailure(w, err)
		return
	}
	s.stateAfter(w, r)
}

func (s *Server) postLiveAct(w http.ResponseWriter, r *http.Request) {
	var a LiveAct
	if !decode(w, r, &a) {
		return
	}
	do := strings.ToLower(strings.TrimSpace(a.Do))
	if !liveActs[do] {
		localhttp.Fail(w, http.StatusBadRequest, "do is tap, doubleTap, longPress, swipe, type, or press.")
		return
	}
	if a.Text != nil && len([]rune(*a.Text)) > maxLiveText {
		localhttp.Fail(w, http.StatusBadRequest, "The text is longer than 4096 characters.")
		return
	}
	// The studio asks the host before it does anything, and the host asks the baton: an act that the agent's
	// ownership forbids is 409 and touches nothing.
	st, err := s.d.Live.Act(r.Context(), a)
	if err != nil {
		s.liveFailure(w, err)
		return
	}
	reply(w, http.StatusOK, st)
}

func (s *Server) postLiveRecord(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On *bool `json:"on"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.On == nil {
		localhttp.Fail(w, http.StatusBadRequest, "on is true or false.")
		return
	}
	if err := s.d.Live.SetRecordHuman(*req.On); err != nil {
		s.liveFailure(w, err)
		return
	}
	s.stateAfter(w, r)
}
