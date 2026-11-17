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

// problem says why text may not be typed, or "" when it may. A text is a date when it starts with a
// real day and the day ends the text or is followed by T or a space. ticket is the text of the file
// of the ticket, which may name the day on purpose.
func (r dateRule) problem(text, ticket string) string {
	if !r.on {
		return ""
	}
	m := dateAtStart.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return r.check(text, m[1], ticket)
}

func (r dateRule) check(shown, day, ticket string) string {
	at, err := time.Parse("2006-01-02", day)
	if err != nil {
		return ""
	}
	diff := int(at.Sub(r.today).Hours() / 24)
	if diff <= -nearDays || diff >= nearDays || namesWord(ticket, day) {
		return ""
	}
	form := "date"
	if rest := strings.TrimPrefix(shown, day); rest != "" {
		form = "local"
		if t, ok := strings.CutPrefix(rest, "T"); ok && zoneAtEnd.MatchString(t) {
			form = "datetime"
		}
	}
	return fmt.Sprintf("holds the date %s, which is near today and will be wrong on a later day: write {{%s}}, or a date at least 10 years away when the value must not move",
		strings.TrimSpace(shown), datevar.Suggest(form, diff))
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
