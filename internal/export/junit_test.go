package export_test

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/export"
)

type junitProblem struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Text    string `xml:",chardata"`
}

type junitProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type junitCase struct {
	Name       string          `xml:"name,attr"`
	Classname  string          `xml:"classname,attr"`
	Time       string          `xml:"time,attr"`
	Properties []junitProperty `xml:"properties>property"`
	Failure    *junitProblem   `xml:"failure"`
	Error      *junitProblem   `xml:"error"`
	Skipped    *struct {
		Message string `xml:"message,attr"`
	} `xml:"skipped"`
	SystemOut string `xml:"system-out"`
}

type junitSuite struct {
	Name       string          `xml:"name,attr"`
	Tests      int             `xml:"tests,attr"`
	Failures   int             `xml:"failures,attr"`
	Errors     int             `xml:"errors,attr"`
	Skipped    int             `xml:"skipped,attr"`
	Time       string          `xml:"time,attr"`
	Timestamp  string          `xml:"timestamp,attr"`
	Properties []junitProperty `xml:"properties>property"`
	Cases      []junitCase     `xml:"testcase"`
}

type junitSuites struct {
	Name     string       `xml:"name,attr"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Errors   int          `xml:"errors,attr"`
	Skipped  int          `xml:"skipped,attr"`
	Time     string       `xml:"time,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

func writeJUnit(b *bytes.Buffer, r export.Run) error { return export.JUnit(b, r) }

func decodeJUnit(t *testing.T, data []byte) junitSuites {
	t.Helper()
	var s junitSuites
	if err := xml.Unmarshal(data, &s); err != nil {
		t.Fatalf("the output is not well-formed XML: %v\n%s", err, data)
	}
	return s
}

// allowed is the dialect of Plan 20: each element with the attributes it may carry.
var allowed = map[string][]string{
	"testsuites": {"name", "tests", "failures", "errors", "skipped", "time"},
	"testsuite":  {"name", "tests", "failures", "errors", "skipped", "time", "timestamp"},
	"properties": {},
	"property":   {"name", "value"},
	"testcase":   {"name", "classname", "time"},
	"failure":    {"message", "type"},
	"error":      {"message", "type"},
	"skipped":    {"message"},
	"system-out": {},
}

// onlyKnownNames walks the tokens, because encoding/xml ignores an element that a struct lacks.
func onlyKnownNames(t *testing.T, data []byte) {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		attrs, known := allowed[el.Name.Local]
		if !known {
			t.Errorf("the output holds the element %q", el.Name.Local)
			continue
		}
		for _, a := range el.Attr {
			if !slices.Contains(attrs, a.Name.Local) {
				t.Errorf("the element %q holds the attribute %q", el.Name.Local, a.Name.Local)
			}
		}
	}
}

func TestJUnitShape(t *testing.T) {
	data := render(t, writeJUnit, sampleRun())
	if !strings.HasPrefix(string(data), `<?xml version="1.0" encoding="UTF-8"?>`+"\n") {
		t.Errorf("the output starts %q", data[:60])
	}
	onlyKnownNames(t, data)
	s := decodeJUnit(t, data)
	if s.Name != "semar r-20261117T090000Z-1a2b" || s.Time != "12.000" {
		t.Errorf("testsuites = %q, time %q", s.Name, s.Time)
	}
	var tests, failures, errs, skipped int
	for _, suite := range s.Suites {
		var f, e, sk int
		for _, c := range suite.Cases {
			switch {
			case c.Failure != nil:
				f++
			case c.Error != nil:
				e++
			case c.Skipped != nil:
				sk++
			}
			if c.Classname != suite.Name {
				t.Errorf("case %q has classname %q in suite %q", c.Name, c.Classname, suite.Name)
			}
			if c.SystemOut == "" {
				t.Errorf("case %q has no semar outcome", c.Name)
			}
		}
		if suite.Tests != len(suite.Cases) || suite.Failures != f || suite.Errors != e || suite.Skipped != sk {
			t.Errorf("suite %q counts %d/%d/%d/%d, children %d/%d/%d/%d", suite.Name, suite.Tests, suite.Failures, suite.Errors, suite.Skipped, len(suite.Cases), f, e, sk)
		}
		tests, failures, errs, skipped = tests+suite.Tests, failures+suite.Failures, errs+suite.Errors, skipped+suite.Skipped
		if suite.Timestamp != "2026-11-17T09:00:00" {
			t.Errorf("timestamp = %q", suite.Timestamp)
		}
	}
	if s.Tests != tests || s.Failures != failures || s.Errors != errs || s.Skipped != skipped {
		t.Errorf("testsuites counts %d/%d/%d/%d, suites add up to %d/%d/%d/%d", s.Tests, s.Failures, s.Errors, s.Skipped, tests, failures, errs, skipped)
	}
	if s.Tests != 7 || s.Failures != 2 || s.Errors != 3 || s.Skipped != 1 {
		t.Errorf("counts = %d/%d/%d/%d, want 7/2/3/1", s.Tests, s.Failures, s.Errors, s.Skipped)
	}

	var names []string
	for _, suite := range s.Suites {
		names = append(names, suite.Name)
	}
	if want := []string{"SHOP-482", "PAY-9", "semar"}; !slices.Equal(names, want) {
		t.Errorf("suites = %v, want %v in the order the tickets first appear, and a scenario with no file in semar", names, want)
	}
	first := s.Suites[0]
	if first.Time != "2.027" {
		t.Errorf("suite time = %q, want the sum of its cases", first.Time)
	}
	props := map[string]string{}
	for _, p := range first.Properties {
		props[p.Name] = p.Value
	}
	if props["semar.run"] != "r-20261117T090000Z-1a2b" || props["semar.environment"] != "uat" || props["semar.build.orders"] != "9f3c2ab" || props["semar.build.payments"] != "77aa01c" {
		t.Errorf("properties = %v", props)
	}
	c := first.Cases[0]
	if c.Name != "SHOP-482-01 Refund above the order total is rejected" || c.Time != "0.412" {
		t.Errorf("first case = %+v", c)
	}
	if c.Failure == nil || c.Failure.Type != "failed" || c.Failure.Message != "step refund-too-much: expected status 422, observed status 200 (source ticket)" {
		t.Errorf("failure = %+v", c.Failure)
	}
	if lines := strings.Split(c.Failure.Text, "\n"); len(lines) != 2 {
		t.Errorf("failure text = %q, want every reason, one per line", c.Failure.Text)
	}
	if c.SystemOut != "semar outcome: failed" {
		t.Errorf("system-out = %q", c.SystemOut)
	}
	unknown := s.Suites[1].Cases[1]
	if len(unknown.Properties) != 1 || unknown.Properties[0].Name != "semar.durationUnknown" {
		t.Errorf("a case with a large action has properties %v", unknown.Properties)
	}
}

func TestJUnitMapping(t *testing.T) {
	tests := []struct {
		outcome domain.Outcome
		element string
		typ     string
	}{
		{domain.OutcomePassed, "", ""},
		{domain.OutcomeFailed, "failure", "failed"},
		{domain.OutcomeNotRun, "skipped", ""},
		{domain.OutcomeBlocked, "error", "blocked"},
		{domain.OutcomeInconclusive, "error", "inconclusive"},
		{domain.OutcomeStale, "error", "stale"},
		{"unheard_of", "error", "unheard_of"}, // never green
	}
	for _, tt := range tests {
		t.Run(string(tt.outcome), func(t *testing.T) {
			s := decodeJUnit(t, render(t, writeJUnit, oneOutcome(tt.outcome)))
			c := s.Suites[0].Cases[0]
			var got string
			var typ string
			switch {
			case c.Failure != nil:
				got, typ = "failure", c.Failure.Type
			case c.Error != nil:
				got, typ = "error", c.Error.Type
			case c.Skipped != nil:
				got = "skipped"
				if c.Skipped.Message != "a reason" {
					t.Errorf("skipped message = %q", c.Skipped.Message)
				}
			}
			if got != tt.element || typ != tt.typ {
				t.Errorf("element %q type %q, want %q and %q", got, typ, tt.element, tt.typ)
			}
			if want := "semar outcome: " + string(tt.outcome); c.SystemOut != want {
				t.Errorf("system-out = %q, want %q", c.SystemOut, want)
			}
		})
	}
}

func TestJUnitEscapes(t *testing.T) {
	r := oneOutcome(domain.OutcomeFailed)
	r.Scenarios[0].Title = `a <b> & "c" 'd' ` + "\x1b[31m\x00￾ end"
	r.Scenarios[0].Ticket = "T<1>\x07"
	r.Scenarios[0].Result.Reasons = []domain.Reason{{Text: "reason <&> \x1b]0;pwned\x07\nsecond"}}
	r.Run.Environment = "u&at\x1b"
	data := render(t, writeJUnit, r)
	if bytes.ContainsAny(data, "\x1b\x00\x07") {
		t.Errorf("the output holds a raw control character: %q", data)
	}
	onlyKnownNames(t, data)
	s := decodeJUnit(t, data)
	if got, want := s.Suites[0].Cases[0].Name, "SHOP-482-01 a <b> & \"c\" 'd' �[31m�� end"; got != want {
		t.Errorf("name = %q, want %q", got, want)
	}
	if got := s.Suites[0].Name; got != "T<1>�" {
		t.Errorf("suite = %q", got)
	}
	if got, want := s.Suites[0].Cases[0].Failure.Text, "reason <&> �]0;pwned�\nsecond"; got != want {
		t.Errorf("failure text = %q, want %q", got, want)
	}
}

func TestJUnitCutsLongText(t *testing.T) {
	r := oneOutcome(domain.OutcomeFailed)
	r.Scenarios[0].Result.Reasons = []domain.Reason{{Text: strings.Repeat("m", 1500)}, {Text: strings.Repeat("d", 9000)}}
	s := decodeJUnit(t, render(t, writeJUnit, r))
	f := s.Suites[0].Cases[0].Failure
	if !strings.HasSuffix(f.Message, "[cut: 500 more characters]") {
		t.Errorf("message ends %q", f.Message[len(f.Message)-30:])
	}
	if !strings.Contains(f.Text, "[cut: ") || len([]rune(f.Text)) > 8100 {
		t.Errorf("detail holds %d characters and no cut mark", len([]rune(f.Text)))
	}
}

func TestJUnitGolden(t *testing.T) {
	golden(t, "junit.golden.xml", render(t, writeJUnit, sampleRun()))
}

func TestJUnitDeterministic(t *testing.T) {
	if a, b := render(t, writeJUnit, sampleRun()), render(t, writeJUnit, sampleRun()); !bytes.Equal(a, b) {
		t.Error("two writes of the same run differ")
	}
}
