package openapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

	"github.com/jancuk/semar/internal/domain"
)

const (
	resourceURL  = "mem:///api-document.json"
	maxEnumShown = 6
	maxEnumText  = 30
)

// schemaStore holds the response schemas of a document as one JSON Schema resource, so that the
// library reads the shared components once. It is shared by every copy of the Document.
type schemaStore struct {
	root map[string]any

	mu       sync.Mutex
	tree     map[string]any // the resource: root's components and definitions, and the schemas below "semar"
	ops      map[string]int // "METHOD path" to the number that names the operation below "semar"
	byStatus []map[string]any
	compiler *jsonschema.Compiler
	compiled map[string]*jsonschema.Schema // "<op>/<status>"; a nil value is a schema that cannot be read
	ready    bool
	broken   bool
}

func newSchemaStore(root map[string]any) *schemaStore {
	return &schemaStore{root: root, ops: map[string]int{}, compiled: map[string]*jsonschema.Schema{}}
}

func opKey(op Operation) string { return op.Method + " " + op.Path }

func (s *schemaStore) add(op Operation, byStatus map[string]any) {
	if len(byStatus) == 0 {
		return
	}
	s.ops[opKey(op)] = len(s.byStatus)
	s.byStatus = append(s.byStatus, byStatus)
}

// Validate checks a JSON body against the schema that the document gives op for the status. checked
// is false when the document has no schema for the status, or the schema cannot be read, and then
// nothing is wrong. Misfits are in path order, with no more than limit of them; total counts all of them.
func (d Document) Validate(op Operation, status int, body []byte, limit int) (misfits []domain.Misfit, total int, checked bool) {
	s := d.schemas
	if s == nil {
		return nil, 0, false
	}
	sch := s.schemaFor(op, status)
	if sch == nil {
		return nil, 0, false
	}
	var inst any
	trimmed := bytes.TrimSpace(body)
	switch {
	case len(trimmed) == 0:
		return []domain.Misfit{{Path: "$", Want: "a JSON body", Got: "empty"}}, 1, true
	default:
		var err error
		if inst, err = jsonschema.UnmarshalJSON(bytes.NewReader(trimmed)); err != nil {
			return []domain.Misfit{{Path: "$", Want: "a JSON body", Got: "text"}}, 1, true
		}
	}
	err := sch.Validate(inst)
	if err == nil {
		return nil, 0, true
	}
	ve, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return nil, 0, false
	}
	var all []domain.Misfit
	collect(ve, inst, &all)
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Path != all[j].Path {
			return all[i].Path < all[j].Path
		}
		return all[i].Want < all[j].Want
	})
	all = slices.Compact(all)
	if len(all) == 0 {
		all = []domain.Misfit{{Path: "$", Want: "a body that fits its schema", Got: typeOf(inst)}}
	}
	total = len(all)
	if len(all) > limit {
		all = all[:limit]
	}
	return all, total, true
}

// schemaFor compiles the schema for the status of op: the exact status, then its range such as
// 4XX, then default.
func (s *schemaStore) schemaFor(op Operation, status int) *jsonschema.Schema {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.ops[opKey(op)]
	if !ok {
		return nil
	}
	exact := strconv.Itoa(status)
	rng := strconv.Itoa(status/100) + "XX"
	var key string
	for k := range s.byStatus[n] {
		switch {
		case k == exact:
			key = k
		case key != exact && strings.EqualFold(k, rng):
			key = k
		case key == "" && k == "default":
			key = k
		}
	}
	if key == "" {
		return nil
	}
	cacheKey := strconv.Itoa(n) + "/" + key
	if sch, seen := s.compiled[cacheKey]; seen {
		return sch
	}
	if !s.prepare() {
		return nil
	}
	sch, err := s.compiler.Compile(resourceURL + "#/semar/" + strconv.Itoa(n) + "/" + pointerPart(key))
	if err != nil {
		sch = nil
	}
	s.compiled[cacheKey] = sch
	return sch
}

// prepare builds the resource once. The caller holds the lock.
func (s *schemaStore) prepare() bool {
	if s.ready {
		return !s.broken
	}
	s.ready = true
	tree := map[string]any{}
	for _, k := range []string{"components", "definitions"} {
		if v, ok := s.root[k]; ok {
			tree[k] = v
		}
	}
	semar := make(map[string]any, len(s.byStatus))
	for i, by := range s.byStatus {
		semar[strconv.Itoa(i)] = by
	}
	tree["semar"] = semar
	convertDocument(tree)
	c := jsonschema.NewCompiler()
	if err := c.AddResource(resourceURL, tree); err != nil {
		s.broken = true
		return false
	}
	s.tree, s.compiler = tree, c
	return true
}

func pointerPart(k string) string {
	return strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
}

// convertDocument turns the schemas of an OpenAPI 3.0 or a Swagger 2 document into the form that
// the library reads. Only schemas are visited: an example or a property that is called nullable is not a keyword.
func convertDocument(tree map[string]any) {
	if c, ok := tree["components"].(map[string]any); ok {
		for _, group := range []string{"schemas"} {
			convertMap(c[group])
		}
	}
	convertMap(tree["definitions"])
	semar, _ := tree["semar"].(map[string]any)
	for _, by := range semar {
		convertMap(by)
	}
}

func convertMap(v any) {
	m, _ := v.(map[string]any)
	for _, s := range m {
		convertSchema(s, 0)
	}
}

const maxConvertDepth = 64

func convertSchema(v any, depth int) {
	m, ok := v.(map[string]any)
	if !ok || depth > maxConvertDepth {
		return
	}
	for _, k := range []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"} {
		if sub, ok := m[k].(map[string]any); ok {
			for _, s := range sub {
				convertSchema(s, depth+1)
			}
		}
	}
	for _, k := range []string{"items", "additionalProperties", "additionalItems", "not", "if", "then", "else", "contains", "propertyNames", "unevaluatedItems", "unevaluatedProperties"} {
		switch sub := m[k].(type) {
		case map[string]any:
			convertSchema(sub, depth+1)
		case []any:
			for _, s := range sub {
				convertSchema(s, depth+1)
			}
		}
	}
	for _, k := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
		if sub, ok := m[k].([]any); ok {
			for _, s := range sub {
				convertSchema(s, depth+1)
			}
		}
	}

	if _, ok := m["required"].(bool); ok {
		delete(m, "required") // Swagger 2 and some documents write it on the property
	}
	if t, ok := m["type"].(string); ok && t == "file" {
		delete(m, "type")
	}
	for _, pair := range [][2]string{{"exclusiveMinimum", "minimum"}, {"exclusiveMaximum", "maximum"}} {
		if b, ok := m[pair[0]].(bool); ok {
			delete(m, pair[0])
			if lim, has := m[pair[1]]; b && has {
				m[pair[0]] = lim
				delete(m, pair[1])
			}
		}
	}
	nullable := isTrue(m["nullable"]) || isTrue(m["x-nullable"])
	delete(m, "nullable")
	delete(m, "x-nullable")
	if !nullable {
		return
	}
	switch t := m["type"].(type) {
	case string:
		m["type"] = []any{t, "null"}
		addNullToEnum(m)
	case []any:
		if !slices.Contains(t, any("null")) {
			m["type"] = append(t, "null")
		}
		addNullToEnum(m)
	default:
		inner := make(map[string]any, len(m))
		for k, val := range m {
			inner[k] = val
		}
		clear(m)
		m["anyOf"] = []any{map[string]any{"type": "null"}, inner}
	}
}

func addNullToEnum(m map[string]any) {
	if e, ok := m["enum"].([]any); ok && !slices.Contains(e, nil) {
		m["enum"] = append(e, nil)
	}
}

// collect turns the leaves of an error into misfits. A failed anyOf or oneOf is one misfit, because
// its alternatives each fail for a reason that does not say which one was meant.
func collect(ve *jsonschema.ValidationError, inst any, out *[]domain.Misfit) {
	here := func(want string) {
		path, got := locate(inst, ve.InstanceLocation)
		*out = append(*out, domain.Misfit{Path: path, Want: want, Got: got})
	}
	switch k := ve.ErrorKind.(type) {
	case *kind.Group, *kind.Schema, *kind.Reference, *kind.AllOf:
		for _, c := range ve.Causes {
			collect(c, inst, out)
		}
	case *kind.AnyOf:
		here("a value that fits one of its alternatives")
	case *kind.OneOf:
		if len(k.Subschemas) > 1 {
			here("a value that fits exactly one of its alternatives, and it fits more")
		} else {
			here("a value that fits one of its alternatives")
		}
	case *kind.Type:
		here(strings.Join(k.Want, " or "))
	case *kind.Required:
		for _, name := range k.Missing {
			path, _ := locate(inst, ve.InstanceLocation)
			*out = append(*out, domain.Misfit{Path: path + segment(name), Want: "required", Got: "missing"})
		}
	case *kind.Enum:
		here("one of " + listText(k.Want))
	case *kind.Const:
		here("the value " + valueText(k.Want))
	case *kind.AdditionalProperties:
		here("no property beyond those it lists")
	case *kind.MinLength:
		here(fmt.Sprintf("at least %d characters", k.Want))
	case *kind.MaxLength:
		here(fmt.Sprintf("at most %d characters", k.Want))
	case *kind.MinItems:
		here(fmt.Sprintf("at least %d items", k.Want))
	case *kind.MaxItems:
		here(fmt.Sprintf("at most %d items", k.Want))
	case *kind.MinProperties:
		here(fmt.Sprintf("at least %d properties", k.Want))
	case *kind.MaxProperties:
		here(fmt.Sprintf("at most %d properties", k.Want))
	case *kind.Minimum:
		here("a value of at least " + ratText(k.Want))
	case *kind.Maximum:
		here("a value of at most " + ratText(k.Want))
	case *kind.ExclusiveMinimum:
		here("a value above " + ratText(k.Want))
	case *kind.ExclusiveMaximum:
		here("a value below " + ratText(k.Want))
	case *kind.MultipleOf:
		here("a multiple of " + ratText(k.Want))
	case *kind.Pattern:
		here("text that matches its pattern")
	case *kind.UniqueItems:
		here("items that are all different")
	case *kind.Not:
		here("a value that it excludes")
	default:
		if len(ve.Causes) > 0 {
			for _, c := range ve.Causes {
				collect(c, inst, out)
			}
			return
		}
		here("a value that fits its schema")
	}
}

// locate gives the path of the location and the JSON type of the value there. A name that is not
// a plain word is shown as *, so a key that carries data never reaches the evidence.
func locate(inst any, loc []string) (path, got string) {
	path = "$"
	cur := inst
	for _, seg := range loc {
		switch c := cur.(type) {
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(c) {
				return path + "." + "*", "unknown"
			}
			path += "[" + seg + "]"
			cur = c[i]
		case map[string]any:
			path += segment(seg)
			cur = c[seg]
		default:
			return path, typeOf(cur)
		}
	}
	return path, typeOf(cur)
}

func segment(name string) string {
	if simpleName.MatchString(name) && len(name) <= 40 {
		return "." + name
	}
	return ".*"
}

// typeOf is the JSON type of a decoded value.
func typeOf(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case json.Number:
		if strings.ContainsAny(x.String(), ".eE") {
			return "number"
		}
		return "integer"
	case float64:
		if x == float64(int64(x)) {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "value"
}

func ratText(r *big.Rat) string {
	if r.IsInt() {
		return r.Num().String()
	}
	return strings.TrimRight(strings.TrimRight(r.FloatString(6), "0"), ".")
}

func valueText(v any) string {
	text := fmt.Sprint(v)
	if len(text) > maxEnumText {
		text = text[:maxEnumText] + "..."
	}
	return text
}

func listText(vals []any) string {
	var parts []string
	for i, v := range vals {
		if i == maxEnumShown {
			parts = append(parts, "...")
			break
		}
		parts = append(parts, valueText(v))
	}
	return strings.Join(parts, ", ")
}
