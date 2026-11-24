package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
)

// DecodeArgs reads the arguments of a tool call into dst, a pointer to a struct. It is strict: the
// arguments must be one JSON object, an unknown key is refused, and a wrong type names the argument.
// Empty or null arguments decode as an empty object. Messages never quote a value, so a secret that
// was sent in the wrong place does not reach a log or the model.
func DecodeArgs(raw json.RawMessage, dst any) error {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || bytes.Equal(t, []byte("null")) {
		t = []byte("{}")
	}
	if t[0] != '{' {
		return errors.New("the arguments must be one JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(t))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return argProblem(err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("the arguments hold more than one JSON value")
	}
	return nil
}

func argProblem(err error) error {
	var typeErr *json.UnmarshalTypeError
	var syn *json.SyntaxError
	msg := err.Error()
	switch {
	case strings.HasPrefix(msg, "json: unknown field "):
		return fmt.Errorf("unknown argument %s", strings.TrimPrefix(msg, "json: unknown field "))
	case errors.As(err, &typeErr):
		name := typeErr.Field
		if name == "" {
			return errors.New("the arguments must be one JSON object")
		}
		return fmt.Errorf("the argument %q must be %s, not %s", name, kindName(typeErr.Type), typeErr.Value)
	case errors.As(err, &syn):
		return errors.New("the arguments are not valid JSON")
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return errors.New("the arguments are not valid JSON")
	default:
		// An error from a custom UnmarshalJSON, such as StringMap, already reads as a sentence.
		return errors.New(strings.TrimPrefix(msg, "json: "))
	}
}

func kindName(t reflect.Type) string {
	if t == nil {
		return "another type"
	}
	switch t.Kind() {
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "a boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "an integer"
	case reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.Slice, reflect.Array:
		return "an array"
	case reflect.Map, reflect.Struct:
		return "an object"
	default:
		return "another type"
	}
}

// StringMap is a map from names to strings that decodes from either form that Decision E8 allows: an
// array of {"name": k, "value": v} objects, which the schemas advertise, or a plain object
// {"k": "v"}. A value may be a string, a number, or a boolean, and is kept as text. It marshals as the
// array form, sorted by name.
type StringMap map[string]string

// UnmarshalJSON accepts the array and the object form. A duplicate or empty name is an error.
func (m *StringMap) UnmarshalJSON(data []byte) error {
	t := bytes.TrimSpace(data)
	if bytes.Equal(t, []byte("null")) {
		return nil
	}
	out := StringMap{}
	switch {
	case len(t) > 0 && t[0] == '[':
		var items []json.RawMessage
		if err := json.Unmarshal(t, &items); err != nil {
			return errors.New("the list of name and value pairs is not valid")
		}
		for i, it := range items {
			var pair map[string]json.RawMessage
			if err := json.Unmarshal(it, &pair); err != nil {
				return fmt.Errorf("item %d must be an object with name and value", i)
			}
			for k := range pair {
				if k != "name" && k != "value" {
					return fmt.Errorf("item %d has the unknown key %q, only name and value are known", i, k)
				}
			}
			var name string
			if err := json.Unmarshal(pair["name"], &name); err != nil || name == "" {
				return fmt.Errorf("item %d needs a name that is a non-empty string", i)
			}
			val, err := scalarText(pair["value"])
			if err != nil {
				return fmt.Errorf("item %d (%s) needs a value that is a string, number, or boolean", i, name)
			}
			if _, dup := out[name]; dup {
				return fmt.Errorf("the name %q appears twice", name)
			}
			out[name] = val
		}
	case len(t) > 0 && t[0] == '{':
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(t, &obj); err != nil {
			return errors.New("the object of names and values is not valid")
		}
		for k, v := range obj {
			if k == "" {
				return errors.New("a name must not be empty")
			}
			val, err := scalarText(v)
			if err != nil {
				return fmt.Errorf("the value of %q must be a string, number, or boolean", k)
			}
			out[k] = val
		}
	default:
		return errors.New("must be an array of {name, value} objects or an object")
	}
	*m = out
	return nil
}

// MarshalJSON writes the array form, sorted by name.
func (m StringMap) MarshalJSON() ([]byte, error) {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	type pair struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	out := make([]pair, 0, len(names))
	for _, k := range names {
		out = append(out, pair{k, m[k]})
	}
	return json.Marshal(out)
}

// scalarText returns a JSON string, number, or boolean as text.
func scalarText(raw json.RawMessage) (string, error) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 {
		return "", errors.New("missing")
	}
	switch c := t[0]; {
	case c == '"':
		var s string
		if err := json.Unmarshal(t, &s); err != nil {
			return "", err
		}
		return s, nil
	case c == '-', c >= '0' && c <= '9', bytes.Equal(t, []byte("true")), bytes.Equal(t, []byte("false")):
		return string(t), nil
	default:
		return "", errors.New("not a scalar")
	}
}
