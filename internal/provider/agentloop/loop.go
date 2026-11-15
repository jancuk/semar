// Package agentloop is Semar's own agent loop for model APIs. A model reads the working
// directory through three read-only tools that are confined to it by the operating system,
// and returns its result through a fourth tool, submit, whose parameters are the output
// schema. One adapter per API format implements Client.
package agentloop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/provider"
)

// Role is the author of a message in the neutral conversation.
type Role string

const (
	RoleUser      Role = "user"      // Text from Semar: the prompt or a reminder
	RoleAssistant Role = "assistant" // Text and Calls from the model
	RoleTool      Role = "tool"      // Results of the calls of the assistant message before it
)

// Message is one entry of the conversation. Both the Anthropic Messages API and the OpenAI
// chat format map onto it: a tool message carries every result of one assistant message,
// which the Anthropic adapter sends as one user message of tool_result blocks and the OpenAI
// adapter as one tool message per result.
type Message struct {
	Role    Role
	Text    string
	Calls   []ToolCall   // assistant only
	Results []ToolResult // tool only
	// Native is the adapter's own encoding of an assistant message, sent back unchanged on
	// every later request. It keeps what the neutral fields cannot hold, such as signed
	// thinking blocks or reasoning text, which some APIs require to be returned as received.
	Native json.RawMessage
}

type ToolCall struct {
	ID   string
	Name string
	Args json.RawMessage // one JSON value as the model wrote it, normally an object
}

type ToolResult struct {
	CallID  string
	Text    string
	IsError bool
}

// Tool is one tool definition offered to the model.
type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage // the JSON Schema of the arguments, an object
}

// Request is the conversation so far and the tools. The adapter adds the model name and the
// options of its API.
type Request struct {
	System   string
	Messages []Message
	Tools    []Tool
}

// Stop says why the model ended its response.
type Stop string

const (
	StopEnd       Stop = "end"        // a natural end or a stop to run tools
	StopMaxTokens Stop = "max_tokens" // the output limit of one response was reached
	StopRefusal   Stop = "refusal"    // the API or the model declined the request
	StopContext   Stop = "context"    // the conversation no longer fits the model's context
)

// Usage of one response. Input counts every input token, cached or not.
type Usage struct {
	Input, Output, CacheRead, CacheWrite int
}

type Response struct {
	Message Message // an assistant message
	Stop    Stop
	Usage   Usage
	Model   string
}

// Client sends one request and returns the model's next message. An error is a
// *provider.Error, or the context's error.
type Client interface {
	Next(ctx context.Context, req Request) (Response, error)
}

const (
	systemText = "You work in a read-only copy of a source tree. Use read_file, search, and list_files to study it; " +
		"every path is relative to the working directory. When you have the answer, call submit once, " +
		"with the complete result object as its arguments. A reply without a tool call is not read."
	reminderText = "Your reply had no tool call. Call submit now with the complete result object as its arguments, " +
		"or call another tool if you still need to read something."
)

const maxCallsPerResponse = 32

// maxJobResultBytes caps the tool results of one job, so the model cannot read without end
// within its turns. Tests lower it.
var maxJobResultBytes = 64 << 20

const budgetText = "error: the tool results of this job reached 64 MiB, so no more tools run; call submit with the result now"

// Run sends job.Prompt and runs the tools the model calls until it calls submit, until
// job.MaxTurns responses were used, or until job.Timeout passed. The output is the argument
// object of submit.
func Run(ctx context.Context, client Client, job provider.Job) (provider.Result, error) {
	if err := checkJob(job); err != nil {
		return provider.Result{}, err
	}
	ws, err := openWorkspace(job.Workdir)
	if err != nil {
		return provider.Result{}, &provider.Error{Class: provider.ValidationError, Detail: "the working directory cannot be opened"}
	}
	defer ws.close()

	runCtx := ctx
	if job.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, job.Timeout)
		defer cancel()
	}

	req := Request{
		System:   systemText,
		Messages: []Message{{Role: RoleUser, Text: job.Prompt}},
		Tools:    toolDefs(job.Schema),
	}
	var res provider.Result
	calls := map[string]int{}
	resultBytes := 0
	reminded := false
	finish := func() {
		res.Tools = toolUses(calls)
	}
	for res.Turns < job.MaxTurns {
		resp, err := client.Next(runCtx, req)
		if err != nil {
			finish()
			return res, loopError(ctx, runCtx, job, err)
		}
		res.Turns++
		res.InputTokens += resp.Usage.Input
		res.OutputTokens += resp.Usage.Output
		res.CacheReadTokens += resp.Usage.CacheRead
		res.CacheWriteTokens += resp.Usage.CacheWrite
		if resp.Model != "" {
			res.Model = resp.Model
		}
		msg := resp.Message
		msg.Role = RoleAssistant
		req.Messages = append(req.Messages, msg)

		for _, c := range msg.Calls {
			name := c.Name
			if !knownTool(name) {
				name = "unknown"
			}
			calls[name]++
		}
		if out, ok := submitted(msg.Calls); ok {
			finish()
			res.Output = out
			return res, nil
		}
		if len(msg.Calls) == 0 {
			switch resp.Stop {
			case StopRefusal:
				finish()
				return res, &provider.Error{Class: provider.ProviderError, Detail: "the model declined the request"}
			case StopContext:
				finish()
				return res, &provider.Error{Class: provider.ProviderError, Detail: "the conversation no longer fits the context window of the model"}
			}
			if reminded {
				finish()
				return res, &provider.Error{Class: provider.ValidationError, Detail: "the model twice replied without calling submit"}
			}
			reminded = true
			req.Messages = append(req.Messages, Message{Role: RoleUser, Text: reminderText})
			continue
		}
		results := make([]ToolResult, 0, len(msg.Calls))
		for i, c := range msg.Calls {
			var text string
			var isErr bool
			switch {
			case i >= maxCallsPerResponse:
				text, isErr = fmt.Sprintf("error: at most %d tool calls run per response; this call was not run", maxCallsPerResponse), true
			case resultBytes >= maxJobResultBytes:
				text, isErr = budgetText, true
			default:
				text, isErr = ws.call(runCtx, c)
				resultBytes += len(text)
				if resultBytes > maxJobResultBytes {
					text, isErr = budgetText, true
				}
			}
			results = append(results, ToolResult{CallID: c.ID, Text: text, IsError: isErr})
		}
		if err := runCtx.Err(); err != nil {
			finish()
			return res, loopError(ctx, runCtx, job, err)
		}
		req.Messages = append(req.Messages, Message{Role: RoleTool, Results: results})
	}
	finish()
	return res, &provider.Error{Class: provider.ValidationError, Detail: fmt.Sprintf("the model reached the turn limit of %d responses without calling submit", job.MaxTurns)}
}

// submitted returns the arguments of the first submit call when they are one JSON object.
// Other arguments get an error result from the workspace, so the model can try again.
func submitted(calls []ToolCall) (json.RawMessage, bool) {
	for _, c := range calls {
		if c.Name != toolSubmit {
			continue
		}
		if obj, ok := jsonObject(c.Args); ok {
			return obj, true
		}
	}
	return nil, false
}

func jsonObject(raw json.RawMessage) (json.RawMessage, bool) {
	raw = bytes.TrimSpace(raw)
	var obj map[string]json.RawMessage
	if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &obj) != nil || obj == nil {
		return nil, false
	}
	return slices.Clone(raw), true
}

func toolUses(calls map[string]int) []provider.ToolUse {
	out := make([]provider.ToolUse, 0, len(calls))
	for name, n := range calls {
		out = append(out, provider.ToolUse{Name: name, Calls: n})
	}
	slices.SortFunc(out, func(a, b provider.ToolUse) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func loopError(parent, runCtx context.Context, job provider.Job, err error) error {
	switch {
	case parent.Err() != nil:
		return fmt.Errorf("run agent loop: %w", parent.Err())
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		return &provider.Error{Class: provider.TransportError, Detail: "the model did not finish in " + job.Timeout.String()}
	}
	var perr *provider.Error
	if errors.As(err, &perr) {
		return perr
	}
	return &provider.Error{Class: provider.ProviderError, Detail: provider.Truncate(provider.Redact(err.Error()))}
}

func checkJob(job provider.Job) error {
	bad := func(format string, args ...any) error {
		return &provider.Error{Class: provider.ValidationError, Detail: fmt.Sprintf(format, args...)}
	}
	switch {
	case job.MaxTurns <= 0:
		return bad("the job needs a positive turn limit")
	case strings.TrimSpace(job.Prompt) == "":
		return bad("the job has no prompt")
	case job.Workdir == "":
		return bad("the job has no working directory")
	}
	if info, err := os.Stat(job.Workdir); err != nil || !info.IsDir() {
		return bad("the working directory %q is not a directory", job.Workdir)
	}
	if _, ok := jsonObject(job.Schema); !ok {
		return bad("the schema is not a JSON object")
	}
	return nil
}
