//go:build unix

package claude_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
)

// recorder keeps the reports of a job and notes a report that comes after Run returned.
type recorder struct {
	mu       sync.Mutex
	reports  []provider.Progress
	returned bool
	late     int
}

func (r *recorder) report(p provider.Progress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.returned {
		r.late++
	}
	r.reports = append(r.reports, p)
}

// run starts the job with the recorder and marks the return.
func (r *recorder) run(t *testing.T, d interface {
	Run(context.Context, provider.Job) (provider.Result, error)
}, j provider.Job) (provider.Result, error) {
	t.Helper()
	j.Progress = r.report
	res, err := d.Run(context.Background(), j)
	r.mu.Lock()
	r.returned = true
	r.mu.Unlock()
	return res, err
}

// check compares the reports with the result. skipped is the number of tool calls that the result
// counts and the live reports do not, because they were on a line above the limit.
func (r *recorder) check(t *testing.T, want []provider.Progress, res provider.Result, skipped int) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if !slices.Equal(r.reports, want) {
		t.Fatalf("reports = %+v\nwant      %+v", r.reports, want)
	}
	if r.late != 0 {
		t.Fatalf("%d reports after Run returned", r.late)
	}
	calls := 0
	for _, u := range res.Tools {
		calls += u.Calls
	}
	if last := r.reports[len(r.reports)-1]; last.ToolCalls+skipped != calls {
		t.Fatalf("the last report has %d tool calls, the result %d", last.ToolCalls, calls)
	}
}

func writeStream(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "stream.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func assistant(id string, calls ...string) string {
	parts := make([]string, 0, len(calls))
	for _, c := range calls {
		callID, name, _ := strings.Cut(c, ":")
		parts = append(parts, fmt.Sprintf(`{"type":"tool_use","id":%q,"name":%q,"input":{}}`, callID, name))
	}
	return fmt.Sprintf(`{"type":"assistant","message":{"id":%q,"content":[%s]}}`, id, strings.Join(parts, ","))
}

const (
	initLine   = `{"type":"system","subtype":"init","tools":["Glob","Grep","Read","StructuredOutput"],"mcp_servers":[],"skills":[],"permissionMode":"default"}`
	resultLine = `{"type":"result","subtype":"success","is_error":false,"num_turns":3,"usage":{"input_tokens":3,"output_tokens":5},"structured_output":{"method":"GET"}}`
)

func TestProgress(t *testing.T) {
	// A long line that would change the totals if it were parsed: a new message and a new call.
	bigAssistant := `{"type":"assistant","message":{"id":"m-long","content":[{"type":"tool_use","id":"t-long","name":"Edit","input":{"pad":"` +
		strings.Repeat("x", provider.MaxStdoutLine+10) + `"}}]}}`
	longName := strings.Repeat("n", 300)
	cases := []struct {
		name      string
		setup     config.Setup
		fixture   func(t *testing.T) string
		want      []provider.Progress
		wantTurns int
		wantTools []provider.ToolUse
		skipped   int // tool calls in the result that no live report counted
	}{
		{"without the setup", config.Setup{}, func(*testing.T) string { return "success.jsonl" }, []provider.Progress{
			{Turns: 1},
			{Turns: 1, ToolCalls: 1, Tool: "Grep"},
			{Turns: 2, ToolCalls: 2, Tool: "Read"},
			{Turns: 2, ToolCalls: 3, Tool: "Glob"},
			{Turns: 3, ToolCalls: 4, Tool: "Read"},
			{Turns: 4, ToolCalls: 4, Tool: "Read"},
		}, 4, []provider.ToolUse{{Name: "Glob", Calls: 1}, {Name: "Grep", Calls: 1}, {Name: "Read", Calls: 2}}, 0},
		// The harness reports four turns for three messages, and its number is the result's.
		{"with the setup", config.Setup{MCP: true, Skills: true}, func(*testing.T) string { return "stream_success.jsonl" }, []provider.Progress{
			{Turns: 1},
			{Turns: 1, ToolCalls: 1, Tool: "Read"},
			{Turns: 1, ToolCalls: 2, Tool: "Read"},
			{Turns: 2, ToolCalls: 5, Tool: "mcp__orders__delete"},
			{Turns: 3, ToolCalls: 5, Tool: "mcp__orders__delete"},
		}, 4, []provider.ToolUse{{Name: "Grep", Calls: 1}, {Name: "Read", Calls: 2}, {Name: "mcp__orders__delete", Calls: 1}, {Name: "mcp__orders__list", Calls: 1}}, 0},
		{"a line above the limit", config.Setup{}, func(t *testing.T) string {
			return writeStream(t, initLine, assistant("m1", "t1:Read"), bigAssistant, assistant("m2", "t2:Grep"), assistant("m3", "t3:StructuredOutput"), resultLine)
		}, []provider.Progress{
			{Turns: 1, ToolCalls: 1, Tool: "Read"},
			{Turns: 2, ToolCalls: 2, Tool: "Grep"},
			{Turns: 3, ToolCalls: 2, Tool: "Grep"},
			// The final parse reads the whole output, so it counts the call on the long line, and
			// the last live report has one call fewer than the result.
		}, 3, []provider.ToolUse{{Name: "Edit", Calls: 1}, {Name: "Grep", Calls: 1}, {Name: "Read", Calls: 1}}, 1},
		{"a long tool name is cut", config.Setup{}, func(t *testing.T) string {
			return writeStream(t, initLine, assistant("m1", "t1:"+longName), resultLine)
		}, []provider.Progress{
			{Turns: 1, ToolCalls: 1, Tool: longName[:provider.MaxToolName]},
		}, 3, []provider.ToolUse{{Name: longName, Calls: 1}}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newBinary(t, c.fixture(t), 0, "")
			var r recorder
			res, err := r.run(t, newSetupDriver(t, config.Provider{Setup: c.setup}, b.path), job(t))
			if err != nil {
				t.Fatal(err)
			}
			if res.Turns != c.wantTurns || !slices.Equal(res.Tools, c.wantTools) {
				t.Fatalf("turns %d, tools %+v", res.Turns, res.Tools)
			}
			r.check(t, c.want, res, c.skipped)
		})
	}
}

func TestProgressArrivesWhileTheJobRuns(t *testing.T) {
	// The harness writes its first response, and the rest only after the first report has
	// created the gate file.
	dir := t.TempDir()
	first := writeStream(t, initLine, assistant("m1", "t1:Read"))
	rest := writeStream(t, assistant("m2", "t2:StructuredOutput"), resultLine)
	gate := filepath.Join(dir, "gate")
	script := filepath.Join(dir, "claude-gated")
	body := fmt.Sprintf("#!/bin/sh\ncat > /dev/null\ncat %q\ni=0\nwhile [ ! -e %q ]; do i=$((i+1)); [ $i -gt 1000 ] && exit 9; sleep 0.01; done\ncat %q\n", first, gate, rest)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	r := recorder{}
	j := job(t)
	j.Progress = func(p provider.Progress) {
		r.report(p)
		if p.Turns == 1 {
			if err := os.WriteFile(gate, nil, 0o600); err != nil {
				t.Error(err)
			}
		}
	}
	res, err := newDriver(t, config.Provider{}, script).Run(context.Background(), j)
	if err != nil {
		t.Fatalf("the first report did not arrive while the job ran: %v", err)
	}
	r.check(t, []provider.Progress{{Turns: 1, ToolCalls: 1, Tool: "Read"}, {Turns: 2, ToolCalls: 1, Tool: "Read"}}, res, 0)
}
