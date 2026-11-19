package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnUploadFileIsAnAbsolutePathWhateverTheFolderIsCalled(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(filepath.Join(".semar", "files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".semar", "files", "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := uploadFile(filepath.Join(".semar", "files"), "a.txt")
	if err != nil || !filepath.IsAbs(got) || !strings.HasSuffix(got, filepath.Join(".semar", "files", "a.txt")) {
		t.Errorf("uploadFile = %q, %v", got, err)
	}
}
