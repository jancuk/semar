// Package effects decides whether one HTTP request may be sent to the test environment.
package effects

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/hostrule"
)

type Class int

const (
	Read Class = iota
	Write
)

type RefusalKind string

const (
	HostNotAllowed   RefusalKind = "host_not_allowed"
	MethodNotAllowed RefusalKind = "method_not_allowed"
	Denied           RefusalKind = "denied"
	ReadOnly         RefusalKind = "read_only"
)

type Request struct {
	Method string
	URL    *url.URL
}

type Refusal struct {
	Kind   RefusalKind
	Detail string
}

func (r *Refusal) Error() string { return strings.ToLower(string(r.Kind) + ": " + r.Detail) }

type pattern struct{ method, path, folded string }

type Policy struct {
	hosts    []string
	methods  []string
	readOnly bool
	deny     []pattern
	once     []pattern
	reads    []pattern
}

func New(env config.Environment) (*Policy, error) {
	p := &Policy{hosts: env.Hosts}
	switch env.Effects.Mode {
	case "test_data":
	case "read_only":
		p.readOnly = true
	default:
		return nil, fmt.Errorf("effects mode %q must be test_data or read_only", env.Effects.Mode)
	}
	for _, m := range env.Methods {
		p.methods = append(p.methods, strings.ToUpper(m))
	}
	for i, h := range env.Hosts {
		if err := hostrule.CheckEntry(h); err != nil {
			return nil, fmt.Errorf("hosts[%d]: %w", i, err)
		}
	}
	var err error
	if p.deny, err = compile("deny", env.Effects.Deny); err != nil {
		return nil, err
	}
	if p.once, err = compile("once", env.Effects.Once); err != nil {
		return nil, err
	}
	if p.reads, err = compile("reads", env.Effects.Reads); err != nil {
		return nil, err
	}
	return p, nil
}

func compile(list string, in []config.OpPattern) ([]pattern, error) {
	out := make([]pattern, len(in))
	for i, op := range in {
		if !doublestar.ValidatePattern(op.Path) {
			return nil, fmt.Errorf("effects.%s[%d]: path pattern %q does not compile", list, i, op.Path)
		}
		out[i] = pattern{method: strings.ToUpper(op.Method), path: op.Path, folded: trimSlash(fold(op.Path))}
	}
	return out, nil
}

func (p *Policy) Classify(r Request) Class {
	method := strings.ToUpper(r.Method)
	if method == "GET" || method == "HEAD" {
		return Read
	}
	if matches(p.reads, method, r.URL, false) {
		return Read
	}
	return Write
}

func (p *Policy) Once(r Request) bool {
	return matches(p.once, strings.ToUpper(r.Method), r.URL, true)
}

// Admit returns nil when the request may be sent, or a *Refusal. The order is
// scheme and host, method, deny list, then mode.
func (p *Policy) Admit(r Request) error {
	if !hostrule.Allowed(r.URL, p.hosts) {
		return &Refusal{HostNotAllowed, "the scheme or host of the request is not one the environment lists"}
	}
	method := strings.ToUpper(r.Method)
	if !contains(p.methods, method) {
		return &Refusal{MethodNotAllowed, fmt.Sprintf("method %s is not in the environment methods", method)}
	}
	if !pathIsNormal(r.URL) {
		return &Refusal{Denied, "the path is not normal: it holds a dot segment, an empty segment, or an encoded slash"}
	}
	if matches(p.deny, method, r.URL, true) {
		return &Refusal{Denied, fmt.Sprintf("%s %s matches the deny list", method, r.URL.Path)}
	}
	if p.readOnly && p.Classify(r) == Write {
		return &Refusal{ReadOnly, fmt.Sprintf("%s %s writes, and the environment is read only", method, r.URL.Path)}
	}
	return nil
}

// matches reports whether a pattern in list holds for the request. A deny or
// once pattern (generous) is matched against the folded path, so a trailing
// slash, a capital letter, or a ;parameter cannot slip past it. A reads
// pattern relaxes a write, so it is matched against the path exactly as written.
// A pattern that errors fails closed: it matches for deny and once, and not for reads.
func matches(list []pattern, method string, u *url.URL, generous bool) bool {
	if u == nil || !pathIsNormal(u) {
		return false
	}
	folded := fold(u.Path)
	trimmed := trimSlash(folded)
	for _, pt := range list {
		if pt.method != method {
			continue
		}
		var ok bool
		var err error
		if generous {
			ok, err = matchEither(pt.folded, folded, trimmed)
		} else {
			ok, err = doublestar.Match(pt.path, u.Path)
		}
		if err != nil {
			if generous {
				return true
			}
			continue
		}
		if ok {
			return true
		}
	}
	return false
}

// matchEither tries the pattern against the folded path with its trailing slash
// kept and with it removed, so "/v1/x/*" still covers "/v1/x/" and "/v1/x"
// still covers "/v1/x/". A match error is returned as is.
func matchEither(pattern, kept, trimmed string) (bool, error) {
	ok, err := doublestar.Match(pattern, kept)
	if err != nil || ok || kept == trimmed {
		return ok, err
	}
	return doublestar.Match(pattern, trimmed)
}

func trimSlash(path string) string {
	if len(path) > 1 {
		return strings.TrimSuffix(path, "/")
	}
	return path
}

// fold lower-cases a path and drops the ;parameters of every segment. It keeps the trailing slash.
func fold(path string) string {
	path = strings.ToLower(path)
	segs := strings.Split(path, "/")
	for i, sg := range segs {
		if k := strings.IndexByte(sg, ';'); k >= 0 {
			segs[i] = sg[:k]
		}
	}
	return strings.Join(segs, "/")
}

// pathIsNormal reports whether the path can be matched as written. A dot
// segment, an empty segment inside the path, or an encoded slash lets a server
// resolve the path to something a pattern did not see.
func pathIsNormal(u *url.URL) bool {
	if strings.Contains(strings.ToLower(u.EscapedPath()), "%2f") {
		return false
	}
	if u.Path == "" {
		return true
	}
	if !strings.HasPrefix(u.Path, "/") {
		return false
	}
	segs := strings.Split(u.Path[1:], "/")
	for i, s := range segs {
		if s == "." || s == ".." || (s == "" && i < len(segs)-1) {
			return false
		}
	}
	return true
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
