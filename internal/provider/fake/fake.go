// Package fake is a provider that returns prepared JSON files, so that every stage runs without a model.
package fake

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
)

const Kind = "fake"

type Fake struct {
	id      string
	root    string
	outputs []string

	mu   sync.Mutex
	next int
	jobs []provider.Job
}

func New(cfg config.Provider, repoRoot string) (*Fake, error) {
	for _, o := range cfg.Outputs {
		if !filepath.IsLocal(o) {
			return nil, fmt.Errorf("provider %s: output %q must be a relative path inside the repository", cfg.ID, o)
		}
	}
	return &Fake{id: cfg.ID, root: repoRoot, outputs: cfg.Outputs}, nil
}

func Factory(repoRoot string) provider.Factory {
	return func(cfg config.Provider) (provider.Instance, error) { return New(cfg, repoRoot) }
}

func (f *Fake) ID() string   { return f.id }
func (f *Fake) Kind() string { return Kind }

func (f *Fake) Health(context.Context) provider.Health {
	return provider.Health{Installed: true, Version: "fake"}
}

func (f *Fake) Run(ctx context.Context, job provider.Job) (provider.Result, error) {
	if err := ctx.Err(); err != nil {
		return provider.Result{}, fmt.Errorf("run fake provider: %w", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobs = append(f.jobs, job)
	if f.next >= len(f.outputs) {
		return provider.Result{}, &provider.Error{Class: provider.ProviderError, Detail: "the fake provider has no output left"}
	}
	name := f.outputs[f.next]
	f.next++
	data, err := os.ReadFile(filepath.Join(f.root, name))
	if err != nil {
		return provider.Result{}, &provider.Error{Class: provider.ProviderError, Detail: fmt.Sprintf("read the fake output %s: %v", name, err)}
	}
	data = bytes.TrimSpace(data)
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil || obj == nil {
		return provider.Result{}, &provider.Error{Class: provider.ValidationError, Detail: fmt.Sprintf("the fake output %s is not one JSON object", name)}
	}
	return provider.Result{Output: json.RawMessage(data), Model: "fake", Turns: 1}, nil
}

// Jobs returns a copy of the jobs that Run received, in order.
func (f *Fake) Jobs() []provider.Job {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]provider.Job(nil), f.jobs...)
}
