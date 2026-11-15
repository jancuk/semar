// Package openapi reads the routes and statuses of an OpenAPI 3 or Swagger 2
// document. It reads only what a comparison needs, follows no reference, and
// does no I/O.
package openapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/yamlfile"
)

const (
	maxBytes      = 5 << 20
	maxOperations = 5000
	// maxWork bounds the keys visited. Aliases let one node be reached many
	// times, so the size of the input alone does not bound the walk.
	maxWork = 2_000_000
)

// Document holds what a comparison needs from an OpenAPI 3 or a Swagger 2 document.
type Document struct {
	Version    string      // the value of "openapi" or "swagger"
	Prefixes   []string    // path prefixes from basePath or from the path of each servers[].url, without a trailing slash
	Operations []Operation // sorted by Path, then Method
}

// Operation is one method on one path.
type Operation struct {
	Method   string   // upper case
	Path     string   // as written, for example /orders/{id}
	Statuses []string // the keys of responses, sorted: "200", "4XX", "default"
	Line     int      // the line of the operation in a YAML document, 0 when unknown
}

// Match is the answer of Lookup.
type Match struct {
	Operation Operation
	Found     bool     // the document has this method on this route
	PathKnown bool     // the document has this route with some method
	Methods   []string // the methods the document has on this route, sorted
}

var methods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// Parse reads an API document. name is used in error messages.
func Parse(data []byte, name string) (Document, error) {
	if len(data) > maxBytes {
		return Document{}, fmt.Errorf("%s: the file is larger than 5 MiB", name)
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var (
		raw rawDoc
		err error
	)
	if t := bytes.TrimLeft(data, " \t\r\n"); len(t) > 0 && t[0] == '{' {
		raw, err = readJSON(data, name)
	} else {
		raw, err = readYAML(data, name)
	}
	if err != nil {
		return Document{}, err
	}
	doc := Document{Version: raw.version, Operations: raw.ops}
	for _, u := range raw.urls {
		if p := prefixOf(u, raw.isURL); p != "" && !slices.Contains(doc.Prefixes, p) {
			doc.Prefixes = append(doc.Prefixes, p)
		}
	}
	sort.Slice(doc.Operations, func(i, j int) bool {
		a, b := doc.Operations[i], doc.Operations[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Method < b.Method
	})
	return doc, nil
}

type rawDoc struct {
	version string
	urls    []string // basePath values, or servers[].url values when isURL
	isURL   bool
	ops     []Operation
}

// prefixOf returns the path part of a server URL or a basePath, or "" when
// there is none that can be read.
func prefixOf(s string, isURL bool) string {
	s = strings.TrimSpace(s)
	if isURL {
		if i := strings.Index(s, "://"); i >= 0 {
			s = s[i+3:]
			j := strings.IndexByte(s, '/')
			if j < 0 {
				return ""
			}
			s = s[j:]
		} else if rest, ok := strings.CutPrefix(s, "//"); ok {
			j := strings.IndexByte(rest, '/')
			if j < 0 {
				return ""
			}
			s = rest[j:]
		}
	}
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	if !strings.HasPrefix(s, "/") {
		return ""
	}
	return strings.TrimRight(s, "/")
}

type budget int

func (b *budget) spend(n int) error {
	*b -= budget(n)
	if *b < 0 {
		return errors.New("too much work")
	}
	return nil
}

func tooMuch(name string) error {
	return fmt.Errorf("%s: the document is too large or too repetitive to read", name)
}

func tooMany(name string) error {
	return fmt.Errorf("%s: the document has more than %d operations", name, maxOperations)
}

func readJSON(data []byte, name string) (rawDoc, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return rawDoc{}, fmt.Errorf("%s: %w", name, err)
	}
	var out rawDoc
	key := "openapi"
	if _, ok := top[key]; !ok {
		key = "swagger"
	}
	if err := json.Unmarshal(top[key], &out.version); err != nil || out.version == "" {
		return rawDoc{}, fmt.Errorf("%s: the document needs a string \"openapi\" or \"swagger\" key", name)
	}
	if key == "swagger" {
		var bp string
		if json.Unmarshal(top["basePath"], &bp) == nil {
			out.urls = append(out.urls, bp)
		}
	} else {
		out.isURL = true
		var servers []struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(top["servers"], &servers) == nil {
			for _, s := range servers {
				out.urls = append(out.urls, s.URL)
			}
		}
	}
	rawPaths, ok := top["paths"]
	if !ok || isNull(rawPaths) {
		return out, nil
	}
	var paths map[string]json.RawMessage
	if err := json.Unmarshal(rawPaths, &paths); err != nil {
		return rawDoc{}, fmt.Errorf("%s: \"paths\" must be a mapping", name)
	}
	b := budget(maxWork)
	for p, rawItem := range paths {
		var item map[string]json.RawMessage
		if json.Unmarshal(rawItem, &item) != nil {
			continue
		}
		if b.spend(len(item)+1) != nil {
			return rawDoc{}, tooMuch(name)
		}
		for _, m := range methods {
			rawOp, ok := item[m]
			if !ok {
				continue
			}
			op := Operation{Method: strings.ToUpper(m), Path: p}
			var o struct {
				Responses map[string]json.RawMessage `json:"responses"`
			}
			if json.Unmarshal(rawOp, &o) == nil {
				if b.spend(len(o.Responses)) != nil {
					return rawDoc{}, tooMuch(name)
				}
				for k := range o.Responses {
					op.Statuses = append(op.Statuses, k)
				}
				sort.Strings(op.Statuses)
			}
			out.ops = append(out.ops, op)
			if len(out.ops) > maxOperations {
				return rawDoc{}, tooMany(name)
			}
		}
	}
	return out, nil
}

func isNull(b json.RawMessage) bool { return string(bytes.TrimSpace(b)) == "null" }

func readYAML(data []byte, name string) (rawDoc, error) {
	var root yaml.Node
	if err := yamlfile.Decode(data, name, &root); err != nil {
		return rawDoc{}, err
	}
	top := resolve(&root)
	if top != nil && top.Kind == yaml.DocumentNode && len(top.Content) == 1 {
		top = resolve(top.Content[0])
	}
	if top == nil || top.Kind != yaml.MappingNode {
		return rawDoc{}, fmt.Errorf("%s: the document is not a mapping", name)
	}
	var out rawDoc
	key := "openapi"
	v := field(top, key)
	if v == nil {
		key = "swagger"
		v = field(top, key)
	}
	// An unquoted 2.0 or 3.1 is a number in YAML, so any scalar is accepted.
	if v == nil || v.Kind != yaml.ScalarNode || v.Value == "" {
		return rawDoc{}, fmt.Errorf("%s: the document needs an \"openapi\" or \"swagger\" key with a version", name)
	}
	out.version = v.Value
	if key == "swagger" {
		if bp := field(top, "basePath"); bp != nil && bp.Kind == yaml.ScalarNode {
			out.urls = append(out.urls, bp.Value)
		}
	} else {
		out.isURL = true
		if servers := field(top, "servers"); servers != nil && servers.Kind == yaml.SequenceNode {
			for _, s := range servers.Content {
				if s = resolve(s); s == nil || s.Kind != yaml.MappingNode {
					continue
				}
				if u := field(s, "url"); u != nil && u.Kind == yaml.ScalarNode {
					out.urls = append(out.urls, u.Value)
				}
			}
		}
	}
	paths := field(top, "paths")
	if paths == nil || (paths.Kind == yaml.ScalarNode && paths.Tag == "!!null") {
		return out, nil
	}
	if paths.Kind != yaml.MappingNode {
		return rawDoc{}, fmt.Errorf("%s: \"paths\" must be a mapping", name)
	}
	b := budget(maxWork)
	for i := 0; i+1 < len(paths.Content); i += 2 {
		pk := resolve(paths.Content[i])
		item := resolve(paths.Content[i+1])
		if pk == nil || pk.Kind != yaml.ScalarNode || item == nil || item.Kind != yaml.MappingNode {
			continue
		}
		if b.spend(len(item.Content)/2+1) != nil {
			return rawDoc{}, tooMuch(name)
		}
		for j := 0; j+1 < len(item.Content); j += 2 {
			mk := resolve(item.Content[j])
			if mk == nil || mk.Kind != yaml.ScalarNode || !slices.Contains(methods, mk.Value) {
				continue
			}
			op := Operation{Method: strings.ToUpper(mk.Value), Path: pk.Value, Line: mk.Line}
			if o := resolve(item.Content[j+1]); o != nil && o.Kind == yaml.MappingNode {
				if r := field(o, "responses"); r != nil && r.Kind == yaml.MappingNode {
					if b.spend(len(r.Content)/2) != nil {
						return rawDoc{}, tooMuch(name)
					}
					for k := 0; k < len(r.Content); k += 2 {
						if sk := resolve(r.Content[k]); sk != nil && sk.Kind == yaml.ScalarNode {
							op.Statuses = append(op.Statuses, sk.Value)
						}
					}
					sort.Strings(op.Statuses)
					op.Statuses = slices.Compact(op.Statuses)
				}
			}
			out.ops = append(out.ops, op)
			if len(out.ops) > maxOperations {
				return rawDoc{}, tooMany(name)
			}
		}
	}
	return out, nil
}

// resolve follows an alias one step at a time, so a chain is bounded.
func resolve(n *yaml.Node) *yaml.Node {
	for i := 0; i < 16 && n != nil; i++ {
		if n.Kind != yaml.AliasNode {
			return n
		}
		n = n.Alias
	}
	return nil
}

// field returns the resolved value of key in a mapping node, or nil.
func field(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if k := resolve(m.Content[i]); k != nil && k.Kind == yaml.ScalarNode && k.Value == key {
			return resolve(m.Content[i+1])
		}
	}
	return nil
}

// normalize removes one trailing slash and turns every {name} into {}.
func normalize(p string) string {
	if len(p) > 1 {
		p = strings.TrimSuffix(p, "/")
	}
	if !strings.Contains(p, "{") {
		return p
	}
	var sb strings.Builder
	for {
		i := strings.IndexByte(p, '{')
		if i < 0 {
			break
		}
		j := strings.IndexByte(p[i:], '}')
		if j < 0 {
			break
		}
		sb.WriteString(p[:i])
		sb.WriteString("{}")
		p = p[i+j+1:]
	}
	sb.WriteString(p)
	return sb.String()
}

// Lookup finds the operation for a method and a route. The route may be
// written with or without a prefix of the document.
func (d Document) Lookup(method, route string) Match {
	var m Match
	want := normalize(route)
	for _, op := range d.Operations {
		if !d.reaches(op.Path, want) {
			continue
		}
		m.PathKnown = true
		if !slices.Contains(m.Methods, op.Method) {
			m.Methods = append(m.Methods, op.Method)
		}
		if !m.Found && strings.EqualFold(op.Method, method) {
			m.Found = true
			m.Operation = op
		}
	}
	sort.Strings(m.Methods)
	return m
}

func (d Document) reaches(path, want string) bool {
	if normalize(path) == want {
		return true
	}
	for _, p := range d.Prefixes {
		if normalize(p+path) == want {
			return true
		}
	}
	return false
}

// Declares reports whether the operation lists the status code, its range
// such as 4XX, or default.
func (o Operation) Declares(status int) bool {
	exact := strconv.Itoa(status)
	rng := strconv.Itoa(status/100) + "XX"
	inRange := status >= 100 && status <= 599
	for _, s := range o.Statuses {
		if s == exact || s == "default" || (inRange && strings.EqualFold(s, rng)) {
			return true
		}
	}
	return false
}
