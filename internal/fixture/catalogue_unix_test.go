//go:build unix

package fixture

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestLoadRefusesSpecialFiles(t *testing.T) {
	repo := repoWith(t, map[string]string{"order.paid.yaml": exampleText(t)})
	if err := syscall.Mkfifo(filepath.Join(repo, ".semar", "fixtures", "pipe.yaml"), 0o600); err != nil {
		t.Skip("no named pipes:", err)
	}
	_, err := Load(repo, nil)
	if err == nil || !strings.Contains(err.Error(), "pipe.yaml is not a regular file") {
		t.Fatalf("err = %v", err)
	}
	if _, err := LoadOne(repo, "pipe", nil); err == nil || !strings.Contains(err.Error(), "is not a regular file") {
		t.Fatalf("LoadOne err = %v", err)
	}
}
