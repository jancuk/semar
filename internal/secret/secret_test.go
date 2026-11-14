package secret_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/secret"
)

func newEnv() *secret.Env {
	vars := map[string]string{"TOKEN": "s3cret-value", "OTHER": "other-value", "EMPTY": ""}
	return secret.NewEnv(func(k string) (string, bool) { v, ok := vars[k]; return v, ok })
}

func TestEnvResolve(t *testing.T) {
	cases := []struct {
		name, ref string
		want      string
		wantErr   error
		errText   string
	}{
		{"set", "env:TOKEN", "s3cret-value", nil, ""},
		{"missing", "env:MISSING", "", nil, "env:MISSING"},
		{"empty value", "env:EMPTY", "", nil, "env:EMPTY"},
		{"empty name", "env:", "", nil, "env:"},
		{"other scheme", "keychain:semar/x", "", secret.ErrUnsupported, "keychain:semar/x"},
		{"no scheme", "TOKEN", "", nil, "TOKEN"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := newEnv().Resolve(context.Background(), c.ref)
			if c.errText == "" {
				if err != nil || got != c.want {
					t.Fatalf("got %q, %v", got, err)
				}
				return
			}
			if err == nil {
				t.Fatal("want an error")
			}
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Fatalf("want %v, got %v", c.wantErr, err)
			}
			if !strings.Contains(err.Error(), c.errText) {
				t.Fatalf("error %q does not name %q", err, c.errText)
			}
			if strings.Contains(err.Error(), "s3cret-value") || got != "" {
				t.Fatal("an error must not hold a value")
			}
			if strings.HasPrefix(c.errText, "{{") && !strings.Contains(err.Error(), "{{secret.NAME}}") {
				t.Errorf("err = %v, want it to show the accepted form {{secret.NAME}}", err)
			}
		})
	}
}

func TestEnvResolveCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := newEnv().Resolve(ctx, "env:TOKEN"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestEnvLoaded(t *testing.T) {
	cases := []struct {
		name string
		refs []string
		want []string
	}{
		{"none", nil, []string{}},
		{"once in first-resolved order", []string{"env:TOKEN", "env:OTHER", "env:TOKEN"}, []string{"s3cret-value", "other-value"}},
		{"failures add nothing", []string{"env:MISSING", "env:OTHER"}, []string{"other-value"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newEnv()
			for _, ref := range c.refs {
				_, _ = r.Resolve(context.Background(), ref)
			}
			if got := r.Loaded(); !slices.Equal(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
	t.Run("returned slice is a copy", func(t *testing.T) {
		r := newEnv()
		_, _ = r.Resolve(context.Background(), "env:TOKEN")
		r.Loaded()[0] = "changed"
		if got := r.Loaded(); got[0] != "s3cret-value" {
			t.Fatal(got)
		}
	})
}

func TestEnvConcurrent(t *testing.T) {
	r := newEnv()
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = r.Resolve(context.Background(), "env:TOKEN")
			_, _ = r.Resolve(context.Background(), "env:OTHER")
			_ = r.Loaded()
		}()
	}
	wg.Wait()
	if got := r.Loaded(); len(got) != 2 {
		t.Fatal(got)
	}
}

func TestExpand(t *testing.T) {
	values := map[string]string{"TOKEN": "s3cret-value", "USER": "alice", "BRACES": "a{{b"}
	cases := []struct {
		name, text, want, errText string
	}{
		{"one placeholder", "Bearer {{secret.TOKEN}}", "Bearer s3cret-value", ""},
		{"two placeholders", "{{secret.USER}}:{{secret.TOKEN}}", "alice:s3cret-value", ""},
		{"no placeholder", "Basic abc", "Basic abc", ""},
		{"unknown name", "Bearer {{secret.NOPE}}", "", "{{secret.NOPE}}"},
		{"empty name", "Bearer {{secret.}}", "", "{{secret.}}"},
		{"not closed", "Bearer {{secret.TOKEN", "", "{{secret."},
		{"spaces inside", "Bearer {{ secret.TOKEN }}", "", "{{ secret.TOKEN }}"},
		{"wrong case", "Bearer {{Secret.TOKEN}}", "", "{{Secret.TOKEN}}"},
		{"another word", "Bearer {{token}}", "", "{{token}}"},
		{"a later stray one", "{{secret.USER}} {{ secret.TOKEN }}", "", "{{ secret.TOKEN }}"},
		{"a value holding braces is not text", "{{secret.BRACES}}", "a{{b", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := secret.Expand(c.text, values)
			if c.errText == "" {
				if err != nil || got != c.want {
					t.Fatalf("got %q, %v", got, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.errText) {
				t.Fatalf("err = %v, want one that names %q", err, c.errText)
			}
			if strings.Contains(err.Error(), "s3cret-value") || got != "" {
				t.Fatal("an error must not hold a value")
			}
			if strings.HasPrefix(c.errText, "{{") && !strings.Contains(err.Error(), "{{secret.NAME}}") {
				t.Errorf("err = %v, want it to show the accepted form {{secret.NAME}}", err)
			}
		})
	}
}
