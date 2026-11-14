package domain_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jancuk/semar/internal/domain"
)

func TestBodyRoundTrip(t *testing.T) {
	cases := []struct {
		name      string
		in        []byte
		want      string
		jsonEqual bool
	}{
		{"nil", nil, `null`, false},
		{"empty", []byte{}, `null`, false},
		{"json", []byte(`{"a": 1}`), `{"json":{"a":1}}`, true},
		{"text", []byte("hello world"), `{"text":"hello world"}`, false},
		{"html", []byte("<html>"), `{"text":"\u003chtml\u003e"}`, false},
		{"quote and newline", []byte("say \"hi\"\nbye"), `{"text":"say \"hi\"\nbye"}`, false},
		{"form", []byte("a=1&b=2"), `{"text":"a=1\u0026b=2"}`, false},
		{"binary", []byte{0xff, 0xfe, 0x00}, `{"base64":"//4A"}`, false},
		{"truncated json", []byte(`{"a":`), `{"text":"{\"a\":"}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := json.Marshal(domain.Body(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Fatalf("marshal = %s, want %s", got, c.want)
			}
			var back domain.Body
			if err := json.Unmarshal(got, &back); err != nil {
				t.Fatal(err)
			}
			switch {
			case len(c.in) == 0:
				if len(back) != 0 {
					t.Fatalf("back = %q, want empty", back)
				}
			case c.jsonEqual:
				var a, b any
				if err := json.Unmarshal(c.in, &a); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(back, &b); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(a, b) {
					t.Fatalf("back = %s, want JSON equal to %s", back, c.in)
				}
			default:
				if !bytes.Equal(back, c.in) {
					t.Fatalf("back = %q, want %q", back, c.in)
				}
			}
		})
	}
}

func TestBodyJSONWithInvalidUTF8StoresAsBase64(t *testing.T) {
	in := []byte("{\"a\":\"\xff\"}")
	got, err := json.Marshal(domain.Body(in))
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.Valid(got) {
		t.Fatalf("marshalled output is not valid UTF-8: %q", got)
	}
	if !strings.HasPrefix(string(got), `{"base64":`) {
		t.Fatalf("marshal = %s, want base64 form", got)
	}
	var back domain.Body
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back, in) {
		t.Fatalf("back = %q, want %q", back, in)
	}
}

func TestBodyEdgeFormsKeepWorking(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		back string
	}{
		{"null literal", `null`, `{"json":null}`, `null`},
		{"json string", `"x"`, `{"json":"x"}`, `"x"`},
		{"json number", `42`, `{"json":42}`, `42`},
		{"only spaces", `   `, `{"text":"   "}`, `   `},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := json.Marshal(domain.Body(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Fatalf("marshal = %s, want %s", got, c.want)
			}
			var back domain.Body
			if err := json.Unmarshal(got, &back); err != nil {
				t.Fatal(err)
			}
			if string(back) != c.back {
				t.Fatalf("back = %q, want %q", back, c.back)
			}
		})
	}
}

func TestBodyUnmarshalErrors(t *testing.T) {
	var b domain.Body
	err := json.Unmarshal([]byte(`{"other": 1}`), &b)
	if err == nil || !strings.Contains(err.Error(), "other") {
		t.Fatalf("err = %v, want one naming other", err)
	}
	if err := json.Unmarshal([]byte(`{"text":"a","json":1}`), &b); err == nil {
		t.Fatal("two keys: want error")
	}
	if err := json.Unmarshal([]byte(`"plain"`), &b); err == nil {
		t.Fatal("bare string: want error")
	}
	if err := json.Unmarshal([]byte(`{"base64":"!!"}`), &b); err == nil {
		t.Fatal("bad base64: want error")
	}
}

func TestActionWithNonJSONBodies(t *testing.T) {
	in := domain.Action{
		Schema:   1,
		Op:       "request",
		Request:  &domain.HTTPRequest{Method: "POST", Body: domain.Body("a=1&b=2")},
		Response: &domain.HTTPResponse{Status: 200, Body: domain.Body{0xff, 0xfe, 0x00}},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out domain.Action
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip differs:\n in %+v\nout %+v", in, out)
	}
}

func TestBodyMarshalKeepsHTMLCharactersWithAnUnescapingEncoder(t *testing.T) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(domain.Body(`{"q":"a<b&c>d"}`)); err != nil {
		t.Fatal(err)
	}
	want := `{"json":{"q":"a<b&c>d"}}` + "\n"
	if buf.String() != want {
		t.Errorf("got %s, want %s", buf.String(), want)
	}
}
