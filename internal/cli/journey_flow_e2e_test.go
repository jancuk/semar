package cli_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/domain"
)

// orderRefFlow signs in and captures the order reference that the app shows, through a pattern.
const orderRefFlow = `apiVersion: semar/v1
kind: Flow
id: login
surface: android
service: shop-app
app: shop-app
steps:
  - step: start
    mobile: { do: launch }
  - step: submit
    mobile: { do: tap, screen: SignIn, element: submit }
    captures:
      - { name: orderRef, element: welcome, pattern: 'Order #([A-Z0-9]+)' }
    expect:
      - check: { element: welcome, contains: "Order #" }
        source: person
`

// orderFromAPhone is a journey whose android leg uses a flow. The flow captures the order reference,
// and a later API leg sends it.
const orderFromAPhone = `apiVersion: semar/v1
kind: Journey
id: order-from-phone
title: A reference from the app is used by the API
owner: team-shop
criticality: standard
authProfile: support-agent
legs:
  - id: phone
    surface: android
    service: shop-app
    app: shop-app
    steps:
      - id: sign-in
        action: Sign in
        use: login
      - id: seen
        action: See the order
        mobile: { do: wait, screen: SignIn, element: welcome }
        expect: [{ check: { element: welcome, contains: "Order #" }, source: person }]
    outputs: [orderRef]
  - id: echo
    surface: api
    service: orders
    needs: [phone]
    steps:
      - id: create
        action: Create an order with the reference
        hint: { method: POST, path: /v1/orders }
        request: { method: POST, path: /v1/orders, json: { amount: 1000, ref: "{{phone.orderRef}}" } }
        expect:
          - { check: { status: 201 }, source: person }
          - { check: { jsonpath: "$.data.ref", equals: "A1B2" }, source: person }
`

func TestJourneyAnAndroidLegUsesAFlowAndALaterAPILegReadsItsCapture(t *testing.T) {
	r := newAndroidRig(t, androidOptions{scenarios: map[string]string{}, apk: "an apk", greeting: "Order #A1B2 placed"})
	r.writeRel(t, ".semar/flows/login.yaml", orderRefFlow)
	r.writeRel(t, ".semar/journeys/order-from-phone.yaml", orderFromAPhone)

	res := r.must(t, cli.ExitOK, "journey", "run", "order-from-phone", "--env", "local")
	contains(t, "the summary", squash(res.stdout), "order-from-phone passed", "phone passed", "echo passed")
	run := r.lastRun(t)
	if len(run.Journeys) != 1 || run.Journeys[0].Outcome != domain.JourneyPassed {
		t.Fatalf("journeys = %+v", run.Journeys)
	}
	if n := r.app.Requests("POST", "/v1/orders"); n != 1 {
		t.Errorf("%d orders were created, want 1", n)
	}
	if r.taps.Load() != 1 {
		t.Errorf("the sign-in button was tapped %d times, want 1", r.taps.Load())
	}
	contains(t, "report.md", r.lastReport(t), "flow login, step submit")
}

func TestJourneyAUseStepNeedsAFlowThatIsThere(t *testing.T) {
	r := newAndroidRig(t, androidOptions{scenarios: map[string]string{}, apk: "an apk"})
	r.writeRel(t, ".semar/journeys/order-from-phone.yaml", orderFromAPhone)
	res := r.must(t, cli.ExitUndecided, "journey", "list")
	contains(t, "the list", res.stdout, "invalid: .semar/journeys/order-from-phone.yaml:", "legs[0].steps[0].use: flow login")
	if strings.Contains(res.stdout, "passed") {
		t.Errorf("list:\n%s", res.stdout)
	}
}
