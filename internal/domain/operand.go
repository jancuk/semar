package domain

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// operandVariable is a well-formed {{name}}: a capture, run.id, or <alias>.<output>.
var operandVariable = regexp.MustCompile(`\{\{([A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)*)\}\}`)

// operandStrings lists the string operands of c that may hold variables, by operator name.
func operandStrings(c Check) []struct{ Op, Text string } {
	var out []struct{ Op, Text string }
	for _, o := range []struct {
		op string
		v  any
	}{{"equals", c.Equals}, {"notEquals", c.NotEquals}, {"contains", c.Contains}} {
		if s, ok := o.v.(string); ok {
			out = append(out, struct{ Op, Text string }{o.op, s})
		}
	}
	return out
}

// OperandVariables lists the variable names in the string operands of equals, notEquals, and
// contains, in order and with repeats removed.
func OperandVariables(c Check) []string {
	var names []string
	for _, o := range operandStrings(c) {
		for _, m := range operandVariable.FindAllStringSubmatch(o.Text, -1) {
			if !slices.Contains(names, m[1]) {
				names = append(names, m[1])
			}
		}
	}
	return names
}

// OperandMalformed says which operand holds a "{{" that is not a well-formed variable, or "".
func OperandMalformed(c Check) string {
	for _, o := range operandStrings(c) {
		if strings.Contains(operandVariable.ReplaceAllString(o.Text, ""), "{{") {
			return fmt.Sprintf("%s holds a malformed variable", o.Op)
		}
	}
	return ""
}

// CapturedText is the value of a variable that is the whole operand of equals or notEquals. A
// capture keeps the text of a value and not its type, so a check compares it with the text of what
// it reads: the capture of the number 0 equals the number 0, which the string "0" never does.
type CapturedText string

// FillOperands returns a copy of c with every variable of its string operands replaced by what
// lookup gives, and the names that lookup had no value for, which stay as written. A replaced
// value is not scanned again. An equals or a notEquals that is one variable and nothing else becomes
// a CapturedText.
func FillOperands(c Check, lookup func(name string) (string, bool)) (Check, []string) {
	var missing []string
	fill := func(v *any, compares bool) {
		s, ok := (*v).(string)
		if !ok {
			return
		}
		if m := operandVariable.FindStringSubmatch(s); compares && m != nil && m[0] == s {
			if val, found := lookup(m[1]); found {
				*v = CapturedText(val)
				return
			}
		}
		*v = operandVariable.ReplaceAllStringFunc(s, func(m string) string {
			name := operandVariable.FindStringSubmatch(m)[1]
			val, found := lookup(name)
			if !found {
				if !slices.Contains(missing, name) {
					missing = append(missing, name)
				}
				return m
			}
			return val
		})
	}
	fill(&c.Equals, true)
	fill(&c.NotEquals, true)
	fill(&c.Contains, false)
	return c, missing
}
