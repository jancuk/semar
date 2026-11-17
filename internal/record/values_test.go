package record

import (
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func vars() Values {
	return NewValues(map[string]string{
		"b.board": "b_81",
		"b.title": "Roadmap",
		"b.name":  "Roadmap Q4",
		"b.count": "5",
		"run.id":  "r-20261117T100000Z-ab12",
	})
}

func TestValuesReplaceWhatTheFixtureGaveInEveryPlace(t *testing.T) {
	v := vars()
	for _, c := range []struct{ in, want string }{
		{"/boards/b_81", "/boards/{{b.board}}"},
		{"/boards/b_81/cards?board=b_81", "/boards/{{b.board}}/cards?board={{b.board}}"},
		{"Card for b_81", "Card for {{b.board}}"},
		{`[data-id="b_81"] > li`, `[data-id="{{b.board}}"] > li`},
		{"Board Roadmap has 5 cards", "Board {{b.title}} has 5 cards"},
		{"r-20261117T100000Z-ab12", "{{run.id}}"},
		{"nothing here", "nothing here"},
	} {
		if got := v.Replace(c.in); got != c.want {
			t.Errorf("Replace(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestValuesTakeTheLongestValueFirstAndOnlyWholeWords(t *testing.T) {
	v := vars()
	if got := v.Replace("Roadmap Q4 and Roadmap"); got != "{{b.name}} and {{b.title}}" {
		t.Errorf("the longer value = %q", got)
	}
	for _, in := range []string{"xb_81", "b_81y", "b_811", "Roadmaps", "5.00", "15"} {
		if got := v.Replace(in); got != in {
			t.Errorf("Replace(%q) = %q: a value inside a longer word is not the value", in, got)
		}
	}
}

func TestValuesOfTwoCharactersOrLessAreNotReplaced(t *testing.T) {
	v := NewValues(map[string]string{"o.qty": "5", "o.code": "ab", "o.id": "o_1"})
	if got := v.Replace("5 items, ab, o_1"); got != "5 items, ab, {{o.id}}" {
		t.Errorf("= %q", got)
	}
}

func TestValuesApplyToAnEvent(t *testing.T) {
	v := vars()
	ev := Event{
		Kind:   KindFill,
		Page:   Page{URL: "http://h/boards/b_81", Title: "Roadmap"},
		Target: &Element{Locator: domain.Locator{Role: "button", Name: "Open Roadmap", CSS: "#b_81", TestID: "board-b_81", Text: "Roadmap"}, Hint: "Open Roadmap"},
		To:     &Element{Locator: domain.Locator{CSS: "li.b_81"}, Hint: "b_81"},
		Value:  "typed b_81",
	}
	got := v.Apply(ev)
	if got.Value != "typed {{b.board}}" || got.Target.Hint != "Open {{b.title}}" || got.To.Hint != "{{b.board}}" {
		t.Errorf("event = %+v / %+v / %+v", got, got.Target, got.To)
	}
	want := domain.Locator{Role: "button", Name: "Open {{b.title}}", CSS: "#b_81", TestID: "board-b_81", Text: "{{b.title}}"}
	want.CSS, want.TestID = "#{{b.board}}", "board-{{b.board}}"
	if got.Target.Locator != want {
		t.Errorf("locator = %+v, want %+v", got.Target.Locator, want)
	}
	if got.To.Locator.CSS != "li.{{b.board}}" {
		t.Errorf("to = %+v", got.To.Locator)
	}
	// The event that came in is not changed.
	if ev.Target.Locator.CSS != "#b_81" || ev.Value != "typed b_81" {
		t.Errorf("the input changed: %+v", ev)
	}
}

func TestValuesDoNotTouchAKeyOrASecret(t *testing.T) {
	v := NewValues(map[string]string{"o.key": "Enter"})
	if got := v.Apply(Event{Kind: KindPress, Value: "Enter"}); got.Value != "Enter" {
		t.Errorf("a key = %q", got.Value)
	}
}

func TestValuesListsWhatToDrop(t *testing.T) {
	if got := vars().Raw(); len(got) != 4 || got[0] != "r-20261117T100000Z-ab12" {
		t.Errorf("raw values, longest first, without the short one: %v", got)
	}
}
