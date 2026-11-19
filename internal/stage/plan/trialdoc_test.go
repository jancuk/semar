package plan

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func TestTrialLineSaysWhenTheResponseDoesNotFitTheDocument(t *testing.T) {
	value := func(s string) string { return s }
	ts := TrialStep{
		Scenario: "PROJ-12-S01", Step: "s1", Result: TrialSent, Method: "GET", Route: "/orders/{id}", Status: 200,
		Doc: &domain.DocCheck{Misfits: []domain.Misfit{{Path: "$.id", Want: "integer", Got: "string"}}, MoreMisfits: 2},
	}
	line := trialLine(ts, domain.Scenario{}, nil, value)
	want := "s1: GET /orders/{id} answered 200, the response does not fit the API document: id is a string, and the document says integer (and 2 more)"
	if line != want {
		t.Errorf("line = %q", line)
	}
	ts.Doc = nil
	if line = trialLine(ts, domain.Scenario{}, nil, value); strings.Contains(line, "document") {
		t.Errorf("a step with no finding says %q", line)
	}
}
