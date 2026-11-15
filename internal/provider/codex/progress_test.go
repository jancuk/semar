//go:build unix

package codex_test

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

func (r *recorder) markReturned() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.returned = true
}

func (r *recorder) check(t *testing.T, want []provider.Progress, res provider.Result) {
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
	last := r.reports[len(r.reports)-1]
	if last.ToolCalls != calls || last.Turns != res.Turns {
		t.Fatalf("the last report is %+v, the result has %d turns and %d tool calls", last, res.Turns, calls)
	}
}

func writeEvents(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func shellItem(kind, id, output string) string {
	return fmt.Sprintf(`{"type":%q,"item":{"id":%q,"type":"command_execution","command":"cat a.go","aggregated_output":%q,"status":"in_progress"}}`, kind, id, output)
}

const (
	answerLine = `{"type":"item.completed","item":{"id":"item_9","type":"agent_message","text":"{\"method\":\"GET\",\"path\":\"/v1/orders\"}"}}`
	turnLine   = `{"type":"turn.completed","usage":{"input_tokens":3,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":1}}`
)

func TestProgress(t *testing.T) {
	longTool := strings.Repeat("t", 300)
	cases := []struct {
		name      string
		fixture   func(t *testing.T) string
		want      []provider.Progress
		wantTools []provider.ToolUse
	}{
		{"the success fixture", func(*testing.T) string { return "success.jsonl" }, []provider.Progress{
			{ToolCalls: 1, Tool: "shell"},
			{ToolCalls: 2, Tool: "shell"},
			{ToolCalls: 3, Tool: "mcp__orders__list_orders"},
			{ToolCalls: 4, Tool: "mcp__orders__list_orders"},
			{Turns: 1, ToolCalls: 4, Tool: "mcp__orders__list_orders"},
		}, []provider.ToolUse{{Name: "mcp__orders__list_orders", Calls: 2}, {Name: "shell", Calls: 2}}},
		{"the latest tool is the call that started last", func(t *testing.T) string {
			return writeEvents(t,
				shellItem("item.started", "item_1", ""),
				`{"type":"item.started","item":{"id":"item_2","type":"web_search","status":"in_progress"}}`,
				shellItem("item.completed", "item_1", "package a"),
				answerLine, turnLine)
		}, []provider.Progress{
			{ToolCalls: 1, Tool: "shell"},
			{ToolCalls: 2, Tool: "web_search"},
			{Turns: 1, ToolCalls: 2, Tool: "web_search"},
		}, []provider.ToolUse{{Name: "shell", Calls: 1}, {Name: "web_search", Calls: 1}}},
		{"a line above the limit", func(t *testing.T) string {
			return writeEvents(t,
				shellItem("item.started", "item_1", ""),
				shellItem("item.completed", "item_1", strings.Repeat("x", provider.MaxStdoutLine+10)),
				shellItem("item.started", "item_2", ""),
				answerLine, turnLine)
		}, []provider.Progress{
			{ToolCalls: 1, Tool: "shell"},
			{ToolCalls: 2, Tool: "shell"},
			{Turns: 1, ToolCalls: 2, Tool: "shell"},
		}, []provider.ToolUse{{Name: "shell", Calls: 2}}},
		{"a long tool name is cut", func(t *testing.T) string {
			return writeEvents(t,
				fmt.Sprintf(`{"type":"item.started","item":{"id":"item_1","type":"mcp_tool_call","server":"s","tool":%q,"status":"in_progress"}}`, longTool),
				answerLine, turnLine)
		}, []provider.Progress{
			{ToolCalls: 1, Tool: ("mcp__s__" + longTool)[:provider.MaxToolName]},
			{Turns: 1, ToolCalls: 1, Tool: ("mcp__s__" + longTool)[:provider.MaxToolName]},
		}, []provider.ToolUse{{Name: "mcp__s__" + longTool, Calls: 1}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newBinary(t, c.fixture(t), 0, "")
			var r recorder
			j := job(t)
			j.Progress = r.report
			res, err := newDriver(t, config.Provider{}, b.path).Run(context.Background(), j)
			r.markReturned()
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(res.Tools, c.wantTools) {
				t.Fatalf("tools = %+v", res.Tools)
			}
			r.check(t, c.want, res)
		})
	}
}

func TestProgressArrivesWhileTheJobRuns(t *testing.T) {
	// The harness writes its first call, and the rest only after the first report has created
	// the gate file.
	dir := t.TempDir()
	first := writeEvents(t, `{"type":"turn.started"}`, shellItem("item.started", "item_1", ""))
	rest := writeEvents(t, shellItem("item.completed", "item_1", "package a"), answerLine, turnLine)
	gate := filepath.Join(dir, "gate")
	script := filepath.Join(dir, "codex-gated")
	body := fmt.Sprintf("#!/bin/sh\ncat > /dev/null\ncat %q\ni=0\nwhile [ ! -e %q ]; do i=$((i+1)); [ $i -gt 1000 ] && exit 9; sleep 0.01; done\ncat %q\n", first, gate, rest)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	var r recorder
	j := job(t)
	j.Progress = func(p provider.Progress) {
		r.report(p)
		if p.ToolCalls == 1 {
			if err := os.WriteFile(gate, nil, 0o600); err != nil {
				t.Error(err)
			}
		}
	}
	res, err := newDriver(t, config.Provider{}, script).Run(context.Background(), j)
	r.markReturned()
	if err != nil {
		t.Fatalf("the first report did not arrive while the job ran: %v", err)
	}
	r.check(t, []provider.Progress{{ToolCalls: 1, Tool: "shell"}, {Turns: 1, ToolCalls: 1, Tool: "shell"}}, res)
}
