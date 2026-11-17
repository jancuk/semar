package scenario_test

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/scenario"
)

// gen builds scenarios that follow the rules of the format, from a seed, with text that tests the writer:
// characters that Gherkin and JSON treat in their own way, and lines that start like Gherkin.
type gen struct{ r *rand.Rand }

var textPool = []string{
	"a", "b", "order", "refund", "Card", "x1", "é", "日本語", "🙂", "#", "@", "|", `"`, `\`, "{{a.b}}", "{x}", "[y]", ":", ";", ",", ".", "-", "_", "/", "'", "(", ")", "<", ">", "&", "*", "\u2028", "\t",
	"Given", "When", "Then", "And", "But", "Scenario:", "Feature:", "Examples:", "equals", "contains", "exists", "as bob,", "step:", ", by model",
}

func (g gen) pick(n int) int { return g.r.IntN(n) }

// line is text of one line that starts and ends with a letter, so that nothing trims it.
func (g gen) line() string {
	var b strings.Builder
	b.WriteString("a")
	for range g.pick(6) {
		b.WriteString(textPool[g.pick(len(textPool))])
		if g.pick(3) == 0 {
			b.WriteString(" ")
		}
	}
	b.WriteString("z")
	return b.String()
}

func (g gen) word() string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789-_."
	n := 1 + g.pick(8)
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[g.pick(len(letters))]
	}
	return "w" + string(b)
}

func (g gen) intent() string {
	switch g.pick(6) {
	case 0:
		return ""
	case 1:
		return g.line()
	case 2:
		return g.line() + "\n"
	}
	var lines []string
	for range 1 + g.pick(4) {
		switch g.pick(4) {
		case 0:
			lines = append(lines, "")
		case 1:
			lines = append(lines, "  "+g.line())
		case 2:
			lines = append(lines, textPool[20+g.pick(len(textPool)-20)]+" "+g.line())
		default:
			lines = append(lines, g.line())
		}
	}
	lines[0], lines[len(lines)-1] = g.line(), g.line()
	if strings.HasPrefix(lines[0], " ") {
		lines[0] = "q"
	}
	out := strings.Join(lines, "\n")
	if g.pick(3) == 0 {
		out += "\n"
	}
	return out
}

func (g gen) value(depth int) any {
	switch g.pick(10) {
	case 0:
		return g.pick(1000) - 500
	case 1:
		return float64(g.pick(2000)-1000) / 8
	case 2:
		return g.pick(2) == 0
	case 3:
		if depth < 3 {
			list := []any{}
			for range g.pick(4) {
				list = append(list, g.value(depth+1))
			}
			return list
		}
	case 4:
		if depth < 3 {
			m := map[string]any{}
			for range g.pick(4) {
				m[g.line()] = g.value(depth + 1)
			}
			return m
		}
	case 5:
		return strings.Join([]string{g.line(), g.line()}, "\n")
	case 6:
		return uint64(1<<63) + uint64(g.pick(100))
	case 7:
		return ""
	}
	return g.line()
}

func (g gen) check() domain.Check {
	switch g.pick(5) {
	case 0:
		n := 100 + g.pick(500)
		return domain.Check{Status: &n}
	case 1:
		return g.operator(domain.Check{Header: "X-" + g.word()}, false)
	case 2:
		return g.operator(domain.Check{Element: "e" + g.word()}, true)
	}
	return g.operator(domain.Check{JSONPath: "$." + g.word() + []string{"", "[0]", `[?(@.k == "equals")]`, ".a b"}[g.pick(4)]}, false)
}

func (g gen) operator(c domain.Check, element bool) domain.Check {
	n := g.pick(5)
	switch g.pick(8) {
	case 0:
		c.Equals = g.value(0)
	case 1:
		c.NotEquals = g.value(0)
	case 2:
		c.Contains = g.value(0)
	case 3:
		v := g.pick(2) == 0
		c.Exists = &v
	case 4:
		if element {
			c.Count = &n
			break
		}
		f := float64(g.pick(1000)-500) / 4
		c.Greater = &f
	case 5:
		if element {
			c.Count = &n
			break
		}
		f := float64(g.pick(1000)-500) / 4
		c.Less = &f
	case 6:
		c.Count = &n
	default:
		if element {
			c.Equals = g.line()
			break
		}
		c.EqualsPath = "$." + g.word()
	}
	return c
}

func (g gen) scenario(i int) domain.Scenario {
	s := domain.Scenario{
		APIVersion: "semar/v1", Kind: "Scenario", ID: domain.ScenarioID(fmt.Sprintf("T-%d", i)), Title: g.line(), Ticket: "T-" + g.word(),
		Intent:      g.intent(),
		Surface:     []domain.Surface{domain.SurfaceAPI, domain.SurfaceWeb, domain.SurfaceAndroid}[g.pick(3)],
		AuthProfile: []string{"default", "", "support-agent", g.word()}[g.pick(4)],
	}
	for k := range g.pick(4) {
		gv := domain.Given{Fixture: domain.FixtureID("f." + g.word()), As: fmt.Sprintf("a%d", k)}
		if g.pick(2) == 0 {
			gv.With = map[string]string{}
			for range 1 + g.pick(3) {
				gv.With[g.word()] = []string{g.line(), "", "x | y", `c:\d\`, "a\nb"}[g.pick(5)]
			}
		}
		if g.pick(3) == 0 {
			gv.Lease = domain.LeaseExclusive
		}
		s.Given = append(s.Given, gv)
	}
	for k := range 1 + g.pick(6) {
		st := domain.Step{ID: domain.StepID(fmt.Sprintf("s%d-%s", k, g.word())), Action: g.line()}
		if g.pick(3) == 0 {
			st.As = g.word()
		}
		if g.pick(3) > 0 {
			st.Hint = &domain.RequestHint{Method: []string{"GET", "POST", "PATCH"}[g.pick(3)], Path: "/v1/" + g.word() + "/{id}"}
		}
		if st.Setup = g.pick(3) == 0; !st.Setup {
			for range g.pick(4) {
				ex := domain.Expectation{Check: g.check(), Source: []domain.Source{domain.SourceTicket, domain.SourceDiff, domain.SourcePerson, domain.SourceBaseline}[g.pick(4)]}
				if g.pick(2) == 0 {
					ex.Cite = g.line()
				}
				if g.pick(3) == 0 {
					ex.By = "model"
				}
				st.Expect = append(st.Expect, ex)
			}
		}
		s.Steps = append(s.Steps, st)
	}
	return s
}

// A scenario that follows the rules of the format is written as Gherkin and read back as the same
// value with the same expectation hash.
func TestRoundTripProperty(t *testing.T) {
	g := gen{rand.New(rand.NewPCG(20261117, 1))}
	for i := range 4000 {
		s := g.scenario(i)
		data, err := scenario.Render(s, "")
		if err != nil {
			t.Fatalf("Render refused scenario %d: %v\n%#v", i, err, s)
		}
		got, err := scenario.Parse(data, "p.feature")
		if err != nil {
			t.Fatalf("Parse refused scenario %d: %v\n%s", i, err, data)
		}
		if domain.ExpectHash(got) != domain.ExpectHash(s) {
			t.Fatalf("scenario %d changed its expectation hash\n%s", i, data)
		}
		if !reflect.DeepEqual(got, s) {
			t.Fatalf("scenario %d is read back different:\n got %#v\nwant %#v\n%s", i, got, s, data)
		}
		again, err := scenario.Render(got, "")
		if err != nil || string(again) != string(data) {
			t.Fatalf("scenario %d is not stable: %v\n%s\n%s", i, err, data, again)
		}
	}
}
