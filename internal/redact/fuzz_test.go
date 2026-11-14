package redact_test

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/redact"
)

func percentAll(s, format string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		fmt.Fprintf(&b, format, s[i])
	}
	return b.String()
}

var encoders = []func(string) string{
	func(s string) string { return s },
	url.QueryEscape,
	url.PathEscape,
	func(s string) string { return percentAll(s, "%%%02X") },
	func(s string) string { return percentAll(s, "%%%02x") },
	func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) },
	func(s string) string { return base64.URLEncoding.EncodeToString([]byte(s)) },
}

func forms(secret string) []string {
	out := make([]string, len(encoders))
	for i, enc := range encoders {
		out[i] = enc(secret)
	}
	return out
}

const fuzzHeader = "X-Note"

func place(where uint8, text string) domain.Action {
	switch where % 4 {
	case 0:
		return domain.Action{Response: &domain.HTTPResponse{Body: []byte(text)}}
	case 1:
		return domain.Action{Request: &domain.HTTPRequest{URL: text}}
	case 2:
		return domain.Action{Request: &domain.HTTPRequest{Headers: map[string][]string{fuzzHeader: {text}}}}
	default:
		return domain.Action{Err: text}
	}
}

// texts returns every string the output carries. json.Marshal of the action
// would base64 the body and escape some bytes, which can hide a form or create
// one, so the fields are read directly.
func texts(a domain.Action) []string {
	out := []string{a.Err, a.Refused, a.Why}
	if a.Request != nil {
		out = append(out, a.Request.URL, a.Request.PathTemplate, string(a.Request.Body))
		for _, vs := range a.Request.Headers {
			out = append(out, vs...)
		}
	}
	if a.Response != nil {
		out = append(out, string(a.Response.Body))
		for _, vs := range a.Response.Headers {
			out = append(out, vs...)
		}
	}
	for _, v := range a.Captures {
		out = append(out, v)
	}
	return out
}

// fitsInMarker reports whether f can be part of a marker, alone or as a JSON
// string. Such a text cannot be removed from the output.
func fitsInMarker(f string) bool {
	const template = `"[redacted:????????]"`
	for k := 0; k+len(f) <= len(template); k++ {
		ok := true
		for i := 0; i < len(f) && ok; i++ {
			c := template[k+i]
			ok = c == f[i] || c == '?' && strings.IndexByte("0123456789abcdef", f[i]) >= 0
		}
		if ok {
			return true
		}
	}
	return false
}

func lenientDecode(s string, plus bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if d, err := hex.DecodeString(s[i+1 : i+3]); err == nil {
				b.Write(d)
				i += 2
				continue
			}
		}
		if plus && s[i] == '+' {
			b.WriteByte(' ')
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// mayNeedRedaction is a test-side oracle: false means the text holds no form of
// the secret in any decoded view and nothing a built-in pattern could match.
func mayNeedRedaction(text, secret string) bool {
	lower := strings.ToLower(text)
	for _, s := range []string{`\`, "eyj", "bearer", "basic"} {
		if strings.Contains(lower, s) {
			return true
		}
	}
	all := append(forms(secret), base64.RawStdEncoding.EncodeToString([]byte(secret)), base64.RawURLEncoding.EncodeToString([]byte(secret)))
	views := []string{text}
	for _, plus := range []bool{false, true} {
		v := text
		for range 4 {
			v = lenientDecode(v, plus)
			views = append(views, v)
		}
	}
	for _, v := range views {
		for _, f := range all {
			if strings.Contains(v, f) {
				return true
			}
		}
	}
	return false
}

func FuzzNoSecretSurvives(f *testing.F) {
	f.Add("prefix", "suffix", "hunter2-token", uint8(0), uint8(0))
	f.Add(`{"k":"`, `"}`, "hunter2-token", uint8(3), uint8(0))
	f.Add(`"`, `"`, `x\nxxxx`, uint8(0), uint8(0))
	f.Add("a=", "&b=1", "hunter2-token", uint8(1), uint8(1))
	f.Add("50% done; t=", "&d=100%", "s3cret-value", uint8(4), uint8(2))
	f.Add("q=", "+%2", "p@ss word/+=1", uint8(1), uint8(3))
	f.Add(`{"`, `":1}`, "s3cret-value", uint8(5), uint8(0))
	f.Add("%", "%4", "hunter2-token", uint8(6), uint8(1))
	f.Add(`{"id":1700000`, `,"n":[1,2]}`, "123456", uint8(0), uint8(0))
	f.Fuzz(func(t *testing.T, a, b, secret string, enc, where uint8) {
		if len(secret) < 6 || strings.ContainsAny(secret, "\x00") {
			t.Skip()
		}
		for _, form := range forms(secret) {
			if fitsInMarker(form) {
				t.Skip()
			}
		}
		r, err := redact.New(config.Redact{}, func() []string { return []string{secret} }, testKey)
		if err != nil {
			t.Fatal(err)
		}
		text := a + encoders[int(enc)%len(encoders)](secret) + b
		in := place(where, text)
		out := r.Action(in)

		for _, s := range texts(out) {
			for _, form := range forms(secret) {
				if strings.Contains(s, form) {
					t.Fatalf("form %q of the secret survived in %q", form, s)
				}
			}
		}
		if again := r.Action(out); !reflect.DeepEqual(again, out) {
			t.Fatalf("second pass changed the action\nonce:  %+v\ntwice: %+v", out, again)
		}
		if in.Response != nil && json.Valid(in.Response.Body) && !json.Valid(out.Response.Body) {
			t.Fatalf("valid JSON %q became invalid: %q", in.Response.Body, out.Response.Body)
		}
		// Without a quote or a backslash, no form can cross a string's quotes or
		// an escape, so the secret is redacted inside the string.
		if !strings.ContainsAny(secret, `"\`) && !strings.ContainsAny(text, `"\`) {
			body := `{"k":"` + text + `"}`
			if json.Valid([]byte(body)) {
				out := r.Action(domain.Action{Response: &domain.HTTPResponse{Body: []byte(body)}})
				var s string
				if json.Unmarshal(out.Response.Body, &s) == nil && redact.IsMarker(s) {
					t.Fatalf("a secret in a JSON string replaced the whole body %q: %q", body, out.Response.Body)
				}
			}
		}
		if clean := a + b; !mayNeedRedaction(clean, secret) {
			in := place(where, clean)
			if out := r.Action(in); !reflect.DeepEqual(out, in) {
				t.Fatalf("text without a secret changed\nin:  %+v\nout: %+v", in, out)
			}
		}
	})
}
