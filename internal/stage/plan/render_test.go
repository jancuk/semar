package plan

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/provider"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func TestRenderGolden(t *testing.T) {
	in := input(t)
	in.Change.Commits = []domain.ChangeCommit{
		{ID: strings.Repeat("c", 40), Subject: "SHOP-482 reject a refund above the total", Author: "Ada", At: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC), Tickets: []string{"SHOP-482"}, Files: []string{"internal/refund/handler.go"}},
		{ID: strings.Repeat("d", 40), Subject: "SHOP-483 rename the reader", Author: "Ada", At: time.Date(2026, 1, 1, 9, 1, 0, 0, time.UTC), Tickets: []string{"SHOP-483"}, Files: []string{"internal/orders/read.go"}},
	}
	in.Change.Tickets = []domain.TicketChange{
		{Key: "SHOP-482", Commits: []string{strings.Repeat("c", 40)}, Files: []string{"internal/refund/handler.go"}},
		{Key: "SHOP-483", Commits: []string{strings.Repeat("d", 40)}, Files: []string{"internal/orders/read.go"}},
	}
	in.Change.Files = []domain.FileChange{
		{Path: "internal/orders/read.go", Status: "modified", Kind: "source", Added: 2, Deleted: 1, Tickets: []string{"SHOP-483"}},
		{Path: "internal/refund/handler.go", Status: "modified", Kind: "source", Added: 9, Deleted: 0, Tickets: []string{"SHOP-482"}},
	}
	d := draft(t)
	scs, scripts, ps := Validate(d, in)
	if len(ps) > 0 {
		t.Fatalf("problems: %v", ps)
	}
	out := Output{
		Draft: d, Scenarios: scs, Scripts: scripts, Attempts: 2,
		Result:    provider.Result{Model: "model-x", InputTokens: 1234, OutputTokens: 56},
		PlannerID: "claude", PlannerKind: "claude",
	}
	var b strings.Builder
	if err := Render(&b, in, out); err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "plan.golden.md")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if b.String() != string(want) {
		t.Errorf("plan.md differs from %s; rerun with -update if the change is intended:\n%s", golden, b.String())
	}
}

func TestParagraphKeepsTextFromStartingAList(t *testing.T) {
	for in, want := range map[string]string{"- a": `\- a`, "+ a": `\+ a`, "12. a": `12\. a`, "3) a": `3\) a`, "a - b": "a - b"} {
		if got := paragraph(in); got != want {
			t.Errorf("paragraph(%q) = %q, want %q", in, got, want)
		}
	}
}
