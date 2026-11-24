package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Schema is a JSON Schema object. The builders below produce only the portable subset of Decision E8,
// which Gemini CLI, Cursor, and others accept: no oneOf, anyOf, $ref, or additionalProperties.
type Schema map[string]any

func described(typ, desc string) Schema {
	s := Schema{"type": typ}
	if desc != "" {
		s["description"] = desc
	}
	return s
}

// Object is an object schema with the given properties. Use Required to mark some as required.
func Object(props map[string]Schema) Schema {
	if props == nil {
		props = map[string]Schema{}
	}
	return Schema{"type": "object", "properties": props}
}

// String is a string schema.
func String(desc string) Schema { return described("string", desc) }

// Number is a number schema.
func Number(desc string) Schema { return described("number", desc) }

// Int is an integer schema.
func Int(desc string) Schema { return described("integer", desc) }

// Bool is a boolean schema.
func Bool(desc string) Schema { return described("boolean", desc) }

// Enum is a string schema limited to values.
func Enum(desc string, values ...string) Schema {
	s := described("string", desc)
	s["enum"] = append([]string(nil), values...)
	return s
}

// Array is an array schema of items.
func Array(desc string, items Schema) Schema {
	s := described("array", desc)
	s["items"] = items
	return s
}

// NameValue is the schema of a map such as `with` under Decision E8: an array of {name, value}
// objects. StringMap decodes it, and also a plain object.
func NameValue(desc string) Schema {
	return Array(desc, Object(map[string]Schema{
		"name":  String("The key."),
		"value": String("The value."),
	}).Required("name", "value"))
}

// Required returns a copy of s that lists names as required.
func (s Schema) Required(names ...string) Schema {
	c := make(Schema, len(s)+1)
	for k, v := range s {
		c[k] = v
	}
	c["required"] = append([]string(nil), names...)
	return c
}

// With returns a copy of s with one more keyword, such as "minimum" or "default".
func (s Schema) With(key string, v any) Schema {
	c := make(Schema, len(s)+1)
	for k, val := range s {
		c[k] = val
	}
	c[key] = v
	return c
}

var portableKeywords = map[string]bool{
	"type": true, "description": true, "title": true, "properties": true, "required": true,
	"items": true, "enum": true, "default": true, "minimum": true, "maximum": true,
	"minItems": true, "maxItems": true, "minLength": true, "maxLength": true,
}

var portableTypes = map[string]bool{
	"object": true, "string": true, "number": true, "integer": true, "boolean": true, "array": true,
}

// ValidatePortableSchema checks s against Decision E8: the input schema of a tool is an object schema
// that uses only the portable keyword subset. It rejects oneOf, anyOf, allOf, $ref, and
// additionalProperties, any other keyword outside the subset, a type that is not one plain string, a
// required name that is not a property, an enum with no value, and an array with no items.
func ValidatePortableSchema(s Schema) error {
	if s == nil {
		return fmt.Errorf("the schema is empty")
	}
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("the schema cannot be encoded: %w", err)
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("the schema is not an object: %w", err)
	}
	if v["type"] != "object" {
		return fmt.Errorf("the top-level schema must have type object")
	}
	if _, ok := v["properties"]; !ok {
		return fmt.Errorf("the top-level schema must list properties, even if there are none")
	}
	return walkSchema("$", v)
}

func walkSchema(path string, v map[string]any) error {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !portableKeywords[k] {
			return fmt.Errorf("%s uses %q, which is outside the portable schema subset (no oneOf, anyOf, $ref, or additionalProperties)", path, k)
		}
	}
	typ, hasType := v["type"].(string)
	if !hasType || !portableTypes[typ] {
		return fmt.Errorf("%s needs a type that is one of object, string, number, integer, boolean, or array", path)
	}
	if typ == "object" {
		props, _ := v["properties"].(map[string]any)
		names := make([]string, 0, len(props))
		for n := range props {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			sub, ok := props[n].(map[string]any)
			if !ok {
				return fmt.Errorf("%s.properties.%s is not a schema", path, n)
			}
			if err := walkSchema(path+"."+n, sub); err != nil {
				return err
			}
		}
		if req, ok := v["required"]; ok {
			list, ok := req.([]any)
			if !ok {
				return fmt.Errorf("%s.required must be a list of names", path)
			}
			for _, r := range list {
				name, ok := r.(string)
				if !ok {
					return fmt.Errorf("%s.required must be a list of names", path)
				}
				if _, found := props[name]; !found {
					return fmt.Errorf("%s requires %q, which is not a property", path, name)
				}
			}
		}
	}
	if typ == "array" {
		items, ok := v["items"].(map[string]any)
		if !ok {
			return fmt.Errorf("%s is an array with no items schema", path)
		}
		if err := walkSchema(path+"[]", items); err != nil {
			return err
		}
	}
	if e, ok := v["enum"]; ok {
		list, ok := e.([]any)
		if !ok || len(list) == 0 {
			return fmt.Errorf("%s.enum must hold at least one value", path)
		}
	}
	return nil
}
