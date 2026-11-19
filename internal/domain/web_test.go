package domain_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func TestWebActionProblems(t *testing.T) {
	ok := []domain.WebAction{
		{Do: domain.WebOpen, Path: "/boards/{{board}}"},
		{Do: domain.WebClick, Screen: "Board", Element: "card"},
		{Do: domain.WebFill, Screen: "Login", Element: "email", Value: "a@example.com"},
		{Do: domain.WebFill, Screen: "Login", Element: "password", Secret: "env:SHOP_PASSWORD"},
		{Do: domain.WebPress, Value: "Enter"},
		{Do: domain.WebPress, Screen: "Login", Element: "email", Value: "Enter"},
		{Do: domain.WebDrag, Screen: "Board", Element: "card", To: "doing"},
		{Do: domain.WebWait},
		{Do: domain.WebWait, Screen: "Board", Element: "card"},
	}
	for _, w := range ok {
		if ps := w.Problems(); len(ps) > 0 {
			t.Errorf("%+v: unexpected problems %v", w, ps)
		}
	}
	bad := []struct {
		w    domain.WebAction
		want string
	}{
		{domain.WebAction{}, "do must be one of open, click, fill, press, drag, wait"},
		{domain.WebAction{Do: "tap"}, "do must be one of"},
		{domain.WebAction{Do: domain.WebOpen}, "open needs the path"},
		{domain.WebAction{Do: domain.WebOpen, Path: "boards"}, "must start with /"},
		{domain.WebAction{Do: domain.WebOpen, Path: "/", Element: "x"}, "open takes no element"},
		{domain.WebAction{Do: domain.WebClick, Screen: "B"}, "click needs the element"},
		{domain.WebAction{Do: domain.WebClick, Element: "x"}, "click needs the screen"},
		{domain.WebAction{Do: domain.WebFill, Screen: "L", Element: "e"}, "fill needs a value or a secret"},
		{domain.WebAction{Do: domain.WebFill, Screen: "L", Element: "e", Value: "a", Secret: "env:X"}, "not both"},
		{domain.WebAction{Do: domain.WebFill, Screen: "L", Element: "e", Secret: "hunter2"}, "secret must be a secret reference"},
		{domain.WebAction{Do: domain.WebPress}, "press needs the value"},
		{domain.WebAction{Do: domain.WebDrag, Screen: "B", Element: "a"}, "drag needs the to"},
		{domain.WebAction{Do: domain.WebClick, Screen: "B", Element: "a", Value: "x"}, "click takes no value"},
		{domain.WebAction{Do: domain.WebOpen, Path: "/{{x"}, "path holds a malformed variable"},
	}
	for _, c := range bad {
		ps := c.w.Problems()
		if !strings.Contains(strings.Join(ps, "; "), c.want) {
			t.Errorf("%+v: problems %v, want one with %q", c.w, ps, c.want)
		}
	}
}

func TestWebWrites(t *testing.T) {
	for op, want := range map[domain.WebOp]bool{
		domain.WebOpen: false, domain.WebWait: false,
		domain.WebClick: true, domain.WebFill: true, domain.WebPress: true, domain.WebDrag: true,
		domain.WebSelect: true, domain.WebCheck: true, domain.WebUncheck: true, domain.WebUpload: true, domain.WebHover: false,
	} {
		if op.Writes() != want {
			t.Errorf("%s.Writes() = %v, want %v", op, !want, want)
		}
	}
}

func TestElementCheckKeepsOldHashes(t *testing.T) {
	sc := domain.Scenario{ID: "a", Intent: "i", Steps: []domain.Step{{ID: "s", Expect: []domain.Expectation{{Check: domain.Check{JSONPath: "$.a", Equals: 1}, Source: domain.SourcePerson}}}}}
	before := domain.ExpectHash(sc)
	sc.Steps[0].Expect = append(sc.Steps[0].Expect, domain.Expectation{Check: domain.Check{Element: "card", Equals: "x"}, Source: domain.SourcePerson})
	if domain.ExpectHash(sc) == before {
		t.Error("an element check did not change the hash")
	}
	sc.Steps[0].Expect = sc.Steps[0].Expect[:1]
	if domain.ExpectHash(sc) != before {
		t.Error("a scenario without an element check changed its hash")
	}
}

func TestWebActionVariables(t *testing.T) {
	w := domain.WebAction{Do: domain.WebFill, Path: "/{{a}}/{{b}}", Value: "{{a}} {{c.d}}"}
	if got := strings.Join(w.Variables(), ","); got != "a,b,c.d" {
		t.Errorf("Variables = %s", got)
	}
}

func TestNewWebActionsAreWellFormed(t *testing.T) {
	ok := func(w domain.WebAction) {
		t.Helper()
		if ps := w.Problems(); len(ps) > 0 {
			t.Errorf("%+v: %v", w, ps)
		}
	}
	bad := func(w domain.WebAction, want string) {
		t.Helper()
		if ps := strings.Join(w.Problems(), "; "); !strings.Contains(ps, want) {
			t.Errorf("%+v: problems = %q, want %q", w, ps, want)
		}
	}
	ok(domain.WebAction{Do: domain.WebSelect, Screen: "S", Element: "status", Value: "Paid"})
	ok(domain.WebAction{Do: domain.WebHover, Screen: "S", Element: "menu"})
	ok(domain.WebAction{Do: domain.WebCheck, Screen: "S", Element: "agree"})
	ok(domain.WebAction{Do: domain.WebUncheck, Screen: "S", Element: "agree"})
	ok(domain.WebAction{Do: domain.WebUpload, Screen: "S", Element: "file", Value: "docs/invoice.pdf"})
	ok(domain.WebAction{Do: domain.WebUpload, Screen: "S", Element: "file", Value: "{{run.id}}.pdf"})
	bad(domain.WebAction{Do: domain.WebSelect, Screen: "S", Element: "status"}, "select needs the value")
	bad(domain.WebAction{Do: domain.WebHover, Screen: "S", Element: "menu", Value: "x"}, "hover takes no value")
	bad(domain.WebAction{Do: domain.WebCheck, Screen: "S"}, "check needs the element")
	upload := func(value string) string {
		return domain.WebAction{Do: domain.WebUpload, Screen: "S", Element: "file", Value: value}.UploadProblem()
	}
	for value, want := range map[string]string{
		"../x.pdf": "must stay below .semar/files/", "a/../../x": "must stay below", "/etc/passwd": "no absolute part",
		`a\b`: "no absolute part", "~/x": "no absolute part",
	} {
		if got := upload(value); !strings.Contains(got, want) {
			t.Errorf("upload %q: problem = %q, want %q", value, got, want)
		}
	}
	for _, fine := range []string{"invoice.pdf", "docs/a b.pdf", "{{run.id}}/../x"} {
		if got := upload(fine); got != "" {
			t.Errorf("upload %q: %s", fine, got)
		}
	}
}

func TestWebChecksKeepOldHashesAndChangeWithTheirFields(t *testing.T) {
	sc := func(c domain.Check) domain.Hash {
		return domain.ExpectHash(domain.Scenario{ID: "a", Intent: "i", Steps: []domain.Step{{ID: "s", Expect: []domain.Expectation{{Check: c, Source: domain.SourcePerson}}}}})
	}
	base := sc(domain.Check{Element: "card", Exists: ptr(true)})
	for _, c := range []domain.Check{
		{Element: "card", State: "hidden"}, {Element: "card", Attribute: "href", Equals: "x"}, {Page: "path", Equals: "/x"},
	} {
		if sc(c) == base {
			t.Errorf("%+v hashes as an element check that exists", c)
		}
	}
	if sc(domain.Check{Element: "card", State: "hidden"}) == sc(domain.Check{Element: "card", State: "visible"}) {
		t.Error("two states hash alike")
	}
}

func ptr[T any](v T) *T { return &v }
