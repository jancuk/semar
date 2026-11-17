package openapi_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/openapi"
)

const shopYAML = `openapi: 3.0.3
paths:
  /v1/orders/{orderId}:
    parameters:
      - $ref: "#/components/parameters/orderId"
    get:
      operationId: getOrder
      parameters:
        - {name: expand, in: query, schema: {type: string, enum: [items, customer]}}
      responses:
        "200": {description: ok, content: {application/json: {schema: {$ref: "#/components/schemas/OrderEnvelope"}}}}
        "404": {description: none}
  /v1/orders/{orderId}/refunds:
    parameters:
      - $ref: "#/components/parameters/orderId"
    post:
      requestBody:
        required: true
        content: {application/json: {schema: {$ref: "#/components/schemas/RefundInput"}}}
      responses:
        "201": {description: ok}
        "422":
          description: refused
          content: {application/json: {schema: {$ref: "#/components/schemas/Error"}}}
  /v1/orders:
    get:
      responses:
        "200": {description: ok, content: {application/json: {schema: {type: object, properties: {data: {type: array, items: {$ref: "#/components/schemas/Order"}}}}}}}
    post:
      requestBody:
        content: {application/json: {schema: {type: object}}}
      responses: {"201": {description: ok}}
components:
  parameters:
    orderId: {name: orderId, in: path, required: true, schema: {type: string}}
  schemas:
    RefundInput:
      type: object
      required: [amount]
      properties:
        amount: {type: integer}
        reason: {type: string, nullable: true}
    Order:
      type: object
      required: [id, status]
      properties:
        id: {type: string}
        status: {type: string, enum: [paid, refunded]}
        parent: {$ref: "#/components/schemas/Order"}
    OrderEnvelope:
      type: object
      properties: {data: {$ref: "#/components/schemas/Order"}}
    Error:
      type: object
      properties: {error: {type: object, properties: {code: {type: string}}}}
`

func opOf(t *testing.T, doc openapi.Document, method, path string) openapi.Operation {
	t.Helper()
	for _, op := range doc.Operations {
		if op.Method == method && op.Path == path {
			return op
		}
	}
	t.Fatalf("no %s %s in %+v", method, path, doc.Operations)
	return openapi.Operation{}
}

func names[T any](list []T, name func(T) string) []string {
	var out []string
	for _, x := range list {
		out = append(out, name(x))
	}
	return out
}

func TestParseReadsTheDetailOfAnOperation(t *testing.T) {
	doc, err := openapi.Parse([]byte(shopYAML), "shop.yaml")
	if err != nil {
		t.Fatal(err)
	}

	get := opOf(t, doc, "GET", "/v1/orders/{orderId}")
	if get.Key != "getOrder" {
		t.Errorf("Key = %q, want the operationId", get.Key)
	}
	wantParams := []openapi.Param{
		{Name: "orderId", In: "path", Required: true, Type: "string"},
		{Name: "expand", In: "query", Type: "string", Enum: []string{"items", "customer"}},
	}
	if !reflect.DeepEqual(get.Params, wantParams) {
		t.Errorf("Params = %+v, want %+v", get.Params, wantParams)
	}
	if len(get.Responses) != 1 || get.Responses[0].Status != "200" {
		t.Fatalf("Responses = %+v", get.Responses)
	}
	got := names(get.Responses[0].Fields, func(f openapi.Field) string { return f.Name })
	want := []string{"$.data", "$.data.id", "$.data.parent", "$.data.status"}
	if !slices.Equal(got, want) {
		t.Errorf("response fields = %v, want %v (a reference to itself stops)", got, want)
	}

	refund := opOf(t, doc, "POST", "/v1/orders/{orderId}/refunds")
	if refund.Key != "post_v1_orders_orderId_refunds" {
		t.Errorf("Key = %q, want a name made from the method and the path", refund.Key)
	}
	wantBody := []openapi.Field{
		{Name: "amount", Type: "integer", Required: true},
		{Name: "reason", Type: "string", Nullable: true},
	}
	if !refund.BodyKnown || !refund.BodyRequired || refund.BodyOpen || !reflect.DeepEqual(refund.Body, wantBody) {
		t.Errorf("body = %+v known=%v required=%v open=%v", refund.Body, refund.BodyKnown, refund.BodyRequired, refund.BodyOpen)
	}
	if len(refund.Responses) != 1 || refund.Responses[0].Status != "422" ||
		!reflect.DeepEqual(names(refund.Responses[0].Fields, func(f openapi.Field) string { return f.Name }), []string{"$.error", "$.error.code"}) {
		t.Errorf("Responses = %+v", refund.Responses)
	}

	list := opOf(t, doc, "GET", "/v1/orders")
	if f := list.Responses[0].Fields; len(f) < 2 || f[0].Name != "$.data" || f[0].Type != "array" || f[1].Name != "$.data[*].id" {
		t.Errorf("array fields = %+v", f)
	}
	create := opOf(t, doc, "POST", "/v1/orders")
	if !create.BodyKnown || !create.BodyOpen || len(create.Body) != 0 {
		t.Errorf("an object with no property is open: %+v", create)
	}
}

func TestParseReadsTheSameDetailFromJSON(t *testing.T) {
	data := `{"openapi":"3.0.0","paths":{"/pets/{id}":{"patch":{"operationId":"patchPet",
	  "parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],
	  "requestBody":{"content":{"application/json":{"schema":{"type":"object","required":["name"],"properties":{"name":{"type":"string"}}}}}},
	  "responses":{"200":{"description":"ok"}}}}}}`
	doc, err := openapi.Parse([]byte(data), "pets.json")
	if err != nil {
		t.Fatal(err)
	}
	op := opOf(t, doc, "PATCH", "/pets/{id}")
	if op.Key != "patchPet" || len(op.Params) != 1 || op.Params[0].Name != "id" || len(op.Body) != 1 || !op.Body[0].Required {
		t.Errorf("op = %+v", op)
	}
}

func TestParseReadsASwagger2Body(t *testing.T) {
	data := `{"swagger":"2.0","definitions":{"In":{"type":"object","properties":{"qty":{"type":"integer"}}}},
	  "paths":{"/cart":{"put":{"parameters":[{"in":"body","name":"b","required":true,"schema":{"$ref":"#/definitions/In"}}],"responses":{"200":{"description":"ok"}}}}}}`
	doc, err := openapi.Parse([]byte(data), "cart.json")
	if err != nil {
		t.Fatal(err)
	}
	op := opOf(t, doc, "PUT", "/cart")
	if !op.BodyKnown || !op.BodyRequired || len(op.Body) != 1 || op.Body[0].Name != "qty" {
		t.Errorf("op = %+v", op)
	}
}

func TestGeneratedKeysAreStableAndUnique(t *testing.T) {
	data := `openapi: 3.0.0
paths:
  /a/{id}: {get: {responses: {"200": {}}}}
  /a/id: {get: {responses: {"200": {}}}}
  /b: {get: {operationId: "not a name!", responses: {"200": {}}}, post: {operationId: get_b, responses: {"200": {}}}}
  /c: {get: {operationId: get_b, responses: {"200": {}}}}
`
	var first []string
	for range 2 {
		doc, err := openapi.Parse([]byte(data), "keys.yaml")
		if err != nil {
			t.Fatal(err)
		}
		keys := names(doc.Operations, func(o openapi.Operation) string { return o.Key })
		if first == nil {
			first = keys
		} else if !slices.Equal(first, keys) {
			t.Errorf("keys differ between runs: %v and %v", first, keys)
		}
		if len(slices.Compact(slices.Sorted(slices.Values(keys)))) != len(keys) {
			t.Errorf("keys are not unique: %v", keys)
		}
		for _, op := range doc.Operations {
			if op.ID != "" && op.Key == "" || strings.ContainsAny(op.Key, " !{}/") {
				t.Errorf("bad key %q for %s %s", op.Key, op.Method, op.Path)
			}
		}
	}
}

func TestDetailIsBoundedForARepetitiveDocument(t *testing.T) {
	var b strings.Builder
	b.WriteString("openapi: 3.0.0\nx: &a [1,1,1,1,1,1,1,1,1,1]\n")
	for i := range 12 {
		b.WriteString("x" + string(rune('a'+i)) + ": &" + string(rune('b'+i)) + " [*a,*a,*a,*a,*a,*a,*a,*a,*a,*a]\n")
	}
	b.WriteString("paths:\n  /a: {get: {responses: {\"200\": {}}}}\n")
	doc, err := openapi.Parse([]byte(b.String()), "alias.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Operations) != 1 {
		t.Fatalf("the routes must stay: %+v", doc.Operations)
	}
}

func TestParseReadsTheFormatOfAParameterAndAField(t *testing.T) {
	const doc = `openapi: 3.0.3
paths:
  /v1/orders:
    get:
      parameters:
        - {name: from, in: query, schema: {type: string, format: date}}
        - {name: owner, in: query, schema: {type: string, format: uuid}}
        - {name: page, in: query, schema: {type: integer}}
      responses: {"200": {description: ok}}
    post:
      requestBody:
        content: {application/json: {schema: {type: object, properties: {dueAt: {type: string, format: date-time}, day: {type: string, format: date}, note: {type: string}}}}}
      responses: {"201": {description: ok}}
`
	d, err := openapi.Parse([]byte(doc), "shop.yaml")
	if err != nil {
		t.Fatal(err)
	}
	format := map[string]string{}
	for _, p := range opOf(t, d, "GET", "/v1/orders").Params {
		format[p.Name] = p.Format
	}
	for _, f := range opOf(t, d, "POST", "/v1/orders").Body {
		format[f.Name] = f.Format
	}
	want := map[string]string{"from": "date", "owner": "uuid", "page": "", "dueAt": "date-time", "day": "date", "note": ""}
	if !reflect.DeepEqual(format, want) {
		t.Errorf("formats = %v, want %v", format, want)
	}
}
