package export_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/export"
)

// sarifMirror holds exactly the fields that Plan 20 writes.
type sarifMirror struct {
	Version string `json:"version"`
	Runs    []struct {
		Tool struct {
			Driver struct {
				Name    string `json:"name"`
				Version string `json:"version"`
				Rules   []struct {
					ID               string `json:"id"`
					ShortDescription struct {
						Text string `json:"text"`
					} `json:"shortDescription"`
				} `json:"rules"`
			} `json:"driver"`
		} `json:"tool"`
		AutomationDetails struct {
			ID string `json:"id"`
		} `json:"automationDetails"`
		Results *[]struct {
			RuleID    string `json:"ruleId"`
			RuleIndex *int   `json:"ruleIndex"`
			Level     string `json:"level"`
			Message   struct {
				Text string `json:"text"`
			} `json:"message"`
			Locations []struct {
				PhysicalLocation struct {
					ArtifactLocation struct {
						URI string `json:"uri"`
					} `json:"artifactLocation"`
					Region *struct {
						StartLine int `json:"startLine"`
					} `json:"region"`
				} `json:"physicalLocation"`
			} `json:"locations"`
			PartialFingerprints map[string]string `json:"partialFingerprints"`
			Properties          map[string]string `json:"properties"`
		} `json:"results"`
	} `json:"runs"`
}

func writeSARIF(b *bytes.Buffer, r export.Run) error { return export.SARIF(b, r) }

func decodeSARIF(t *testing.T, data []byte) sarifMirror {
	t.Helper()
	var m sarifMirror
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("the output does not decode into the mirror: %v\n%s", err, data)
	}
	if len(m.Runs) != 1 || m.Runs[0].Results == nil {
		t.Fatalf("want one run with a results list:\n%s", data)
	}
	return m
}

func TestSARIFShape(t *testing.T) {
	m := decodeSARIF(t, render(t, writeSARIF, sampleRun()))
	if m.Version != "2.1.0" {
		t.Errorf("version = %q", m.Version)
	}
	run := m.Runs[0]
	if run.Tool.Driver.Name != "Semar" || run.Tool.Driver.Version != "0.9.0" {
		t.Errorf("driver = %+v", run.Tool.Driver)
	}
	rules := run.Tool.Driver.Rules
	if len(rules) != 3 || rules[0].ID != "semar/expected-result-not-met" || rules[1].ID != "semar/invariant-broken" || rules[2].ID != "semar/journey-failed" {
		t.Fatalf("rules = %+v", rules)
	}
	if run.AutomationDetails.ID != "semar/r-20261117T090000Z-1a2b" {
		t.Errorf("automationDetails = %+v", run.AutomationDetails)
	}
	results := *run.Results
	if len(results) != 3 {
		t.Fatalf("%d results, want 3: the confirmed false check and the invariant of SHOP-482-01, and the check of LEGACY-1", len(results))
	}
	for _, res := range results {
		if res.RuleIndex == nil || *res.RuleIndex < 0 || *res.RuleIndex > 1 || rules[*res.RuleIndex].ID != res.RuleID {
			t.Errorf("result %q has ruleIndex %v for rule %q", res.Message.Text, res.RuleIndex, res.RuleID)
		}
		if res.Level != "error" {
			t.Errorf("level = %q", res.Level)
		}
		for _, l := range res.Locations {
			uri := l.PhysicalLocation.ArtifactLocation.URI
			if !filepath.IsLocal(uri) || strings.Contains(uri, `\`) || strings.Contains(uri, "://") {
				t.Errorf("uri %q is not a relative, local path with slashes", uri)
			}
		}
		if len(res.PartialFingerprints["semar/v1"]) != 64 {
			t.Errorf("fingerprints = %v", res.PartialFingerprints)
		}
	}
	first := results[0]
	if first.RuleID != "semar/expected-result-not-met" || *first.RuleIndex != 0 {
		t.Errorf("first = %+v", first)
	}
	if first.Message.Text != "SHOP-482-01 step refund-too-much: expected status 422, observed status 200 (source ticket)" {
		t.Errorf("message = %q", first.Message.Text)
	}
	if len(first.Locations) != 1 || first.Locations[0].PhysicalLocation.ArtifactLocation.URI != ".semar/scenarios/SHOP-482/SHOP-482-01.feature" ||
		first.Locations[0].PhysicalLocation.Region == nil || first.Locations[0].PhysicalLocation.Region.StartLine != 14 {
		t.Errorf("locations = %+v", first.Locations)
	}
	if first.Properties["semarRun"] != "r-20261117T090000Z-1a2b" || first.Properties["semarScenario"] != "SHOP-482-01" || first.Properties["semarStep"] != "refund-too-much" {
		t.Errorf("properties = %v", first.Properties)
	}
	second := results[1]
	if second.RuleID != "semar/invariant-broken" || *second.RuleIndex != 1 {
		t.Errorf("second = %+v", second)
	}
	gone := results[2]
	if len(gone.Locations) != 0 || !strings.HasPrefix(gone.Message.Text, "LEGACY-1 ") {
		t.Errorf("a scenario with no file: %+v", gone)
	}
}

func TestSARIFOnlyFailed(t *testing.T) {
	r := sampleRun()
	var kept []export.Scenario
	for _, sc := range r.Scenarios {
		if sc.Result.Outcome != domain.OutcomeFailed {
			// a blocked or inconclusive scenario with a false check is still not a finding
			sc.Result.Checks = []domain.CheckResult{{Step: "x", Index: 0, Source: "ticket", State: domain.CheckFalse, Expected: "a", Observed: "b", Confirmed: true},
				{Step: "x", Index: -1, Source: domain.SourceInvariant, State: domain.CheckFalse, Expected: "c", Observed: "d", Confirmed: true}}
			kept = append(kept, sc)
		}
	}
	r.Scenarios = kept
	out := render(t, writeSARIF, r)
	m := decodeSARIF(t, out)
	if n := len(*m.Runs[0].Results); n != 0 {
		t.Errorf("%d results for a run with no failed scenario", n)
	}
	if !strings.Contains(string(out), `"results": []`) {
		t.Errorf("results is not an empty list:\n%s", out)
	}
}

func TestSARIFUnconfirmedFalseNotAFinding(t *testing.T) {
	r := oneOutcome(domain.OutcomeFailed)
	r.Scenarios[0].Result.Checks = []domain.CheckResult{
		{Step: "s", Index: 0, Source: "diff", State: domain.CheckFalse, Expected: "a", Observed: "b", Confirmed: false},
		{Step: "s", Index: 1, Source: "ticket", State: domain.CheckTrue, Expected: "a", Observed: "a", Confirmed: true},
		{Step: "s", Index: 2, Source: "ticket", State: domain.CheckNotEvaluated, Expected: "a", Confirmed: true},
		{Step: "s", Index: -1, Source: domain.SourceInvariant, State: domain.CheckTrue, Expected: "no server error", Observed: "status 200", Confirmed: true},
	}
	m := decodeSARIF(t, render(t, writeSARIF, r))
	if n := len(*m.Runs[0].Results); n != 0 {
		t.Errorf("%d results, want none", n)
	}
}

func TestSARIFInvariant(t *testing.T) {
	r := oneOutcome(domain.OutcomeFailed)
	r.Scenarios[0].Result.Checks = []domain.CheckResult{
		{Step: "refund", Index: -1, Source: domain.SourceInvariant, State: domain.CheckFalse, Expected: "no server error", Observed: "status 503", Detail: "the body is empty", Confirmed: true},
	}
	m := decodeSARIF(t, render(t, writeSARIF, r))
	res := *m.Runs[0].Results
	if len(res) != 1 || res[0].RuleID != "semar/invariant-broken" || *res[0].RuleIndex != 1 {
		t.Fatalf("results = %+v", res)
	}
	if got := res[0].Message.Text; !strings.Contains(got, "SHOP-482-01") || !strings.Contains(got, "no server error") || !strings.Contains(got, "status 503") {
		t.Errorf("message = %q", got)
	}
}

func TestSARIFRegionAndLocationAreLeftOutWhenUnknown(t *testing.T) {
	r := oneOutcome(domain.OutcomeFailed)
	r.Scenarios[0].Result.Checks = []domain.CheckResult{{Step: "nowhere", Index: 0, Source: "ticket", State: domain.CheckFalse, Expected: "a", Observed: "b", Confirmed: true}}
	out := render(t, writeSARIF, r)
	m := decodeSARIF(t, out)
	loc := (*m.Runs[0].Results)[0].Locations
	if len(loc) != 1 || loc[0].PhysicalLocation.Region != nil {
		t.Errorf("a step with no known line: locations = %+v", loc)
	}
	if strings.Contains(string(out), "region") {
		t.Errorf("the output holds a region:\n%s", out)
	}
}

func TestSARIFURIIsRelativeLocalAndEncoded(t *testing.T) {
	tests := []struct {
		name, file, want string
	}{
		{"spaces and hash", ".semar/scenarios/my dir/A #1.yaml", ".semar/scenarios/my%20dir/A%20%231.yaml"},
		{"percent", ".semar/scenarios/50%/A.yaml", ".semar/scenarios/50%25/A.yaml"},
		{"leaves the repository", "../outside/A.yaml", ""},
		{"absolute", "/etc/A.yaml", ""},
		{"scheme", "file:///etc/A.yaml", ""},
		{"colon in the first segment", "c:/A.yaml", ""},
		{"empty segment", ".semar//A.yaml", ""},
		{"dot segment", ".semar/./A.yaml", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := oneOutcome(domain.OutcomeFailed)
			r.Scenarios[0].File = tt.file
			r.Scenarios[0].Result.Checks = []domain.CheckResult{{Step: "s", Index: 0, Source: "ticket", State: domain.CheckFalse, Expected: "a", Observed: "b", Confirmed: true}}
			m := decodeSARIF(t, render(t, writeSARIF, r))
			loc := (*m.Runs[0].Results)[0].Locations
			got := ""
			if len(loc) == 1 {
				got = loc[0].PhysicalLocation.ArtifactLocation.URI
			}
			if got != tt.want {
				t.Errorf("uri = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSARIFFingerprintStable(t *testing.T) {
	first := sampleRun()
	second := sampleRun()
	second.Run.ID = "r-20261118T101500Z-ffff"
	second.Run.StartedAt = second.Run.StartedAt.Add(24 * 3600e9)
	second.Scenarios[0].DurationMS = 9999
	second.Scenarios[0].Result.Checks[0].Observed = "status 201"
	fp := func(r export.Run) []string {
		var out []string
		for _, res := range *decodeSARIF(t, render(t, writeSARIF, r)).Runs[0].Results {
			out = append(out, res.PartialFingerprints["semar/v1"])
		}
		return out
	}
	a, b := fp(first), fp(second)
	if len(a) != 3 || strings.Join(a, ",") != strings.Join(b, ",") {
		t.Errorf("fingerprints changed with the run:\n%v\n%v", a, b)
	}
	if a[0] == a[1] || a[1] == a[2] || a[0] == a[2] {
		t.Errorf("two findings share a fingerprint: %v", a)
	}

	// two invariants of one step are two findings
	r := oneOutcome(domain.OutcomeFailed)
	r.Scenarios[0].Result.Checks = []domain.CheckResult{
		{Step: "s", Index: -1, Source: domain.SourceInvariant, State: domain.CheckFalse, Expected: "no server error", Observed: "status 500", Confirmed: true},
		{Step: "s", Index: -1, Source: domain.SourceInvariant, State: domain.CheckFalse, Expected: "response matches the schema", Observed: "no", Confirmed: true},
	}
	got := fp(r)
	if len(got) != 2 || got[0] == got[1] {
		t.Errorf("two invariants of a step share a fingerprint: %v", got)
	}
}

func TestSARIFNoSchemaKey(t *testing.T) {
	out := string(render(t, writeSARIF, sampleRun()))
	if strings.Contains(out, "$schema") {
		t.Errorf("the output holds $schema:\n%s", out)
	}
}

func TestSARIFCutsLongText(t *testing.T) {
	r := oneOutcome(domain.OutcomeFailed)
	r.Scenarios[0].Result.Checks = []domain.CheckResult{{Step: "s", Index: 0, Source: "ticket", State: domain.CheckFalse,
		Expected: "a", Observed: strings.Repeat("o", 5000), Confirmed: true}}
	m := decodeSARIF(t, render(t, writeSARIF, r))
	if got := (*m.Runs[0].Results)[0].Message.Text; len([]rune(got)) > 1100 || !strings.Contains(got, "[cut: ") {
		t.Errorf("message holds %d characters", len([]rune(got)))
	}
}

func TestSARIFGolden(t *testing.T) {
	golden(t, "sarif.golden.json", render(t, writeSARIF, sampleRun()))
}

func TestSARIFDeterministic(t *testing.T) {
	if a, b := render(t, writeSARIF, sampleRun()), render(t, writeSARIF, sampleRun()); !bytes.Equal(a, b) {
		t.Error("two writes of the same run differ")
	}
}
