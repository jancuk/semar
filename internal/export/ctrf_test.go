package export_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/export"
)

// The mirror holds exactly the fields that Plan 20 writes. A field that CTRF() adds and the
// mirror lacks fails the decode, so the test proves that nothing else is written.
type ctrfMirror struct {
	Results struct {
		Tool struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"tool"`
		Summary struct {
			Tests   *int  `json:"tests"`
			Passed  *int  `json:"passed"`
			Failed  *int  `json:"failed"`
			Pending *int  `json:"pending"`
			Skipped *int  `json:"skipped"`
			Other   *int  `json:"other"`
			Start   int64 `json:"start"`
			Stop    int64 `json:"stop"`
		} `json:"summary"`
		Tests []struct {
			Name     string            `json:"name"`
			Status   string            `json:"status"`
			Duration *int64            `json:"duration"`
			Message  *string           `json:"message"`
			Trace    *string           `json:"trace"`
			Suite    string            `json:"suite"`
			Extra    map[string]string `json:"extra"`
		} `json:"tests"`
		Environment *struct {
			TestEnvironment string `json:"testEnvironment"`
		} `json:"environment"`
		Extra map[string]string `json:"extra"`
	} `json:"results"`
}

func decodeCTRF(t *testing.T, data []byte) ctrfMirror {
	t.Helper()
	var m ctrfMirror
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("the output does not decode into the mirror: %v\n%s", err, data)
	}
	return m
}

func TestCTRFShape(t *testing.T) {
	m := decodeCTRF(t, render(t, func(b *bytes.Buffer, r export.Run) error { return export.CTRF(b, r) }, sampleRun()))
	res := m.Results
	if res.Tool.Name != "semar" || res.Tool.Version != "0.9.0" {
		t.Errorf("tool = %+v", res.Tool)
	}
	s := res.Summary
	for name, p := range map[string]*int{"tests": s.Tests, "passed": s.Passed, "failed": s.Failed, "pending": s.Pending, "skipped": s.Skipped, "other": s.Other} {
		if p == nil {
			t.Fatalf("the count %s is missing", name)
		}
	}
	if sum := *s.Passed + *s.Failed + *s.Pending + *s.Skipped + *s.Other; sum != *s.Tests || *s.Tests != len(res.Tests) {
		t.Errorf("counts %+v do not add up to tests = %d with %d tests listed", s, *s.Tests, len(res.Tests))
	}
	if *s.Passed != 1 || *s.Failed != 2 || *s.Skipped != 1 || *s.Other != 3 || *s.Pending != 0 {
		t.Errorf("counts = passed %d failed %d skipped %d other %d pending %d", *s.Passed, *s.Failed, *s.Skipped, *s.Other, *s.Pending)
	}
	if s.Start != 1794906000000 || s.Stop != 1794906012000 {
		t.Errorf("start %d, stop %d", s.Start, s.Stop)
	}
	for _, tc := range res.Tests {
		switch tc.Status {
		case "passed", "failed", "skipped", "pending", "other":
		default:
			t.Errorf("test %q has status %q", tc.Name, tc.Status)
		}
		if tc.Status == "pending" {
			t.Errorf("test %q is pending, which Semar never writes", tc.Name)
		}
		if tc.Name == "" || tc.Duration == nil {
			t.Errorf("test %+v lacks a name or an integer duration", tc)
		}
		if tc.Status == "passed" && tc.Message != nil {
			t.Errorf("a passed test %q has a message", tc.Name)
		}
	}
	first := res.Tests[0]
	if first.Name != "SHOP-482-01 Refund above the order total is rejected" || first.Status != "failed" || *first.Duration != 412 || first.Suite != "SHOP-482" {
		t.Errorf("first test = %+v", first)
	}
	if first.Message == nil || *first.Message != "step refund-too-much: expected status 422, observed status 200 (source ticket)" {
		t.Errorf("message = %v", first.Message)
	}
	if first.Trace == nil || strings.Count(*first.Trace, "\n") != 1 {
		t.Errorf("trace = %v, want both reasons, one per line", first.Trace)
	}
	if first.Extra["semarOutcome"] != "failed" || first.Extra["semarRun"] != "r-20261117T090000Z-1a2b" || first.Extra["semarEnvironment"] != "uat" {
		t.Errorf("extra = %v", first.Extra)
	}
	if res.Environment == nil || res.Environment.TestEnvironment != "uat" {
		t.Errorf("environment = %+v", res.Environment)
	}
	if res.Extra["semarRun"] != "r-20261117T090000Z-1a2b" || res.Extra["semarBuilds"] != "orders 9f3c2ab, payments 77aa01c" {
		t.Errorf("extra = %v", res.Extra)
	}
	last := res.Tests[len(res.Tests)-1]
	if last.Name != "LEGACY-1" || last.Suite != "" {
		t.Errorf("a scenario with no file: name %q, suite %q", last.Name, last.Suite)
	}
	unknown := res.Tests[5]
	if unknown.Extra["semarDurationUnknown"] != "true" || *unknown.Duration != 2000 {
		t.Errorf("a scenario with a large action: %+v", unknown)
	}
}

func TestCTRFMapping(t *testing.T) {
	tests := []struct {
		outcome domain.Outcome
		want    string
	}{
		{domain.OutcomePassed, "passed"},
		{domain.OutcomeFailed, "failed"},
		{domain.OutcomeNotRun, "skipped"},
		{domain.OutcomeBlocked, "other"},
		{domain.OutcomeInconclusive, "other"},
		{domain.OutcomeStale, "other"},
		{"unheard_of", "other"}, // a value that run.json should not hold is never a pass
	}
	for _, tt := range tests {
		t.Run(string(tt.outcome), func(t *testing.T) {
			m := decodeCTRF(t, render(t, func(b *bytes.Buffer, r export.Run) error { return export.CTRF(b, r) }, oneOutcome(tt.outcome)))
			got := m.Results.Tests[0]
			if got.Status != tt.want {
				t.Errorf("status = %q, want %q", got.Status, tt.want)
			}
			if got.Extra["semarOutcome"] != string(tt.outcome) {
				t.Errorf("extra.semarOutcome = %q, want %q", got.Extra["semarOutcome"], tt.outcome)
			}
			if tt.outcome == domain.OutcomePassed != (got.Message == nil) {
				t.Errorf("message = %v for outcome %s", got.Message, tt.outcome)
			}
		})
	}
}

func TestCTRFNoFormatVersionKeys(t *testing.T) {
	out := string(render(t, func(b *bytes.Buffer, r export.Run) error { return export.CTRF(b, r) }, sampleRun()))
	for _, key := range []string{"reportFormat", "specVersion"} {
		if strings.Contains(out, key) {
			t.Errorf("the output holds %q, which this plan does not verify", key)
		}
	}
}

func TestCTRFCutsLongText(t *testing.T) {
	r := oneOutcome(domain.OutcomeFailed)
	r.Scenarios[0].Result.Reasons = []domain.Reason{{Text: strings.Repeat("m", 1200)}, {Text: strings.Repeat("d", 9000)}}
	m := decodeCTRF(t, render(t, func(b *bytes.Buffer, r export.Run) error { return export.CTRF(b, r) }, r))
	tc := m.Results.Tests[0]
	if !strings.HasSuffix(*tc.Message, "[cut: 200 more characters]") {
		t.Errorf("message ends %q", (*tc.Message)[len(*tc.Message)-40:])
	}
	if !strings.Contains(*tc.Trace, "[cut: ") || len([]rune(*tc.Trace)) > 8100 {
		t.Errorf("trace holds %d characters", len([]rune(*tc.Trace)))
	}
}

func TestCTRFControlCharactersStayEscapedInJSON(t *testing.T) {
	r := oneOutcome(domain.OutcomeFailed)
	r.Scenarios[0].Title = "a\x1b[31mred\x00"
	out := render(t, func(b *bytes.Buffer, r export.Run) error { return export.CTRF(b, r) }, r)
	if bytes.ContainsAny(out, "\x1b\x00") {
		t.Errorf("the output holds a raw control character: %q", out)
	}
	decodeCTRF(t, out)
}

func TestCTRFGolden(t *testing.T) {
	golden(t, "ctrf.golden.json", render(t, func(b *bytes.Buffer, r export.Run) error { return export.CTRF(b, r) }, sampleRun()))
}

func TestCTRFDeterministic(t *testing.T) {
	w := func(b *bytes.Buffer, r export.Run) error { return export.CTRF(b, r) }
	a, b := render(t, w, sampleRun()), render(t, w, sampleRun())
	if !bytes.Equal(a, b) {
		t.Error("two writes of the same run differ")
	}
}

func TestCTRFCarriesTheModelCount(t *testing.T) {
	extra := func(r export.Run) map[string]string {
		return decodeCTRF(t, render(t, func(b *bytes.Buffer, r export.Run) error { return export.CTRF(b, r) }, r)).Results.Tests[0].Extra
	}
	if got := extra(modelChecks(oneOutcome(domain.OutcomePassed), 2))["semarByModel"]; got != "2" {
		t.Errorf("semarByModel = %q, want 2", got)
	}
	if v, ok := extra(modelChecks(oneOutcome(domain.OutcomePassed), 0))["semarByModel"]; ok {
		t.Errorf("semarByModel = %q with no model result", v)
	}
}
