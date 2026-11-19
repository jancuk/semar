package openapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/yamlfile"
)

// Param is a path, query, or header parameter of an operation.
type Param struct {
	Name     string
	In       string // "path", "query", or "header"
	Required bool
	Type     string   // "string", "integer", "number", "boolean", "array", or "" when the document says none
	Format   string   // the format of the value, such as "date", "date-time", or "uuid", or "" when the document says none
	Enum     []string // the allowed values, when the document lists them
	Limits
}

// Field is a property of a JSON body. In Operation.Body, Name is the property of the top level.
// In a Response, Name is a JSONPath, such as $.data.id or $.data.items[*].id.
type Field struct {
	Name     string
	Type     string // "string", "integer", "number", "boolean", "object", "array", or "" when the document says none
	Required bool
	Nullable bool
	Enum     []string
	Format   string // as Param.Format
	Limits
	// Open is set on an object that the document leaves open: it lists no property, or it allows
	// others. A name below an open field cannot be called wrong.
	Open bool
}

// Response is a status with the fields of its JSON body, when the document describes them.
type Response struct {
	Status string
	Fields []Field
}

const (
	maxRefHops    = 16
	maxSchemaDeep = 5
	maxFieldsEach = 80
	maxTreeNodes  = 3_000_000
	maxTreeDepth  = 200
)

// readDetail fills the parameters, the request body, and the responses of each operation, and the
// Key of each. It follows only references inside the document ("#/..."). A document that cannot
// be read as a tree a second time, or that is too large for it, leaves the details empty, and the
// routes and statuses that Parse read stay.
func (d *Document) readDetail(data []byte, name string) {
	d.assignKeys()
	root, err := readTree(data, name)
	if err != nil {
		return
	}
	top, ok := root.(map[string]any)
	if !ok {
		return
	}
	paths, _ := top["paths"].(map[string]any)
	store := newSchemaStore(top)
	d.schemas = store
	for i := range d.Operations {
		op := &d.Operations[i]
		item, _ := deref(top, paths[op.Path]).(map[string]any)
		raw, _ := deref(top, item[strings.ToLower(op.Method)]).(map[string]any)
		if raw == nil {
			continue
		}
		op.Params = readParams(top, item["parameters"], raw["parameters"])
		op.Body, op.BodyRequired, op.BodyKnown, op.BodyOpen = readBody(top, raw)
		op.Responses = readResponses(top, raw["responses"])
		store.add(*op, responseSchemas(top, raw["responses"]))
	}
}

// assignKeys gives each operation a name that is stable between runs: its operationId when that
// is a plain name, else the method and the path as words, such as post_v1_orders_orderId_refunds. A
// name that two operations would share gets a number, in the order of the operations.
func (d *Document) assignKeys() {
	used := map[string]bool{}
	for i := range d.Operations {
		op := &d.Operations[i]
		if validKey.MatchString(op.ID) && !used[op.ID] {
			used[op.ID] = true
			op.Key = op.ID
		}
	}
	for i := range d.Operations {
		op := &d.Operations[i]
		if op.Key != "" {
			continue
		}
		base := generatedKey(op.Method, op.Path)
		key := base
		for n := 2; used[key]; n++ {
			key = fmt.Sprintf("%s_%d", base, n)
		}
		used[key] = true
		op.Key = key
	}
}

var (
	validKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,79}$`)
	notWord  = regexp.MustCompile(`[^A-Za-z0-9]+`)
)

func generatedKey(method, path string) string {
	parts := []string{strings.ToLower(method)}
	for _, seg := range strings.Split(path, "/") {
		seg = strings.Trim(notWord.ReplaceAllString(seg, "_"), "_")
		if seg != "" {
			parts = append(parts, seg)
		}
	}
	key := strings.Join(parts, "_")
	if len(key) > 80 {
		key = key[:80]
	}
	return key
}

// readTree reads a JSON or YAML document into maps, slices, strings, booleans, and nil. Numbers
// are kept as their text. The work is bounded, so an alias cannot make a small file large.
func readTree(data []byte, name string) (any, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if t := bytes.TrimLeft(data, " \t\r\n"); len(t) > 0 && t[0] == '{' {
		var v any
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return v, nil
	}
	var root yaml.Node
	if err := yamlfile.Decode(data, name, &root); err != nil {
		return nil, err
	}
	b := budget(maxTreeNodes)
	return nodeTree(&root, &b, 0)
}

func nodeTree(n *yaml.Node, b *budget, depth int) (any, error) {
	n = resolve(n)
	if n == nil || depth > maxTreeDepth {
		return nil, nil
	}
	if err := b.spend(1); err != nil {
		return nil, err
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 1 {
			return nodeTree(n.Content[0], b, depth)
		}
		return nil, nil
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := resolve(n.Content[i])
			if k == nil || k.Kind != yaml.ScalarNode {
				continue
			}
			v, err := nodeTree(n.Content[i+1], b, depth+1)
			if err != nil {
				return nil, err
			}
			m[k.Value] = v
		}
		return m, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := nodeTree(c, b, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!null":
			return nil, nil
		case "!!bool":
			return n.Value == "true", nil
		case "!!int", "!!float":
			// A schema compares numbers with numbers, so a plain one keeps its type.
			if jsonNumber.MatchString(n.Value) {
				return json.Number(n.Value), nil
			}
		}
		return n.Value, nil
	}
	return nil, nil
}

// deref follows "$ref" values that point into the document, a bounded number of times. A
// reference that points elsewhere, or nowhere, gives nil.
func deref(root map[string]any, v any) any {
	for range maxRefHops {
		m, ok := v.(map[string]any)
		if !ok {
			return v
		}
		ref, ok := m["$ref"].(string)
		if !ok {
			return v
		}
		v = pointer(root, ref)
	}
	return nil
}

func pointer(root map[string]any, ref string) any {
	rest, ok := strings.CutPrefix(ref, "#/")
	if !ok {
		return nil
	}
	var cur any = root
	for _, part := range strings.Split(rest, "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[part]
	}
	return cur
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	}
	return ""
}

func isTrue(v any) bool { b, _ := v.(bool); return b }

func readParams(root map[string]any, lists ...any) []Param {
	var out []Param
	for _, list := range lists {
		items, _ := list.([]any)
		for _, it := range items {
			m, _ := deref(root, it).(map[string]any)
			p := Param{Name: str(m["name"]), In: str(m["in"]), Required: isTrue(m["required"])}
			if p.Name == "" || (p.In != "path" && p.In != "query" && p.In != "header") {
				continue
			}
			schema, _ := deref(root, m["schema"]).(map[string]any)
			if schema == nil {
				schema = m // Swagger 2 writes the type on the parameter
			}
			p.Type, p.Format, p.Enum, p.Limits = str(schema["type"]), str(schema["format"]), enumOf(schema), limitsOf(schema)
			if p.In == "path" {
				p.Required = true
			}
			// The operation's list comes later and wins over the path item's.
			if i := slices.IndexFunc(out, func(q Param) bool { return q.Name == p.Name && q.In == p.In }); i >= 0 {
				out[i] = p
				continue
			}
			out = append(out, p)
		}
	}
	return out
}

func enumOf(schema map[string]any) []string {
	list, _ := schema["enum"].([]any)
	var out []string
	for _, e := range list {
		if s := str(e); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func readBody(root map[string]any, op map[string]any) (fields []Field, required, known, open bool) {
	var schema any
	if rb, ok := deref(root, op["requestBody"]).(map[string]any); ok {
		required = isTrue(rb["required"])
		content, _ := rb["content"].(map[string]any)
		schema = jsonSchemaOf(root, content)
	} else {
		// Swagger 2 has a parameter in the body.
		params, _ := op["parameters"].([]any)
		for _, it := range params {
			if m, ok := deref(root, it).(map[string]any); ok && str(m["in"]) == "body" {
				schema, required = m["schema"], isTrue(m["required"])
			}
		}
	}
	if schema == nil {
		return nil, false, false, false
	}
	var seen []string
	props, req, open := properties(root, schema, &seen)
	for _, name := range sortedKeys(props) {
		f := fieldOf(root, name, props[name])
		f.Required = slices.Contains(req, name)
		fields = append(fields, f)
	}
	if len(fields) == 0 && !open {
		return nil, required, false, false
	}
	return fields, required, true, open
}

// jsonSchemaOf takes the schema of the JSON media type: application/json first, then any type
// that ends in +json.
func jsonSchemaOf(root map[string]any, content map[string]any) any {
	for _, name := range sortedKeys(content) {
		if name == "application/json" || strings.HasSuffix(name, "+json") {
			if m, ok := deref(root, content[name]).(map[string]any); ok {
				return m["schema"]
			}
		}
	}
	return nil
}

func readResponses(root map[string]any, v any) []Response {
	responses, _ := v.(map[string]any)
	var out []Response
	for _, status := range sortedKeys(responses) {
		schema := responseSchema(root, responses[status])
		if schema == nil {
			continue
		}
		var fields []Field
		var seen []string
		flatten(root, "$", schema, 0, &fields, &seen)
		if len(fields) > 0 {
			out = append(out, Response{Status: status, Fields: fields})
		}
	}
	return out
}

// responseSchema is the schema of the JSON body of one response, nil when the response has none.
func responseSchema(root map[string]any, response any) any {
	resp, _ := deref(root, response).(map[string]any)
	if content, ok := resp["content"].(map[string]any); ok {
		return jsonSchemaOf(root, content)
	}
	return resp["schema"] // Swagger 2
}

// responseSchemas maps each status to the schema of its JSON body, as the document writes it.
func responseSchemas(root map[string]any, v any) map[string]any {
	responses, _ := v.(map[string]any)
	out := map[string]any{}
	for status, resp := range responses {
		if schema := responseSchema(root, resp); schema != nil {
			out[status] = schema
		}
	}
	return out
}

// properties merges the properties of a schema, of its allOf, and of its alternatives.
func properties(root map[string]any, schema any, seen *[]string) (props map[string]any, required []string, open bool) {
	props = map[string]any{}
	var walk func(s any, depth int)
	walk = func(s any, depth int) {
		if depth > maxRefHops {
			return
		}
		// The caller guards the schema it passes. A reference inside it is guarded here.
		if m, ok := s.(map[string]any); ok && depth > 0 {
			if ref, ok := m["$ref"].(string); ok {
				if slices.Contains(*seen, ref) {
					return
				}
				*seen = append(*seen, ref)
				defer func() { *seen = (*seen)[:len(*seen)-1] }()
			}
		}
		m, _ := deref(root, s).(map[string]any)
		if m == nil {
			return
		}
		if p, ok := m["properties"].(map[string]any); ok {
			for k, v := range p {
				props[k] = v
			}
		}
		if r, ok := m["required"].([]any); ok {
			for _, x := range r {
				if s := str(x); s != "" {
					required = append(required, s)
				}
			}
		}
		if ap, ok := m["additionalProperties"]; ok && ap != false {
			open = true
		}
		for _, key := range []string{"allOf", "oneOf", "anyOf"} {
			list, _ := m[key].([]any)
			for _, sub := range list {
				walk(sub, depth+1)
			}
		}
	}
	walk(schema, 0)
	// A schema with no property of its own, such as type: object alone, takes anything.
	if len(props) == 0 {
		m, _ := deref(root, schema).(map[string]any)
		if m != nil && (str(m["type"]) == "object" || m["type"] == nil) {
			open = true
		}
	}
	return props, required, open
}

func fieldOf(root map[string]any, name string, schema any) Field {
	m, _ := deref(root, schema).(map[string]any)
	f := Field{Name: name, Type: str(m["type"]), Format: str(m["format"]), Nullable: isTrue(m["nullable"]), Enum: enumOf(m), Limits: limitsOf(m)}
	if t, ok := m["type"].([]any); ok { // OpenAPI 3.1: type: [string, "null"]
		f.Type = ""
		for _, x := range t {
			if s := str(x); s == "null" {
				f.Nullable = true
			} else if f.Type == "" {
				f.Type = s
			}
		}
	}
	if f.Type == "" {
		if _, ok := m["properties"]; ok {
			f.Type = "object"
		}
	}
	return f
}

// flatten lists the JSONPaths of an object schema, a few levels deep. An array adds [*] to the
// path of its items.
func flatten(root map[string]any, prefix string, schema any, depth int, out *[]Field, seen *[]string) {
	if depth >= maxSchemaDeep || len(*out) >= maxFieldsEach {
		return
	}
	if m, ok := schema.(map[string]any); ok {
		if ref, ok := m["$ref"].(string); ok {
			if slices.Contains(*seen, ref) {
				return
			}
			*seen = append(*seen, ref)
			defer func() { *seen = (*seen)[:len(*seen)-1] }()
		}
	}
	props, required, _ := properties(root, schema, seen)
	for _, name := range sortedKeys(props) {
		if len(*out) >= maxFieldsEach {
			return
		}
		path := prefix + "." + name
		if !simpleName.MatchString(name) {
			path = prefix + "['" + strings.ReplaceAll(name, "'", `\'`) + "']"
		}
		f := fieldOf(root, path, props[name])
		f.Required = slices.Contains(required, name)
		child, _ := deref(root, props[name]).(map[string]any)
		switch f.Type {
		case "object", "":
			var cp []string
			p, _, o := properties(root, props[name], &cp)
			f.Open = o
			if len(p) > 0 || o {
				f.Type = "object"
			}
			*out = append(*out, f)
			flatten(root, path, props[name], depth+1, out, seen)
		case "array":
			*out = append(*out, f)
			if child != nil {
				flatten(root, path+"[*]", child["items"], depth+1, out, seen)
			}
		default:
			*out = append(*out, f)
		}
	}
}

var jsonNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

var simpleName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
