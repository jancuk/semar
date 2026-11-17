package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/knowledge"
	"github.com/jancuk/semar/internal/openapi"
)

// shopAPI is the API document of the shop that the criteria tests plan for. refunds has no
// operationId, so its key is made from the method and the path.
const shopAPI = `openapi: 3.0.3
paths:
  /v1/orders:
    post:
      operationId: createOrder
      summary: Create an order
      requestBody:
        required: true
        content: {application/json: {schema: {$ref: "#/components/schemas/OrderInput"}}}
      responses:
        "201": {description: created, content: {application/json: {schema: {$ref: "#/components/schemas/OrderEnvelope"}}}}
  /v1/orders/{orderId}:
    parameters:
      - {name: orderId, in: path, required: true, schema: {type: string}}
    get:
      operationId: getOrder
      summary: One order
      responses:
        "200": {description: ok, content: {application/json: {schema: {$ref: "#/components/schemas/OrderEnvelope"}}}}
        "404": {description: not found}
  /v1/orders/{orderId}/refunds:
    parameters:
      - {name: orderId, in: path, required: true, schema: {type: string}}
    post:
      summary: Refund part or all of an order
      requestBody:
        required: true
        content: {application/json: {schema: {$ref: "#/components/schemas/RefundInput"}}}
      responses:
        "201": {description: refunded, content: {application/json: {schema: {$ref: "#/components/schemas/OrderEnvelope"}}}}
        "422": {description: refused, content: {application/json: {schema: {$ref: "#/components/schemas/Error"}}}}
  /v1/health:
    get:
      operationId: health
      responses:
        "200": {description: ok}
components:
  schemas:
    OrderInput:
      type: object
      required: [customer]
      properties: {customer: {type: string}}
    RefundInput:
      type: object
      required: [amount]
      properties: {amount: {type: integer}, reason: {type: string}}
    Order:
      type: object
      required: [id, total, refundedTotal]
      properties: {id: {type: string}, total: {type: integer}, refundedTotal: {type: integer}, status: {type: string, enum: [paid, refunded]}}
    OrderEnvelope:
      type: object
      properties: {data: {$ref: "#/components/schemas/Order"}}
    Error:
      type: object
      properties: {error: {type: object, properties: {code: {type: string}, message: {type: string}}}}
`

const shopTicket = `# SHOP-482: Refunds of an order

## Goal

A support agent refunds part of an order.

## Acceptance criteria

1. A refund above the order total answers 422 with the error code REFUND_EXCEEDS_TOTAL, and the order stays as it was.
2. A refund of part of the total answers 201, and refundedTotal of the order grows by the amount.
3. Web: the order page shows the refund in its history.

## Notes

Not a criterion.
`

const shopRoutes = `package refund

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/health", h.Health)
	mux.HandleFunc("POST /v1/orders/{id}/refunds", h.Refund)
}
`

const shopHandler = `package refund

func (h *Handler) Refund(w http.ResponseWriter, r *http.Request) {
	if in.Amount > order.Total {
		writeError(w, 422, "REFUND_EXCEEDS_TOTAL")
	}
}
`

// shopDiff changes the handler of the refund and, far from any route, the file that registers it.
const shopDiff = `diff --git a/internal/refund/handler.go b/internal/refund/handler.go
index 1111111..2222222 100644
--- a/internal/refund/handler.go
+++ b/internal/refund/handler.go
@@ -3,5 +3,5 @@ func (h *Handler) Refund(w http.ResponseWriter, r *http.Request) {
 	// the amount is checked first
-	if in.Amount >= order.Total {
+	if in.Amount > order.Total {
 		writeError(w, 422, "REFUND_EXCEEDS_TOTAL")
diff --git a/internal/refund/routes.go b/internal/refund/routes.go
index 3333333..4444444 100644
--- a/internal/refund/routes.go
+++ b/internal/refund/routes.go
@@ -1,3 +1,4 @@
+// Routes of the refunds.
 package refund
 
diff --git a/web/orders.js b/web/orders.js
index 5555555..6666666 100644
--- a/web/orders.js
+++ b/web/orders.js
@@ -1,2 +1,2 @@
-render(order)
+render(order, history)
`

// shopInput is a plan input for the shop: one ticket with three criteria, an API document, one
// fixture, two accounts, and a source copy with the handler and the router of the refunds.
func shopInput(t *testing.T) Input {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"internal/refund/handler.go": shopHandler,
		"internal/refund/routes.go":  shopRoutes,
		"web/orders.js":              "render(order, history)\n",
		ticket482:                    shopTicket,
	} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	doc, err := openapi.Parse([]byte(shopAPI), "docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	api := knowledge.Source{ID: "api", Kind: config.KnowledgeOpenAPI, Path: "docs/openapi.yaml", Files: 1, API: &doc}
	files := []domain.FileChange{
		{Path: "internal/refund/handler.go", Status: "modified", Kind: "source", Added: 1, Deleted: 1, Tickets: []string{"SHOP-482"}},
		{Path: "internal/refund/routes.go", Status: "modified", Kind: "source", Added: 1, Tickets: []string{"SHOP-482"}},
		{Path: "web/orders.js", Status: "modified", Kind: "source", Added: 1, Deleted: 1, Tickets: []string{"SHOP-482"}},
	}
	return Input{
		Change: domain.ChangeSet{
			Base: "main", Head: "release", BaseCommit: strings.Repeat("a", 40), HeadCommit: strings.Repeat("b", 40),
			Tickets: []domain.TicketChange{{Key: "SHOP-482"}}, Files: files,
		},
		SourceDir: dir, Diff: []byte(shopDiff),
		Services: []string{"shop"}, Methods: []string{"GET", "POST", "PATCH"}, AuthProfile: "default",
		Limits:    config.Plan{MaxScenarios: 12},
		Knowledge: []knowledge.Source{api},
		Tickets:   []knowledge.Ticket{{Key: "SHOP-482", Path: ticket482, Text: shopTicket}},
		Fixtures: []fixture.Entry{{
			ID: "order.paid", Description: "A paid order for 100000 of a customer", Approved: true,
			Params: []fixture.ParamEntry{{Name: "customer", Required: true}}, Outputs: []string{"order"},
		}},
		Accounts: []Account{{Name: "default", Description: "A support agent"}, {Name: "bob", Description: "A customer with no refund rights"}},
	}
}
