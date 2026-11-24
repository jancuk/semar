package flow_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/flow"
)

func mobileStep(name string, do domain.MobileOp) domain.ScriptStep {
	return domain.ScriptStep{Step: domain.StepID(name), Mobile: &domain.MobileAction{Do: do}}
}

func mkFlow(id string, params []domain.FlowParam, steps ...domain.ScriptStep) domain.Flow {
	return domain.Flow{
		APIVersion: "semar/v1", Kind: "Flow", ID: id, Surface: domain.SurfaceAndroid,
		Service: "shop-app", App: "shop-app", Params: params, Steps: steps,
	}
}

func useOf(name, id string, with map[string]string) domain.ScriptStep {
	return domain.ScriptStep{Step: domain.StepID(name), Use: id, With: with}
}

func resolver(fs ...domain.Flow) flow.Resolver {
	return func(id string) (domain.Flow, error) {
		for _, f := range fs {
			if f.ID == id {
				return f, nil
			}
		}
		return domain.Flow{}, flow.ErrNotFound
	}
}

func problemsText(ps []flow.Problem) string {
	var out []string
	for _, p := range ps {
		out = append(out, p.String())
	}
	return strings.Join(out, "\n")
}

func TestAFlowStepMayHoldChecks(t *testing.T) {
	f := validFlow(t)
	f.Steps[3].Expect = []domain.Expectation{{Check: domain.Check{Element: "welcome", Exists: ptr(true)}, Source: domain.SourcePerson}}
	if ps := flow.Validate(f, "login"); len(ps) > 0 {
		t.Fatalf("problems = %v", ps)
	}
	data, err := flow.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	again, err := flow.Parse(data, "login.yaml", "login")
	if err != nil || len(again.Steps[3].Expect) != 1 || again.Steps[3].Expect[0].Source != domain.SourcePerson {
		t.Fatalf("round trip: %v\n%s", err, data)
	}
}

func TestAFlowCheckIsRefusedWhenItIsNotAPersonElementCheck(t *testing.T) {
	st := 200
	tests := []struct {
		name string
		e    domain.Expectation
		want string
	}{
		{"source ticket", domain.Expectation{Check: domain.Check{Element: "welcome", Exists: ptr(true)}, Source: domain.SourceTicket}, "must be person"},
		{"a status check", domain.Expectation{Check: domain.Check{Status: &st}, Source: domain.SourcePerson}, "reads an element"},
		{"a malformed element check", domain.Expectation{Check: domain.Check{Element: "welcome"}, Source: domain.SourcePerson}, "exactly one operator"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := validFlow(t)
			f.Steps[3].Expect = []domain.Expectation{tt.e}
			if ps := flow.Validate(f, "login"); !strings.Contains(problemsText(ps), tt.want) {
				t.Errorf("problems = %v, want %q", ps, tt.want)
			}
		})
	}
	f := mkFlow("outer", nil, useOf("a", "login", nil))
	f.Steps[0].Expect = []domain.Expectation{{Check: domain.Check{Element: "x", Exists: ptr(true)}, Source: domain.SourcePerson}}
	if ps := flow.Validate(f, "outer"); !strings.Contains(problemsText(ps), "takes no checks") {
		t.Errorf("problems = %v", ps)
	}
}

func TestCapturePatternRoundTripsThroughMarshal(t *testing.T) {
	f := validFlow(t)
	f.Steps[3].Captures[0].Pattern = `Hi (\w+)`
	data, err := flow.Marshal(f)
	if err != nil || !strings.Contains(string(data), "pattern:") {
		t.Fatalf("err = %v\n%s", err, data)
	}
	again, err := flow.Parse(data, "login.yaml", "login")
	if err != nil || again.Steps[3].Captures[0].Pattern != `Hi (\w+)` {
		t.Fatalf("err = %v, flow = %+v", err, again.Steps[3].Captures)
	}
}

func TestValidateWithRefusesACircle(t *testing.T) {
	a := mkFlow("a", nil, useOf("go", "a", nil))
	if got := problemsText(flow.ValidateWith(a, "a", resolver(a))); !strings.Contains(got, "flow a uses a") {
		t.Errorf("a to a: %q", got)
	}
	a = mkFlow("a", nil, useOf("go", "b", nil))
	b := mkFlow("b", nil, useOf("back", "a", nil))
	if got := problemsText(flow.ValidateWith(a, "a", resolver(a, b))); !strings.Contains(got, "flow a uses b, which uses a") {
		t.Errorf("a to b to a: %q", got)
	}
}

func TestValidateWithLimitsTheDepth(t *testing.T) {
	d := mkFlow("d", nil, mobileStep("s", domain.MobileLaunch))
	c := mkFlow("c", nil, useOf("go", "d", nil))
	b := mkFlow("b", nil, useOf("go", "c", nil))
	a := mkFlow("a", nil, useOf("go", "b", nil))
	if got := problemsText(flow.ValidateWith(a, "a", resolver(a, b, c, d))); !strings.Contains(got, "nested more than 3 deep: a > b > c > d") {
		t.Errorf("four levels: %q", got)
	}
	c = mkFlow("c", nil, mobileStep("s", domain.MobileLaunch))
	if ps := flow.ValidateWith(a, "a", resolver(a, b, c)); len(ps) > 0 {
		t.Errorf("three levels: %v", ps)
	}
}

func TestValidateWithChecksWithAndTheScope(t *testing.T) {
	inner := mkFlow("inner", []domain.FlowParam{{Name: "email"}, {Name: "tag", Default: ptr("x")}},
		domain.ScriptStep{Step: "s", Mobile: &domain.MobileAction{Do: domain.MobileTap, Screen: "SignIn", Element: "submit"},
			Captures: []domain.Capture{{Name: "orderRef", Element: "ref", Pattern: `#(\d+)`}}})
	tests := []struct {
		name string
		with map[string]string
		want string
	}{
		{"an unknown key", map[string]string{"email": "a", "color": "red"}, "has no param color"},
		{"a missing required param", nil, "needs the param email"},
		{"a variable that is not known", map[string]string{"email": "{{who}}"}, "{{who}} is not a param"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outer := mkFlow("outer", nil, useOf("in", "inner", tt.with))
			if got := problemsText(flow.ValidateWith(outer, "outer", resolver(outer, inner))); !strings.Contains(got, tt.want) {
				t.Errorf("problems = %q, want %q", got, tt.want)
			}
		})
	}
	// A capture of the used flow is known to the steps after it, and a clash with a param is refused.
	later := domain.ScriptStep{Step: "after", Mobile: &domain.MobileAction{Do: domain.MobileType, Screen: "SignIn", Element: "email", Value: "{{orderRef}}"}}
	outer := mkFlow("outer", nil, useOf("in", "inner", map[string]string{"email": "a"}), later)
	if ps := flow.ValidateWith(outer, "outer", resolver(outer, inner)); len(ps) > 0 {
		t.Errorf("a nested capture must be visible: %v", ps)
	}
	if ps := flow.Validate(outer, "outer"); len(ps) > 0 {
		t.Errorf("without the flows a later step is not judged: %v", ps)
	}
	clash := mkFlow("outer", []domain.FlowParam{{Name: "orderRef"}}, useOf("in", "inner", map[string]string{"email": "a"}))
	if got := problemsText(flow.ValidateWith(clash, "outer", resolver(clash, inner))); !strings.Contains(got, "orderRef, which is already") {
		t.Errorf("clash: %q", got)
	}
	missing := mkFlow("outer", nil, useOf("in", "gone", nil))
	if got := problemsText(flow.ValidateWith(missing, "outer", resolver(missing))); !strings.Contains(got, "flow gone") {
		t.Errorf("a missing flow: %q", got)
	}
}

func TestSourceRefusesACircleOfFlowFiles(t *testing.T) {
	repo := t.TempDir()
	step := func(use string) string {
		return "apiVersion: semar/v1\nkind: Flow\nid: ID\nsurface: android\nservice: shop-app\napp: shop-app\nsteps:\n  - step: go\n    use: " + use + "\n"
	}
	writeFile(t, repo, ".semar/flows/a.yaml", strings.Replace(step("b"), "ID", "a", 1))
	writeFile(t, repo, ".semar/flows/b.yaml", strings.Replace(step("a"), "ID", "b", 1))
	src := flow.Loader(repo)
	_, err := src.Entry("a")
	if err == nil || !strings.Contains(err.Error(), "flow a uses b, which uses a") {
		t.Fatalf("err = %v", err)
	}
	if _, err := src.Entry("a"); err == nil {
		t.Error("the refusal is not kept")
	}
}

func TestSourceLoadsAFlowThatUsesAnother(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".semar/flows/login.yaml", loginYAML)
	writeFile(t, repo, ".semar/flows/outer.yaml", "apiVersion: semar/v1\nkind: Flow\nid: outer\nsurface: android\nservice: shop-app\napp: shop-app\nsteps:\n  - step: in\n    use: login\n    with: { email: a@b.c }\n")
	e, err := flow.Loader(repo).Entry("outer")
	if err != nil || !slices.Equal(e.Flow.Steps[0].Captures, nil) || e.Flow.Steps[0].Use != "login" {
		t.Fatalf("err = %v, flow = %+v", err, e.Flow)
	}
	writeFile(t, repo, ".semar/flows/outer.yaml", "apiVersion: semar/v1\nkind: Flow\nid: outer\nsurface: android\nservice: shop-app\napp: shop-app\nsteps:\n  - step: in\n    use: login\n")
	if _, err := flow.Loader(repo).Entry("outer"); err == nil || !strings.Contains(err.Error(), "needs the param email") {
		t.Errorf("err = %v", err)
	}
}

func ptr[T any](v T) *T { return &v }
