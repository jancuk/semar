package cli_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/sampleapp"
	"github.com/jancuk/semar/internal/userconfig"
)

// A plan with an API kind: the model lists the files, reads one, and submits a draft, and the
// key comes from an environment variable through the resolver.
func TestPlanThroughTheOpenAIKind(t *testing.T) {
	const key = "test-key-0123"
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+key {
			t.Errorf("%s %s with %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &req)
		if req.Model != "acme-chat" {
			t.Errorf("model %q", req.Model)
		}
		call := func(id, name, args string) string {
			return fmt.Sprintf(`{"model":"acme-chat-0301","choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,`+
				`"tool_calls":[{"id":%q,"type":"function","function":{"name":%q,"arguments":%s}}]}}],`, id, name, args)
		}
		n := calls.Add(1)
		var out string
		switch n {
		case 1:
			out = call("c1", "list_files", `"{\"glob\":\"internal/**\"}"`) + `"usage":{"prompt_tokens":100,"completion_tokens":10}}`
		case 2:
			out = call("c2", "read_file", `"{\"path\":\"internal/refund/handler.go\"}"`) + `"usage":{"prompt_tokens":200,"completion_tokens":20}}`
		default:
			out = call("c3", "submit", goodDraft) + `"usage":{"prompt_tokens":300,"completion_tokens":30}}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, out)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ACME_KEY", key)

	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, goodDraft)
	p.yourFile(t, userconfig.File{Version: 1, Default: "acme", Providers: []userconfig.Provider{
		{ID: "acme", Kind: "openai", Model: "acme-chat", BaseURL: srv.URL + "/v1", APIKey: "env:ACME_KEY"},
	}})
	res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release")
	contains(t, "stderr", res.stderr, "provider acme (openai) plans release with acme-chat, and sends the files it reads to "+srv.URL+"/v1;")
	contains(t, "stdout", res.stdout, "SHOP-482 (behaviour): 1 scenario")

	for _, f := range []string{"SHOP-482/SHOP-482-01.yaml", "SHOP-483/SHOP-483-01.yaml"} {
		if !exists(t, p.path(".semar", "scenarios", filepath.FromSlash(f))) {
			t.Errorf("scenario file %s was not written", f)
		}
	}
	md, err := os.ReadFile(p.path(".semar", "plans", p.head(t)[:12], "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "plan.md", string(md),
		"Provider: acme (openai)", "Model: acme-chat-0301", "Tokens: 600 input, 60 output",
		"the planner had the three file tools only", "Tool calls: list_files 1, read_file 1, submit 1", "Refused tool calls: 0")

	recs := p.records(t)
	if len(recs) != 1 {
		t.Fatalf("%d usage records, want 1", len(recs))
	}
	r := recs[0]
	if r.Provider != "acme" || r.Kind != "openai" || r.Model != "acme-chat-0301" || r.Outcome != "ok" || r.Command != "plan" ||
		r.Ref != p.head(t)[:12] || r.Attempts != 1 || r.Turns != 3 || r.InputTokens != 600 || r.OutputTokens != 60 || r.At.IsZero() || r.At.After(time.Now().Add(time.Hour)) {
		t.Errorf("record = %+v", r)
	}
	raw, err := os.ReadFile(p.opts.Home + "/usage.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), key) || strings.Contains(string(md), key) {
		t.Error("the key is in the usage log or in plan.md")
	}
}
