package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jancuk/semar/internal/provider"
)

const maxDetailRunes = 300

type envelope struct {
	IsError          bool            `json:"is_error"`
	Subtype          string          `json:"subtype"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	NumTurns         int             `json:"num_turns"`
	APIErrorStatus   json.RawMessage `json:"api_error_status"`
	Usage            struct {
		InputTokens              int `json:"input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		OutputTokens             int `json:"output_tokens"`
	} `json:"usage"`
	ModelUsage map[string]struct {
		OutputTokens int `json:"outputTokens"`
	} `json:"modelUsage"`
}

func parseOutput(stdout []byte, exitCode int, stderrTail []byte) (provider.Result, error) {
	var env envelope
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &env); err != nil {
		return provider.Result{}, &provider.Error{
			Class:  provider.ProviderError,
			Detail: fmt.Sprintf("the harness output is not JSON, exit code %d: %s", exitCode, provider.LastLine(stderrTail)),
		}
	}
	if env.IsError {
		return provider.Result{}, classify(env)
	}
	out := bytes.TrimSpace(env.StructuredOutput)
	var obj map[string]json.RawMessage
	if len(out) == 0 || out[0] != '{' || json.Unmarshal(out, &obj) != nil || obj == nil {
		return provider.Result{}, &provider.Error{Class: provider.ValidationError, Detail: "the output holds no object"}
	}
	return provider.Result{
		Output:       json.RawMessage(out),
		Model:        mainModel(env),
		Turns:        env.NumTurns,
		InputTokens:  env.Usage.InputTokens + env.Usage.CacheCreationInputTokens + env.Usage.CacheReadInputTokens,
		OutputTokens: env.Usage.OutputTokens,
	}, nil
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
