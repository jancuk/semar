// Package xpathlite selects nodes of a native UI tree with a small, fixed part of XPath 1.0. It is
// not an XPath engine. It reads these forms and refuses everything else when the text is compiled:
//
//	/hierarchy/android.widget.FrameLayout          an absolute path, one step per level
//	//android.widget.Button                        a node at any depth
//	//*[@text='Pay']                               any class
//	//Button[@text='Pay' and @enabled='true']      and
//	//Button[contains(@content-desc,'Pay')]        contains and starts-with on an attribute
//	//Button[starts-with(@resource-id,'com.')]
//	//android.widget.TextView[2]                   the nth match among the children of a parent
//	//android.widget.TextView[last()]
//
// The package knows no platform: it reads a tree through the Node interface.
package xpathlite

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Limits keep a hostile expression cheap to run.
const (
	maxLength = 1024
	maxSteps  = 16
	maxPreds  = 6
	maxConds  = 6
	// maxWork is how many nodes one Select may visit. A tree of 20,000 nodes and a hostile expression
	// such as //*//*//* would otherwise take far longer.
	maxWork = 2_000_000
)

// Node is one node of a tree. N is the type of the node itself, such as *Node.
type Node[N any] interface {
	// Name is the class of the node, such as android.widget.Button.
	Name() string
	// Attr gives the value of an attribute, and false when the node has none.
	Attr(name string) (string, bool)
	// Children lists the children of the node in document order.
	Children() []N
}

// Expr is a compiled expression.
type Expr struct {
	src   string
	steps []step
}

type step struct {
	deep  bool   // // : the node is at any depth below
	name  string // "*" matches any class
	preds []pred
}

type predKind int

const (
	predAnd predKind = iota
	predIndex
	predLast
)

type pred struct {
	kind  predKind
	index int
	conds []cond
}

type condKind int

const (
	condEquals condKind = iota
	condContains
	condStarts
)

type cond struct {
	kind  condKind
	attr  string
	value string
}

// String is the expression as it was written.
func (e *Expr) String() string { return e.src }

// Validate says whether the text is an expression that this package reads.
func Validate(src string) error {
	_, err := Compile(src)
	return err
}

// Compile reads an expression. The error says in plain words what is not supported.
func Compile(src string) (*Expr, error) {
	if len(src) > maxLength {
		return nil, fmt.Errorf("the xpath is longer than %d bytes", maxLength)
	}
	p := &parser{s: src}
	e := &Expr{src: src}
	if strings.TrimSpace(src) == "" {
		return nil, errors.New("the xpath is empty")
	}
	p.space()
	if !p.at('/') {
		return nil, p.fail("an xpath starts with / or //")
	}
	for !p.end() {
		var st step
		switch {
		case p.eat("//"):
			st.deep = true
		case p.eat("/"):
		default:
			return nil, p.fail("expected / or //")
		}
		if len(e.steps) >= maxSteps {
			return nil, fmt.Errorf("the xpath has more than %d steps", maxSteps)
		}
		name, err := p.name()
		if err != nil {
			return nil, err
		}
		st.name = name
		for p.at('[') {
			if len(st.preds) >= maxPreds {
				return nil, fmt.Errorf("a step has more than %d predicates", maxPreds)
			}
			pr, err := p.pred()
			if err != nil {
				return nil, err
			}
			st.preds = append(st.preds, pr)
		}
		e.steps = append(e.steps, st)
		p.space()
	}
	return e, nil
}

type parser struct {
	s string
	i int
}

func (p *parser) end() bool      { return p.i >= len(p.s) }
func (p *parser) at(c byte) bool { return p.i < len(p.s) && p.s[p.i] == c }
func (p *parser) space()         { p.i += len(p.s[p.i:]) - len(strings.TrimLeft(p.s[p.i:], " \t\r\n")) }
func (p *parser) fail(msg string) error {
	return fmt.Errorf("the xpath is not supported at position %d: %s", p.i+1, msg)
}

func (p *parser) eat(tok string) bool {
	if strings.HasPrefix(p.s[p.i:], tok) {
		p.i += len(tok)
		return true
	}
	return false
}

// word reads a keyword that is not part of a longer name.
func (p *parser) word(w string) bool {
	if !strings.HasPrefix(p.s[p.i:], w) {
		return false
	}
	if rest := p.s[p.i+len(w):]; rest != "" && isNameChar(rune(rest[0])) {
		return false
	}
	p.i += len(w)
	return true
}

func isNameStart(r rune) bool { return r == '_' || unicode.IsLetter(r) }
func isNameChar(r rune) bool {
	return isNameStart(r) || unicode.IsDigit(r) || r == '.' || r == '$' || r == '-' || r == ':'
}

// name reads a class name or *.
func (p *parser) name() (string, error) {
	if p.eat("*") {
		return "*", nil
	}
	start := p.i
	for p.i < len(p.s) && isNameChar(rune(p.s[p.i])) {
		if p.i == start && !isNameStart(rune(p.s[p.i])) {
			break
		}
		p.i++
	}
	if p.i == start {
		return "", p.fail("expected a class name or *; . .. text() and axes are not supported")
	}
	n := p.s[start:p.i]
	if strings.Contains(n, "::") || p.at('(') {
		return "", p.fail("axes and functions are not supported here")
	}
	return n, nil
}

func (p *parser) pred() (pred, error) {
	p.i++ // [
	p.space()
	var out pred
	switch {
	case p.eat("last()"):
		out.kind = predLast
	case p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9':
		start := p.i
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
		}
		n, err := strconv.Atoi(p.s[start:p.i])
		if err != nil || n < 1 || n > 10000 {
			return out, p.fail("a position is a whole number from 1")
		}
		out.kind, out.index = predIndex, n
	default:
		out.kind = predAnd
		for {
			if len(out.conds) >= maxConds {
				return out, fmt.Errorf("a predicate has more than %d conditions", maxConds)
			}
			c, err := p.cond()
			if err != nil {
				return out, err
			}
			out.conds = append(out.conds, c)
			p.space()
			if !p.word("and") {
				break
			}
			p.space()
		}
	}
	p.space()
	if !p.eat("]") {
		return out, p.fail("expected ]; only and, =, contains, starts-with, a position, and last() are supported")
	}
	return out, nil
}

func (p *parser) attr() (string, error) {
	if !p.eat("@") {
		return "", p.fail("expected @attribute")
	}
	start := p.i
	for p.i < len(p.s) && isNameChar(rune(p.s[p.i])) {
		p.i++
	}
	if p.i == start {
		return "", p.fail("expected an attribute name")
	}
	return p.s[start:p.i], nil
}

func (p *parser) str() (string, error) {
	if p.i >= len(p.s) || (p.s[p.i] != '\'' && p.s[p.i] != '"') {
		return "", p.fail("expected a quoted text")
	}
	q := p.s[p.i]
	p.i++
	start := p.i
	for p.i < len(p.s) && p.s[p.i] != q {
		p.i++
	}
	if p.i >= len(p.s) {
		return "", p.fail("the quoted text is not closed")
	}
	v := p.s[start:p.i]
	p.i++
	return v, nil
}

func (p *parser) cond() (cond, error) {
	var c cond
	switch {
	case p.eat("contains("), p.eat("starts-with("):
		c.kind = condContains
		if strings.HasSuffix(p.s[:p.i], "starts-with(") {
			c.kind = condStarts
		}
		p.space()
		a, err := p.attr()
		if err != nil {
			return c, err
		}
		p.space()
		if !p.eat(",") {
			return c, p.fail("expected , after the attribute")
		}
		p.space()
		v, err := p.str()
		if err != nil {
			return c, err
		}
		p.space()
		if !p.eat(")") {
			return c, p.fail("expected )")
		}
		c.attr, c.value = a, v
	case p.at('@'):
		a, err := p.attr()
		if err != nil {
			return c, err
		}
		p.space()
		if !p.eat("=") {
			return c, p.fail("only @attribute='value' is supported, and not !=, <, >, or a bare attribute")
		}
		p.space()
		v, err := p.str()
		if err != nil {
			return c, err
		}
		c.kind, c.attr, c.value = condEquals, a, v
	default:
		return c, p.fail("expected @attribute, contains(), or starts-with(); or, not(), and functions are not supported")
	}
	return c, nil
}

func (c cond) holds(get func(string) (string, bool)) bool {
	v, ok := get(c.attr)
	if !ok {
		return false
	}
	switch c.kind {
	case condEquals:
		return v == c.value
	case condContains:
		return strings.Contains(v, c.value)
	}
	return strings.HasPrefix(v, c.value)
}

// Select gives the nodes that the expression picks out, in document order and without repeats. The
// first step of an absolute path is matched against root itself. N must be comparable, as a pointer is.
func Select[N interface {
	comparable
	Node[N]
}](e *Expr, root N) []N {
	var zero N
	if e == nil || root == zero {
		return nil
	}
	work := 0
	// The document node has the root as its only child.
	ctx := []ctxNode[N]{{doc: true}}
	for _, st := range e.steps {
		var next []N
		seen := map[N]bool{}
		for _, c := range ctx {
			for _, n := range stepFrom(st, c, root, &work) {
				if !seen[n] {
					seen[n] = true
					next = append(next, n)
				}
			}
		}
		ctx = ctx[:0]
		for _, n := range next {
			ctx = append(ctx, ctxNode[N]{n: n})
		}
		if len(ctx) == 0 || work > maxWork {
			return nil
		}
	}
	out := make([]N, len(ctx))
	for i, c := range ctx {
		out[i] = c.n
	}
	return sortDocument(out, root)
}

type ctxNode[N any] struct {
	doc bool
	n   N
}

func children[N interface {
	comparable
	Node[N]
}](c ctxNode[N], root N) []N {
	if c.doc {
		return []N{root}
	}
	return c.n.Children()
}

// stepFrom applies one step to one context node. A // step is /descendant-or-self::node()/child::name,
// so a position counts among the children of each parent.
func stepFrom[N interface {
	comparable
	Node[N]
}](st step, c ctxNode[N], root N, work *int) []N {
	parents := []ctxNode[N]{c}
	if st.deep {
		parents = descendantsOrSelf(c, root, work)
	}
	var out []N
	for _, p := range parents {
		var list []N
		if *work > maxWork {
			return nil
		}
		for _, k := range children(p, root) {
			if st.name == "*" || k.Name() == st.name {
				list = append(list, k)
			}
		}
		for _, pr := range st.preds {
			list = applyPred(pr, list)
		}
		out = append(out, list...)
	}
	return out
}

func descendantsOrSelf[N interface {
	comparable
	Node[N]
}](c ctxNode[N], root N, work *int) []ctxNode[N] {
	var out []ctxNode[N]
	var walk func(ctxNode[N])
	walk = func(x ctxNode[N]) {
		if *work++; *work > maxWork {
			return
		}
		out = append(out, x)
		for _, k := range children(x, root) {
			walk(ctxNode[N]{n: k})
		}
	}
	walk(c)
	return out
}

func applyPred[N interface {
	comparable
	Node[N]
}](pr pred, list []N) []N {
	switch pr.kind {
	case predIndex:
		if pr.index <= len(list) {
			return []N{list[pr.index-1]}
		}
		return nil
	case predLast:
		if len(list) == 0 {
			return nil
		}
		return []N{list[len(list)-1]}
	}
	var out []N
	for _, n := range list {
		ok := true
		for _, c := range pr.conds {
			if !c.holds(n.Attr) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, n)
		}
	}
	return out
}

// sortDocument puts the nodes in document order, which a // step after a positional one can break.
func sortDocument[N interface {
	comparable
	Node[N]
}](nodes []N, root N) []N {
	if len(nodes) < 2 {
		return nodes
	}
	want := map[N]bool{}
	for _, n := range nodes {
		want[n] = true
	}
	out := make([]N, 0, len(nodes))
	var walk func(N)
	walk = func(n N) {
		if want[n] {
			out = append(out, n)
		}
		for _, k := range n.Children() {
			walk(k)
		}
	}
	walk(root)
	return out
}
