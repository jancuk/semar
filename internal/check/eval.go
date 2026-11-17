// Package check decides whether one check holds for one recorded action. It is
// pure: it does no I/O and changes none of its arguments.
package check

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/theory/jsonpath"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/redact"
)

const (
	noResponse    = "no response was recorded"
	redactedValue = "the value is redacted"
	notExact      = "a number cannot be compared exactly"
	maxText       = 200
)

type Result struct {
	State    domain.CheckState
	Expected string
	Observed string
	Detail   string
}

// Eval decides one check against one action. It never panics.
func Eval(c domain.Check, a domain.Action) (res Result) {
	defer func() {
		if r := recover(); r != nil {
			res = Result{State: domain.CheckNotEvaluated, Detail: fmt.Sprintf("the check could not be evaluated: %v", r)}
		}
	}()

	ops := operators(c)
	if problem := malformed(c, ops); problem != "" {
		return Result{State: domain.CheckNotEvaluated, Detail: "the check is malformed: " + problem}
	}
	res.Expected = short(describe(c, ops))
	if c.Element != "" {
		evalElement(&res, c, ops[0], a.Web)
		res.Observed = short(res.Observed)
		return res
	}
	if a.Response == nil {
		res.State = domain.CheckNotEvaluated
		res.Detail = noResponse
		return res
	}
	switch {
	case c.Status != nil:
		evalStatus(&res, *c.Status, a.Response)
	case c.JSONPath != "":
		evalJSONPath(&res, c, ops[0], a.Response)
	default:
		evalHeader(&res, c, ops[0], a.Response)
	}
	res.Observed = short(res.Observed)
	return res
}

// Describe is the expected value of a check as Eval reports it, or why the check is malformed.
// It cuts a long value; DescribeFull does not.
func Describe(c domain.Check) string {
	return short(DescribeFull(c))
}

// DescribeFull is Describe without the cut, for a person who must see the whole value before confirming it.
func DescribeFull(c domain.Check) string {
	ops := operators(c)
	if problem := malformed(c, ops); problem != "" {
		return "malformed check: " + problem
	}
	return describe(c, ops)
}

func malformed(c domain.Check, ops []string) string {
	kinds := 0
	if c.Status != nil {
		kinds++
	}
	if c.JSONPath != "" {
		kinds++
	}
	if c.Header != "" {
		kinds++
	}
	if c.Element != "" {
		kinds++
	}
	switch {
	case kinds != 1:
		return "it must name exactly one of status, jsonpath, header, or element"
	case c.Status != nil && len(ops) > 0:
		return "a status check takes no operator"
	case c.Status == nil && len(ops) != 1:
		return "a jsonpath, header, or element check needs exactly one operator"
	}
	return ""
}

func operators(c domain.Check) []string {
	// equals: null cannot be written: an operand that decodes to nil reads as no operator.
	var ops []string
	add := func(set bool, name string) {
		if set {
			ops = append(ops, name)
		}
	}
	add(c.Equals != nil, "equals")
	add(c.NotEquals != nil, "notEquals")
	add(c.Contains != nil, "contains")
	add(c.Exists != nil, "exists")
	add(c.Greater != nil, "greater")
	add(c.Less != nil, "less")
	add(c.Count != nil, "count")
	add(c.EqualsPath != "", "equalsPath")
	return ops
}

func describe(c domain.Check, ops []string) string {
	if c.Status != nil {
		return statusText(*c.Status)
	}
	subject := c.JSONPath
	switch {
	case c.Element != "":
		subject = "element " + c.Element
	case subject == "":
		subject = "header " + c.Header
	}
	return subject + " " + ops[0] + " " + operandText(c, ops[0])
}

func operandText(c domain.Check, op string) string {
	switch op {
	case "equals":
		return text(c.Equals)
	case "notEquals":
		return text(c.NotEquals)
	case "contains":
		return text(c.Contains)
	case "exists":
		return strconv.FormatBool(*c.Exists)
	case "greater":
		return strconv.FormatFloat(*c.Greater, 'g', -1, 64)
	case "less":
		return strconv.FormatFloat(*c.Less, 'g', -1, 64)
	case "count":
		return strconv.Itoa(*c.Count)
	default:
		return c.EqualsPath
	}
}

func statusText(status int) string { return "status " + strconv.Itoa(status) }

func evalStatus(res *Result, want int, resp *domain.HTTPResponse) {
	res.Observed = statusText(resp.Status)
	res.State = state(resp.Status == want)
}

func evalJSONPath(res *Result, c domain.Check, op string, resp *domain.HTTPResponse) {
	path, err := jsonpath.Parse(c.JSONPath)
	if err != nil {
		notEvaluated(res, "invalid JSONPath "+c.JSONPath+": "+err.Error())
		return
	}
	if resp.Truncated {
		notEvaluated(res, "the response body was cut at the size limit")
		return
	}
	doc, err := decodeNumbers(resp.Body)
	if err != nil {
		notEvaluated(res, "the response body is not JSON")
		return
	}
	nodes := path.Select(doc)
	res.Observed = nodesText(nodes)

	switch op {
	case "exists":
		res.State = state((len(nodes) > 0) == *c.Exists)
		return
	case "count":
		res.State = state(countOf(path, nodes) == *c.Count)
		return
	}

	value, ok := single(res, nodes, c.JSONPath)
	if !ok {
		return
	}
	if hasMarker(value) {
		notEvaluated(res, redactedValue)
		return
	}

	switch op {
	case "equals", "notEquals":
		want, err := normalise(operandValue(c, op))
		if err != nil {
			notEvaluated(res, "the expected value cannot be compared: "+err.Error())
			return
		}
		if !exact(value) || !exact(want) {
			notEvaluated(res, notExact)
			return
		}
		res.State = state(equalJSON(value, want) == (op == "equals"))
	case "contains":
		evalContains(res, c.Contains, value)
	case "greater", "less":
		n, isNumber := value.(json.Number)
		if !isNumber {
			res.State = domain.CheckFalse
			res.Detail = op + " needs a number, got " + typeName(value)
			return
		}
		bound := c.Greater
		if op == "less" {
			bound = c.Less
		}
		got, errGot := ratOf(n)
		want, errWant := ratOf(json.Number(strconv.FormatFloat(*bound, 'g', -1, 64)))
		if errGot != nil || errWant != nil {
			notEvaluated(res, notExact)
			return
		}
		if op == "greater" {
			res.State = state(got.Cmp(want) > 0)
		} else {
			res.State = state(got.Cmp(want) < 0)
		}
	case "equalsPath":
		evalEqualsPath(res, c, value, doc)
	}
}

func operandValue(c domain.Check, op string) any {
	if op == "equals" {
		return c.Equals
	}
	return c.NotEquals
}

func evalContains(res *Result, expected, value any) {
	switch v := value.(type) {
	case string:
		sub, ok := expected.(string)
		if !ok {
			res.State = domain.CheckFalse
			res.Detail = "contains on a string needs a string value, got " + typeName(expected)
			return
		}
		res.State = state(strings.Contains(v, sub))
	case []any:
		want, err := normalise(expected)
		if err != nil {
			notEvaluated(res, "the expected value cannot be compared: "+err.Error())
			return
		}
		if !exact(v) || !exact(want) {
			notEvaluated(res, notExact)
			return
		}
		found := false
		for _, e := range v {
			if equalJSON(e, want) {
				found = true
				break
			}
		}
		res.State = state(found)
	default:
		res.State = domain.CheckFalse
		res.Detail = "contains needs a string or an array, got " + typeName(value)
	}
}

func evalEqualsPath(res *Result, c domain.Check, first, doc any) {
	other, err := jsonpath.Parse(c.EqualsPath)
	if err != nil {
		notEvaluated(res, "invalid JSONPath "+c.EqualsPath+": "+err.Error())
		return
	}
	second, ok := single(res, other.Select(doc), c.EqualsPath)
	if !ok {
		return
	}
	res.Observed = short(text(first) + " vs " + text(second))
	if hasMarker(second) {
		notEvaluated(res, redactedValue)
		return
	}
	a, errA := normalise(first)
	b, errB := normalise(second)
	if errA != nil || errB != nil {
		notEvaluated(res, "the values cannot be compared")
		return
	}
	if !exact(a) || !exact(b) {
		notEvaluated(res, notExact)
		return
	}
	res.State = state(equalJSON(a, b))
}

// single returns the one selected value. With none or several it sets a false
// result that names the path, and reports false.
func single(res *Result, nodes jsonpath.NodeList, path string) (any, bool) {
	switch len(nodes) {
	case 1:
		return nodes[0], true
	case 0:
		res.State = domain.CheckFalse
		res.Detail = "no value at " + path
	default:
		res.State = domain.CheckFalse
		res.Detail = fmt.Sprintf("%d values at %s", len(nodes), path)
	}
	return nil, false
}

func evalHeader(res *Result, c domain.Check, op string, resp *domain.HTTPResponse) {
	var values []string
	for name, v := range resp.Headers {
		if strings.EqualFold(name, c.Header) {
			values = append(values, v...)
		}
	}
	res.Observed = "no header " + c.Header
	if len(values) > 0 {
		res.Observed = strings.Join(values, ", ")
	}

	if op == "exists" {
		res.State = state((len(values) > 0) == *c.Exists)
		return
	}
	switch op {
	case "equals", "notEquals", "contains":
	default:
		notEvaluated(res, "the operator "+op+" is not supported on a header check")
		return
	}
	if len(values) == 0 {
		res.State = domain.CheckFalse
		res.Detail = "no header " + c.Header
		return
	}

	want := text(c.Contains)
	if op != "contains" {
		want = text(operandValue(c, op))
	}
	matched, redacted := false, false
	for _, v := range values {
		switch {
		case redact.HasMarker(v):
			redacted = true
		case op == "contains" && strings.Contains(v, want), op != "contains" && v == want:
			matched = true
		}
	}
	switch {
	case op == "notEquals" && redacted:
		notEvaluated(res, redactedValue)
	case matched:
		res.State = state(op != "notEquals")
	case redacted:
		notEvaluated(res, redactedValue)
	default:
		res.State = state(op == "notEquals")
	}
}

// hasMarker reports whether a value, or anything inside it, holds a redaction
// marker.
func hasMarker(v any) bool {
	switch x := v.(type) {
	case string:
		return redact.HasMarker(x)
	case []any:
		for _, e := range x {
			if hasMarker(e) {
				return true
			}
		}
	case map[string]any:
		for _, e := range x {
			if hasMarker(e) {
				return true
			}
		}
	}
	return false
}

func notEvaluated(res *Result, detail string) {
	res.State = domain.CheckNotEvaluated
	res.Detail = detail
}

func state(ok bool) domain.CheckState {
	if ok {
		return domain.CheckTrue
	}
	return domain.CheckFalse
}

// decodeNumbers decodes one JSON document and keeps every number as its exact
// text.
func decodeNumbers(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("data after the JSON value")
	}
	return out, nil
}

// normalise passes a value through encoding/json, so a YAML int or float
// becomes its exact decimal text and a map or slice becomes the shape the
// response body decodes to. A float that is not finite fails here.
func normalise(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return decodeNumbers(raw)
}

func ratOf(n json.Number) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(string(n))
	if !ok {
		return nil, fmt.Errorf("bad number %q", string(n))
	}
	return r, nil
}

// exact reports whether every number in v can be turned into an exact decimal.
func exact(v any) bool {
	switch x := v.(type) {
	case json.Number:
		_, err := ratOf(x)
		return err == nil
	case []any:
		for _, e := range x {
			if !exact(e) {
				return false
			}
		}
	case map[string]any:
		for _, e := range x {
			if !exact(e) {
				return false
			}
		}
	}
	return true
}

// equalJSON compares two values from decodeNumbers. Numbers compare as exact
// decimals, and a number never equals a string. Call exact first.
func equalJSON(a, b any) bool {
	switch x := a.(type) {
	case json.Number:
		y, ok := b.(json.Number)
		if !ok {
			return false
		}
		rx, errX := ratOf(x)
		ry, errY := ratOf(y)
		return errX == nil && errY == nil && rx.Cmp(ry) == 0
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !equalJSON(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, xv := range x {
			yv, ok := y[k]
			if !ok || !equalJSON(xv, yv) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}

func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case json.Number, float64, int, int64, uint64:
		return "number"
	case bool:
		return "boolean"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func nodesText(nodes jsonpath.NodeList) string {
	switch len(nodes) {
	case 0:
		return "no value"
	case 1:
		return text(nodes[0])
	default:
		return fmt.Sprintf("%d values", len(nodes))
	}
}

func text(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(raw)
}

func short(s string) string {
	if utf8.RuneCountInString(s) <= maxText {
		return s
	}
	r := []rune(s)
	return string(r[:maxText-3]) + "..."
}

// countOf is the length of the array when a singular path (names and indexes only, RFC 9535)
// selects one array, and the number of selected nodes otherwise. "$.data.cards" with count 0
// then means "no cards", as a reader expects, and "$.data.cards[*]" keeps counting nodes.
func countOf(path *jsonpath.Path, nodes []any) int {
	if len(nodes) == 1 && path.Query().Singular() != nil {
		if items, ok := nodes[0].([]any); ok {
			return len(items)
		}
	}
	return len(nodes)
}
