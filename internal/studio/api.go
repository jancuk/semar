package studio

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/localhttp"
	"github.com/jancuk/semar/internal/mobilerec"
)

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	return localhttp.Decode(w, r, v, false, maxBody)
}

func reply(w http.ResponseWriter, code int, v any) { localhttp.Reply(w, code, v) }

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// FrameView is a frame with the picture of the device. The picture is a JPEG as a data address, and it is
// left out when the screen shows a secret, because the pixels of a picture cannot be scrubbed.
type FrameView struct {
	*mobilerec.Frame
	Image       string `json:"image,omitempty"`
	ImageHidden bool   `json:"imageHidden,omitempty"`
	ImageError  string `json:"imageError,omitempty"`
}

// sessionReply is the answer of every call that changes the recording.
type sessionReply struct {
	Frame    *FrameView       `json:"frame,omitempty"`
	Steps    []mobilerec.Step `json:"steps"`
	Kind     mobilerec.Kind   `json:"kind"`
	ID       string           `json:"id"`
	Device   string           `json:"device,omitempty"`
	Physical bool             `json:"physicalDevice"`
	CanUndo  bool             `json:"canUndo"`
	// Cursor is the number of steps that the device has been taken through: a new step goes after them.
	Cursor int `json:"cursor"`
	// Dirty says that a deleted step was done on the device, so it is not where the steps say: Go here fixes it.
	Dirty bool `json:"dirty"`
	// Common is how many elements the common section of the screens file holds. ScreenNames are the names
	// that a screen can have.
	Common      int      `json:"common"`
	ScreenNames []string `json:"screenNames"`
	// Flow says how a flow that the call ran ended.
	Flow string `json:"flow,omitempty"`
	// Notice is what a call says besides its result, such as the other steps that type the same text.
	Notice string `json:"notice,omitempty"`
	// Captured is the value that a capture took, for a preview. It was checked to be no secret.
	Captured *capturedReply `json:"captured,omitempty"`
}

type capturedReply struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// errorReply is an error answer. A stale frame carries the frame that the screen shows now.
type errorReply struct {
	Error        string `json:"error"`
	Stale        bool   `json:"stale,omitempty"`
	NeedsConfirm bool   `json:"needsConfirm,omitempty"`
	NeedsSecret  bool   `json:"needsSecret,omitempty"`
	// NeedsSecrets and NeedsParams name the values that a replay lacks: give them and call again.
	NeedsSecrets []string      `json:"needsSecrets,omitempty"`
	NeedsParams  []string      `json:"needsParams,omitempty"`
	Exists       bool          `json:"exists,omitempty"`
	Session      *sessionReply `json:"session,omitempty"`
}

// scrub takes the secrets of the session out of a message.
func (s *Server) scrubWith(sess *mobilerec.Session, msg string) string {
	return sess.Overlay().Scrub(msg)
}

func (s *Server) scrub(msg string) string {
	if s.sess != nil {
		msg = s.sess.Overlay().Scrub(msg)
	}
	return msg
}

// failure answers an error of the session, in words that hold no secret. The caller holds the lock.
func (s *Server) failure(ctx context.Context, w http.ResponseWriter, err error) {
	s.failWith(ctx, w, err, http.StatusInternalServerError)
}

// refuse answers an error of a call that edits the recording. An error that no case names is the person's
// request that cannot be done, and not a failure of the device.
func (s *Server) refuse(ctx context.Context, w http.ResponseWriter, err error) {
	s.failWith(ctx, w, err, http.StatusBadRequest)
}

// failWith is failure with the code of an error that no case names.
func (s *Server) failWith(ctx context.Context, w http.ResponseWriter, err error, code int) {
	body := errorReply{Error: oneLine(s.scrub(err.Error()))}
	var stale *mobilerec.StaleError
	var act *mobilerec.ActError
	var needs *mobilerec.NeedsValues
	var unavailable *mobile.Unavailable
	switch {
	case errors.As(err, &needs):
		code, body.NeedsSecrets, body.NeedsParams = http.StatusPreconditionRequired, needs.Secrets, needs.Params
	case errors.As(err, &stale):
		code, body.Stale = http.StatusConflict, true
		body.Session = s.snapshot(ctx, stale.Frame, true)
	case errors.Is(err, mobilerec.ErrNeedsConfirm):
		code, body.NeedsConfirm = http.StatusPreconditionRequired, true
	case errors.Is(err, mobilerec.ErrNeedsSecret):
		code, body.NeedsSecret = http.StatusUnprocessableEntity, true
	case errors.Is(err, mobilerec.ErrNothingToUndo), errors.Is(err, mobilerec.ErrNotSynced), errors.Is(err, mobilerec.ErrNotAtStep):
		code = http.StatusConflict
	case errors.Is(err, fs.ErrExist):
		code, body.Exists = http.StatusConflict, true
	case errors.Is(err, mobilerec.ErrNoLocator), errors.Is(err, mobilerec.ErrSecretOnScreen),
		errors.Is(err, mobilerec.ErrNoCheck), errors.Is(err, mobilerec.ErrNothingToSave):
		code = http.StatusUnprocessableEntity
	case errors.As(err, &act):
		code = http.StatusBadRequest
	case errors.As(err, &unavailable):
		code = http.StatusServiceUnavailable
	}
	reply(w, code, body)
}

// snapshot is the answer about the recording. The caller holds the lock.
func (s *Server) snapshot(ctx context.Context, f *mobilerec.Frame, withImage bool) *sessionReply {
	out := &sessionReply{Steps: []mobilerec.Step{}}
	sess := s.sess
	if sess == nil {
		return out
	}
	out.Kind, out.ID, out.Device, out.Physical, out.CanUndo = sess.Kind(), sess.ID(), sess.Device(), s.physical, sess.Last() != nil
	out.Steps = sess.Steps()
	out.Cursor, out.Dirty, out.Common, out.ScreenNames = sess.Cursor(), sess.Dirty(), sess.CommonCount(), sess.ScreenNames()
	if f != nil {
		v := &FrameView{Frame: f}
		switch {
		case f.Secret:
			v.ImageHidden = true
		case withImage:
			if jpg := sess.LastShotJPEG(f.ID); len(jpg) > 0 {
				v.Image = "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpg)
			} else {
				png, err := sess.Capture(ctx)
				switch {
				case errors.Is(err, mobilerec.ErrSecretOnScreen):
					v.ImageHidden = true
				case err != nil:
					v.ImageError = oneLine(sess.Overlay().Scrub(err.Error()))
				default:
					v.Image = jpegAddress(png)
				}
			}
		}
		out.Frame = v
	}
	return out
}

const studioMaxShotSide = 960

// jpegAddress is a PNG of the device as a small JPEG data address, downscaled to at most 960px on its
// longest side. A picture that cannot be decoded as PNG is sent as it is.
func jpegAddress(data []byte) string {
	if shrunk, err := mobile.ShrinkTo(data, studioMaxShotSide, nil); err == nil {
		return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(shrunk)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		if _, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil {
			return ""
		}
		return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 70}); err != nil {
		return ""
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// session gives the session, or answers that there is none. The caller holds the lock.
func (s *Server) session(w http.ResponseWriter) *mobilerec.Session {
	if s.sess == nil {
		localhttp.Fail(w, http.StatusConflict, "There is no recording yet: start one first.")
		return nil
	}
	return s.sess
}

// done answers a call that gave a frame.
func (s *Server) done(ctx context.Context, w http.ResponseWriter, f *mobilerec.Frame, err error, withImage bool) {
	if err != nil {
		s.failure(ctx, w, err)
		return
	}
	reply(w, http.StatusOK, s.snapshot(ctx, f, withImage))
}

type startRequest struct {
	Kind         string            `json:"kind"`
	ID           string            `json:"id"`
	Ticket       string            `json:"ticket"`
	Title        string            `json:"title"`
	Service      string            `json:"service"`
	DeviceSerial string            `json:"deviceSerial"`
	UseFlows     []string          `json:"useFlows"`
	With         map[string]string `json:"with"`
	ConfirmClear bool              `json:"confirmClear"`
	Replace      bool              `json:"replace"`
	// Resume reopens a saved flow or scenario: its steps are replayed up to At, and recording goes on from
	// there. Its kind and ID replace the ones above. Secrets and Params give the values that the steps need
	// and that no file holds: they are kept in memory only.
	Resume  *resumeRequest    `json:"resume"`
	Secrets map[string]string `json:"secrets"`
	Params  map[string]string `json:"params"`
}

type resumeRequest struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	// At is the number of steps to replay, from 1. 0 is all of them.
	At int `json:"at"`
}

func kindOf(v string) (mobilerec.Kind, error) {
	switch v {
	case "", string(mobilerec.KindScenario):
		return mobilerec.KindScenario, nil
	case string(mobilerec.KindFlow):
		return mobilerec.KindFlow, nil
	}
	return "", errors.New("the kind of a recording is flow or scenario")
}

func (s *Server) postStart(w http.ResponseWriter, r *http.Request) {
	var req startRequest
	if !decode(w, r, &req) {
		return
	}
	// Secrets are kept in memory only: the request holds them for this call, and they go nowhere else.
	secrets := req.Secrets
	req.Secrets = nil
	if res := req.Resume; res != nil {
		if len(req.UseFlows) > 0 {
			localhttp.Fail(w, http.StatusBadRequest, "A reopened recording has its steps already: leave out the flow to start with.")
			return
		}
		if res.ID == "" || res.ID == defaultID || res.At < 0 {
			localhttp.Fail(w, http.StatusBadRequest, "Name the saved flow or scenario to reopen, and a step from 1.")
			return
		}
		req.Kind, req.ID, req.Replace = res.Kind, res.ID, true
	}
	kind, err := kindOf(req.Kind)
	if err != nil {
		localhttp.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.DeviceSerial != "" && !serialPattern.MatchString(req.DeviceSerial) {
		localhttp.Fail(w, http.StatusBadRequest, "The serial of the device is not one that adb gives.")
		return
	}
	for _, id := range req.UseFlows {
		if err := flow.CheckID(id); err != nil {
			localhttp.Fail(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	id := req.ID
	if id == "" {
		id = defaultID
	}
	if err := checkRecordID(kind, id); err != nil {
		localhttp.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx := s.base
	st, serr := s.d.Status()
	switch {
	case serr != nil:
		localhttp.Fail(w, http.StatusConflict, oneLine(serr.Error()))
		return
	case !st.HasConfig || st.Problem != "":
		msg := st.Problem
		if msg == "" {
			msg = "There is no configuration yet: use the guided setup."
		}
		localhttp.Fail(w, http.StatusConflict, oneLine(msg))
		return
	}
	physical := s.isPhysical(ctx, req.DeviceSerial)
	if physical && !req.ConfirmClear {
		reply(w, http.StatusPreconditionRequired, errorReply{
			Error:        "This is a physical device. A recording resets the app first, and a replay of a sign-in may need reset: clear, which deletes the data of the app on it. Confirm to go on.",
			NeedsConfirm: true,
		})
		return
	}
	// The recording before this one gives the device away first: two would fight for the lease.
	if old := s.sess; old != nil {
		s.sess = nil
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
		_ = old.Close(cctx)
		cancel()
		old.Wipe()
	}
	var use string
	if len(req.UseFlows) > 0 {
		use = req.UseFlows[0]
	}
	ticket := req.Ticket
	if ticket == "" && kind == mobilerec.KindScenario && req.ID != "" && s.d.TicketOf != nil {
		ticket = s.d.TicketOf(req.ID)
	}
	sr := SessionRequest{
		Kind: kind, ID: id, Ticket: ticket, Title: req.Title, Service: req.Service, Serial: req.DeviceSerial,
		Use: use, With: req.With, Replace: req.Replace, ConfirmClear: req.ConfirmClear,
	}
	if req.Resume != nil {
		sr.Resume, sr.ResumeAt = true, req.Resume.At
	}
	sess, err := s.d.NewSession(sr)
	if err != nil {
		localhttp.Fail(w, http.StatusBadRequest, oneLine(err.Error()))
		return
	}
	for name, value := range secrets {
		if err := sess.SetSecret(name, value); err != nil {
			sess.Wipe()
			localhttp.Fail(w, http.StatusBadRequest, oneLine(err.Error()))
			return
		}
	}
	clear(secrets)
	for name, value := range req.Params {
		if err := sess.SetParam(name, value); err != nil {
			sess.Wipe()
			localhttp.Fail(w, http.StatusBadRequest, oneLine(s.scrubWith(sess, err.Error())))
			return
		}
	}
	if req.Resume != nil {
		// The values that the steps need and that no file holds are asked for before anything starts, so
		// that the page asks for them and sends the start again with them.
		if secrets, params := sess.MissingSecrets(req.Resume.At), sess.MissingParams(req.Resume.At); len(secrets) > 0 || len(params) > 0 {
			sess.Wipe()
			reply(w, http.StatusPreconditionRequired, errorReply{
				Error:        (&mobilerec.NeedsValues{Secrets: secrets, Params: params}).Error(),
				NeedsSecrets: secrets, NeedsParams: params,
			})
			return
		}
	}
	f, err := sess.Start(ctx)
	if err != nil {
		s.sess = sess
		s.failure(ctx, w, err)
		s.sess = nil
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
		_ = sess.Close(cctx)
		cancel()
		sess.Wipe()
		return
	}
	s.sess, s.service, s.physical = sess, req.Service, physical || !sess.Emulator() && sess.Device() != ""
	var ranFlow string
	for _, id := range req.UseFlows[min(1, len(req.UseFlows)):] {
		var res flow.RunResult
		if f, res, err = sess.UseFlow(ctx, id, nil); err != nil {
			s.failure(ctx, w, err)
			return
		}
		ranFlow = id + " " + res.Status
	}
	out := s.snapshot(ctx, f, true)
	out.Flow = ranFlow
	reply(w, http.StatusOK, out)
}

// checkRecordID checks the name of a recording that the person gave at the start. The placeholder is no
// name: it is checked again when the recording is finished.
func checkRecordID(kind mobilerec.Kind, id string) error {
	if id == defaultID {
		return nil
	}
	if kind == mobilerec.KindFlow {
		return flow.CheckID(id)
	}
	return idcheck.Scenario(id)
}

func (s *Server) postRefresh(w http.ResponseWriter, r *http.Request) {
	var none struct{}
	if !localhttp.Decode(w, r, &none, true, maxBody) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.session(w)
	if sess == nil {
		return
	}
	f, err := sess.Refresh(s.base)
	s.done(s.base, w, f, err, true)
}

type peekRequest struct {
	FrameID uint64 `json:"frameId"`
}

type peekReply struct {
	Changed bool `json:"changed"`
	// Slow says that the screen takes longer to read than a peek may hold the device: the page stops asking.
	Slow bool `json:"slow,omitempty"`
	*sessionReply
}

// peekLimit bounds the one read of a peek. A person who clicks waits for it, so it must stay short.
const peekLimit = 1500 * time.Millisecond

// postPeek says whether the device still shows the frame that the page has, and gives the new screen when
// it does not. The page asks a moment after an action, so that a screen that loaded late is not left stale.
func (s *Server) postPeek(w http.ResponseWriter, r *http.Request) {
	var req peekRequest
	if !decode(w, r, &req) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.session(w)
	if sess == nil {
		return
	}
	f, changed, slow, err := sess.Peek(s.base, req.FrameID, peekLimit)
	if err != nil {
		s.failure(s.base, w, err)
		return
	}
	if !changed {
		reply(w, http.StatusOK, peekReply{Slow: slow})
		return
	}
	reply(w, http.StatusOK, peekReply{Changed: true, sessionReply: s.snapshot(s.base, f, true)})
}

type actRequest struct {
	FrameID uint64 `json:"frameId"`
	Do      string `json:"do"`
	Index   *int   `json:"index"`
	X       *int   `json:"x"`
	Y       *int   `json:"y"`
	Value   string `json:"value"`
	// SecretEnv names the variable of a secret that a type uses. EphemeralSecret is its value, sent once,
	// and kept in memory for this session only. Without it, the value is the one that the session or the
	// environment already holds.
	SecretEnv       string `json:"secretEnv"`
	EphemeralSecret string `json:"ephemeralSecret"`
	Screen          string `json:"screen"`
	// Candidate chooses the locator of the element: 0 is the best one, K is the Kth of the candidates.
	Candidate int `json:"candidate"`
}

func target(index, x, y *int) (*mobilerec.Target, bool) {
	switch {
	case index != nil && *index >= 0:
		return &mobilerec.Target{Index: *index}, true
	case x != nil && y != nil:
		return &mobilerec.Target{X: *x, Y: *y, ByPoint: true}, true
	}
	return nil, false
}

func (s *Server) postAct(w http.ResponseWriter, r *http.Request) {
	var req actRequest
	if !decode(w, r, &req) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.session(w)
	if sess == nil {
		return
	}
	act := mobilerec.Act{Do: req.Do, Frame: req.FrameID, Value: req.Value, Screen: req.Screen, Candidate: req.Candidate}
	if t, ok := target(req.Index, req.X, req.Y); ok {
		act.Target = t
	}
	if req.SecretEnv != "" {
		if req.Do != mobilerec.DoType {
			localhttp.Fail(w, http.StatusBadRequest, "A secret is typed: use the operation type.")
			return
		}
		value := req.EphemeralSecret
		if value == "" {
			value, _ = sess.Overlay().Lookup(req.SecretEnv)
		}
		if value == "" {
			localhttp.Fail(w, http.StatusBadRequest, "Give the value of the secret: it is kept in memory for this session only.")
			return
		}
		act.Value, act.Secret = value, req.SecretEnv
		req.EphemeralSecret = ""
	}
	f, err := sess.Act(s.base, act)
	s.done(s.base, w, f, err, true)
}

type assertRequest struct {
	FrameID   uint64 `json:"frameId"`
	Index     *int   `json:"index"`
	X         *int   `json:"x"`
	Y         *int   `json:"y"`
	Kind      string `json:"kind"`
	Value     string `json:"value"`
	Attribute string `json:"attribute"`
	Screen    string `json:"screen"`
	Candidate int    `json:"candidate"`
}

func (s *Server) postAssert(w http.ResponseWriter, r *http.Request) {
	var req assertRequest
	if !decode(w, r, &req) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.session(w)
	if sess == nil {
		return
	}
	t, ok := target(req.Index, req.X, req.Y)
	if !ok {
		localhttp.Fail(w, http.StatusBadRequest, "A check needs an element: send its index, or a point of the screen.")
		return
	}
	f, err := sess.Assert(s.base, mobilerec.Assert{
		Frame: req.FrameID, Target: *t, Kind: mobilerec.AssertKind(req.Kind), Value: req.Value, Attribute: req.Attribute, Screen: req.Screen, Candidate: req.Candidate,
	})
	// A check does nothing on the device, so the picture stays.
	s.done(s.base, w, f, err, false)
}

type screenRequest struct {
	Name string `json:"name"`
	// Rename renames the recorded steps that are on the screen From (the screen that the frame shows, when
	// it is empty) to Name, and makes a later screen that would have had the old name get the new one.
	Rename bool   `json:"rename"`
	From   string `json:"from"`
}

func (s *Server) postScreen(w http.ResponseWriter, r *http.Request) {
	var req screenRequest
	if !decode(w, r, &req) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.session(w)
	if sess == nil {
		return
	}
	var f *mobilerec.Frame
	var err error
	if req.Rename {
		from := req.From
		if cur := sess.Frame(); from == "" && cur != nil {
			from = cur.Screen
		}
		f, err = sess.RenameScreen(from, req.Name)
	} else {
		f, err = sess.SetScreen(req.Name)
	}
	if err != nil {
		s.refuse(s.base, w, err)
		return
	}
	s.done(s.base, w, f, nil, false)
}

type useFlowRequest struct {
	Flow string            `json:"flow"`
	With map[string]string `json:"with"`
}

func (s *Server) postUseFlow(w http.ResponseWriter, r *http.Request) {
	var req useFlowRequest
	if !decode(w, r, &req) {
		return
	}
	if err := flow.CheckID(req.Flow); err != nil {
		localhttp.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.session(w)
	if sess == nil {
		return
	}
	f, res, err := sess.UseFlow(s.base, req.Flow, req.With)
	if err != nil {
		s.failure(s.base, w, err)
		return
	}
	out := s.snapshot(s.base, f, true)
	out.Flow = req.Flow + " " + res.Status
	reply(w, http.StatusOK, out)
}

type editRequest struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Screen    string `json:"screen"`
	Element   string `json:"element"`
	SecretEnv string `json:"secretEnv"`
	// Common sends the element of the step to the common section of the screens file, or takes it back.
	Common *bool `json:"common"`
	// Param turns the typed text of a step of a flow into the param of that name. Required gives it no default.
	Param    string `json:"param"`
	Required bool   `json:"required"`
}

// patchStep edits what a step says about itself. There is no locator in the request, and an unknown
// field is refused, so a locator cannot be sent: an old step has no screen to check one against.
func (s *Server) patchStep(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || n < 0 {
		localhttp.Fail(w, http.StatusBadRequest, "The step is a number from 0.")
		return
	}
	var req editRequest
	if !decode(w, r, &req) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.session(w)
	if sess == nil {
		return
	}
	edit := mobilerec.Edit{ID: req.ID, Text: req.Text, Screen: req.Screen, Element: req.Element, Secret: req.SecretEnv, Common: req.Common}
	if edit != (mobilerec.Edit{}) || req.Param == "" {
		if err = sess.EditStep(n+1, edit); err != nil {
			s.refuse(s.base, w, err)
			return
		}
	}
	var notice string
	if req.Param != "" {
		if notice, err = sess.MakeParam(n+1, req.Param, req.Required); err != nil {
			s.refuse(s.base, w, err)
			return
		}
	}
	// Turning a text into a secret reads the frame again, with the text taken out: the picture is as it was.
	out := s.snapshot(s.base, sess.Frame(), false)
	out.Notice = s.scrub(oneLine(notice))
	reply(w, http.StatusOK, out)
}

func (s *Server) deleteLast(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.session(w)
	if sess == nil {
		return
	}
	if err := sess.DeleteLastStep(); err != nil {
		localhttp.Fail(w, http.StatusConflict, err.Error())
		return
	}
	reply(w, http.StatusOK, s.snapshot(s.base, sess.Frame(), false))
}

type finishRequest struct {
	Kind         string `json:"kind"`
	ID           string `json:"id"`
	Ticket       string `json:"ticket"`
	Title        string `json:"title"`
	Replace      bool   `json:"replace"`
	ConfirmClear bool   `json:"confirmClear"`
}

type finishReply struct {
	sessionReply
	Files   []string          `json:"files"`
	Notes   []string          `json:"notes,omitempty"`
	Preview map[string]string `json:"preview"`
}

func (s *Server) postFinish(w http.ResponseWriter, r *http.Request) {
	var req finishRequest
	if !decode(w, r, &req) {
		return
	}
	kind, err := kindOf(req.Kind)
	if err != nil {
		localhttp.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ID == "" || req.ID == defaultID {
		localhttp.Fail(w, http.StatusBadRequest, "Name the recording: its ID is the name of the file.")
		return
	}
	if err := checkRecordID(kind, req.ID); err != nil {
		localhttp.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.session(w)
	if sess == nil {
		return
	}
	ticket := req.Ticket
	if ticket == "" && kind == mobilerec.KindScenario && s.d.TicketOf != nil {
		ticket = s.d.TicketOf(req.ID)
	}
	if err := sess.Retarget(mobilerec.Next{Kind: kind, ID: req.ID, Ticket: ticket, Title: req.Title}); err != nil {
		localhttp.Fail(w, http.StatusBadRequest, oneLine(s.scrub(err.Error())))
		return
	}
	if req.ConfirmClear {
		sess.ConfirmClear()
	}
	sv, err := sess.Save(mobilerec.SaveOptions{Replace: req.Replace})
	if err != nil {
		s.failure(s.base, w, err)
		return
	}
	out := finishReply{sessionReply: *s.snapshot(s.base, nil, false), Files: sv.Files, Notes: sv.Draft.Notes, Preview: map[string]string{}}
	if root, err := os.OpenRoot(s.d.Repo); err == nil {
		defer root.Close()
		for _, rel := range sv.Files {
			f, err := root.Open(rel)
			if err != nil {
				continue
			}
			data, _ := io.ReadAll(io.LimitReader(f, previewLimit*2+1))
			f.Close()
			text := sess.Overlay().Scrub(string(data))
			if len(text) > previewLimit {
				text = text[:previewLimit] + "\n..."
			} else if len(data) > previewLimit*2 {
				text += "\n..."
			}
			out.Preview[rel] = text
		}
	}
	reply(w, http.StatusOK, out)
}

type verifyReply struct {
	sessionReply
	Verify VerifyResult `json:"verify"`
}

// postVerify gives the device away, runs what was saved on a clean device session, and takes the device
// back. The lease is free while the callback runs: a second session on the device would be refused.
func (s *Server) postVerify(w http.ResponseWriter, r *http.Request) {
	var none struct{}
	if !localhttp.Decode(w, r, &none, true, maxBody) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.session(w)
	if sess == nil {
		return
	}
	saved := sess.Last()
	if saved == nil {
		localhttp.Fail(w, http.StatusConflict, "Save the recording first: finish it, then verify.")
		return
	}
	ctx := s.base
	if err := sess.Suspend(ctx); err != nil {
		s.failure(ctx, w, err)
		return
	}
	res, verr := s.d.Verify(ctx, sess, saved)
	var f *mobilerec.Frame
	var rerr error
	if ctx.Err() == nil {
		f, rerr = sess.Resume(context.WithoutCancel(ctx))
	}
	if verr != nil {
		s.failure(ctx, w, errors.Join(verr, resumeError(rerr)))
		return
	}
	res.Message = s.scrub(res.Message)
	for i := range res.Steps {
		res.Steps[i].Detail = s.scrub(res.Steps[i].Detail)
	}
	switch {
	case rerr != nil:
		res.Message += " The device could not be taken back: " + oneLine(s.scrub(rerr.Error()))
		f = nil
	case f != nil:
		res.Message += " The device is back at the start of the app."
	}
	reply(w, http.StatusOK, verifyReply{sessionReply: *s.snapshot(ctx, f, true), Verify: res})
}

func resumeError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New("the device could not be taken back: " + err.Error())
}

func (s *Server) postUndo(w http.ResponseWriter, r *http.Request) {
	var none struct{}
	if !localhttp.Decode(w, r, &none, true, maxBody) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.session(w)
	if sess == nil {
		return
	}
	if err := sess.Undo(); err != nil {
		s.failure(s.base, w, err)
		return
	}
	reply(w, http.StatusOK, s.snapshot(s.base, sess.Frame(), false))
}

type resetRequest struct {
	Kind     string            `json:"kind"`
	ID       string            `json:"id"`
	Ticket   string            `json:"ticket"`
	Title    string            `json:"title"`
	UseFlows []string          `json:"useFlows"`
	With     map[string]string `json:"with"`
}

// postReset drops the steps, resets the app, and starts the next recording, so that a person who finished
// login can record task-submission from the signed in start that login gives.
func (s *Server) postReset(w http.ResponseWriter, r *http.Request) {
	var req resetRequest
	if !decode(w, r, &req) {
		return
	}
	kind, err := kindOf(req.Kind)
	if err != nil {
		localhttp.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	id := req.ID
	if id == "" {
		id = defaultID
	}
	if err := checkRecordID(kind, id); err != nil {
		localhttp.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	for _, f := range req.UseFlows {
		if err := flow.CheckID(f); err != nil {
			localhttp.Fail(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.session(w)
	if sess == nil {
		return
	}
	next := &mobilerec.Next{Kind: kind, ID: id, Ticket: req.Ticket, Title: req.Title, With: req.With}
	if len(req.UseFlows) > 0 {
		next.Use = req.UseFlows[0]
	}
	f, err := sess.ResetSession(s.base, next)
	if err != nil {
		s.failure(s.base, w, err)
		return
	}
	var ranFlow string
	for _, id := range req.UseFlows[min(1, len(req.UseFlows)):] {
		var res flow.RunResult
		if f, res, err = sess.UseFlow(s.base, id, nil); err != nil {
			s.failure(s.base, w, err)
			return
		}
		ranFlow = id + " " + res.Status
	}
	out := s.snapshot(s.base, f, true)
	out.Flow = ranFlow
	reply(w, http.StatusOK, out)
}

// stepIndex reads the step of a path. Like the edit of a step, it counts from 0, as the page lists the steps.
func stepIndex(w http.ResponseWriter, r *http.Request) (int, bool) {
	n, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || n < 0 {
		localhttp.Fail(w, http.StatusBadRequest, "The step is a number from 0.")
		return 0, false
	}
	return n, true
}

// deleteStep takes any step off the recording. A step that the device had done leaves the answer dirty.
func (s *Server) deleteStep(w http.ResponseWriter, r *http.Request) {
	n, ok := stepIndex(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.session(w)
	if sess == nil {
		return
	}
	if err := sess.DeleteStep(n + 1); err != nil {
		// The only way that a delete is refused is a step that is not there.
		localhttp.Fail(w, http.StatusNotFound, oneLine(s.scrub(err.Error())))
		return
	}
	reply(w, http.StatusOK, s.snapshot(s.base, sess.Frame(), false))
}

type gotoRequest struct {
	// N is the number of steps to take the device through, from 1: the Nth is the step with n of N.
	N int `json:"n"`
	// Secrets and Params give the values that the steps need and that no file holds. They are kept in memory
	// only, and the answers never hold them.
	Secrets map[string]string `json:"secrets"`
	Params  map[string]string `json:"params"`
	// ConfirmClear says that the person agreed to reset: clear on a physical device.
	ConfirmClear bool `json:"confirmClear"`
}

// postGoto resets the app and takes the device through the first n steps. A step that does not go
// through leaves the cursor at the last good one, and the answer says so with the frame of now.
func (s *Server) postGoto(w http.ResponseWriter, r *http.Request) {
	var req gotoRequest
	if !decode(w, r, &req) {
		return
	}
	secrets, params := req.Secrets, req.Params
	req.Secrets, req.Params = nil, nil
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.session(w)
	if sess == nil {
		return
	}
	// A step that is not there is refused before any value is kept.
	if n := len(sess.Steps()); req.N < 1 || req.N > n {
		localhttp.Fail(w, http.StatusBadRequest, fmt.Sprintf("There is no step %d: the recording has %d.", req.N, n))
		return
	}
	for name, value := range secrets {
		if err := sess.SetSecret(name, value); err != nil {
			localhttp.Fail(w, http.StatusBadRequest, oneLine(s.scrub(err.Error())))
			return
		}
	}
	clear(secrets)
	if req.ConfirmClear {
		sess.ConfirmClear()
	}
	for name, value := range params {
		if err := sess.SetParam(name, value); err != nil {
			localhttp.Fail(w, http.StatusBadRequest, oneLine(s.scrub(err.Error())))
			return
		}
	}
	f, err := sess.Goto(s.base, req.N)
	if err != nil && f != nil {
		// Some steps went through: the answer carries where the device is now.
		body := errorReply{Error: oneLine(s.scrub(err.Error())), Session: s.snapshot(s.base, f, true)}
		reply(w, http.StatusConflict, body)
		return
	}
	if err != nil {
		s.refuse(s.base, w, err)
		return
	}
	s.done(s.base, w, f, nil, true)
}

func (s *Server) getLocators(w http.ResponseWriter, r *http.Request) {
	n, ok := stepIndex(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.session(w)
	if sess == nil {
		return
	}
	list, err := sess.Locators(n + 1)
	if err != nil {
		s.refuse(s.base, w, err)
		return
	}
	if list == nil {
		list = []mobilerec.LocatorInfo{}
	}
	reply(w, http.StatusOK, struct {
		Locators []mobilerec.LocatorInfo `json:"locators"`
	}{list})
}

type locatorRequest struct {
	// Choice is the Kth of the locators of the step: it makes the step use one that it already has.
	Choice int `json:"choice"`
	// FrameID and Element (or X and Y) point the step at another element of the frame that shows the screen
	// before the step. Candidate is as in an action.
	FrameID   uint64 `json:"frameId"`
	Element   *int   `json:"element"`
	X         *int   `json:"x"`
	Y         *int   `json:"y"`
	Candidate int    `json:"candidate"`
}

// postLocator changes how the element of a step is found. It never rewrites the screens file: the element
// gets a new name when the recording is saved.
func (s *Server) postLocator(w http.ResponseWriter, r *http.Request) {
	n, ok := stepIndex(w, r)
	if !ok {
		return
	}
	var req locatorRequest
	if !decode(w, r, &req) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.session(w)
	if sess == nil {
		return
	}
	if req.Element != nil || req.X != nil {
		t, ok := target(req.Element, req.X, req.Y)
		if !ok {
			localhttp.Fail(w, http.StatusBadRequest, "Send the element of the frame, or a point of it.")
			return
		}
		if _, err := sess.Repick(s.base, n+1, req.FrameID, *t, req.Candidate); err != nil {
			s.refuse(s.base, w, err)
			return
		}
	} else {
		if req.Choice < 1 {
			localhttp.Fail(w, http.StatusBadRequest, "Send choice, the number of a locator from 1, or the element to use.")
			return
		}
		if err := sess.SetLocator(n+1, req.Choice); err != nil {
			s.refuse(s.base, w, err)
			return
		}
	}
	reply(w, http.StatusOK, s.snapshot(s.base, sess.Frame(), false))
}

// getCandidates lists the locators that find an element of a frame, with how many elements each finds.
func (s *Server) getCandidates(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	frameID, err := strconv.ParseUint(q.Get("frameId"), 10, 64)
	if err != nil {
		localhttp.Fail(w, http.StatusBadRequest, "Send frameId, the number of the frame that you looked at.")
		return
	}
	num := func(key string) *int {
		v, err := strconv.Atoi(q.Get(key))
		if err != nil {
			return nil
		}
		return &v
	}
	t, ok := target(num("index"), num("x"), num("y"))
	if !ok {
		localhttp.Fail(w, http.StatusBadRequest, "Send index, the element of the frame, or x and y.")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.session(w)
	if sess == nil {
		return
	}
	list, err := sess.Candidates(s.base, frameID, *t, q.Get("do"))
	if err != nil {
		s.refuse(s.base, w, err)
		return
	}
	if list.Usable == nil {
		list.Usable = []mobilerec.LocatorInfo{}
	}
	reply(w, http.StatusOK, list)
}

type captureRequest struct {
	FrameID   uint64 `json:"frameId"`
	Index     *int   `json:"index"`
	X         *int   `json:"x"`
	Y         *int   `json:"y"`
	Candidate int    `json:"candidate"`
	Name      string `json:"name"`
	Pattern   string `json:"pattern"`
	Screen    string `json:"screen"`
}

// postCapture keeps the text of an element, or the first group of a pattern on it, as {{name}} for the
// steps after it. It does nothing to the device, and the answer holds the value for a preview.
func (s *Server) postCapture(w http.ResponseWriter, r *http.Request) {
	var req captureRequest
	if !decode(w, r, &req) {
		return
	}
	t, ok := target(req.Index, req.X, req.Y)
	if !ok {
		localhttp.Fail(w, http.StatusBadRequest, "A capture needs an element: send its index, or a point of the screen.")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.session(w)
	if sess == nil {
		return
	}
	f, value, err := sess.CaptureValue(s.base, mobilerec.CaptureReq{
		Frame: req.FrameID, Target: *t, Candidate: req.Candidate, Name: req.Name, Pattern: req.Pattern, Screen: req.Screen,
	})
	if err != nil {
		s.refuse(s.base, w, err)
		return
	}
	out := s.snapshot(s.base, f, false)
	out.Captured = &capturedReply{Name: req.Name, Value: s.scrub(value)}
	reply(w, http.StatusOK, out)
}
