package plan

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/openapi"
)

func existingWithTitle(ticket, id, title string) map[string][]domain.Scenario {
	return map[string][]domain.Scenario{ticket: {{ID: domain.ScenarioID(id), Title: title}}}
}

const refundOp = "post_v1_orders_orderId_refunds"

// goodAnswer is the answer to criterion 1 of the shop ticket.
const goodAnswer = `{"answer":{"kind":"scenario","title":"A refund above the total is refused","intent":"Before: a refund above the total was accepted. Now: it is refused with 422.",
 "given":[{"fixture":"order.paid","as":"order","with":[{"name":"customer","value":"c-1"}]}],
 "steps":[
  {"id":"refund-too-much","setup":false,"as":"default","action":"Refund more than the total","operation":"post_v1_orders_orderId_refunds",
   "path":[{"name":"orderId","value":"{{order.order}}"}],"query":[],"body":[{"field":"amount","value":150000}],"captures":[],
   "expect":[{"subject":"status","name":"","operator":"equals","value":422},{"subject":"jsonpath","name":"$.error.code","operator":"equals","value":"REFUND_EXCEEDS_TOTAL"}]},
  {"id":"order-unchanged","setup":false,"as":"default","action":"Read the order again","operation":"getOrder",
   "path":[{"name":"orderId","value":"{{order.order}}"}],"query":[],"body":[],"captures":[],
   "expect":[{"subject":"jsonpath","name":"$.data.refundedTotal","operator":"equals","value":0}]}
 ]}}`

func answerOf(t *testing.T, text string) Answer {
	t.Helper()
	a, err := DecodeAnswer([]byte(text))
	if err != nil {
		t.Fatalf("DecodeAnswer: %v\n%s", err, text)
	}
	return a
}

func firstPacket(t *testing.T) (Input, Packet) {
	t.Helper()
	in := shopInput(t)
	return in, Packets(in, "v1")[0]
}

// withStep rewrites the first step of goodAnswer.
func withStep(t *testing.T, change func(st map[string]any)) Answer {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(goodAnswer), &raw); err != nil {
		t.Fatal(err)
	}
	steps := raw["answer"].(map[string]any)["steps"].([]any)
	change(steps[0].(map[string]any))
	data, _ := json.Marshal(raw)
	return answerOf(t, string(data))
}

func messages(ps []Problem) string {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString(p.Path + ": " + p.Message + "\n")
	}
	return b.String()
}

func TestConvertTakesMethodHintAndPathFromTheOperation(t *testing.T) {
	in, p := firstPacket(t)
	res, ps := p.Convert(answerOf(t, goodAnswer), in)
	if len(ps) > 0 {
		t.Fatalf("problems:\n%s", messages(ps))
	}
	sd := res.Scenario
	if sd == nil || sd.Ticket != "SHOP-482" || len(sd.Steps) != 2 || len(sd.Given) != 1 {
		t.Fatalf("scenario = %+v", sd)
	}
	st := sd.Steps[0]
	if st.Method != "POST" || st.Hint != "/v1/orders/{orderId}/refunds" || st.Path != "/v1/orders/{{order.order}}/refunds" {
		t.Errorf("method %q, hint %q, path %q", st.Method, st.Hint, st.Path)
	}
	if st.As != "" || st.Service != "" {
		t.Errorf("the default account and the only service are left out: as %q, service %q", st.As, st.Service)
	}
	body, _ := st.JSON.(map[string]any)
	if n, ok := body["amount"].(json.Number); !ok || n.String() != "150000" || len(body) != 1 {
		t.Errorf("body = %#v", st.JSON)
	}
	wantWhy := ticket482 + ":9 A refund above the order total answers 422 with the error code REFUND_EXCEEDS_TOTAL, and the order stays as it was"
	if len(st.Expect) != 2 || st.Expect[0].Source != "ticket" || st.Expect[0].Why != p.Why() || !strings.HasPrefix(st.Expect[0].Why, ticket482+":9 ") {
		t.Errorf("expect = %+v, want the cite of the criterion, fixed by code (e.g. %s)", st.Expect, wantWhy)
	}
	if st.Expect[0].Check.Status == nil || *st.Expect[0].Check.Status != 422 || st.Expect[1].Check.JSONPath != "$.error.code" || st.Expect[1].Check.Equals != "REFUND_EXCEEDS_TOTAL" {
		t.Errorf("checks = %+v", st.Expect)
	}
	if g := sd.Given[0]; g.Fixture != "order.paid" || g.As != "order" || g.With["customer"] != "c-1" {
		t.Errorf("given = %+v", g)
	}
	if got := sd.Steps[1]; got.Method != "GET" || got.Hint != "/v1/orders/{orderId}" || got.Path != "/v1/orders/{{order.order}}" {
		t.Errorf("second step = %+v", got)
	}
}

func TestConvertActsAsAnotherAccountAndNamesTheService(t *testing.T) {
	in, p := firstPacket(t)
	in.Services = []string{"shop", "audit"}
	a := withStep(t, func(st map[string]any) { st["as"] = "bob" })
	res, ps := p.Convert(a, in)
	if len(ps) > 0 {
		t.Fatalf("problems:\n%s", messages(ps))
	}
	if st := res.Scenario.Steps[0]; st.As != "bob" || st.Service != "shop" {
		t.Errorf("as %q, service %q", st.As, st.Service)
	}
}

func TestConvertSaysInPlainWordsWhatIsWrong(t *testing.T) {
	in, p := firstPacket(t)
	for name, tc := range map[string]struct {
		change func(st map[string]any)
		want   string
	}{
		"a body field that does not exist": {func(st map[string]any) {
			st["body"] = []any{map[string]any{"field": "amountt", "value": 1}, map[string]any{"field": "amount", "value": 5}}
		}, "field amountt does not exist in " + refundOp + "; its fields are amount, reason"},
		"a required field is missing": {func(st map[string]any) {
			st["body"] = []any{map[string]any{"field": "reason", "value": "damaged"}}
		}, "field amount is required by " + refundOp + " and is missing"},
		"a body with no field at all": {func(st map[string]any) { st["body"] = []any{} },
			"field amount is required by " + refundOp + " and is missing"},
		"a value of the wrong type": {func(st map[string]any) {
			st["body"] = []any{map[string]any{"field": "amount", "value": "a lot"}}
		}, "field amount of " + refundOp + " is an integer"},
		"a fraction for an integer": {func(st map[string]any) {
			st["body"] = []any{map[string]any{"field": "amount", "value": 1.5}}
		}, "1.5 is not a whole number"},
		"a path parameter that does not exist": {func(st map[string]any) {
			st["path"] = []any{map[string]any{"name": "id", "value": "{{order.order}}"}}
		}, "path parameter id does not exist in " + refundOp + "; its path parameters are orderId"},
		"a path parameter that is missing": {func(st map[string]any) { st["path"] = []any{} },
			"path parameter orderId of " + refundOp + " is missing"},
		"an ID that nothing gave": {func(st map[string]any) {
			st["path"] = []any{map[string]any{"name": "orderId", "value": "o_1"}}
		}, `the literal "o_1" is an ID that no step or fixture gave`},
		"a query parameter that does not exist": {func(st map[string]any) {
			st["query"] = []any{map[string]any{"name": "page", "value": "1"}}
		}, "query parameter page does not exist in " + refundOp + "; it has no query parameters"},
		"an operation outside the packet": {func(st map[string]any) { st["operation"] = "deleteOrder" },
			`the operation "deleteOrder" is not one of the operations of this criterion: ` + refundOp},
		"a response field that does not exist": {func(st map[string]any) {
			st["expect"] = []any{map[string]any{"subject": "jsonpath", "name": "$.error.codee", "operator": "equals", "value": "x"}}
		}, "$.error.codee, which no response of " + refundOp + " declares; its response fields are $.data"},
		"a capture of a field that does not exist": {func(st map[string]any) {
			st["captures"] = []any{map[string]any{"name": "id", "jsonpath": "$.data.idd"}}
		}, "capture id reads $.data.idd, which no response"},
		"a setup step with a result": {func(st map[string]any) { st["setup"] = true },
			"step refund-too-much is a setup step and has expected results"},
		"a step with no result": {func(st map[string]any) { st["expect"] = []any{} },
			"step refund-too-much has no expected result"},
		"a status that is not a number": {func(st map[string]any) {
			st["expect"] = []any{map[string]any{"subject": "status", "name": "", "operator": "equals", "value": "422"}}
		}, "the status must be a whole number from 100 to 599"},
		"null for equals": {func(st map[string]any) {
			st["expect"] = []any{map[string]any{"subject": "jsonpath", "name": "$.error.code", "operator": "equals", "value": nil}}
		}, "cannot compare with null"},
	} {
		t.Run(name, func(t *testing.T) {
			_, ps := p.Convert(withStep(t, tc.change), in)
			if got := messages(ps); !strings.Contains(got, tc.want) {
				t.Errorf("problems:\n%swant one with %q", got, tc.want)
			}
		})
	}
}

func TestConvertAManualCheckAndANotDraftedEntry(t *testing.T) {
	in, p := firstPacket(t)
	manual := answerOf(t, `{"answer":{"kind":"manualCheck","title":"The order page shows the refund","manualSteps":["Open the order","Read the history"],"expected":"The refund is in the history"}}`)
	res, ps := p.Convert(manual, in)
	if len(ps) > 0 || res.Manual == nil || res.Manual.Cite != p.Why() || res.Manual.Ticket != "SHOP-482" || len(res.Manual.Steps) != 2 {
		t.Errorf("manual = %+v, problems %s", res.Manual, messages(ps))
	}
	if _, ps := p.Convert(answerOf(t, `{"answer":{"kind":"manualCheck","title":"","manualSteps":[],"expected":""}}`), in); len(ps) != 3 {
		t.Errorf("problems:\n%s", messages(ps))
	}
	nd := answerOf(t, `{"answer":{"kind":"notDrafted","what":"the refund of an order of another shop","needs":"fixture order.other"}}`)
	res, ps = p.Convert(nd, in)
	if len(ps) > 0 || res.NotDrafted == nil || res.NotDrafted.Cite != p.Why() || res.NotDrafted.Needs != "fixture order.other" {
		t.Errorf("notDrafted = %+v, problems %s", res.NotDrafted, messages(ps))
	}
}

func TestCiteIsTheCriterionLineAndItsFirstSentence(t *testing.T) {
	_, p := firstPacket(t)
	p.Criterion = "A card moves. Then it stays."
	if got := p.Why(); got != ticket482+":9 A card moves" {
		t.Errorf("why = %q", got)
	}
}

func TestDecodeAnswerIsStrict(t *testing.T) {
	for name, text := range map[string]string{
		"an unknown field":   `{"answer":{"kind":"notDrafted","what":"w","needs":"n","method":"GET"}}`,
		"an unknown kind":    `{"answer":{"kind":"scenarios"}}`,
		"text after":         `{"answer":{"kind":"notDrafted"}} {}`,
		"no answer":          `{"kind":"notDrafted"}`,
		"not an object":      `[]`,
		"a method of a step": `{"answer":{"kind":"scenario","steps":[{"id":"a","method":"GET"}]}}`,
	} {
		if _, err := DecodeAnswer([]byte(text)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestCheckAnswerRunsTheRulesOfTheWholeDraft(t *testing.T) {
	in, p := firstPacket(t)
	res, ps := CheckAnswer(in, p, answerOf(t, goodAnswer))
	if len(ps) > 0 || res.Scenario == nil {
		t.Fatalf("problems:\n%s", messages(ps))
	}

	// A variable that nothing defines is a problem of the scenario, and it names the step.
	bad := withStep(t, func(st map[string]any) {
		st["path"] = []any{map[string]any{"name": "orderId", "value": "{{ordr.order}}"}}
	})
	if _, ps := CheckAnswer(in, p, bad); len(ps) == 0 || !strings.Contains(messages(ps), "ordr") {
		t.Errorf("an unknown variable gave %q", messages(ps))
	}

	// The title of a scenario that exists already is refused, in the words of the answer.
	in2, p2 := firstPacket(t)
	in2.Existing = existingWithTitle("SHOP-482", "SHOP-482-01", "A refund above the total is refused")
	_, ps = CheckAnswer(in2, p2, answerOf(t, goodAnswer))
	if got := messages(ps); !strings.Contains(got, "title: title") || !strings.Contains(got, "repeats a scenario") {
		t.Errorf("a repeated title gave %q", got)
	}

	// A fixture that does not exist.
	in3, p3 := firstPacket(t)
	nofix := strings.Replace(goodAnswer, `"fixture":"order.paid"`, `"fixture":"order.gold"`, 1)
	if _, ps := CheckAnswer(in3, p3, answerOf(t, nofix)); len(ps) == 0 {
		t.Error("an unknown fixture was accepted")
	}
}

// walkSchema calls visit for every schema node below root.
func walkSchema(root any, visit func(node map[string]any)) {
	switch x := root.(type) {
	case map[string]any:
		visit(x)
		for _, v := range x {
			walkSchema(v, visit)
		}
	case []any:
		for _, v := range x {
			walkSchema(v, visit)
		}
	}
}

func enumValues(node any) []string {
	m, _ := node.(map[string]any)
	list, _ := m["enum"].([]any)
	var out []string
	for _, v := range list {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestAnswerSchemaIsClosed(t *testing.T) {
	in, p := firstPacket(t)
	_ = in
	var root map[string]any
	if err := json.Unmarshal(p.AnswerSchema(), &root); err != nil {
		t.Fatal(err)
	}
	// Every harness accepts the same form: each object lists all its properties as required and
	// allows no other, and each value has a type or a list of choices.
	walkSchema(root, func(n map[string]any) {
		if n["type"] == "object" {
			props, _ := n["properties"].(map[string]any)
			var required []string
			for _, r := range n["required"].([]any) {
				required = append(required, r.(string))
			}
			var names []string
			for k := range props {
				names = append(names, k)
			}
			slices.Sort(names)
			slices.Sort(required)
			if n["additionalProperties"] != false || !slices.Equal(names, required) {
				t.Errorf("an object is open or has an optional property: %v required %v", names, required)
			}
		}
	})

	var ops, accounts, fixtures, bodyFields, kinds []string
	walkSchema(root, func(n map[string]any) {
		props, _ := n["properties"].(map[string]any)
		ops = append(ops, enumValues(props["operation"])...)
		fixtures = append(fixtures, enumValues(props["fixture"])...)
		bodyFields = append(bodyFields, enumValues(props["field"])...)
		kinds = append(kinds, enumValues(props["kind"])...)
	})
	defs, _ := root["$defs"].(map[string]any)
	accounts = enumValues(defs["account"])
	if want := []string{"post_v1_orders_orderId_refunds", "createOrder", "getOrder", "health"}; !slices.Equal(ops, want) {
		t.Errorf("operations = %v, want %v", ops, want)
	}
	if !slices.Contains(accounts, "bob") || !slices.Contains(accounts, "default") || !slices.Equal(fixtures, []string{"order.paid"}) {
		t.Errorf("accounts %v, fixtures %v", accounts, fixtures)
	}
	if !slices.Contains(bodyFields, "amount") || !slices.Contains(bodyFields, "customer") || slices.Contains(bodyFields, "listId") {
		t.Errorf("body fields = %v", bodyFields)
	}
	if !slices.Equal(kinds, []string{KindScenario, KindManualCheck, KindNotDrafted}) {
		t.Errorf("kinds = %v", kinds)
	}

	// The value of amount is an integer, and the value of reason is a string.
	found := map[string]any{}
	walkSchema(root, func(n map[string]any) {
		props, _ := n["properties"].(map[string]any)
		if vs := enumValues(props["field"]); len(vs) == 1 {
			found[vs[0]] = props["value"].(map[string]any)["type"]
		}
	})
	if found["amount"] != "integer" || found["reason"] != "string" {
		t.Errorf("value types = %v", found)
	}
}

func TestAnswerSchemaFollowsTheDocument(t *testing.T) {
	_, p := firstPacket(t)
	p.Operations = []PacketOperation{{
		Key: "patchCard", Method: "PATCH", Route: "/cards/{id}", BodyKnown: true,
		Params: nil,
		Body: []openapi.Field{
			{Name: "dueAt", Type: "string", Nullable: true},
			{Name: "state", Type: "string", Enum: []string{"open", "done"}},
			{Name: "labels", Type: "array"},
		},
	}}
	var root map[string]any
	if err := json.Unmarshal(p.AnswerSchema(), &root); err != nil {
		t.Fatal(err)
	}
	types := map[string]any{}
	walkSchema(root, func(n map[string]any) {
		props, _ := n["properties"].(map[string]any)
		if vs := enumValues(props["field"]); len(vs) == 1 {
			types[vs[0]] = props["value"]
		}
	})
	if got, _ := json.Marshal(types["dueAt"]); string(got) != `{"type":["string","null"]}` {
		t.Errorf("dueAt = %s: a nullable field accepts null", got)
	}
	if got, _ := json.Marshal(types["state"]); string(got) != `{"enum":["open","done"],"type":"string"}` {
		t.Errorf("state = %s", got)
	}
	if m, _ := types["labels"].(map[string]any); m["type"] != "string" {
		t.Errorf("an array is written as JSON text: %v", types["labels"])
	}
}
