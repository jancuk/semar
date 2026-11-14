// Package secret resolves secret references such as "env:NAME" to values.
package secret

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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

var placeholder = regexp.MustCompile(`\{\{secret\.([^{}]*)\}\}`)

// maxQuoted bounds the configuration text an error quotes.
const maxQuoted = 40

// Expand replaces each {{secret.NAME}} in text with values[NAME]. Text that only looks like
// a placeholder, such as {{ secret.NAME }} or {{Secret.NAME}}, is refused instead of being
// sent as written. Its errors quote configuration text and never hold a value.
func Expand(text string, values map[string]string) (string, error) {
	if rest := placeholder.ReplaceAllString(text, ""); strings.Contains(rest, "{{") {
		_, stray, _ := strings.Cut(rest, "{{")
		stray = "{{" + stray
		if end := strings.Index(stray, "}}"); end >= 0 {
			stray = stray[:end+2]
		}
		if r := []rune(stray); len(r) > maxQuoted {
			stray = string(r[:maxQuoted]) + "..."
		}
		return "", fmt.Errorf("%s is not a placeholder: write {{secret.NAME}}, with that exact spelling and no spaces", stray)
	}
	var missing []string
	out := placeholder.ReplaceAllStringFunc(text, func(m string) string {
		v, ok := values[placeholder.FindStringSubmatch(m)[1]]
		if !ok {
			missing = append(missing, m)
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("%s names no secret of the auth profile; the accepted form is {{secret.NAME}}", strings.Join(missing, ", "))
	}
	return out, nil
}
