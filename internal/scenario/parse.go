package scenario

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/domain"
)

func Parse(data []byte, name string) (domain.Scenario, error) {
	var s domain.Scenario
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		if errors.Is(err, io.EOF) {
			return domain.Scenario{}, fmt.Errorf("%s: the file is empty", name)
		}
		return domain.Scenario{}, decodeError(name, err)
	}
	var extra yaml.Node
	switch err := dec.Decode(&extra); {
	case err == nil:
		return domain.Scenario{}, fmt.Errorf("%s: the file holds more than one YAML document", name)
	case !errors.Is(err, io.EOF):
		return domain.Scenario{}, decodeError(name, err)
	}
	for i := range s.Steps {
		for j := range s.Steps[i].Expect {
			c := &s.Steps[i].Expect[j].Check
			for _, v := range []*any{&c.Equals, &c.NotEquals, &c.Contains} {
				n, err := jsonable(*v)
				if err != nil {
					return domain.Scenario{}, fmt.Errorf("%s: steps[%d].expect[%d]: %w", name, i, j, err)
				}
				*v = n
			}
		}
	}
	return s, nil
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

func Load(path string) (domain.Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.Scenario{}, fmt.Errorf("read scenario %s: %w", path, err)
	}
	return Parse(data, path)
}

// jsonable turns a decoded YAML value into one that encoding/json can encode.
// YAML allows mapping keys that are not strings, and JSON does not.
func jsonable(v any) (any, error) {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			n, err := jsonable(e)
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
			n, err := jsonable(e)
			if err != nil {
				return nil, err
			}
			out[ks] = n
		}
		return out, nil
	case []any:
		for i, e := range x {
			n, err := jsonable(e)
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
