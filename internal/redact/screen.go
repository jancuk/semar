package redact

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/theory/jsonpath"

	"github.com/jancuk/semar/internal/config"
)

// A Screen tells whether a request that comes from outside holds a secret that Semar must not copy.
// It judges by what the redaction rules name and by the shape of a secret. Unlike a Redactor it
// holds no key and no secret of a run, because it never writes a marker: the caller drops the thing.
type Screen struct {
	headers  map[string]bool
	paths    []*jsonpath.Path
	patterns []pattern
}

// secretNames are the parts of a header, key, or variable name that mark a secret.
var secretNames = []string{
	"password", "passwd", "passcode", "pwd", "secret", "token", "apikey", "authorization", "credential",
	"cookie", "sessionid", "privatekey", "signature", "csrf", "xsrf",
}

// secretExact are names that mark a secret only when they are the whole name.
var secretExact = []string{"otp", "pin", "auth", "key", "session", "sid"}

// NewScreen reads the redaction rules of the configuration. It names the entry that does not compile.
func NewScreen(cfg config.Redact) (*Screen, error) {
	s := &Screen{headers: map[string]bool{}, patterns: slices.Clone(builtins)}
	for _, h := range slices.Concat(fixedHeaders, cfg.Headers) {
		s.headers[strings.ToLower(h)] = true
	}
	for _, p := range cfg.JSONPaths {
		path, err := jsonpath.Parse(p)
		if err != nil {
			return nil, fmt.Errorf("redact jsonPath %q: %w", p, err)
		}
		s.paths = append(s.paths, path)
	}
	for _, p := range cfg.Patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("redact pattern %q: %w", p, err)
		}
		s.patterns = append(s.patterns, pattern{re: re})
	}
	return s, nil
}

// Header reports whether the redaction rules name the header.
func (s *Screen) Header(name string) bool { return s.headers[strings.ToLower(name)] }

// Name reports whether a header, key, or variable name says that its value is a secret.
func Name(name string) bool {
	flat := strings.NewReplacer("-", "", "_", "", ".", "", " ", "").Replace(strings.ToLower(name))
	if slices.Contains(secretExact, flat) {
		return true
	}
	return slices.ContainsFunc(secretNames, func(part string) bool { return strings.Contains(flat, part) })
}

// Value reports whether text has the shape of a secret: a token, a bearer or basic credential, or a
// match of a configured pattern.
func (s *Screen) Value(text string) bool {
	for _, p := range s.patterns {
		for _, loc := range p.re.FindAllStringIndex(text, -1) {
			if loc[1] > loc[0] && (p.valid == nil || p.valid(text[loc[0]:loc[1]])) {
				return true
			}
		}
	}
	return false
}

// Paths reports whether a configured JSONPath finds a value in doc, a decoded JSON document.
func (s *Screen) Paths(doc any) bool {
	return slices.ContainsFunc(s.paths, func(p *jsonpath.Path) bool { return len(p.Select(doc)) > 0 })
}
