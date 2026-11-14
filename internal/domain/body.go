package domain

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// Body holds the bytes of a request or response body. It is stored as JSON,
// text, or base64, so it is always storable and readable; a JSON body comes
// back equal as JSON, with insignificant whitespace compacted.
type Body []byte

func (b Body) MarshalJSON() ([]byte, error) {
	switch {
	case len(b) == 0:
		return []byte("null"), nil
	case json.Valid(b) && utf8.Valid(b):
		return json.Marshal(struct {
			JSON json.RawMessage `json:"json"`
		}{json.RawMessage(b)})
	case utf8.Valid(b):
		return json.Marshal(struct {
			Text string `json:"text"`
		}{string(b)})
	default:
		return json.Marshal(struct {
			Base64 string `json:"base64"`
		}{base64.StdEncoding.EncodeToString(b)})
	}
}

func (b *Body) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*b = nil
		return nil
	}
	var form map[string]json.RawMessage
	if err := json.Unmarshal(data, &form); err != nil {
		return fmt.Errorf("body must be an object with one of json, text, or base64: %w", err)
	}
	if len(form) != 1 {
		return fmt.Errorf("body must have exactly one of json, text, or base64, got %d keys", len(form))
	}
	for key, raw := range form {
		switch key {
		case "json":
			var buf bytes.Buffer
			if err := json.Compact(&buf, raw); err != nil {
				return fmt.Errorf("body json: %w", err)
			}
			*b = buf.Bytes()
		case "text":
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				return fmt.Errorf("body text: %w", err)
			}
			*b = Body(s)
		case "base64":
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				return fmt.Errorf("body base64: %w", err)
			}
			decoded, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				return fmt.Errorf("body base64: %w", err)
			}
			*b = decoded
		default:
			return fmt.Errorf("unknown body key %q, want json, text, or base64", key)
		}
	}
	return nil
}
