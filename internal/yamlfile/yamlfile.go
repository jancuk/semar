// Package yamlfile decodes one strict YAML document and reports every problem
// as "<name>:<line>: <message>".
package yamlfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

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
		return decodeError(name, data, err)
	}
	var extra yaml.Node
	switch err := dec.Decode(&extra); {
	case err == nil:
		return fmt.Errorf("%s: the file holds more than one YAML document", name)
	case !errors.Is(err, io.EOF):
		return decodeError(name, data, err)
	}
	return nil
}

func decodeError(name string, data []byte, err error) error {
	var root yaml.Node
	if len(data) > 0 {
		if yaml.Unmarshal(data, &root) != nil {
			root = yaml.Node{}
		}
	}
	return decodeErrorAt(name, &root, err)
}

func decodeErrorAt(name string, root *yaml.Node, err error) error {
	var les *yaml.LoadErrors
	if !errors.As(err, &les) {
		return fmt.Errorf("%s: %w", name, err)
	}
	errs := make([]error, len(les.Errors))
	for i, le := range les.Errors {
		key, parent := keyAt(root, le.Mark.Line, le.Mark.Column)
		errs[i] = fmt.Errorf("%s:%d: %s", name, le.Mark.Line, plainMessage(le.Message, key, parent))
	}
	return errors.Join(errs...)
}

var (
	unknownField = regexp.MustCompile(`^field (\S+) not found in type `)
	wrongType    = regexp.MustCompile("^cannot (?:construct|unmarshal) (?:!!\\w+(?: `[^`]*`)?) into (.+)$")
)

// plainMessage turns the decoder's messages that name a Go type into text about the file. key is the
// key at the place of the error and parent the key that holds it, "" where the decoder cannot say.
func plainMessage(msg, key, parent string) string {
	if m := unknownField.FindStringSubmatch(msg); m != nil {
		out := "unknown key " + m[1]
		if parent != "" {
			out += " in " + parent
		}
		return out
	}
	m := wrongType.FindStringSubmatch(msg)
	if m == nil {
		return msg
	}
	subject := "the value"
	if key != "" {
		subject = key
	}
	switch t := m[1]; {
	case t == "bool":
		return subject + " must be true or false"
	case t == "string":
		return subject + " must be text"
	case t == "time.Duration":
		return subject + " must be a duration such as 30s"
	case strings.HasPrefix(t, "[]"):
		return subject + " must be a list"
	case strings.HasPrefix(t, "map[") || strings.Contains(t, "."):
		return subject + " must be a mapping"
	case strings.HasPrefix(t, "int"), strings.HasPrefix(t, "uint"), strings.HasPrefix(t, "float"):
		return subject + " must be a number"
	}
	return subject + " has the wrong kind of value"
}

// keyAt finds the key at line and column of the document: the mapping key that starts there, or the
// key whose value starts there, and the key of the mapping that holds it.
func keyAt(n *yaml.Node, line, col int) (key, parent string) {
	var walk func(n *yaml.Node, holder string, depth int) bool
	walk = func(n *yaml.Node, holder string, depth int) bool {
		if n == nil || depth > 64 {
			return false
		}
		switch n.Kind {
		case yaml.DocumentNode, yaml.SequenceNode:
			for _, c := range n.Content {
				if n.Kind == yaml.SequenceNode && c.Kind != yaml.MappingNode && c.Kind != yaml.SequenceNode &&
					c.Line == line && c.Column == col {
					key, parent = holder, ""
					return true
				}
				if walk(c, holder, depth+1) {
					return true
				}
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				if k.Line == line && k.Column == col {
					key, parent = k.Value, holder
					return true
				}
				if v.Line == line && v.Column == col && v.Kind != yaml.MappingNode {
					key, parent = k.Value, holder
					return true
				}
				if walk(v, k.Value, depth+1) {
					return true
				}
			}
		}
		return false
	}
	walk(n, "", 0)
	return key, parent
}

// LoadNode decodes an already parsed node into v with the same strictness and
// error format as Decode.
func LoadNode(node *yaml.Node, name string, v any) error {
	if err := node.Load(v, yaml.WithKnownFields()); err != nil {
		return decodeErrorAt(name, node, err)
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
