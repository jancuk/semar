package domain_test

import (
	"reflect"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func TestOperandVariables(t *testing.T) {
	c := domain.Check{JSONPath: "$.board", Equals: "board {{o.board}} of {{run.id}}", Contains: 5}
	got := domain.OperandVariables(c)
	want := []string{"o.board", "run.id"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if v := domain.OperandVariables(domain.Check{JSONPath: "$.a", Equals: 3}); len(v) != 0 {
		t.Fatalf("a number holds no variable, got %v", v)
	}
}

func TestOperandMalformed(t *testing.T) {
	for _, s := range []string{"{{", "a {{ b }}", "{{1x}}", "}}{{x"} {
		if domain.OperandMalformed(domain.Check{JSONPath: "$.a", NotEquals: s}) == "" {
			t.Errorf("%q must be malformed", s)
		}
	}
	if m := domain.OperandMalformed(domain.Check{JSONPath: "$.a", NotEquals: "plain {{x}}"}); m != "" {
		t.Errorf("unexpected problem %q", m)
	}
}

func TestFillOperands(t *testing.T) {
	c := domain.Check{JSONPath: "$.board", Equals: "{{o.board}}-{{n}}"}
	vars := map[string]string{"o.board": "b2", "n": "7"}
	got, missing := domain.FillOperands(c, func(n string) (string, bool) { v, ok := vars[n]; return v, ok })
	if len(missing) != 0 || got.Equals != "b2-7" {
		t.Fatalf("got %v %v", got.Equals, missing)
	}
	if c.Equals != "{{o.board}}-{{n}}" {
		t.Fatal("the input was changed")
	}
	_, missing = domain.FillOperands(c, func(n string) (string, bool) { return "", false })
	if !reflect.DeepEqual(missing, []string{"o.board", "n"}) {
		t.Fatalf("missing = %v", missing)
	}
	got, _ = domain.FillOperands(domain.Check{Equals: "{{a}}{{b}}"}, func(n string) (string, bool) {
		return map[string]string{"a": "{{b}}", "b": "x"}[n], true
	})
	if got.Equals != "{{b}}x" {
		t.Fatalf("got %v", got.Equals)
	}
}

func TestFillOperandsMarksAValueThatIsOneWholeVariable(t *testing.T) {
	vars := map[string]string{"position": "0"}
	lookup := func(n string) (string, bool) { v, ok := vars[n]; return v, ok }
	got, _ := domain.FillOperands(domain.Check{JSONPath: "$.p", Equals: "{{position}}", NotEquals: "{{position}}", Contains: "{{position}}"}, lookup)
	if got.Equals != domain.CapturedText("0") || got.NotEquals != domain.CapturedText("0") {
		t.Fatalf("equals %#v, notEquals %#v: want the captured text 0", got.Equals, got.NotEquals)
	}
	if got.Contains != "0" {
		t.Fatalf("contains %#v: want the string 0", got.Contains)
	}
	got, _ = domain.FillOperands(domain.Check{JSONPath: "$.p", Equals: "n{{position}}"}, lookup)
	if got.Equals != "n0" {
		t.Fatalf("a variable inside a text gave %#v", got.Equals)
	}
}
