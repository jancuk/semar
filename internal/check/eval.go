// Package check decides whether one check holds for one recorded action. It is
// pure: it does no I/O and changes none of its arguments.
package check

import (
	"encoding/json"
	"fmt"
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
	switch {
	case kinds != 1:
		return "it must name exactly one of status, jsonpath, or header"
	case c.Status != nil && len(ops) > 0:
		return "a status check takes no operator"
	case c.Status == nil && len(ops) != 1:
		return "a jsonpath or header check needs exactly one operator"
	}
	return ""
}

func operators(c domain.Check) []string {
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
	if subject == "" {
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
	var doc any
	if err := json.Unmarshal(resp.Body, &doc); err != nil {
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
		res.State = state(len(nodes) == *c.Count)
		return
	}

	value, ok := single(res, nodes, c.JSONPath)
	if !ok {
		return
	}
	if isRedacted(value) {
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
		res.State = state(equalJSON(value, want) == (op == "equals"))
	case "contains":
		evalContains(res, c.Contains, value)
	case "greater", "less":
		n, isNumber := value.(float64)
		if !isNumber {
			res.State = domain.CheckFalse
			res.Detail = op + " needs a number, got " + typeName(value)
			return
		}
		if op == "greater" {
			res.State = state(n > *c.Greater)
		} else {
			res.State = state(n < *c.Less)
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
	if isRedacted(second) {
		notEvaluated(res, redactedValue)
		return
	}
	a, errA := normalise(first)
	b, errB := normalise(second)
	if errA != nil || errB != nil {
		notEvaluated(res, "the values cannot be compared")
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
		case redact.IsMarker(v):
			redacted = true
		case op == "contains" && strings.Contains(v, want), op != "contains" && v == want:
			matched = true
		}
	}
	switch {
	case matched:
		res.State = state(op != "notEquals")
	case redacted:
		notEvaluated(res, redactedValue)
	default:
		res.State = state(op == "notEquals")
	}
}

func isRedacted(v any) bool {
	s, ok := v.(string)
	return ok && redact.IsMarker(s)
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

// normalise passes a value through encoding/json, so a YAML int and a decoded
// JSON number both become float64, and a map or slice becomes the shape the
// response body decodes to.
func normalise(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func equalJSON(a, b any) bool {
	af, aIsNumber := a.(float64)
	bf, bIsNumber := b.(float64)
	if aIsNumber && bIsNumber {
		return af == bf
	}
	return reflect.DeepEqual(a, b)
}

func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case float64, int, int64, uint64:
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
