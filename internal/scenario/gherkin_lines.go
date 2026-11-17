package scenario

import (
	"fmt"

	"github.com/jancuk/semar/internal/domain"
)

// Line is one line of a scenario as the Gherkin file has it: its keyword and its sentence. A Then or
// an And that states an expected result carries the expectation. A line of a step, and a line of one
// of its expected results, name the step, and an expected result its position in Expect.
type Line struct {
	Keyword, Text string
	Expect        *domain.Expectation
	Step          domain.StepID
	Index         int
}

// Lines are the Given, When, and Then lines of s in the words of Render, with no comments and no
// tables. A line that Render would refuse is still given, in the words it can.
func Lines(s domain.Scenario) []Line {
	r := &renderer{}
	var out []Line
	for i, g := range s.Given {
		key := "Given"
		if i > 0 {
			key = "And"
		}
		out = append(out, Line{Keyword: key, Text: r.givenLine(fmt.Sprintf("given[%d]", i), g)})
	}
	prev := kindNone
	if len(s.Given) > 0 {
		prev = kindContext
	}
	for _, st := range s.Steps {
		k, word := kindAction, "When"
		if st.Setup {
			k, word = kindContext, "Given"
		}
		if prev == k {
			word = "And"
		}
		out = append(out, Line{Keyword: word, Text: stepText(st), Step: st.ID})
		prev = k
		for i := range st.Expect {
			word := "Then"
			if prev == kindOutcome {
				word = "And"
			}
			prev = kindOutcome
			sentence, _ := r.check("expect", st.Expect[i].Check)
			out = append(out, Line{Keyword: word, Text: sentence, Expect: &st.Expect[i], Step: st.ID, Index: i})
		}
	}
	return out
}
