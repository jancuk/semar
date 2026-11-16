package cli

import "github.com/jancuk/semar/internal/domain"

// FixtureHashOf is the hash that confirm --fixture shows for the fixture file in repo.
func FixtureHashOf(repo string, id domain.FixtureID) (domain.Hash, error) {
	fx, err := loadFixtureForApproval(repo, id)
	if err != nil {
		return "", err
	}
	return domain.FixtureHash(fx), nil
}
