package provider_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/provider"
)

func TestChildEnv(t *testing.T) {
	parent := map[string]string{
		"PATH": "/bin", "HOME": "/h", "USER": "u", "LOGNAME": "u", "LANG": "C", "TERM": "xterm", "TMPDIR": "/t",
		"ANTHROPIC_API_KEY": "sk-secret", "CLAUDECODE": "1", "SOMETHING_ELSE": "x",
	}
	lookup := func(k string) (string, bool) { v, ok := parent[k]; return v, ok }
	got := provider.ChildEnv(lookup)
	want := []string{"PATH=/bin", "HOME=/h", "USER=u", "LOGNAME=u", "LANG=C", "TERM=xterm", "TMPDIR=/t"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
	for _, kv := range got {
		for _, banned := range []string{"ANTHROPIC_API_KEY", "CLAUDECODE", "SOMETHING_ELSE"} {
			if strings.HasPrefix(kv, banned+"=") {
				t.Fatalf("%s was passed", banned)
			}
		}
	}
	delete(parent, "LANG")
	if got := provider.ChildEnv(lookup); len(got) != 6 {
		t.Fatalf("an unset variable must be skipped, got %v", got)
	}
}

func TestSetupEnv(t *testing.T) {
	environ := []string{
		"PATH=/bin", "HOME=/h", "CLAUDE_CONFIG_DIR=/h/.claude", "EDITOR=vi", "ORDERS_DB_URL=postgres://db.example.com",
		"ANTHROPIC_API_KEY=sk-1", "ANTHROPIC_BASE_URL=https://proxy.example.com", "OPENAI_API_KEY=sk-2",
		"OPENAI_BASE_URL=https://api.example.com", "AZURE_OPENAI_API_KEY=k", "DEEPSEEK_API_KEY=k", "GEMINI_API_KEY=k",
		"GOOGLE_API_KEY=k", "GOOGLE_CLOUD_PROJECT=acme", "OPENROUTER_API_KEY=k", "anthropic_api_key=lower",
		"SEMAR_CLAUDE_BIN=/x", "SEMAR_HOME=/y",
		"CLAUDECODE=1", "CLAUDE_CODE_ENTRYPOINT=cli", "CLAUDE_CODE_SESSION_ID=s", "CLAUDE_CODE_OAUTH_TOKEN=t",
		"CLAUDE_AGENT_SDK_VERSION=1", "CLAUDE_PLUGIN_DATA=/p", "CLAUDE_PID=1", "CLAUDE_EFFORT=high", "AI_AGENT=x",
		"ACME_TOKEN=secret", "acme_password=secret",
		"AWS_BEARER_TOKEN_BEDROCK=k", "AWS_REGION=eu-west-1", "HF_TOKEN=k", "HF_HOME=/h/.hf", "HUGGINGFACE_HUB_TOKEN=k",
		"HUGGING_FACE_HUB_TOKEN=k", "TOGETHER_API_KEY=k", "COHERE_API_KEY=k", "PERPLEXITY_API_KEY=k", "FIREWORKS_API_KEY=k",
		"REPLICATE_API_TOKEN=k", "CEREBRAS_API_KEY=k", "CODEX_HOME=/h/.codex",
		"=C:=C:\\", "BROKEN",
	}
	got := provider.SetupEnv(environ, []string{"ACME_TOKEN", "ACME_PASSWORD"})
	want := []string{"PATH=/bin", "HOME=/h", "CLAUDE_CONFIG_DIR=/h/.claude", "EDITOR=vi", "ORDERS_DB_URL=postgres://db.example.com", "GOOGLE_CLOUD_PROJECT=acme", "AWS_REGION=eu-west-1", "HF_HOME=/h/.hf", "CODEX_HOME=/h/.codex"}
	if !slices.Equal(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	if got := provider.SetupEnv(nil, nil); len(got) != 0 {
		t.Fatalf("got %q", got)
	}
}
