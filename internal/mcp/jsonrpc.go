// Package mcp is a small Model Context Protocol server: JSON-RPC 2.0 framing, the tools capability,
// a stdio transport, and a Streamable HTTP transport. It imports only the standard library, so any
// part of Semar can use it without a cycle.
package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// MaxMessage is the largest message, in bytes, that any transport reads: one stdio line or one HTTP
// body.
const MaxMessage = 4 << 20

// maxBatch is the most entries in one JSON-RPC batch array.
const maxBatch = 256

// The JSON-RPC 2.0 error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603
)

// ID is a request id: a JSON string or number, echoed back exactly as it came.
type ID = json.RawMessage

// Request is a JSON-RPC request. A request with no id is a notification.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      ID              `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// IsNotification reports whether the request carries no id, so no answer is owed.
func (r Request) IsNotification() bool { return len(r.ID) == 0 }

// Response is a JSON-RPC answer. A nil ID marshals as null, which is what a parse error needs.
type Response struct {
	JSONRPC string `json:"jsonrpc"`
	ID      ID     `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   *Error `json:"error,omitempty"`
}

// Error is a JSON-RPC error object.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error makes *Error an error.
func (e *Error) Error() string { return fmt.Sprintf("json-rpc error %d: %s", e.Code, e.Message) }

// Marshal encodes the response as one line of JSON.
func (r Response) Marshal() []byte {
	r.JSONRPC = "2.0"
	data, err := json.Marshal(r)
	if err != nil {
		data = []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"The answer could not be encoded."}}`)
	}
	return data
}

// Message is one decoded entry of a message or batch.
type Message struct {
	Request
	// Invalid is set when the entry is not a valid request. ID holds its id when that could be read.
	Invalid *Error
	// Response is true for a client answer or error, which the server ignores.
	Response bool
}

type rawMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

// Decode reads one message or one batch from raw. The batch result is true when raw was an array.
// A non-nil error is a whole-message fault: a parse error, an empty or oversized batch, or a value that
// is neither an object nor an array. Faults of one entry are in Message.Invalid. Decode never panics
// and never reads more than MaxMessage bytes.
func Decode(raw []byte) (msgs []Message, batch bool, err *Error) {
	if len(raw) > MaxMessage {
		return nil, false, &Error{Code: CodeInvalidRequest, Message: "The message is larger than 4 MiB."}
	}
	t := bytes.TrimSpace(raw)
	if len(t) == 0 {
		return nil, false, &Error{Code: CodeParseError, Message: "The message is empty."}
	}
	if !json.Valid(t) {
		return nil, false, &Error{Code: CodeParseError, Message: "The message is not valid JSON."}
	}
	switch t[0] {
	case '{':
		return []Message{decodeOne(t)}, false, nil
	case '[':
		var items []json.RawMessage
		if e := json.Unmarshal(t, &items); e != nil {
			return nil, false, &Error{Code: CodeParseError, Message: "The message is not valid JSON."}
		}
		if len(items) == 0 {
			return nil, true, &Error{Code: CodeInvalidRequest, Message: "A batch must hold at least one request."}
		}
		if len(items) > maxBatch {
			return nil, true, &Error{Code: CodeInvalidRequest, Message: fmt.Sprintf("A batch holds at most %d requests.", maxBatch)}
		}
		out := make([]Message, len(items))
		for i, it := range items {
			out[i] = decodeOne(it)
		}
		return out, true, nil
	default:
		return nil, false, &Error{Code: CodeInvalidRequest, Message: "The message must be a JSON object or an array of them."}
	}
}

func decodeOne(item []byte) Message {
	invalid := func(id json.RawMessage, why string) Message {
		if !validID(id) {
			id = nil
		}
		return Message{Request: Request{ID: id}, Invalid: &Error{Code: CodeInvalidRequest, Message: why}}
	}
	var m rawMessage
	if err := json.Unmarshal(item, &m); err != nil {
		return invalid(nil, "The request must be a JSON object with jsonrpc, id, method, and params.")
	}
	if len(m.ID) > 0 && !validID(m.ID) {
		return invalid(nil, "The id must be a string or a number.")
	}
	if m.JSONRPC != "2.0" {
		return invalid(m.ID, `The jsonrpc member must be "2.0".`)
	}
	if m.Method == "" {
		if len(m.Result) > 0 || len(m.Error) > 0 {
			return Message{Request: Request{JSONRPC: m.JSONRPC, ID: m.ID}, Response: true}
		}
		return invalid(m.ID, "The request has no method.")
	}
	if p := bytes.TrimSpace(m.Params); len(p) > 0 && p[0] != '{' && p[0] != '[' && !bytes.Equal(p, []byte("null")) {
		return invalid(m.ID, "The params must be an object or an array.")
	}
	return Message{Request: Request{JSONRPC: m.JSONRPC, ID: m.ID, Method: m.Method, Params: m.Params}}
}

// validID reports whether id is a JSON string, number, or null.
func validID(id json.RawMessage) bool {
	if len(id) == 0 {
		return false
	}
	switch c := id[0]; {
	case c == '"', c == '-', c >= '0' && c <= '9':
		return true
	default:
		return bytes.Equal(id, []byte("null"))
	}
}

func errResponse(id ID, code int, msg string) *Response {
	return &Response{JSONRPC: "2.0", ID: id, Error: &Error{Code: code, Message: msg}}
}
