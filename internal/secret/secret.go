// Package secret resolves secret references such as "env:NAME" to values.
package secret

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

var ErrUnsupported = errors.New("secret scheme is not supported yet")

type Resolver interface {
	Resolve(ctx context.Context, ref string) (string, error)
	// Loaded returns every value resolved so far, once each, in the order first resolved.
	Loaded() []string
}

// Env resolves "env:NAME" references from a lookup function. It is safe for concurrent use.
type Env struct {
	lookup func(string) (string, bool)

	mu     sync.Mutex
	loaded []string
}

func NewEnv(lookup func(string) (string, bool)) *Env {
	return &Env{lookup: lookup}
}

// Resolve's errors name the reference and never hold a value.
func (e *Env) Resolve(ctx context.Context, ref string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("resolve %s: %w", ref, err)
	}
	scheme, name, ok := strings.Cut(ref, ":")
	if !ok {
		return "", fmt.Errorf("secret reference %q needs a scheme such as env:NAME", ref)
	}
	if scheme != "env" {
		return "", fmt.Errorf("secret reference %s: %w", ref, ErrUnsupported)
	}
	if name == "" {
		return "", fmt.Errorf("secret reference %s: the variable name is empty", ref)
	}
	v, found := e.lookup(name)
	if !found || v == "" {
		return "", fmt.Errorf("secret reference %s: the variable is not set or is empty", ref)
	}
	e.mu.Lock()
	if !slices.Contains(e.loaded, v) {
		e.loaded = append(e.loaded, v)
	}
	e.mu.Unlock()
	return v, nil
}

func (e *Env) Loaded() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string{}, e.loaded...)
}
