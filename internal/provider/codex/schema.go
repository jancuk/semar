package codex

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
)

// strictSchema reports whether the strict mode of the OpenAI API accepts the schema, which
// Codex always uses: every object lists all its properties as required and allows no others, and
// every value has a type. A schema it misjudges as strict fails with a validation error.
func strictSchema(schema json.RawMessage) bool {
	var root any
	if json.Unmarshal(schema, &root) != nil {
		return false
	}
	node, ok := root.(map[string]any)
	return ok && hasType(node, "object") && strictNode(node)
}

func strictNode(node map[string]any) bool {
	for _, key := range []string{"anyOf", "allOf", "oneOf"} {
		if list, ok := node[key].([]any); ok {
			if key != "anyOf" {
				return false
			}
			for _, item := range list {
				sub, ok := item.(map[string]any)
				if !ok || !strictNode(sub) {
					return false
				}
			}
			return true
		}
	}
	if _, ok := node["$ref"]; ok {
		return true
	}
	if _, ok := node["type"]; !ok {
		_, isEnum := node["enum"]
		_, isConst := node["const"]
		return isEnum || isConst
	}
	if hasType(node, "object") {
		if node["additionalProperties"] != false {
			return false
		}
		props, _ := node["properties"].(map[string]any)
		required, _ := node["required"].([]any)
		var names []string
		for _, r := range required {
			s, ok := r.(string)
			if !ok {
				return false
			}
			names = append(names, s)
		}
		slices.Sort(names)
		if !slices.Equal(names, slices.Sorted(maps.Keys(props))) {
			return false
		}
		for _, p := range props {
			sub, ok := p.(map[string]any)
			if !ok || !strictNode(sub) {
				return false
			}
		}
	}
	if hasType(node, "array") {
		items, ok := node["items"].(map[string]any)
		if !ok || !strictNode(items) {
			return false
		}
	}
	for _, key := range []string{"$defs", "definitions"} {
		defs, _ := node[key].(map[string]any)
		for _, d := range defs {
			sub, ok := d.(map[string]any)
			if !ok || !strictNode(sub) {
				return false
			}
		}
	}
	return true
}

func hasType(node map[string]any, want string) bool {
	switch t := node["type"].(type) {
	case string:
		return t == want
	case []any:
		return slices.Contains(t, any(want))
	}
	return false
}

// schemaPrompt asks for the shape that the strict mode could not enforce.
func schemaPrompt(schema json.RawMessage) string {
	var compact bytes.Buffer
	if json.Compact(&compact, schema) != nil {
		compact.Write(schema)
	}
	return "\n\nAnswer with one JSON object and nothing else: no prose before or after it, and no code fence. The object follows this JSON Schema:\n" + compact.String() + "\n"
}
