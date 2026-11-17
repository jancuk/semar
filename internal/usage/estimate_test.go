package usage

import (
	"strings"
	"testing"
	"time"
)

func criteriaRecord(provider, kind, asked, model string, jobs int, cost float64) Record {
	return Record{
		At: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC), Provider: provider, Kind: kind, Model: model, Asked: asked,
		Command: "plan", Pipeline: "criteria", Outcome: "ok", Attempts: jobs, CostUSD: cost,
	}
}

func writeAll(t *testing.T, home string, recs ...Record) {
	t.Helper()
	for _, r := range recs {
		if err := Append(home, r); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEstimateCostIsTheMeanCostOfAJobTimesTheCriteria(t *testing.T) {
	home := t.TempDir()
	writeAll(t, home,
		criteriaRecord("claude", "claude", "haiku", "claude-haiku-4-5", 16, 0.08),
		criteriaRecord("claude", "claude", "haiku", "claude-haiku-4-5", 8, 0.04),
		criteriaRecord("claude", "claude", "sonnet", "claude-sonnet-4-5", 4, 0.9),
	)
	e, err := EstimateCost(home, "claude", "haiku", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !e.Known || e.Jobs != 24 || e.Criteria != 10 || e.USD < 0.0499 || e.USD > 0.0501 {
		t.Fatalf("estimate = %+v", e)
	}
	if got := e.String(); got != "about $0.05 for 10 criteria, from 24 earlier jobs of claude haiku" {
		t.Errorf("String = %q", got)
	}
}

func TestEstimateCostSaysSoWhenThereIsNoRecord(t *testing.T) {
	home := t.TempDir()
	e, err := EstimateCost(home, "claude", "haiku", 10)
	if err != nil || e.Known {
		t.Fatalf("estimate = %+v, %v", e, err)
	}
	if got := e.String(); !strings.HasPrefix(got, "no estimate yet:") || !strings.Contains(got, "claude haiku") {
		t.Errorf("String = %q", got)
	}
}

func TestEstimateCostIgnoresWhatIsNotAJobOfTheCriteriaPipelineOfThatModel(t *testing.T) {
	home := t.TempDir()
	single := criteriaRecord("claude", "claude", "", "claude-opus-4-5", 2, 1.6)
	single.Pipeline = ""
	failed := criteriaRecord("claude", "claude", "haiku", "claude-haiku-4-5", 5, 0.5)
	failed.Outcome = "failed:timeout"
	writeAll(t, home, single, failed,
		criteriaRecord("other", "claude", "haiku", "claude-haiku-4-5", 5, 0.5),
		criteriaRecord("claude", "claude", "sonnet", "claude-sonnet-4-5", 5, 0.5),
	)
	if e, _ := EstimateCost(home, "claude", "haiku", 10); e.Known {
		t.Errorf("estimate = %+v, want none", e)
	}
}

func TestEstimateCostSaysWhenTheProviderReportsNoCost(t *testing.T) {
	home := t.TempDir()
	writeAll(t, home, criteriaRecord("codex", "codex", "gpt-6-luna", "gpt-6-luna", 16, 0))
	e, _ := EstimateCost(home, "codex", "gpt-6-luna", 10)
	if e.Known || !strings.Contains(e.String(), "reports no cost") {
		t.Errorf("estimate = %+v, %q", e, e.String())
	}
}

func TestEstimateCostMatchesTheModelThatWasAskedForOrReported(t *testing.T) {
	home := t.TempDir()
	old := criteriaRecord("claude", "claude", "", "claude-haiku-4-5", 10, 0.1)
	writeAll(t, home, old)
	if e, _ := EstimateCost(home, "claude", "haiku", 10); !e.Known {
		t.Errorf("a record with no asked model should match the reported model: %+v", e)
	}
	if e, _ := EstimateCost(home, "claude", "claude-haiku-4-5", 10); !e.Known {
		t.Errorf("the reported name should match: %+v", e)
	}
}
