package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

func FuzzDecode(f *testing.F) {
	for _, seed := range []string{
		``, `{}`, `[]`, `[{}]`, `null`, `42`, `"x"`,
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":null,"method":"x","params":[1]}`,
		`{"jsonrpc":"2.0","id":{"a":1},"method":"x"}`,
		`{"jsonrpc":"2.0","id":1,"result":{}}`,
		`[{"jsonrpc":"2.0","id":1,"method":"ping"},5,[],null]`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"with":[{"name":"a","value":"b"}]}}}`,
		"{\"jsonrpc\":\"2.0\",\"id\":\"\xff\",\"method\":\"ping\"}",
	} {
		f.Add([]byte(seed))
	}
	s, err := NewServer(Info{Name: "fuzz"}, testTools()[:1], nil)
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		msgs, batch, derr := Decode(data)
		if derr != nil {
			if msgs != nil || derr.Code == 0 || derr.Message == "" {
				t.Fatalf("bad error result: %v %v", msgs, derr)
			}
		} else if len(msgs) == 0 || (!batch && len(msgs) != 1) {
			t.Fatalf("batch=%v msgs=%d", batch, len(msgs))
		}
		// Whatever the input, Handle answers with valid JSON or with nothing, and never panics.
		out := s.Handle(context.Background(), nil, data)
		if out != nil && !json.Valid(out) {
			t.Fatalf("invalid answer %q for %q", out, data)
		}
		// The arguments decoder takes any bytes too.
		var a echoArgs
		_ = DecodeArgs(data, &a)
		var m StringMap
		_ = json.Unmarshal(data, &m)
	})
}
