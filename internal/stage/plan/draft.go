// Package plan turns a change set into scenarios and scripts through one planner job, and
// validates everything the planner returns before anything is written.
package plan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Draft is what the planner returns. It has no field for a source, a host, a secret, an auth
// profile, a scenario ID, or a file to write (R17, R18): code assigns all of them.
type Draft struct {
	Summary    string          `json:"summary"`
	Tickets    []TicketDraft   `json:"tickets"`
	Scenarios  []ScenarioDraft `json:"scenarios"`
	NotDrafted []NotDrafted    `json:"notDrafted"`
	Uncovered  []Uncovered     `json:"uncovered"`
	// Differences are where the planner says a document and the code disagree. Code checks
	// that both places exist, and cannot check that they disagree.
	Differences []Difference `json:"differences,omitempty"`
}

type TicketDraft struct {
	Key       string `json:"key"`
	Kind      string `json:"kind"` // behaviour | refactor | config | none
	Behaviour string `json:"behaviour"`
	Risk      string `json:"risk"` // low | standard | high
	Reason    string `json:"reason"`
}

type ScenarioDraft struct {
	Ticket string      `json:"ticket"`
	Title  string      `json:"title"`
	Intent string      `json:"intent"`
	Steps  []StepDraft `json:"steps"`
}

type StepDraft struct {
	ID       string            `json:"id"`
	Setup    bool              `json:"setup,omitempty"`
	Action   string            `json:"action"`
	Service  string            `json:"service,omitempty"`
	Method   string            `json:"method"`
	Hint     string            `json:"hint"`
	Path     string            `json:"path"`
	Query    map[string]string `json:"query,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	JSON     any               `json:"json,omitempty"`
	Captures []CaptureDraft    `json:"captures,omitempty"`
	Expect   []ExpectDraft     `json:"expect"`
}

type CaptureDraft struct {
	Name     string `json:"name"`
	JSONPath string `json:"jsonpath"`
}

type ExpectDraft struct {
	Check CheckDraft `json:"check"`
	Why   string     `json:"why"`
}

// CheckDraft has the keys of a check in a scenario file. The bounds stay as JSON text until
// code has checked that a float64 holds them exactly.
type CheckDraft struct {
	Status     *int         `json:"status,omitempty"`
	JSONPath   string       `json:"jsonpath,omitempty"`
	Header     string       `json:"header,omitempty"`
	Equals     any          `json:"equals,omitempty"`
	NotEquals  any          `json:"notEquals,omitempty"`
	Contains   any          `json:"contains,omitempty"`
	Exists     *bool        `json:"exists,omitempty"`
	Greater    *json.Number `json:"greater,omitempty"`
	Less       *json.Number `json:"less,omitempty"`
	Count      *int         `json:"count,omitempty"`
	EqualsPath string       `json:"equalsPath,omitempty"`
}

type NotDrafted struct {
	Ticket string `json:"ticket"`
	What   string `json:"what"`
	Needs  string `json:"needs"`
}

type Uncovered struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

// Difference is one place where a document of the repository and the code disagree.
type Difference struct {
	Ticket   string `json:"ticket"`
	Document string `json:"document"` // starts with <file>:<line> of a document
	Code     string `json:"code"`     // starts with <file>:<line> of the code
	What     string `json:"what"`
}

// DecodeDraft reads one JSON object strictly: an unknown field and trailing data are errors,
// and a number keeps its text.
func DecodeDraft(data []byte) (Draft, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	var d Draft
	if err := dec.Decode(&d); err != nil {
		return Draft{}, err
	}
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return Draft{}, fmt.Errorf("data follows the draft object")
	}
	return d, nil
}

// Schema is the JSON Schema of a draft. It uses only the keywords that every harness accepts in
// a tool schema. Code enforces what the schema cannot say, such as "exactly one of".
func Schema() json.RawMessage { return json.RawMessage(schema) }

const schema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["summary", "tickets", "scenarios", "notDrafted", "uncovered"],
  "properties": {
    "summary": {"type": "string"},
    "tickets": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["key", "kind", "behaviour", "risk", "reason"],
        "properties": {
          "key": {"type": "string"},
          "kind": {"type": "string", "enum": ["behaviour", "refactor", "config", "none"]},
          "behaviour": {"type": "string"},
          "risk": {"type": "string", "enum": ["low", "standard", "high"]},
          "reason": {"type": "string"}
        }
      }
    },
    "scenarios": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["ticket", "title", "intent", "steps"],
        "properties": {
          "ticket": {"type": "string"},
          "title": {"type": "string"},
          "intent": {"type": "string"},
          "steps": {
            "type": "array",
            "items": {
              "type": "object",
              "additionalProperties": false,
              "required": ["id", "action", "method", "hint", "path", "expect"],
              "properties": {
                "id": {"type": "string"},
                "setup": {"type": "boolean"},
                "action": {"type": "string"},
                "service": {"type": "string"},
                "method": {"type": "string", "enum": ["GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"]},
                "hint": {"type": "string"},
                "path": {"type": "string"},
                "query": {"type": "object"},
                "headers": {"type": "object"},
                "json": {},
                "captures": {
                  "type": "array",
                  "items": {
                    "type": "object",
                    "additionalProperties": false,
                    "required": ["name", "jsonpath"],
                    "properties": {
                      "name": {"type": "string"},
                      "jsonpath": {"type": "string"}
                    }
                  }
                },
                "expect": {
                  "type": "array",
                  "items": {
                    "type": "object",
                    "additionalProperties": false,
                    "required": ["check", "why"],
                    "properties": {
                      "check": {
                        "type": "object",
                        "additionalProperties": false,
                        "properties": {
                          "status": {"type": "integer"},
                          "jsonpath": {"type": "string"},
                          "header": {"type": "string"},
                          "equals": {},
                          "notEquals": {},
                          "contains": {},
                          "exists": {"type": "boolean"},
                          "greater": {"type": "number"},
                          "less": {"type": "number"},
                          "count": {"type": "integer"},
                          "equalsPath": {"type": "string"}
                        }
                      },
                      "why": {"type": "string"}
                    }
                  }
                }
              }
            }
          }
        }
      }
    },
    "notDrafted": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["ticket", "what", "needs"],
        "properties": {
          "ticket": {"type": "string"},
          "what": {"type": "string"},
          "needs": {"type": "string"}
        }
      }
    },
    "uncovered": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["file", "reason"],
        "properties": {
          "file": {"type": "string"},
          "reason": {"type": "string"}
        }
      }
    },
    "differences": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["ticket", "document", "code", "what"],
        "properties": {
          "ticket": {"type": "string"},
          "document": {"type": "string"},
          "code": {"type": "string"},
          "what": {"type": "string"}
        }
      }
    }
  }
}`
