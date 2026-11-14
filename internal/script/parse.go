// Package script reads script files and checks that a script belongs to its scenario.
package script

import (
	"fmt"
	"os"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/yamlfile"
)

func Parse(data []byte, name string) (domain.Script, error) {
	var s domain.Script
	if err := yamlfile.Decode(data, name, &s); err != nil {
		return domain.Script{}, err
	}
	if err := yamlfile.ExactNumbers(data, name, jsonBody); err != nil {
		return domain.Script{}, err
	}
	for i := range s.Steps {
		r := s.Steps[i].Request
		if r == nil {
			continue
		}
		n, err := yamlfile.JSONable(r.JSON)
		if err != nil {
			return domain.Script{}, fmt.Errorf("%s: steps[%d].request.json: %w", name, i, err)
		}
		r.JSON = n
	}
	return s, nil
}

func Load(path string) (domain.Script, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.Script{}, fmt.Errorf("read script %s: %w", path, err)
	}
	return Parse(data, path)
}

// jsonBody names the request body, whose numbers are sent as the decoded value.
func jsonBody(key string) (yamlfile.NumberKind, bool) {
	return yamlfile.AnyNumber, key == "json"
}
