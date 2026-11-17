package scenario_test

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/scenario"
)

func ptr[T any](v T) *T { return &v }

func gOne(c domain.Check) domain.Scenario {
	return domain.Scenario{
		APIVersion: "semar/v1", Kind: "Scenario", ID: "A-1", Title: "t", Ticket: "T-1", Surface: domain.SurfaceAPI, AuthProfile: "default",
		Steps: []domain.Step{{
			ID: "s", Action: "do it",
			Expect: []domain.Expectation{{Check: c, Source: domain.SourceDiff}},
		}},
	}
}

// gRoundTrip renders in, reads it back, and wants want, and the same expectation hash as in.
func gRoundTrip(t *testing.T, in, want domain.Scenario) string {
	t.Helper()
	data, err := scenario.Render(in, "")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got, err := scenario.Parse(data, "m.feature")
	if err != nil {
		t.Fatalf("Parse(Render(x)): %v\n%s", err, data)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse(Render(x)) = %#v, want %#v\n%s", got, want, data)
	}
	if domain.ExpectHash(got) != domain.ExpectHash(in) {
		t.Fatalf("the expectation hash changed\n%s", data)
	}
	return string(data)
}

// fullScenario holds every part of a scenario that Gherkin carries.
func fullScenario() domain.Scenario {
	return domain.Scenario{
		APIVersion: "semar/v1", Kind: "Scenario", ID: "SHOP-1-02", Title: "A refund above the total is refused", Ticket: "SHOP-1",
		Intent:  "Before, any refund was accepted.\nNow a refund above the total gives 422.",
		Surface: domain.SurfaceAPI, AuthProfile: "support",
		Given: []domain.Given{
			{Fixture: "order.paid", As: "order", With: map[string]string{"customer": "c-1", "amount": "1000"}, Lease: domain.LeaseExclusive},
			{Fixture: "customer.verified", As: "customer"},
		},
		Steps: []domain.Step{
			{ID: "create", Setup: true, Action: "Create a paid order", Hint: &domain.RequestHint{Method: "POST", Path: "/v1/orders"}},
			{ID: "ask", Setup: true, As: "bob", Action: "Bob asks for a quote", Hint: &domain.RequestHint{Method: "GET", Path: "/v1/quotes"}},
			{
				ID: "refund", Action: "Ask for a refund of 150000", As: "bob",
				Hint: &domain.RequestHint{Method: "POST", Path: "/v1/orders/{id}/refunds"},
				Expect: []domain.Expectation{
					{Check: domain.Check{Status: ptr(422)}, Source: domain.SourceTicket, Cite: "docs/SHOP-1.md:12 a refund above the total is refused", By: "model"},
					{Check: domain.Check{JSONPath: "$.error.code", Equals: "REFUND_EXCEEDS_TOTAL"}, Source: domain.SourceDiff},
					{Check: domain.Check{Header: "Content-Type", Contains: "json"}, Source: domain.SourcePerson},
				},
			},
			{ID: "open", Setup: true, Action: "Open the order page"},
			{
				ID: "look", Action: "Read the order again", Hint: &domain.RequestHint{Method: "GET", Path: "/v1/orders/{id}"},
				Expect: []domain.Expectation{
					{Check: domain.Check{JSONPath: "$.data.refunded_total", Equals: 0}, Source: domain.SourceTicket, Cite: "SHOP-1: the order stays unchanged"},
					{Check: domain.Check{Element: "total", Equals: "{{order.total}}"}, Source: domain.SourceBaseline},
				},
			},
		},
	}
}

func TestRenderWritesTheFullScenarioAsReadableGherkin(t *testing.T) {
	got := gRoundTrip(t, fullScenario(), fullScenario())
	want, err := os.ReadFile("testdata/full.feature")
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("Render differs from testdata/full.feature:\n%s", got)
	}
}

func TestThenFormsRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		check domain.Check
		line  string // the whole Then line
	}{
		{"status", domain.Check{Status: ptr(422)}, `the status is 422`},
		{"status zero", domain.Check{Status: ptr(0)}, `the status is 0`},
		{"equals", domain.Check{JSONPath: "$.error.code", Equals: "cross_board_move"}, `$.error.code equals "cross_board_move"`},
		{"notEquals", domain.Check{JSONPath: "$.a", NotEquals: 3}, `$.a does not equal 3`},
		{"contains", domain.Check{JSONPath: "$.a", Contains: "x"}, `$.a contains "x"`},
		{"exists", domain.Check{JSONPath: "$.data.id", Exists: ptr(true)}, `$.data.id exists`},
		{"does not exist", domain.Check{JSONPath: "$.data.id", Exists: ptr(false)}, `$.data.id does not exist`},
		{"greater", domain.Check{JSONPath: "$.a", Greater: ptr(0.1)}, `$.a is greater than 0.1`},
		{"greater whole", domain.Check{JSONPath: "$.a", Greater: ptr(5.0)}, `$.a is greater than 5`},
		{"less", domain.Check{JSONPath: "$.a", Less: ptr(-2.5e-9)}, `$.a is less than -2.5e-9`},
		{"less large", domain.Check{JSONPath: "$.a", Less: ptr(1e300)}, `$.a is less than 1e+300`},
		{"count", domain.Check{JSONPath: "$.data.items", Count: ptr(3)}, `$.data.items has 3 items`},
		{"count one", domain.Check{JSONPath: "$.data.items", Count: ptr(1)}, `$.data.items has 1 item`},
		{"count zero", domain.Check{JSONPath: "$.data.items", Count: ptr(0)}, `$.data.items has 0 items`},
		{"equalsPath", domain.Check{JSONPath: "$.a", EqualsPath: "$.b"}, `$.a equals the value at $.b`},
		{"header equals", domain.Check{Header: "Content-Type", Equals: "application/json"}, `the header Content-Type equals "application/json"`},
		{"header contains", domain.Check{Header: "Location", Contains: "/v1/"}, `the header Location contains "/v1/"`},
		{"header does not exist", domain.Check{Header: "X-Debug", Exists: ptr(false)}, `the header X-Debug does not exist`},
		{"header count", domain.Check{Header: "Set-Cookie", Count: ptr(2)}, `the header Set-Cookie has 2 items`},
		{"element equals", domain.Check{Element: "todo-first", Equals: "{{b_title}}"}, `the element todo-first equals "{{b_title}}"`},
		{"element notEquals", domain.Check{Element: "todo-first", NotEquals: "x"}, `the element todo-first does not equal "x"`},
		{"element contains", domain.Check{Element: "banner", Contains: "saved"}, `the element banner contains "saved"`},
		{"element exists", domain.Check{Element: "banner", Exists: ptr(true)}, `the element banner exists`},
		{"element count", domain.Check{Element: "card", Count: ptr(4)}, `the element card has 4 items`},
		{"path with spaces and operator words", domain.Check{JSONPath: `$.items[?(@.name == "a equals b")].id`, Equals: "x equals y"}, `$.items[?(@.name == "a equals b")].id equals "x equals y"`},
		{"path that ends in an operator word", domain.Check{JSONPath: "$.a.exists", Exists: ptr(true)}, `$.a.exists exists`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := gOne(tt.check)
			text := gRoundTrip(t, in, in)
			if !strings.Contains(text, "\n    Then "+tt.line+"\n") {
				t.Errorf("want the line %q in\n%s", "Then "+tt.line, text)
			}
		})
	}
}

func TestOperandRoundTrips(t *testing.T) {
	tests := []struct {
		name    string
		operand any
	}{
		{"int", 422},
		{"negative int", -7},
		{"zero", 0},
		{"2^53", 9007199254740992},
		{"largest uint64", uint64(18446744073709551615)},
		{"float", 0.1},
		{"whole float", 100000.0},
		{"negative zero", math.Copysign(0, -1)},
		{"small float", 4.9e-324},
		{"large float", 1.7976931348623157e308},
		{"exponent float", 1e21},
		{"true", true},
		{"false", false},
		{"string of digits", "422"},
		{"string true", "true"},
		{"string null", "null"},
		{"string empty", ""},
		{"leading space", " x"},
		{"trailing space", "x "},
		{"colon space", "a: b"},
		{"hash", "a #b"},
		{"leading hash", "#b"},
		{"newline", "a\nb"},
		{"newline at end", "a\nb\n"},
		{"blank lines", "a\n\nb"},
		{"tab", "a\tb"},
		{"carriage return", "a\r\nb"},
		{"variable", "{{order_id}}"},
		{"html characters", "<a href=\"x\">&</a>"},
		{"pipe and backslash", `a | b \ c`},
		{"quote", `say "hi"`},
		{"unicode", "pengembalian dana \u00e9 \u2028 \u0000"},
		{"nil in a list", []any{nil, 1, "a"}},
		{"nested", map[string]any{"b": []any{1, 2.5, map[string]any{"z": "y", "a": nil}}, "a": true}},
		{"empty list", []any{}},
		{"empty object", map[string]any{}},
		{"keys that look like other things", map[string]any{"1": "a", "": "b", "true": "c", "a: b": "d", "x\"y": "e"}},
	}
	for _, tt := range tests {
		for _, op := range []string{"equals", "notEquals", "contains"} {
			t.Run(tt.name+"/"+op, func(t *testing.T) {
				c := domain.Check{JSONPath: "$.a"}
				switch op {
				case "equals":
					c.Equals = tt.operand
				case "notEquals":
					c.NotEquals = tt.operand
				default:
					c.Contains = tt.operand
				}
				in := gOne(c)
				gRoundTrip(t, in, in)
			})
		}
	}
}

func TestOperandNeverBreaksTheLine(t *testing.T) {
	in := gOne(domain.Check{JSONPath: "$.a", Equals: "line one\nline two\r\n\u2028"})
	text := gRoundTrip(t, in, in)
	if !strings.Contains(text, `equals "line one\nline two\r\n\u2028"`) {
		t.Errorf("the value must be one escaped line\n%s", text)
	}
}

func TestOtherIntegerTypesReadAsInt(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want any
	}{
		{"int64", int64(5), 5},
		{"small uint64", uint64(5), 5},
		{"json integer", json.Number("100000"), 100000},
		{"json negative", json.Number("-12"), -12},
		{"json uint64", json.Number("18446744073709551615"), uint64(18446744073709551615)},
		{"json float", json.Number("0.1"), 0.1},
		{"json float exponent", json.Number("1e+21"), 1e21},
		{"json in a list", []any{json.Number("1"), json.Number("2.5")}, []any{1, 2.5}},
		{"json in an object", map[string]any{"a": json.Number("7")}, map[string]any{"a": 7}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gRoundTrip(t, gOne(domain.Check{JSONPath: "$.a", Equals: tt.in}), gOne(domain.Check{JSONPath: "$.a", Equals: tt.want}))
		})
	}
}

func TestRenderRefusesWhatParseCannotHold(t *testing.T) {
	tests := []struct {
		name  string
		check domain.Check
		want  []string
	}{
		{"NaN operand", domain.Check{JSONPath: "$.a", Equals: math.NaN()}, []string{"steps[0].expect[0].check.equals", "finite"}},
		{"infinite operand", domain.Check{JSONPath: "$.a", NotEquals: math.Inf(1)}, []string{"notEquals", "finite"}},
		{"NaN in a list", domain.Check{JSONPath: "$.a", Contains: []any{1, math.NaN()}}, []string{"contains[1]", "finite"}},
		{"infinite in an object", domain.Check{JSONPath: "$.a", Equals: map[string]any{"k": math.Inf(-1)}}, []string{"equals.k", "finite"}},
		{"NaN bound", domain.Check{JSONPath: "$.a", Greater: ptr(math.NaN())}, []string{"check.greater", "finite"}},
		{"infinite bound", domain.Check{JSONPath: "$.a", Less: ptr(math.Inf(1))}, []string{"check.less", "finite"}},
		{"wide json integer", domain.Check{JSONPath: "$.a", Equals: json.Number("123456789012345678901234")}, []string{"equals", "too wide"}},
		{"json float with no exact short text", domain.Check{JSONPath: "$.a", Equals: json.Number("0.1000000000000000055511151231257827")}, []string{"equals", "exactly"}},
		{"json float out of range", domain.Check{JSONPath: "$.a", Equals: json.Number("1e400")}, []string{"equals", "range"}},
		{"json text that is not a number", domain.Check{JSONPath: "$.a", Equals: json.Number("0x10")}, []string{"equals", "not a JSON number"}},
		{"json float stored under another text", domain.Check{JSONPath: "$.a", Equals: json.Number("100.0")}, []string{"equals", "write 100"}},
		{"json negative zero", domain.Check{JSONPath: "$.a", Equals: json.Number("-0")}, []string{"equals", "write 0"}},
		{"json exponent text", domain.Check{JSONPath: "$.a", Equals: json.Number("1e3")}, []string{"equals", "write 1000"}},
		{"invalid UTF-8 operand", domain.Check{JSONPath: "$.a", Equals: "a\xffb"}, []string{"equals", "UTF-8"}},
		{"invalid UTF-8 key", domain.Check{JSONPath: "$.a", Equals: map[string]any{"a\xff": 1}}, []string{"equals", "UTF-8"}},
		{"nil object", domain.Check{JSONPath: "$.a", Equals: map[string]any(nil)}, []string{"equals", "nil object"}},
		{"nil list", domain.Check{JSONPath: "$.a", Equals: []any(nil)}, []string{"equals", "nil list"}},
		{"unsupported type", domain.Check{JSONPath: "$.a", Equals: []string{"a"}}, []string{"equals", "[]string"}},
		{"float32", domain.Check{JSONPath: "$.a", Equals: float32(0.5)}, []string{"equals", "float32"}},
		{"empty check", domain.Check{}, []string{"check", "exactly one of status"}},
		{"two subjects", domain.Check{JSONPath: "$.a", Header: "X", Equals: 1}, []string{"check", "exactly one of status"}},
		{"two operators", domain.Check{JSONPath: "$.a", Equals: 1, Contains: 2}, []string{"check", "exactly one operator"}},
		{"no operator", domain.Check{JSONPath: "$.a"}, []string{"check", "exactly one operator"}},
		{"status with an operator", domain.Check{Status: ptr(200), Equals: 1}, []string{"check", "takes no operator"}},
		{"path that is not a path", domain.Check{JSONPath: "a.b", Equals: 1}, []string{"check.jsonpath", "start with $"}},
		{"header with a space", domain.Check{Header: "X Y", Equals: 1}, []string{"check.header", "one word"}},
		{"element with a space", domain.Check{Element: "x y", Equals: 1}, []string{"check.element", "one word"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := scenario.Render(gOne(tt.check), "")
			if err == nil {
				t.Fatal("Render accepted the value")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("err = %v, want it to contain %q", err, w)
				}
			}
		})
	}

	t.Run("deeply nested", func(t *testing.T) {
		var v any = "x"
		for range 1000 {
			v = []any{v}
		}
		if _, err := scenario.Render(gOne(domain.Check{JSONPath: "$.a", Equals: v}), ""); err == nil {
			t.Fatal("Render accepted a value nested 1000 levels")
		}
	})
}

func TestRenderRefusesTextThatGherkinCannotCarry(t *testing.T) {
	tests := []struct {
		name string
		edit func(*domain.Scenario)
		want []string
	}{
		{"invalid UTF-8 title", func(s *domain.Scenario) { s.Title = "a\xff" }, []string{"title", "UTF-8"}},
		{"title on two lines", func(s *domain.Scenario) { s.Title = "a\nb" }, []string{"title", "one line"}},
		{"action on two lines", func(s *domain.Scenario) { s.Steps[0].Action = "a\nb" }, []string{"steps[0].action", "one line"}},
		{"empty action", func(s *domain.Scenario) { s.Steps[0].Action = "" }, []string{"steps[0].action", "required"}},
		{"action with a space at the end", func(s *domain.Scenario) { s.Steps[0].Action = "a " }, []string{"steps[0].action", "start or end with a space"}},
		{"cite on two lines", func(s *domain.Scenario) { s.Steps[0].Expect[0].Cite = "a\nb" }, []string{"expect[0].cite", "one line"}},
		{"intent that ends with two line breaks", func(s *domain.Scenario) { s.Intent = "a\n\n" }, []string{"intent", "more than one line break"}},
		{"intent that starts with a line break", func(s *domain.Scenario) { s.Intent = "\na" }, []string{"intent", "blank line"}},
		{"intent that is all indented", func(s *domain.Scenario) { s.Intent = "  a\n  b" }, []string{"intent", "starts with a space"}},
		{"intent with a line of spaces", func(s *domain.Scenario) { s.Intent = "a\n  \nb" }, []string{"intent", "line 2", "only spaces"}},
		{"intent with a carriage return", func(s *domain.Scenario) { s.Intent = "a\r\nb" }, []string{"intent", "carriage return"}},
		{"source with a space", func(s *domain.Scenario) { s.Steps[0].Expect[0].Source = "a b" }, []string{"source", "one word"}},
		{"no source", func(s *domain.Scenario) { s.Steps[0].Expect[0].Source = "" }, []string{"source", "one word"}},
		{"by something else", func(s *domain.Scenario) { s.Steps[0].Expect[0].By = "robot" }, []string{"by", "model"}},
		{"a setup step with an expected result", func(s *domain.Scenario) { s.Steps[0].Setup = true }, []string{"steps[0].expect", "setup"}},
		{"an action that reads as another account", func(s *domain.Scenario) { s.Steps[0].Action = "as bob, do it" }, []string{"steps[0]", "different value"}},
		{"a setup action that reads as a fixture", func(s *domain.Scenario) {
			s.Steps[0].Setup, s.Steps[0].Expect = true, nil
			s.Steps[0].Action = "the fixture order.paid as order"
		}, []string{"different value"}},
		{"an action with a hint that has no path", func(s *domain.Scenario) { s.Steps[0].Hint = &domain.RequestHint{Method: "GET"} }, []string{"steps[0].hint"}},
		{"a step ID with a semicolon", func(s *domain.Scenario) { s.Steps[0].ID = "a;b" }, []string{"steps[0].id", "semicolon"}},
		{"no scenario ID", func(s *domain.Scenario) { s.ID = "" }, []string{"id", "required"}},
		{"no ticket", func(s *domain.Scenario) { s.Ticket = "" }, []string{"ticket", "required"}},
		{"no surface", func(s *domain.Scenario) { s.Surface = "" }, []string{"surface", "required"}},
		{"an ID with a space", func(s *domain.Scenario) { s.ID = "A 1" }, []string{"id", "one word"}},
		{"a ticket with a hash", func(s *domain.Scenario) { s.Ticket = "T#1" }, []string{"ticket", "one word"}},
		{"a profile with a space", func(s *domain.Scenario) { s.AuthProfile = "a b" }, []string{"authProfile", "one word"}},
		{"an account with a comma", func(s *domain.Scenario) { s.Steps[0].As = "a,b" }, []string{"steps[0].as", "comma"}},
		{"a fixture parameter with a space at the end", func(s *domain.Scenario) {
			s.Given = []domain.Given{{Fixture: "f.x", As: "a", With: map[string]string{"k": "v "}}}
		}, []string{"given[0].with.k", "space"}},
		{"a fixture parameter that is not UTF-8", func(s *domain.Scenario) {
			s.Given = []domain.Given{{Fixture: "f.x", As: "a", With: map[string]string{"c": "a\xffb"}}}
		}, []string{"given[0].with.c", "UTF-8"}},
		{"a lease that is not known", func(s *domain.Scenario) {
			s.Given = []domain.Given{{Fixture: "f.x", As: "a", Lease: "shared"}}
		}, []string{"given[0].lease"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := gOne(domain.Check{Status: ptr(200)})
			tt.edit(&s)
			_, err := scenario.Render(s, "")
			if err == nil {
				t.Fatal("Render accepted the scenario")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("err = %v, want it to contain %q", err, w)
				}
			}
		})
	}
}

func TestAnIntentThatGherkinWouldReadAsAStepIsEscaped(t *testing.T) {
	tests := []struct {
		name   string
		intent string
		line   string // the line of the file
	}{
		{"a step", "Given a paid order, its owner reads it.", "    \\Given a paid order, its owner reads it.\n"},
		{"a step in the second line", "ok\nWhen a card moves, it is refused", "    ok\n    \\When a card moves, it is refused\n"},
		{"an And", "And so on", "    \\And so on\n"},
		{"a star", "* a bullet", "    \\* a bullet\n"},
		{"a comment", "# note", "    \\# note\n"},
		{"a tag", "@here", "    \\@here\n"},
		{"a table row", "| a | b |", "    \\| a | b |\n"},
		{"a doc string", `"""`, "    \\\"\"\"\n"},
		{"a Scenario", "Scenario: x", "    \\Scenario: x\n"},
		{"a Feature", "Feature: x", "    \\Feature: x\n"},
		{"an Examples", "Examples:", "    \\Examples:\n"},
		{"a backslash", `\server`, "    \\\\server\n"},
		{"an indented step", "a\n  Then b", "    a\n      \\Then b\n"},
		{"text that is only like one", "Givens are fine, Then: too", "    Givens are fine, Then: too\n"},
		{"a phrase with a colon", "Now: a refund works.", "    Now: a refund works.\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := gOne(domain.Check{Status: ptr(200)})
			s.Intent = tt.intent
			text := gRoundTrip(t, s, s)
			if !strings.Contains(text, "  Scenario: t\n"+tt.line) {
				t.Errorf("want the line %q in\n%s", tt.line, text)
			}
		})
	}
}

func TestRenderSetsApiVersionAndKind(t *testing.T) {
	in := gOne(domain.Check{Status: ptr(200)})
	want := in
	in.APIVersion, in.Kind = "", ""
	gRoundTrip(t, in, want)

	for _, tt := range []struct {
		name string
		edit func(*domain.Scenario)
		want string
	}{
		{"other apiVersion", func(s *domain.Scenario) { s.APIVersion = "semar/v2" }, "apiVersion"},
		{"other kind", func(s *domain.Scenario) { s.Kind = "Script" }, "kind"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := gOne(domain.Check{Status: ptr(200)})
			tt.edit(&s)
			if _, err := scenario.Render(s, ""); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want one that names %s", err, tt.want)
			}
		})
	}
}

func TestTagsCarryTheScenarioFields(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		tags    string
	}{
		{"the default profile writes no auth tag", "default", "@id:A-1 @ticket:T-1 @surface:api"},
		{"another profile", "support-agent", "@id:A-1 @ticket:T-1 @surface:api @auth:support-agent"},
		{"no profile", "", "@id:A-1 @ticket:T-1 @surface:api @noauth"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := gOne(domain.Check{Status: ptr(200)})
			s.AuthProfile = tt.profile
			text := gRoundTrip(t, s, s)
			if !strings.Contains(text, "\n  "+tt.tags+"\n  Scenario: t\n") {
				t.Errorf("want the tag line %q in\n%s", tt.tags, text)
			}
		})
	}

	t.Run("the surface of a web scenario", func(t *testing.T) {
		s := gOne(domain.Check{Element: "x", Exists: ptr(true)})
		s.Surface = domain.SurfaceWeb
		if text := gRoundTrip(t, s, s); !strings.Contains(text, "@surface:web") {
			t.Errorf("no surface tag in\n%s", text)
		}
	})
	t.Run("an intent that ends with a line break", func(t *testing.T) {
		s := gOne(domain.Check{Status: ptr(200)})
		s.Intent = "Before: a.\nNow: b.\n"
		text := gRoundTrip(t, s, s)
		if !strings.Contains(text, " @intent-newline\n") {
			t.Errorf("want the tag @intent-newline in\n%s", text)
		}
		s.Intent = "\n"
		gRoundTrip(t, s, s)
	})
	t.Run("the feature line", func(t *testing.T) {
		s := gOne(domain.Check{Status: ptr(200)})
		for subject, want := range map[string]string{"": "Feature: T-1\n", "  Refunds \n of orders": "Feature: Refunds of orders\n"} {
			data, err := scenario.Render(s, subject)
			if err != nil || !strings.HasPrefix(string(data), want) {
				t.Errorf("subject %q: %v\n%s", subject, err, data)
			}
		}
	})
}

func TestAnAccountPrefixesAStep(t *testing.T) {
	in := gOne(domain.Check{JSONPath: "$.board", Equals: "{{o.board}}"})
	in.Steps[0].As = "carol"
	in.Steps[0].Action = "Carol moves card A to Doing"
	in.Steps[0].Expect[0].By = "model"
	text := gRoundTrip(t, in, in)
	for _, want := range []string{"\n    When as carol, Carol moves card A to Doing\n", ", by model\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("want %q in\n%s", want, text)
		}
	}
	plain := gOne(domain.Check{Status: ptr(200)})
	text = gRoundTrip(t, plain, plain)
	if strings.Contains(text, "as ") && strings.Contains(text, "When as ") || strings.Contains(text, "by model") {
		t.Errorf("an unset as or by must write nothing\n%s", text)
	}
}

func TestGivenRoundTrips(t *testing.T) {
	tests := []struct {
		name  string
		given []domain.Given
		want  []domain.Given // what Parse gives back. Nil means the same as given
	}{
		{"one entry", []domain.Given{{Fixture: "order.paid", As: "order"}}, nil},
		{"with and lease", []domain.Given{{Fixture: "order.paid", As: "order", With: map[string]string{"customer": "c-1", "amount": "1000"}, Lease: domain.LeaseExclusive}}, nil},
		{"two entries", []domain.Given{{Fixture: "customer.verified", As: "customer"}, {Fixture: "order.paid", As: "order", With: map[string]string{"customer": "{{data.customers.default}}"}}}, nil},
		{"values that look like other things", []domain.Given{{Fixture: "f.x", As: "a", With: map[string]string{"status": "true", "count": "3", "equals": "1.50", "n": "null", "d": "2026-11-14", "e": "", "m": "a: b", "o": "010"}}}, nil},
		{"values with a bar, a backslash, and a line break", []domain.Given{{Fixture: "f.x", As: "a", With: map[string]string{"a": `x | y`, "b": `c:\dir\`, "c": "line\nbreak", "d": `\n`, "e": `\|`}}}, nil},
		{"unicode and control text", []domain.Given{{Fixture: "f.x", As: "a", With: map[string]string{"c": "café   \x1b"}}}, nil},
		{"an empty with is none", []domain.Given{{Fixture: "f.x", As: "a", With: map[string]string{}}}, []domain.Given{{Fixture: "f.x", As: "a"}}},
		{"an empty list is none", []domain.Given{}, nil},
		{"no list", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := gOne(domain.Check{Status: ptr(200)})
			in.Given = tt.given
			want := in
			switch {
			case tt.want != nil:
				want.Given = tt.want
			case len(tt.given) == 0:
				want.Given = nil
			}
			text := gRoundTrip(t, in, want)
			if len(want.Given) == 0 && strings.Contains(text, "fixture") {
				t.Errorf("no given entries must write no fixture line\n%s", text)
			}
		})
	}
}

func TestGivenIsAFixtureLineWithATable(t *testing.T) {
	in := gOne(domain.Check{Status: ptr(200)})
	in.Given = []domain.Given{
		{Fixture: "order.paid", As: "order", With: map[string]string{"zeta": "1", "alpha": "two words"}, Lease: domain.LeaseExclusive},
		{Fixture: "customer.verified", As: "customer"},
	}
	text := gRoundTrip(t, in, in)
	want := "\n    Given the fixture order.paid as order with an exclusive lease\n" +
		"      | alpha | two words |\n" +
		"      | zeta  | 1         |\n" +
		"    And the fixture customer.verified as customer\n" +
		"    When do it\n"
	if !strings.Contains(text, want) {
		t.Errorf("want\n%s\nin\n%s", want, text)
	}
}

func TestRenderIsStable(t *testing.T) {
	first, err := scenario.Render(fullScenario(), "")
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		again, err := scenario.Render(fullScenario(), "")
		if err != nil || string(again) != string(first) {
			t.Fatalf("output differs between calls: %v", err)
		}
	}
}

func TestAnEmptyStepListRoundTrips(t *testing.T) {
	for name, steps := range map[string][]domain.Step{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			in := gOne(domain.Check{})
			in.Steps = steps
			want := in
			want.Steps = nil
			gRoundTrip(t, in, want)
		})
	}
}

func TestParseReadsAHandWrittenFile(t *testing.T) {
	text := `# a note for the team
Feature: Refunds
  Refunds give money back to a customer.

  @id:SHOP-9-01   @ticket:SHOP-9
  @surface:api
  Scenario: Refund
      Before, no refund.
        Now: a refund works.

    Given   the fixture order.paid as order
          | customer | c-1 |
    Given Create an order
    # step: create; POST /v1/orders
    But Create a second order
    # step: second
    When as bob,   Bob refunds
    # step:  refund ;  POST   /v1/orders/{id}/refunds
    # a note that Semar does not read
    Then the status is 200
    # ticket SHOP-9: it works

    And $.a equals 1
    # person
    * $.b exists
    # diff
`
	got, err := scenario.Parse([]byte(text), "h.feature")
	if err != nil {
		t.Fatal(err)
	}
	want := domain.Scenario{
		APIVersion: "semar/v1", Kind: "Scenario", ID: "SHOP-9-01", Title: "Refund", Ticket: "SHOP-9", Surface: domain.SurfaceAPI, AuthProfile: "default",
		Intent: "Before, no refund.\n  Now: a refund works.",
		Given:  []domain.Given{{Fixture: "order.paid", As: "order", With: map[string]string{"customer": "c-1"}}},
		Steps: []domain.Step{
			{ID: "create", Setup: true, Action: "Create an order", Hint: &domain.RequestHint{Method: "POST", Path: "/v1/orders"}},
			{ID: "second", Setup: true, Action: "Create a second order"},
			{
				ID: "refund", As: "bob", Action: "Bob refunds", Hint: &domain.RequestHint{Method: "POST", Path: "/v1/orders/{id}/refunds"},
				Expect: []domain.Expectation{
					{Check: domain.Check{Status: ptr(200)}, Source: domain.SourceTicket, Cite: "SHOP-9: it works"},
					{Check: domain.Check{JSONPath: "$.a", Equals: 1}, Source: domain.SourcePerson},
					{Check: domain.Check{JSONPath: "$.b", Exists: ptr(true)}, Source: domain.SourceDiff},
				},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
}

func TestParseErrorsNameTheLineInPlainWords(t *testing.T) {
	head := "Feature: F\n\n  @id:A-1 @ticket:T-1 @surface:api\n  Scenario: S\n"
	step := "    When do it\n    # step: s\n"
	tests := []struct {
		name string
		text string
		want []string
	}{
		{"not Gherkin at all", "this is not a scenario\n", []string{"f.feature:1:", "cannot place"}},
		{"a Scenario with no Feature", "Scenario: x\n", []string{"f.feature:1:", "cannot place"}},
		{"nothing", "", []string{"f.feature:1:", "no Feature"}},
		{"a Feature with no Scenario", "Feature: F\n", []string{"f.feature:1:", "no Scenario"}},
		{"a line that is not a step", head + "    When do it\n    # step: s\n    garbage here\n", []string{"f.feature:7:", `"    garbage here"`, "Given, When, Then, or And"}},
		{"another language", "# language: id\nFitur: F\n", []string{"f.feature:2:", "English"}},
		{"a language that does not exist", "# language: zz\nFeature: F\n", []string{"f.feature:1:", "Language not supported"}},
		{"two Scenarios", head + step + "\n  @id:A-2 @ticket:T-1 @surface:api\n  Scenario: Second\n    When x\n    # step: t\n", []string{"f.feature:9:", "one Scenario", "own file"}},
		{"a Background", "Feature: F\n  Background:\n    Given x\n  Scenario: S\n", []string{"f.feature:2:", "Background"}},
		{"a Rule", "Feature: F\n  Rule: R\n    Scenario: S\n", []string{"f.feature:2:", "Rule"}},
		{"an Examples table", head + step + "  Examples:\n    | a |\n    | 1 |\n", []string{"f.feature:7:", "Examples"}},
		{"tags on the Feature", "@id:A-1\nFeature: F\n  Scenario: S\n", []string{"f.feature:1:", "Scenario line"}},
		{"no id tag", "Feature: F\n  @ticket:T-1 @surface:api\n  Scenario: S\n" + step, []string{"f.feature:3:", "@id:<scenario id>"}},
		{"no ticket tag", "Feature: F\n  @id:A-1 @surface:api\n  Scenario: S\n" + step, []string{"f.feature:3:", "@ticket:<key>"}},
		{"no surface tag", "Feature: F\n  @id:A-1 @ticket:T-1\n  Scenario: S\n" + step, []string{"f.feature:3:", "@surface:api"}},
		{"a tag with no value", "Feature: F\n  @id: @ticket:T-1 @surface:api\n  Scenario: S\n" + step, []string{"f.feature:2:", "@id: has no value"}},
		{"a tag twice", "Feature: F\n  @id:A-1 @id:A-2 @ticket:T-1 @surface:api\n  Scenario: S\n" + step, []string{"f.feature:2:", "twice"}},
		{"a tag that is not used", "Feature: F\n  @id:A-1 @ticket:T-1 @surface:api @smoke\n  Scenario: S\n" + step, []string{"f.feature:2:", "@smoke", "is not used", "@auth:<profile>"}},
		{"auth and noauth", "Feature: F\n  @id:A-1 @ticket:T-1 @surface:api @auth:x @noauth\n  Scenario: S\n" + step, []string{"f.feature:2:", "not both"}},
		{"a step with no ID", head + "    When do it\n    Then the status is 200\n    # diff\n", []string{"f.feature:5:", "no ID", "# step: <id>"}},
		{"a step comment with no ID", head + "    When do it\n    # step:\n", []string{"f.feature:6:", "no ID"}},
		{"a step comment that is not an ID", head + "    When do it\n    # a note\n", []string{"f.feature:5:", "no ID"}},
		{"a request with no path", head + "    When do it\n    # step: s; POST\n", []string{"f.feature:6:", "<METHOD> <path>"}},
		{"the ID comment after a blank line", head + "    When do it\n\n    # step: s\n", []string{"f.feature:5:", "no ID", "directly under"}},
		{"a Then that Semar cannot read", head + step + "    Then the answer is nice\n    # diff\n", []string{"f.feature:7:", `"the answer is nice"`, "the status is 422", "does not exist", "the element <id>"}},
		{"a Then with a value that is not JSON", head + step + "    Then $.a equals yes\n    # diff\n", []string{"f.feature:7:", `"$.a equals yes"`, "double quotes"}},
		{"a Then that equals null", head + step + "    Then $.a equals null\n    # diff\n", []string{"f.feature:7:", "null cannot be checked"}},
		{"a Then with no source", head + step + "    Then the status is 200\n", []string{"f.feature:7:", "no source", "# ticket"}},
		{"a Then with an empty source comment", head + step + "    Then the status is 200\n    #\n", []string{"f.feature:8:", "no source"}},
		{"a Then before any When", head + "    Then the status is 200\n    # diff\n", []string{"f.feature:5:", "put it under a When"}},
		{"a Then under a setup step", head + "    Given do it\n    # step: s\n    Then the status is 200\n    # diff\n", []string{"f.feature:7:", "put it under a When"}},
		{"an And first", head + "    And do it\n", []string{"f.feature:5:", "Given or When"}},
		{"a table under a When", head + "    When do it\n      | a | b |\n    # step: s\n", []string{"f.feature:6:", "table", "fixture"}},
		{"a table under a Then", head + step + "    Then the status is 200\n      | a | b |\n    # diff\n", []string{"f.feature:8:", "table"}},
		{"a doc string", head + "    When do it\n      \"\"\"\n      text\n      \"\"\"\n    # step: s\n", []string{"f.feature:6:", "doc string"}},
		{"a fixture line that is not a fixture", head + "    Given the fixture order.paid\n", []string{"f.feature:5:", "Given the fixture <id> as <name>"}},
		{"a fixture parameter with three cells", head + "    Given the fixture a.b as c\n      | a | b | c |\n" + step, []string{"f.feature:6:", "two cells"}},
		{"a fixture parameter twice", head + "    Given the fixture a.b as c\n      | a | 1 |\n      | a | 2 |\n" + step, []string{"f.feature:7:", `"a" appears twice`}},
		{"a table with an uneven row", head + "    Given the fixture a.b as c\n      | a | 1 |\n      | b |\n" + step, []string{"f.feature:7:", "cell"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := scenario.Parse([]byte(tt.text), "f.feature")
			if err == nil {
				t.Fatal("Parse accepted the file")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("err = %v\nwant it to contain %q", err, w)
				}
			}
			if strings.Contains(err.Error(), "#EOF") || strings.Contains(err.Error(), "#StepLine") {
				t.Errorf("the error names the tokens of the parser, not plain words: %v", err)
			}
		})
	}
}

func TestFeatureFilesInTheRepositoryRoundTrip(t *testing.T) {
	for _, path := range []string{
		"testdata/valid.feature",
		"testdata/full.feature",
		"../../testdata/e2e/.semar/scenarios/SHOP-482/SHOP-482-01.feature",
		"../../testdata/e2e-fixtures/.semar/scenarios/SHOP-482/SHOP-482-05.feature",
		"../replay/testdata/SHOP-482-01.feature",
		"../replay/testdata/SHOP-482-05.feature",
	} {
		t.Run(path, func(t *testing.T) {
			s, err := scenario.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			text, err := scenario.Render(s, "")
			if err != nil {
				t.Fatal(err)
			}
			gRoundTrip(t, s, s)
			if path != "testdata/full.feature" && string(text) != readFile(t, path) {
				t.Errorf("%s is not what Render writes for it:\n%s", path, text)
			}
		})
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The YAML file and the Gherkin file of one scenario give the same scenario, and so the same
// expectation hash: a confirmation or a script that was made for one is valid for the other.
func TestConvertedFileIsTheSameScenario(t *testing.T) {
	old, err := scenario.LoadYAML("testdata/valid.yaml")
	if err != nil {
		t.Fatal(err)
	}
	now, err := scenario.Load("testdata/valid.feature")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(old, now) {
		t.Errorf("YAML %#v\nGherkin %#v", old, now)
	}
	if domain.ExpectHash(old) != domain.ExpectHash(now) {
		t.Error("the expectation hash differs")
	}
}

func FuzzRender(f *testing.F) {
	f.Add("title", "action", "cite", "422", "text/html", "$.a", 0.1, int64(5), "1e3", uint8(0))
	f.Add("a: b", "#x", "line\nbreak", "true", " x ", "$..b", 1e21, int64(-1), "100.0", uint8(1))
	f.Add("", "\x00", "\u2028", "010", "{{a}}", "$", math.Copysign(0, -1), int64(0), "0.1000000000000000055511151231257827", uint8(2))
	f.Add("a\xff", "x", "y", "~", "", "$.x", math.NaN(), int64(1), "-0", uint8(3))
	f.Add("\na", " a\n", "a\n\n", "- a", "\t", "$.x", math.Inf(1), int64(2), "123456789012345678901234", uint8(4))
	f.Add("| x", "When x", "Given y", "|", "\\", "$.x equals 1", 2.5, int64(3), "1", uint8(5))
	f.Fuzz(func(t *testing.T, title, action, cite, str, header, path string, fl float64, n int64, num string, shape uint8) {
		var operand any
		exact := true
		switch shape % 7 {
		case 0:
			operand = str
		case 1:
			operand = fl
		case 2:
			operand = n
		case 3:
			operand = json.Number(num)
			exact = false
		case 4:
			operand = []any{str, fl, nil, true, map[string]any{str: int(n), header: []any{}}}
		case 5:
			operand = map[string]any{str: map[string]any{header: fl}, "z": []any{str}}
		default:
			operand = nil
		}
		in := domain.Scenario{
			Title: title, Ticket: "T-1", Surface: domain.SurfaceAPI, ID: "A-1", AuthProfile: "default", Intent: str,
			Given: []domain.Given{{Fixture: "f.x", As: "a", With: map[string]string{header: str}}},
			Steps: []domain.Step{{
				ID: "s", Action: action,
				Expect: []domain.Expectation{
					{Check: domain.Check{JSONPath: path, Equals: operand}, Source: domain.SourceTicket, Cite: cite},
					{Check: domain.Check{Header: header, Contains: str}, Source: domain.SourceDiff},
					{Check: domain.Check{JSONPath: path, Greater: &fl}, Source: domain.SourceDiff},
				},
			}},
		}
		data, err := scenario.Render(in, "")
		if err != nil {
			return
		}
		got, err := scenario.Parse(data, "fuzz.feature")
		if err != nil {
			t.Fatalf("Parse refused what Render wrote: %v\n%q", err, data)
		}
		if domain.ExpectHash(got) != domain.ExpectHash(in) {
			t.Fatalf("the expectation hash changed\n%q", data)
		}
		want := in
		want.APIVersion, want.Kind = "semar/v1", "Scenario"
		if exact {
			if n, ok := operand.(int64); ok {
				want.Steps = append([]domain.Step(nil), in.Steps...)
				want.Steps[0].Expect = append([]domain.Expectation(nil), in.Steps[0].Expect...)
				want.Steps[0].Expect[0].Check.Equals = int(n)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Parse(Render(x)) = %#v, want %#v\n%q", got, want, data)
			}
		}
	})
}

func FuzzParseFeature(f *testing.F) {
	for _, p := range []string{"testdata/full.feature"} {
		data, err := os.ReadFile(p)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Add([]byte(""))
	f.Add([]byte("Feature: x\n  Scenario: y\n    Given the fixture a.b as c\n      | k | v |\n"))
	f.Add([]byte("\x00\xff: ~\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		s, err := scenario.Parse(data, "fuzz.feature")
		if err != nil {
			return
		}
		if !utf8.Valid(data) {
			return
		}
		if got := domain.ExpectHash(s); len(got) != 64 {
			t.Fatalf("a parsed scenario has hash %q, want 64 hex characters", got)
		}
		// What Parse accepts, Render writes in the form that Parse reads back to the same scenario,
		// unless Render says in a plain sentence why it cannot.
		out, err := scenario.Render(s, "")
		if err != nil {
			return
		}
		back, err := scenario.Parse(out, "fuzz.feature")
		if err != nil || !reflect.DeepEqual(back, s) {
			t.Fatalf("Render(Parse(x)) does not read back: %v\n%s", err, out)
		}
	})
}
