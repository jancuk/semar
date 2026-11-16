package obligation

import (
	"fmt"
	"strings"
)

// SummaryLine is the line that opens a report: "42 use cases: 35 verified, 2 failed, 3 blocked,
// 2 need a person". It is empty when there is no obligation. A second sentence counts the
// manual results apart, because a person's check is not a scenario pass (L5).
func SummaryLine(entries []Entry) string {
	if len(entries) == 0 {
		return ""
	}
	count := map[State]int{}
	passed, failed := 0, 0
	for _, e := range entries {
		count[e.State]++
		if e.Manual != nil {
			if e.Manual.Result == "passed" {
				passed++
			} else {
				failed++
			}
		}
	}
	noun := "use cases"
	if len(entries) == 1 {
		noun = "use case"
	}
	parts := []string{fmt.Sprintf("%d verified", count[Verified])}
	if n := count[Failed]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", n))
	}
	if n := count[Blocked]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d blocked", n))
	}
	if n := count[NeedsAPerson]; n > 0 {
		verb := "need"
		if n == 1 {
			verb = "needs"
		}
		parts = append(parts, fmt.Sprintf("%d %s a person", n, verb))
	}
	if n := count[Deferred]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d deferred", n))
	}
	line := fmt.Sprintf("%d %s: %s", len(entries), noun, strings.Join(parts, ", "))
	var checked []string
	if passed > 0 {
		checked = append(checked, fmt.Sprintf("%d passed", passed))
	}
	if failed > 0 {
		checked = append(checked, fmt.Sprintf("%d failed", failed))
	}
	if len(checked) > 0 {
		line += ". Checked by a person: " + strings.Join(checked, ", ") + "."
	}
	return line
}
