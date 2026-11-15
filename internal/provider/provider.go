// Package provider is the contract between a stage and the agent harness that
// runs one job, and the helpers that every driver shares.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/config"
)

type FailureClass string

const (
	UsageLimit      FailureClass = "usage_limit"
	ProviderError   FailureClass = "provider_error"
	TransportError  FailureClass = "transport_error"
	PermissionError FailureClass = "permission_error"
	ValidationError FailureClass = "validation_error"
	NotInstalled    FailureClass = "not_installed"
	Unknown         FailureClass = "unknown"
)

// Error is the failure of a job. Detail never holds the prompt or the output of the model.
type Error struct {
	Class  FailureClass
	Detail string
}

func (e *Error) Error() string { return string(e.Class) + ": " + e.Detail }

type Health struct {
	Installed bool
	Version   string
	Detail    string
}

type Job struct {
	Prompt   string
	Workdir  string          // the only directory the harness may read
	Schema   json.RawMessage // the JSON Schema of the output
	Model    string          // "" means: pass no model flag
	MaxTurns int
	Timeout  time.Duration
}

type Result struct {
	Output       json.RawMessage // one JSON object
	Model        string
	Turns        int
	InputTokens  int
	OutputTokens int
}

type Instance interface {
	ID() string
	Kind() string
	Health(ctx context.Context) Health                // no model turn (H6)
	Run(ctx context.Context, job Job) (Result, error) // a failure is a *Error
}

type Factory func(cfg config.Provider) (Instance, error)

type Registry struct {
	factories map[string]Factory
}

func NewRegistry() *Registry { return &Registry{factories: map[string]Factory{}} }

func (r *Registry) Register(kind string, f Factory) { r.factories[kind] = f }

func (r *Registry) New(cfg config.Provider) (Instance, error) {
	f, ok := r.factories[cfg.Kind]
	if !ok {
		kinds := make([]string, 0, len(r.factories))
		for k := range r.factories {
			kinds = append(kinds, k)
		}
		slices.Sort(kinds)
		return nil, fmt.Errorf("provider %q has the unknown kind %q, known kinds: %s", cfg.ID, cfg.Kind, strings.Join(kinds, ", "))
	}
	return f(cfg)
}
