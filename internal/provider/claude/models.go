package claude

import (
	"context"

	"github.com/jancuk/semar/internal/provider"
)

var _ provider.ModelLister = (*Driver)(nil)

// Models returns a fixed list because the harness has no command that lists models. These
// aliases are what its --model flag accepts for the latest model of each family, so the list
// does not age with each release.
func (d *Driver) Models(context.Context) ([]provider.Model, error) {
	return []provider.Model{
		{ID: "fable", Label: "The latest Fable model"},
		{ID: "opus", Label: "The latest Opus model"},
		{ID: "sonnet", Label: "The latest Sonnet model"},
		{ID: "haiku", Label: "The latest Haiku model"},
	}, nil
}
