package plan

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/knowledge"
	"github.com/jancuk/semar/internal/openapi"
)

// listPacket is the first packet of the shop with one more operation that takes IDs in a query and a body.
func listPacket(t *testing.T) (Input, Packet) {
	in, p := firstPacket(t)
	p.Operations = append([]PacketOperation{{
		Key: "createOrder", Method: "POST", Route: "/v1/orders", BodyKnown: true,
		Params: []openapi.Param{
			{Name: "customerId", In: "query", Type: "string"},
			{Name: "owner", In: "query", Type: "string", Format: "uuid"},
			{Name: "region", In: "query", Type: "string", Enum: []string{"r-1", "r-2"}},
			{Name: "status", In: "query", Type: "string"},
			{Name: "id", In: "query", Type: "string"},
		},
		Body: []openapi.Field{
			{Name: "customerId", Type: "string"},
			{Name: "parent_id", Type: "string", Nullable: true},
			{Name: "itemIDs", Type: "string"},
			{Name: "warehouseId", Type: "integer"},
			{Name: "region", Type: "string", Enum: []string{"r-1", "r-2"}},
			{Name: "regionId", Type: "string", Enum: []string{"r-1", "r-2"}},
			{Name: "ids", Type: "array"},
			{Name: "note", Type: "string"},
			{Name: "paid", Type: "boolean"},
			{Name: "tag", Type: "string", Format: "uuid"},
		},
	}}, p.Operations...)
	return in, p
}

func idStep(status int, query, body []any) func(st map[string]any) {
	return func(st map[string]any) {
		st["operation"], st["path"], st["captures"] = "createOrder", []any{}, []any{}
		st["query"], st["body"] = query, body
		st["expect"] = []any{map[string]any{"subject": "status", "name": "", "operator": "equals", "value": status}}
	}
}

func nv(name string, value any) any  { return map[string]any{"name": name, "value": value} }
func bf(field string, value any) any { return map[string]any{"field": field, "value": value} }

func TestConvertRefusesATypedInID(t *testing.T) {
	in, p := listPacket(t)
	tests := []struct {
		name  string
		query []any
		body  []any
		path  string
		says  string
	}{
		{"a query ID", []any{nv("customerId", "c-1042")}, nil, "steps[0].query[0]",
			`query parameter customerId of createOrder holds the ID "c-1042", which no step or fixture gave: capture it in an earlier step, take it from a fixture, or name it in the ticket`},
		{"a query parameter named id", []any{nv("id", "o-7")}, nil, "steps[0].query[0]", `query parameter id of createOrder holds the ID "o-7"`},
		{"a query uuid", []any{nv("owner", "9b2f6c1e-0000-4000-8000-000000000001")}, nil, "steps[0].query[0]", "query parameter owner"},
		{"a body ID", nil, []any{bf("customerId", "c-1042")}, "steps[0].body[0]",
			`field customerId of createOrder holds the ID "c-1042", which no step or fixture gave: capture it in an earlier step, take it from a fixture, or name it in the ticket`},
		{"a body ID with an underscore", nil, []any{bf("parent_id", "p-1")}, "steps[0].body[0]", `field parent_id of createOrder holds the ID "p-1"`},
		{"a body ID that ends in IDs", nil, []any{bf("itemIDs", "i-1,i-2")}, "steps[0].body[0]", "field itemIDs"},
		{"a body ID that is a number", nil, []any{bf("warehouseId", 12)}, "steps[0].body[0]", `field warehouseId of createOrder holds the ID "12"`},
		{"a body uuid", nil, []any{bf("tag", "9b2f6c1e-0000-4000-8000-000000000001")}, "steps[0].body[0]", "field tag"},
		{"an ID that only looks like the one of the ticket", []any{nv("customerId", "c-1042")}, nil, "steps[0].query[0]", "c-1042"},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			in := in
			if c.name == "an ID that only looks like the one of the ticket" {
				in.Tickets = []knowledge.Ticket{{Key: "SHOP-482", Text: "customer c-10420 and xc-1042 and c-1042x"}}
			}
			_, ps := p.Convert(withStep(t, idStep(200, c.query, c.body)), in)
			for _, pr := range ps {
				if pr.Path == c.path && strings.Contains(pr.Message, c.says) {
					return
				}
			}
			t.Fatalf("want a problem at %s that says %q, got\n%s", c.path, c.says, messages(ps))
		})
	}
}

func TestConvertKeepsAnIDThatSomethingGave(t *testing.T) {
	in, p := listPacket(t)
	in.Tickets = []knowledge.Ticket{{Key: "SHOP-482", Text: "Customer c-1042 (and 'c-2000') has two orders.\nWarehouse 77 is closed."}}
	tests := []struct {
		name   string
		status int
		query  []any
		body   []any
	}{
		{"a variable", 200, []any{nv("customerId", "{{order.customer}}")}, []any{bf("customerId", "{{customer_id}}")}},
		{"a variable inside text", 200, nil, []any{bf("customerId", "c-{{run.id}}")}},
		{"a value of an enum of the query parameter", 200, []any{nv("region", "r-1")}, nil},
		{"a value of an enum of the field", 200, nil, []any{bf("regionId", "r-2")}},
		{"an ID that the ticket names", 200, []any{nv("customerId", "c-1042")}, []any{bf("customerId", "c-2000")}},
		{"a number that the ticket names", 200, nil, []any{bf("warehouseId", 77)}},
		{"a refusal", 404, []any{nv("customerId", "c-9999")}, []any{bf("customerId", "c-9999")}},
		{"a refusal with a 422", 422, nil, []any{bf("customerId", "c-9999")}},
		{"a name that is no ID", 200, []any{nv("status", "open")}, []any{bf("note", "see c-9999"), bf("paid", true), bf("region", "r-1")}},
		{"a null that the document allows", 200, nil, []any{bf("parent_id", nil)}},
		{"an array of IDs written as text", 200, nil, []any{bf("ids", `["a","b"]`)}},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			if _, ps := p.Convert(withStep(t, idStep(c.status, c.query, c.body)), in); len(ps) > 0 {
				t.Fatalf("problems:\n%s", messages(ps))
			}
		})
	}
}

func TestCheckAnswerRefusesATypedInIDAndSaysSo(t *testing.T) {
	in, p := listPacket(t)
	_, ps := CheckAnswer(in, p, withStep(t, idStep(200, nil, []any{bf("customerId", "c-1042")})))
	if !strings.Contains(messages(ps), "which no step or fixture gave") {
		t.Fatalf("got %s", messages(ps))
	}
}
