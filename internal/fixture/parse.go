// Package fixture reads the fixture files of a repository and checks them. A fixture file is
// untrusted text: it is read once, through a real directory, within fixed limits.
package fixture

import (
	"bytes"
	"fmt"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/yamlfile"
)

// MaxFileSize is the most that one fixture file may hold (F1).
const MaxFileSize = 256 << 10

const tooLarge = "the file is larger than 256 KiB"

// Parse decodes one fixture file. name is what an error calls the file. Parse checks the format
// (F1, F15, and the numbers of a body); Validate checks the rest of the rules.
func Parse(data []byte, name string) (domain.Fixture, error) {
	if len(data) > MaxFileSize {
		return domain.Fixture{}, fmt.Errorf("%s: %s", name, tooLarge)
	}
	if err := refuseGiven(data, name); err != nil {
		return domain.Fixture{}, err
	}
	var fx domain.Fixture
	if err := yamlfile.Decode(data, name, &fx); err != nil {
		return domain.Fixture{}, err
	}
	// Numbers in a request body and in an until are held exactly, as in a script and in a scenario
	// check, so that the hash of a fixture is the hash of the value that a run sends.
	numbers := func(key string) (yamlfile.NumberKind, bool) {
		return yamlfile.AnyNumber, key == "json" || key == "equals"
	}
	if err := yamlfile.ExactNumbers(data, name, numbers); err != nil {
		return domain.Fixture{}, err
	}
	for _, phase := range []struct {
		name  string
		steps []domain.FixtureStep
	}{{domain.PhaseFind, fx.Find}, {domain.PhaseCreate, fx.Create}, {domain.PhaseCleanup, fx.Cleanup}} {
		for i := range phase.steps {
			st := &phase.steps[i]
			var err error
			if st.Request.JSON, err = yamlfile.JSONable(st.Request.JSON); err != nil {
				return domain.Fixture{}, fmt.Errorf("%s: %s[%d].request.json: %w", name, phase.name, i, err)
			}
			if st.Until != nil {
				if st.Until.Equals, err = yamlfile.JSONable(st.Until.Equals); err != nil {
					return domain.Fixture{}, fmt.Errorf("%s: %s[%d].until.equals: %w", name, phase.name, i, err)
				}
			}
		}
	}
	return fx, nil
}

// topKeys returns the key and value nodes of the top mapping of the file. It returns nothing for
// a file that does not decode, which Decode reports with its line.
func topKeys(data []byte) (keys, values []*yaml.Node) {
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(&doc); err != nil || len(doc.Content) == 0 {
		return nil, nil
	}
	m := resolve(doc.Content[0])
	if m.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		keys, values = append(keys, m.Content[i]), append(values, m.Content[i+1])
	}
	return keys, values
}

func resolve(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		return n.Alias
	}
	return n
}

func isKey(n *yaml.Node, name string) bool { return n.Kind == yaml.ScalarNode && n.Value == name }

// refuseGiven gives rule F15 its own message, which an unknown-key error would not.
func refuseGiven(data []byte, name string) error {
	keys, _ := topKeys(data)
	for _, k := range keys {
		if isKey(k, "given") {
			return fmt.Errorf("%s:%d: given: a fixture cannot use another fixture in this version", name, k.Line)
		}
	}
	return nil
}
