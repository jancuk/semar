package cli_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/domain"
)

// orderOnAPhone is a journey of two legs: an order is placed through the API, and an android leg types
// its ID in the app and signs in.
const orderOnAPhone = `apiVersion: semar/v1
kind: Journey
id: order-on-phone
title: An order ID is used in the app
owner: team-shop
criticality: standard
authProfile: support-agent
legs:
  - id: place
    surface: api
    service: orders
    steps:
      - id: create
        action: Create an order of 1000
        hint: { method: POST, path: /v1/orders }
        request: { method: POST, path: /v1/orders, json: { amount: 1000 } }
        captures: [{ name: id, jsonpath: "$.data.id" }]
        expect: [{ check: { status: 201 }, source: person }]
    outputs: [id]
  - id: phone
    surface: android
    service: shop-app
    app: shop-app
    needs: [place]
    steps:
      - id: start
        action: Start the app
        mobile: { do: launch }
      - id: email
        action: Type the order ID
        mobile: { do: type, screen: SignIn, element: email, value: "{{place.id}}" }
      - id: sign-in
        action: Tap sign in
        mobile: { do: tap, screen: SignIn, element: submit }
        expect: [{ check: { element: welcome, equals: "Hi Ana" }, source: person }]
        captures: [{ name: greeting, element: welcome }]
    outputs: [greeting]
invariants:
  - equal: ["{{phone.greeting}}", "{{phone.greeting}}"]
`

func TestJourneyAnAPILegFeedsAnAndroidLeg(t *testing.T) {
	r := newAndroidRig(t, androidOptions{scenarios: map[string]string{}, apk: "an apk"})
	r.writeRel(t, ".semar/journeys/order-on-phone.yaml", orderOnAPhone)

	res := r.must(t, cli.ExitOK, "journey", "run", "order-on-phone", "--env", "local")
	contains(t, "the summary", squash(res.stdout), "order-on-phone passed", "place passed", "phone passed")

	run := r.lastRun(t)
	if len(run.Journeys) != 1 || run.Journeys[0].Outcome != domain.JourneyPassed {
		t.Fatalf("journeys = %+v", run.Journeys)
	}
	// The ID that the API gave is what the app was told to type.
	typed := r.server.Typed()
	if len(typed) != 1 || typed[0] == "" || strings.Contains(typed[0], "{{") {
		t.Fatalf("typed = %q, want the ID of the order that the API leg made", typed)
	}
	if n := r.app.Requests("POST", "/v1/orders"); n != 1 {
		t.Errorf("%d orders were created, want 1", n)
	}
	md := r.lastReport(t)
	contains(t, "report.md", md, "## Journeys", "- Leg phone: passed", "## Device steps", "Tap sign in (tap SignIn)")
	if r.server.Deleted() != 1 {
		t.Errorf("the session was deleted %d times, want 1", r.server.Deleted())
	}
}

func TestJourneyAnAndroidLegInAReadOnlyEnvironmentIsRefusedAndSendsNothing(t *testing.T) {
	r := newAndroidRig(t, androidOptions{scenarios: map[string]string{}, readOnly: true, apk: "an apk"})
	r.writeRel(t, ".semar/journeys/order-on-phone.yaml", orderOnAPhone)
	r.must(t, cli.ExitUndecided, "journey", "run", "order-on-phone", "--env", "local")
	if reqs := r.server.Requests(); len(reqs) != 0 {
		t.Errorf("the device server got %d requests", len(reqs))
	}
}

func TestJourneyAnAndroidLegNeedsItsScreens(t *testing.T) {
	r := newAndroidRig(t, androidOptions{scenarios: map[string]string{}, apk: "an apk"})
	r.edit(t, ".semar/screens/shop-app.yaml", func(s string) string { return strings.Replace(s, "email:", "mail:", 1) })
	r.writeRel(t, ".semar/journeys/order-on-phone.yaml", orderOnAPhone)
	res := r.must(t, cli.ExitUndecided, "journey", "list")
	contains(t, "the list", res.stdout, "invalid: .semar/journeys/order-on-phone.yaml:", "legs[1].steps[1].mobile: element email is not on screen SignIn of shop-app")
}
