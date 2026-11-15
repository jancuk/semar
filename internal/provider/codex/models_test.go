//go:build unix

package codex_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/config"
)

// catalogBinary writes a binary that records its arguments and working directory, and then
// runs body.
func catalogBinary(t *testing.T, body string) (path, args, cwd string) {
	t.Helper()
	dir := t.TempDir()
	path, args, cwd = filepath.Join(dir, "codex"), filepath.Join(dir, "args"), filepath.Join(dir, "cwd")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\npwd -P > %q\n%s\n", args, cwd, body)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, args, cwd
}

func TestModels(t *testing.T) {
	catalog := `{"models":[
	  {"slug":"acme-small","display_name":"Acme Small","visibility":"list","priority":5},
	  {"slug":"acme-hidden","display_name":"Hidden","visibility":"hide","priority":0},
	  {"slug":"bad slug","display_name":"Bad","visibility":"list","priority":1},
	  {"slug":"acme-large","display_name":"Acme Large","visibility":"list","priority":2}]}`
	bin, args, cwd := catalogBinary(t, "cat <<'EOF'\n"+catalog+"\nEOF")
	got, err := newDriver(t, config.Provider{}, bin).Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "acme-large" || got[0].Label != "Acme Large" || got[1].ID != "acme-small" {
		t.Fatalf("models = %+v", got)
	}
	if read(t, args) != "debug\nmodels\n" {
		t.Fatalf("args = %q", read(t, args))
	}
	if wd := strings.TrimSpace(read(t, cwd)); !strings.Contains(wd, "semar-codex-models-") {
		t.Fatalf("working directory = %q", wd)
	}
}

func TestModelsAreCut(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"models":[`)
	for i := 0; i < 150; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"slug":"acme-%03d","display_name":"A","visibility":"list","priority":%d}`, i, i)
	}
	b.WriteString(`]}`)
	bin, _, _ := catalogBinary(t, "cat <<'EOF'\n"+b.String()+"\nEOF")
	got, err := newDriver(t, config.Provider{}, bin).Models(context.Background())
	if err != nil || len(got) != 100 || got[99].ID != "acme-099" {
		t.Fatalf("%d models, error %v", len(got), err)
	}
}

func TestModelsFailures(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"broken JSON":      {"echo '{\"models\":'", "not a model list"},
		"a failing binary": {"echo 'no network' >&2; exit 3", "exit code 3: no network"},
		"too much output":  {"head -c 5000000 /dev/zero | tr '\\0' 'x'", "larger than 4 MiB"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			bin, _, _ := catalogBinary(t, c.body)
			_, err := newDriver(t, config.Provider{}, bin).Models(context.Background())
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want %q", err, c.want)
			}
		})
	}
	t.Run("a binary that hangs", func(t *testing.T) {
		bin, _, _ := catalogBinary(t, "sleep 30")
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		if _, err := newDriver(t, config.Provider{}, bin).Models(ctx); err == nil {
			t.Fatal("no error")
		}
	})
	t.Run("no binary", func(t *testing.T) {
		if _, err := newDriver(t, config.Provider{}, filepath.Join(t.TempDir(), "missing")).Models(context.Background()); err == nil {
			t.Fatal("no error")
		}
	})
}

func TestModelsCarryLevelsAndDescription(t *testing.T) {
	catalog := `{"models":[
	  {"slug":"acme-large","display_name":"Acme Large","description":"The model for hard problems.","visibility":"list","priority":1,
	   "default_reasoning_level":"medium",
	   "supported_reasoning_levels":[{"effort":"low","description":"x"},{"effort":"medium","description":"y"},{"effort":"High","description":"bad case"},{"effort":"high"},{"effort":"low"},{"effort":"extra high"}]},
	  {"slug":"acme-plain","display_name":"Acme Plain","visibility":"list","priority":2,"default_reasoning_level":"Not Valid"}]}`
	bin, _, _ := catalogBinary(t, "cat <<'EOF'\n"+catalog+"\nEOF")
	got, err := newDriver(t, config.Provider{}, bin).Models(context.Background())
	if err != nil || len(got) != 2 {
		t.Fatalf("models = %+v, error %v", got, err)
	}
	large := got[0]
	if large.Description != "The model for hard problems." || !slices.Equal(large.Reasoning, []string{"low", "medium", "high"}) || large.DefaultReasoning != "medium" {
		t.Fatalf("acme-large = %+v", large)
	}
	if plain := got[1]; plain.Description != "" || len(plain.Reasoning) != 0 || plain.DefaultReasoning != "" {
		t.Fatalf("acme-plain = %+v", plain)
	}
}
