package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
)

// LatestVersion is the newest protocol revision that the server speaks.
const LatestVersion = "2025-11-25"

var supportedVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// SupportedVersions lists the protocol revisions that the server speaks, newest first.
func SupportedVersions() []string { return append([]string(nil), supportedVersions...) }

// VersionSupported reports whether v is a protocol revision that the server speaks.
func VersionSupported(v string) bool {
	for _, s := range supportedVersions {
		if s == v {
			return true
		}
	}
	return false
}

// Info says who the server is. Instructions is a short guide that clients may show to the model.
type Info struct {
	Name         string
	Version      string
	Instructions string
}

// ClientInfo is what the client said about itself in initialize.
type ClientInfo struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

// Content is one piece of a tool result: Type is "text" (Text) or "image" (Data as base64 and MimeType).
type Content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// Result is what a tool returns. Structured is optional machine-readable output; put the same facts in
// a text Content too, because not every client reads structuredContent.
type Result struct {
	Content    []Content `json:"content"`
	IsError    bool      `json:"isError,omitempty"`
	Structured any       `json:"structuredContent,omitempty"`
}

// TextResult is a result of one text block.
func TextResult(text string) Result {
	return Result{Content: []Content{{Type: "text", Text: text}}}
}

// ErrorResult is a tool failure that the model can read and react to.
func ErrorResult(text string) Result {
	return Result{Content: []Content{{Type: "text", Text: text}}, IsError: true}
}

// StructuredResult is a result that carries v as structuredContent and as JSON text.
func StructuredResult(v any) (Result, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return Result{}, fmt.Errorf("encode the result: %w", err)
	}
	return Result{Content: []Content{{Type: "text", Text: string(data)}}, Structured: v}, nil
}

// ImageResult is a result of one image and an optional caption.
func ImageResult(base64Data, mimeType, caption string) Result {
	r := Result{Content: []Content{{Type: "image", Data: base64Data, MimeType: mimeType}}}
	if caption != "" {
		r.Content = append(r.Content, Content{Type: "text", Text: caption})
	}
	return r
}

// Annotations are the hints that clients use to decide when to ask the person.
type Annotations struct {
	Title       string `json:"title,omitempty"`
	ReadOnly    *bool  `json:"readOnlyHint,omitempty"`
	Destructive *bool  `json:"destructiveHint,omitempty"`
	Idempotent  *bool  `json:"idempotentHint,omitempty"`
	OpenWorld   *bool  `json:"openWorldHint,omitempty"`
}

// BoolPtr returns a pointer to b, for the fields of Annotations.
func BoolPtr(b bool) *bool { return &b }

func (a Annotations) zero() bool {
	return a.Title == "" && a.ReadOnly == nil && a.Destructive == nil && a.Idempotent == nil && a.OpenWorld == nil
}

// Call is what a tool handler gets.
type Call struct {
	Name    string
	Args    json.RawMessage
	Client  ClientInfo
	Session string
	// Progress reports done of total (0 when unknown) with a short message. It does nothing unless the
	// call came over stdio with a progressToken in _meta. HTTP answers stay plain JSON, so it is a
	// no-op there. Never nil.
	Progress func(done, total float64, msg string)
}

// Handler runs a tool. A returned error becomes an isError result with the error text; return an
// ErrorResult directly to control the text. The context ends when the client cancels the call.
type Handler func(ctx context.Context, call Call) (Result, error)

// Middleware wraps the handler of a tool. It gets the tool so it can look at Surface, Write, and the
// annotations, for example to refuse writes while a person is in control.
type Middleware func(t Tool, next Handler) Handler

// Tool is one callable tool. Surface groups tools ("mobile", "web", "api", "journey", "control") and
// Write is true for a tool that changes something, that is, one that is not Annotations.ReadOnly.
// Neither is sent to the client.
type Tool struct {
	Name        string
	Title       string
	Description string
	Input       Schema
	Annotations Annotations
	Surface     string
	Write       bool
	Handler     Handler
}

type toolWire struct {
	Name        string       `json:"name"`
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description,omitempty"`
	InputSchema Schema       `json:"inputSchema"`
	Annotations *Annotations `json:"annotations,omitempty"`
}

// Session is the state of one client connection: what it said in initialize and its log level. A
// stdio process has one session; the HTTP transport makes one per Mcp-Session-Id.
type Session struct {
	id string

	mu          sync.Mutex
	client      ClientInfo
	version     string
	initialized bool
	level       string
	notify      func(method string, params any)
}

// NewSession makes a session with the given id.
func NewSession(id string) *Session { return &Session{id: id} }

// ID is the session id, which is empty for a throwaway session.
func (s *Session) ID() string { return s.id }

// Client is what the client said about itself.
func (s *Session) Client() ClientInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.client
}

// ProtocolVersion is the revision that was negotiated, or "" before initialize.
func (s *Session) ProtocolVersion() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// Initialized reports whether the client sent notifications/initialized.
func (s *Session) Initialized() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initialized
}

// LogLevel is the level that the client set with logging/setLevel, or "".
func (s *Session) LogLevel() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.level
}

// SetNotifier sets how the server sends a notification to this client. Only the stdio transport sets
// it; without it, progress is dropped.
func (s *Session) SetNotifier(fn func(method string, params any)) {
	s.mu.Lock()
	s.notify = fn
	s.mu.Unlock()
}

func (s *Session) notifier() func(method string, params any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notify
}

type flight struct {
	session   string
	cancel    context.CancelFunc
	cancelled atomic.Bool
}

// Server dispatches MCP requests to tools. It is safe for concurrent use.
type Server struct {
	info Info
	log  *slog.Logger

	mu       sync.RWMutex
	tools    []Tool
	byName   map[string]int
	mws      []Middleware
	handlers []Handler

	fmu      sync.Mutex
	inflight map[string]*flight
}

// NewServer checks the tools and builds a server. It rejects an empty or duplicate name, a missing
// handler, and an input schema outside the portable subset of Decision E8. A nil Input becomes an
// object with no properties. log may be nil.
func NewServer(info Info, tools []Tool, log *slog.Logger) (*Server, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := &Server{info: info, log: log, byName: map[string]int{}, inflight: map[string]*flight{}}
	for _, t := range tools {
		if t.Name == "" {
			return nil, errors.New("a tool has no name")
		}
		if _, dup := s.byName[t.Name]; dup {
			return nil, fmt.Errorf("the tool %q is registered twice", t.Name)
		}
		if t.Handler == nil {
			return nil, fmt.Errorf("the tool %q has no handler", t.Name)
		}
		if t.Input == nil {
			t.Input = Object(nil)
		}
		if err := ValidatePortableSchema(t.Input); err != nil {
			return nil, fmt.Errorf("the schema of tool %q: %w", t.Name, err)
		}
		s.byName[t.Name] = len(s.tools)
		s.tools = append(s.tools, t)
	}
	s.rebuild()
	return s, nil
}

// Use adds middleware. The first one added is the outermost, so it runs first. It applies to every
// tool and to calls that start after Use returns.
func (s *Server) Use(mw ...Middleware) {
	s.mu.Lock()
	s.mws = append(s.mws, mw...)
	s.rebuild()
	s.mu.Unlock()
}

// rebuild wraps every handler. The caller holds s.mu, or owns s.
func (s *Server) rebuild() {
	s.handlers = make([]Handler, len(s.tools))
	for i, t := range s.tools {
		h := t.Handler
		for j := len(s.mws) - 1; j >= 0; j-- {
			h = s.mws[j](t, h)
		}
		s.handlers[i] = h
	}
}

// Tools returns the registered tools in order.
func (s *Server) Tools() []Tool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Tool(nil), s.tools...)
}

// Info returns what the server says about itself.
func (s *Server) Info() Info { return s.info }

// Handle answers one raw message or batch. It returns nil when no answer is owed: a notification, a
// batch of notifications, or a call that the client cancelled. sess may be nil.
func (s *Server) Handle(ctx context.Context, sess *Session, raw []byte) []byte {
	return s.handle(ctx, sess, raw, nil)
}

// handle is Handle plus started, which runs once the call is registered as in flight (or at once for
// anything else). The stdio loop waits for it so a cancellation that arrives next finds its call.
func (s *Server) handle(ctx context.Context, sess *Session, raw []byte, started func()) []byte {
	var once sync.Once
	start := func() {
		if started != nil {
			once.Do(started)
		}
	}
	defer start()
	if sess == nil {
		sess = NewSession("")
	}
	msgs, batch, derr := Decode(raw)
	if derr != nil {
		return Response{ID: nil, Error: derr}.Marshal()
	}
	if !batch {
		if r := s.process(ctx, sess, msgs[0], false, start); r != nil {
			return r.Marshal()
		}
		return nil
	}
	start()
	var out [][]byte
	for _, m := range msgs {
		if r := s.process(ctx, sess, m, true, nil); r != nil {
			out = append(out, r.Marshal())
		}
	}
	if len(out) == 0 {
		return nil
	}
	return append(append([]byte("["), bytes.Join(out, []byte(","))...), ']')
}

func (s *Server) process(ctx context.Context, sess *Session, m Message, inBatch bool, start func()) *Response {
	if start == nil {
		start = func() {}
	}
	if m.Invalid != nil {
		start()
		return &Response{ID: m.ID, Error: m.Invalid}
	}
	if m.Response {
		start()
		return nil
	}
	if m.IsNotification() {
		start()
		s.notification(sess, m)
		return nil
	}
	var (
		result any
		rerr   *Error
	)
	switch m.Method {
	case "initialize":
		start()
		result, rerr = s.initialize(sess, m, inBatch)
	case "ping":
		start()
		result = struct{}{}
	case "tools/list":
		start()
		result = s.list()
	case "tools/call":
		var drop bool
		result, rerr, drop = s.callTool(ctx, sess, m, start)
		if drop {
			return nil
		}
	case "logging/setLevel":
		start()
		result, rerr = s.setLevel(sess, m)
	default:
		start()
		rerr = &Error{Code: CodeMethodNotFound, Message: fmt.Sprintf("The method %q is not supported.", m.Method)}
	}
	if rerr != nil {
		return &Response{ID: m.ID, Error: rerr}
	}
	enc, err := json.Marshal(result)
	if err != nil {
		s.log.Error("encode a result", "method", m.Method, "err", err)
		return errResponse(m.ID, CodeInternal, "Internal error.")
	}
	return &Response{ID: m.ID, Result: json.RawMessage(enc)}
}

func (s *Server) notification(sess *Session, m Message) {
	switch m.Method {
	case "notifications/initialized":
		sess.mu.Lock()
		sess.initialized = true
		sess.mu.Unlock()
	case "notifications/cancelled":
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
			Reason    string          `json:"reason"`
		}
		if json.Unmarshal(m.Params, &p) != nil || len(p.RequestID) == 0 {
			return
		}
		key := flightKey(sess.id, p.RequestID)
		s.fmu.Lock()
		f := s.inflight[key]
		s.fmu.Unlock()
		if f != nil {
			f.cancelled.Store(true)
			f.cancel()
		}
	}
}

func flightKey(session string, id json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, id); err != nil {
		buf.Reset()
		buf.Write(id)
	}
	return session + "\x00" + buf.String()
}

// CloseSession cancels every call that is still running for the session.
func (s *Server) CloseSession(id string) {
	s.fmu.Lock()
	var fl []*flight
	for _, f := range s.inflight {
		if f.session == id {
			fl = append(fl, f)
		}
	}
	s.fmu.Unlock()
	for _, f := range fl {
		f.cancelled.Store(true)
		f.cancel()
	}
}

func (s *Server) initialize(sess *Session, m Message, inBatch bool) (any, *Error) {
	if inBatch {
		return nil, &Error{Code: CodeInvalidRequest, Message: "initialize must not be part of a batch."}
	}
	var p struct {
		ProtocolVersion string     `json:"protocolVersion"`
		ClientInfo      ClientInfo `json:"clientInfo"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil || p.ProtocolVersion == "" {
		return nil, &Error{Code: CodeInvalidParams, Message: "initialize needs a protocolVersion string."}
	}
	version := LatestVersion
	if VersionSupported(p.ProtocolVersion) {
		version = p.ProtocolVersion
	}
	sess.mu.Lock()
	sess.client = p.ClientInfo
	sess.version = version
	sess.mu.Unlock()
	res := map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			"tools":   map[string]any{"listChanged": false},
			"logging": map[string]any{},
		},
		"serverInfo": map[string]any{"name": s.info.Name, "version": s.info.Version},
	}
	if s.info.Instructions != "" {
		res["instructions"] = s.info.Instructions
	}
	return res, nil
}

func (s *Server) list() any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]toolWire, len(s.tools))
	for i, t := range s.tools {
		w := toolWire{Name: t.Name, Title: t.Title, Description: t.Description, InputSchema: t.Input}
		if !t.Annotations.zero() {
			a := t.Annotations
			w.Annotations = &a
		}
		out[i] = w
	}
	return map[string]any{"tools": out}
}

var logLevels = map[string]bool{
	"debug": true, "info": true, "notice": true, "warning": true,
	"error": true, "critical": true, "alert": true, "emergency": true,
}

func (s *Server) setLevel(sess *Session, m Message) (any, *Error) {
	var p struct {
		Level string `json:"level"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil || !logLevels[p.Level] {
		return nil, &Error{Code: CodeInvalidParams, Message: "logging/setLevel needs a level such as debug, info, warning, or error."}
	}
	sess.mu.Lock()
	sess.level = p.Level
	sess.mu.Unlock()
	return struct{}{}, nil
}

func (s *Server) callTool(ctx context.Context, sess *Session, m Message, start func()) (res any, rerr *Error, drop bool) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Meta      struct {
			ProgressToken json.RawMessage `json:"progressToken"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil || p.Name == "" {
		start()
		return nil, &Error{Code: CodeInvalidParams, Message: "tools/call needs a tool name."}, false
	}
	s.mu.RLock()
	idx, ok := s.byName[p.Name]
	var h Handler
	if ok {
		h = s.handlers[idx]
	}
	s.mu.RUnlock()
	if !ok {
		start()
		return nil, &Error{Code: CodeInvalidParams, Message: fmt.Sprintf("Unknown tool %q.", p.Name)}, false
	}
	args := bytes.TrimSpace(p.Arguments)
	if len(args) == 0 || bytes.Equal(args, []byte("null")) {
		args = []byte("{}")
	}
	if args[0] != '{' {
		start()
		return ErrorResult("The arguments must be one JSON object."), nil, false
	}

	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	key := flightKey(sess.id, m.ID)
	f := &flight{session: sess.id, cancel: cancel}
	s.fmu.Lock()
	if _, busy := s.inflight[key]; busy {
		s.fmu.Unlock()
		start()
		return nil, &Error{Code: CodeInvalidRequest, Message: "That request id is already in use."}, false
	}
	s.inflight[key] = f
	s.fmu.Unlock()
	defer func() {
		s.fmu.Lock()
		delete(s.inflight, key)
		s.fmu.Unlock()
	}()
	start()

	var finished atomic.Bool
	defer finished.Store(true)
	progress := func(float64, float64, string) {}
	if notify := sess.notifier(); notify != nil && validProgressToken(p.Meta.ProgressToken) {
		token := p.Meta.ProgressToken
		progress = func(done, total float64, msg string) {
			if finished.Load() || cctx.Err() != nil {
				return
			}
			params := map[string]any{"progressToken": token, "progress": done}
			if total > 0 {
				params["total"] = total
			}
			if msg != "" {
				params["message"] = msg
			}
			notify("notifications/progress", params)
		}
	}
	call := Call{Name: p.Name, Args: json.RawMessage(args), Client: sess.Client(), Session: sess.id, Progress: progress}

	out, err, panicked := s.invoke(cctx, h, call)
	if f.cancelled.Load() || ctx.Err() != nil {
		return nil, nil, true
	}
	if panicked {
		return nil, &Error{Code: CodeInternal, Message: "Internal error."}, false
	}
	if err != nil {
		out = ErrorResult(err.Error())
	}
	if out.Content == nil {
		out.Content = []Content{}
	}
	return out, nil, false
}

func validProgressToken(t json.RawMessage) bool {
	t = bytes.TrimSpace(t)
	return len(t) > 0 && (t[0] == '"' || t[0] == '-' || (t[0] >= '0' && t[0] <= '9'))
}

// invoke runs a handler and turns a panic into a flag. The stack goes to the log, never to the client.
func (s *Server) invoke(ctx context.Context, h Handler, call Call) (res Result, err error, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("tool handler panicked", "tool", call.Name, "panic", fmt.Sprint(r), "stack", strings.TrimSpace(string(debug.Stack())))
			res, err, panicked = Result{}, nil, true
		}
	}()
	res, err = h(ctx, call)
	return res, err, false
}
