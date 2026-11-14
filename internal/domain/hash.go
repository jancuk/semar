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
// When encoding fails it returns the empty Hash: an empty hash matches no script,
// so the scenario cannot run on a guess.
func ExpectHash(s Scenario) Hash {
	h := hashedScenario{Intent: s.Intent, AuthProfile: s.AuthProfile, Steps: make([]hashedStep, len(s.Steps))}
	for i, st := range s.Steps {
		h.Steps[i] = hashedStep{ID: st.ID, Setup: st.Setup, Hint: st.Hint, Expect: st.Expect}
	}
	b, err := json.Marshal(h)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return Hash(hex.EncodeToString(sum[:]))
}
