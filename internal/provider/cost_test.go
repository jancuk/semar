package provider

import "testing"

func TestReportsCostOnlyForAKindThatReportsIt(t *testing.T) {
	for kind, want := range map[string]bool{KindClaude: true, KindFake: true, KindCodex: false, KindAnthropic: false, KindOpenAI: false} {
		if got := ReportsCost(kind); got != want {
			t.Errorf("ReportsCost(%s) = %v, want %v", kind, got, want)
		}
	}
}
