package prompt

import (
	"strings"
	"testing"
)

func TestPlanVersion(t *testing.T) {
	if got := PlanVersion(); got != "1" {
		t.Errorf("PlanVersion() = %q, want 1", got)
	}
}

func TestPlanRendersEveryField(t *testing.T) {
	d := PlanData{
		ChangeTable:  "CHANGE-TABLE",
		Services:     "`orders`",
		Methods:      "GET, POST",
		MaxScenarios: 7,
		Existing:     "  - `SHOP-1-01`: an old one",
	}
	first, err := Plan(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"CHANGE-TABLE", "`orders`", "GET, POST", "At most 7 scenarios", "SHOP-1-01", "{{name}}"} {
		if !strings.Contains(first, want) {
			t.Errorf("the first prompt lacks %q", want)
		}
	}
	for _, unwanted := range []string{"version: 1", "previous draft was rejected", "[[", "]]"} {
		if strings.Contains(first, unwanted) {
			t.Errorf("the first prompt holds %q", unwanted)
		}
	}

	d.Attempt = true
	d.Problems = "- `scenarios[0].title`: a title [[.Problems]] {{order_id}}"
	d.Existing = ""
	again, err := Plan(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"previous draft was rejected", ".semar-plan/previous.json", d.Problems} {
		if !strings.Contains(again, want) {
			t.Errorf("the second prompt lacks %q", want)
		}
	}
	if strings.Contains(again, "already exist") {
		t.Error("the prompt lists existing scenarios when there are none")
	}
}

func TestSplit(t *testing.T) {
	for _, src := range []string{"", "hello\n", "version:\nbody", "versions: 2\n"} {
		if _, _, err := split(src); err == nil {
			t.Errorf("split(%q) accepted a prompt with no version line", src)
		}
	}
	v, body, err := split("version: 3\n\nbody\n")
	if err != nil || v != "3" || body != "body\n" {
		t.Errorf("split = %q, %q, %v", v, body, err)
	}
}
