package export

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/domain"
)

const (
	ruleExpectedNotMet = 0
	ruleInvariant      = 1
	ruleJourneyFailed  = 2
)

var sarifRules = []sarifRule{
	{ID: "semar/expected-result-not-met", ShortDescription: sarifText{Text: "An expected result was not met"}},
	{ID: "semar/invariant-broken", ShortDescription: sarifText{Text: "A built-in invariant was broken"}},
	{ID: "semar/journey-failed", ShortDescription: sarifText{Text: "A journey failed"}},
}

type sarifLog struct {
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool              sarifTool       `json:"tool"`
	AutomationDetails sarifAutomation `json:"automationDetails"`
	Results           []sarifResult   `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name    string      `json:"name"`
	Version string      `json:"version"`
	Rules   []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string    `json:"id"`
	ShortDescription sarifText `json:"shortDescription"`
}

type sarifText struct {
	Text string `json:"text"`
}

type sarifAutomation struct {
	ID string `json:"id"`
}

type sarifResult struct {
	RuleID              string            `json:"ruleId"`
	RuleIndex           int               `json:"ruleIndex"`
	Level               string            `json:"level"`
	Message             sarifText         `json:"message"`
	Locations           []sarifLocation   `json:"locations,omitempty"`
	PartialFingerprints map[string]string `json:"partialFingerprints"`
	Properties          map[string]string `json:"properties"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysical `json:"physicalLocation"`
}

type sarifPhysical struct {
	ArtifactLocation sarifArtifact `json:"artifactLocation"`
	Region           *sarifRegion  `json:"region,omitempty"`
}

type sarifArtifact struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine int `json:"startLine"`
}

// SARIF writes every expected result of the run that does not hold as SARIF 2.1.0 results. The $schema key
// is not written, because the address of the published schema is not verified.
func SARIF(w io.Writer, r Run) error {
	results := []sarifResult{}
	for _, sc := range r.Scenarios {
		if sc.Result.Outcome != domain.OutcomeFailed {
			continue
		}
		for _, c := range sc.Result.Checks {
			rule, ok := findingRule(c)
			if !ok {
				continue
			}
			results = append(results, sarifResultFor(r, sc, c, rule))
		}
	}
	for _, jc := range journeyCases(r) {
		if jc.Result.Outcome == domain.JourneyFailed {
			results = append(results, sarifJourneyResult(r, jc))
		}
	}
	log := sarifLog{Version: "2.1.0", Runs: []sarifRun{{
		Tool:              sarifTool{Driver: sarifDriver{Name: "Semar", Version: r.Version, Rules: sarifRules}},
		AutomationDetails: sarifAutomation{ID: "semar/" + string(r.Run.ID)},
		Results:           results,
	}}}
	data, err := atomicfile.EncodeJSON(log)
	if err != nil {
		return fmt.Errorf("encode sarif report of run %s: %w", r.Run.ID, err)
	}
	_, err = w.Write(data)
	return err
}

// sarifJourneyResult is the finding of a failed journey. It names the first failing leg and gives
// the reasons of the journey, which include each invariant that is false.
func sarifJourneyResult(r Run, jc journeyCase) sarifResult {
	id := string(jc.Result.Journey)
	msg := "journey " + id + " failed"
	if jc.Result.FirstFailing != "" {
		msg += " at leg " + string(jc.Result.FirstFailing)
	}
	if jc.Detail != "" {
		msg += ": " + strings.ReplaceAll(jc.Detail, "\n", "; ")
	}
	props := map[string]string{"semarRun": string(r.Run.ID), "semarJourney": id}
	if jc.Result.FirstFailing != "" {
		props["semarLeg"] = string(jc.Result.FirstFailing)
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{id, sarifRules[ruleJourneyFailed].ID}, "\x00")))
	return sarifResult{
		RuleID:              sarifRules[ruleJourneyFailed].ID,
		RuleIndex:           ruleJourneyFailed,
		Level:               "error",
		Message:             sarifText{Text: cutMessage(msg)},
		PartialFingerprints: map[string]string{"semar/v1": hex.EncodeToString(sum[:])},
		Properties:          props,
	}
}

// findingRule says which rule a check result breaks. Every false check is a finding, whoever
// wrote the expected result. A check that was not evaluated is not.
func findingRule(c domain.CheckResult) (int, bool) {
	if c.State != domain.CheckFalse {
		return 0, false
	}
	switch {
	case c.Source == domain.SourceInvariant:
		return ruleInvariant, true
	case c.Index >= 0:
		return ruleExpectedNotMet, true
	}
	return 0, false
}

func sarifResultFor(r Run, sc Scenario, c domain.CheckResult, rule int) sarifResult {
	id := string(sc.Result.Scenario)
	var msg string
	if rule == ruleInvariant {
		msg = fmt.Sprintf("%s step %s broke an invariant: expected %s, observed %s", id, c.Step, c.Expected, c.Observed)
	} else {
		by := ""
		if c.ByModel {
			by = ", by model"
		}
		msg = fmt.Sprintf("%s step %s: expected %s, observed %s (source %s%s)", id, c.Step, c.Expected, c.Observed, c.Source, by)
	}
	if c.Detail != "" {
		msg += ": " + c.Detail
	}
	res := sarifResult{
		RuleID:              sarifRules[rule].ID,
		RuleIndex:           rule,
		Level:               "error",
		Message:             sarifText{Text: cutMessage(msg)},
		PartialFingerprints: map[string]string{"semar/v1": fingerprint(id, c, sarifRules[rule].ID, rule)},
		Properties:          map[string]string{"semarRun": string(r.Run.ID), "semarScenario": id, "semarStep": string(c.Step)},
	}
	if uri, ok := artifactURI(sc.File); ok {
		loc := sarifPhysical{ArtifactLocation: sarifArtifact{URI: uri}}
		if line := sc.StepLines[c.Step]; line > 0 {
			loc.Region = &sarifRegion{StartLine: line}
		}
		res.Locations = []sarifLocation{{PhysicalLocation: loc}}
	}
	return res
}

// fingerprint is the SHA-256 of the scenario, step, check index, and rule, so that a reader
// matches one finding across runs. Every invariant has the index -1, so an invariant adds
// what it expected, which names it.
func fingerprint(scenario string, c domain.CheckResult, rule string, ruleNo int) string {
	parts := []string{scenario, string(c.Step), strconv.Itoa(c.Index), rule}
	if ruleNo == ruleInvariant {
		parts = append(parts, c.Expected)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// artifactURI turns a path from the repository root into a relative URI reference. A path
// that is not local gives no location, and each segment is percent-encoded.
func artifactURI(file string) (string, bool) {
	if file == "" || !filepath.IsLocal(filepath.FromSlash(file)) || path.Clean(file) != file {
		return "", false
	}
	segments := strings.Split(file, "/")
	// a colon in the first segment would read as the end of a scheme
	if strings.Contains(segments[0], ":") {
		return "", false
	}
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return strings.Join(segments, "/"), true
}
