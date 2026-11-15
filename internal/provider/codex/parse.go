package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/provider"
)

// maxEventLine bounds one event. A longer line is skipped, so an answer that long is no answer.
const maxEventLine = 4 << 20

type event struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Item    *struct {
		ID     string `json:"id"`
		Type   string `json:"type"`
		Text   string `json:"text"`
		Status string `json:"status"`
		Server string `json:"server"`
		Tool   string `json:"tool"`
	} `json:"item"`
	Usage *struct {
		InputTokens       int `json:"input_tokens"`
		CachedInputTokens int `json:"cached_input_tokens"`
		CacheWriteTokens  int `json:"cache_write_input_tokens"`
		OutputTokens      int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// parseEvents reads the JSON lines of `codex exec --json`. The answer is the text of the last
// agent message, the usage is on each turn.completed, and a failure is a turn.failed or an
// error event. An item of the type "error" is a warning and does not end the job.
func parseEvents(stdout []byte, exitCode int, stderrTail []byte) (provider.Result, error) {
	var (
		res       provider.Result
		events    int
		answer    string
		failed    string
		lastError string
		tools     = map[string]string{}
		denied    = map[string]bool{}
	)
	for line := range bytes.SplitSeq(stdout, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || len(line) > maxEventLine {
			continue
		}
		var ev event
		if json.Unmarshal(line, &ev) != nil || ev.Type == "" {
			continue
		}
		events++
		switch ev.Type {
		case "item.started", "item.updated", "item.completed":
			if ev.Item == nil {
				continue
			}
			if name := toolName(ev.Item.Type, ev.Item.Server, ev.Item.Tool); name != "" {
				tools[ev.Item.ID] = name
				if ev.Item.Status == "declined" {
					denied[ev.Item.ID] = true
				}
			}
			if ev.Type == "item.completed" && ev.Item.Type == "agent_message" {
				answer = ev.Item.Text
			}
		case "turn.completed":
			res.Turns++
			if u := ev.Usage; u != nil {
				res.InputTokens += u.InputTokens
				res.CacheReadTokens += u.CachedInputTokens
				res.CacheWriteTokens += u.CacheWriteTokens
				res.OutputTokens += u.OutputTokens
			}
		case "turn.failed":
			failed = "the turn failed"
			if ev.Error != nil && ev.Error.Message != "" {
				failed = ev.Error.Message
			}
		case "error":
			lastError = ev.Message
		}
	}

	switch {
	case events == 0:
		return provider.Result{}, &provider.Error{
			Class:  provider.ProviderError,
			Detail: fmt.Sprintf("the harness output is not JSON, exit code %d: %s", exitCode, provider.LastLine(stderrTail)),
		}
	case failed != "":
		return provider.Result{}, classify(failed)
	case res.Turns == 0 && lastError != "":
		return provider.Result{}, classify(lastError)
	case res.Turns == 0:
		return provider.Result{}, &provider.Error{
			Class:  provider.ProviderError,
			Detail: fmt.Sprintf("the harness ended before its turn finished, exit code %d: %s", exitCode, provider.LastLine(stderrTail)),
		}
	}
	out, ok := answerObject(answer)
	if !ok {
		return provider.Result{}, &provider.Error{Class: provider.ValidationError, Detail: "the answer is not one JSON object"}
	}
	res.Output = out
	res.Denied = len(denied)
	counts := map[string]int{}
	for _, name := range tools {
		counts[name]++
	}
	for _, name := range slices.Sorted(maps.Keys(counts)) {
		res.Tools = append(res.Tools, provider.ToolUse{Name: name, Calls: counts[name]})
	}
	return res, nil
}

// toolName is the name under which a call is counted. Every shell command counts as "shell",
// and an MCP tool keeps the name that the Claude driver reports for it.
func toolName(itemType, server, tool string) string {
	switch itemType {
	case "command_execution":
		return "shell"
	case "mcp_tool_call":
		if server == "" || tool == "" {
			return "mcp"
		}
		return "mcp__" + server + "__" + tool
	case "web_search":
		return "web_search"
	case "file_change":
		return "apply_patch"
	}
	return ""
}

// answerObject accepts one JSON object, alone or in one code fence, which a model sometimes
// adds when the schema is in the prompt.
func answerObject(text string) (json.RawMessage, bool) {
	s := strings.TrimSpace(text)
	if rest, ok := strings.CutPrefix(s, "```"); ok {
		if _, body, found := strings.Cut(rest, "\n"); found {
			if inner, ok := strings.CutSuffix(strings.TrimSpace(body), "```"); ok {
				s = strings.TrimSpace(inner)
			}
		}
	}
	if !strings.HasPrefix(s, "{") {
		return nil, false
	}
	dec := json.NewDecoder(strings.NewReader(s))
	var obj map[string]json.RawMessage
	if dec.Decode(&obj) != nil || obj == nil {
		return nil, false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, false
	}
	return json.RawMessage(s), true
}

// classify maps the message of a failed turn. The message is often the API's own error object
// as a string, with a status and a code.
func classify(msg string) *provider.Error {
	text, code, status := msg, "", 0
	var api struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
		Status int `json:"status"`
	}
	if json.Unmarshal([]byte(msg), &api) == nil {
		if api.Error.Message != "" {
			text = api.Error.Message
		}
		code, status = api.Error.Code, api.Status
	}
	detail := provider.Truncate(provider.Redact(text))
	lower := strings.ToLower(msg)
	has := func(words ...string) bool {
		return slices.ContainsFunc(words, func(w string) bool { return strings.Contains(lower, w) })
	}
	switch {
	case status == 429 || has("usage limit", "usage_limit", "rate limit", "rate_limit", "too many requests", "quota"):
		return &provider.Error{Class: provider.UsageLimit, Detail: "the harness reports a usage or rate limit: " + detail}
	case status == 401 || status == 403 || has("not logged in", "not signed in", "unauthorized", "codex login", "authentication"):
		return &provider.Error{Class: provider.PermissionError, Detail: "the harness is not signed in or was refused: " + detail}
	case code == "invalid_json_schema" || has("invalid schema"):
		return &provider.Error{Class: provider.ValidationError, Detail: "the harness refused the output schema: " + detail}
	}
	return &provider.Error{Class: provider.ProviderError, Detail: "the harness reported an error: " + detail}
}
