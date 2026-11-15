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
	in.SetupOn = true
	in.Reasoning = "high"
	in.LeftOut = []LeftOut{{Path: "assets/logo.png", Reason: "larger than 5 MiB"}, {Path: "vendor/lib", Reason: "a submodule"}}
	d := draft(t)
	scs, scripts, ps := Validate(d, in)
	if len(ps) > 0 {
		t.Fatalf("problems: %v", ps)
	}
	out := Output{
		Draft: d, Scenarios: scs, Scripts: scripts, Attempts: 2,
		Result: provider.Result{
			Model: "model-x", Reasoning: "high", InputTokens: 1234, OutputTokens: 56, Denied: 2,
			Tools:  []provider.ToolUse{{Name: "Read", Calls: 7}, {Name: "mcp__tracker__get_issue", Calls: 1}, {Name: "mcp__wiki__search", Calls: 2}},
			Loaded: provider.Loaded{MCPServers: []string{"tracker", "wiki"}, Skills: 3, PermissionMode: "default"},
		},
		PlannerID: "claude", PlannerKind: "claude",
	}
	var b strings.Builder
	if err := Render(&b, in, out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "tracker") || strings.Contains(b.String(), "wiki") {
		t.Error("plan.md names an MCP server")
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

func TestSetupLine(t *testing.T) {
	loaded := provider.Loaded{MCPServers: []string{"secret-server-name"}, Skills: 2, PermissionMode: "plan"}
	cases := []struct {
		name  string
		on    bool
		l     provider.Loaded
		want  string
		never string
	}{
		{"off", false, loaded, "the planner had the three file tools only", "secret-server-name"},
		{"on and reported", true, loaded, "the planner could use 1 MCP servers and 2 skills of your own setup, under the permission mode plan", "secret-server-name"},
		{"on and not reported", true, provider.Loaded{}, "did not report what it loaded", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := setupLine(tc.on, tc.l)
			if !strings.Contains(got, tc.want) {
				t.Errorf("got %q, want it to hold %q", got, tc.want)
			}
			if tc.never != "" && strings.Contains(got, tc.never) {
				t.Errorf("%q names a server", got)
			}
		})
	}
}

func TestRenderShowsReasoningOnlyWhenSet(t *testing.T) {
	render := func(in Input, level string) string {
		level2 := in.Reasoning
		in = input(t)
		in.Reasoning = level2
		d := draft(t)
		scs, scripts, ps := Validate(d, in)
		if len(ps) > 0 {
			t.Fatalf("problems: %v", ps)
		}
		var b strings.Builder
		out := Output{Draft: d, Scenarios: scs, Scripts: scripts, Attempts: 1, Result: provider.Result{Reasoning: level}, PlannerID: "claude", PlannerKind: "claude"}
		if err := Render(&b, in, out); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	if got := render(Input{}, ""); strings.Contains(got, "Reasoning") {
		t.Errorf("plan.md names a level that was not set:\n%s", got)
	}
	if got := render(Input{}, "low"); !strings.Contains(got, "- Reasoning: low\n") {
		t.Errorf("the level of the result is missing:\n%s", got)
	}
	if got := render(Input{Reasoning: "max"}, ""); !strings.Contains(got, "- Reasoning: max\n") {
		t.Errorf("the level of the input is missing:\n%s", got)
	}
}
