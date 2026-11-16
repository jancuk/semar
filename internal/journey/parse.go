// Package journey reads journey files, checks them, and turns a leg into a scenario and a script.
// A journey file is untrusted text: it is read once, through a real directory, within fixed limits.
package journey

import (
	"bytes"
	"fmt"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/yamlfile"
)

// MaxFileSize is the most that one journey file may hold (J1).
const MaxFileSize = 256 << 10

const tooLarge = "the file is larger than 256 KiB"

// Parse decodes one journey file. name is what an error calls the file. Parse checks the format
// (J1 and J9); Validate checks the rest of the rules.
func Parse(data []byte, name string) (domain.Journey, error) {
	if len(data) > MaxFileSize {
		return domain.Journey{}, fmt.Errorf("%s: %s", name, tooLarge)
	}
	if err := refuseLegGiven(data, name); err != nil {
		return domain.Journey{}, err
	}
	var j domain.Journey
	if err := yamlfile.Decode(data, name, &j); err != nil {
		return domain.Journey{}, err
	}
	if err := yamlfile.ExactNumbers(data, name, operandKind); err != nil {
		return domain.Journey{}, err
	}
	for i := range j.Legs {
		leg := &j.Legs[i]
		if w := leg.Wait; w != nil {
			n, err := yamlfile.JSONable(w.Until.Equals)
			if err != nil {
				return domain.Journey{}, fmt.Errorf("%s: legs[%d].wait.until.equals: %w", name, i, err)
			}
			w.Until.Equals = n
		}
		for k := range leg.Steps {
			st := &leg.Steps[k]
			where := fmt.Sprintf("%s: legs[%d].steps[%d]", name, i, k)
			n, err := yamlfile.JSONable(st.Request.JSON)
			if err != nil {
				return domain.Journey{}, fmt.Errorf("%s.request.json: %w", where, err)
			}
			st.Request.JSON = n
			for e := range st.Expect {
				c := &st.Expect[e].Check
				for _, v := range []*any{&c.Equals, &c.NotEquals, &c.Contains} {
					n, err := yamlfile.JSONable(*v)
					if err != nil {
						return domain.Journey{}, fmt.Errorf("%s.expect[%d]: %w", where, e, err)
					}
					*v = n
				}
			}
		}
	}
	return j, nil
}

// operandKind names the keys that hold a number, and the type each is decoded into: the operands
// of a check, the equals of a wait, and the body of a request.
func operandKind(key string) (yamlfile.NumberKind, bool) {
	switch key {
	case "equals", "notEquals", "contains", "json":
		return yamlfile.AnyNumber, true
	case "greater", "less":
		return yamlfile.FloatNumber, true
	case "status", "count":
		return yamlfile.IntNumber, true
	}
	return 0, false
}

// refuseLegGiven gives rule J9 its own message, which an unknown-key error would not. It returns
// nothing for a file that does not decode, which Decode reports with its line.
func refuseLegGiven(data []byte, name string) error {
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(&doc); err != nil || len(doc.Content) == 0 {
		return nil
	}
	top := resolve(doc.Content[0])
	legs := mapValue(top, "legs")
	if legs == nil || legs.Kind != yaml.SequenceNode {
		return nil
	}
	for i, l := range legs.Content {
		l = resolve(l)
		if l.Kind != yaml.MappingNode {
			continue
		}
		for k := 0; k+1 < len(l.Content); k += 2 {
			if key := l.Content[k]; key.Kind == yaml.ScalarNode && key.Value == "given" {
				return fmt.Errorf("%s:%d: legs[%d].given: put the fixtures of a journey under its given", name, key.Line, i)
			}
		}
	}
	return nil
}

func resolve(n *yaml.Node) *yaml.Node {
	for range 8 {
		if n.Kind != yaml.AliasNode || n.Alias == nil {
			return n
		}
		n = n.Alias
	}
	return n
}

// mapValue is the value of key in a mapping node, or nil.
func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if k := m.Content[i]; k.Kind == yaml.ScalarNode && k.Value == key {
			return resolve(m.Content[i+1])
		}
	}
	return nil
}
