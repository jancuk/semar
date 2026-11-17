package plan

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/datevar"
)

// nearDays is how far from the day of the plan a typed date still changes its meaning as days pass.
const nearDays = 3650

var (
	dateAtStart = regexp.MustCompile(`^([0-9]{4}-[0-9]{2}-[0-9]{2})(?:$|T| )`)
	dateInText  = regexp.MustCompile(`(?:^|[^0-9A-Za-z])([0-9]{4}-[0-9]{2}-[0-9]{2})(?:$|[^0-9])`)
	zoneAtEnd   = regexp.MustCompile(`(?:Z|[+-][0-9]{2}:?[0-9]{2})$`)
)

// dateRule refuses a typed date that is near the day of the plan (D7 of Plan 31). The zero rule
// refuses nothing.
type dateRule struct {
	today time.Time // midnight UTC of the day of the plan in the zone of the machine
	on    bool
}

func newDateRule(now time.Time) dateRule {
	if now.IsZero() {
		return dateRule{}
	}
	y, m, d := now.In(time.Local).Date()
	return dateRule{today: time.Date(y, m, d, 0, 0, 0, 0, time.UTC), on: true}
}

// near gives the distance in days of a real day from the day of the plan, and says whether that is
// near and the ticket does not name it (D8).
func (r dateRule) near(day, ticket string) (int, bool) {
	at, err := time.Parse("2006-01-02", day)
	if err != nil {
		return 0, false
	}
	diff := int(at.Sub(r.today).Hours() / 24)
	return diff, diff > -nearDays && diff < nearDays && !namesWord(ticket, day)
}

func refusal(shown string, form string, days int) string {
	return fmt.Sprintf("holds the date %s, which is near today and will be wrong on a later day: write {{%s}}, or a date at least 10 years away when the value must not move",
		shown, datevar.Suggest(form, days))
}

// problem says why text may not be typed, or "" when it may. A text is a date when it starts with a
// real day and the day ends the text or is followed by T or a space. ticket is the text of the file
// of the ticket, which may name the day on purpose.
func (r dateRule) problem(text, ticket string) string {
	m := dateAtStart.FindStringSubmatch(text)
	if !r.on || m == nil {
		return ""
	}
	days, near := r.near(m[1], ticket)
	if !near {
		return ""
	}
	form := "date"
	if rest := text[len(m[1]):]; rest != "" {
		form = "local"
		if t, ok := strings.CutPrefix(rest, "T"); ok && zoneAtEnd.MatchString(t) {
			form = "datetime"
		}
	}
	return refusal(strings.TrimSpace(text), form, days)
}

// problemInWords is problem for a text of a page, which shows words around a date, such as
// "Due 2026-12-01": a date at the start of any word counts. A browser field holds no zone, so a
// time is a local one.
func (r dateRule) problemInWords(text, ticket string) string {
	m := dateInText.FindStringSubmatchIndex(text)
	if !r.on || m == nil {
		return ""
	}
	day := text[m[2]:m[3]]
	days, near := r.near(day, ticket)
	if !near {
		return ""
	}
	form := "date"
	if strings.HasPrefix(text[m[3]:], "T") {
		form = "local"
	}
	return refusal(day, form, days)
}

// dates refuses a typed date that is near today in the query values, the JSON body, the expected
// values, and the with values of a draft scenario (D7), unless its ticket file names the day (D8).
func (v *validator) dates(sp string, sd ScenarioDraft) {
	rule := newDateRule(v.in.Now)
	if !rule.on {
		return
	}
	ticket := v.ticketFiles[sd.Ticket].Text
	refuse := func(path, where, text string) {
		if msg := rule.problem(text, ticket); msg != "" {
			v.add(path, "%s %s", where, msg)
		}
	}
	for i, g := range sd.Given {
		for _, k := range slices.Sorted(maps.Keys(g.With)) {
			refuse(fmt.Sprintf("%s.given[%d]", sp, i), "with "+k, g.With[k])
		}
	}
	for j, st := range sd.Steps {
		p := fmt.Sprintf("%s.steps[%d]", sp, j)
		for _, k := range slices.Sorted(maps.Keys(st.Query)) {
			refuse(p, "query "+k, st.Query[k])
		}
		walkStrings("json", st.JSON, func(where, s string) { refuse(p, where, s) })
		for k, e := range st.Expect {
			for _, o := range []struct {
				op string
				v  any
			}{{"equals", e.Check.Equals}, {"notEquals", e.Check.NotEquals}, {"contains", e.Check.Contains}} {
				if s, ok := o.v.(string); ok {
					refuse(fmt.Sprintf("%s.expect[%d].check", p, k), o.op, s)
				}
			}
		}
	}
}

// walkStrings visits every string of a JSON value, with its place: json.items[0].due.
func walkStrings(where string, v any, visit func(where, s string)) {
	switch x := v.(type) {
	case string:
		visit(where, x)
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(x)) {
			walkStrings(where+"."+k, x[k], visit)
		}
	case []any:
		for i, e := range x {
			walkStrings(fmt.Sprintf("%s[%d]", where, i), e, visit)
		}
	}
}
