package anthropic

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/agentloop"
)

type cacheControl struct {
	Type string `json:"type"`
}

var ephemeral = &cacheControl{Type: "ephemeral"}

type textBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type toolUseBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type toolResultBlock struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

type message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type request struct {
	Model        string        `json:"model"`
	MaxTokens    int           `json:"max_tokens"`
	System       []textBlock   `json:"system,omitempty"`
	Messages     []message     `json:"messages"`
	Tools        []tool        `json:"tools"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
	OutputConfig *outputConfig `json:"output_config,omitempty"`
}

type outputConfig struct {
	Effort string `json:"effort"`
}

// buildRequest maps the neutral conversation. The tools, the system text, and the prompt never
// change during a job, so a cache breakpoint on the system text and one on the prompt let
// every later turn read them from the cache. The conversation is only ever appended to, as
// the API requires for the thinking blocks that assistant messages may carry.
func buildRequest(model string, req agentloop.Request, autoCache bool) request {
	out := request{Model: model, MaxTokens: maxTokens}
	if req.System != "" {
		out.System = []textBlock{{Type: "text", Text: req.System, CacheControl: ephemeral}}
	}
	if autoCache {
		out.CacheControl = ephemeral
	}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, tool{Name: t.Name, Description: t.Description, InputSchema: t.Schema})
	}
	for i, m := range req.Messages {
		switch m.Role {
		case agentloop.RoleUser:
			block := textBlock{Type: "text", Text: m.Text}
			if i == 0 {
				block.CacheControl = ephemeral
			}
			out.Messages = append(out.Messages, message{Role: "user", Content: []textBlock{block}})
		case agentloop.RoleAssistant:
			content := assistantContent(m)
			if content == nil {
				// An empty assistant message is refused; the next user message joins the one before.
				continue
			}
			out.Messages = append(out.Messages, message{Role: "assistant", Content: content})
		case agentloop.RoleTool:
			blocks := make([]toolResultBlock, 0, len(m.Results))
			for _, r := range m.Results {
				blocks = append(blocks, toolResultBlock{Type: "tool_result", ToolUseID: r.CallID, Content: r.Text, IsError: r.IsError})
			}
			out.Messages = append(out.Messages, message{Role: "user", Content: blocks})
		}
	}
	return out
}

func assistantContent(m agentloop.Message) any {
	if native := bytes.TrimSpace(m.Native); len(native) > 0 && string(native) != "[]" && string(native) != "null" {
		return json.RawMessage(native)
	}
	var blocks []any
	if m.Text != "" {
		blocks = append(blocks, textBlock{Type: "text", Text: m.Text})
	}
	for _, c := range m.Calls {
		blocks = append(blocks, toolUseBlock{Type: "tool_use", ID: c.ID, Name: c.Name, Input: c.Args})
	}
	if len(blocks) == 0 {
		return nil
	}
	return blocks
}

type response struct {
	Type       string            `json:"type"`
	Model      string            `json:"model"`
	Content    []json.RawMessage `json:"content"`
	StopReason string            `json:"stop_reason"`
	Usage      struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

func parseResponse(data []byte) (agentloop.Response, error) {
	var raw struct {
		Content json.RawMessage `json:"content"`
	}
	var resp response
	if json.Unmarshal(data, &resp) != nil || json.Unmarshal(data, &raw) != nil || resp.Content == nil {
		return agentloop.Response{}, &provider.Error{Class: provider.ProviderError, Detail: "the API answered with a body that is not a message"}
	}
	msg := agentloop.Message{Role: agentloop.RoleAssistant, Native: bytes.Clone(raw.Content)}
	var text []string
	for _, b := range resp.Content {
		var block struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		if json.Unmarshal(b, &block) != nil {
			return agentloop.Response{}, &provider.Error{Class: provider.ProviderError, Detail: "the API answered with a content block that is not an object"}
		}
		switch block.Type {
		case "text":
			text = append(text, block.Text)
		case "tool_use":
			msg.Calls = append(msg.Calls, agentloop.ToolCall{ID: block.ID, Name: block.Name, Args: block.Input})
		}
	}
	msg.Text = strings.Join(text, "\n")
	u := resp.Usage
	return agentloop.Response{
		Message: msg,
		Stop:    stop(resp.StopReason),
		Model:   resp.Model,
		Usage: agentloop.Usage{
			Input:      u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens,
			Output:     u.OutputTokens,
			CacheRead:  u.CacheReadInputTokens,
			CacheWrite: u.CacheCreationInputTokens,
		},
	}, nil
}

func stop(reason string) agentloop.Stop {
	switch reason {
	case "max_tokens":
		return agentloop.StopMaxTokens
	case "refusal":
		return agentloop.StopRefusal
	case "model_context_window_exceeded":
		return agentloop.StopContext
	}
	return agentloop.StopEnd
}
