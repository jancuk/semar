package yamlfile

import (
	"bytes"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	yaml "go.yaml.in/yaml/v4"
)

// NumberKind is the Go type that a key's value is decoded into.
type NumberKind int

const (
	AnyNumber   NumberKind = iota // any: a scalar, or numbers inside a list or a mapping
	FloatNumber                   // float64
	IntNumber                     // int
)

// ExactNumbers refuses a number under a key that kindOf names when the decoded value is not
// exactly the number its text says. A float64 counts as the shortest decimal that gives it
// back, which is how internal/check reads an operand. It also refuses .inf and .nan, and a
// plain scalar that looks like a number but that YAML reads as a string. Call it after Decode.
func ExactNumbers(data []byte, name string, kindOf func(key string) (NumberKind, bool)) error {
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(&doc); err != nil {
		return decodeError(name, err)
	}
	w := numberWalk{name: name, kindOf: kindOf, seen: map[numberVisit]bool{}}
	return w.find(&doc)
}

type numberVisit struct {
	node *yaml.Node
	key  string
}

type numberWalk struct {
	name   string
	kindOf func(string) (NumberKind, bool)
	// seen holds alias targets already walked, so a node that many aliases share is read once
	seen map[numberVisit]bool
}

func (w *numberWalk) once(n *yaml.Node, key string) bool {
	v := numberVisit{n, key}
	if w.seen[v] {
		return false
	}
	w.seen[v] = true
	return true
}

func (w *numberWalk) find(n *yaml.Node) error {
	switch n.Kind {
	case yaml.AliasNode:
		if n.Alias != nil && w.once(n.Alias, "") {
			return w.find(n.Alias)
		}
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			if err := w.find(c); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			var err error
			if kind, ok := w.kindOf(k.Value); ok && k.Kind == yaml.ScalarNode {
				err = w.operand(v, k.Value, kind)
			} else {
				err = w.find(v)
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *numberWalk) operand(n *yaml.Node, key string, kind NumberKind) error {
	switch n.Kind {
	case yaml.ScalarNode:
		return w.scalar(n, key, kind)
	case yaml.AliasNode:
		if n.Alias != nil && w.once(n.Alias, key) {
			return w.operand(n.Alias, key, kind)
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			if err := w.operand(c, key, kind); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		for i := 1; i < len(n.Content); i += 2 {
			if err := w.operand(n.Content[i], key, kind); err != nil {
				return err
			}
		}
	}
	return nil
}

const exactHint = "write fewer digits, or quote the value if a string is meant"

func (w *numberWalk) scalar(n *yaml.Node, key string, kind NumberKind) error {
	switch n.ShortTag() {
	case "!!int", "!!float":
	case "!!str":
		if n.Style == 0 && looksNumeric(n.Value) {
			return fmt.Errorf("%s:%d: %s %s looks like a number, but YAML reads it as a string: %s", w.name, n.Line, key, cut(n.Value), exactHint)
		}
		return nil
	default:
		return nil
	}
	if notFinite(n.Value) {
		return fmt.Errorf("%s:%d: %s cannot be %s: write a finite number", w.name, n.Line, key, cut(n.Value))
	}
	got, shown, err := decodedNumber(n, kind)
	if err != nil {
		return decodeError(w.name, err)
	}
	want, ok := textNumber(n.Value)
	if !ok || got == nil || want.Cmp(got) != 0 {
		return fmt.Errorf("%s:%d: %s %s cannot be held exactly and would be read as %s: %s", w.name, n.Line, key, cut(n.Value), shown, exactHint)
	}
	return nil
}

// decodedNumber decodes the scalar the way the whole file was decoded and returns its exact value.
func decodedNumber(n *yaml.Node, kind NumberKind) (*big.Rat, string, error) {
	var v any
	switch kind {
	case FloatNumber:
		var f float64
		if err := n.Load(&f); err != nil {
			return nil, "", err
		}
		v = f
	case IntNumber:
		var i int
		if err := n.Load(&i); err != nil {
			return nil, "", err
		}
		v = i
	default:
		if err := n.Load(&v); err != nil {
			return nil, "", err
		}
	}
	switch x := v.(type) {
	case int:
		return new(big.Rat).SetInt64(int64(x)), strconv.Itoa(x), nil
	case int64:
		return new(big.Rat).SetInt64(x), strconv.FormatInt(x, 10), nil
	case uint64:
		return new(big.Rat).SetInt(new(big.Int).SetUint64(x)), strconv.FormatUint(x, 10), nil
	case float64:
		s := strconv.FormatFloat(x, 'g', -1, 64)
		if r, ok := new(big.Rat).SetString(s); ok {
			return r, s, nil
		}
		return nil, s, nil
	default:
		return nil, fmt.Sprintf("%v", v), nil
	}
}

func notFinite(s string) bool {
	_, t := splitSign(strings.ToLower(s))
	return t == ".inf" || t == ".nan"
}

var (
	decimalNumber = regexp.MustCompile(`^(?:([0-9]+)(?:\.([0-9]*))?|\.([0-9]+))(?:[eE]([-+]?[0-9]+))?$`)
	prefixed      = regexp.MustCompile(`^0([xX][0-9a-fA-F]+|[oO][0-7]+|[bB][01]+)$`)
)

// Limits past which no int64, uint64, or shortest float64 decimal can equal the text.
const (
	maxDigits   = 40
	maxExponent = 400
)

// textNumber returns the number that the scalar's text says to a reader: a leading zero is
// a decimal digit, not an octal prefix. It reports false for text it cannot read within limits.
func textNumber(s string) (*big.Rat, bool) {
	neg, p := splitSign(strings.ReplaceAll(s, "_", ""))
	var r *big.Rat
	if m := prefixed.FindStringSubmatch(p); m != nil {
		base := 16
		switch m[1][0] {
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		i, ok := new(big.Int).SetString(m[1][1:], base)
		if !ok {
			return nil, false
		}
		r = new(big.Rat).SetInt(i)
	} else if m := decimalNumber.FindStringSubmatch(p); m != nil {
		var ok bool
		if r, ok = decimal(m[1]+m[2]+m[3], len(m[2])+len(m[3]), m[4]); !ok {
			return nil, false
		}
	} else {
		return nil, false
	}
	if neg {
		r.Neg(r)
	}
	return r, true
}

// decimal returns digits × 10^(exp - fracLen), refusing values past the limits before any big arithmetic.
func decimal(digits string, fracLen int, exp string) (*big.Rat, bool) {
	e := 0
	if exp != "" {
		n, err := strconv.Atoi(exp)
		if err != nil {
			return nil, false
		}
		e = n
	}
	e -= fracLen
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		return new(big.Rat), true
	}
	trimmed := strings.TrimRight(digits, "0")
	e += len(digits) - len(trimmed)
	if len(trimmed) > maxDigits || e > maxExponent || e < -maxExponent {
		return nil, false
	}
	mant, ok := new(big.Int).SetString(trimmed, 10)
	if !ok {
		return nil, false
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(abs(e))), nil)
	if e >= 0 {
		return new(big.Rat).SetInt(mant.Mul(mant, scale)), true
	}
	return new(big.Rat).SetFrac(mant, scale), true
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func splitSign(s string) (neg bool, rest string) {
	if s != "" && (s[0] == '+' || s[0] == '-') {
		return s[0] == '-', s[1:]
	}
	return false, s
}

func looksNumeric(s string) bool {
	_, p := splitSign(strings.ReplaceAll(s, "_", ""))
	return decimalNumber.MatchString(p) || prefixed.MatchString(p)
}

func cut(s string) string {
	const limit = 40
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}
