package claude

import (
	"context"
	"slices"

	"github.com/jancuk/semar/internal/provider"
)

var _ provider.ModelLister = (*Driver)(nil)

// Models returns a fixed list because the harness has no command that lists models. These
// aliases are what its --model flag accepts for the latest model of each family, so the list
// does not age with each release. Every alias takes the levels of --effort, and the harness
// chooses the default.
func (d *Driver) Models(context.Context) ([]provider.Model, error) {
	families := []struct{ id, label string }{
		{"fable", "The latest Fable model"},
		{"opus", "The latest Opus model"},
		{"sonnet", "The latest Sonnet model"},
		{"haiku", "The latest Haiku model"},
	}
	out := make([]provider.Model, len(families))
	for i, f := range families {
		out[i] = provider.Model{ID: f.id, Label: f.label, Reasoning: slices.Clone(Levels)}
	}
	return out, nil
}
