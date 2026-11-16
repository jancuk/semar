package redact

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

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
		next, ok := s.jsonOnce(cur)
		if !ok {
			break
		}
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

// jsonOnce replaces the values at the configured JSONPaths, redacts the inside
// of every string, keys included, and turns a number or literal that holds a
// secret, and a number that a pattern matches, into a marker string. true,
// false, and null are left to secrets only, so a loose pattern cannot hide them. A last pass over the whole text catches a
// secret that crosses a string's quotes or hides in its escapes; the caller
// checks that the result is still JSON. It reports false for a duplicate key
// when JSONPaths are configured: the paths were resolved on a tree where the
// last key wins, so a value under an earlier one could escape them.
func (s *scrubber) jsonOnce(t string) (string, bool) {
	sc := &jsonScanner{t: t, dupCheck: len(s.r.paths) > 0}
	sc.value(s.selected(t))
	for _, rv := range sc.raws {
		s.mask(rv.raw, rv.marker)
	}
	if sc.dup {
		return "", false
	}
	edits := sc.edits
	for _, str := range sc.strings {
		inner := t[str.start:str.end]
		if out := s.redact(inner, jsonUnescaped, true); out != inner {
			edits = append(edits, edit{str.start, str.end, out})
		}
	}
	for _, tok := range sc.scalars {
		lit := t[tok.start:tok.end]
		if len(s.secretSpans(sourceView(lit))) > 0 || isNumber(lit) && len(s.patternSpans(sourceView(lit))) > 0 {
			edits = append(edits, edit{tok.start, tok.end, `"` + s.r.Marker(lit) + `"`})
		}
	}
	return s.redact(applyEdits(t, edits), sourceView, false), true
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
			marker := s.markerOf(n.Node)
			trie.add(n.Path, `"`+marker+`"`)
			s.remember(n.Node, marker)
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
	return s.r.Marker(compact(node))
}

// remember records the text that a capture stores for node, and for every value
// inside it, so that capturesOf can mask a capture of a value that a JSONPath
// removed from the body. A value inside a container goes by its own marker, as
// it would if a path selected it. The text is the string, the number as it was
// written, true or false, and the compact JSON of a container.
func (s *scrubber) remember(node any, marker string) {
	switch v := node.(type) {
	case string:
		s.mask(v, marker)
	case json.Number:
		s.mask(v.String(), marker)
	case bool:
		s.mask(strconv.FormatBool(v), marker)
	case nil:
		s.mask("null", marker)
	case map[string]any:
		s.mask(compact(v), marker)
		for _, k := range slices.Sorted(maps.Keys(v)) {
			s.remember(v[k], s.markerOf(v[k]))
		}
	case []any:
		s.mask(compact(v), marker)
		for _, e := range v {
			s.remember(e, s.markerOf(e))
		}
	}
}

// mask lets the first marker stand when two values have the same text. A marker
// is its own text and masking it changes nothing, so it is not recorded.
func (s *scrubber) mask(raw, marker string) {
	if IsMarker(raw) {
		return
	}
	if s.masked == nil {
		s.masked = map[string]string{}
	}
	if _, ok := s.masked[raw]; !ok {
		s.masked[raw] = marker
	}
}

func compact(node any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(node) // a value decoded from JSON always encodes
	return strings.TrimSuffix(buf.String(), "\n")
}

type edit struct {
	start, end int
	text       string
}

// applyEdits keeps the outermost of nested edits, and the first of two on the
// same span, so a value at a JSONPath wins over the strings inside it.
func applyEdits(t string, edits []edit) string {
	sort.SliceStable(edits, func(i, j int) bool {
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

// isNumber reports whether a scalar token of a valid document is a number, not true, false, or null.
func isNumber(lit string) bool { return lit[0] == '-' || lit[0] >= '0' && lit[0] <= '9' }

type token struct{ start, end int }

// A rawValue is the source text of a container that a JSONPath replaced, laid
// out as the body had it, and the marker that now stands for it.
type rawValue struct{ raw, marker string }

// jsonScanner reads a document that json.Valid accepted. It records the inside
// of every string, keys included, every number and literal, and an edit for
// every selected value. With dupCheck set, it reports a key that repeats in
// one object.
type jsonScanner struct {
	t        string
	pos      int
	strings  []token
	scalars  []token
	edits    []edit
	raws     []rawValue
	dupCheck bool
	dup      bool
}

func (sc *jsonScanner) value(sel *pathTrie) {
	sc.space()
	start := sc.pos
	switch sc.t[sc.pos] {
	case '{':
		sc.pos++
		var seen map[string]bool
		for i := 0; !sc.end('}', i); i++ {
			key := sc.str()
			var child *pathTrie
			if sc.dupCheck || sel != nil && len(sel.names) > 0 {
				name := keyName(sc.t[key.start:key.end])
				if sel != nil {
					child = sel.names[name]
				}
				if sc.dupCheck {
					if seen == nil {
						seen = map[string]bool{}
					}
					sc.dup = sc.dup || seen[name]
					seen[name] = true
				}
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
		sc.scalars = append(sc.scalars, token{start, sc.pos})
	}
	if sel != nil && sel.replacement != "" {
		sc.edits = append(sc.edits, edit{start, sc.pos, sel.replacement})
		if c := sc.t[start]; c == '{' || c == '[' {
			sc.raws = append(sc.raws, rawValue{sc.t[start:sc.pos], strings.Trim(sel.replacement, `"`)})
		}
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

// keyName decodes the inside of a key as encoding/json does for the tree the
// paths are resolved on, invalid UTF-8 included.
func keyName(inner string) string {
	if !strings.Contains(inner, `\`) && utf8.ValidString(inner) {
		return inner
	}
	var name string
	_ = json.Unmarshal([]byte(`"`+inner+`"`), &name) // the scanner only passes the inside of a valid string
	return name
}

func (sc *jsonScanner) space() {
	for sc.pos < len(sc.t) && strings.IndexByte(" \t\r\n", sc.t[sc.pos]) >= 0 {
		sc.pos++
	}
}
