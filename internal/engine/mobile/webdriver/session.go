package webdriver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Session is one session of a server.
type Session struct {
	c  *Client
	ID string
	// Capabilities are those that the server gave back, as raw JSON.
	Capabilities json.RawMessage
}

// NewSession starts a session with the capabilities. They go in alwaysMatch, with one empty
// firstMatch, as the W3C protocol asks. The id is read from value.sessionId, and from the top level for
// a server that still speaks the legacy protocol.
func (c *Client) NewSession(ctx context.Context, caps map[string]any) (*Session, error) {
	if caps == nil {
		caps = map[string]any{}
	}
	body := map[string]any{"capabilities": map[string]any{"alwaysMatch": caps, "firstMatch": []map[string]any{{}}}}
	v, env, err := c.call(ctx, http.MethodPost, "/session", body, false)
	if err != nil {
		return nil, err
	}
	var s struct {
		SessionID    string          `json:"sessionId"`
		Capabilities json.RawMessage `json:"capabilities"`
	}
	_ = json.Unmarshal(v, &s)
	id := s.SessionID
	if id == "" {
		id = env.SessionID
	}
	if id == "" {
		return nil, errors.New("the server started no session: its answer holds no session id")
	}
	if !safeID.MatchString(id) {
		return nil, errors.New("the server gave a session id that is not an id")
	}
	return &Session{c: c, ID: id, Capabilities: s.Capabilities}, nil
}

// Attach is a session that the caller knows by its id, such as one that another process started.
func (c *Client) Attach(id string) (*Session, error) {
	if !safeID.MatchString(id) {
		return nil, errors.New("the session id is not an id")
	}
	return &Session{c: c, ID: id}, nil
}

func (s *Session) path(rest string) string { return "/session/" + s.ID + rest }

// Delete ends the session: DELETE /session/{id}.
func (s *Session) Delete(ctx context.Context) error {
	_, _, err := s.c.call(ctx, http.MethodDelete, s.path(""), nil, false)
	return err
}

// Source reads the page source: GET /session/{id}/source. It is XML on Android and on iOS.
func (s *Session) Source(ctx context.Context) (string, error) {
	v, _, err := s.c.call(ctx, http.MethodGet, s.path("/source"), nil, false)
	if err != nil {
		return "", err
	}
	var src string
	if err := json.Unmarshal(v, &src); err != nil {
		return "", errors.New("the source is not text")
	}
	return src, nil
}

// Screenshot gives a PNG: GET /session/{id}/screenshot.
func (s *Session) Screenshot(ctx context.Context) ([]byte, error) {
	v, _, err := s.c.call(ctx, http.MethodGet, s.path("/screenshot"), nil, false)
	if err != nil {
		return nil, err
	}
	var b64 string
	if err := json.Unmarshal(v, &b64); err != nil {
		return nil, errors.New("the screenshot is not text")
	}
	data, err := base64.StdEncoding.DecodeString(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' {
			return -1
		}
		return r
	}, b64))
	if err != nil {
		return nil, errors.New("the screenshot is not base64")
	}
	return data, nil
}

// Active gives the id of the focused element: GET /session/{id}/element/active.
func (s *Session) Active(ctx context.Context) (string, error) {
	v, _, err := s.c.call(ctx, http.MethodGet, s.path("/element/active"), nil, false)
	if err != nil {
		return "", err
	}
	return elementID(v)
}

// elementID reads the id of an element, under the W3C key or under ELEMENT.
func elementID(v json.RawMessage) (string, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(v, &m); err != nil {
		return "", errors.New("the server gave no element")
	}
	for _, key := range []string{w3cElementKey, legacyElementKey} {
		raw, ok := m[key]
		if !ok {
			continue
		}
		var id string
		if json.Unmarshal(raw, &id) != nil || !safeID.MatchString(id) {
			return "", errors.New("the server gave an element id that is not an id")
		}
		return id, nil
	}
	return "", errors.New("the server gave no element")
}

func (s *Session) element(id, rest string) (string, error) {
	if !safeID.MatchString(id) {
		return "", fmt.Errorf("the element id is not an id")
	}
	return s.path("/element/" + id + rest), nil
}

// Clear empties a field: POST /session/{id}/element/{e}/clear.
func (s *Session) Clear(ctx context.Context, element string) error {
	p, err := s.element(element, "/clear")
	if err != nil {
		return err
	}
	_, _, err = s.c.call(ctx, http.MethodPost, p, map[string]any{}, false)
	return err
}

// SetValue types text in a field: POST /session/{id}/element/{e}/value. The server appends it to what
// the field holds, so a caller that wants to replace the text clears the field first. An error of this
// call never holds the message of the server, which may quote the text.
func (s *Session) SetValue(ctx context.Context, element, text string) error {
	p, err := s.element(element, "/value")
	if err != nil {
		return err
	}
	_, _, err = s.c.call(ctx, http.MethodPost, p, map[string]any{"text": text}, true)
	return err
}

// Back presses the back button: POST /session/{id}/back.
func (s *Session) Back(ctx context.Context) error {
	_, _, err := s.c.call(ctx, http.MethodPost, s.path("/back"), map[string]any{}, false)
	return err
}

// Settings changes the settings of the server: POST /session/{id}/appium/settings.
func (s *Session) Settings(ctx context.Context, settings map[string]any) error {
	_, _, err := s.c.call(ctx, http.MethodPost, s.path("/appium/settings"), map[string]any{"settings": settings}, false)
	return err
}

// PressKeycode presses an Android key by its code. It is for the bare UiAutomator2 server, which has no
// execute endpoint: POST /session/{id}/appium/device/press_keycode.
func (s *Session) PressKeycode(ctx context.Context, code int) error {
	_, _, err := s.c.call(ctx, http.MethodPost, s.path("/appium/device/press_keycode"), map[string]any{"keycode": code}, false)
	return err
}

// Execute runs a script of the server, such as "mobile: deepLink": POST /session/{id}/execute/sync. A
// server that has no such script answers with an error that says so.
func (s *Session) Execute(ctx context.Context, script string, args map[string]any) (json.RawMessage, error) {
	if args == nil {
		args = map[string]any{}
	}
	v, _, err := s.c.call(ctx, http.MethodPost, s.path("/execute/sync"), map[string]any{"script": script, "args": []any{args}}, false)
	return v, err
}
