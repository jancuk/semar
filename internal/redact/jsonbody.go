package redact

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	"github.com/theory/jsonpath/spec"
)

// body redacts a body. A JSON body is edited in place, so it stays valid JSON
// and every byte outside a secret, layout and key order included, is kept.
func (s *scrubber) body(b []byte) []byte {
	if b == nil {
		return nil
	}
	if !json.Valid(b) {
		return []byte(s.text(string(b)))
	}
	cur := string(b)
	for range maxRounds {
		next := s.jsonOnce(cur)
		if next == cur {
			return []byte(cur)
		}
		if !json.Valid([]byte(next)) {
			break
		}
		cur = next
	}
	return []byte(`"` + s.r.Marker(string(b)) + `"`)
}

// jsonOnce replaces the values at the configured JSONPaths and redacts the
// inside of every string, keys included. A last pass over the whole text
// catches a secret that crosses a string's quotes or hides in its escapes; the
// caller checks that the result is still JSON.
func (s *scrubber) jsonOnce(t string) string {
	sc := &jsonScanner{t: t}
	sc.value(s.selected(t))
	edits := sc.edits
	for _, str := range sc.strings {
		inner := t[str.start:str.end]
		if out := s.redact(inner, jsonUnescaped, true); out != inner {
			edits = append(edits, edit{str.start, str.end, out})
		}
	}
	return s.redact(applyEdits(t, edits), sourceView, false)
}

// A pathTrie holds the normalized paths that the configured JSONPaths select,
// and the JSON text that replaces the value at each.
type pathTrie struct {
	names       map[string]*pathTrie
	indexes     map[int]*pathTrie
	replacement string
}

func (s *scrubber) selected(t string) *pathTrie {
	if len(s.r.paths) == 0 {
		return nil
	}
	var root any
	dec := json.NewDecoder(strings.NewReader(t))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return nil
	}
	trie := &pathTrie{}
	for _, p := range s.r.paths {
		for _, n := range p.SelectLocated(root) {
			trie.add(n.Path, `"`+s.markerOf(n.Node)+`"`)
		}
	}
	return trie
}

func (t *pathTrie) add(path spec.NormalizedPath, replacement string) {
	for _, sel := range path {
		switch sel := sel.(type) {
		case spec.Name:
			t = childOf(&t.names, string(sel))
		case spec.Index:
			t = childOf(&t.indexes, int(sel))
		}
	}
	t.replacement = replacement
}

func childOf[K comparable](m *map[K]*pathTrie, k K) *pathTrie {
	if *m == nil {
		*m = map[K]*pathTrie{}
	}
	if (*m)[k] == nil {
		(*m)[k] = &pathTrie{}
	}
	return (*m)[k]
}

// markerOf gives a string its own marker, and any other value the marker of
// its compact JSON encoding, so a check cannot pass on a typed zero.
func (s *scrubber) markerOf(node any) string {
	if str, ok := node.(string); ok {
		if IsMarker(str) {
			return str
		}
		return s.r.Marker(str)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(node) // a value decoded from JSON always encodes
	return s.r.Marker(strings.TrimSuffix(buf.String(), "\n"))
}

type edit struct {
	start, end int
	text       string
}

// applyEdits keeps the outermost of nested edits, so a value at a JSONPath
// wins over the strings inside it.
func applyEdits(t string, edits []edit) string {
	sort.Slice(edits, func(i, j int) bool {
		if edits[i].start != edits[j].start {
			return edits[i].start < edits[j].start
		}
		return edits[i].end > edits[j].end
	})
	var b strings.Builder
	pos := 0
	for _, e := range edits {
		if e.start < pos {
			continue
		}
		b.WriteString(t[pos:e.start])
		b.WriteString(e.text)
		pos = e.end
	}
	b.WriteString(t[pos:])
	return b.String()
}

type token struct{ start, end int }

// jsonScanner reads a document that json.Valid accepted. It records the inside
// of every string, keys included, and an edit for every selected value.
type jsonScanner struct {
	t       string
	pos     int
	strings []token
	edits   []edit
}

func (sc *jsonScanner) value(sel *pathTrie) {
	sc.space()
	start := sc.pos
	switch sc.t[sc.pos] {
	case '{':
		sc.pos++
		for i := 0; !sc.end('}', i); i++ {
			key := sc.str()
			var child *pathTrie
			if sel != nil && len(sel.names) > 0 {
				var name string
				_ = json.Unmarshal([]byte(sc.t[key.start-1:key.end+1]), &name) // decoded as the tree's keys are
				child = sel.names[name]
			}
			sc.space()
			sc.pos++
			sc.value(child)
		}
	case '[':
		sc.pos++
		for i := 0; !sc.end(']', i); i++ {
			var child *pathTrie
			if sel != nil {
				child = sel.indexes[i]
			}
			sc.value(child)
		}
	case '"':
		sc.str()
	default:
		for sc.pos < len(sc.t) && !strings.ContainsRune(",]} \t\r\n", rune(sc.t[sc.pos])) {
			sc.pos++
		}
	}
	if sel != nil && sel.replacement != "" {
		sc.edits = append(sc.edits, edit{start, sc.pos, sel.replacement})
	}
}

// end skips the comma before member i and reports whether the container
// closes, consuming the closing byte.
func (sc *jsonScanner) end(closing byte, i int) bool {
	sc.space()
	if i > 0 && sc.t[sc.pos] == ',' {
		sc.pos++
		sc.space()
	}
	if sc.t[sc.pos] == closing {
		sc.pos++
		return true
	}
	return false
}

func (sc *jsonScanner) str() token {
	sc.pos++
	start := sc.pos
	for sc.t[sc.pos] != '"' {
		if sc.t[sc.pos] == '\\' {
			sc.pos++
		}
		sc.pos++
	}
	tok := token{start, sc.pos}
	sc.pos++
	sc.strings = append(sc.strings, tok)
	return tok
}

func (sc *jsonScanner) space() {
	for sc.pos < len(sc.t) && strings.IndexByte(" \t\r\n", sc.t[sc.pos]) >= 0 {
		sc.pos++
	}
}
