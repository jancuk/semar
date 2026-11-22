// Package redact removes secrets from a recorded action before it is stored or
// shown to an agent. Only the bytes that encode a secret change: checks run on
// the stored action, so every other byte is evidence and stays as it was.
package redact

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/theory/jsonpath"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
)

const (
	minSecretLen = 6
	minKeyLen    = 16
	maxDecodes   = 4
	maxRounds    = 4

	// A value that a JSONPath removed is kept for the rest of the run, so that a later action
	// cannot store it. The set is bounded: values of fewer than minSecretLen bytes or more than
	// maxLearnedLen bytes are not kept, and nor is any value after the first maxLearned.
	maxLearned    = 512
	maxLearnedLen = 4096
)

var fixedHeaders = []string{"Authorization", "Cookie", "Set-Cookie", "Proxy-Authorization"}

// A Redactor serves one run. Besides the secrets, it keeps every value that a configured JSONPath
// removed from an action and removes it from every later action of the run, wherever it appears.
type Redactor struct {
	paths    []*jsonpath.Path
	headers  map[string]bool
	patterns []pattern
	secrets  func() []string
	key      []byte

	mu      sync.Mutex
	learned []learnedValue // in the order they were first seen
	known   map[string]bool
}

// learnedValue is a value that a JSONPath removed, and the marker that its body showed.
type learnedValue struct{ text, marker string }

// New returns an error that names the entry when a JSONPath or a pattern does
// not compile. The key makes markers of low-entropy values, such as a one-time
// code, impossible to reverse by trying every candidate.
func New(cfg config.Redact, secrets func() []string, key []byte) (*Redactor, error) {
	if len(key) < minKeyLen {
		return nil, fmt.Errorf("redact key has %d bytes, need at least %d", len(key), minKeyLen)
	}
	r := &Redactor{headers: map[string]bool{}, secrets: secrets, key: bytes.Clone(key)}
	for _, h := range slices.Concat(fixedHeaders, cfg.Headers) {
		r.headers[strings.ToLower(h)] = true
	}
	for _, p := range cfg.JSONPaths {
		path, err := jsonpath.Parse(p)
		if err != nil {
			return nil, fmt.Errorf("redact jsonPath %q: %w", p, err)
		}
		r.paths = append(r.paths, path)
	}
	r.patterns = slices.Clone(builtins)
	for _, p := range cfg.Patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("redact pattern %q: %w", p, err)
		}
		r.patterns = append(r.patterns, pattern{re: re})
	}
	return r, nil
}

// Marker is what replaces a redacted value. It carries a short keyed hash so
// two exchanges of one run can still be compared.
func (r *Redactor) Marker(value string) string {
	mac := hmac.New(sha256.New, r.key)
	mac.Write([]byte(value))
	return "[redacted:" + hex.EncodeToString(mac.Sum(nil)[:4]) + "]"
}

func IsMarker(s string) bool {
	loc := markerRE.FindStringIndex(s)
	return loc != nil && loc[0] == 0 && loc[1] == len(s)
}

// HasMarker reports whether s holds at least one marker anywhere.
func HasMarker(s string) bool { return markerRE.MatchString(s) }

// Action returns a deep copy of a with every secret removed.
func (r *Redactor) Action(a domain.Action) domain.Action {
	if len(r.paths) > 0 {
		// The bodies are read once first, so that a value they show under a JSONPath is known
		// before the URL, the headers, and the error of this action are redacted.
		pre := r.newScrubber()
		if a.Request != nil {
			pre.body(a.Request.Body)
		}
		if a.Response != nil {
			pre.body(a.Response.Body)
		}
		r.learn(pre.masked)
	}
	s := r.newScrubber()
	out := a
	if a.Request != nil {
		req := *a.Request
		req.URL = s.text(req.URL)
		req.PathTemplate = s.text(req.PathTemplate)
		req.Headers = r.headersOf(s, req.Headers)
		req.Body = s.body(req.Body)
		out.Request = &req
	}
	if a.Response != nil {
		resp := *a.Response
		resp.URL = s.text(resp.URL)
		resp.Headers = r.headersOf(s, resp.Headers)
		resp.Body = s.body(resp.Body)
		out.Response = &resp
	}
	if a.Captures != nil {
		out.Captures = capturesOf(s, a.Captures)
	}
	if a.Web != nil {
		w := webRecord(s, *a.Web)
		out.Web = &w
	}
	if a.Mobile != nil {
		m := mobileRecord(s, *a.Mobile)
		out.Mobile = &m
	}
	if a.Doc != nil {
		d := *a.Doc
		d.Misfits = make([]domain.Misfit, len(a.Doc.Misfits))
		for i, m := range a.Doc.Misfits {
			d.Misfits[i] = domain.Misfit{Path: s.text(m.Path), Want: s.text(m.Want), Got: m.Got}
		}
		out.Doc = &d
	}
	out.Err = s.text(a.Err)
	out.Refused = s.text(a.Refused)
	out.Why = s.text(a.Why)
	r.learn(s.masked)
	return out
}

// learn keeps the values that this action's JSONPaths removed, in sorted order, so that the set
// is the same for the same actions.
func (r *Redactor) learn(masked map[string]string) {
	if len(masked) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, text := range slices.Sorted(maps.Keys(masked)) {
		if len(r.learned) >= maxLearned {
			return
		}
		if len(text) < minSecretLen || len(text) > maxLearnedLen || r.known[text] {
			continue
		}
		if r.known == nil {
			r.known = map[string]bool{}
		}
		r.known[text] = true
		r.learned = append(r.learned, learnedValue{text, masked[text]})
	}
}

func (r *Redactor) learnedValues() []learnedValue {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.learned)
}

func (r *Redactor) headersOf(s *scrubber, in map[string][]string) map[string][]string {
	if in == nil {
		return nil
	}
	out := make(map[string][]string, len(in))
	// Sorted, so that values whose names collide after redaction keep one order.
	for _, name := range slices.Sorted(maps.Keys(in)) {
		values := in[name]
		vs := make([]string, len(values))
		for i, v := range values {
			switch {
			case r.headers[strings.ToLower(name)] && !IsMarker(v):
				vs[i] = r.Marker(v)
			case r.headers[strings.ToLower(name)]:
				vs[i] = v
			default:
				vs[i] = s.text(v)
			}
		}
		redacted := s.text(name)
		if prev, ok := out[redacted]; ok {
			vs = append(prev, vs...)
		}
		out[redacted] = vs
	}
	return out
}

// capturesOf joins the values of keys that collide after redaction, as HTTP
// joins repeated fields, so that no captured value is dropped.
//
// A capture whose whole text equals a value that a configured JSONPath removed
// from a body shows that value's marker. The capture holds no path, so a capture
// that equals such a value by coincidence is masked too. That is the safe
// direction: a check on it is not evaluated, while the other error would store
// the value that the configuration says must not be stored. The caller's
// captures are not touched, so later steps still read the raw values.
func capturesOf(s *scrubber, in map[string]string) map[string]string {
	joined := make(map[string][]string, len(in))
	for _, k := range slices.Sorted(maps.Keys(in)) {
		redacted := s.text(k)
		v := in[k]
		if marker, ok := s.masked[v]; ok {
			v = marker
		}
		joined[redacted] = append(joined[redacted], v)
	}
	out := make(map[string]string, len(joined))
	for k, vs := range joined {
		out[k] = s.text(strings.Join(vs, ", "))
	}
	return out
}

type scrubber struct {
	r     *Redactor
	forms []form
	// markers maps a value learned from an earlier action to the marker its body showed.
	markers map[string]string
	// masked maps the text of each value that a JSONPath replaced, in any body
	// of the action, to the marker that the body shows.
	masked map[string]string
}

// A form is one literal way a loaded secret can appear in text. Encodings that
// percent decoding undoes are also found through the decoded views.
type form struct{ text, secret string }

func (r *Redactor) newScrubber() *scrubber {
	s := &scrubber{r: r}
	var list []string
	if r.secrets != nil {
		list = slices.Clone(r.secrets())
	}
	for _, l := range r.learnedValues() {
		if s.markers == nil {
			s.markers = map[string]string{}
		}
		if _, ok := s.markers[l.text]; !ok {
			s.markers[l.text] = l.marker
		}
		list = append(list, l.text)
	}
	for _, sec := range list {
		if len(sec) < minSecretLen {
			continue
		}
		forms := []string{
			sec,
			url.QueryEscape(sec),
			url.PathEscape(sec),
			base64.StdEncoding.EncodeToString([]byte(sec)),
			base64.RawStdEncoding.EncodeToString([]byte(sec)),
			base64.URLEncoding.EncodeToString([]byte(sec)),
			base64.RawURLEncoding.EncodeToString([]byte(sec)),
		}
		for _, f := range forms {
			if !slices.ContainsFunc(s.forms, func(e form) bool { return e.text == f }) {
				s.forms = append(s.forms, form{f, sec})
			}
		}
	}
	return s
}

// A span is a part of the source text to replace. value is the secret it
// encodes, or empty when the marker hashes the source text itself.
type span struct {
	start, end int
	value      string
}

// secretSpans finds the loaded secrets in base and in up to maxDecodes levels
// of its percent-decoded views, one chain keeping + and one reading it as a
// space, and maps every hit back to the source.
func (s *scrubber) secretSpans(base view) []span {
	if len(s.forms) == 0 {
		return nil
	}
	spans := s.hits(base)
	for _, plus := range []bool{false, true} {
		v := base
		for range maxDecodes {
			next, changed := v.percentDecoded(plus)
			if !changed {
				break
			}
			v = next
			spans = append(spans, s.hits(v)...)
		}
	}
	return spans
}

func (s *scrubber) hits(v view) []span {
	var spans []span
	for _, f := range s.forms {
		for from := 0; ; {
			i := strings.Index(v.text[from:], f.text)
			if i < 0 {
				break
			}
			start, end := v.sourceSpan(from+i, from+i+len(f.text))
			spans = append(spans, span{start, end, f.secret})
			from += i + 1
		}
	}
	return spans
}

// redact replaces what it finds in src, then looks again, because a marker can
// complete a secret together with the text before it. A text that does not
// settle is replaced as a whole.
func (s *scrubber) redact(src string, decode func(string) view, patterns bool) string {
	for round := 0; ; round++ {
		v := decode(src)
		spans := s.secretSpans(v)
		if patterns {
			spans = append(spans, s.patternSpans(v)...)
		}
		switch {
		case len(spans) == 0:
			return src
		case round == maxRounds:
			return s.r.Marker(src)
		}
		src = s.replace(src, spans)
	}
}

func (s *scrubber) text(t string) string { return s.redact(t, sourceView, true) }

func (s *scrubber) replace(t string, spans []span) string {
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end > spans[j].end
	})
	merged := []span{spans[0]}
	for _, sp := range spans[1:] {
		last := &merged[len(merged)-1]
		if sp.start >= last.end {
			merged = append(merged, sp)
			continue
		}
		last.end = max(last.end, sp.end)
		if sp.value != last.value {
			last.value = ""
		}
	}
	var b strings.Builder
	pos := 0
	for _, sp := range merged {
		b.WriteString(t[pos:sp.start])
		v := sp.value
		if v == "" {
			v = t[sp.start:sp.end]
		}
		if m, ok := s.markers[v]; ok {
			b.WriteString(m)
		} else {
			b.WriteString(s.r.Marker(v))
		}
		pos = sp.end
	}
	b.WriteString(t[pos:])
	return b.String()
}
