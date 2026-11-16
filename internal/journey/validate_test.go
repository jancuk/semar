package journey

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
)

// payJSON sets the body of the one step of leg pay, which holds the variable orderId.
func payJSON(orderID string) func(j *domain.Journey) {
	return func(j *domain.Journey) {
		j.Legs[1].Steps[0].Request.JSON = map[string]any{"orderId": orderID}
	}
}

func manyLegs(n int) func(j *domain.Journey) {
	return func(j *domain.Journey) {
		j.Invariants = nil
		for len(j.Legs) < n {
			i := len(j.Legs)
			j.Legs = append(j.Legs, domain.Leg{
				ID: domain.LegID(fmt.Sprintf("extra%d", i)), Surface: domain.SurfaceAPI, Service: "orders",
				Steps: []domain.LegStep{{
					ID: "read", Action: "Read", Hint: &domain.RequestHint{Method: "GET", Path: "/v1/orders"},
					Request: domain.APIRequest{Method: "GET", Path: "/v1/orders"},
				}},
			})
		}
	}
}

func manySteps(n int) func(j *domain.Journey) {
	return func(j *domain.Journey) {
		first := j.Legs[0].Steps[0]
		j.Legs[0].Steps = []domain.LegStep{first}
		for i := 1; i < n; i++ {
			s := first
			s.ID = domain.StepID(fmt.Sprintf("s%d", i))
			s.Captures = nil
			j.Legs[0].Steps = append(j.Legs[0].Steps, s)
		}
	}
}

// TestValidateRules has one case for each of the rules J2 to J16, and more for the rules that hold
// several. Each case names where the problem is and what its text says, and no problem is reported
// twice. A case may also cause problems that follow from the edit, such as a leg that is renamed
// while another leg still reads its outputs. J1 and J9 are cases of TestParseRefusals.
func TestValidateRules(t *testing.T) {
	cases := []struct {
		rule, name string
		edit       func(j *domain.Journey)
		file       string // the file name, for rule J2
		path, want string
	}{
		{rule: "J2", name: "api version", edit: func(j *domain.Journey) { j.APIVersion = "semar/v2" }, path: "apiVersion", want: "apiVersion must be semar/v1"},
		{rule: "J2", name: "kind", edit: func(j *domain.Journey) { j.Kind = "Scenario" }, path: "kind", want: "kind must be Journey"},
		{rule: "J2", name: "id with a space", edit: func(j *domain.Journey) { j.ID = "check out" }, path: "id", want: `invalid journey ID "check out"`},
		{rule: "J2", name: "empty id", edit: func(j *domain.Journey) { j.ID = "" }, path: "id", want: "invalid journey ID"},
		{rule: "J2", name: "id with a slash", edit: func(j *domain.Journey) { j.ID = "a/b" }, path: "id", want: "invalid journey ID"},
		{rule: "J2", name: "id with a dot", edit: func(j *domain.Journey) { j.ID = "check.out" }, path: "id", want: "id check.out must not hold a dot"},
		{rule: "J2", name: "file name", file: "check-out.yaml", path: "id", want: "id checkout does not match the file name check-out.yaml"},
		{rule: "J2", name: "file name with yml", file: "checkout.yml"},
		{rule: "J2", name: "file name without an extension", file: "checkout", path: "id", want: "does not match the file name checkout"},
		{rule: "J2", name: "file in a directory", file: ".semar/journeys/checkout.yaml"},

		{rule: "J3", name: "no title", edit: func(j *domain.Journey) { j.Title = " " }, path: "title", want: "title is required"},
		{rule: "J3", name: "title on two lines", edit: func(j *domain.Journey) { j.Title = "one\ntwo" }, path: "title", want: "title must be one line"},
		{rule: "J3", name: "title with a control character", edit: func(j *domain.Journey) { j.Title = "a\x07b" }, path: "title", want: "title must be one line"},
		{rule: "J3", name: "title too long", edit: func(j *domain.Journey) { j.Title = strings.Repeat("é", 201) }, path: "title", want: "title has 201 characters; at most 200"},
		{rule: "J3", name: "title of 200 characters", edit: func(j *domain.Journey) { j.Title = strings.Repeat("é", 200) }},
		{rule: "J3", name: "title that is not UTF-8", edit: func(j *domain.Journey) { j.Title = "a\xffb" }, path: "title", want: "not valid UTF-8"},
		{rule: "J3", name: "no owner", edit: func(j *domain.Journey) { j.Owner = "" }, path: "owner", want: "owner is required: a named product or QA owner owns a journey"},
		{rule: "J3", name: "owner with a space", edit: func(j *domain.Journey) { j.Owner = "team commerce" }, path: "owner", want: `invalid owner "team commerce"`},
		{rule: "J3", name: "owner that climbs", edit: func(j *domain.Journey) { j.Owner = "../x" }, path: "owner", want: "invalid owner"},

		{rule: "J4", name: "criticality", edit: func(j *domain.Journey) { j.Criticality = "high" }, path: "criticality", want: "criticality must be critical or standard"},
		{rule: "J4", name: "no criticality", edit: func(j *domain.Journey) { j.Criticality = "" }, path: "criticality", want: "criticality must be critical or standard"},
		{rule: "J4", name: "standard", edit: func(j *domain.Journey) { j.Criticality = "standard" }},
		{rule: "J4", name: "auth profile", edit: func(j *domain.Journey) { j.AuthProfile = "a b" }, path: "authProfile", want: "invalid auth profile name"},

		{rule: "J5", name: "alias is a leg", edit: func(j *domain.Journey) {
			j.Given = []domain.Given{{Fixture: "order.paid", As: "place"}}
		}, path: "given[0].as", want: "place is also a leg ID"},
		{rule: "J5", name: "alias twice", edit: func(j *domain.Journey) {
			j.Given = []domain.Given{{Fixture: "order.paid", As: "order"}, {Fixture: "order.paid", As: "order"}}
		}, path: "given[1].as", want: "order is used twice"},
		{rule: "J5", name: "reserved alias", edit: func(j *domain.Journey) {
			j.Given = []domain.Given{{Fixture: "order.paid", As: "run"}}
		}, path: "given[0].as", want: "the name run is reserved"},
		{rule: "J5", name: "lease", edit: func(j *domain.Journey) {
			j.Given = []domain.Given{{Fixture: "order.paid", As: "order", Lease: "shared"}}
		}, path: "given[0].lease", want: "lease must be exclusive or empty"},
		{rule: "J5", name: "fixture ID", edit: func(j *domain.Journey) {
			j.Given = []domain.Given{{Fixture: "Order Paid", As: "order"}}
		}, path: "given[0].fixture", want: "must match"},
		{rule: "J5", name: "too many", edit: func(j *domain.Journey) {
			for i := range 9 {
				j.Given = append(j.Given, domain.Given{Fixture: "order.paid", As: fmt.Sprintf("o%d", i)})
			}
		}, path: "given", want: "at most 8 given entries are allowed, there are 9"},
		{rule: "J5", name: "with value too long", edit: func(j *domain.Journey) {
			j.Given = []domain.Given{{Fixture: "order.paid", As: "order", With: map[string]string{"customer": strings.Repeat("x", 257)}}}
		}, path: "given[0].with.customer", want: "longer than 256 bytes"},
		{rule: "J5", name: "a given entry", edit: func(j *domain.Journey) {
			j.Given = []domain.Given{{Fixture: "order.paid", As: "order", With: map[string]string{"customer": "c-1"}, Lease: "exclusive"}}
		}},

		{rule: "J6", name: "id used twice", edit: func(j *domain.Journey) { j.Legs[2].ID = "pay"; j.Invariants = nil }, path: "legs[2].id", want: "pay is used twice"},
		{rule: "J6", name: "upper case id", edit: func(j *domain.Journey) { j.Legs[0].ID = "Place"; j.Legs[1].Needs = nil; j.Invariants = nil }, path: "legs[0].id", want: `id "Place" must match ^[a-z][a-z0-9_-]*$`},
		{rule: "J6", name: "id with a dot", edit: func(j *domain.Journey) { j.Legs[0].ID = "a.b"; j.Legs[1].Needs = nil; j.Invariants = nil }, path: "legs[0].id", want: "must match"},
		{rule: "J6", name: "id with a hyphen", edit: func(j *domain.Journey) { j.Legs[2].ID = "wait-paid"; j.Invariants = nil }},
		{rule: "J6", name: "reserved run", edit: func(j *domain.Journey) { j.Legs[2].ID = "run"; j.Invariants = nil }, path: "legs[2].id", want: "the name run is reserved"},
		{rule: "J6", name: "reserved secret", edit: func(j *domain.Journey) { j.Legs[2].ID = "secret"; j.Invariants = nil }, path: "legs[2].id", want: "the name secret is reserved"},
		{rule: "J6", name: "no legs", edit: func(j *domain.Journey) { j.Legs, j.Invariants = nil, nil }, path: "legs", want: "a journey needs 1 to 12 legs"},
		{rule: "J6", name: "twelve legs", edit: manyLegs(12)},
		{rule: "J6", name: "thirteen legs", edit: manyLegs(13), path: "legs", want: "13 legs; at most 12"},

		{rule: "J7", name: "web", edit: func(j *domain.Journey) { j.Legs[0].Surface = "web" }, path: "legs[0].surface", want: "web legs are not supported yet: this version runs api legs only; web legs come in M2 and android legs in M3"},
		{rule: "J7", name: "android", edit: func(j *domain.Journey) { j.Legs[0].Surface = "android" }, path: "legs[0].surface", want: "android legs are not supported yet: this version runs api legs only; web legs come in M2 and android legs in M3"},
		{rule: "J7", name: "unknown surface", edit: func(j *domain.Journey) { j.Legs[0].Surface = "ios" }, path: "legs[0].surface", want: `surface "ios" is not known: this version runs api legs only`},
		{rule: "J7", name: "no surface", edit: func(j *domain.Journey) { j.Legs[0].Surface = "" }, path: "legs[0].surface", want: "surface is required"},

		{rule: "J8", name: "no service", edit: func(j *domain.Journey) { j.Legs[0].Service = "" }, path: "legs[0].service", want: "service is required"},
		{rule: "J8", name: "service with a slash", edit: func(j *domain.Journey) {
			j.Legs[0].Service = "a/b"
			j.Legs[0].Steps[0].Request.Service = ""
		}, path: "legs[0].service", want: "invalid service name"},
		{rule: "J8", name: "request in another service", edit: func(j *domain.Journey) { j.Legs[1].Steps[0].Request.Service = "billing" }, path: "legs[1].steps[0].request.service", want: "billing differs from the leg's service payments"},
		{rule: "J8", name: "request in the same service", edit: func(j *domain.Journey) { j.Legs[1].Steps[0].Request.Service = "payments" }},

		{rule: "J10", name: "needs an unknown leg", edit: func(j *domain.Journey) { j.Legs[1].Needs = []domain.LegID{"nope"} }, path: "legs[1].needs[0]", want: "nope is not a leg of this journey"},
		{rule: "J10", name: "needs itself", edit: func(j *domain.Journey) { j.Legs[1].Needs = []domain.LegID{"pay"} }, path: "legs[1].needs[0]", want: "leg pay cannot need itself"},
		{rule: "J10", name: "needs twice", edit: func(j *domain.Journey) { j.Legs[1].Needs = []domain.LegID{"place", "place"} }, path: "legs[1].needs[1]", want: "place is listed twice"},
		{rule: "J10", name: "needs a later leg", edit: func(j *domain.Journey) {
			j.Legs[0].Needs = []domain.LegID{"settled"}
			j.Legs[1].Needs = nil
			j.Legs[2].Needs = []domain.LegID{"place"}
		}, path: "legs[0].needs", want: "needs has a cycle: place, settled, place"},

		{rule: "J11", name: "no steps", edit: func(j *domain.Journey) {
			j.Legs[0].Steps, j.Legs[0].Outputs, j.Invariants = nil, nil, nil
			j.Legs[1].Steps[0].Request.JSON = nil
		}, path: "legs[0].steps", want: "a leg needs 1 to 20 steps"},
		{rule: "J11", name: "twenty steps", edit: manySteps(20)},
		{rule: "J11", name: "twenty one steps", edit: manySteps(21), path: "legs[0].steps", want: "21 steps; at most 20"},
		{rule: "J11", name: "no hint", edit: func(j *domain.Journey) { j.Legs[1].Steps[0].Hint = nil }, path: "legs[1].steps[0].hint", want: "a step needs a hint"},
		{rule: "J11", name: "method differs from the hint", edit: func(j *domain.Journey) { j.Legs[1].Steps[0].Hint.Method = "PUT" }, path: "legs[1].steps[0].request", want: "the request is POST and the step expects PUT"},
		{rule: "J11", name: "path differs from the hint", edit: func(j *domain.Journey) { j.Legs[1].Steps[0].Hint.Path = "/v1/refunds" }, path: "legs[1].steps[0].request", want: "the request path /v1/payments does not match /v1/refunds"},
		{rule: "J11", name: "no method", edit: func(j *domain.Journey) { j.Legs[1].Steps[0].Request.Method = "" }, path: "legs[1].steps[0].request", want: "the request has no method"},
		{rule: "J11", name: "path without a slash", edit: func(j *domain.Journey) { j.Legs[1].Steps[0].Request.Path = "v1/payments" }, path: "legs[1].steps[0].request", want: "must start with /"},
		{rule: "J11", name: "unknown method", edit: func(j *domain.Journey) { j.Legs[1].Steps[0].Request.Method = "TRACE" }, path: "legs[1].steps[0].request", want: "method TRACE is not one of"},
		{rule: "J11", name: "step id twice", edit: func(j *domain.Journey) {
			s := j.Legs[0].Steps[0]
			s.Captures = nil
			j.Legs[0].Steps = append(j.Legs[0].Steps, s)
		}, path: "legs[0].steps[1].id", want: `duplicate step id "create"`},
		{rule: "J11", name: "step id", edit: func(j *domain.Journey) { j.Legs[0].Steps[0].ID = "a b" }, path: "legs[0].steps[0].id", want: "must match"},
		{rule: "J11", name: "setup step with an expected result", edit: func(j *domain.Journey) { j.Legs[0].Steps[0].Setup = true }, path: "legs[0].steps[0].expect", want: "a setup step has no expected result"},
		{rule: "J11", name: "check without an operator", edit: func(j *domain.Journey) {
			j.Legs[2].Steps[0].Expect[0].Check.Equals = nil
		}, path: "legs[2].steps[0].expect[0].check", want: "a jsonpath or header check needs exactly one operator"},
		{rule: "J11", name: "the source invariant", edit: func(j *domain.Journey) { j.Legs[0].Steps[0].Expect[0].Source = domain.SourceInvariant }, path: "legs[0].steps[0].expect[0].source", want: "invariants are fixed in Semar"},
		{rule: "J11", name: "unknown source", edit: func(j *domain.Journey) { j.Legs[0].Steps[0].Expect[0].Source = "guess" }, path: "legs[0].steps[0].expect[0].source", want: "source must be one of"},
		{rule: "J11", name: "ticket without a cite", edit: func(j *domain.Journey) { j.Legs[0].Steps[0].Expect[0].Source = domain.SourceTicket }, path: "legs[0].steps[0].expect[0].cite", want: "cite is required"},
		{rule: "J11", name: "capture jsonpath", edit: func(j *domain.Journey) { j.Legs[0].Steps[0].Captures[0].JSONPath = "$.[" }, path: "legs[0].steps[0].request", want: "jsonpath does not parse"},
		{rule: "J11", name: "capture twice in a leg", edit: func(j *domain.Journey) {
			s := j.Legs[0].Steps[0]
			s.ID = "again"
			j.Legs[0].Steps = append(j.Legs[0].Steps, s)
		}, path: "legs[0].steps[1].request", want: "capture id is defined more than once"},
		{rule: "J11", name: "variable before any capture", edit: payJSON("{{orderId}}"), path: "legs[1].steps[0].request", want: "orderId is used before any step captures it"},
		{rule: "J11", name: "header that the client sets", edit: func(j *domain.Journey) {
			j.Legs[1].Steps[0].Request.Headers = map[string]string{"Host": "example.com"}
		}, path: "legs[1].steps[0].request", want: "header Host is set by the HTTP client"},
		{rule: "J11", name: "secret", edit: payJSON("{{secret.TOKEN}}"), path: "legs[1].steps[0].request", want: "a script cannot use a secret"},
		{rule: "J11", name: "data", edit: payJSON("{{data.customer}}"), path: "legs[1].steps[0].request", want: "put a data value in a fixture parameter"},
		{rule: "J11", name: "malformed variable", edit: payJSON("{{place.id"), path: "legs[1].steps[0].request", want: "malformed variable"},
		{rule: "J11", name: "path uses an earlier capture", edit: func(j *domain.Journey) {
			// the read follows a capture of the same leg and must use a value from it
			s := j.Legs[2].Steps[0]
			s.ID = "again"
			s.Expect = nil
			s.Request.Path = "/v1/orders/fixed"
			j.Legs[2].Steps = append([]domain.LegStep{j.Legs[2].Steps[0]}, s)
			j.Legs[2].Steps[1].Captures = nil
		}, path: "legs[2].steps[1].request", want: "does not use a value from an earlier step"},

		{rule: "J12", name: "an output of a leg that is not needed", edit: payJSON("{{settled.id}}"), path: "legs[1].steps[0].request.json.orderId", want: "{{settled.id}} is an output of leg settled, which leg pay does not need"},
		{rule: "J12", name: "an output that the leg does not have", edit: payJSON("{{place.total}}"), path: "legs[1].steps[0].request.json.orderId", want: "{{place.total}} is not an output of leg place; its outputs are id, amount"},
		{rule: "J12", name: "an output of a leg with none", edit: func(j *domain.Journey) { j.Legs[0].Outputs = nil; j.Invariants = nil }, path: "legs[1].steps[0].request.json.orderId", want: "{{place.id}} is not an output of leg place: it has no outputs"},
		{rule: "J12", name: "an output of the leg itself", edit: payJSON("{{pay.amount}}"), path: "legs[1].steps[0].request.json.orderId", want: "is an output of leg pay itself: use the name of the capture"},
		{rule: "J12", name: "an unknown head", edit: payJSON("{{nobody.id}}"), path: "legs[1].steps[0].request.json.orderId", want: "{{nobody.id}} is not a capture, run.id, an output of a given entry, or an output of a leg that leg pay needs"},
		{rule: "J12", name: "a name with two dots", edit: payJSON("{{place.id.x}}"), path: "legs[1].steps[0].request.json.orderId", want: "{{place.id.x}} is not an output of leg place"},
		{rule: "J12", name: "in the path", edit: func(j *domain.Journey) {
			j.Legs[1].Steps[0].Hint.Path = "/v1/payments/{id}"
			j.Legs[1].Steps[0].Request.Path = "/v1/payments/{{settled.id}}"
		}, path: "legs[1].steps[0].request.path", want: "which leg pay does not need"},
		{rule: "J12", name: "in a query value", edit: func(j *domain.Journey) {
			j.Legs[1].Steps[0].Request.Query = map[string]string{"order": "{{settled.id}}"}
		}, path: "legs[1].steps[0].request.query.order", want: "which leg pay does not need"},
		{rule: "J12", name: "in a header value", edit: func(j *domain.Journey) {
			j.Legs[1].Steps[0].Request.Headers = map[string]string{"X-Order": "{{settled.id}}"}
		}, path: "legs[1].steps[0].request.headers.X-Order", want: "which leg pay does not need"},
		{rule: "J12", name: "an output two legs away", edit: payJSON("{{place.id}}")},
		{rule: "J12", name: "a capture of the leg and run.id", edit: payJSON("ref {{run.id}}")},
		{rule: "J12", name: "an output of a given entry", edit: func(j *domain.Journey) {
			j.Given = []domain.Given{{Fixture: "order.paid", As: "order"}}
			payJSON("{{order.id}}")(j)
		}},
		{rule: "J12", name: "an output that the fixture does not have", edit: func(j *domain.Journey) {
			j.Given = []domain.Given{{Fixture: "order.paid", As: "order"}}
			payJSON("{{order.total}}")(j)
		}, path: "legs[1].steps[0].request.json.orderId", want: "{{order.total}} is not an output of fixture order.paid; its outputs are id"},

		{rule: "J13", name: "output not captured", edit: func(j *domain.Journey) { j.Legs[0].Outputs = []string{"id", "total"}; j.Invariants = nil }, path: "legs[0].outputs", want: "total is not captured by any step of leg place"},
		{rule: "J13", name: "output listed twice", edit: func(j *domain.Journey) { j.Legs[0].Outputs = []string{"id", "id", "amount"} }, path: "legs[0].outputs", want: "id is listed twice"},
		{rule: "J13", name: "output name", edit: func(j *domain.Journey) { j.Legs[0].Outputs = []string{"id", "a.b", "amount"} }, path: "legs[0].outputs", want: `output "a.b" must match`},
		{rule: "J13", name: "output of another leg's capture", edit: func(j *domain.Journey) { j.Legs[1].Outputs = []string{"id"}; j.Invariants = nil }, path: "legs[1].outputs", want: "id is not captured by any step of leg pay"},

		{rule: "J14", name: "wait on a write", edit: func(j *domain.Journey) { j.Legs[1].Wait = j.Legs[2].Wait }, path: "legs[1].wait", want: "the last step of leg pay is POST; a wait repeats the last step, which must read"},
		{rule: "J14", name: "wait on a HEAD", edit: func(j *domain.Journey) {
			j.Legs[2].Steps[0].Request.Method, j.Legs[2].Steps[0].Hint.Method = "HEAD", "HEAD"
		}},
		{rule: "J14", name: "no timeout", edit: func(j *domain.Journey) { j.Legs[2].Wait.Timeout = 0 }, path: "legs[2].wait.timeout", want: "wait needs a timeout above 0"},
		{rule: "J14", name: "negative timeout", edit: func(j *domain.Journey) { j.Legs[2].Wait.Timeout = -time.Second }, path: "legs[2].wait.timeout", want: "wait needs a timeout above 0"},
		{rule: "J14", name: "timeout of 15 minutes", edit: func(j *domain.Journey) { j.Legs[2].Wait.Timeout = 15 * time.Minute }},
		{rule: "J14", name: "timeout above 15 minutes", edit: func(j *domain.Journey) { j.Legs[2].Wait.Timeout = 15*time.Minute + time.Nanosecond }, path: "legs[2].wait.timeout", want: "wait timeout 15m0.000000001s is above the limit of 15m0s"},
		{rule: "J14", name: "every under 100ms", edit: func(j *domain.Journey) { j.Legs[2].Wait.Every = 99 * time.Millisecond }, path: "legs[2].wait.every", want: "wait every 99ms is under 100ms"},
		{rule: "J14", name: "every of 100ms", edit: func(j *domain.Journey) { j.Legs[2].Wait.Every = 100 * time.Millisecond }},
		{rule: "J14", name: "every above the timeout", edit: func(j *domain.Journey) { j.Legs[2].Wait.Every = time.Minute }, path: "legs[2].wait.every", want: "wait every 1m0s is above the timeout 30s"},
		{rule: "J14", name: "every omitted", edit: func(j *domain.Journey) { j.Legs[2].Wait.Every = 0 }},
		{rule: "J14", name: "jsonpath", edit: func(j *domain.Journey) { j.Legs[2].Wait.Until.JSONPath = "$.[" }, path: "legs[2].wait.until.jsonpath", want: "until jsonpath does not parse"},
		{rule: "J14", name: "no jsonpath", edit: func(j *domain.Journey) { j.Legs[2].Wait.Until.JSONPath = "" }, path: "legs[2].wait.until.jsonpath", want: "until jsonpath does not parse"},
		{rule: "J14", name: "equals is a list", edit: func(j *domain.Journey) { j.Legs[2].Wait.Until.Equals = []any{"paid"} }, path: "legs[2].wait.until.equals", want: "must be a string, a number, or a boolean"},
		{rule: "J14", name: "equals is a mapping", edit: func(j *domain.Journey) { j.Legs[2].Wait.Until.Equals = map[string]any{"a": 1} }, path: "legs[2].wait.until.equals", want: "must be a string, a number, or a boolean"},
		{rule: "J14", name: "no equals", edit: func(j *domain.Journey) { j.Legs[2].Wait.Until.Equals = nil }, path: "legs[2].wait.until.equals", want: "must be a string, a number, or a boolean"},
		{rule: "J14", name: "equals is a number", edit: func(j *domain.Journey) { j.Legs[2].Wait.Until.Equals = 3 }},
		{rule: "J14", name: "equals is a boolean", edit: func(j *domain.Journey) { j.Legs[2].Wait.Until.Equals = true }},

		{rule: "J15", name: "eight invariants", edit: func(j *domain.Journey) {
			for len(j.Invariants) < 8 {
				j.Invariants = append(j.Invariants, j.Invariants[0])
			}
		}},
		{rule: "J15", name: "nine invariants", edit: func(j *domain.Journey) {
			for len(j.Invariants) < 9 {
				j.Invariants = append(j.Invariants, j.Invariants[0])
			}
		}, path: "invariants", want: "9 invariants; at most 8"},
		{rule: "J15", name: "one entry", edit: func(j *domain.Journey) { j.Invariants[0].Equal = []string{"{{place.amount}}"} }, path: "invariants[0].equal", want: "equal needs 2 to 6 entries, there are 1"},
		{rule: "J15", name: "no entries", edit: func(j *domain.Journey) { j.Invariants[0].Equal = nil }, path: "invariants[0].equal", want: "equal needs 2 to 6 entries, there are 0"},
		{rule: "J15", name: "six entries", edit: func(j *domain.Journey) {
			e := "{{place.amount}}"
			j.Invariants[0].Equal = []string{e, e, e, e, e, e}
		}},
		{rule: "J15", name: "seven entries", edit: func(j *domain.Journey) {
			e := "{{place.amount}}"
			j.Invariants[0].Equal = []string{e, e, e, e, e, e, e}
		}, path: "invariants[0].equal", want: "equal needs 2 to 6 entries, there are 7"},
		{rule: "J15", name: "text around the variable", edit: func(j *domain.Journey) { j.Invariants[0].Equal[1] = "total {{pay.amount}}" }, path: "invariants[0].equal[1]", want: `"total {{pay.amount}}" must be one variable and nothing else`},
		{rule: "J15", name: "a leg that is not there", edit: func(j *domain.Journey) { j.Invariants[0].Equal[1] = "{{nobody.amount}}" }, path: "invariants[0].equal[1]", want: "{{nobody.amount}} is not an output of a leg or of a given entry"},
		{rule: "J15", name: "an output that is not there", edit: func(j *domain.Journey) { j.Invariants[0].Equal[1] = "{{pay.total}}" }, path: "invariants[0].equal[1]", want: "{{pay.total}} is not an output of leg pay; its outputs are amount"},
		{rule: "J15", name: "an output of a given entry", edit: func(j *domain.Journey) {
			j.Given = []domain.Given{{Fixture: "order.paid", As: "order"}}
			j.Invariants[0].Equal[1] = "{{order.id}}"
		}},
		{rule: "J15", name: "an output that the fixture does not have", edit: func(j *domain.Journey) {
			j.Given = []domain.Given{{Fixture: "order.paid", As: "order"}}
			j.Invariants[0].Equal[1] = "{{order.total}}"
		}, path: "invariants[0].equal[1]", want: "{{order.total}} is not an output of fixture order.paid; its outputs are id"},

		// settled is the longest leg: 120 + ".settled" is 128 bytes
		{rule: "J16", name: "leg scenario of 128 bytes", edit: func(j *domain.Journey) { j.ID = domain.JourneyID(strings.Repeat("a", 120)) }},
		{rule: "J16", name: "leg scenario of 129 bytes", edit: func(j *domain.Journey) {
			j.ID = domain.JourneyID(strings.Repeat("a", 121))
		}, path: "legs[2].id", want: "the leg scenario " + strings.Repeat("a", 121) + ".settled is too long: at most 128 bytes"},
	}
	for _, tc := range cases {
		t.Run(tc.rule+" "+tc.name, func(t *testing.T) {
			j := example(t)
			if tc.edit != nil {
				tc.edit(&j)
			}
			ps := validate(j, tc.file, orderPaid)
			if tc.want == "" {
				if len(ps) != 0 {
					t.Fatalf("problems = %v, want none", ps)
				}
				return
			}
			if !hasProblem(ps, tc.path, tc.want) {
				t.Fatalf("no problem at %q holding %q in %v", tc.path, tc.want, ps)
			}
			seen := map[Problem]bool{}
			for _, p := range ps {
				if seen[p] {
					t.Errorf("problem reported twice: %v", p)
				}
				seen[p] = true
			}
		})
	}
}

func TestTheExampleHasNoProblems(t *testing.T) {
	if ps := validate(example(t), exampleName, orderPaid); len(ps) != 0 {
		t.Fatalf("problems = %v", ps)
	}
}

func TestWebSurfaceRefused(t *testing.T) {
	j := example(t)
	j.Legs[1].Surface = domain.SurfaceWeb
	// A web leg has no request, so its steps are not read as api steps and add no problem.
	j.Legs[1].Steps, j.Legs[1].Service = nil, ""
	ps := Validate(j, nil)
	want := "web legs are not supported yet: this version runs api legs only; web legs come in M2 and android legs in M3"
	if len(ps) == 0 || !hasProblem(ps, "legs[1].surface", want) {
		t.Fatalf("problems = %v", ps)
	}
	for _, p := range ps {
		if strings.Contains(p.Path, "steps") || strings.Contains(p.Path, "service") {
			t.Errorf("the refused leg is also read as an api leg: %v", p)
		}
	}
}

func TestAndroidSurfaceRefused(t *testing.T) {
	j := example(t)
	j.Legs[1].Surface = domain.SurfaceAndroid
	j.Legs[1].Steps, j.Legs[1].Service = nil, ""
	want := "android legs are not supported yet: this version runs api legs only; web legs come in M2 and android legs in M3"
	if ps := Validate(j, nil); !hasProblem(ps, "legs[1].surface", want) {
		t.Fatalf("problems = %v", ps)
	}
}

// TestBindingThroughNeeds is rule R3: a leg may read an output two needs away, and not a sibling's.
func TestBindingThroughNeeds(t *testing.T) {
	// settled needs pay, and pay needs place: settled reads {{place.id}} in the example
	j := example(t)
	if got := j.Legs[2].Steps[0].Request.Path; got != "/v1/orders/{{place.id}}" {
		t.Fatalf("the example no longer reads an output two needs away: %s", got)
	}
	if ps := Validate(j, nil); len(ps) != 0 {
		t.Fatalf("two needs away: %v", ps)
	}

	// audit needs place, as pay does. It is pay's sibling, and may not read pay's output.
	sibling := func(ref string) domain.Leg {
		return domain.Leg{
			ID: "audit", Surface: domain.SurfaceAPI, Service: "orders", Needs: []domain.LegID{"place"},
			Steps: []domain.LegStep{{
				ID: "read", Action: "Read", Hint: &domain.RequestHint{Method: "GET", Path: "/v1/orders/{id}"},
				Request: domain.APIRequest{Method: "GET", Path: "/v1/orders/" + ref},
			}},
		}
	}
	j = example(t)
	j.Legs = append(j.Legs, sibling("{{pay.amount}}"))
	ps := Validate(j, nil)
	if !hasProblem(ps, "legs[3].steps[0].request.path", "{{pay.amount}} is an output of leg pay, which leg audit does not need") || len(ps) != 1 {
		t.Fatalf("a sibling: %v", ps)
	}
	j = example(t)
	j.Legs = append(j.Legs, sibling("{{place.id}}"))
	if ps := Validate(j, nil); len(ps) != 0 {
		t.Fatalf("a direct need: %v", ps)
	}

	// An output of a leg that comes later in the file is read the same way, when it is needed.
	j = example(t)
	j.Legs[0], j.Legs[2] = j.Legs[2], j.Legs[0]
	if ps := Validate(j, nil); len(ps) != 0 {
		t.Fatalf("legs in another file order: %v", ps)
	}
}

func TestInvariantTemplateOneVariable(t *testing.T) {
	for _, tc := range []struct {
		entry, want string // want is "" for an accepted entry
	}{
		{"{{place.amount}}", ""},
		{"total {{pay.amount}}", "must be one variable and nothing else"},
		{"{{pay.amount}} ", "must be one variable and nothing else"},
		{" {{pay.amount}}", "must be one variable and nothing else"},
		{"{{pay.amount}}{{pay.amount}}", "must be one variable and nothing else"},
		{"{{pay.amount}}\n", "must be one variable and nothing else"},
		{"pay.amount", "must be one variable and nothing else"},
		{"", "must be one variable and nothing else"},
		{"{{amount}}", "must be one variable and nothing else"},
		{"{{run.id}}", "is not an output of a leg or of a given entry"},
		{"{{secret.token}}", "is not an output of a leg or of a given entry"},
		{"{{place.id.x}}", "must be one variable and nothing else"},
		{"{{pay.total}}", "is not an output of leg pay"},
	} {
		t.Run(tc.entry, func(t *testing.T) {
			j := example(t)
			j.Invariants[0].Equal[0] = tc.entry
			ps := Validate(j, nil)
			if tc.want == "" {
				if len(ps) != 0 {
					t.Fatalf("problems = %v", ps)
				}
				return
			}
			if !hasProblem(ps, "invariants[0].equal[0]", tc.want) || len(ps) != 1 {
				t.Fatalf("problems = %v, want one that holds %q", ps, tc.want)
			}
		})
	}
}

func TestCycleNamesLegs(t *testing.T) {
	j := example(t)
	// pay needs settled, and settled needs pay
	j.Legs[0].Outputs, j.Invariants = nil, nil
	j.Legs[1].Needs = []domain.LegID{"settled"}
	j.Legs[1].Steps[0].Request.JSON = nil
	j.Legs[2].Steps[0].Request.Path = "/v1/orders/{{amount}}"
	ps := Validate(j, nil)
	if !hasProblem(ps, "legs[1].needs", "needs has a cycle: pay, settled, pay") {
		t.Fatalf("problems = %v", ps)
	}
	if _, err := Order(j); err == nil || !strings.Contains(err.Error(), "needs has a cycle: pay, settled, pay") {
		t.Fatalf("Order err = %v", err)
	}
}

func TestStepHintNotValidIsNamed(t *testing.T) {
	for _, tc := range []struct {
		name, old, with, path, want string
	}{
		{"hint is not a path", `hint: { method: POST, path: /v1/payments }`, `hint: { method: POST, path: v1/payments }`, "legs[1].steps[0].request", "does not match v1/payments"},
		{"hint has a query", `hint: { method: POST, path: /v1/payments }`, `hint: { method: POST, path: "/v1/payments?x=1" }`, "legs[1].steps[0].request", "does not match /v1/payments?x=1"},
		{"hint has another method", `hint: { method: POST, path: /v1/payments }`, `hint: { method: GET, path: /v1/payments }`, "legs[1].steps[0].request", "the request is POST and the step expects GET"},
		{"no hint", "        hint: { method: POST, path: /v1/payments }\n", "", "legs[1].steps[0].hint", "a step needs a hint"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j, err := Parse([]byte(edited(t, tc.old, tc.with)), "x.yaml")
			if err != nil {
				t.Fatal(err)
			}
			if ps := Validate(j, nil); !hasProblem(ps, tc.path, tc.want) {
				t.Fatalf("problems = %v", ps)
			}
		})
	}
}

func TestFixtureThatDoesNotExistIsNamed(t *testing.T) {
	j := example(t)
	j.Given = []domain.Given{{Fixture: "order.refunded", As: "order"}}
	ps := Validate(j, orderPaid)
	want := "fixture order.refunded was not found among the valid fixtures of .semar/fixtures"
	if !hasProblem(ps, "given[0].fixture", want) || len(ps) != 1 {
		t.Fatalf("problems = %v", ps)
	}
	// A leg that reads an output of the missing fixture is not a second problem for any output name.
	j.Legs[1].Steps[0].Request.JSON = map[string]any{"orderId": "{{order.id}}"}
	if ps := Validate(j, orderPaid); len(ps) != 1 {
		t.Fatalf("problems = %v", ps)
	}
	// Without a lookup the fixtures are not checked.
	if ps := Validate(j, nil); len(ps) != 0 {
		t.Fatalf("problems without a lookup = %v", ps)
	}
}

func TestProblemString(t *testing.T) {
	for _, tc := range []struct {
		p    Problem
		want string
	}{
		{Problem{File: "a.yaml", Line: 4, Path: "legs[0].id", Message: "x"}, "a.yaml:4: legs[0].id: x"},
		{Problem{File: "a.yaml", Path: "id", Message: "x"}, "a.yaml: id: x"},
		{Problem{Message: "x"}, "x"},
	} {
		if got := tc.p.String(); got != tc.want {
			t.Errorf("String() = %q, want %q", got, tc.want)
		}
	}
}
