package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/agentloop"
)

// The request uses only the fields that servers of this format share: no strict schemas, no
// tool_choice (some reasoning modes refuse a forced choice), and no output limit, whose field
// name differs between servers, so each server's default applies.
type request struct {
	Model    string `json:"model"`
	Messages []any  `json:"messages"`
	Tools    []tool `json:"tools"`
}

type tool struct {
	Type     string   `json:"type"`
	Function function `json:"function"`
}

type function struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type textMessage struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// assistantMessage is how an assistant message is sent back. Content is kept as the server
// wrote it, and reasoning_content is returned when the server sent one: DeepSeek, for one,
// refuses a conversation with tools in which it is missing.
type assistantMessage struct {
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content"`
	ToolCalls        []wireCall      `json:"tool_calls,omitempty"`
	ReasoningContent json.RawMessage `json:"reasoning_content,omitempty"`
}

type wireCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func buildRequest(model string, req agentloop.Request) request {
	out := request{Model: model, Messages: []any{}}
	if req.System != "" {
		out.Messages = append(out.Messages, textMessage{Role: "system", Content: req.System})
	}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, tool{Type: "function", Function: function{Name: t.Name, Description: t.Description, Parameters: t.Schema}})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case agentloop.RoleUser:
			out.Messages = append(out.Messages, textMessage{Role: "user", Content: m.Text})
		case agentloop.RoleAssistant:
			if a, ok := assistant(m); ok {
				out.Messages = append(out.Messages, a)
			}
		case agentloop.RoleTool:
			for _, r := range m.Results {
				out.Messages = append(out.Messages, textMessage{Role: "tool", Content: r.Text, ToolCallID: r.CallID})
			}
		}
	}
	return out
}

func assistant(m agentloop.Message) (any, bool) {
	if native := bytes.TrimSpace(m.Native); len(native) > 0 && string(native) != "null" {
		return json.RawMessage(native), true
	}
	if m.Text == "" && len(m.Calls) == 0 {
		return nil, false
	}
	a := assistantMessage{Role: "assistant", Content: json.RawMessage("null")}
	if m.Text != "" {
		a.Content, _ = json.Marshal(m.Text)
	}
	for _, c := range m.Calls {
		w := wireCall{ID: c.ID, Type: "function"}
		w.Function.Name, w.Function.Arguments = c.Name, string(c.Args)
		a.ToolCalls = append(a.ToolCalls, w)
	}
	return a, true
}

type response struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content          json.RawMessage `json:"content"`
			ReasoningContent json.RawMessage `json:"reasoning_content"`
			ToolCalls        []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens         int `json:"prompt_tokens"`
		CompletionTokens     int `json:"completion_tokens"`
		PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
		PromptTokensDetails  struct {
			CachedTokens     int `json:"cached_tokens"`
			CacheWriteTokens int `json:"cache_write_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error json.RawMessage `json:"error"`
}

// parseResponse reads the first choice. turn makes up call IDs for a server that sends none.
func parseResponse(data []byte, turn int) (agentloop.Response, error) {
	var resp response
	if err := json.Unmarshal(data, &resp); err != nil {
		return agentloop.Response{}, &provider.Error{Class: provider.ProviderError, Detail: "the API answered with a body that is not a chat completion"}
	}
	if len(resp.Choices) == 0 {
		detail := "the API answered with no choice"
		if len(resp.Error) > 0 && string(resp.Error) != "null" {
			detail = "the API answered with an error: " + provider.Truncate(provider.Redact(errorText(resp.Error)))
		}
		return agentloop.Response{}, &provider.Error{Class: provider.ProviderError, Detail: detail}
	}
	choice := resp.Choices[0]
	text, ok := contentText(choice.Message.Content)
	if !ok {
		return agentloop.Response{}, &provider.Error{Class: provider.ProviderError, Detail: "the API answered with message content that is neither text nor a list of parts"}
	}
	msg := agentloop.Message{Role: agentloop.RoleAssistant, Text: text}
	native := assistantMessage{Role: "assistant", Content: choice.Message.Content}
	if len(bytes.TrimSpace(native.Content)) == 0 {
		native.Content = json.RawMessage("null")
	}
	if r := bytes.TrimSpace(choice.Message.ReasoningContent); len(r) > 0 && string(r) != "null" {
		native.ReasoningContent = bytes.Clone(r)
	}
	for i, c := range choice.Message.ToolCalls {
		id := c.ID
		if id == "" {
			id = fmt.Sprintf("call_%d_%d", turn, i)
		}
		args, wire := arguments(c.Function.Arguments)
		msg.Calls = append(msg.Calls, agentloop.ToolCall{ID: id, Name: c.Function.Name, Args: args})
		w := wireCall{ID: id, Type: "function"}
		w.Function.Name, w.Function.Arguments = c.Function.Name, wire
		native.ToolCalls = append(native.ToolCalls, w)
	}
	if text == "" && len(msg.Calls) == 0 && native.ReasoningContent == nil {
		msg.Native = nil
	} else {
		if len(msg.Calls) == 0 && string(native.Content) == "null" {
			native.Content = json.RawMessage(`""`)
		}
		msg.Native, _ = json.Marshal(native)
	}
	u := resp.Usage
	cacheRead := u.PromptTokensDetails.CachedTokens
	if cacheRead == 0 {
		cacheRead = u.PromptCacheHitTokens
	}
	return agentloop.Response{
		Message: msg,
		Stop:    stop(choice.FinishReason),
		Model:   resp.Model,
		Usage: agentloop.Usage{
			Input:      u.PromptTokens,
			Output:     u.CompletionTokens,
			CacheRead:  cacheRead,
			CacheWrite: u.PromptTokensDetails.CacheWriteTokens,
		},
	}, nil
}

// arguments returns the arguments as JSON and as the text to send back. The format sends them
// as a string of JSON, but some servers send the object itself, and some encode the string twice.
func arguments(raw json.RawMessage) (json.RawMessage, string) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage("{}"), "{}"
	}
	if raw[0] != '"' {
		return bytes.Clone(raw), string(raw)
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return bytes.Clone(raw), string(raw)
	}
	inner := strings.TrimSpace(s)
	if inner == "" {
		return json.RawMessage("{}"), s
	}
	if inner[0] == '"' {
		var twice string
		if json.Unmarshal([]byte(inner), &twice) == nil {
			inner = strings.TrimSpace(twice)
		}
	}
	return json.RawMessage(inner), s
}

func contentText(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return "", true
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, true
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return "", false
	}
	var texts []string
	for _, p := range parts {
		if p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n"), true
}

func errorText(raw json.RawMessage) string {
	var obj struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Message != "" {
		return obj.Message
	}
	return string(raw)
}

func stop(reason string) agentloop.Stop {
	switch reason {
	case "length":
		return agentloop.StopMaxTokens
	case "content_filter":
		return agentloop.StopRefusal
	}
	return agentloop.StopEnd
}
