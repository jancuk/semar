package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type hashedStep struct {
	ID     StepID
	Setup  bool
	Hint   *RequestHint
	Expect []Expectation
}

type hashedScenario struct {
	Intent      string
	AuthProfile string
	Steps       []hashedStep
}

// ExpectHash covers what a person confirmed. Title and a step's Action text are left out
// so rewording does not invalidate a confirmation or a script.
func ExpectHash(s Scenario) Hash {
	h := hashedScenario{Intent: s.Intent, AuthProfile: s.AuthProfile, Steps: make([]hashedStep, len(s.Steps))}
	for i, st := range s.Steps {
		h.Steps[i] = hashedStep{ID: st.ID, Setup: st.Setup, Hint: st.Hint, Expect: st.Expect}
	}
	// Check values decoded from YAML are JSON-marshalable, so the error is unreachable.
	b, _ := json.Marshal(h)
	sum := sha256.Sum256(b)
	return Hash(hex.EncodeToString(sum[:]))
}
