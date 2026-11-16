//go:build unix

package journey

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestLoadRefusesSpecialFiles(t *testing.T) {
	repo := repoWith(t, map[string]string{"checkout.yaml": exampleText(t)})
	if err := syscall.Mkfifo(filepath.Join(repo, ".semar", "journeys", "pipe.yaml"), 0o600); err != nil {
		t.Skip("no named pipes:", err)
	}
	_, _, err := Load(repo)
	if err == nil || !strings.Contains(err.Error(), "pipe.yaml is not a regular file") {
		t.Fatalf("err = %v", err)
	}
}
