// Package yamlfile decodes one strict YAML document and reports every problem
// as "<name>:<line>: <message>".
package yamlfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	yaml "go.yaml.in/yaml/v4"
)

// Decode reads exactly one YAML document into v. An unknown field, an empty
// file, and a second document are errors that name the file.
func Decode(data []byte, name string, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%s: the file is empty", name)
		}
		return decodeError(name, err)
	}
	var extra yaml.Node
	switch err := dec.Decode(&extra); {
	case err == nil:
		return fmt.Errorf("%s: the file holds more than one YAML document", name)
	case !errors.Is(err, io.EOF):
		return decodeError(name, err)
	}
	return nil
}

func decodeError(name string, err error) error {
	var les *yaml.LoadErrors
	if !errors.As(err, &les) {
		return fmt.Errorf("%s: %w", name, err)
	}
	errs := make([]error, len(les.Errors))
	for i, le := range les.Errors {
		errs[i] = fmt.Errorf("%s:%d: %s", name, le.Mark.Line, le.Message)
	}
	return errors.Join(errs...)
}

// LoadNode decodes an already parsed node into v with the same strictness and
// error format as Decode.
func LoadNode(node *yaml.Node, name string, v any) error {
	if err := node.Load(v, yaml.WithKnownFields()); err != nil {
		return decodeError(name, err)
	}
	return nil
}

// JSONable turns a decoded YAML value into one that encoding/json can encode.
// YAML allows mapping keys that are not strings, and JSON does not.
func JSONable(v any) (any, error) {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			n, err := JSONable(e)
			if err != nil {
				return nil, err
			}
			x[k] = n
		}
		return x, nil
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			ks, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("a mapping key must be a string, got %v", k)
			}
			n, err := JSONable(e)
			if err != nil {
				return nil, err
			}
			out[ks] = n
		}
		return out, nil
	case []any:
		for i, e := range x {
			n, err := JSONable(e)
			if err != nil {
				return nil, err
			}
			x[i] = n
		}
		return x, nil
	default:
		return v, nil
	}
}
