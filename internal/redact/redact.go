// Package redact removes secrets from a recorded action before it is stored or
// shown to an agent.
package redact

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/theory/jsonpath"
	"github.com/theory/jsonpath/spec"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
)

const (
	minSecretLen = 6
	maxDecodes   = 4
)

var (
	fixedHeaders = []string{"Authorization", "Cookie", "Set-Cookie", "Proxy-Authorization"}
	markerRE     = regexp.MustCompile(`\[redacted:[0-9a-f]{8}\]`)
	builtinRE    = []*regexp.Regexp{
		regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*`),
		regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9\-._~+/]+=*`),
	}
)

type Redactor struct {
	paths    []*jsonpath.Path
	headers  map[string]bool
	patterns []*regexp.Regexp
	secrets  func() []string
}

// New returns an error that names the entry when a JSONPath or a pattern does not compile.
func New(cfg config.Redact, secrets func() []string) (*Redactor, error) {
	r := &Redactor{headers: map[string]bool{}, secrets: secrets}
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
	r.patterns = slices.Clone(builtinRE)
	for _, p := range cfg.Patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("redact pattern %q: %w", p, err)
		}
		r.patterns = append(r.patterns, re)
	}
	return r, nil
}

// Marker is what replaces a redacted value. It carries a short stable hash so
// two exchanges can still be compared.
func Marker(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "[redacted:" + hex.EncodeToString(sum[:4]) + "]"
}

func IsMarker(s string) bool {
	loc := markerRE.FindStringIndex(s)
	return loc != nil && loc[0] == 0 && loc[1] == len(s)
}

// Action returns a deep copy of a with every secret removed.
func (r *Redactor) Action(a domain.Action) domain.Action {
	s := r.newScrubber()
	out := a
	if a.Request != nil {
		req := *a.Request
		req.URL = s.text(req.URL)
		req.Headers = r.headersOf(s, req.Headers)
		req.Body = s.body(req.Body)
		out.Request = &req
	}
	if a.Response != nil {
		resp := *a.Response
		resp.Headers = r.headersOf(s, resp.Headers)
		resp.Body = s.body(resp.Body)
		out.Response = &resp
	}
	if a.Captures != nil {
		out.Captures = make(map[string]string, len(a.Captures))
		for k, v := range a.Captures {
			out.Captures[k] = s.text(v)
		}
	}
	out.Err = s.text(a.Err)
	out.Refused = s.text(a.Refused)
	return out
}

func (r *Redactor) headersOf(s *scrubber, in map[string][]string) map[string][]string {
	if in == nil {
		return nil
	}
	out := make(map[string][]string, len(in))
	for name, values := range in {
		vs := make([]string, len(values))
		for i, v := range values {
			switch {
			case r.headers[strings.ToLower(name)] && !IsMarker(v):
				vs[i] = Marker(v)
			case r.headers[strings.ToLower(name)]:
				vs[i] = v
			default:
				vs[i] = s.text(v)
			}
		}
		out[name] = vs
	}
	return out
}

type scrubber struct {
	r     *Redactor
	forms []form
}

// A form is one way a loaded secret can appear in text.
type form struct{ text, secret string }

func (r *Redactor) newScrubber() *scrubber {
	s := &scrubber{r: r}
	var list []string
	if r.secrets != nil {
		list = r.secrets()
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

type span struct {
	start, end int
	value      string
}

func (s *scrubber) secretSpans(t string) []span {
	var spans []span
	for _, f := range s.forms {
		for from := 0; ; {
			i := strings.Index(t[from:], f.text)
			if i < 0 {
				break
			}
			spans = append(spans, span{from + i, from + i + len(f.text), f.secret})
			from += i + 1
		}
	}
	return spans
}

// patternSpans looks for patterns only outside the given spans and outside
// existing markers, so a second pass finds nothing new.
func (s *scrubber) patternSpans(t string, taken []span) []span {
	var protected []span
	protected = append(protected, taken...)
	for _, loc := range markerRE.FindAllStringIndex(t, -1) {
		protected = append(protected, span{start: loc[0], end: loc[1]})
	}
	sort.Slice(protected, func(i, j int) bool { return protected[i].start < protected[j].start })

	var spans []span
	pos := 0
	scan := func(from, to int) {
		if from >= to {
			return
		}
		seg := t[from:to]
		for _, re := range s.r.patterns {
			for _, loc := range re.FindAllStringIndex(seg, -1) {
				if loc[1] > loc[0] {
					spans = append(spans, span{from + loc[0], from + loc[1], ""})
				}
			}
		}
	}
	for _, p := range protected {
		scan(pos, p.start)
		pos = max(pos, p.end)
	}
	scan(pos, len(t))
	return spans
}

func replaceSpans(t string, spans []span) string {
	if len(spans) == 0 {
		return t
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end > spans[j].end
	})
	merged := []span{spans[0]}
	for _, sp := range spans[1:] {
		last := &merged[len(merged)-1]
		if sp.start < last.end {
			last.end = max(last.end, sp.end)
			last.value = ""
			continue
		}
		merged = append(merged, sp)
	}
	var b strings.Builder
	pos := 0
	for _, sp := range merged {
		b.WriteString(t[pos:sp.start])
		v := sp.value
		if v == "" {
			v = t[sp.start:sp.end]
		}
		b.WriteString(Marker(v))
		pos = sp.end
	}
	b.WriteString(t[pos:])
	return b.String()
}

// text redacts one string. When a percent-decoded form of it holds a secret,
// that decoded text is what gets redacted, because a sender may encode
// characters, such as a hyphen, that Go's URL escaping leaves alone.
func (s *scrubber) text(t string) string {
	found, cur := t, t
	for range maxDecodes {
		d, err := url.PathUnescape(cur)
		if err != nil || d == cur {
			break
		}
		cur = d
		if len(s.secretSpans(cur)) > 0 {
			found = cur
		}
	}
	secrets := s.secretSpans(found)
	return s.secretsOnly(replaceSpans(found, slices.Concat(secrets, s.patternSpans(found, secrets))))
}

// secretsOnly repeats until no secret is left, because a marker can complete a
// secret together with the text before it. A text that never settles is
// replaced as a whole.
func (s *scrubber) secretsOnly(t string) string {
	for range maxDecodes {
		spans := s.secretSpans(t)
		if len(spans) == 0 {
			return t
		}
		t = replaceSpans(t, spans)
	}
	if len(s.secretSpans(t)) > 0 {
		return Marker(t)
	}
	return t
}

// body redacts a body. JSON is walked value by value and stays valid JSON.
func (s *scrubber) body(b []byte) []byte {
	if b == nil {
		return nil
	}
	if !json.Valid(b) {
		return []byte(s.text(string(b)))
	}
	var root any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return []byte(s.text(string(b)))
	}
	before, err := encode(root)
	if err != nil {
		return jsonMarker(b)
	}
	root = s.walk(s.applyPaths(root))
	out, err := encode(root)
	if err != nil {
		return jsonMarker(b)
	}
	// A secret can still sit in an object key, or in the escaped text of the
	// original, so the encoded result is checked as plain text once more.
	if final := s.secretsOnly(string(out)); final != string(out) {
		if !json.Valid([]byte(final)) {
			return jsonMarker(b)
		}
		return []byte(final)
	}
	// Untouched input keeps its bytes, unless the original text carries a secret
	// that decoding hid, such as an invalid UTF-8 byte inside a string.
	if bytes.Equal(before, out) && s.secretsOnly(string(b)) == string(b) {
		return bytes.Clone(b)
	}
	return out
}

func jsonMarker(b []byte) []byte {
	out, _ := encode(Marker(string(b)))
	return out
}

func encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

func (s *scrubber) walk(v any) any {
	switch x := v.(type) {
	case string:
		return s.text(x)
	case []any:
		for i := range x {
			x[i] = s.walk(x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = s.walk(x[k])
		}
	}
	return v
}

func (s *scrubber) applyPaths(root any) any {
	type edit struct {
		path   spec.NormalizedPath
		marker string
	}
	var edits []edit
	for _, p := range s.r.paths {
		for _, n := range p.SelectLocated(root) {
			edits = append(edits, edit{n.Path, markerOf(n.Node)})
		}
	}
	for _, e := range edits {
		root = setAt(root, e.path, e.marker)
	}
	return root
}

func markerOf(node any) string {
	if str, ok := node.(string); ok {
		if IsMarker(str) {
			return str
		}
		return Marker(str)
	}
	enc, err := encode(node)
	if err != nil {
		return Marker(fmt.Sprint(node))
	}
	return Marker(string(enc))
}

// setAt replaces the value at path. A path that no longer resolves, because an
// enclosing value was replaced first, is skipped.
func setAt(root any, path spec.NormalizedPath, value string) any {
	if len(path) == 0 {
		return value
	}
	cur := root
	for i, sel := range path {
		last := i == len(path)-1
		switch c := cur.(type) {
		case map[string]any:
			name, ok := sel.(spec.Name)
			if !ok {
				return root
			}
			if last {
				c[string(name)] = value
				return root
			}
			cur = c[string(name)]
		case []any:
			idx, ok := sel.(spec.Index)
			if !ok || int(idx) < 0 || int(idx) >= len(c) {
				return root
			}
			if last {
				c[idx] = value
				return root
			}
			cur = c[idx]
		default:
			return root
		}
	}
	return root
}
