// Package datevar computes the date variables of a script: {{date.today}}, {{date.plus7d}},
// {{datetime.now}}, {{local.minus1d}} and the like. A name is a date variable only when it is
// exactly one of these forms, so a name cannot carry text into a request.
package datevar

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

const maxOffset = 36500

var pattern = regexp.MustCompile(`^(date|datetime|local)\.(?:(today|now)|(plus|minus)([1-9][0-9]*)([dhm]))$`)

type spec struct {
	form   string
	sign   int
	n      int
	unit   byte
	offset bool
}

func parse(name string) (spec, bool) {
	m := pattern.FindStringSubmatch(name)
	if m == nil {
		return spec{}, false
	}
	s := spec{form: m[1]}
	switch {
	case m[2] == "today":
		return s, s.form == "date"
	case m[2] == "now":
		return s, s.form != "date"
	}
	n, err := strconv.Atoi(m[4])
	if err != nil || n > maxOffset {
		return spec{}, false
	}
	s.offset, s.n, s.unit = true, n, m[5][0]
	if s.form == "date" && s.unit != 'd' {
		return spec{}, false
	}
	s.sign = 1
	if m[3] == "minus" {
		s.sign = -1
	}
	return s, true
}

// Is says whether name is a date variable.
func Is(name string) bool {
	_, ok := parse(name)
	return ok
}

// Text gives the value of the date variable name for the instant at. A date and a local time are
// read in zone, and a datetime is RFC 3339 in UTC. A day is a calendar day of the zone, so a local
// time keeps its clock reading across a change of summer time.
func Text(name string, at time.Time, zone *time.Location) (string, bool) {
	s, ok := parse(name)
	if !ok {
		return "", false
	}
	if s.form == "datetime" {
		zone = time.UTC
	}
	t := at.In(zone)
	if s.offset {
		switch s.unit {
		case 'd':
			t = t.AddDate(0, 0, s.sign*s.n)
		case 'h':
			t = t.Add(time.Duration(s.sign*s.n) * time.Hour)
		case 'm':
			t = t.Add(time.Duration(s.sign*s.n) * time.Minute)
		}
	}
	switch s.form {
	case "date":
		return t.Format("2006-01-02"), true
	case "local":
		return t.Format("2006-01-02T15:04"), true
	}
	return t.Format("2006-01-02T15:04:05Z"), true
}

// Suggest is the name of the form (date, datetime, or local) that gives the day that is days
// from today.
func Suggest(form string, days int) string {
	switch {
	case days == 0 && form == "date":
		return "date.today"
	case days == 0:
		return form + ".now"
	case days > 0:
		return fmt.Sprintf("%s.plus%dd", form, days)
	}
	return fmt.Sprintf("%s.minus%dd", form, -days)
}
