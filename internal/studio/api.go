package studio

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"strings"

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
	// Flow says how a flow that the call ran ended.
	Flow string `json:"flow,omitempty"`
}

// errorReply is an error answer. A stale frame carries the frame that the screen shows now.
type errorReply struct {
	Error        string        `json:"error"`
	Stale        bool          `json:"stale,omitempty"`
	NeedsConfirm bool          `json:"needsConfirm,omitempty"`
	NeedsSecret  bool          `json:"needsSecret,omitempty"`
	Exists       bool          `json:"exists,omitempty"`
	Session      *sessionReply `json:"session,omitempty"`
}

// scrub takes the secrets of the session out of a message.
func (s *Server) scrub(msg string) string {
	if s.sess != nil {
		msg = s.sess.Overlay().Scrub(msg)
	}
	return msg
}

// failure answers an error of the session, in words that hold no secret. The caller holds the lock.
func (s *Server) failure(ctx context.Context, w http.ResponseWriter, err error) {
	body := errorReply{Error: oneLine(s.scrub(err.Error()))}
	code := http.StatusInternalServerError
	var stale *mobilerec.StaleError
	var act *mobilerec.ActError
	var unavailable *mobile.Unavailable
	switch {
	case errors.As(err, &stale):
		code, body.Stale = http.StatusConflict, true
		body.Session = s.snapshot(ctx, stale.Frame, true)
	case errors.Is(err, mobilerec.ErrNeedsConfirm):
		code, body.NeedsConfirm = http.StatusPreconditionRequired, true
	case errors.Is(err, mobilerec.ErrNeedsSecret):
		code, body.NeedsSecret = http.StatusUnprocessableEntity, true
	case errors.Is(err, mobilerec.ErrNothingToUndo):
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
	if f != nil {
		v := &FrameView{Frame: f}
		switch {
		case f.Secret:
			v.ImageHidden = true
		case withImage:
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
		out.Frame = v
	}
	return out
}

// jpegAddress is a PNG of the device as a small JPEG data address. A picture that cannot be decoded is
// sent as it is.
func jpegAddress(data []byte) string {
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
	kind, err := kindOf(req.Kind)
	if err != nil {
		localhttp.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.DeviceSerial != "" && !serialPattern.MatchString(req.DeviceSerial) {
		localhttp.Fail(w, http.StatusBadRequest, "The serial of the device is not one that adb gives.")
		return
	}
	if kind == mobilerec.KindFlow && len(req.UseFlows) > 0 {
		localhttp.Fail(w, http.StatusBadRequest, "A flow does not use another flow: record a scenario to run a flow first.")
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
	sess, err := s.d.NewSession(SessionRequest{
		Kind: kind, ID: req.ID, Ticket: ticket, Title: req.Title, Service: req.Service, Serial: req.DeviceSerial,
		Use: use, With: req.With, Replace: req.Replace, ConfirmClear: req.ConfirmClear,
	})
	if err != nil {
		localhttp.Fail(w, http.StatusBadRequest, oneLine(err.Error()))
		return
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
	act := mobilerec.Act{Do: req.Do, Frame: req.FrameID, Value: req.Value, Screen: req.Screen}
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
		Frame: req.FrameID, Target: *t, Kind: mobilerec.AssertKind(req.Kind), Value: req.Value, Attribute: req.Attribute, Screen: req.Screen,
	})
	// A check does nothing on the device, so the picture stays.
	s.done(s.base, w, f, err, false)
}

type screenRequest struct {
	Name string `json:"name"`
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
	f, err := sess.SetScreen(req.Name)
	s.done(s.base, w, f, err, false)
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
	err = sess.EditStep(n+1, mobilerec.Edit{ID: req.ID, Text: req.Text, Screen: req.Screen, Element: req.Element, Secret: req.SecretEnv})
	if err != nil {
		s.failure(s.base, w, err)
		return
	}
	// Turning a text into a secret reads the frame again, with the text taken out: the picture is as it was.
	reply(w, http.StatusOK, s.snapshot(s.base, sess.Frame(), false))
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
	if rerr != nil {
		res.Message += " The device could not be taken back: " + oneLine(s.scrub(rerr.Error()))
		f = nil
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
	if kind == mobilerec.KindFlow && len(req.UseFlows) > 0 {
		localhttp.Fail(w, http.StatusBadRequest, "A flow does not use another flow: record a scenario to run a flow first.")
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
