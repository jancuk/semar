package scenario_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/scenario"
)

func TestLinesAreTheKeywordAndTheSentenceOfTheGherkinFile(t *testing.T) {
	sc := fullScenario()
	data, err := scenario.Render(sc, "")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimSpace(l)
		for _, k := range []string{"Given ", "And ", "When ", "Then "} {
			if strings.HasPrefix(l, k) {
				want = append(want, l)
			}
		}
	}
	var got []string
	for _, l := range scenario.Lines(sc) {
		got = append(got, l.Keyword+" "+l.Text)
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("lines:\n%s\nwant the lines of the file:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestAnExpectationLineCarriesItsExpectation(t *testing.T) {
	sc := gOne(domain.Check{Status: ptr(200)})
	sc.Steps[0].Expect[0].By = "model"
	ls := scenario.Lines(sc)
	if len(ls) != 2 || ls[0].Expect != nil || ls[1].Expect == nil || ls[1].Expect.By != "model" || ls[1].Text != "the status is 200" {
		t.Errorf("lines %+v", ls)
	}
}
