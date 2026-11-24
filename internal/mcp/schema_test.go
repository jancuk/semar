package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSchemaBuilders(t *testing.T) {
	s := Object(map[string]Schema{
		"a": String("A."),
		"b": Number("B."),
		"c": Int("C."),
		"d": Bool("D."),
		"e": Enum("E.", "x", "y"),
		"f": Array("F.", String("")),
	}).Required("a")
	data, _ := json.Marshal(s)
	want := `{"properties":{"a":{"description":"A.","type":"string"},"b":{"description":"B.","type":"number"},"c":{"description":"C.","type":"integer"},"d":{"description":"D.","type":"boolean"},"e":{"description":"E.","enum":["x","y"],"type":"string"},"f":{"description":"F.","items":{"type":"string"},"type":"array"}},"required":["a"],"type":"object"}`
	if string(data) != want {
		t.Errorf("schema =\n%s\nwant\n%s", data, want)
	}
	if err := ValidatePortableSchema(s); err != nil {
		t.Error(err)
	}
	// Required and With copy, they never edit the receiver.
	base := Object(nil)
	_ = base.Required("x")
	_ = base.With("title", "t")
	if _, ok := base["required"]; ok || base["title"] != nil {
		t.Error("the builders edited their receiver")
	}
}

func TestValidatePortableSchemaRejects(t *testing.T) {
	obj := func(extra map[string]any) Schema {
		s := Schema{"type": "object", "properties": map[string]any{}}
		for k, v := range extra {
			s[k] = v
		}
		return s
	}
	prop := func(p map[string]any) Schema { return obj(map[string]any{"properties": map[string]any{"x": p}}) }
	for name, tc := range map[string]struct {
		s    Schema
		frag string
	}{
		"nil":                    {nil, "empty"},
		"top level not object":   {Schema{"type": "string"}, "type object"},
		"no properties":          {Schema{"type": "object"}, "properties"},
		"oneOf":                  {obj(map[string]any{"oneOf": []any{}}), `"oneOf"`},
		"anyOf":                  {prop(map[string]any{"anyOf": []any{map[string]any{"type": "string"}}}), `"anyOf"`},
		"allOf":                  {obj(map[string]any{"allOf": []any{}}), `"allOf"`},
		"$ref":                   {prop(map[string]any{"$ref": "#/$defs/a"}), `"$ref"`},
		"additionalProperties":   {obj(map[string]any{"additionalProperties": false}), `"additionalProperties"`},
		"nested additionalProps": {prop(map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": map[string]any{"type": "string"}}), `"additionalProperties"`},
		"in array items":         {prop(map[string]any{"type": "array", "items": map[string]any{"type": "string", "pattern": "x"}}), `"pattern"`},
		"type list":              {prop(map[string]any{"type": []any{"string", "null"}}), "needs a type"},
		"unknown type":           {prop(map[string]any{"type": "null"}), "needs a type"},
		"no type":                {prop(map[string]any{"description": "x"}), "needs a type"},
		"array without items":    {prop(map[string]any{"type": "array"}), "no items"},
		"empty enum":             {prop(map[string]any{"type": "string", "enum": []any{}}), "enum"},
		"required unknown":       {obj(map[string]any{"required": []string{"ghost"}}), "ghost"},
		"property not schema":    {obj(map[string]any{"properties": map[string]any{"x": 5}}), "not a schema"},
	} {
		err := ValidatePortableSchema(tc.s)
		if err == nil || !strings.Contains(err.Error(), tc.frag) {
			t.Errorf("%s: err = %v, want a mention of %q", name, err, tc.frag)
		}
	}
}

func TestValidatePortableSchemaAcceptsKeywordNamedProperties(t *testing.T) {
	// A property may be called like a keyword, since property names are not keywords.
	s := Object(map[string]Schema{"default": String("x"), "oneOf": String("y"), "$ref": String("z")})
	if err := ValidatePortableSchema(s); err != nil {
		t.Error(err)
	}
	full := Object(map[string]Schema{
		"n": Int("n").With("minimum", 1).With("maximum", 9).With("default", 3),
		"s": String("s").With("minLength", 1).With("maxLength", 5),
		"a": Array("a", String("")).With("minItems", 1).With("maxItems", 4),
		"w": NameValue("w"),
	}).With("title", "T")
	if err := ValidatePortableSchema(full); err != nil {
		t.Error(err)
	}
}
