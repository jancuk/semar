package mcpkit

import (
	"crypto/rand"
	"slices"
	"strings"
	"sync"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/redact"
)

// minDynamic is the shortest value that Add keeps. A shorter one would blot out ordinary text.
const minDynamic = 4

// Scrubber removes secrets from text that goes back to the model. It wraps a redactor, which knows the
// secrets that a run or a recording loaded, and a live set of values that arrive later, such as one that
// was typed through an environment variable. It is safe for concurrent use.
type Scrubber struct {
	r *redact.Redactor

	mu  sync.Mutex
	dyn []string
}

// NewScrubber makes a scrubber over r. With a nil r it makes a redactor of its own with no configuration
// and a random key, which is what a server with no repository uses. values are added at once.
func NewScrubber(r *redact.Redactor, values ...string) *Scrubber {
	s := &Scrubber{r: r}
	if s.r == nil {
		key := make([]byte, 32)
		_, _ = rand.Read(key)
		own, err := redact.New(config.Redact{}, s.snapshot, key)
		if err == nil {
			s.r = own
		}
	}
	s.Add(values...)
	return s
}

func (s *Scrubber) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.dyn)
}

// Add teaches the scrubber more secret values. A value of fewer than four bytes is ignored.
func (s *Scrubber) Add(values ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range values {
		if len(v) >= minDynamic && !slices.Contains(s.dyn, v) {
			s.dyn = append(s.dyn, v)
		}
	}
}

// Text replaces every secret in text with a marker.
func (s *Scrubber) Text(text string) string {
	if text == "" {
		return text
	}
	out := text
	if s.r != nil {
		out = s.r.Text(out)
	}
	for _, v := range s.sorted() {
		if strings.Contains(out, v) {
			out = strings.ReplaceAll(out, v, s.marker(v))
		}
	}
	return out
}

// sorted gives the dynamic values longest first, so that a value that holds another is replaced whole.
func (s *Scrubber) sorted() []string {
	d := s.snapshot()
	slices.SortFunc(d, func(a, b string) int { return len(b) - len(a) })
	return d
}

func (s *Scrubber) marker(v string) string {
	if s.r != nil {
		return s.r.Marker(v)
	}
	return "[redacted]"
}

// Leaks reports whether text shows a secret: a value that scrubbing would change.
func (s *Scrubber) Leaks(text string) bool {
	if text == "" {
		return false
	}
	return s.Text(text) != text
}
