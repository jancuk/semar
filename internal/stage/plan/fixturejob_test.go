package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/knowledge"
	"github.com/jancuk/semar/internal/openapi"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/fake"
)

const refundedFixtureAnswer = `{"fixture":{
 "create":[
  {"operation":"createOrder","path":[],"query":[],"body":[{"field":"customer","value":"semar-{{run.id}}"}],"captures":[{"name":"id","jsonpath":"$.data.id"}]},
  {"operation":"post_v1_orders_orderId_refunds","path":[{"name":"orderId","value":"{{id}}"}],"query":[],"body":[{"field":"amount","value":40000}],"captures":[]}],
 "outputs":["id"],"cleanup":[]}}`

func refundedNeed() FixtureNeed {
	return FixtureNeed{ID: "order.refunded", State: "An order that has been refunded in part", Outputs: []string{"id"}, Cites: []string{ticket482 + ":9"}, Tickets: []string{"SHOP-482"}}
}

func fixturePacket(t *testing.T) (Input, FixturePacket) {
	t.Helper()
	in, p := firstPacket(t)
	return in, NewFixturePacket(in, refundedNeed(), p.Operations, "v1")
}

func decodeFixture(t *testing.T, text string) FixtureAnswer {
	t.Helper()
	a, err := DecodeFixtureAnswer([]byte(text))
	if err != nil {
		t.Fatalf("DecodeFixtureAnswer: %v\n%s", err, text)
	}
	return a
}

func TestAFixtureAnswerBecomesAFixtureFile(t *testing.T) {
	in, fp := fixturePacket(t)
	fx, ps := fp.Convert(decodeFixture(t, refundedFixtureAnswer), in)
	if len(ps) > 0 {
		t.Fatalf("problems:\n%s", messages(ps))
	}
	if fx.ID != "order.refunded" || fx.Owner != "semar" || fx.Version != "1.0.0" || fx.APIVersion != "semar/v1" || fx.Kind != "Fixture" ||
		fx.Description != "An order that has been refunded in part" || len(fx.Outputs) != 1 || len(fx.Create) != 2 || len(fx.Cleanup) != 0 {
		t.Fatalf("fixture = %+v", fx)
	}
	first, second := fx.Create[0], fx.Create[1]
	if first.Request.Service != "shop" || first.Request.Method != "POST" || first.Request.Path != "/v1/orders" ||
		first.Request.JSON.(map[string]any)["customer"] != "semar-{{run.id}}" || first.Captures[0] != (domain.Capture{Name: "id", JSONPath: "$.data.id"}) {
		t.Errorf("first step = %+v", first)
	}
	if second.Request.Path != "/v1/orders/{{id}}/refunds" || second.Request.JSON.(map[string]any)["amount"] != int64(40000) {
		t.Errorf("second step = %+v", second.Request)
	}
	if got := fixture.Validate(fx, in.Services); len(got) > 0 {
		t.Errorf("the fixture loader refuses it: %v", got)
	}
}

func TestAFixtureAnswerIsCheckedWithTheChecksOfACriterionAndTheFixtureLoader(t *testing.T) {
	in, fp := fixturePacket(t)
	change := func(from, to string) string { return strings.Replace(refundedFixtureAnswer, from, to, 1) }
	for name, tc := range map[string]struct{ answer, want string }{
		"an operation that is not listed": {change(`"createOrder"`, `"deleteEverything"`), "the operation \"deleteEverything\" is not one of the operations"},
		"a field that does not exist":     {change(`"customer"`, `"custmer"`), "field custmer does not exist in createOrder; its fields are customer"},
		"a required field left out":       {change(`"body":[{"field":"customer","value":"semar-{{run.id}}"}]`, `"body":[]`), "field customer is required by createOrder and is missing"},
		"a literal ID":                    {change(`"{{id}}"`, `"o_1"`), "path parameter orderId must be written as {{name}}"},
		"a variable that nothing gives":   {change(`"{{id}}"`, `"{{order}}"`), "{{order}} is not a parameter, run.id, or a capture of an earlier create step"},
		"an output that is not captured":  {change(`"outputs":["id"]`, `"outputs":["order"]`), "output order is not captured by any create step"},
		"no create step":                  {`{"fixture":{"create":[],"outputs":["id"],"cleanup":[]}}`, "a fixture has 1 to 16 create steps"},
		"a capture the response lacks":    {change(`"$.data.id"`, `"$.data.ident"`), "which no response of createOrder declares"},
	} {
		t.Run(name, func(t *testing.T) {
			a, err := DecodeFixtureAnswer([]byte(tc.answer))
			if err != nil {
				t.Fatal(err)
			}
			_, ps := fp.Convert(a, in)
			if !strings.Contains(messages(ps), tc.want) {
				t.Errorf("problems = %q, want one that holds %q", messages(ps), tc.want)
			}
		})
	}
}

func TestTheFixtureSchemaOffersOnlyTheOperationsOfItsPacket(t *testing.T) {
	_, fp := fixturePacket(t)
	s := string(fp.Schema())
	for _, want := range []string{`"createOrder"`, `"post_v1_orders_orderId_refunds"`, `"create"`, `"outputs"`, `"cleanup"`} {
		if !strings.Contains(s, want) {
			t.Errorf("the schema lacks %s", want)
		}
	}
	for _, not := range []string{`"find"`, `"until"`, `"expect"`} {
		if strings.Contains(s, not) {
			t.Errorf("the schema offers %s, which a drafted fixture does not have", not)
		}
	}
}

func stepAccounts(t *testing.T, fp FixturePacket) []any {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(fp.Schema(), &schema); err != nil {
		t.Fatal(err)
	}
	fx := schema["properties"].(map[string]any)["fixture"].(map[string]any)["properties"].(map[string]any)
	var out []any
	for _, phase := range []string{"create", "cleanup"} {
		step := fx[phase].(map[string]any)["items"].(map[string]any)
		if branches, ok := step["anyOf"].([]any); ok {
			step = branches[0].(map[string]any)
		}
		as, ok := step["properties"].(map[string]any)["as"].(map[string]any)
		if !ok {
			return nil
		}
		out = append(out, as["enum"])
	}
	return out
}

func TestEachStepOfTheFixtureSchemaChoosesAnAccount(t *testing.T) {
	_, fp := fixturePacket(t)
	got := stepAccounts(t, fp)
	if len(got) != 2 || !reflect.DeepEqual(got[0], []any{"default", "bob"}) || !reflect.DeepEqual(got[1], []any{"default", "bob"}) {
		t.Errorf("the accounts of a create step and of a cleanup step = %v", got)
	}
	fp.Accounts = fp.Accounts[:1]
	if got := stepAccounts(t, fp); got != nil {
		t.Errorf("one account is no choice, and the schema offers %v", got)
	}
}

func TestTheFixturePromptListsTheAccountsAndSaysWhenToActAsOne(t *testing.T) {
	_, fp := fixturePacket(t)
	text, err := fp.Prompt(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"- `bob`: A customer with no refund rights.", "- `default`: A support agent.", "acts as that account"} {
		if !strings.Contains(text, want) {
			t.Errorf("the prompt lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "cannot send a request as another account") {
		t.Errorf("the prompt still says that a fixture cannot act as another account:\n%s", text)
	}
	fp.Accounts = fp.Accounts[:1]
	text, _ = fp.Prompt(nil, nil)
	if strings.Contains(text, "acts as that account") || strings.Contains(text, "- `default`:") {
		t.Errorf("one account is no choice, and the prompt offers it:\n%s", text)
	}
}

func TestAStepThatActsAsAnotherAccountKeepsItAndTheDefaultIsLeftOut(t *testing.T) {
	in, fp := fixturePacket(t)
	answer := strings.Replace(refundedFixtureAnswer, `{"operation":"createOrder",`, `{"as":"bob","operation":"createOrder",`, 1)
	answer = strings.Replace(answer, `{"operation":"post_v1_orders_orderId_refunds",`, `{"as":"default","operation":"post_v1_orders_orderId_refunds",`, 1)
	fx, ps := fp.Convert(decodeFixture(t, answer), in)
	if len(ps) > 0 {
		t.Fatalf("problems:\n%s", messages(ps))
	}
	if fx.Create[0].As != "bob" || fx.Create[1].As != "" {
		t.Errorf("as = %q, %q; want bob, and none for the default account", fx.Create[0].As, fx.Create[1].As)
	}
	data, err := fixture.Marshal(fx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "as:") != 1 || !strings.Contains(string(data), "as: bob") {
		t.Errorf("the file:\n%s", data)
	}
	bad := strings.Replace(refundedFixtureAnswer, `{"operation":"createOrder",`, `{"as":"carol","operation":"createOrder",`, 1)
	if _, ps := fp.Convert(decodeFixture(t, bad), in); !strings.Contains(messages(ps), "carol is not an auth profile of the configuration: use one of bob, default") {
		t.Errorf("problems = %q", messages(ps))
	}
}

func TestTheFixturePromptHoldsTheStateTheOperationsAndTheAccount(t *testing.T) {
	_, fp := fixturePacket(t)
	text, err := fp.Prompt(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"order.refunded", "An order that has been refunded in part", "`createOrder`", "`default`", "You write one fixture"} {
		if !strings.Contains(text, want) {
			t.Errorf("the prompt lacks %q:\n%s", want, text)
		}
	}
	text, _ = fp.Prompt([]byte(`{"fixture":{}}`), []Problem{{Path: "create[0]", Message: "field custmer does not exist"}})
	if !strings.Contains(text, "Your previous answer was rejected") || !strings.Contains(text, "field custmer does not exist") {
		t.Errorf("the correction prompt lacks the problems:\n%s", text)
	}
}

// fixtureScript answers each job of a plan that needs a fixture: a criterion needs order.refunded
// until the fixture is listed, a fixture job gets the next of its answers, and a criterion that sees
// the fixture writes a scenario that names it.
type fixtureScript struct {
	mu       sync.Mutex
	fixtures []string // the answers to fixture jobs, in turn
	prompts  []string
	jobs     []provider.Job
}

const scenarioWithRefundedFixture = `{"answer":{"kind":"scenario","title":"A refund above the total is refused","intent":"Before: x. Now: y.",
 "given":[{"fixture":"order.refunded","as":"order","with":[]}],
 "steps":[{"id":"refund","setup":false,"as":"default","action":"Refund","operation":"post_v1_orders_orderId_refunds",
  "path":[{"name":"orderId","value":"{{order.id}}"}],"query":[],"body":[{"field":"amount","value":150000}],"captures":[],
  "expect":[{"subject":"status","name":"","operator":"equals","value":422}]}]}}`

func (s *fixtureScript) fake(id string) *fake.Fake {
	return fake.NewFunc(id, func(j provider.Job) (provider.Result, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.prompts = append(s.prompts, j.Prompt)
		s.jobs = append(s.jobs, j)
		var out string
		switch {
		case strings.Contains(j.Prompt, "You write one fixture"):
			out = s.fixtures[0]
			if len(s.fixtures) > 1 {
				s.fixtures = s.fixtures[1:]
			}
		case strings.Contains(j.Prompt, ticket482+":9\n") && strings.Contains(j.Prompt, "- `order.refunded`"):
			out = scenarioWithRefundedFixture
		case strings.Contains(j.Prompt, ticket482+":9\n"):
			out = needAnswer("order.refunded", "An order that has been refunded in part", "id")
		case strings.Contains(j.Prompt, ticket482+":10\n"):
			out = refundAnswer("A partial refund is accepted", 40000, 201, "amount")
		default:
			out = manualAnswer
		}
		return provider.Result{Output: json.RawMessage(out), Model: j.Model, Turns: 1, InputTokens: 100, OutputTokens: 10, CostUSD: 0.01}, nil
	})
}

func TestAFixtureIsDraftedAndTheCriterionIsPlannedAgainWithIt(t *testing.T) {
	in := shopInput(t)
	s := &fixtureScript{fixtures: []string{refundedFixtureAnswer}}
	in.Ladder = ladder(Rung{Planner: s.fake("p"), Model: "haiku"})
	in.JobDir = t.TempDir()
	out, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.FixtureDrafts) != 1 {
		t.Fatalf("drafts = %+v", out.FixtureDrafts)
	}
	d := out.FixtureDrafts[0]
	if !d.Drafted || d.Fixture.ID != "order.refunded" || d.Rung != 0 || d.Corrections != 0 || len(d.Need.Cites) != 1 {
		t.Errorf("draft = %+v", d)
	}
	if len(out.Scenarios) != 2 || len(out.Draft.ManualChecks) != 1 {
		t.Fatalf("scenarios %d, manual checks %d", len(out.Scenarios), len(out.Draft.ManualChecks))
	}
	sc := out.Scenarios[0]
	if len(sc.Given) != 1 || sc.Given[0].Fixture != "order.refunded" || sc.Given[0].As != "order" {
		t.Errorf("the scenario does not name the drafted fixture: %+v", sc.Given)
	}
	for _, nd := range out.Draft.NotDrafted {
		t.Errorf("a criterion stays a gap: %+v", nd)
	}
	if len(s.jobs) != 5 {
		t.Errorf("jobs = %d, want 3 criteria, 1 fixture, and 1 second pass", len(s.jobs))
	}
	for _, j := range s.jobs {
		if strings.Contains(j.Prompt, "You write one fixture") && (!j.NoTools || j.Workdir != in.JobDir || len(j.Schema) == 0) {
			t.Errorf("the fixture job = %+v", j)
		}
	}
	if st := out.Pipeline; st.FixtureJobs != 1 || st.FixturesDrafted != 1 {
		t.Errorf("stats = %+v", st)
	}
}

func TestAFixtureJobIsCorrectedAndThenGoesUpTheLadder(t *testing.T) {
	in := shopInput(t)
	bad := strings.Replace(refundedFixtureAnswer, `"customer"`, `"custmer"`, 1)
	s := &fixtureScript{fixtures: []string{bad, bad, refundedFixtureAnswer}}
	in.Ladder = ladder(Rung{Planner: s.fake("p"), Model: "haiku"}, Rung{Planner: s.fake("q"), Model: "sonnet"})
	out, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	d := out.FixtureDrafts[0]
	if !d.Drafted || d.Rung != 1 || d.Corrections != 1 {
		t.Fatalf("draft = %+v", d)
	}
	if !strings.Contains(messages(d.Returned), "field custmer does not exist in createOrder") {
		t.Errorf("the problem that went back = %q", messages(d.Returned))
	}
}

func TestAFixtureNoModelCanWriteLeavesItsCriterionNotDrafted(t *testing.T) {
	in := shopInput(t)
	bad := strings.Replace(refundedFixtureAnswer, `"customer"`, `"custmer"`, 1)
	s := &fixtureScript{fixtures: []string{bad}}
	in.Ladder = ladder(Rung{Planner: s.fake("p"), Model: "haiku"})
	out, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	d := out.FixtureDrafts[0]
	if d.Drafted || len(d.Problems) == 0 {
		t.Fatalf("draft = %+v", d)
	}
	if len(out.Draft.NotDrafted) != 1 || !strings.Contains(out.Draft.NotDrafted[0].Needs, "fixture order.refunded") ||
		!strings.Contains(out.Draft.NotDrafted[0].Needs, "was not drafted: create[0].body[0]: field custmer does not exist") {
		t.Errorf("not drafted = %+v", out.Draft.NotDrafted)
	}
	if len(out.Unaccounted) != 0 {
		t.Errorf("the criterion is accounted for as a gap: %v", out.Unaccounted)
	}
}

type trialScript struct {
	mu    sync.Mutex
	runs  int
	steps []FixtureTrial
}

func (t *trialScript) TryFixture(_ context.Context, fx domain.Fixture) (FixtureTrial, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.steps[min(t.runs, len(t.steps)-1)]
	t.runs++
	return r, nil
}

func TestAFixtureThatFailsItsTrialGoesBackToTheModelWithTheProblem(t *testing.T) {
	in := shopInput(t)
	s := &fixtureScript{fixtures: []string{refundedFixtureAnswer}}
	tr := &trialScript{steps: []FixtureTrial{
		{Ran: true, Problems: []string{"trial: create[0] POST /v1/orders answered 400"}},
		{Ran: true, Requests: 2, CleanedUp: true},
	}}
	in.Ladder = ladder(Rung{Planner: s.fake("p"), Model: "haiku"})
	in.FixtureTrial = tr
	out, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	d := out.FixtureDrafts[0]
	if !d.Drafted || d.Corrections != 1 || tr.runs != 2 || !d.Trial.Ran || d.Trial.Requests != 2 {
		t.Fatalf("draft = %+v, trial runs %d", d, tr.runs)
	}
	correction := ""
	for _, p := range s.prompts {
		if strings.Contains(p, "You write one fixture") && strings.Contains(p, "Your previous answer was rejected") {
			correction = p
		}
	}
	if !strings.Contains(correction, "trial: create[0] POST /v1/orders answered 400") {
		t.Errorf("the correction does not hold the problem of the trial:\n%s", correction)
	}
}

func TestATrialThatCannotRunLeavesTheFixtureDraftedAndSaysWhy(t *testing.T) {
	in := shopInput(t)
	s := &fixtureScript{fixtures: []string{refundedFixtureAnswer}}
	in.Ladder = ladder(Rung{Planner: s.fake("p"), Model: "haiku"})
	in.FixtureTrial = &trialScript{steps: []FixtureTrial{{Skipped: "uat allows only reads"}}}
	out, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	d := out.FixtureDrafts[0]
	if !d.Drafted || d.Trial.Ran || d.Trial.Skipped != "uat allows only reads" {
		t.Errorf("draft = %+v", d)
	}
}

func TestOnlyTheFirstFiveNeedsAreDrafted(t *testing.T) {
	var needs []FixtureNeed
	for i := range 7 {
		needs = append(needs, FixtureNeed{ID: fmt.Sprintf("f.n%d", i)})
	}
	kept, left := capNeeds(needs)
	if len(kept) != MaxFixtureDrafts || len(left) != 2 || left[0].ID != "f.n5" {
		t.Errorf("kept %d, left %v", len(kept), left)
	}
}

func bigDocument(n int) string {
	var b strings.Builder
	b.WriteString("openapi: 3.0.3\npaths:\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "  /v1/thing%d:\n    post:\n      operationId: makeThing%d\n      responses:\n        \"201\": {description: made}\n", i, i)
	}
	return b.String()
}

func TestAFixtureSeesTheOperationsOfTheDocumentsAndNotOnlyThoseOfItsCriteria(t *testing.T) {
	in := shopInput(t)
	doc, err := openapi.Parse([]byte(bigDocument(60)), "docs/big.yaml")
	if err != nil {
		t.Fatal(err)
	}
	in.Knowledge = []knowledge.Source{{ID: "api", Kind: config.KnowledgeOpenAPI, Path: "docs/big.yaml", Files: 1, API: &doc}}
	in.Methods = []string{"POST"}
	first := []PacketOperation{{Key: "makeThing30", Method: "POST", Route: "/v1/thing30"}}

	ops := fixtureOperations(in, first)
	if len(ops) != maxFixtureOperations || ops[0].Key != "makeThing30" {
		t.Fatalf("got %d operations, the first %q; want %d, led by the one of the criterion", len(ops), ops[0].Key, maxFixtureOperations)
	}
	seen := map[string]bool{}
	for _, op := range ops {
		if seen[op.Key] {
			t.Errorf("operation %s is listed twice", op.Key)
		}
		seen[op.Key] = true
	}

	in.Methods = []string{"GET"}
	if got := fixtureOperations(in, nil); len(got) != 0 {
		t.Errorf("an operation with a method that the environment refuses is offered: %d", len(got))
	}
}

func TestACaptureOfAWholeObjectIsAProblemInAFixtureAndInAScenario(t *testing.T) {
	in, fp := fixturePacket(t)
	whole := strings.Replace(refundedFixtureAnswer, `"$.data.id"`, `"$.data"`, 1)
	_, ps := fp.Convert(decodeFixture(t, whole), in)
	want := "capture id reads $.data, which is an object, and a capture holds one value: read a field of it, such as $.data.id"
	if !strings.Contains(messages(ps), want) {
		t.Errorf("problems = %q, want one that holds %q", messages(ps), want)
	}

	in2, p := firstPacket(t)
	a := withStep(t, func(st map[string]any) {
		st["captures"] = []any{map[string]any{"name": "order", "jsonpath": "$.data"}}
	})
	_, ps = p.Convert(a, in2)
	if !strings.Contains(messages(ps), "capture order reads $.data, which is an object") {
		t.Errorf("a scenario step may capture an object: %q", messages(ps))
	}
}

func TestTheFixturePromptHasNoDoubleBlankLine(t *testing.T) {
	_, fp := fixturePacket(t)
	for _, accounts := range [][]Account{fp.Accounts, fp.Accounts[:1]} {
		fp.Accounts = accounts
		text, err := fp.Prompt(nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "\n\n\n") {
			t.Errorf("the prompt holds two blank lines in a row:\n%s", text)
		}
	}
}
