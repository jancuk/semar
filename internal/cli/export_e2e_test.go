package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/sampleapp"
	"github.com/jancuk/semar/internal/scenario"
)

const (
	exportCustomer  = "c-secret-4471" // a value at a configured JSON path, so it is redacted
	exportFailedID  = "SHOP-482-01"
	exportPassedID  = "SHOP-482-02"
	exportBlockedID = "SHOP-482-03"
)

// The mirrors hold exactly the fields that Plan 20 writes. A field that an export adds and a
// mirror lacks fails the decode.

type exportCTRF struct {
	Results struct {
		Tool struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"tool"`
		Summary struct {
			Tests, Passed, Failed, Pending, Skipped, Other int
			Start                                          int64 `json:"start"`
			Stop                                           int64 `json:"stop"`
		} `json:"summary"`
		Tests []struct {
			Name     string            `json:"name"`
			Status   string            `json:"status"`
			Duration *int64            `json:"duration"`
			Message  string            `json:"message"`
			Trace    string            `json:"trace"`
			Suite    string            `json:"suite"`
			Extra    map[string]string `json:"extra"`
		} `json:"tests"`
		Environment *struct {
			TestEnvironment string `json:"testEnvironment"`
		} `json:"environment"`
		Extra map[string]string `json:"extra"`
	} `json:"results"`
}

type exportSARIF struct {
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

// The JUnit mirror collects any element or attribute it does not name in Unknown, which a test
// then requires to be empty, because encoding/xml has no strict mode.
type exportUnknown struct {
	Elements []struct{ XMLName xml.Name } `xml:",any"`
	Attrs    []xml.Attr                   `xml:",any,attr"`
}

type exportJUnitProblem struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Text    string `xml:",chardata"`
	Unknown exportUnknown
}

type exportJUnitProperty struct {
	Name    string `xml:"name,attr"`
	Value   string `xml:"value,attr"`
	Unknown exportUnknown
}

type exportJUnitCase struct {
	Name       string                `xml:"name,attr"`
	Classname  string                `xml:"classname,attr"`
	Time       string                `xml:"time,attr"`
	Properties []exportJUnitProperty `xml:"properties>property"`
	Failure    *exportJUnitProblem   `xml:"failure"`
	Error      *exportJUnitProblem   `xml:"error"`
	Skipped    *struct {
		Message string `xml:"message,attr"`
		Unknown exportUnknown
	} `xml:"skipped"`
	SystemOut string `xml:"system-out"`
	Unknown   exportUnknown
}

type exportJUnitSuite struct {
	Name       string                `xml:"name,attr"`
	Tests      int                   `xml:"tests,attr"`
	Failures   int                   `xml:"failures,attr"`
	Errors     int                   `xml:"errors,attr"`
	Skipped    int                   `xml:"skipped,attr"`
	Time       string                `xml:"time,attr"`
	Timestamp  string                `xml:"timestamp,attr"`
	Properties []exportJUnitProperty `xml:"properties>property"`
	Cases      []exportJUnitCase     `xml:"testcase"`
	Unknown    exportUnknown
}

type exportJUnit struct {
	XMLName  xml.Name           `xml:"testsuites"`
	Name     string             `xml:"name,attr"`
	Tests    int                `xml:"tests,attr"`
	Failures int                `xml:"failures,attr"`
	Errors   int                `xml:"errors,attr"`
	Skipped  int                `xml:"skipped,attr"`
	Time     string             `xml:"time,attr"`
	Suites   []exportJUnitSuite `xml:"testsuite"`
	Unknown  exportUnknown
}

// exportedRun is a run exported in the three formats, each through cli.Run with --out.
type exportedRun struct {
	id    string
	raw   map[string][]byte // by format
	ctrf  exportCTRF
	junit exportJUnit
	sarif exportSARIF
}

func (e exportedRun) cases() []exportJUnitCase {
	var all []exportJUnitCase
	for _, s := range e.junit.Suites {
		all = append(all, s.Cases...)
	}
	return all
}

func (e exportedRun) testCase(t *testing.T, name string) exportJUnitCase {
	t.Helper()
	for _, c := range e.cases() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("the JUnit export has no test case %q", name)
	return exportJUnitCase{}
}

func strictJSON(t *testing.T, what string, data []byte, into any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		t.Fatalf("the %s export does not decode into its mirror: %v\n%s", what, err, data)
	}
	if dec.More() {
		t.Fatalf("the %s export holds more than one JSON value", what)
	}
}

func noUnknownXML(t *testing.T, where string, u exportUnknown) {
	t.Helper()
	if len(u.Elements) > 0 || len(u.Attrs) > 0 {
		t.Errorf("%s holds elements %v or attributes %v that Plan 20 does not write", where, u.Elements, u.Attrs)
	}
}

func (e *exportedRun) decode(t *testing.T) {
	t.Helper()
	strictJSON(t, "ctrf", e.raw["ctrf"], &e.ctrf)
	strictJSON(t, "sarif", e.raw["sarif"], &e.sarif)
	if err := xml.Unmarshal(e.raw["junit"], &e.junit); err != nil {
		t.Fatalf("the junit export does not decode: %v\n%s", err, e.raw["junit"])
	}
	noUnknownXML(t, "testsuites", e.junit.Unknown)
	for _, s := range e.junit.Suites {
		noUnknownXML(t, "testsuite "+s.Name, s.Unknown)
		for _, p := range s.Properties {
			noUnknownXML(t, "a property of "+s.Name, p.Unknown)
		}
		for _, c := range s.Cases {
			noUnknownXML(t, "testcase "+c.Name, c.Unknown)
			for _, p := range []*exportJUnitProblem{c.Failure, c.Error} {
				if p != nil {
					noUnknownXML(t, "a problem of "+c.Name, p.Unknown)
				}
			}
			if c.Skipped != nil {
				noUnknownXML(t, "skipped of "+c.Name, c.Skipped.Unknown)
			}
		}
	}
	if len(e.sarif.Runs) != 1 || e.sarif.Runs[0].Results == nil {
		t.Fatalf("the sarif export wants one run with a results list:\n%s", e.raw["sarif"])
	}
}

// exportAll exports the run in all three formats through cli.Run with --out, checks the file
// mode, and decodes each file with its mirror.
func (r *repo) exportAll(t *testing.T, id string) exportedRun {
	t.Helper()
	e := exportedRun{id: id, raw: map[string][]byte{}}
	dir := t.TempDir()
	for _, format := range []string{"ctrf", "junit", "sarif"} {
		out := filepath.Join(dir, id+"."+format)
		res := r.must(t, cli.ExitOK, "export", format, id, "--out", out)
		if res.stdout != "" {
			t.Errorf("export %s with --out printed %q", format, res.stdout)
		}
		contains(t, "the export message", res.stderr, "wrote "+format+" for run "+id+" to "+out)
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		e.raw[format] = data
	}
	e.decode(t)
	return e
}

// assertNoneOf fails when an export holds any form of a value.
func (e exportedRun) assertNoneOf(t *testing.T, values ...string) {
	t.Helper()
	for format, data := range e.raw {
		for _, v := range values {
			for _, form := range forms(v) {
				if bytes.Contains(data, []byte(form)) {
					t.Errorf("the %s export holds %q:\n%s", format, form, data)
				}
			}
		}
	}
}

// exportE2ERepo is the sample application, with three scenarios of ticket SHOP-482 that give one
// failed, one passed, and one blocked outcome, and a service that echoes the credential in a header. An
// interruption is set by arm: the next refund cancels the run while the request is in flight.
func exportE2ERepo(t *testing.T, arm *atomic.Pointer[func()]) *repo {
	t.Helper()
	r := newRepo(t, repoOptions{
		app: sampleapp.Options{Defects: defects(sampleapp.RefundOverTotal)},
		wrap: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("X-Echo", req.Header.Get("Authorization"))
				if cancel := arm.Load(); cancel != nil && strings.HasSuffix(req.URL.Path, "/refunds") {
					(*cancel)() // the write is in flight when the run is cancelled, as in a real interruption
				}
				next.ServeHTTP(w, req)
			})
		},
	})
	r.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "authProfiles:", "redact:\n  jsonPaths: [\"$.data.customer\"]\nauthProfiles:", 1)
	})

	// 01 fails: the refund above the total is accepted, and the customer and the echoed credential
	// are not what the checks say, so their observed values are redacted in the evidence.
	r.exportScenario(t, exportFailedID, "Refund above the order total is rejected", `
    Given Create a paid order for 100000
    # step: create-order; POST /v1/orders
    When Request a refund of 150000 for that order
    # step: refund-too-much; POST /v1/orders/{id}/refunds
    Then the status is 422
    # ticket SHOP-482: a refund above the order total must be rejected
    When Read the order again
    # step: order-unchanged; GET /v1/orders/{id}
    Then $.data.customer equals "nobody"
    # ticket SHOP-482: the customer is kept
    And the header X-Echo equals "nothing"
    # ticket SHOP-482: no credential is echoed
`, fmt.Sprintf(`
  - step: create-order
    request: { service: orders, method: POST, path: /v1/orders, json: { amount: 100000, customer: %s } }
    captures: [{ name: order_id, jsonpath: "$.data.id" }]
  - step: refund-too-much
    request: { service: orders, method: POST, path: "/v1/orders/{{order_id}}/refunds", json: { amount: 150000 } }
  - step: order-unchanged
    request: { service: orders, method: GET, path: "/v1/orders/{{order_id}}" }
`, exportCustomer))

	// 02 passes: a refund within the total is accepted.
	r.exportScenario(t, exportPassedID, "Refund within the order total is accepted", `
    Given Create a paid order for 100000
    # step: create-order; POST /v1/orders
    When Request a refund of 50000 for that order
    # step: refund-some; POST /v1/orders/{id}/refunds
    Then the status is 201
    # person
`, `
  - step: create-order
    request: { service: orders, method: POST, path: /v1/orders, json: { amount: 100000 } }
    captures: [{ name: order_id, jsonpath: "$.data.id" }]
  - step: refund-some
    request: { service: orders, method: POST, path: "/v1/orders/{{order_id}}/refunds", json: { amount: 50000 } }
`)

	// 03 is blocked: the setup order is refused, so the scenario cannot say anything about refunds.
	r.exportScenario(t, exportBlockedID, "Refund of a second order is accepted", `
    Given Create a paid order for 100000
    # step: create-order; POST /v1/orders
    When Request a refund of 50000 for that order
    # step: refund-some; POST /v1/orders/{id}/refunds
    Then the status is 201
    # person
`, `
  - step: create-order
    request: { service: orders, method: POST, path: /v1/orders, json: { amount: -5 } }
    captures: [{ name: order_id, jsonpath: "$.data.id" }]
  - step: refund-some
    request: { service: orders, method: POST, path: "/v1/orders/{{order_id}}/refunds", json: { amount: 50000 } }
`)
	// The original scenario of the tree stays out of the way.
	if err := os.Remove(r.scriptFile()); err != nil {
		t.Fatal(err)
	}
	return r
}

// exportScenario writes a scenario file and its script, adds the script, and confirms the scenario.
func (r *repo) exportScenario(t *testing.T, id, title, steps, scriptSteps string) {
	t.Helper()
	file := ".semar/scenarios/SHOP-482/" + id + ".feature"
	r.writeRel(t, file, "Feature: Refunds of an order\n\n  @id:"+id+" @ticket:SHOP-482 @surface:api @auth:support-agent\n  Scenario: "+title+"\n"+
		"    Refunds of an order follow its total.\n"+steps)
	sc, err := scenario.Load(r.path(filepath.FromSlash(file)))
	if err != nil {
		t.Fatal(err)
	}
	script := r.path("scripts", id+".yaml")
	r.writeRel(t, "scripts/"+id+".yaml", fmt.Sprintf("apiVersion: semar/v1\nkind: Script\nscenario: %s\nexpectHash: %q\n"+
		"authoredBy: { instance: hand, model: none, harness: none }\nsteps:%s", id, domain.ExpectHash(sc), scriptSteps))
	r.must(t, cli.ExitOK, "script", "add", script)
	r.must(t, cli.ExitOK, "review", id, "--yes")
}

func TestExportEndToEnd(t *testing.T) {
	var arm atomic.Pointer[func()]
	r := exportE2ERepo(t, &arm)

	res := r.must(t, cli.ExitFailed, "run", "--all")
	contains(t, "the summary", res.stdout, exportFailedID, exportPassedID, exportBlockedID)
	id := filepath.Base(r.latestRun(t))
	run := r.lastRun(t)
	want := map[domain.ScenarioID]domain.Outcome{exportFailedID: domain.OutcomeFailed, exportPassedID: domain.OutcomePassed, exportBlockedID: domain.OutcomeBlocked}
	if len(run.Results) != len(want) {
		t.Fatalf("run.json has %d results, want %d", len(run.Results), len(want))
	}
	falseChecks := 0
	for _, rr := range run.Results {
		if rr.Outcome != want[rr.Scenario] {
			t.Fatalf("scenario %s is %s, want %s: %+v", rr.Scenario, rr.Outcome, want[rr.Scenario], rr.Reasons)
		}
		for _, c := range rr.Checks {
			if c.State == domain.CheckFalse && c.Confirmed {
				falseChecks++
			}
		}
	}

	e := r.exportAll(t, id)
	titles := map[string]string{
		exportFailedID:  exportFailedID + " Refund above the order total is rejected",
		exportPassedID:  exportPassedID + " Refund within the order total is accepted",
		exportBlockedID: exportBlockedID + " Refund of a second order is accepted",
	}

	t.Run("CTRF", func(t *testing.T) {
		res := e.ctrf.Results
		if res.Tool.Name != "semar" || res.Tool.Version != cli.Version {
			t.Errorf("tool = %+v", res.Tool)
		}
		s := res.Summary
		if s.Tests != 3 || s.Passed != 1 || s.Failed != 1 || s.Skipped != 0 || s.Other != 1 || s.Pending != 0 || len(res.Tests) != 3 {
			t.Errorf("summary = %+v with %d tests, want 3 tests: 1 passed, 1 failed, 1 other", s, len(res.Tests))
		}
		if res.Environment == nil || res.Environment.TestEnvironment != "local" || res.Extra["semarRun"] != id {
			t.Errorf("environment %+v, extra %v", res.Environment, res.Extra)
		}
		got := map[string]string{}
		for _, tc := range res.Tests {
			got[tc.Name] = tc.Status
			if tc.Duration == nil || tc.Suite != "SHOP-482" {
				t.Errorf("test %+v lacks a duration or the suite SHOP-482", tc)
			}
			if tc.Status == "failed" {
				contains(t, "the failed message", tc.Message, "step refund-too-much: expected status 422, observed status 201")
				// The checks on the redacted values are not evaluated, so the reasons carry no value at all.
				contains(t, "the failed trace", tc.Trace, "expected status 422, observed status 201", "a check could not be evaluated: the value is redacted")
				if tc.Extra["semarOutcome"] != "failed" {
					t.Errorf("extra = %v", tc.Extra)
				}
			}
		}
		for sid, status := range map[string]string{exportFailedID: "failed", exportPassedID: "passed", exportBlockedID: "other"} {
			if got[titles[sid]] != status {
				t.Errorf("%s has status %q, want %q (all: %v)", sid, got[titles[sid]], status, got)
			}
		}
		for _, tc := range res.Tests {
			if tc.Name == titles[exportBlockedID] && tc.Extra["semarOutcome"] != "blocked" {
				t.Errorf("the blocked scenario carries %v, want its Semar outcome", tc.Extra)
			}
		}
	})

	t.Run("JUnit", func(t *testing.T) {
		j := e.junit
		if j.Tests != 3 || j.Failures != 1 || j.Errors != 1 || j.Skipped != 0 || len(e.cases()) != 3 {
			t.Errorf("testsuites = tests %d failures %d errors %d skipped %d, want 3, 1, 1, 0", j.Tests, j.Failures, j.Errors, j.Skipped)
		}
		if len(j.Suites) != 1 || j.Suites[0].Name != "SHOP-482" || j.Suites[0].Tests != 3 || j.Suites[0].Failures != 1 || j.Suites[0].Errors != 1 {
			t.Errorf("suites = %+v", j.Suites)
		}
		failed := e.testCase(t, titles[exportFailedID])
		if failed.Failure == nil || failed.Error != nil {
			t.Fatalf("the failed scenario = %+v", failed)
		}
		contains(t, "the failure", failed.Failure.Message+failed.Failure.Text, "expected status 422, observed status 201")
		if p := e.testCase(t, titles[exportPassedID]); p.Failure != nil || p.Error != nil || p.Skipped != nil {
			t.Errorf("the passed scenario = %+v", p)
		}
		if b := e.testCase(t, titles[exportBlockedID]); b.Error == nil || b.Failure != nil || b.Error.Type != "blocked" {
			t.Errorf("the blocked scenario = %+v, want an error of type blocked", b)
		}
	})

	t.Run("SARIF", func(t *testing.T) {
		sr := e.sarif.Runs[0]
		if e.sarif.Version != "2.1.0" || sr.Tool.Driver.Name != "Semar" {
			t.Errorf("version %q, driver %+v", e.sarif.Version, sr.Tool.Driver)
		}
		results := *sr.Results
		if len(results) != falseChecks || falseChecks != 1 {
			t.Fatalf("%d results for %d false confirmed checks, want 1:\n%s", len(results), falseChecks, e.raw["sarif"])
		}
		for _, x := range results {
			if len(x.Locations) != 1 || x.Locations[0].PhysicalLocation.ArtifactLocation.URI != ".semar/scenarios/SHOP-482/"+exportFailedID+".feature" {
				t.Errorf("result %+v is not located in the failed scenario file", x)
			}
			if x.Properties["semarScenario"] != "" && x.Properties["semarScenario"] != exportFailedID {
				t.Errorf("result of %q: only a failed scenario is a finding", x.Properties["semarScenario"])
			}
		}
		contains(t, "the first result", (*sr.Results)[0].Message.Text, "expected status 422", "observed status 201")
	})

	t.Run("the redacted values appear in no export", func(t *testing.T) {
		// The service did send both values, and the stored evidence holds their markers, so there was
		// something to leak. The checks on them are not evaluated, so no reason repeats them.
		marked := 0
		err := filepath.WalkDir(filepath.Join(r.latestRunOf(t, id), "exchanges"), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := os.ReadFile(p)
			if bytes.Contains(data, []byte("[redacted:")) {
				marked++
			}
			return err
		})
		if err != nil || marked == 0 {
			t.Fatalf("no stored exchange holds a redaction marker (%v), so the check below proves nothing", err)
		}
		e.assertNoneOf(t, token, "Bearer "+token, exportCustomer)
		// The stored copy of the script holds the literal that the person wrote in it, so it is skipped.
		scripts, err := filepath.Glob(r.path(".semar", "store", "scripts", exportFailedID, "*.yaml"))
		if err != nil || len(scripts) != 1 {
			t.Fatalf("stored scripts %v, %v", scripts, err)
		}
		r.assertNoSecret(t, []string{token, exportCustomer}, filepath.ToSlash(filepath.Join("store", "scripts", exportFailedID, filepath.Base(scripts[0]))))
	})

	t.Run("a report file is 0644 and is not replaced without --force", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "reports", "report.json")
		r.must(t, cli.ExitOK, "export", "ctrf", id, "--out", out)
		if runtime.GOOS != "windows" {
			info, err := os.Stat(out)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o644 {
				t.Errorf("mode = %v, want 0644", info.Mode().Perm())
			}
		}
		first, err := os.ReadFile(out)
		if err != nil || !bytes.Equal(first, e.raw["ctrf"]) {
			t.Fatalf("the file differs from the first export of the same run: %v", err)
		}

		if err := os.WriteFile(out, []byte("keep me"), 0o600); err != nil {
			t.Fatal(err)
		}
		refused := r.must(t, cli.ExitError, "export", "ctrf", id, "--out", out)
		contains(t, "stderr", refused.stderr, out, "--force")
		if got, _ := os.ReadFile(out); string(got) != "keep me" {
			t.Errorf("the file now holds %q", got)
		}
		r.must(t, cli.ExitOK, "export", "ctrf", id, "--out", out, "--force")
		if got, _ := os.ReadFile(out); !bytes.Equal(got, first) {
			t.Errorf("--force wrote %q", got)
		}
	})

	t.Run("an interrupted run is refused", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		stop := func() { cancel() }
		arm.Store(&stop)
		defer arm.Store(nil)
		if res := r.semar(t, ctx, "run", "--scenario", exportFailedID); res.code != cli.ExitUndecided {
			t.Fatalf("the interrupted run exited %d, want %d\nstdout:\n%s\nstderr:\n%s", res.code, cli.ExitUndecided, res.stdout, res.stderr)
		}
		interrupted := filepath.Base(r.latestRun(t))
		if interrupted == id || exists(t, filepath.Join(r.latestRun(t), "run.json")) {
			t.Fatalf("run %s is not an interrupted run", interrupted)
		}
		for _, format := range []string{"ctrf", "junit", "sarif"} {
			out := filepath.Join(t.TempDir(), "report")
			for _, args := range [][]string{{"export", format, interrupted}, {"export", format, interrupted, "--out", out}} {
				refused := r.must(t, cli.ExitError, args...)
				contains(t, "stderr", refused.stderr, "run "+interrupted+" is interrupted")
				if refused.stdout != "" {
					t.Errorf("a refused export printed %q", refused.stdout)
				}
			}
			if exists(t, out) {
				t.Errorf("a file was written for an interrupted run")
			}
		}
	})
}

// A journey of the run is a test case of its own, and it is failed when an invariant is false even
// though every leg passed.
func TestExportJourneyEndToEnd(t *testing.T) {
	cases := []struct {
		name      string
		pay       sampleapp.PaymentOptions
		code      int
		outcome   domain.JourneyOutcome
		status    string // CTRF
		failures  int    // JUnit failures
		sarifRule string
	}{
		{"passed", sampleapp.PaymentOptions{}, cli.ExitOK, domain.JourneyPassed, "passed", 0, ""},
		{"a false invariant", sampleapp.PaymentOptions{Defects: defects(sampleapp.PaymentAmountWrong)}, cli.ExitFailed, domain.JourneyFailed, "failed", 1, "semar/journey-failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := journeyRepo(t, sampleapp.Options{}, tc.pay)
			r.must(t, tc.code, "journey", "run", "checkout")
			j := singleJourney(t, r)
			if j.Outcome != tc.outcome {
				t.Fatalf("the journey is %s, want %s", j.Outcome, tc.outcome)
			}
			legs := len(j.Legs)
			if tc.name == "a false invariant" {
				for _, l := range j.Legs {
					if l.Outcome != domain.OutcomePassed {
						t.Fatalf("leg %s is %s: every leg must pass for this case", l.Leg, l.Outcome)
					}
				}
			}
			e := r.exportAll(t, filepath.Base(r.latestRun(t)))

			s := e.ctrf.Results.Summary
			if s.Tests != legs+1 || s.Passed != legs+1-tc.failures || s.Failed != tc.failures {
				t.Errorf("summary = %+v, want %d tests with %d failed", s, legs+1, tc.failures)
			}
			last := e.ctrf.Results.Tests[len(e.ctrf.Results.Tests)-1]
			if last.Name != "journey checkout" || last.Suite != "semar journeys" || last.Status != tc.status || last.Extra["semarOutcome"] != string(tc.outcome) {
				t.Errorf("the journey test = %+v, want status %q", last, tc.status)
			}
			if tc.failures > 0 {
				contains(t, "the journey message", last.Message, "invariant 1 is false: 1000, 1001, 1000")
			}

			jt := e.testCase(t, "journey checkout")
			if e.junit.Tests != legs+1 || e.junit.Failures != tc.failures || e.junit.Errors != 0 || jt.Classname != "semar journeys" {
				t.Errorf("junit = tests %d failures %d errors %d, journey case %+v", e.junit.Tests, e.junit.Failures, e.junit.Errors, jt)
			}
			if (jt.Failure != nil) != (tc.failures > 0) {
				t.Errorf("the journey case = %+v, want failure %v", jt, tc.failures > 0)
			}
			if jt.Failure != nil {
				contains(t, "the journey failure", jt.Failure.Message, "invariant 1 is false: 1000, 1001, 1000")
			}
			for _, name := range checkoutLegs {
				if c := e.testCase(t, name); c.Failure != nil || c.Error != nil {
					t.Errorf("leg %s = %+v, want a pass", name, c)
				}
			}

			results := *e.sarif.Runs[0].Results
			if tc.sarifRule == "" {
				if len(results) != 0 {
					t.Errorf("a passed journey gave %d SARIF results", len(results))
				}
				return
			}
			if len(results) != 1 || results[0].RuleID != tc.sarifRule {
				t.Fatalf("results = %+v, want one of rule %s", results, tc.sarifRule)
			}
			contains(t, "the SARIF result", results[0].Message.Text, "checkout", "invariant 1 is false: 1000, 1001, 1000")
			e.assertNoneOf(t, token, "tok-pay")
		})
	}
}

func (r *repo) latestRunOf(t *testing.T, id string) string {
	t.Helper()
	return r.path(".semar", "runs", id)
}

// An unconfirmed scenario is inconclusive and is no finding.
func TestExportEndToEndInconclusive(t *testing.T) {
	r := newRepo(t, repoOptions{})
	r.must(t, cli.ExitOK, "script", "add", r.scriptFile())

	r.must(t, cli.ExitUndecided, "run", "--scenario", scenarioID)
	e := r.exportAll(t, filepath.Base(r.latestRun(t)))
	if s := e.ctrf.Results.Summary; s.Tests != 1 || s.Other != 1 || s.Passed != 0 || s.Failed != 0 || e.ctrf.Results.Tests[0].Extra["semarOutcome"] != "inconclusive" {
		t.Errorf("ctrf summary = %+v, tests %+v", s, e.ctrf.Results.Tests)
	}
	if e.junit.Tests != 1 || e.junit.Errors != 1 || e.junit.Failures != 0 || e.cases()[0].Error == nil || e.cases()[0].Error.Type != "inconclusive" {
		t.Errorf("junit = %+v", e.junit)
	}
	if n := len(*e.sarif.Runs[0].Results); n != 0 {
		t.Errorf("an inconclusive scenario gave %d SARIF results", n)
	}
}

// A server error is a broken invariant, and a run whose evidence changed after it was written is
// refused by every format.
func TestExportEndToEndInvariantAndChangedEvidence(t *testing.T) {
	r := newRepo(t, repoOptions{app: sampleapp.Options{Defects: defects(sampleapp.RefundServerError)}})
	r.addScriptAndConfirm(t)
	r.must(t, cli.ExitFailed, "run", "--scenario", scenarioID)
	id := filepath.Base(r.latestRun(t))
	e := r.exportAll(t, id)
	results := *e.sarif.Runs[0].Results
	if len(results) == 0 || e.ctrf.Results.Summary.Failed != 1 || e.junit.Failures != 1 {
		t.Fatalf("sarif results %+v, ctrf %+v, junit failures %d", results, e.ctrf.Results.Summary, e.junit.Failures)
	}
	invariant := 0
	for _, x := range results {
		if x.RuleID == "semar/invariant-broken" {
			invariant++
		}
	}
	if invariant != 1 {
		t.Errorf("%d results of rule semar/invariant-broken, want 1:\n%s", invariant, e.raw["sarif"])
	}
	e.assertNoneOf(t, token, "Bearer "+token)

	exchanges, err := filepath.Glob(filepath.Join(r.latestRun(t), "exchanges", scenarioID, "*"))
	if err != nil || len(exchanges) == 0 {
		t.Fatalf("exchanges %v, %v", exchanges, err)
	}
	f, err := os.OpenFile(exchanges[0], os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(" "); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"ctrf", "junit", "sarif"} {
		out := filepath.Join(t.TempDir(), "report")
		refused := r.must(t, cli.ExitError, "export", format, id, "--out", out)
		contains(t, "stderr", refused.stderr, "changed after it was written", filepath.ToSlash(filepath.Join("exchanges", scenarioID, filepath.Base(exchanges[0]))))
		if exists(t, out) {
			t.Errorf("a %s file was written for a run that changed", format)
		}
	}
}
