package mcp

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
)

// Header names of the Streamable HTTP transport and of the Semar token.
const (
	SessionHeader  = "Mcp-Session-Id"
	ProtocolHeader = "MCP-Protocol-Version"
	TokenHeader    = "X-Semar-Token"
)

const maxSessions = 256

// HTTPOptions configures NewHTTPHandler.
type HTTPOptions struct {
	// Token is the secret that every request must carry, as "Authorization: Bearer <token>" or in
	// X-Semar-Token. An empty token refuses every request.
	Token string
	// MaxBody is the largest request body in bytes. The default and the ceiling is MaxMessage.
	MaxBody int64
	// Path is the one URL that is served. The default is "/mcp".
	Path string
}

type httpHandler struct {
	s    *Server
	o    HTTPOptions
	mu   sync.Mutex
	sess map[string]*Session
}

// NewHTTPHandler serves the Streamable HTTP transport on one path for loopback clients: POST carries
// JSON-RPC and gets application/json (or 202 with no body when only notifications were sent), GET is
// 405 because the server never starts a stream, and DELETE with Mcp-Session-Id ends a session. Every
// request must pass these guards, in order: the Host is a loopback host with the port that the request
// arrived on (this defeats DNS rebinding), the Origin is absent or a loopback origin on that port, the
// token matches, the method and Content-Type are right, the body is at most MaxBody, and the
// MCP-Protocol-Version header, when present, is supported. Progress notifications are not sent over
// HTTP.
func NewHTTPHandler(s *Server, o HTTPOptions) http.Handler {
	if o.MaxBody <= 0 || o.MaxBody > MaxMessage {
		o.MaxBody = MaxMessage
	}
	if o.Path == "" {
		o.Path = "/mcp"
	}
	return &httpHandler{s: s, o: o, sess: map[string]*Session{}}
}

func (h *httpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hd := w.Header()
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Referrer-Policy", "no-referrer")

	port, ok := listenPort(r)
	if !ok || !hostAllowed(r.Host, port) {
		httpFail(w, http.StatusForbidden, "The Host header must be 127.0.0.1, localhost, or [::1] with the port of the server.")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && !originAllowed(origin, port) {
		httpFail(w, http.StatusForbidden, "The request comes from another origin.")
		return
	}
	if h.o.Token == "" || !tokenEqual(bearerOrHeader(r), h.o.Token) {
		hd.Set("WWW-Authenticate", "Bearer")
		httpFail(w, http.StatusUnauthorized, "The token is missing or wrong.")
		return
	}
	if r.URL.Path != h.o.Path {
		httpFail(w, http.StatusNotFound, "Not found.")
		return
	}
	switch r.Method {
	case http.MethodPost:
		h.post(w, r)
	case http.MethodDelete:
		h.delete(w, r)
	default:
		hd.Set("Allow", "POST, DELETE")
		httpFail(w, http.StatusMethodNotAllowed, "This server does not stream. Use POST.")
	}
}

func (h *httpHandler) post(w http.ResponseWriter, r *http.Request) {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		httpFail(w, http.StatusUnsupportedMediaType, "Send the request as application/json.")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.o.MaxBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpFail(w, http.StatusRequestEntityTooLarge, "The request is larger than 4 MiB.")
			return
		}
		httpFail(w, http.StatusBadRequest, "The request could not be read.")
		return
	}
	if v := r.Header.Get(ProtocolHeader); v != "" && !VersionSupported(v) {
		httpFail(w, http.StatusBadRequest, "The MCP-Protocol-Version is not supported.")
		return
	}

	msgs, batch, derr := Decode(body)
	if derr != nil {
		writeJSON(w, http.StatusBadRequest, Response{Error: derr}.Marshal())
		return
	}
	isInit := !batch && len(msgs) == 1 && msgs[0].Invalid == nil && !msgs[0].Response &&
		!msgs[0].IsNotification() && msgs[0].Method == "initialize"

	var sess *Session
	if isInit {
		id, err := newSessionID()
		if err != nil {
			httpFail(w, http.StatusInternalServerError, "A session could not be made.")
			return
		}
		sess = NewSession(id)
	} else {
		id := r.Header.Get(SessionHeader)
		if id == "" {
			httpFail(w, http.StatusBadRequest, "Initialize first, then send the Mcp-Session-Id header.")
			return
		}
		if sess = h.lookup(id); sess == nil {
			httpFail(w, http.StatusNotFound, "The session is unknown. Initialize again.")
			return
		}
	}

	resp := h.s.Handle(r.Context(), sess, body)
	if isInit {
		var out struct {
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal(resp, &out) == nil && len(out.Error) == 0 {
			if !h.add(sess) {
				httpFail(w, http.StatusServiceUnavailable, "Too many sessions. End one with DELETE.")
				return
			}
			w.Header().Set(SessionHeader, sess.ID())
		}
	}
	if resp == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *httpHandler) delete(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get(SessionHeader)
	if id == "" {
		httpFail(w, http.StatusBadRequest, "The Mcp-Session-Id header is missing.")
		return
	}
	h.mu.Lock()
	_, ok := h.sess[id]
	delete(h.sess, id)
	h.mu.Unlock()
	if !ok {
		httpFail(w, http.StatusNotFound, "The session is unknown.")
		return
	}
	h.s.CloseSession(id)
	w.WriteHeader(http.StatusNoContent)
}

func (h *httpHandler) lookup(id string) *Session {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sess[id]
}

func (h *httpHandler) add(s *Session) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.sess) >= maxSessions {
		return false
	}
	h.sess[s.ID()] = s
	return true
}

func newSessionID() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func writeJSON(w http.ResponseWriter, code int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(body)
}

func httpFail(w http.ResponseWriter, code int, msg string) {
	data, _ := json.Marshal(struct {
		Error string `json:"error"`
	}{msg})
	writeJSON(w, code, data)
}

// listenPort is the port that the request arrived on.
func listenPort(r *http.Request) (string, bool) {
	addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return "", false
	}
	_, port, err := net.SplitHostPort(addr.String())
	return port, err == nil
}

func hostAllowed(host, port string) bool {
	return host == "127.0.0.1:"+port || host == "localhost:"+port || host == "[::1]:"+port
}

func originAllowed(origin, port string) bool {
	return origin == "http://127.0.0.1:"+port || origin == "http://localhost:"+port || origin == "http://[::1]:"+port
}

// bearerOrHeader returns the token of "Authorization: Bearer <t>", or else X-Semar-Token. The
// internal/localhttp package has the same helper for the other local pages; this package stays on the
// standard library only.
func bearerOrHeader(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); len(auth) > 7 && strings.EqualFold(auth[:7], "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return r.Header.Get(TokenHeader)
}

func tokenEqual(a, b string) bool {
	return a != "" && b != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
