package datevar_test

import (
	"testing"
	"time"

	"github.com/jancuk/semar/internal/datevar"
)

func TestText(t *testing.T) {
	jakarta := time.FixedZone("UTC+7", 7*3600)
	at := time.Date(2026, 11, 18, 7, 15, 2, 0, time.UTC)
	for name, want := range map[string]string{
		"date.today":        "2026-11-18",
		"date.plus7d":       "2026-11-25",
		"date.minus90d":     "2026-08-20",
		"datetime.now":      "2026-11-18T07:15:02Z",
		"datetime.plus2h":   "2026-11-18T09:15:02Z",
		"datetime.minus30m": "2026-11-18T06:45:02Z",
		"datetime.plus1d":   "2026-11-19T07:15:02Z",
		"local.now":         "2026-11-18T14:15",
		"local.minus1d":     "2026-11-17T14:15",
		"local.plus3h":      "2026-11-18T17:15",
	} {
		got, ok := datevar.Text(name, at, jakarta)
		if !ok || got != want {
			t.Errorf("%s: got %q %v, want %q", name, got, ok, want)
		}
	}
}

func TestTextUsesTheDayOfTheZone(t *testing.T) {
	late := time.Date(2026, 11, 18, 20, 0, 0, 0, time.UTC)
	jakarta := time.FixedZone("UTC+7", 7*3600)
	if got, _ := datevar.Text("date.today", late, jakarta); got != "2026-11-19" {
		t.Errorf("date.today in UTC+7 at 20.00 UTC: got %q", got)
	}
	if got, _ := datevar.Text("date.today", late, time.UTC); got != "2026-11-18" {
		t.Errorf("date.today in UTC: got %q", got)
	}
}

func TestTextAcrossMonthEnd(t *testing.T) {
	at := time.Date(2028, 2, 28, 12, 0, 0, 0, time.UTC)
	if got, _ := datevar.Text("date.plus1d", at, time.UTC); got != "2028-02-29" {
		t.Errorf("leap day: got %q", got)
	}
	if got, _ := datevar.Text("date.plus2d", at, time.UTC); got != "2028-03-01" {
		t.Errorf("month end: got %q", got)
	}
}

func TestNotADateVariable(t *testing.T) {
	for _, name := range []string{
		"", "date", "date.", "date.now", "date.today2", "date.plus0d", "date.plus01d", "date.plus36501d",
		"date.plus7h", "date.plus7", "date.plus-7d", "date.minus0d", "date.plusd", "date.Plus7d", "Date.today",
		"datetime.today", "datetime.plus1x", "datetime.plus1", "local.today", "local.plus0m", "date.today.x",
		"o.date.today", "run.id", "date.plus 7d", "date.plus7d ", "date.plus７d",
	} {
		if _, ok := datevar.Text(name, time.Now(), time.UTC); ok {
			t.Errorf("%q must not be a date variable", name)
		}
		if datevar.Is(name) {
			t.Errorf("Is(%q) must be false", name)
		}
	}
	for _, name := range []string{"date.plus36500d", "datetime.minus36500m", "local.now", "date.today"} {
		if !datevar.Is(name) {
			t.Errorf("Is(%q) must be true", name)
		}
	}
}

func TestSuggest(t *testing.T) {
	for _, c := range []struct {
		form string
		days int
		want string
	}{
		{"date", 0, "date.today"}, {"date", 7, "date.plus7d"}, {"date", -3, "date.minus3d"},
		{"datetime", 0, "datetime.now"}, {"datetime", 7, "datetime.plus7d"},
		{"local", -1, "local.minus1d"}, {"local", 0, "local.now"},
	} {
		if got := datevar.Suggest(c.form, c.days); got != c.want {
			t.Errorf("Suggest(%s, %d) = %q, want %q", c.form, c.days, got, c.want)
		}
	}
}
