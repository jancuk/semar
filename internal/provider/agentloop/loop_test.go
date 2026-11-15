package agentloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/provider"
)

const testSchema = `{"type":"object","properties":{"summary":{"type":"string"}},"required":["summary"]}`

// script is a Client that answers with one scripted step per request.
type script struct {
	t     *testing.T
	steps []func(req Request) (Response, error)
	reqs  []Request
}

func (s *script) Next(ctx context.Context, req Request) (Response, error) {
	s.t.Helper()
	req.Messages = slices.Clone(req.Messages)
	s.reqs = append(s.reqs, req)
	if len(s.steps) == 0 {
		s.t.Fatalf("request %d has no scripted answer", len(s.reqs))
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	return step(req)
}

func calls(cs ...ToolCall) Response {
	return Response{Message: Message{Role: RoleAssistant, Calls: cs}, Usage: Usage{Input: 100, Output: 10, CacheRead: 60, CacheWrite: 20}, Model: "acme-1"}
}

func call(id, name, args string) ToolCall {
	return ToolCall{ID: id, Name: name, Args: json.RawMessage(args)}
}

func newJob(t *testing.T) provider.Job {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "orders"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "orders", "handler.go"), "package orders\n\nfunc Refund() {}\n")
	return provider.Job{Prompt: "Plan the tests.", Workdir: dir, Schema: json.RawMessage(testSchema), MaxTurns: 5, Timeout: time.Minute}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func classOf(t *testing.T, err error) provider.FailureClass {
	t.Helper()
	var perr *provider.Error
	if !errors.As(err, &perr) {
		t.Fatalf("error %v is not a *provider.Error", err)
	}
	return perr.Class
}

func TestRunFullConversation(t *testing.T) {
	job := newJob(t)
	s := &script{t: t}
	s.steps = append(s.steps,
		func(req Request) (Response, error) {
			if len(req.Messages) != 1 || req.Messages[0].Role != RoleUser || req.Messages[0].Text != job.Prompt {
				t.Fatalf("first request messages = %+v", req.Messages)
			}
			if req.System == "" {
				t.Error("no system text")
			}
			var names []string
			for _, tl := range req.Tools {
				names = append(names, tl.Name)
			}
			if !slices.Equal(names, []string{"read_file", "search", "list_files", "submit"}) {
				t.Errorf("tools = %v", names)
			}
			if string(req.Tools[3].Schema) != testSchema {
				t.Errorf("submit schema = %s", req.Tools[3].Schema)
			}
			return calls(
				call("c1", "read_file", `{"path":"orders/handler.go"}`),
				call("c2", "list_files", `{"glob":"**/*.go"}`),
				call("c3", "search", `{"pattern":"func \\w+","glob":"*.go"}`),
			), nil
		},
		func(req Request) (Response, error) {
			last := req.Messages[len(req.Messages)-1]
			if last.Role != RoleTool || len(last.Results) != 3 {
				t.Fatalf("last message = %+v", last)
			}
			want := map[string]string{
				"c1": "3\tfunc Refund() {}",
				"c2": "orders/handler.go",
				"c3": "orders/handler.go:3: func Refund() {}",
			}
			for _, r := range last.Results {
				if r.IsError || !strings.Contains(r.Text, want[r.CallID]) {
					t.Errorf("result %s = %q, error %v", r.CallID, r.Text, r.IsError)
				}
			}
			if req.Messages[1].Role != RoleAssistant || len(req.Messages[1].Calls) != 3 {
				t.Errorf("assistant message = %+v", req.Messages[1])
			}
			return calls(call("c4", "submit", `{"summary":"one refund route"}`)), nil
		},
	)
	res, err := Run(context.Background(), s, job)
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Output) != `{"summary":"one refund route"}` {
		t.Errorf("output = %s", res.Output)
	}
	if res.Turns != 2 || res.InputTokens != 200 || res.OutputTokens != 20 || res.CacheReadTokens != 120 || res.CacheWriteTokens != 40 {
		t.Errorf("usage = %+v", res)
	}
	if res.Model != "acme-1" || res.CostUSD != 0 {
		t.Errorf("model %q, cost %v", res.Model, res.CostUSD)
	}
	wantTools := []provider.ToolUse{{Name: "list_files", Calls: 1}, {Name: "read_file", Calls: 1}, {Name: "search", Calls: 1}, {Name: "submit", Calls: 1}}
	if !slices.Equal(res.Tools, wantTools) {
		t.Errorf("tools = %+v", res.Tools)
	}
}

func TestRunTurnLimit(t *testing.T) {
	job := newJob(t)
	job.MaxTurns = 3
	s := &script{t: t}
	for range 3 {
		s.steps = append(s.steps, func(Request) (Response, error) {
			return calls(call("c", "read_file", `{"path":"orders/handler.go"}`)), nil
		})
	}
	res, err := Run(context.Background(), s, job)
	if classOf(t, err) != provider.ValidationError || !strings.Contains(err.Error(), "reached the turn limit") {
		t.Fatalf("error = %v", err)
	}
	if res.Turns != 3 || len(s.reqs) != 3 || res.InputTokens != 300 {
		t.Errorf("turns %d, requests %d, input %d", res.Turns, len(s.reqs), res.InputTokens)
	}
	if !slices.Equal(res.Tools, []provider.ToolUse{{Name: "read_file", Calls: 3}}) {
		t.Errorf("tools = %+v", res.Tools)
	}
}

type blocking struct{}

func (blocking) Next(ctx context.Context, _ Request) (Response, error) {
	<-ctx.Done()
	return Response{}, ctx.Err()
}

func TestRunTimeout(t *testing.T) {
	job := newJob(t)
	job.Timeout = 20 * time.Millisecond
	_, err := Run(context.Background(), blocking{}, job)
	if classOf(t, err) != provider.TransportError || !strings.Contains(err.Error(), "did not finish in 20ms") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunCancelled(t *testing.T) {
	job := newJob(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)
	_, err := Run(ctx, blocking{}, job)
	var perr *provider.Error
	if !errors.Is(err, context.Canceled) || errors.As(err, &perr) {
		t.Fatalf("error = %v", err)
	}
}

func TestRunReminder(t *testing.T) {
	job := newJob(t)
	s := &script{t: t}
	s.steps = append(s.steps,
		func(Request) (Response, error) {
			return Response{Message: Message{Text: "Here is my plan."}, Usage: Usage{Input: 5}}, nil
		},
		func(req Request) (Response, error) {
			last := req.Messages[len(req.Messages)-1]
			if last.Role != RoleUser || !strings.Contains(last.Text, "submit") {
				t.Fatalf("last message = %+v", last)
			}
			if req.Messages[1].Text != "Here is my plan." {
				t.Errorf("the reply was not kept: %+v", req.Messages[1])
			}
			return calls(call("c1", "submit", `{"summary":"s"}`)), nil
		},
	)
	res, err := Run(context.Background(), s, job)
	if err != nil {
		t.Fatal(err)
	}
	if res.Turns != 2 {
		t.Errorf("turns = %d", res.Turns)
	}
}

func TestRunNoToolCallTwice(t *testing.T) {
	job := newJob(t)
	s := &script{t: t}
	for range 2 {
		s.steps = append(s.steps, func(Request) (Response, error) { return Response{Message: Message{Text: "done"}}, nil })
	}
	_, err := Run(context.Background(), s, job)
	if classOf(t, err) != provider.ValidationError || !strings.Contains(err.Error(), "without calling submit") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "done") {
		t.Error("the error holds the model's text")
	}
}

func TestRunRefusal(t *testing.T) {
	job := newJob(t)
	s := &script{t: t}
	s.steps = append(s.steps, func(Request) (Response, error) {
		return Response{Message: Message{Text: "secret plan text"}, Stop: StopRefusal}, nil
	})
	_, err := Run(context.Background(), s, job)
	if classOf(t, err) != provider.ProviderError || strings.Contains(err.Error(), "secret plan text") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunToolErrorsGoBackToTheModel(t *testing.T) {
	job := newJob(t)
	s := &script{t: t}
	s.steps = append(s.steps,
		func(Request) (Response, error) {
			return calls(
				call("e1", "read_file", `{"path":"orders/missing.go"}`),
				call("e2", "search", `{"pattern":"(unclosed"}`),
				call("e3", "write_file", `{"path":"x"}`),
				call("e4", "submit", `["not","an","object"]`),
				call("e5", "read_file", `{"path":`),
			), nil
		},
		func(req Request) (Response, error) {
			last := req.Messages[len(req.Messages)-1]
			want := map[string]string{
				"e1": "no such file: orders/missing.go",
				"e2": "not a valid RE2 regular expression",
				"e3": `no tool named "write_file"`,
				"e4": "submit needs the complete result as one JSON object",
				"e5": "not a valid JSON object",
			}
			for _, r := range last.Results {
				if !r.IsError || !strings.Contains(r.Text, want[r.CallID]) {
					t.Errorf("result %s = %q, error %v", r.CallID, r.Text, r.IsError)
				}
			}
			return calls(call("ok", "submit", `{"summary":"s"}`)), nil
		},
	)
	res, err := Run(context.Background(), s, job)
	if err != nil {
		t.Fatal(err)
	}
	want := []provider.ToolUse{{Name: "read_file", Calls: 2}, {Name: "search", Calls: 1}, {Name: "submit", Calls: 2}, {Name: "unknown", Calls: 1}}
	if !slices.Equal(res.Tools, want) {
		t.Errorf("tools = %+v", res.Tools)
	}
}

func TestRunClientError(t *testing.T) {
	job := newJob(t)
	s := &script{t: t}
	s.steps = append(s.steps, func(Request) (Response, error) {
		return Response{}, &provider.Error{Class: provider.UsageLimit, Detail: "the API answered 429"}
	})
	_, err := Run(context.Background(), s, job)
	if classOf(t, err) != provider.UsageLimit {
		t.Fatalf("error = %v", err)
	}
}

func TestRunChecksTheJob(t *testing.T) {
	for name, mutate := range map[string]func(*provider.Job){
		"no prompt":    func(j *provider.Job) { j.Prompt = " " },
		"no turns":     func(j *provider.Job) { j.MaxTurns = 0 },
		"bad schema":   func(j *provider.Job) { j.Schema = json.RawMessage(`[1]`) },
		"no directory": func(j *provider.Job) { j.Workdir = filepath.Join(j.Workdir, "missing") },
	} {
		t.Run(name, func(t *testing.T) {
			job := newJob(t)
			mutate(&job)
			_, err := Run(context.Background(), &script{t: t}, job)
			if classOf(t, err) != provider.ValidationError {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestRunCallsPerResponse(t *testing.T) {
	job := newJob(t)
	s := &script{t: t}
	s.steps = append(s.steps,
		func(Request) (Response, error) {
			var cs []ToolCall
			for i := range 40 {
				cs = append(cs, call(fmt.Sprint("r", i), "read_file", `{"path":"orders/handler.go"}`))
			}
			return calls(cs...), nil
		},
		func(req Request) (Response, error) {
			results := req.Messages[len(req.Messages)-1].Results
			if len(results) != 40 {
				t.Fatalf("%d results", len(results))
			}
			for i, r := range results {
				refused := strings.Contains(r.Text, "at most 32 tool calls run per response")
				if refused != (i >= 32) || refused != r.IsError || (!refused && !strings.Contains(r.Text, "func Refund")) {
					t.Errorf("result %d = %q, error %v", i, r.Text, r.IsError)
				}
			}
			return calls(call("ok", "submit", `{"summary":"s"}`)), nil
		},
	)
	if _, err := Run(context.Background(), s, job); err != nil {
		t.Fatal(err)
	}
}

func TestRunResultBudget(t *testing.T) {
	old := maxJobResultBytes
	maxJobResultBytes = 50
	t.Cleanup(func() { maxJobResultBytes = old })
	job := newJob(t)
	read := call("r", "read_file", `{"path":"orders/handler.go"}`)
	s := &script{t: t}
	s.steps = append(s.steps,
		func(Request) (Response, error) { return calls(read, read), nil },
		func(req Request) (Response, error) {
			results := req.Messages[len(req.Messages)-1].Results
			if results[0].IsError || !strings.Contains(results[0].Text, "func Refund") {
				t.Errorf("first result = %q", results[0].Text)
			}
			if !results[1].IsError || !strings.Contains(results[1].Text, "reached 64 MiB") {
				t.Errorf("second result = %q", results[1].Text)
			}
			return calls(read), nil
		},
		func(req Request) (Response, error) {
			r := req.Messages[len(req.Messages)-1].Results[0]
			if !r.IsError || !strings.Contains(r.Text, "call submit") {
				t.Errorf("result after the budget = %q", r.Text)
			}
			return calls(call("ok", "submit", `{"summary":"s"}`)), nil
		},
	)
	if _, err := Run(context.Background(), s, job); err != nil {
		t.Fatal(err)
	}
}

func TestRunReportsProgress(t *testing.T) {
	answer := func(r Response) func(Request) (Response, error) {
		return func(Request) (Response, error) { return r, nil }
	}
	cases := []struct {
		name    string
		turns   int
		steps   []func(Request) (Response, error)
		want    []provider.Progress
		wantErr bool
	}{
		{"until submit", 5, []func(Request) (Response, error){
			answer(calls(call("c1", "read_file", `{"path":"orders/handler.go"}`), call("c2", "list_files", `{"glob":"**"}`))),
			answer(Response{Message: Message{Role: RoleAssistant, Text: "thinking"}, Stop: StopEnd}),
			answer(calls(call("c3", "delete_file", `{}`), call("c4", "search", `{"pattern":"Refund"}`))),
			answer(calls(call("c5", "submit", `{"summary":"done"}`))),
		}, []provider.Progress{
			{Turns: 1, ToolCalls: 2, Tool: "list_files"},
			{Turns: 2, ToolCalls: 2, Tool: "list_files"},
			{Turns: 3, ToolCalls: 4, Tool: "search"},
			{Turns: 4, ToolCalls: 5, Tool: "submit"},
		}, false},
		{"the turn limit", 2, []func(Request) (Response, error){
			answer(calls(call("c1", "rm -rf", `{}`))),
			answer(calls(call("c2", "read_file", `{"path":"orders/handler.go"}`))),
		}, []provider.Progress{
			{Turns: 1, ToolCalls: 1, Tool: "unknown"},
			{Turns: 2, ToolCalls: 2, Tool: "read_file"},
		}, true},
		{"a client error ends the reports", 5, []func(Request) (Response, error){
			answer(calls(call("c1", "list_files", `{"glob":"**"}`))),
			func(Request) (Response, error) {
				return Response{}, &provider.Error{Class: provider.ProviderError, Detail: "boom"}
			},
		}, []provider.Progress{
			{Turns: 1, ToolCalls: 1, Tool: "list_files"},
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			job := newJob(t)
			job.MaxTurns = c.turns
			var got []provider.Progress
			returned := false
			job.Progress = func(p provider.Progress) {
				if returned {
					t.Error("a report after Run returned")
				}
				got = append(got, p)
			}
			res, err := Run(context.Background(), &script{t: t, steps: c.steps}, job)
			returned = true
			if (err != nil) != c.wantErr {
				t.Fatalf("error = %v", err)
			}
			if !slices.Equal(got, c.want) {
				t.Fatalf("reports = %+v\nwant      %+v", got, c.want)
			}
			sum := 0
			for _, u := range res.Tools {
				sum += u.Calls
			}
			if last := got[len(got)-1]; last.Turns != res.Turns || last.ToolCalls != sum {
				t.Fatalf("the last report is %+v, the result has %d turns and %d tool calls", last, res.Turns, sum)
			}
		})
	}
}
