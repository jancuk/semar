package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// The hashed shape is private and has fixed keys. A field is encoded only when set,
// so adding an optional field to Check later does not change any stored hash.
type hashedHint struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

type hashedCheck struct {
	Status     *int     `json:"status,omitempty"`
	JSONPath   string   `json:"jsonpath,omitempty"`
	Header     string   `json:"header,omitempty"`
	Equals     any      `json:"equals,omitempty"`
	NotEquals  any      `json:"notEquals,omitempty"`
	Contains   any      `json:"contains,omitempty"`
	Exists     *bool    `json:"exists,omitempty"`
	Greater    *float64 `json:"greater,omitempty"`
	Less       *float64 `json:"less,omitempty"`
	Count      *int     `json:"count,omitempty"`
	EqualsPath string   `json:"equalsPath,omitempty"`
}

type hashedExpectation struct {
	Check  hashedCheck `json:"check"`
	Source Source      `json:"source"`
	Cite   string      `json:"cite,omitempty"`
}

type hashedStep struct {
	ID     StepID              `json:"id"`
	Setup  bool                `json:"setup,omitempty"`
	Hint   *hashedHint         `json:"hint,omitempty"`
	Expect []hashedExpectation `json:"expect,omitempty"`
}

type hashedGiven struct {
	Fixture FixtureID         `json:"fixture"`
	As      string            `json:"as"`
	With    map[string]string `json:"with,omitempty"`
	Lease   LeaseMode         `json:"lease,omitempty"`
}

// Given is last and encoded only when set, so a scenario without it hashes as before.
type hashedScenario struct {
	Intent      string        `json:"intent"`
	AuthProfile string        `json:"authProfile"`
	Steps       []hashedStep  `json:"steps"`
	Given       []hashedGiven `json:"given,omitempty"`
}

// ExpectHash covers what a person confirmed. Title and a step's Action text are left out
// so rewording does not invalidate a confirmation or a script.
// When encoding fails it returns the empty Hash: an empty hash matches no script,
// so the scenario cannot run on a guess.
func ExpectHash(s Scenario) Hash {
	h := hashedScenario{Intent: s.Intent, AuthProfile: s.AuthProfile, Steps: make([]hashedStep, len(s.Steps))}
	for i, st := range s.Steps {
		hs := hashedStep{ID: st.ID, Setup: st.Setup}
		if st.Hint != nil {
			hs.Hint = &hashedHint{Method: st.Hint.Method, Path: st.Hint.Path}
		}
		for _, e := range st.Expect {
			hs.Expect = append(hs.Expect, hashedExpectation{
				Check:  hashedCheck(e.Check),
				Source: e.Source,
				Cite:   e.Cite,
			})
		}
		h.Steps[i] = hs
	}
	for _, g := range s.Given {
		h.Given = append(h.Given, hashedGiven(g))
	}
	b, err := json.Marshal(h)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return Hash(hex.EncodeToString(sum[:]))
}
