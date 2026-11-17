package plan

import (
	"slices"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/openapi"
)

func keysOf(ops []PacketOperation) []string {
	var out []string
	for _, op := range ops {
		out = append(out, op.Key)
	}
	return out
}

func TestPacketsHaveOnePerCriterion(t *testing.T) {
	ps := Packets(shopInput(t), "v1")
	if len(ps) != 3 {
		t.Fatalf("got %d packets, want 3", len(ps))
	}
	for i, want := range []struct {
		cite string
		num  int
	}{{ticket482 + ":9", 1}, {ticket482 + ":10", 2}, {ticket482 + ":11", 3}} {
		p := ps[i]
		if p.Ticket != "SHOP-482" || p.Cite != want.cite || p.Number != want.num || p.Version != "v1" {
			t.Errorf("packet %d = %s %s %d", i, p.Ticket, p.Cite, p.Number)
		}
	}
	if !strings.Contains(ps[0].Criterion, "REFUND_EXCEEDS_TOTAL") {
		t.Errorf("criterion = %q", ps[0].Criterion)
	}
	if len(ps[0].Fixtures) != 1 || len(ps[0].Accounts) != 2 || ps[0].Account != "default" {
		t.Errorf("fixtures and accounts: %+v", ps[0])
	}
}

func TestPacketOperationsComeFromTheDiffTheRegistrationsAndTheTicket(t *testing.T) {
	in := shopInput(t)
	ps := Packets(in, "v1")
	// The diff changes the handler Refund and a comment of the file that registers it. No route is
	// in the diff, so the refund operation comes from the registration, and it comes first. The
	// operations that share the word orders with it follow, and then the rest of this small document.
	got := keysOf(ps[0].Operations)
	if !slices.Equal(got, []string{"post_v1_orders_orderId_refunds", "createOrder", "getOrder", "health"}) {
		t.Errorf("operations = %v", got)
	}
	op := ps[0].Operations[0]
	if op.Method != "POST" || op.Route != "/v1/orders/{orderId}/refunds" || len(op.Params) != 1 || op.Params[0].Name != "orderId" ||
		len(op.Body) != 2 || !op.Body[0].Required || len(op.Responses) != 2 {
		t.Errorf("operation = %+v", op)
	}

	// A route that the ticket names is an operation of the ticket, and the one that the criterion
	// names comes first.
	in.Tickets[0].Text = strings.Replace(shopTicket, "2. A refund of part", "2. `GET /v1/orders/{orderId}` shows it after a refund of part", 1)
	ps = Packets(in, "v1")
	if got := keysOf(ps[1].Operations); !slices.Equal(got[:2], []string{"getOrder", "post_v1_orders_orderId_refunds"}) {
		t.Errorf("operations of criterion 2 = %v", got)
	}
	if got := keysOf(ps[0].Operations); !slices.Equal(got[:2], []string{"getOrder", "post_v1_orders_orderId_refunds"}) {
		t.Errorf("operations of criterion 1 = %v: the ticket names the route, so it is reached", got)
	}
}

func TestPacketOperationFromAnOperationIDAndARouteInTheDiff(t *testing.T) {
	in := shopInput(t)
	in.Diff = []byte("diff --git a/web/orders.js b/web/orders.js\n--- a/web/orders.js\n+++ b/web/orders.js\n@@ -1 +1 @@\n+fetch(\"/v1/orders/\" + id)\n+createOrder(x)\n")
	in.Change.Files = in.Change.Files[2:]
	ps := Packets(in, "v1")
	got := keysOf(ps[0].Operations)
	if got[0] != "createOrder" {
		// "/v1/orders/" ends the route /v1/orders, and the operationId createOrder is a word of the diff.
		t.Errorf("operations = %v", got)
	}
}

func TestPacketOperationsKeepOnlyAllowedMethods(t *testing.T) {
	in := shopInput(t)
	in.Methods = []string{"GET"}
	for _, op := range Packets(in, "v1")[0].Operations {
		if op.Method != "GET" {
			t.Errorf("%s %s is not an allowed method", op.Method, op.Route)
		}
	}
}

func TestPacketDiffIsTheDiffOfTheTicketsFilesWithinTheBudget(t *testing.T) {
	in := shopInput(t)
	in.Change.Files[2].Tickets = []string{"SHOP-999"}
	p := Packets(in, "v1")[0]
	if strings.Contains(p.Diff, "web/orders.js") || !strings.Contains(p.Diff, "handler.go") {
		t.Errorf("the packet shows the files of another ticket:\n%s", p.Diff)
	}

	in = shopInput(t)
	big := "diff --git a/internal/refund/handler.go b/internal/refund/handler.go\n--- a/internal/refund/handler.go\n+++ b/internal/refund/handler.go\n@@ -1 +1 @@\n" +
		strings.Repeat("+// a long line of a big change\n", 2000)
	in.Diff = []byte(big + shopDiff[strings.Index(shopDiff, "diff --git a/web"):])
	p = Packets(in, "v1")[0]
	if len(p.Diff) > MaxPacketDiffBytes+200 {
		t.Errorf("the diff is %d bytes, over the budget %d", len(p.Diff), MaxPacketDiffBytes)
	}
	if !strings.Contains(p.Diff, "web/orders.js") {
		t.Errorf("the small file fits whole and comes first:\n%.200s", p.Diff)
	}
	if len(p.DiffOmitted) != 1 || !strings.Contains(p.DiffOmitted[0], "internal/refund/handler.go") {
		t.Errorf("omitted = %v", p.DiffOmitted)
	}
}

func TestPacketDiffForATicketThatNoFileNamesIsTheWholeDiff(t *testing.T) {
	in := shopInput(t)
	for i := range in.Change.Files {
		in.Change.Files[i].Tickets = nil
	}
	p := Packets(in, "v1")[0]
	if !strings.Contains(p.Diff, "handler.go") || !strings.Contains(p.Diff, "web/orders.js") {
		t.Errorf("diff = %s", p.Diff)
	}
}

func TestPacketHashIgnoresTheCiteAndTheExistingScenarios(t *testing.T) {
	in := shopInput(t)
	a := Packets(in, "v1")[0]
	b := a
	b.Cite, b.Number = ticket482+":99", 7
	b.Existing = []string{"SHOP-482-01: Refund too much"}
	if a.Hash() != b.Hash() {
		t.Error("a moved line or an existing scenario changed the hash")
	}
	for name, change := range map[string]func(p *Packet){
		"criterion": func(p *Packet) { p.Criterion += " and more" },
		"diff":      func(p *Packet) { p.Diff += "+x\n" },
		"operation": func(p *Packet) { p.Operations = p.Operations[:0] },
		"fixture":   func(p *Packet) { p.Fixtures = nil },
		"account":   func(p *Packet) { p.Accounts = p.Accounts[:1] },
		"version":   func(p *Packet) { p.Version = "v2" },
		"ticket":    func(p *Packet) { p.Ticket = "SHOP-1" },
	} {
		c := a
		change(&c)
		if c.Hash() == a.Hash() {
			t.Errorf("a change of the %s kept the hash", name)
		}
	}
	if a.Hash() != Packets(in, "v1")[0].Hash() {
		t.Error("the hash is not stable")
	}
}

func TestPacketsListExistingScenarios(t *testing.T) {
	in := shopInput(t)
	in.Existing = map[string][]domain.Scenario{"SHOP-482": {{ID: "SHOP-482-01", Title: "Refund too much"}}}
	if got := Packets(in, "v1")[0].Existing; !slices.Equal(got, []string{"SHOP-482-01: Refund too much"}) {
		t.Errorf("existing = %v", got)
	}
}

func TestPacketsOfATicketWithoutCriteriaAreNone(t *testing.T) {
	in := shopInput(t)
	in.Tickets[0].Text = "# SHOP-482\n\nNo list.\n"
	if got := Packets(in, "v1"); len(got) != 0 {
		t.Errorf("got %d packets", len(got))
	}
}

func TestPacketsReadGivenWhenThenCriteria(t *testing.T) {
	in := shopInput(t)
	in.Tickets[0].Text = "## Acceptance criteria\n\nGiven a paid order\nWhen a refund above the total is made\nThen the answer is 422\n"
	ps := Packets(in, "v1")
	if len(ps) != 1 || ps[0].Criterion != "Given a paid order When a refund above the total is made Then the answer is 422" {
		t.Errorf("packets = %+v", ps)
	}
}

func TestRegistrationsInTheUsualRouters(t *testing.T) {
	for name, tc := range map[string]struct {
		text   string
		route  string
		method string
		hand   string
	}{
		"go mux":    {`mux.HandleFunc("POST /v1/cards/{id}/move", h.Move)`, "/v1/cards/{}/move", "POST", "h.Move"},
		"express":   {`router.post('/cards/:id/move', moveCard);`, "/cards/{}/move", "", "moveCard"},
		"decorator": {"@router.post(\"/cards/{card_id}/move\")\nasync def move_card(card_id: str):\n", "/cards/{}/move", "", "move_card"},
		"gin":       {`r.PATCH("/v1/cards/:id", handlers.PatchCard)`, "/v1/cards/{}", "", "handlers.PatchCard"},
	} {
		t.Run(name, func(t *testing.T) {
			got := registrationsIn(tc.text)
			if len(got) != 1 || got[0].Route != tc.route || got[0].Method != tc.method || !slices.Contains(got[0].Handlers, tc.hand) {
				t.Errorf("got %+v", got)
			}
		})
	}
	if got := registrationsIn(`log.Println("/")` + "\n" + `fmt.Println("see /")`); len(got) != 0 {
		t.Errorf("a slash is not a route: %+v", got)
	}
}

func TestRegistrationMatchesAGroupedRoute(t *testing.T) {
	doc, err := openapi.Parse([]byte("openapi: 3.0.0\nservers: [{url: \"https://x.example.com/v1\"}]\npaths:\n  /cards/{id}/move:\n    post: {responses: {\"200\": {}}}\n"), "d")
	if err != nil {
		t.Fatal(err)
	}
	op := doc.Operations[0]
	for route, want := range map[string]bool{"/cards/{}/move": true, "/v1/cards/{}/move": true, "/move": false, "/cards/{}": false} {
		if got := registrationMatches(registration{Route: route}, doc, op); got != want {
			t.Errorf("%s: got %v, want %v", route, got, want)
		}
	}
	if registrationMatches(registration{Route: "/cards/{}/move", Method: "GET"}, doc, op) {
		t.Error("the method differs")
	}
}

func TestPacketOperationsOfALargeDocumentAreTheNearOnes(t *testing.T) {
	in := shopInput(t)
	var doc strings.Builder
	doc.WriteString("openapi: 3.0.0\npaths:\n  /v1/orders/{orderId}/refunds:\n    post: {operationId: refund, responses: {\"201\": {}}}\n  /v1/orders:\n    post: {operationId: createOrder, responses: {\"201\": {}}}\n")
	for i := range maxPacketOperations {
		doc.WriteString("  /v1/other" + string(rune('a'+i)) + ":\n    get: {responses: {\"200\": {}}}\n")
	}
	d, err := openapi.Parse([]byte(doc.String()), "docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	in.Knowledge[0].API = &d
	got := keysOf(Packets(in, "v1")[0].Operations)
	if !slices.Equal(got, []string{"refund", "createOrder"}) {
		t.Errorf("operations = %v: only the touched and the near ones are kept when the document is large", got)
	}
}
