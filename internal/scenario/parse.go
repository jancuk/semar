package scenario

import (
	"fmt"
	"os"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/yamlfile"
)

func Parse(data []byte, name string) (domain.Scenario, error) {
	var s domain.Scenario
	if err := yamlfile.Decode(data, name, &s); err != nil {
		return domain.Scenario{}, err
	}
	for i := range s.Steps {
		for j := range s.Steps[i].Expect {
			c := &s.Steps[i].Expect[j].Check
			for _, v := range []*any{&c.Equals, &c.NotEquals, &c.Contains} {
				n, err := yamlfile.JSONable(*v)
				if err != nil {
					return domain.Scenario{}, fmt.Errorf("%s: steps[%d].expect[%d]: %w", name, i, j, err)
				}
				*v = n
			}
		}
	}
	return s, nil
}

func Load(path string) (domain.Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.Scenario{}, fmt.Errorf("read scenario %s: %w", path, err)
	}
	return Parse(data, path)
}
