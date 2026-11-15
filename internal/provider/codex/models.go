package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
)

const (
	maxModels      = 100
	maxCatalogSize = 4 << 20
)

type catalogEntry struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
	Visibility  string `json:"visibility"`
	Priority    int    `json:"priority"`
}

var _ provider.ModelLister = (*Driver)(nil)

// Models reads the catalog that `codex debug models` prints from the installed binary. The
// command makes no model turn. Entries that Codex hides from its own picker are left out.
func (d *Driver) Models(ctx context.Context) ([]provider.Model, error) {
	path, perr := d.binary("")
	if perr != nil {
		return nil, errors.New(perr.Detail)
	}
	dir, err := os.MkdirTemp("", "semar-codex-models-")
	if err != nil {
		return nil, errors.New("there is no empty directory for the model list")
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	res, err := provider.RunProcess(ctx, provider.ProcessSpec{
		Path:      path,
		Args:      []string{"debug", "models"},
		Dir:       dir,
		Env:       d.env(),
		StdoutCap: maxCatalogSize,
	})
	switch {
	case errors.Is(err, provider.ErrOutputTooLarge):
		return nil, errors.New("the model list of Codex is larger than 4 MiB")
	case errors.Is(err, context.DeadlineExceeded):
		return nil, fmt.Errorf("codex debug models did not answer in %s", statusTimeout)
	case err != nil:
		return nil, errors.New("codex debug models could not be run: " + provider.Truncate(provider.Redact(err.Error())))
	case res.ExitCode != 0:
		return nil, fmt.Errorf("codex debug models ended with exit code %d: %s", res.ExitCode, provider.LastLine(res.StderrTail))
	}
	var catalog struct {
		Models []catalogEntry `json:"models"`
	}
	if json.Unmarshal(res.Stdout, &catalog) != nil {
		return nil, errors.New("codex debug models gave an answer that is not a model list")
	}
	entries := catalog.Models[:0:0]
	for _, m := range catalog.Models {
		if m.Visibility == "list" && config.CheckModel(m.Slug) == nil {
			entries = append(entries, m)
		}
	}
	slices.SortStableFunc(entries, func(a, b catalogEntry) int {
		return a.Priority - b.Priority
	})
	out := make([]provider.Model, 0, len(entries))
	for _, m := range entries {
		if len(out) == maxModels {
			break
		}
		out = append(out, provider.Model{ID: m.Slug, Label: m.DisplayName})
	}
	return out, nil
}
