package evidence_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
)

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks are not available:", err)
	}
}

func TestOpenRefusesALinkedOrNonDirectoryStore(t *testing.T) {
	tests := []struct {
		name    string
		replace string
		link    bool
		want    string
	}{
		{name: "linked runs", replace: "runs", link: true, want: "symbolic link"},
		{name: "linked store", replace: "store", link: true, want: "symbolic link"},
		{name: "linked root", replace: "", link: true, want: "symbolic link"},
		{name: "file as runs", replace: "runs", want: "not a directory"},
		{name: "file as store", replace: "store", want: "not a directory"},
		{name: "file as root", replace: "", want: "not a directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := t.TempDir()
			root := filepath.Join(repo, ".semar")
			if tt.replace != "" {
				if err := os.Mkdir(root, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			bad := filepath.Join(root, tt.replace)
			target := filepath.Join(t.TempDir(), "elsewhere")
			if err := os.Mkdir(target, 0o755); err != nil {
				t.Fatal(err)
			}
			// a mode that Open would change if it followed the link
			if err := os.Chmod(target, 0o755); err != nil {
				t.Fatal(err)
			}
			if tt.link {
				symlinkOrSkip(t, target, bad)
			} else if err := os.WriteFile(bad, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}

			_, err = evidence.Open(root)
			if err == nil {
				t.Fatal("Open succeeded")
			}
			if !strings.Contains(err.Error(), bad) || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to name %s and say %q", err, bad, tt.want)
			}
			after, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			if after.Mode() != before.Mode() {
				t.Errorf("target mode changed from %v to %v", before.Mode(), after.Mode())
			}
			entries, err := os.ReadDir(target)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Errorf("Open created %v in the link's target", entries)
			}
		})
	}
}

func TestOpenCreatesTheStoreWithSafeModes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo", ".semar")
	if _, err := evidence.Open(root); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{root, filepath.Join(root, "runs"), filepath.Join(root, "store")} {
		info, err := os.Lstat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Errorf("%s = %v", dir, info.Mode())
		}
	}
}

func TestRedactKeyRefusesAnUnsafeKeyFile(t *testing.T) {
	key := strings.Repeat("k", 32)
	tests := []struct {
		name  string
		setup func(t *testing.T, path string)
		want  string
	}{
		{
			name: "readable by others",
			setup: func(t *testing.T, path string) {
				if runtime.GOOS == "windows" {
					t.Skip("Windows has no permission bits for others")
				}
				writeMode(t, path, []byte(key), 0o644)
			},
			want: "0644",
		},
		{
			name: "readable by the group",
			setup: func(t *testing.T, path string) {
				if runtime.GOOS == "windows" {
					t.Skip("Windows has no permission bits for the group")
				}
				writeMode(t, path, []byte(key), 0o640)
			},
			want: "0640",
		},
		{
			name: "a symbolic link to a safe file",
			setup: func(t *testing.T, path string) {
				target := filepath.Join(t.TempDir(), "real.key")
				writeMode(t, target, []byte(key), 0o600)
				symlinkOrSkip(t, target, path)
			},
			want: "symbolic link",
		},
		{
			name: "a directory",
			setup: func(t *testing.T, path string) {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			},
			want: "not a regular file",
		},
		{
			name:  "wrong length",
			setup: func(t *testing.T, path string) { writeMode(t, path, []byte("short-secret-value"), 0o600) },
			want:  "holds 18 bytes",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openStore(t)
			path := filepath.Join(s.Root(), "store", "redact.key")
			tt.setup(t, path)
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}

			got, err := s.RedactKey()
			if err == nil {
				t.Fatalf("RedactKey = %x, want an error", got)
			}
			msg := err.Error()
			if !strings.Contains(msg, path) || !strings.Contains(msg, tt.want) || !strings.Contains(msg, "delete") {
				t.Errorf("err = %v, want it to name %s, say %q, and say to delete it", err, path, tt.want)
			}
			if strings.Contains(msg, key) || strings.Contains(msg, "short-secret-value") {
				t.Errorf("err holds the key: %v", err)
			}
			after, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if after.Mode() != before.Mode() {
				t.Errorf("mode changed from %v to %v", before.Mode(), after.Mode())
			}
		})
	}
}

func writeMode(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestScriptsRefuseALinkedOrNonDirectoryComponent(t *testing.T) {
	tests := []struct {
		name string
		at   string
		link bool
		want string
	}{
		{name: "linked scripts", at: "scripts", link: true, want: "symbolic link"},
		{name: "linked scenario directory", at: filepath.Join("scripts", "refund"), link: true, want: "symbolic link"},
		{name: "file as scripts", at: "scripts", want: "not a directory"},
		{name: "file as scenario directory", at: filepath.Join("scripts", "refund"), want: "not a directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openStore(t)
			bad := filepath.Join(s.Root(), "store", tt.at)
			if err := os.MkdirAll(filepath.Dir(bad), 0o700); err != nil {
				t.Fatal(err)
			}
			target := t.TempDir()
			if tt.link {
				symlinkOrSkip(t, target, bad)
			} else if err := os.WriteFile(bad, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			check := func(what string, err error) {
				t.Helper()
				if err == nil || !strings.Contains(err.Error(), bad) || !strings.Contains(err.Error(), tt.want) {
					t.Errorf("%s: err = %v, want it to name %s and say %q", what, err, bad, tt.want)
				}
			}
			check("PutScript", s.PutScript(scriptFixture("refund", "aaa")))
			_, _, err := s.Script("refund", "aaa")
			check("Script", err)
			check("RemoveScript", s.RemoveScript("refund", "aaa"))
			entries, err := os.ReadDir(target)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Errorf("a script was written at the link's target: %v", entries)
			}
		})
	}
}

func TestRemoveScript(t *testing.T) {
	s := openStore(t)
	for _, h := range []domain.Hash{"aaa", "bbb"} {
		if err := s.PutScript(scriptFixture("refund", string(h))); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(s.Root(), "store", "scripts", "refund")
	if err := s.RemoveScript("refund", "aaa"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Script("refund", "aaa"); ok || err != nil {
		t.Errorf("Script(aaa) after RemoveScript = %v, %v", ok, err)
	}
	if _, ok, err := s.Script("refund", "bbb"); !ok || err != nil {
		t.Errorf("Script(bbb) = %v, %v; the other script must stay", ok, err)
	}
	if err := s.RemoveScript("refund", "bbb"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Errorf("the empty directory of the scenario stays: %v", err)
	}
	if err := s.RemoveScript("refund", "bbb"); err != nil {
		t.Errorf("removing a script that is not stored: %v", err)
	}
}
