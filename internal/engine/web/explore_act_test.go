package web_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/chromedp/chromedp/kb"

	"github.com/jancuk/semar/internal/engine/web"
	"github.com/jancuk/semar/internal/pagelist"
	"github.com/jancuk/semar/internal/record"
)

// explore opens a page on a recorder and lists it.
func (r *recRig) explore(path string) (*web.Explorer, pagelist.List) {
	r.t.Helper()
	r.open(path)
	r.events(1)
	x := web.NewExplorer(r.rec)
	l, err := x.List(r.t.Context())
	if err != nil {
		r.t.Fatal(err)
	}
	return x, l
}

func refOf(t *testing.T, l pagelist.List, role, name string) string {
	t.Helper()
	for _, e := range l.Elements {
		if e.Role == role && e.Name == name {
			return e.Ref
		}
	}
	t.Fatalf("the list has no %s %q:\n%s", role, name, l.String())
	return ""
}

// seen is what the recorder heard, without the address, which differs between two servers.
func seen(evs []record.Event) []record.Event {
	out := append([]record.Event(nil), evs...)
	for i := range out {
		out[i].Page.URL = ""
	}
	return out
}

// An action on a reference is heard as the event of the same action by a person.
func TestAnActionOnAReferenceIsHeardAsAPersonMakesIt(t *testing.T) {
	person := newRecRig(t, map[string]string{"/refund": refundPage})
	person.open("/refund")
	person.events(1)
	person.typeInto("#amount", "25")
	person.key(kb.Enter)
	person.click("#refund")
	want := person.events(3)

	model := newRecRig(t, map[string]string{"/refund": refundPage})
	x, l := model.explore("/refund")
	ctx := t.Context()
	model.must(x.Fill(ctx, refOf(t, l, "textbox", "Amount"), "25"))
	model.must(x.Press(ctx, refOf(t, l, "textbox", "Amount"), "Enter"))
	model.must(x.Click(ctx, refOf(t, l, "button", "Refund")))
	got := model.events(3)

	if !reflect.DeepEqual(seen(got), seen(want)) {
		t.Errorf("the model was heard as\n%s\nand a person as\n%s", describeEvents(got), describeEvents(want))
	}
}

func TestADragOnReferencesIsHeardAsAPersonMakesIt(t *testing.T) {
	person := newRecRig(t, map[string]string{"/board": dragPage})
	person.open("/board")
	person.events(1)
	person.drag(".card", "#doing")
	want := person.events(1)

	model := newRecRig(t, map[string]string{"/board": dragPage})
	x, l := model.explore("/board")
	model.must(x.Drag(t.Context(), refOf(t, l, "listitem", "Buy milk"), refOf(t, l, "list", "Doing")))
	got := model.events(1)
	if !reflect.DeepEqual(seen(got), seen(want)) {
		t.Errorf("the model was heard as\n%s\nand a person as\n%s", describeEvents(got), describeEvents(want))
	}
	model.quiet()
}

func TestACheckIsAnOptionClickThatThePageDoesNotSee(t *testing.T) {
	person := newRecRig(t, map[string]string{"/refund": refundPage})
	person.open("/refund")
	person.events(1)
	person.altClick("#status")
	want := person.events(1)

	model := newRecRig(t, map[string]string{"/refund": refundPage})
	x, l := model.explore("/refund")
	shown, err := x.Check(t.Context(), refOf(t, l, "text", "idle"))
	if err != nil {
		t.Fatal(err)
	}
	got := model.events(1)
	if shown != "idle" || !reflect.DeepEqual(seen(got), seen(want)) {
		t.Errorf("shown %q; the model was heard as\n%s\nand a person as\n%s", shown, describeEvents(got), describeEvents(want))
	}
	model.quiet()
}

func TestAnActionScrollsToAnElementOutOfTheView(t *testing.T) {
	page := `<!doctype html><title>Long</title><div style="height:4000px"></div><button id="far" onclick="document.title='clicked'">Far away</button>`
	r := newRecRig(t, map[string]string{"/long": page})
	x, l := r.explore("/long")
	if err := x.Click(t.Context(), refOf(t, l, "button", "Far away")); err != nil {
		t.Fatal(err)
	}
	if ev := r.events(1)[0]; ev.Kind != record.KindClick {
		t.Errorf("the click: %s", describeEvents([]record.Event{ev}))
	}
}

func TestAnActionThatCannotBeDoneSaysWhy(t *testing.T) {
	page := `<!doctype html><title>Odd</title>
<button disabled>Off</button>
<input type="password" aria-label="Password">
<div><input type="text" aria-label="Empty"><button>Beside</button></div>
<div style="position:relative"><button id="under">Under</button><div style="position:absolute;inset:0;background:#fff;opacity:.99"></div></div>
<button id="gone" onclick="this.remove()">Gone</button>
<p>text</p>`
	r := newRecRig(t, map[string]string{"/odd": page})
	x, l := r.explore("/odd")
	ctx := t.Context()
	for name, tc := range map[string]struct {
		do   func() error
		want string
	}{
		"a disabled button":         {func() error { return x.Click(ctx, refOf(t, l, "button", "Off")) }, "is disabled"},
		"a reference of no list":    {func() error { return x.Click(ctx, "e99") }, "e99 is not in the last list"},
		"a field of a text":         {func() error { return x.Fill(ctx, refOf(t, l, "text", "text"), "x") }, "is not a field that takes text"},
		"a covered button":          {func() error { return x.Click(ctx, refOf(t, l, "button", "Under")) }, "covered"},
		"a check of a password":     {func() error { _, err := x.Check(ctx, refOf(t, l, "textbox", "Password")); return err }, "password"},
		"a key that is not known":   {func() error { return x.Press(ctx, "", "F13") }, "Enter or Escape"},
		"a check of an empty field": {func() error { _, err := x.Check(ctx, refOf(t, l, "textbox", "Empty")); return err }, "shows no text"},
		"a check of a hidden text":  {func() error { _, err := x.Check(ctx, refOf(t, l, "button", "Under")); return err }, "covered"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := tc.do(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v, want one with %q", err, tc.want)
			}
		})
	}

	if err := x.Click(ctx, refOf(t, l, "button", "Gone")); err != nil {
		t.Fatal(err)
	}
	err := x.Click(ctx, refOf(t, l, "button", "Gone"))
	if err == nil || !strings.Contains(err.Error(), "no longer on the page") {
		t.Errorf("a click on an element that left the page: %v", err)
	}
}

func TestAFillTypesTheIDOfTheRunForItsVariable(t *testing.T) {
	r := newRecRig(t, map[string]string{"/refund": refundPage})
	x, l := r.explore("/refund")
	x.RunID = "r-test-0001"
	r.must(x.Fill(t.Context(), refOf(t, l, "textbox", "Amount"), "Board {{run.id}}"))
	if err := x.Click(t.Context(), refOf(t, l, "button", "Refund")); err != nil {
		t.Fatal(err)
	}
	got := r.events(2)
	if got[0].Kind != record.KindFill || got[0].Value != "Board r-test-0001" {
		t.Errorf("the page was typed %s", describeEvents(got[:1]))
	}
}

func TestAFillOfAPasswordFieldIsNeverReadBack(t *testing.T) {
	page := `<!doctype html><title>Pw</title><input type="password" aria-label="Password"><button>Go</button>`
	r := newRecRig(t, map[string]string{"/pw": page})
	x, l := r.explore("/pw")
	r.must(x.Fill(t.Context(), refOf(t, l, "textbox", "Password"), "hunter2-pass"))
	if err := x.Click(t.Context(), refOf(t, l, "button", "Go")); err != nil {
		t.Fatal(err)
	}
	for _, ev := range r.events(2) {
		if strings.Contains(describeEvents([]record.Event{ev}), "hunter2") {
			t.Errorf("an event holds the password: %s", describeEvents([]record.Event{ev}))
		}
	}
	if l2, err := x.List(t.Context()); err != nil || strings.Contains(l2.String(), "hunter2") {
		t.Errorf("the list holds the password: %v\n%s", err, l2.String())
	}
}

func TestADateFieldIsListedForWhatItIsAndRefusesTypedText(t *testing.T) {
	page := `<!doctype html><title>Due</title><label for="d">Due date</label><input id="d" type="datetime-local"><label for="m">Month</label><input id="m" type="month"><input type="text" aria-label="Plain">`
	r := newRecRig(t, map[string]string{"/due": page})
	x, l := r.explore("/due")
	for _, kind := range []string{"datetime-local", "month"} {
		ref := ""
		for _, e := range l.Elements {
			if e.Role == kind {
				ref = e.Ref
			}
		}
		if ref == "" {
			t.Fatalf("the list has no %s field:\n%s", kind, l.String())
		}
		err := x.Fill(t.Context(), ref, "2026-12-01")
		if err == nil || !strings.Contains(err.Error(), "a "+kind+" field, which a flow cannot type into") {
			t.Errorf("fill of a %s field: %v", kind, err)
		}
	}
	r.must(x.Fill(t.Context(), refOf(t, l, "textbox", "Plain"), "ok"))
}
