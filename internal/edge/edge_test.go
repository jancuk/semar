package edge_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/edge"
	"github.com/jancuk/semar/internal/openapi"
)

const shopAPI = `openapi: 3.0.3
paths:
  /orders:
    post:
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [amount, customer]
              properties:
                amount: {type: integer, minimum: 1, maximum: 1000}
                customer: {type: string, minLength: 2, maxLength: 8}
                channel: {type: string, enum: [web, app]}
                note: {type: string}
      responses:
        "201": {description: created}
        "400": {description: refused}
        "401": {description: no session}
    get:
      parameters:
        - {name: status, in: query, schema: {type: string, enum: [created, paid]}}
        - {name: limit, in: query, schema: {type: integer, minimum: 1, maximum: 50}}
      responses:
        "200": {description: ok}
        "422": {description: bad query}
        "401": {description: no session}
  /orders/{id}:
    get:
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}}
      responses:
        "200": {description: ok}
        "404": {description: none}
`

func operation(t *testing.T, method, path string) openapi.Operation {
	t.Helper()
	doc, err := openapi.Parse([]byte(shopAPI), "shop.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m := doc.Lookup(method, path)
	if !m.Found {
		t.Fatalf("no operation %s %s", method, path)
	}
	return m.Operation
}

func body(t *testing.T, text string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func find(t *testing.T, cases []edge.Case, kind edge.Kind, on string) edge.Case {
	t.Helper()
	for _, c := range cases {
		if c.Kind == kind && c.On == on {
			return c
		}
	}
	t.Fatalf("no %s case on %q in %v", kind, on, kinds(cases))
	return edge.Case{}
}

func kinds(cases []edge.Case) []string {
	var out []string
	for _, c := range cases {
		out = append(out, string(c.Kind)+":"+c.On)
	}
	return out
}

func TestAWriteGetsTheKindsOfItsBody(t *testing.T) {
	op := operation(t, "POST", "/orders")
	base := domain.APIRequest{Service: "orders", Method: "POST", Path: "/v1/orders",
		JSON: body(t, `{"amount": 5, "customer": "acme", "channel": "web"}`)}
	cases, left := edge.Generate(op, base)

	req := find(t, cases, edge.RequiredField, "amount")
	if _, ok := req.Request.JSON.(map[string]any)["amount"]; ok {
		t.Error("the required-field case still sends amount")
	}
	if req.Status != 400 {
		t.Errorf("status = %d, want 400, the first declared", req.Status)
	}
	if _, ok := base.JSON.(map[string]any)["amount"]; !ok {
		t.Error("the base request was changed")
	}
	if got := find(t, cases, edge.WrongType, "amount").Request.JSON.(map[string]any)["amount"]; got != "semar-wrong-type" {
		t.Errorf("wrong type of amount = %#v", got)
	}
	if got := find(t, cases, edge.WrongType, "customer").Request.JSON.(map[string]any)["customer"]; got != json.Number("12345") {
		t.Errorf("wrong type of customer = %#v", got)
	}
	if got := find(t, cases, edge.OutOfRange, "customer.maxLength"); got.Status != 400 {
		t.Errorf("out of range status = %d", got.Status)
	}
	if n := countKind(cases, edge.OutOfRange); n != 4 {
		t.Errorf("out-of-range cases = %d, want minLength, maxLength, minimum and maximum", n)
	}
	enum := find(t, cases, edge.NotInEnum, "channel").Request.JSON.(map[string]any)["channel"]
	if s, _ := enum.(string); s == "web" || s == "app" || s == "" {
		t.Errorf("not-in-enum value = %#v", enum)
	}
	if c := find(t, cases, edge.NoSession, ""); c.Status != 401 || !c.NoSession {
		t.Errorf("no-session case = %+v", c)
	}
	if countKind(cases, edge.UnknownID) != 0 {
		t.Error("a path with no parameter has an unknown-id case")
	}
	if len(left) != 0 {
		t.Errorf("left = %v", left)
	}
}

func countKind(cases []edge.Case, k edge.Kind) int {
	n := 0
	for _, c := range cases {
		if c.Kind == k {
			n++
		}
	}
	return n
}

func TestAReadGetsItsQueryAndItsPathCases(t *testing.T) {
	list := operation(t, "GET", "/orders")
	cases, left := edge.Generate(list, domain.APIRequest{Service: "orders", Method: "GET", Path: "/v1/orders"})
	if c := find(t, cases, edge.NotInEnum, "status"); c.Request.Query["status"] != "semar-not-a-listed-value" || c.Status != 422 {
		t.Errorf("enum case = %+v", c)
	}
	if c := find(t, cases, edge.OutOfRange, "limit.maximum"); c.Request.Query["limit"] != "0" && c.Request.Query["limit"] != "51" {
		t.Errorf("range case = %+v", c)
	}
	if countKind(cases, edge.RequiredField)+countKind(cases, edge.WrongType) != 0 {
		t.Errorf("a read with no body has body cases: %v", kinds(cases))
	}
	if len(left) != 0 {
		t.Errorf("left = %v", left)
	}

	one := operation(t, "GET", "/orders/{id}")
	cases, left = edge.Generate(one, domain.APIRequest{Service: "orders", Method: "GET", Path: "/v1/orders/{{order_id}}"})
	if c := find(t, cases, edge.UnknownID, "id"); c.Request.Path != "/v1/orders/semar-no-such-id" || c.Status != 404 {
		t.Errorf("unknown id = %+v", c)
	}
	if len(left) != 1 || left[0].Kind != edge.NoSession || left[0].Reason != edge.ReasonNoStatus {
		t.Errorf("a read with no 401 should leave out the no-session case with its reason: %v", left)
	}
}

func TestTheStatusComesFromWhatTheOperationDeclares(t *testing.T) {
	for _, tc := range []struct {
		statuses string
		want     int // 0 for left out
	}{
		{`"200": {description: ok}, "400": {description: x}, "422": {description: x}`, 400},
		{`"200": {description: ok}, "422": {description: x}`, 422},
		{`"200": {description: ok}, "409": {description: x}`, 409},
		{`"200": {description: ok}, "4XX": {description: x}, default: {description: x}`, 0},
		{`"200": {description: ok}, "404": {description: x}, "401": {description: x}`, 0},
	} {
		doc := "openapi: 3.0.3\npaths:\n  /x:\n    post:\n      requestBody:\n        content:\n          application/json:\n            schema: {type: object, required: [a], properties: {a: {type: string}}}\n      responses: {" + tc.statuses + "}\n"
		d, err := openapi.Parse([]byte(doc), "x.yaml")
		if err != nil {
			t.Fatal(err)
		}
		op := d.Lookup("POST", "/x").Operation
		cases, left := edge.Generate(op, domain.APIRequest{Method: "POST", Path: "/x", JSON: map[string]any{"a": "v"}})
		var got int
		for _, c := range cases {
			if c.Kind == edge.RequiredField {
				got = c.Status
			}
		}
		if got != tc.want {
			t.Errorf("%s: status %d, want %d", tc.statuses, got, tc.want)
		}
		if tc.want == 0 && !slices.ContainsFunc(left, func(l edge.Left) bool { return l.Kind == edge.RequiredField && l.Reason == edge.ReasonNoStatus }) {
			t.Errorf("%s: the case that has no status is not counted: %v", tc.statuses, left)
		}
	}
}

func TestSelectKeepsTheHigherPriorityByOperationAndByPlan(t *testing.T) {
	mk := func(op string, kind edge.Kind, order int) edge.Candidate {
		return edge.Candidate{Case: edge.Case{Kind: kind, On: "n"}, Operation: op, Order: order}
	}
	in := []edge.Candidate{
		mk("A", edge.NoSession, 0), mk("A", edge.RequiredField, 1), mk("A", edge.WrongType, 2), mk("A", edge.NotInEnum, 3),
		mk("B", edge.OtherAccount, 4), mk("B", edge.RequiredField, 5),
	}
	kept, left := edge.Select(in, 3, 4)
	var got []int
	for _, c := range kept {
		got = append(got, c.Order)
	}
	if !slices.Equal(got, []int{1, 2, 3, 5}) {
		t.Errorf("kept orders %v, want [1 2 3 5]: the first three of A by priority, then the plan limit cuts the lowest", got)
	}
	if len(left) != 2 {
		t.Fatalf("left = %v", left)
	}
	reasons, counts := edge.CountLeft(left)
	if len(reasons) != 2 || counts[reasons[0]] != 1 {
		t.Errorf("reasons %v %v", reasons, counts)
	}
}

func TestSummaryCountsWhatItDraftedAndWhatItLeftOut(t *testing.T) {
	line := edge.Summary(3, []edge.Left{{Reason: edge.ReasonNoStatus}, {Reason: edge.ReasonNoStatus}, {Reason: edge.ReasonWrite}})
	want := "edge cases: 3 drafted, 3 left out (2 because the API document declares no fitting status; 1 because it writes, and a write is never tried as another account); each needs your review"
	if line != want {
		t.Errorf("summary:\n%s\nwant:\n%s", line, want)
	}
}

func TestIDsOfEdgeCases(t *testing.T) {
	if id := edge.IDOf("PROJ-12", 3); id != "PROJ-12-E03" {
		t.Errorf("id = %s", id)
	}
	for id, want := range map[domain.ScenarioID]int{"PROJ-12-E07": 7, "PROJ-12-01": 0, "PROJ-12-E": 0, "PROJ-12-Ex1": 0, "OTHER-1-E02": 0} {
		if got := edge.Number("PROJ-12", id); got != want {
			t.Errorf("Number(%s) = %d, want %d", id, got, want)
		}
	}
}
