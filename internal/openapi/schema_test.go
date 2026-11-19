package openapi_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/openapi"
)

const shopDoc = `
openapi: 3.0.3
info: {title: shop, version: "1"}
paths:
  /orders/{id}:
    get:
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {$ref: "#/components/schemas/Order"}
        "4XX":
          description: bad
          content:
            application/json:
              schema: {$ref: "#/components/schemas/Error"}
  /orders/new:
    get:
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {type: object, required: [fresh], properties: {fresh: {type: boolean}}}
  /things:
    get:
      responses:
        default:
          description: any
          content:
            application/json:
              schema: {type: array, items: {type: integer}}
components:
  schemas:
    Error:
      type: object
      required: [code]
      properties:
        code: {type: string}
    Order:
      type: object
      additionalProperties: false
      required: [id, status, items]
      properties:
        id: {type: integer}
        status: {type: string, enum: [open, paid]}
        note: {type: string, nullable: true}
        total: {type: number, minimum: 0, exclusiveMinimum: true}
        label: {type: string, minLength: 2, maxLength: 4}
        kind:
          oneOf:
            - {type: string}
            - {type: integer}
        nullable: {type: boolean}
        items:
          type: array
          items:
            allOf:
              - {$ref: "#/components/schemas/Line"}
    Line:
      type: object
      required: [sku]
      properties:
        sku: {type: string}
        qty: {type: integer, maximum: 9}
`

func parseShop(t *testing.T) openapi.Document {
	t.Helper()
	d, err := openapi.Parse([]byte(shopDoc), "shop.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func validate(t *testing.T, d openapi.Document, method, path string, status int, body string) ([]string, int, bool) {
	t.Helper()
	m := d.MatchPath(method, path)
	if !m.Found {
		t.Fatalf("no operation for %s %s", method, path)
	}
	misfits, total, checked := d.Validate(m.Operation, status, []byte(body), 5)
	var out []string
	for _, f := range misfits {
		out = append(out, f.Path+"|"+f.Want+"|"+f.Got)
	}
	return out, total, checked
}

func TestValidateAcceptsAFittingBody(t *testing.T) {
	d := parseShop(t)
	got, total, checked := validate(t, d, "GET", "/orders/42", 200,
		`{"id":7,"status":"paid","note":null,"total":1.5,"label":"abc","kind":3,"nullable":true,"items":[{"sku":"a","qty":2}]}`)
	if !checked || total != 0 || len(got) != 0 {
		t.Fatalf("checked=%v total=%d misfits=%v", checked, total, got)
	}
}

func TestValidateNamesEachKindOfMisfit(t *testing.T) {
	d := parseShop(t)
	cases := []struct {
		name, body string
		want       []string
	}{
		{"wrong type", `{"id":"7","status":"open","items":[]}`, []string{"$.id|integer|string"}},
		{"missing required", `{"id":7,"status":"open"}`, []string{"$.items|required|missing"}},
		{"nested and indexed", `{"id":7,"status":"open","items":[{"sku":"a"},{"qty":"x"}]}`,
			[]string{"$.items[1].qty|integer|string", "$.items[1].sku|required|missing"}},
		{"enum", `{"id":7,"status":"gone","items":[]}`, []string{"$.status|one of open, paid|string"}},
		{"null in a field that is not nullable", `{"id":null,"status":"open","items":[]}`, []string{"$.id|integer|null"}},
		{"exclusive minimum from the 3.0 boolean", `{"id":7,"status":"open","items":[],"total":0}`, []string{"$.total|a value above 0|integer"}},
		{"length", `{"id":7,"status":"open","items":[],"label":"abcde"}`, []string{"$.label|at most 4 characters|string"}},
		{"number limit", `{"id":7,"status":"open","items":[{"sku":"a","qty":10}]}`, []string{"$.items[0].qty|a value of at most 9|integer"}},
		{"one of", `{"id":7,"status":"open","items":[],"kind":true}`, []string{"$.kind|a value that fits one of its alternatives|boolean"}},
		{"extra property", `{"id":7,"status":"open","items":[],"surprise":1}`, []string{"$|no property beyond those it lists|object"}},
		{"not an object", `[1]`, []string{"$|object|array"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, total, checked := validate(t, d, "GET", "/orders/42", 200, tc.body)
			if !checked || total != len(tc.want) {
				t.Fatalf("checked=%v total=%d got=%v, want %v", checked, total, got, tc.want)
			}
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidateNeverHoldsAValueOfTheResponse(t *testing.T) {
	d := parseShop(t)
	got, _, _ := validate(t, d, "GET", "/orders/42", 200,
		`{"id":"SECRET-ID","status":"SECRET-STATUS","items":[{"sku":123,"qty":"SECRET-QTY"}],"surprise-SECRET-KEY":1,"label":"SECRET"}`)
	if len(got) == 0 {
		t.Fatal("no misfit")
	}
	for _, line := range got {
		if strings.Contains(line, "SECRET") {
			t.Errorf("a misfit holds a value or a key of the response: %s", line)
		}
	}
}

func TestValidateKeepsAtMostLimitAndCountsAll(t *testing.T) {
	d := parseShop(t)
	m := d.MatchPath("GET", "/things")
	misfits, total, checked := d.Validate(m.Operation, 200, []byte(`["a","b","c","d","e","f","g","h"]`), 5)
	if !checked || len(misfits) != 5 || total != 8 {
		t.Fatalf("kept %d of %d, checked=%v", len(misfits), total, checked)
	}
}

func TestValidateUsesRangeAndDefaultSchemas(t *testing.T) {
	d := parseShop(t)
	if got, total, checked := validate(t, d, "GET", "/orders/42", 404, `{"code":1}`); !checked || total != 1 {
		t.Errorf("4XX schema: checked=%v total=%d %v", checked, total, got)
	}
	if _, total, checked := validate(t, d, "GET", "/things", 500, `[1,2]`); !checked || total != 0 {
		t.Errorf("default schema: checked=%v total=%d", checked, total)
	}
}

func TestValidateNothingWhenThereIsNoSchema(t *testing.T) {
	d := parseShop(t)
	m := d.MatchPath("GET", "/orders/42")
	if _, _, checked := d.Validate(m.Operation, 201, []byte(`{}`), 5); checked {
		t.Error("a status with no schema was checked")
	}
}

func TestValidateBodyThatIsNotJSON(t *testing.T) {
	d := parseShop(t)
	for body, got := range map[string]string{"": "empty", "<html>": "text"} {
		m, _, _ := validate(t, d, "GET", "/orders/42", 200, body)
		if len(m) != 1 || !strings.HasSuffix(m[0], "|"+got) {
			t.Errorf("body %q: %v", body, m)
		}
	}
}

func TestValidateReadsSwagger2AndJSONDocuments(t *testing.T) {
	doc := `{"swagger":"2.0","basePath":"/v1","paths":{"/pets":{"get":{"responses":{"200":{"description":"ok",
	  "schema":{"$ref":"#/definitions/Pet"}}}}}},"definitions":{"Pet":{"type":"object","required":["name"],
	  "properties":{"name":{"type":"string"},"age":{"type":"integer","x-nullable":true}}}}}`
	d, err := openapi.Parse([]byte(doc), "pets.json")
	if err != nil {
		t.Fatal(err)
	}
	if got, total, _ := validate(t, d, "GET", "/v1/pets", 200, `{"name":"a","age":null}`); total != 0 {
		t.Errorf("x-nullable: %v", got)
	}
	if got, _, _ := validate(t, d, "GET", "/pets", 200, `{"age":"1"}`); len(got) != 2 {
		t.Errorf("got %v", got)
	}
}

func TestMatchPathPrefersALiteralSegment(t *testing.T) {
	d := parseShop(t)
	if m := d.MatchPath("GET", "/orders/new"); m.Operation.Path != "/orders/new" {
		t.Errorf("matched %s", m.Operation.Path)
	}
	if m := d.MatchPath("GET", "/orders/42"); m.Operation.Path != "/orders/{id}" {
		t.Errorf("matched %s", m.Operation.Path)
	}
	if m := d.MatchPath("POST", "/orders/42"); m.Found || !m.PathKnown {
		t.Errorf("a method that the document lacks: %+v", m)
	}
	if m := d.MatchPath("GET", "/nothing"); m.PathKnown {
		t.Error("an unknown path was found")
	}
}
