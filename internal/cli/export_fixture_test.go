package cli

import (
	"fmt"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/fixture"
)

// FixtureHashOf is the hash of the fixture file in repo as the catalogue reads it. confirm --fixture
// must show and approve this hash.
func FixtureHashOf(repo string, id domain.FixtureID) (domain.Hash, error) {
	c, err := fixture.Load(repo, nil)
	if err != nil {
		return "", err
	}
	if len(c.Problems) > 0 {
		return "", fmt.Errorf("fixture files have problems: %v", c.Problems)
	}
	f, ok := c.Get(id)
	if !ok {
		return "", fmt.Errorf("fixture %s is not in the catalogue", id)
	}
	return domain.FixtureHash(f), nil
}
