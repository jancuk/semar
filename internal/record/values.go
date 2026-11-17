package record

import (
	"maps"
	"slices"
	"strings"
	"unicode"

	"github.com/jancuk/semar/internal/domain"
)

// minValue is the shortest value that is replaced by its variable. A value of one or two characters,
// such as an amount of 5, is as likely to be typed text as the ID that a fixture gave.
const minValue = 3

type pair struct{ value, name string }

// Values are what a fixture gave, by variable name, such as o.id or run.id. They are raw values and
// are never written to a file: Replace is what turns them into variables.
type Values struct{ pairs []pair }

// NewValues takes the variables of a resolved fixture, and the run ID as run.id.
func NewValues(vars map[string]string) Values {
	var v Values
	for _, name := range slices.Sorted(maps.Keys(vars)) {
		if len(vars[name]) >= minValue {
			v.pairs = append(v.pairs, pair{vars[name], name})
		}
	}
	// The longest value first, so that Roadmap Q4 is not read as Roadmap.
	slices.SortStableFunc(v.pairs, func(a, b pair) int { return len(b.value) - len(a.value) })
	return v
}

// Raw lists the values, longest first.
func (v Values) Raw() []string {
	out := make([]string, len(v.pairs))
	for i, p := range v.pairs {
		out[i] = p.value
	}
	return out
}

// Replace puts {{name}} in place of every value that stands as a whole word: the character before it
// and the one after it are not a letter or a digit.
func (v Values) Replace(s string) string {
	if len(v.pairs) == 0 || s == "" {
		return s
	}
	// A value is cut out in one pass, so that a variable that was put in is never searched again.
	var out strings.Builder
	for i := 0; i < len(s); {
		hit := false
		for _, p := range v.pairs {
			if !strings.HasPrefix(s[i:], p.value) || wordAt(s, i-1) || wordAt(s, i+len(p.value)) {
				continue
			}
			out.WriteString("{{" + p.name + "}}")
			i += len(p.value)
			hit = true
			break
		}
		if !hit {
			out.WriteByte(s[i])
			i++
		}
	}
	return out.String()
}

// wordAt says whether the byte at i belongs to a word: an ASCII letter or digit, or a byte of a
// longer character. An index outside the text is not.
func wordAt(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return false
	}
	c := rune(s[i])
	return c >= 0x80 || unicode.IsLetter(c) || unicode.IsDigit(c)
}

// Apply gives the event with every value replaced in the text that a person typed, the text of a
// check, and the elements: their locators and the names that they are called by. The key of a press
// is not text, and a secret has no text.
func (v Values) Apply(ev Event) Event {
	if len(v.pairs) == 0 {
		return ev
	}
	if ev.Kind == KindFill || ev.Kind == KindCheck {
		ev.Value = v.Replace(ev.Value)
	}
	ev.Target = v.element(ev.Target)
	ev.To = v.element(ev.To)
	return ev
}

func (v Values) element(e *Element) *Element {
	if e == nil {
		return nil
	}
	l := e.Locator
	return &Element{
		Hint: v.Replace(e.Hint),
		Locator: domain.Locator{
			TestID: v.Replace(l.TestID), Role: l.Role, Name: v.Replace(l.Name),
			Text: v.Replace(l.Text), CSS: v.Replace(l.CSS), Within: l.Within,
		},
	}
}
