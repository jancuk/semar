package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

type echoArgs struct {
	Text string    `json:"text"`
	N    int       `json:"n"`
	With StringMap `json:"with"`
}

// testTools is a small set of tools: a read-only echo, a blocking one, one that panics, one that
// fails, and one that reports progress.
func testTools() []Tool {
	return []Tool{
		{
			Name: "echo", Title: "Echo", Description: "Return the text.", Surface: "mobile",
			Input: Object(map[string]Schema{
				"text": String("What to echo."),
				"n":    Int("A count.").With("minimum", 0),
				"mode": Enum("How.", "plain", "loud"),
				"with": NameValue("Pairs."),
			}).Required("text"),
			Annotations: Annotations{ReadOnly: BoolPtr(true), OpenWorld: BoolPtr(false)},
			Handler: func(ctx context.Context, c Call) (Result, error) {
				var a struct {
					echoArgs
					Mode string `json:"mode"`
				}
				if err := DecodeArgs(c.Args, &a); err != nil {
					return ErrorResult("Bad arguments: " + err.Error()), nil
				}
				return TextResult(fmt.Sprintf("%s|%d|%d|%s|%s", a.Text, a.N, len(a.With), c.Client.Name, c.Session)), nil
			},
		},
		{
			Name: "block", Description: "Wait until cancelled.", Surface: "control", Write: true,
			Handler: func(ctx context.Context, c Call) (Result, error) {
				<-ctx.Done()
				return Result{}, ctx.Err()
			},
		},
		{
			Name: "boom", Description: "Panic.",
			Handler: func(ctx context.Context, c Call) (Result, error) { panic("secret stack detail") },
		},
		{
			Name: "fail", Description: "Fail.",
			Handler: func(ctx context.Context, c Call) (Result, error) { return Result{}, fmt.Errorf("the device is gone") },
		},
		{
			Name: "work", Description: "Report progress.",
			Handler: func(ctx context.Context, c Call) (Result, error) {
				c.Progress(1, 3, "one")
				c.Progress(2, 3, "")
				return TextResult("done"), nil
			},
		},
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := NewServer(Info{Name: "semar", Version: "test", Instructions: "Use the tools."}, testTools(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// rpc sends one message and decodes the single answer into a generic map.
func rpc(t *testing.T, s *Server, sess *Session, msg string) map[string]any {
	t.Helper()
	out := s.Handle(context.Background(), sess, []byte(msg))
	if out == nil {
		t.Fatalf("no answer to %s", msg)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("answer %s is not an object: %v", out, err)
	}
	return m
}

func errCode(t *testing.T, m map[string]any) int {
	t.Helper()
	e, ok := m["error"].(map[string]any)
	if !ok {
		t.Fatalf("no error in %v", m)
	}
	return int(e["code"].(float64))
}

func resultOf(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	r, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %v", m)
	}
	return r
}

func textOf(t *testing.T, m map[string]any) (string, bool) {
	t.Helper()
	r := resultOf(t, m)
	c := r["content"].([]any)
	isErr, _ := r["isError"].(bool)
	return c[0].(map[string]any)["text"].(string), isErr
}
