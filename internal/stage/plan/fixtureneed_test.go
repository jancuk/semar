package plan

import (
	"context"
	"strings"
	"testing"
)

func needAnswer(id, state string, outputs ...string) string {
	list := `"` + strings.Join(outputs, `","`) + `"`
	if len(outputs) == 0 {
		list = ""
	}
	return `{"answer":{"kind":"needsFixture","what":"A refund of an order that has two refunds already","fixtureId":"` + id +
		`","fixtureState":"` + state + `","fixtureOutputs":[` + list + `]}}`
}

func TestAnAnswerCanNameTheFixtureItNeeds(t *testing.T) {
	in, p := firstPacket(t)
	res, ps := CheckAnswer(in, p, answerOf(t, needAnswer("order.refunded", "A paid order that has been refunded twice", "id")))
	if len(ps) > 0 {
		t.Fatalf("problems:\n%s", messages(ps))
	}
	nd := res.NotDrafted
	if nd == nil || !strings.HasPrefix(nd.Needs, "fixture order.refunded") || res.Need == nil {
		t.Fatalf("result = %+v", res)
	}
	if res.Need.ID != "order.refunded" || res.Need.State != "A paid order that has been refunded twice" || len(res.Need.Outputs) != 1 {
		t.Errorf("need = %+v", res.Need)
	}
}

func TestAFixtureNeedIsCheckedLikeAnyAnswer(t *testing.T) {
	in, p := firstPacket(t)
	for name, tc := range map[string]struct {
		answer string
		want   string
	}{
		"an ID of the wrong form": {needAnswer("Order Refunded", "x", "id"), "fixtureId"},
		"an ID the catalogue has": {needAnswer("order.paid", "x", "id"), "order.paid is in the catalogue already; name it in given"},
		"no state":                {needAnswer("order.refunded", " ", "id"), "fixtureState"},
		"no output":               {needAnswer("order.refunded", "x"), "fixtureOutputs"},
		"a bad output name":       {needAnswer("order.refunded", "x", "1id"), "fixtureOutputs[0]"},
		"a repeated output":       {needAnswer("order.refunded", "x", "id", "id"), "listed twice"},
	} {
		t.Run(name, func(t *testing.T) {
			_, ps := CheckAnswer(in, p, answerOf(t, tc.answer))
			if !strings.Contains(messages(ps), tc.want) {
				t.Errorf("problems = %q, want one that holds %q", messages(ps), tc.want)
			}
		})
	}
}

func TestTheAnswerSchemaOffersTheNeedsFixtureKind(t *testing.T) {
	_, p := firstPacket(t)
	s := string(p.AnswerSchema())
	for _, want := range []string{`"needsFixture"`, `"fixtureId"`, `"fixtureState"`, `"fixtureOutputs"`} {
		if !strings.Contains(s, want) {
			t.Errorf("the schema lacks %s", want)
		}
	}
}

func TestThePromptSaysWhenToNameAFixture(t *testing.T) {
	_, p := firstPacket(t)
	text, err := p.Prompt(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "`needsFixture`") || !strings.Contains(text, "fixtureState") {
		t.Errorf("the prompt does not explain needsFixture:\n%s", text)
	}
}

func TestThePlanCollectsEachDistinctFixtureOnce(t *testing.T) {
	in := shopInput(t)
	a := shopAnswers()
	a.byLine[":9"] = needAnswer("order.refunded", "A paid order that has been refunded twice", "id")
	a.byLine[":10"] = needAnswer("order.refunded", "An order with two refunds", "id", "refunds")
	a.byLine[":11"] = needAnswer("order.shared", "An order that a second account may read", "id")
	in.Ladder = ladder(Rung{Planner: a.fake("p"), Model: "haiku"})
	out, err := RunCriteria(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.FixtureNeeds) != 2 {
		t.Fatalf("needs = %+v", out.FixtureNeeds)
	}
	first := out.FixtureNeeds[0]
	if first.ID != "order.refunded" || first.State != "A paid order that has been refunded twice" ||
		strings.Join(first.Outputs, ",") != "id,refunds" || len(first.Cites) != 2 {
		t.Errorf("first need = %+v", first)
	}
	if got := out.FixtureNeeds[1]; got.ID != "order.shared" || len(got.Cites) != 1 {
		t.Errorf("second need = %+v", got)
	}
	if len(out.Draft.NotDrafted) != 3 {
		t.Errorf("each criterion still stands as a gap: %+v", out.Draft.NotDrafted)
	}
}
