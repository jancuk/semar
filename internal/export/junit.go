package export

import (
	"encoding/xml"
	"fmt"
	"io"
	"sort"

	"github.com/jancuk/semar/internal/domain"
)

// suiteForNoTicket holds a scenario whose file is gone or names no ticket.
const suiteForNoTicket = "semar"

type junitSuites struct {
	XMLName  xml.Name      `xml:"testsuites"`
	Name     string        `xml:"name,attr"`
	Tests    int           `xml:"tests,attr"`
	Failures int           `xml:"failures,attr"`
	Errors   int           `xml:"errors,attr"`
	Skipped  int           `xml:"skipped,attr"`
	Time     string        `xml:"time,attr"`
	Suites   []*junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name       string          `xml:"name,attr"`
	Tests      int             `xml:"tests,attr"`
	Failures   int             `xml:"failures,attr"`
	Errors     int             `xml:"errors,attr"`
	Skipped    int             `xml:"skipped,attr"`
	Time       string          `xml:"time,attr"`
	Timestamp  string          `xml:"timestamp,attr"`
	Properties junitProperties `xml:"properties"`
	Cases      []junitCase     `xml:"testcase"`
	ms         int64
}

type junitProperties struct {
	List []junitProperty `xml:"property"`
}

type junitProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type junitCase struct {
	Name       string           `xml:"name,attr"`
	Classname  string           `xml:"classname,attr"`
	Time       string           `xml:"time,attr"`
	Properties *junitProperties `xml:"properties,omitempty"`
	Failure    *junitProblem    `xml:"failure,omitempty"`
	Error      *junitProblem    `xml:"error,omitempty"`
	Skipped    *junitSkipped    `xml:"skipped,omitempty"`
	SystemOut  string           `xml:"system-out"`
}

type junitProblem struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Text    string `xml:",chardata"`
}

type junitSkipped struct {
	Message string `xml:"message,attr"`
}

// JUnit writes the run as JUnit XML in the common dialect: one testsuite per ticket, in the
// order each ticket first appears. D5 gives the mapping of outcomes.
func JUnit(w io.Writer, r Run) error {
	top := junitSuites{Name: "semar " + xmlText(string(r.Run.ID))}
	if !r.Run.StartedAt.IsZero() && !r.Run.EndedAt.IsZero() {
		top.Time = seconds(r.Run.EndedAt.Sub(r.Run.StartedAt).Milliseconds())
	} else {
		top.Time = seconds(0)
	}
	timestamp := ""
	if !r.Run.StartedAt.IsZero() {
		timestamp = r.Run.StartedAt.UTC().Format("2006-01-02T15:04:05")
	}
	byName := map[string]*junitSuite{}
	for _, sc := range r.Scenarios {
		name := xmlText(sc.Ticket)
		if name == "" {
			name = suiteForNoTicket
		}
		suite := byName[name]
		if suite == nil {
			suite = &junitSuite{Name: name, Timestamp: timestamp, Properties: runProperties(r.Run)}
			byName[name] = suite
			top.Suites = append(top.Suites, suite)
		}
		c := junitCase{
			Name:      xmlText(testName(sc)),
			Classname: name,
			Time:      seconds(sc.DurationMS),
			SystemOut: "semar outcome: " + xmlText(cutMessage(string(sc.Result.Outcome))),
		}
		if sc.Unknown {
			c.Properties = &junitProperties{List: []junitProperty{{Name: "semar.durationUnknown", Value: "true"}}}
		}
		message, detail := reasonTexts(sc.Result.Reasons)
		message, detail = xmlText(message), xmlText(detail)
		outcome := xmlText(cutMessage(string(sc.Result.Outcome)))
		switch sc.Result.Outcome {
		case domain.OutcomePassed:
		case domain.OutcomeFailed:
			c.Failure = &junitProblem{Message: message, Type: outcome, Text: detail}
			suite.Failures++
			top.Failures++
		case domain.OutcomeNotRun:
			c.Skipped = &junitSkipped{Message: message}
			suite.Skipped++
			top.Skipped++
		default:
			c.Error = &junitProblem{Message: message, Type: outcome, Text: detail}
			suite.Errors++
			top.Errors++
		}
		suite.Cases = append(suite.Cases, c)
		suite.Tests++
		top.Tests++
		suite.ms += sc.DurationMS
	}
	for _, suite := range top.Suites {
		suite.Time = seconds(suite.ms)
	}
	body, err := xml.MarshalIndent(top, "", "  ")
	if err != nil {
		return fmt.Errorf("encode junit report of run %s: %w", r.Run.ID, err)
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	if _, err := w.Write(append(body, '\n')); err != nil {
		return err
	}
	return nil
}

// runProperties lists the run, the environment, and the builds, builds in service order.
func runProperties(run domain.Run) junitProperties {
	props := []junitProperty{{Name: "semar.run", Value: xmlText(string(run.ID))}}
	if run.Environment != "" {
		props = append(props, junitProperty{Name: "semar.environment", Value: xmlText(cutMessage(run.Environment))})
	}
	builds := run.BuildsEnd
	if len(builds) == 0 {
		builds = run.BuildsStart
	}
	sorted := append([]domain.BuildRef(nil), builds...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Service < sorted[j].Service })
	for _, b := range sorted {
		props = append(props, junitProperty{Name: "semar.build." + xmlText(cutMessage(b.Service)), Value: xmlText(cutMessage(b.Commit))})
	}
	return junitProperties{List: props}
}

// seconds writes milliseconds as seconds with three decimals.
func seconds(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	return fmt.Sprintf("%d.%03d", ms/1000, ms%1000)
}
