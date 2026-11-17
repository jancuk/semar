package plan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"github.com/jancuk/semar/internal/openapi"
)

// The kinds of answer to one criterion. A job gives exactly one.
const (
	KindScenario    = "scenario"
	KindManualCheck = "manualCheck"
	KindNotDrafted  = "notDrafted"
	// KindNeedsFixture says that the criterion needs data that no fixture gives, and names the
	// fixture that would give it.
	KindNeedsFixture = "needsFixture"
)

// Answer is what a job returns for one criterion. It names an operation of its packet, an account,
// and a fixture, and never writes a method, a path, a host, or the cite of the criterion: code
// supplies them from the packet. Only the fields of its kind are used, and the schema of the
// answer gives every other kind its own fields.
type Answer struct {
	Kind        string        `json:"kind"`
	Title       string        `json:"title,omitempty"`
	Intent      string        `json:"intent,omitempty"`
	Given       []AnswerGiven `json:"given,omitempty"`
	Steps       []AnswerStep  `json:"steps,omitempty"`
	ManualSteps []string      `json:"manualSteps,omitempty"`
	// Surface is where a person makes a manual check: web, mobile, or other. Code explores the web ones.
	Surface  string `json:"surface,omitempty"`
	Expected string `json:"expected,omitempty"`
	What     string `json:"what,omitempty"`
	Needs    string `json:"needs,omitempty"`
	// FixtureID, FixtureState, and FixtureOutputs name the fixture of a needsFixture answer: the ID,
	// one sentence on the state that it prepares, and the values that a step needs from it.
	FixtureID      string   `json:"fixtureId,omitempty"`
	FixtureState   string   `json:"fixtureState,omitempty"`
	FixtureOutputs []string `json:"fixtureOutputs,omitempty"`
}

type AnswerGiven struct {
	Fixture string       `json:"fixture"`
	As      string       `json:"as"`
	With    []NamedValue `json:"with"`
}

// NamedValue is a name with a value. In a path or a query, the value is a string. In a fixture's
// parameters it is a string too.
type NamedValue struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

// BodyField is one property of the JSON body of a request.
type BodyField struct {
	Field string `json:"field"`
	Value any    `json:"value"`
}

type AnswerStep struct {
	ID        string         `json:"id"`
	Setup     bool           `json:"setup"`
	As        string         `json:"as"`
	Action    string         `json:"action"`
	Operation string         `json:"operation"`
	Path      []NamedValue   `json:"path"`
	Query     []NamedValue   `json:"query"`
	Body      []BodyField    `json:"body"`
	Captures  []CaptureDraft `json:"captures"`
	Expect    []AnswerCheck  `json:"expect"`
}

// AnswerCheck is a Then line as a structured object. Subject is status, jsonpath, or header, Name
// is the JSONPath or the header name, and Operator is one of the operators of a check.
type AnswerCheck struct {
	Subject  string `json:"subject"`
	Name     string `json:"name"`
	Operator string `json:"operator"`
	Value    any    `json:"value"`
}

type envelope struct {
	Answer Answer `json:"answer"`
}

// DecodeAnswer reads the output of a job strictly: an unknown field and data after the object are
// errors, and a number keeps its text.
func DecodeAnswer(data []byte) (Answer, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	var e envelope
	if err := dec.Decode(&e); err != nil {
		return Answer{}, err
	}
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return Answer{}, errors.New("data follows the answer object")
	}
	switch e.Answer.Kind {
	case KindScenario, KindManualCheck, KindNotDrafted, KindNeedsFixture:
	default:
		return Answer{}, fmt.Errorf("the answer kind %q must be one of %s, %s, %s, %s", e.Answer.Kind, KindScenario, KindManualCheck, KindNeedsFixture, KindNotDrafted)
	}
	return e.Answer, nil
}

// The surfaces of a manual check: a page in a browser, a screen of a phone app, or anything else.
var Surfaces = []string{SurfaceWeb, SurfaceMobile, SurfaceOther}

const (
	SurfaceWeb    = "web"
	SurfaceMobile = "mobile"
	SurfaceOther  = "other"
)

// The operators of a check, as the structured Then line names them.
var (
	checkOperators = []string{"equals", "notEquals", "contains", "exists", "notExists", "greater", "less", "count", "equalsPath"}
	checkSubjects  = []string{"status", "jsonpath", "header"}
)

// AnswerSchema is the JSON Schema of an answer to this packet. It is closed: the kind is one of
// three, each with its own fields, and a step chooses its operation from the packet's operations,
// its account from the accounts, and its fixtures from the fixtures. Within an operation, a path
// parameter, a query parameter, and a body field are chosen from the ones that the document
// declares, and a body value has the type that the document gives. Every object lists all its
// properties as required and allows no others, which is the form that every harness accepts. Code
// checks what a schema cannot say, such as a required field.
func (p Packet) AnswerSchema() json.RawMessage {
	scenario := object(map[string]any{
		"kind":   enum("string", KindScenario),
		"title":  text("A short title that names the requirement, not the code."),
		"intent": text("Two sentences: what was true before the change and what is true now."),
		"given":  array(p.givenSchema(), "The fixtures that prepare the data. Leave empty when the steps create it."),
		"steps":  array(p.stepSchema(), "The requests, in order. A setup step prepares data and has no expect."),
	})
	manual := object(map[string]any{
		"kind":        enum("string", KindManualCheck),
		"title":       text("A short title."),
		"surface":     enum("string", Surfaces...),
		"manualSteps": array(text("One action of the person."), "What the person does, 1 to 20 steps."),
		"expected":    text("What the person must see."),
	})
	notDrafted := object(map[string]any{
		"kind":  enum("string", KindNotDrafted),
		"what":  text("What cannot be tested through the API."),
		"needs": text("What is missing, such as fixture <proposed id>, or an account."),
	})
	needsFixture := object(map[string]any{
		"kind":           enum("string", KindNeedsFixture),
		"what":           text("What the scenario would test, in one sentence."),
		"fixtureId":      text("The ID of the fixture, such as order.paid: lower case words with dots."),
		"fixtureState":   text("One sentence on the state that the fixture prepares, such as: a board of the default account that a second account is a member of."),
		"fixtureOutputs": array(text("The name of one value that a step needs from the fixture, such as id."), "The values that a step reads from the fixture, 1 to 16 names."),
	})
	root := object(map[string]any{"answer": map[string]any{"anyOf": []any{scenario, manual, needsFixture, notDrafted}}})
	// The parts that every operation shares are written once.
	root["$defs"] = map[string]any{"check": p.checkSchema(), "account": p.accountSchema()}
	data, err := json.Marshal(root)
	if err != nil {
		panic(fmt.Sprintf("encode the answer schema: %v", err)) // the schema holds strings and maps
	}
	return data
}

func object(props map[string]any) map[string]any {
	required := make([]string, 0, len(props))
	for k := range props {
		required = append(required, k)
	}
	sort.Strings(required)
	return map[string]any{"type": "object", "additionalProperties": false, "required": required, "properties": props}
}

func text(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func enum(typ string, values ...string) map[string]any {
	list := make([]any, len(values))
	for i, v := range values {
		list[i] = v
	}
	return map[string]any{"type": typ, "enum": list}
}

func array(items any, desc string) map[string]any {
	a := map[string]any{"type": "array", "items": items}
	if desc != "" {
		a["description"] = desc
	}
	return a
}

// anyOf is one schema when there is one, so that the schema stays small.
func anyOf(branches []any) any {
	if len(branches) == 1 {
		return branches[0]
	}
	return map[string]any{"anyOf": branches}
}

// jsonValue is any JSON scalar.
func jsonValue(desc string) map[string]any {
	return map[string]any{"description": desc, "anyOf": []any{
		map[string]any{"type": "string"}, map[string]any{"type": "number"},
		map[string]any{"type": "boolean"}, map[string]any{"type": "null"},
	}}
}

func (p Packet) accountNames() []string {
	names := make([]string, len(p.Accounts))
	for i, a := range p.Accounts {
		names[i] = a.Name
	}
	return names
}

func (p Packet) givenSchema() any {
	if len(p.Fixtures) == 0 {
		return object(map[string]any{"fixture": text("No fixture exists. Leave given empty."), "as": text("A name."), "with": array(namedValue(nil), "")})
	}
	var branches []any
	for _, f := range p.Fixtures {
		names := make([]string, len(f.Params))
		for i, pa := range f.Params {
			names[i] = pa.Name
		}
		with := array(namedValue(nil), "The parameters of the fixture. Give every one that is required.")
		if len(names) > 0 {
			with = array(namedValue(names), "The parameters of the fixture. Give every one that is required.")
		}
		branches = append(branches, object(map[string]any{
			"fixture": enum("string", string(f.ID)),
			"as":      text("A short lower case name for what the fixture gives. A step writes {{name.output}}."),
			"with":    with,
		}))
	}
	return anyOf(branches)
}

// namedValue is {name, value} with a string value. names limits the name when there are any.
func namedValue(names []string) map[string]any {
	name := map[string]any{"type": "string"}
	if len(names) > 0 {
		name = enum("string", names...)
	}
	return object(map[string]any{"name": name, "value": map[string]any{"type": "string"}})
}

// accountSchema is the account of a step.
func (p Packet) accountSchema() map[string]any {
	accounts := text("The account of the step.")
	if names := p.accountNames(); len(names) > 0 {
		accounts = enum("string", names...)
		accounts["description"] = "The account that sends the request. " + p.Account + " is the default."
	}
	return accounts
}

func (p Packet) stepSchema() any {
	accounts := map[string]any{"$ref": "#/$defs/account"}
	var branches []any
	for _, op := range p.Operations {
		branches = append(branches, object(map[string]any{
			"id":        text("A short unique name, lower case with hyphens."),
			"setup":     map[string]any{"type": "boolean", "description": "true for a step that prepares data. Such a step has an empty expect."},
			"as":        accounts,
			"action":    text("What the step does, in one sentence."),
			"operation": enum("string", op.Key),
			"path":      array(pathItems(op), "The path parameters. Each is {{name}} of an earlier capture or fixture output, or missing-{{run.id}} for an ID that cannot exist."),
			"query":     array(queryItems(op), "The query parameters."),
			"body":      array(bodyItems(op), "The fields of the JSON body."),
			"captures":  array(captureItem(op), "Values of this response that a later step uses as {{name}}."),
			"expect":    array(map[string]any{"$ref": "#/$defs/check"}, "What the response must be. The criterion decides it."),
		}))
	}
	if len(branches) == 0 {
		return object(map[string]any{"id": text("A name."), "operation": text("No operation exists. Answer notDrafted.")})
	}
	return anyOf(branches)
}

func pathItems(op PacketOperation) any {
	var names []string
	for _, pa := range op.Params {
		if pa.In == "path" {
			names = append(names, pa.Name)
		}
	}
	return namedValue(names)
}

func queryItems(op PacketOperation) any {
	var names []string
	for _, pa := range op.Params {
		if pa.In == "query" {
			names = append(names, pa.Name)
		}
	}
	return namedValue(names)
}

// bodyItems has one branch for each declared field, so that the value has the field's type.
func bodyItems(op PacketOperation) any {
	generic := object(map[string]any{"field": map[string]any{"type": "string"}, "value": jsonValue("The value.")})
	if !op.BodyKnown || op.BodyOpen || len(op.Body) == 0 {
		return generic
	}
	var branches []any
	for _, f := range op.Body {
		branches = append(branches, object(map[string]any{
			"field": enum("string", f.Name),
			"value": valueSchema(f),
		}))
	}
	return anyOf(branches)
}

// valueSchema is the schema of the value of a body field. It names the type that the document gives
// the field and accepts any scalar, because a step that expects a refusal may send a value of another
// type, which is how a validation rule is tested. Code holds a step that expects no refusal to the
// type. An object or an array is written as the text of its JSON, because a closed schema cannot hold
// a free-form object.
func valueSchema(f openapi.Field) map[string]any {
	switch f.Type {
	case "object", "array":
		return map[string]any{"type": "string", "description": "The JSON text of the " + f.Type + ", for example " + map[string]string{"object": `{"a":1}`, "array": `["a"]`}[f.Type]}
	}
	desc := "The value."
	if f.Type != "" {
		desc = "The value, " + article(f.Type) + " " + f.Type + "."
	}
	if len(f.Enum) > 0 {
		desc += " One of " + strings.Join(f.Enum, ", ") + "."
	}
	if f.Nullable {
		desc += " It may be null."
	}
	return jsonValue(desc)
}

// captureItem lets a capture read a field that a success response declares. A document that
// declares none leaves the path free.
func captureItem(op PacketOperation) any {
	var paths []string
	for _, r := range op.Responses {
		if strings.HasPrefix(r.Status, "2") {
			for _, f := range r.Fields {
				paths = append(paths, f.Name)
			}
		}
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)
	jp := map[string]any{"type": "string", "description": "A JSONPath into the response body."}
	if len(paths) > 0 {
		jp = enum("string", paths...)
	}
	return object(map[string]any{"name": text("A name for the value, lower case with underscores."), "jsonpath": jp})
}

func (p Packet) checkSchema() any {
	return object(map[string]any{
		"subject":  enum("string", checkSubjects...),
		"name":     text("For jsonpath, the JSONPath such as $.error.code. For header, the header name. For status, an empty string."),
		"operator": enum("string", checkOperators...),
		"value":    jsonValue("The value to compare. For status, the status code as a number. For exists and notExists, write null. For every other operator, never null. For count, a whole number. For equalsPath, the other JSONPath as a string."),
	})
}
