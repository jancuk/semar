package record_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/record"
	"github.com/jancuk/semar/internal/scenario"
	"github.com/jancuk/semar/internal/script"
)

const base = "http://shop.test"

func el(role, name string) *record.Element {
	return &record.Element{Locator: domain.Locator{Role: role, Name: name}, Hint: name}
}

func css(sel, hint string) *record.Element {
	return &record.Element{Locator: domain.Locator{CSS: sel}, Hint: hint}
}

func onPage(path, title string) record.Page { return record.Page{URL: base + path, Title: title} }

func options() record.Options {
	return record.Options{
		ID: "SHOP-482-07", Ticket: "SHOP-482", App: "shop", Service: "orders", BaseURL: base,
		AuthProfile: "support-agent", Date: time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC),
	}
}

// refundFlow is the recording of the plan's contract: open an order, refund 5, and check two things.
func refundFlow() []record.Event {
	page := onPage("/orders/o_4471", "Order o_4471")
	return []record.Event{
		{Kind: record.KindOpen, Page: record.Page{URL: base + "/orders/o_4471"}},
		{Kind: record.KindClick, Page: page, Target: el("button", "Refund")},
		{Kind: record.KindFill, Page: page, Target: el("textbox", "Amount"), Value: "5"},
		{Kind: record.KindClick, Page: page, Target: el("button", "Confirm refund")},
		{Kind: record.KindCheck, Page: page, Target: css("#refund-total", "refund-total"), Value: "5.00"},
		{Kind: record.KindCheck, Page: page, Target: css("#status", "status"), Value: "partly refunded"},
	}
}

func build(t *testing.T, events []record.Event, o record.Options) record.Draft {
	t.Helper()
	d, err := record.Build(events, o)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func feature(t *testing.T, d record.Draft) string {
	t.Helper()
	data, err := scenario.Render(d.Scenario, "Refunds")
	if err != nil {
		t.Fatalf("the scenario cannot be written: %v", err)
	}
	return string(data)
}

func TestBuildTheScenarioOfThePlansContract(t *testing.T) {
	o := options()
	o.Given = []domain.Given{{Fixture: "order.paid", As: "o"}}
	o.Values = record.NewValues(map[string]string{"o.id": "o_4471"})
	d := build(t, refundFlow(), o)

	want := `Feature: Refunds

  @id:SHOP-482-07 @ticket:SHOP-482 @surface:web @auth:support-agent
  Scenario: Recorded on 2026-11-17: Order
    Given the fixture order.paid as o
    And Open /orders/{{o.id}}
    # step: open
    When Click refund
    # step: click-refund
    And Type "5" in amount
    # step: fill-amount
    And Click confirm-refund
    # step: click-confirm-refund
    Then the element refund-total equals "5.00"
    # person
    And the element status equals "partly refunded"
    # person
`
	if got := feature(t, d); got != want {
		t.Errorf("the scenario:\n%s\nwant\n%s", got, want)
	}
	if d.Actions != 4 || d.Checks != 2 {
		t.Errorf("%d actions and %d checks", d.Actions, d.Checks)
	}

	steps := d.Script.Steps
	if len(steps) != 4 {
		t.Fatalf("script steps = %+v", steps)
	}
	for i, want := range []domain.WebAction{
		{Service: "orders", App: "shop", Do: domain.WebOpen, Path: "/orders/{{o.id}}"},
		{Service: "orders", App: "shop", Do: domain.WebClick, Screen: "Order", Element: "refund"},
		{Service: "orders", App: "shop", Do: domain.WebFill, Screen: "Order", Element: "amount", Value: "5"},
		{Service: "orders", App: "shop", Do: domain.WebClick, Screen: "Order", Element: "confirm-refund"},
	} {
		if *steps[i].Web != want {
			t.Errorf("script step %d = %+v, want %+v", i, *steps[i].Web, want)
		}
	}
	if d.Script.Scenario != "SHOP-482-07" || d.Script.ExpectHash != domain.ExpectHash(d.Scenario) || d.Script.AuthoredBy.Instance != "person" {
		t.Errorf("script = %+v", d.Script)
	}
	if ps := script.Validate(d.Scenario, d.Script); len(ps) > 0 {
		t.Errorf("the script does not fit its scenario: %+v", ps)
	}

	if len(d.Added) != 1 || d.Added[0].Screen != "Order" {
		t.Fatalf("screens = %+v", d.Added)
	}
	var names []string
	for _, e := range d.Added[0].Elements {
		names = append(names, e.Name)
	}
	if !slices.Equal(names, []string{"refund", "amount", "confirm-refund", "refund-total", "status"}) {
		t.Errorf("elements = %v", names)
	}
	if d.Added[0].Elements[1].Locator != (domain.Locator{Role: "textbox", Name: "Amount"}) {
		t.Errorf("amount = %+v", d.Added[0].Elements[1])
	}
}

func TestBuildWithNoCheckWritesNothing(t *testing.T) {
	events := refundFlow()[:4]
	_, err := record.Build(events, options())
	if !errors.Is(err, record.ErrNoCheck) {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(err.Error(), "at least one check") || !strings.Contains(err.Error(), "Option") {
		t.Errorf("the message must say that a test needs a check and how to record one: %v", err)
	}
}

func TestBuildAddsAWaitWhenTheCheckIsOnAnotherPageThanTheLastStep(t *testing.T) {
	receipt := onPage("/receipt", "Receipt")
	events := []record.Event{
		{Kind: record.KindOpen, Page: record.Page{URL: base + "/orders"}},
		{Kind: record.KindClick, Page: onPage("/orders", "Orders"), Target: el("button", "Pay")},
		{Kind: record.KindCheck, Page: receipt, Target: css("#paid", "paid"), Value: "yes"},
		{Kind: record.KindCheck, Page: receipt, Target: css("#sum", "sum"), Value: "5"},
	}
	d := build(t, events, options())
	text := feature(t, d)
	for _, want := range []string{
		"Given Open /orders\n    # step: open\n",
		"When Click pay\n    # step: click-pay\n",
		"And Wait for paid\n    # step: wait-paid\n",
		"Then the element paid equals \"yes\"\n    # person\n    And the element sum equals \"5\"\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the scenario lacks %q:\n%s", want, text)
		}
	}
	wait := d.Script.Steps[2].Web
	if wait.Do != domain.WebWait || wait.Screen != "Receipt" || wait.Element != "paid" {
		t.Errorf("wait = %+v", wait)
	}
	if ps := script.Validate(d.Scenario, d.Script); len(ps) > 0 {
		t.Errorf("the script does not fit its scenario: %+v", ps)
	}
	if d.Actions != 2 || d.Checks != 2 {
		t.Errorf("a wait is no action of the person: %d actions and %d checks", d.Actions, d.Checks)
	}
}

func TestBuildAddsAWaitWhenTheOnlyStepBeforeTheCheckIsAnOpen(t *testing.T) {
	events := []record.Event{
		{Kind: record.KindOpen, Page: record.Page{URL: base + "/orders"}},
		{Kind: record.KindCheck, Page: onPage("/orders", "Orders"), Target: css("#n", "count"), Value: "3"},
	}
	d := build(t, events, options())
	if !strings.Contains(feature(t, d), "When Wait for count\n") {
		t.Errorf("a leading open is a Given and holds no check, so a wait is the When:\n%s", feature(t, d))
	}
}

func TestBuildLaterOpensAreStepsOfTheFlow(t *testing.T) {
	events := []record.Event{
		{Kind: record.KindOpen, Page: record.Page{URL: base + "/a"}},
		{Kind: record.KindClick, Page: onPage("/a", "A"), Target: el("button", "Go")},
		{Kind: record.KindOpen, Page: record.Page{URL: base + "/b?x=1#frag"}},
		{Kind: record.KindCheck, Page: onPage("/b", "B"), Target: css("#t", "t"), Value: "x"},
	}
	text := feature(t, build(t, events, options()))
	if !strings.Contains(text, "And Open /b?x=1#frag\n    # step: open-2\n") {
		t.Errorf("a later open:\n%s", text)
	}
}

func TestBuildKeysAndDrags(t *testing.T) {
	page := onPage("/board", "Board")
	events := []record.Event{
		{Kind: record.KindOpen, Page: record.Page{URL: base + "/board"}},
		{Kind: record.KindDrag, Page: page, Target: &record.Element{Locator: domain.Locator{Text: "Buy milk"}, Hint: "Buy milk"}, To: el("list", "Doing")},
		{Kind: record.KindFill, Page: page, Target: el("textbox", "Note"), Value: "done"},
		{Kind: record.KindPress, Page: page, Target: el("textbox", "Note"), Value: "Enter"},
		{Kind: record.KindPress, Page: page, Value: "Escape"},
		{Kind: record.KindCheck, Page: page, Target: css("#status", "status"), Value: "escaped"},
	}
	d := build(t, events, options())
	text := feature(t, d)
	for _, want := range []string{
		"When Drag buy-milk to doing\n    # step: drag-buy-milk-doing\n",
		"And Press Enter in note\n    # step: press-enter-note\n",
		"And Press Escape\n    # step: press-escape\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the scenario lacks %q:\n%s", want, text)
		}
	}
	drag := d.Script.Steps[1].Web
	if drag.Do != domain.WebDrag || drag.Element != "buy-milk" || drag.To != "doing" || drag.Screen != "Board" {
		t.Errorf("drag = %+v", drag)
	}
	esc := d.Script.Steps[4].Web
	if esc.Do != domain.WebPress || esc.Value != "Escape" || esc.Element != "" || esc.Screen != "" {
		t.Errorf("a key with no element needs no screen: %+v", esc)
	}
	// The check follows a step with no screen, so a wait stands between them.
	if w := d.Script.Steps[5].Web; w.Do != domain.WebWait || w.Screen != "Board" {
		t.Errorf("wait = %+v", w)
	}
	if ps := script.Validate(d.Scenario, d.Script); len(ps) > 0 {
		t.Errorf("the script does not fit its scenario: %+v", ps)
	}
}

func TestBuildKeepsOnlyTheLastOfTwoFillsInARow(t *testing.T) {
	page := onPage("/f", "F")
	events := []record.Event{
		{Kind: record.KindFill, Page: page, Target: el("textbox", "Amount"), Value: "5"},
		{Kind: record.KindFill, Page: page, Target: el("textbox", "Amount"), Value: "50"},
		{Kind: record.KindFill, Page: page, Target: el("textbox", "Amount"), Value: ""},
		{Kind: record.KindFill, Page: page, Target: el("textbox", "Amount"), Value: "500"},
		{Kind: record.KindCheck, Page: page, Target: css("#s", "s"), Value: "x"},
	}
	d := build(t, events, options())
	if n := len(d.Script.Steps); n != 1 || d.Script.Steps[0].Web.Value != "500" {
		t.Errorf("steps = %+v", d.Script.Steps)
	}
	if d.Actions != 1 {
		t.Errorf("actions = %d", d.Actions)
	}
}

func TestBuildLeavesOutAMoveToAnotherHostAndSaysSo(t *testing.T) {
	events := []record.Event{
		{Kind: record.KindOpen, Page: record.Page{URL: "http://elsewhere.test/x"}},
		{Kind: record.KindOpen, Page: record.Page{URL: base + "/orders"}},
		{Kind: record.KindCheck, Page: onPage("/orders", "Orders"), Target: css("#n", "n"), Value: "3"},
	}
	d := build(t, events, options())
	if len(d.Notes) != 1 || !strings.Contains(d.Notes[0], "elsewhere.test") {
		t.Errorf("notes = %v", d.Notes)
	}
	if d.Script.Steps[0].Web.Path != "/orders" {
		t.Errorf("steps = %+v", d.Script.Steps[0].Web)
	}
}

func TestBuildKeepsTheScreensFileAndAddsToIt(t *testing.T) {
	o := options()
	o.Values = record.NewValues(map[string]string{"o.id": "o_4471"})
	o.Existing = domain.Screens{App: "shop", Screens: map[string]map[string]domain.Locator{
		"Order": {
			"pay":    {Role: "button", Name: "Refund"},
			"status": {CSS: "#other-status"},
		},
	}}
	d := build(t, refundFlow(), o)
	text := feature(t, d)
	// The element that the file has under another name keeps its name, and a name that is taken by
	// another locator gets a number.
	for _, want := range []string{"When Click pay\n", "And the element status-2 equals \"partly refunded\""} {
		if !strings.Contains(text, want) {
			t.Errorf("the scenario lacks %q:\n%s", want, text)
		}
	}
	var added []string
	for _, e := range d.Added[0].Elements {
		added = append(added, e.Name)
	}
	if !slices.Equal(added, []string{"amount", "confirm-refund", "refund-total", "status-2"}) {
		t.Errorf("added = %v: only what the file does not have", added)
	}
}

func TestBuildNamesASecretByTheEnvironmentVariableAndNeverByItsValue(t *testing.T) {
	page := onPage("/login", "Sign in")
	events := []record.Event{
		{Kind: record.KindOpen, Page: record.Page{URL: base + "/login"}},
		{Kind: record.KindFill, Page: page, Target: el("textbox", "Email"), Value: "ana@example.com"},
		{Kind: record.KindFill, Page: page, Target: el("textbox", "Password"), Secret: true},
		{Kind: record.KindClick, Page: page, Target: el("button", "Sign in")},
		{Kind: record.KindCheck, Page: onPage("/home", "Home"), Target: css("#hello", "hello"), Value: "Hello Ana"},
	}
	if _, err := record.Build(events, options()); !errors.Is(err, record.ErrSecretName) {
		t.Fatalf("a recording with a password and no name for it: %v", err)
	}
	o := options()
	o.SecretEnv = "SHOP_PASSWORD"
	d := build(t, events, o)
	pw := d.Script.Steps[2].Web
	if pw.Do != domain.WebFill || pw.Secret != "env:SHOP_PASSWORD" || pw.Value != "" {
		t.Errorf("password step = %+v", pw)
	}
	if text := feature(t, d); !strings.Contains(text, "And Type the secret env:SHOP_PASSWORD in password\n") {
		t.Errorf("the scenario:\n%s", text)
	}
	if ps := script.Validate(d.Scenario, d.Script); len(ps) > 0 {
		t.Errorf("the script does not fit its scenario: %+v", ps)
	}
	o.SecretEnv = "not a name"
	if _, err := record.Build(events, o); err == nil {
		t.Error("a name that is not an environment variable was accepted")
	}
}

func TestBuildTitlesTheScenarioWithTheDateAndTheFirstPage(t *testing.T) {
	d := build(t, refundFlow(), options())
	if d.Scenario.Title != "Recorded on 2026-11-17: Order o_4471" {
		t.Errorf("title = %q", d.Scenario.Title)
	}
	if d.Scenario.AuthProfile != "support-agent" || d.Scenario.Surface != domain.SurfaceWeb || d.Scenario.Ticket != "SHOP-482" {
		t.Errorf("scenario = %+v", d.Scenario)
	}
}
