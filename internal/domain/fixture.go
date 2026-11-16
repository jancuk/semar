package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

type FixtureID string // order.paid
type LeaseMode string // "" | exclusive

const LeaseExclusive LeaseMode = "exclusive"

// Given names a fixture and the name its outputs take in the scenario.
type Given struct {
	Fixture FixtureID         `yaml:"fixture"`
	As      string            `yaml:"as"`
	With    map[string]string `yaml:"with,omitempty"`
	Lease   LeaseMode         `yaml:"lease,omitempty"`
}

type Fixture struct {
	APIVersion  string           `yaml:"apiVersion"`
	Kind        string           `yaml:"kind"`
	ID          FixtureID        `yaml:"id"`
	Description string           `yaml:"description"`
	Version     string           `yaml:"version"`
	Owner       string           `yaml:"owner"`
	Params      map[string]Param `yaml:"params,omitempty"`
	Lease       LeaseMode        `yaml:"lease,omitempty"`
	Find        []FixtureStep    `yaml:"find,omitempty"`
	Create      []FixtureStep    `yaml:"create,omitempty"`
	Outputs     []string         `yaml:"outputs"`
	Cleanup     []FixtureStep    `yaml:"cleanup,omitempty"`
}

// Param has a default, or nil when every given entry must set it in with.
type Param struct {
	Default *string `yaml:"default,omitempty"`
}

type FixtureStep struct {
	Request  APIRequest `yaml:"request"`
	Captures []Capture  `yaml:"captures,omitempty"`
	Until    *Until     `yaml:"until,omitempty"`
}

// Until repeats a read until the value at JSONPath equals Equals. A fixed sleep has no type.
type Until struct {
	JSONPath string        `yaml:"jsonpath"`
	Equals   any           `yaml:"equals"`
	Timeout  time.Duration `yaml:"timeout"`
	Every    time.Duration `yaml:"every,omitempty"` // first wait, 2s by default, doubled up to 15s
}

// FixtureUse marks an action that a fixture sent.
type FixtureUse struct {
	ID    FixtureID `json:"id"`
	As    string    `json:"as"`
	Phase string    `json:"phase"` // find | create | cleanup
	Index int       `json:"index"` // 1-based position in the phase
}

// FixtureRecord is what one given entry did in one scenario. Every run stores it (FR-275, TRD rule 4).
type FixtureRecord struct {
	As      string            `json:"as"`
	Fixture FixtureID         `json:"fixture"`
	Version string            `json:"version"`
	Hash    Hash              `json:"hash"`
	Via     string            `json:"via"`               // find | create | none
	Outputs map[string]string `json:"outputs,omitempty"` // the redacted values of the recorded captures
	Cleanup string            `json:"cleanup"`           // done | failed | none | not_needed
	Left    []string          `json:"left,omitempty"`    // what may still exist, one plain sentence each
	Actions []ActionID        `json:"actions"`
}

// GivenFind is what the find steps of one given entry gave. Plan 16 reads it.
type GivenFind struct {
	As      string
	Fixture FixtureID
	Found   bool
	Problem string // why find could not be tried or failed; "" otherwise
	Actions []ActionID
	Outputs map[string]string `json:"-"` // raw values, for later requests only. Never stored
}

const (
	PhaseFind    = "find"
	PhaseCreate  = "create"
	PhaseCleanup = "cleanup"
)

// The hashed shape is private and has fixed keys, so a change to a yaml tag or to the name of a
// field cannot change a stored approval.
type hashedParam struct {
	Default *string `json:"default,omitempty"`
}

type hashedRequest struct {
	Service string            `json:"service"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   map[string]string `json:"query,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	JSON    any               `json:"json,omitempty"`
}

type hashedCapture struct {
	Name     string `json:"name"`
	JSONPath string `json:"jsonpath"`
}

type hashedUntil struct {
	JSONPath string `json:"jsonpath"`
	Equals   any    `json:"equals"`
	Timeout  int64  `json:"timeout"`
	Every    int64  `json:"every,omitempty"`
}

type hashedFixtureStep struct {
	Request  hashedRequest   `json:"request"`
	Captures []hashedCapture `json:"captures,omitempty"`
	Until    *hashedUntil    `json:"until,omitempty"`
}

type hashedFixture struct {
	ID          FixtureID              `json:"id"`
	Description string                 `json:"description"`
	Version     string                 `json:"version"`
	Owner       string                 `json:"owner"`
	Params      map[string]hashedParam `json:"params,omitempty"`
	Lease       LeaseMode              `json:"lease,omitempty"`
	Find        []hashedFixtureStep    `json:"find,omitempty"`
	Create      []hashedFixtureStep    `json:"create,omitempty"`
	Outputs     []string               `json:"outputs"`
	Cleanup     []hashedFixtureStep    `json:"cleanup,omitempty"`
}

func hashedSteps(in []FixtureStep) []hashedFixtureStep {
	if len(in) == 0 {
		return nil
	}
	out := make([]hashedFixtureStep, len(in))
	for i, st := range in {
		hs := hashedFixtureStep{Request: hashedRequest(st.Request)}
		for _, c := range st.Captures {
			hs.Captures = append(hs.Captures, hashedCapture(c))
		}
		if st.Until != nil {
			hs.Until = &hashedUntil{
				JSONPath: st.Until.JSONPath,
				Equals:   st.Until.Equals,
				Timeout:  int64(st.Until.Timeout),
				Every:    int64(st.Until.Every),
			}
		}
		out[i] = hs
	}
	return out
}

// FixtureHash covers everything a person approved: every field except APIVersion and Kind.
// When encoding fails it returns the empty Hash, which matches no approval.
func FixtureHash(f Fixture) Hash {
	h := hashedFixture{
		ID: f.ID, Description: f.Description, Version: f.Version, Owner: f.Owner,
		Lease: f.Lease, Outputs: f.Outputs,
		Find: hashedSteps(f.Find), Create: hashedSteps(f.Create), Cleanup: hashedSteps(f.Cleanup),
	}
	if len(f.Params) > 0 {
		h.Params = make(map[string]hashedParam, len(f.Params))
		for k, p := range f.Params {
			h.Params[k] = hashedParam(p)
		}
	}
	b, err := json.Marshal(h)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return Hash(hex.EncodeToString(sum[:]))
}
