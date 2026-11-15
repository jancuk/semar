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
