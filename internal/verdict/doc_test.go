package verdict_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/verdict"
)

func withDoc(step domain.StepID, d *domain.DocCheck) mod {
	return change(step, func(ev *verdict.StepEvidence) {
		a := *ev.Action
		a.Doc = d
		ev.Action = &a
	})
}

func TestSchemaMisfitFailsOnlyInFailMode(t *testing.T) {
	misfit := []domain.Misfit{{Path: "$.data.items[0].id", Want: "integer", Got: "string"}}
	res := verdict.Compute(with(pass, withDoc("refund-too-much", &domain.DocCheck{Env: "shop", Misfits: misfit, MoreMisfits: 2, SchemaFail: true}))())
	if res.Outcome != domain.OutcomeFailed {
		t.Fatalf("outcome %s", res.Outcome)
	}
	text := reasons(res)
	for _, want := range []string{
		"the response does not fit the API document: data.items[0].id is a string, and the document says integer (and 2 more)",
		"environments.shop.checks.schema to warn",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("reasons miss %q:\n%s", want, text)
		}
	}
	res = verdict.Compute(with(pass, withDoc("refund-too-much", &domain.DocCheck{Misfits: misfit}))())
	if res.Outcome != domain.OutcomePassed {
		t.Errorf("warn mode changed the outcome to %s", res.Outcome)
	}
}

func TestUndeclaredStatusNeverFails(t *testing.T) {
	res := verdict.Compute(with(pass, withDoc("refund-too-much", &domain.DocCheck{Undeclared: true, Status: 418, SchemaFail: true}))())
	if res.Outcome != domain.OutcomePassed {
		t.Errorf("outcome %s", res.Outcome)
	}
}

func TestSlowResponseFailsOnlyWhenTheSettingSaysSo(t *testing.T) {
	slow := domain.DocCheck{Env: "shop", LimitMS: 2000, TookMS: 2300}
	if res := verdict.Compute(with(pass, withDoc("refund-too-much", &slow))()); res.Outcome != domain.OutcomePassed {
		t.Errorf("warn: %s", res.Outcome)
	}
	slow.SlowFail = true
	res := verdict.Compute(with(pass, withDoc("refund-too-much", &slow))())
	if res.Outcome != domain.OutcomeFailed || !strings.Contains(reasons(res), "took 2300 ms, over the limit of 2000 ms") {
		t.Errorf("fail: %s\n%s", res.Outcome, reasons(res))
	}
}

func TestSetupStepIsNotJudgedByTheDocument(t *testing.T) {
	d := &domain.DocCheck{Misfits: []domain.Misfit{{Path: "$", Want: "object", Got: "array"}}, SchemaFail: true}
	if res := verdict.Compute(with(pass, withDoc("create-order", d))()); res.Outcome != domain.OutcomePassed {
		t.Errorf("outcome %s", res.Outcome)
	}
}
