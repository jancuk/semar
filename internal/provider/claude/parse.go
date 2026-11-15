package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/provider"
)

// maxEventLine bounds one event of the stream. A longer line is skipped, so that one huge
// tool result cannot cost the whole job, and a job whose result was skipped fails.
const maxEventLine = 8 << 20

const answerTool = "StructuredOutput"

type envelope struct {
	IsError           bool              `json:"is_error"`
	Subtype           string            `json:"subtype"`
	StructuredOutput  json.RawMessage   `json:"structured_output"`
	NumTurns          int               `json:"num_turns"`
	APIErrorStatus    json.RawMessage   `json:"api_error_status"`
	TotalCostUSD      float64           `json:"total_cost_usd"`
	PermissionDenials []json.RawMessage `json:"permission_denials"`
	Usage             struct {
		InputTokens              int `json:"input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		OutputTokens             int `json:"output_tokens"`
	} `json:"usage"`
	ModelUsage map[string]struct {
		OutputTokens int `json:"outputTokens"`
	} `json:"modelUsage"`
}

func notJSON(exitCode int, stderrTail []byte) *provider.Error {
	return &provider.Error{
		Class:  provider.ProviderError,
		Detail: fmt.Sprintf("the harness output is not JSON, exit code %d: %s", exitCode, provider.LastLine(stderrTail)),
	}
}

func fromEnvelope(env envelope) (provider.Result, error) {
	if env.IsError {
		return provider.Result{}, classify(env)
	}
	out := bytes.TrimSpace(env.StructuredOutput)
	var obj map[string]json.RawMessage
	if len(out) == 0 || out[0] != '{' || json.Unmarshal(out, &obj) != nil || obj == nil {
		return provider.Result{}, &provider.Error{Class: provider.ValidationError, Detail: "the output holds no object"}
	}
	return provider.Result{
		Output:           json.RawMessage(out),
		Model:            mainModel(env),
		Turns:            env.NumTurns,
		InputTokens:      env.Usage.InputTokens + env.Usage.CacheCreationInputTokens + env.Usage.CacheReadInputTokens,
		OutputTokens:     env.Usage.OutputTokens,
		CacheReadTokens:  env.Usage.CacheReadInputTokens,
		CacheWriteTokens: env.Usage.CacheCreationInputTokens,
		CostUSD:          env.TotalCostUSD,
		Denied:           len(env.PermissionDenials),
	}, nil
}

type initEvent struct {
	MCPServers []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"mcp_servers"`
	Skills         []json.RawMessage `json:"skills"`
	Tools          []string          `json:"tools"`
	PermissionMode string            `json:"permissionMode"`
}

type assistantEvent struct {
	Message struct {
		ID      string `json:"id"`
		Content []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"content"`
	} `json:"message"`
}

type rateLimitEvent struct {
	Info struct {
		Windows map[string]struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    int64   `json:"resetsAt"` // Unix seconds
			Status      string  `json:"status"`
		} `json:"unifiedWindows"`
	} `json:"rate_limit_info"`
}

// tally counts the responses and the tool calls of a stream. The reports while the job runs
// and the final parse count with it, so that the last report agrees with Result.Tools.
type tally struct {
	messages map[string]bool
	seen     map[string]bool
	calls    map[string]int
	now      provider.Progress
}

func newTally() *tally {
	return &tally{messages: map[string]bool{}, seen: map[string]bool{}, calls: map[string]int{}}
}

// add counts one assistant event and says whether the totals changed. One message can come in
// several events, so the message ID counts each response once and the call ID each call once.
// The harness returns the answer through a tool of its own, which is no call of the job.
func (t *tally) add(ev assistantEvent) bool {
	before := t.now
	if id := ev.Message.ID; id == "" || !t.messages[id] {
		t.messages[id] = true
		t.now.Turns++
	}
	for _, c := range ev.Message.Content {
		if c.Type != "tool_use" || c.Name == "" || c.Name == answerTool || (c.ID != "" && t.seen[c.ID]) {
			continue
		}
		t.seen[c.ID] = true
		t.calls[c.Name]++
		t.now.ToolCalls++
		t.now.Tool = provider.ProgressTool(c.Name)
	}
	return t.now != before
}

// addLine counts one line of a running stream and says whether the totals changed.
func (t *tally) addLine(line []byte) bool {
	var head struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(line, &head) != nil || head.Type != "assistant" {
		return false
	}
	var ev assistantEvent
	if json.Unmarshal(line, &ev) != nil {
		return false
	}
	return t.add(ev)
}

// parseStream reads the events of --output-format stream-json, one JSON object per line.
// An event of an unknown type, and a line that is not JSON, is skipped.
func parseStream(stdout []byte, exitCode int, stderrTail []byte) (provider.Result, error) {
	var (
		result  *envelope
		start   *initEvent
		anyJSON bool
		tooLong bool
		counted = newTally()
		limits  = map[string]provider.Limit{}
	)
	for rest := stdout; len(rest) > 0; {
		var line []byte
		line, rest, _ = bytes.Cut(rest, []byte("\n"))
		if len(line) > maxEventLine {
			tooLong = true
			continue
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var head struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
		}
		if json.Unmarshal(line, &head) != nil {
			continue
		}
		anyJSON = true
		switch {
		case head.Type == "system" && head.Subtype == "init":
			var ev initEvent
			if json.Unmarshal(line, &ev) == nil {
				start = &ev
			}
		case head.Type == "assistant":
			var ev assistantEvent
			if json.Unmarshal(line, &ev) == nil {
				counted.add(ev)
			}
		case head.Type == "rate_limit_event":
			var ev rateLimitEvent
			if json.Unmarshal(line, &ev) != nil {
				continue
			}
			for name, w := range ev.Info.Windows {
				l := provider.Limit{Window: name, Utilization: w.Utilization, Status: w.Status}
				if w.ResetsAt > 0 {
					l.ResetsAt = time.Unix(w.ResetsAt, 0).UTC()
				}
				limits[name] = l
			}
		case head.Type == "result":
			var ev envelope
			if json.Unmarshal(line, &ev) == nil {
				result = &ev
			}
		}
	}
	if result == nil {
		if !anyJSON && !tooLong {
			return provider.Result{}, notJSON(exitCode, stderrTail)
		}
		detail := fmt.Sprintf("the harness output holds no result event, exit code %d: %s", exitCode, provider.LastLine(stderrTail))
		if tooLong {
			detail = fmt.Sprintf("the harness output holds no result event that Semar could read, and a line was longer than %d MiB, exit code %d", maxEventLine>>20, exitCode)
		}
		return provider.Result{}, &provider.Error{Class: provider.ProviderError, Detail: detail}
	}
	res, err := fromEnvelope(*result)
	if err != nil {
		return res, err
	}
	for name, n := range counted.calls {
		res.Tools = append(res.Tools, provider.ToolUse{Name: name, Calls: n})
	}
	slices.SortFunc(res.Tools, func(a, b provider.ToolUse) int { return strings.Compare(a.Name, b.Name) })
	for _, l := range limits {
		res.Limits = append(res.Limits, l)
	}
	slices.SortFunc(res.Limits, func(a, b provider.Limit) int { return strings.Compare(a.Window, b.Window) })
	if start != nil {
		res.Loaded = loaded(*start)
	}
	return res, nil
}

// loaded names what the job could use: the MCP servers that connected or were still
// connecting, and the skills only when the Skill tool was there to call them.
func loaded(ev initEvent) provider.Loaded {
	var l provider.Loaded
	for _, s := range ev.MCPServers {
		if s.Name != "" && (s.Status == "connected" || s.Status == "pending") {
			l.MCPServers = append(l.MCPServers, s.Name)
		}
	}
	slices.Sort(l.MCPServers)
	l.MCPServers = slices.Compact(l.MCPServers)
	if slices.Contains(ev.Tools, "Skill") {
		l.Skills = len(ev.Skills)
	}
	l.PermissionMode = ev.PermissionMode
	return l
}

func classify(env envelope) *provider.Error {
	subtype := provider.Truncate(provider.Redact(env.Subtype))
	lower := strings.ToLower(env.Subtype)
	status := string(bytes.TrimSpace(env.APIErrorStatus))
	switch {
	case env.Subtype == "error_max_turns":
		return &provider.Error{Class: provider.ValidationError, Detail: "the harness reached the turn limit"}
	case status == "429" || strings.Contains(lower, "usage limit") || strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "usage_limit") || strings.Contains(lower, "rate_limit"):
		return &provider.Error{Class: provider.UsageLimit, Detail: "the harness reports a usage or rate limit: " + subtype}
	case status == "401" || status == "403":
		return &provider.Error{Class: provider.PermissionError, Detail: "the harness was refused with status " + status + ": " + subtype}
	}
	return &provider.Error{Class: provider.ProviderError, Detail: "the harness reported an error: " + subtype}
}

// mainModel is the model that wrote the most output. A tie goes to the first name in alphabetical order.
func mainModel(env envelope) string {
	best, bestTokens := "", -1
	for name, u := range env.ModelUsage {
		if u.OutputTokens > bestTokens || (u.OutputTokens == bestTokens && name < best) {
			best, bestTokens = name, u.OutputTokens
		}
	}
	return best
}
