package android_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile/android"
)

func fakeProgram(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFindADBWhereSources(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the programs are shell scripts")
	}
	repo := t.TempDir()
	for _, c := range []struct {
		name   string
		goos   string
		setup  func(root string, env map[string]string) (want string)
		source string
	}{
		{"env wins", "darwin", func(root string, env map[string]string) string {
			p := fakeProgram(t, filepath.Join(root, "bin", "adb"))
			env["SEMAR_ADB_BIN"] = p
			env["PATH"] = filepath.Join(root, "other")
			fakeProgram(t, filepath.Join(root, "other", "adb"))
			return p
		}, android.ADBFromEnv},
		{"path", "linux", func(root string, env map[string]string) string {
			env["PATH"] = "relative/bin:" + filepath.Join(root, "p")
			return fakeProgram(t, filepath.Join(root, "p", "adb"))
		}, android.ADBFromPath},
		{"path before the sdk", "linux", func(root string, env map[string]string) string {
			env["PATH"] = filepath.Join(root, "p")
			env["ANDROID_HOME"] = filepath.Join(root, "sdk")
			fakeProgram(t, filepath.Join(root, "sdk", "platform-tools", "adb"))
			return fakeProgram(t, filepath.Join(root, "p", "adb"))
		}, android.ADBFromPath},
		{"android home", "linux", func(root string, env map[string]string) string {
			env["ANDROID_HOME"] = filepath.Join(root, "home")
			env["ANDROID_SDK_ROOT"] = filepath.Join(root, "sdkroot")
			fakeProgram(t, filepath.Join(root, "sdkroot", "platform-tools", "adb"))
			return fakeProgram(t, filepath.Join(root, "home", "platform-tools", "adb"))
		}, android.ADBFromAndroidHome},
		{"sdk root", "linux", func(root string, env map[string]string) string {
			env["ANDROID_SDK_ROOT"] = filepath.Join(root, "sdkroot")
			return fakeProgram(t, filepath.Join(root, "sdkroot", "platform-tools", "adb"))
		}, android.ADBFromSDKRoot},
		{"sdk default on macOS", "darwin", func(root string, env map[string]string) string {
			env["HOME"] = filepath.Join(root, "h")
			return fakeProgram(t, filepath.Join(root, "h", "Library", "Android", "sdk", "platform-tools", "adb"))
		}, android.ADBFromSDKDefault},
		{"sdk default on linux", "linux", func(root string, env map[string]string) string {
			env["HOME"] = filepath.Join(root, "h")
			return fakeProgram(t, filepath.Join(root, "h", "Android", "Sdk", "platform-tools", "adb"))
		}, android.ADBFromSDKDefault},
		{"homebrew on macOS", "darwin", func(root string, env map[string]string) string {
			real := fakeProgram(t, filepath.Join(root, "Cellar", "adb"))
			link := filepath.Join(root, "hb", "adb")
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(real, link); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(android.SetHomebrewADB([]string{filepath.Join(root, "missing", "adb"), link}))
			return link
		}, android.ADBFromHomebrew},
		{"the sdk before homebrew", "darwin", func(root string, env map[string]string) string {
			env["HOME"] = filepath.Join(root, "h")
			t.Cleanup(android.SetHomebrewADB([]string{fakeProgram(t, filepath.Join(root, "hb", "adb"))}))
			return fakeProgram(t, filepath.Join(root, "h", "Library", "Android", "sdk", "platform-tools", "adb"))
		}, android.ADBFromSDKDefault},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			env := map[string]string{}
			want := c.setup(root, env)
			lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
			got, source, err := android.FindADBFor(lookup, repo, c.goos)
			if err != nil {
				t.Fatal(err)
			}
			if got != want || source != c.source {
				t.Errorf("got %s (%s), want %s (%s)", got, source, want, c.source)
			}
			// FindADB gives the same path.
			if c.goos == runtime.GOOS {
				if p, err := android.FindADB(lookup, repo); err != nil || p != want {
					t.Errorf("FindADB = %s, %v", p, err)
				}
			}
		})
	}
}

func TestFindADBHomebrewIsForMacOnly(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(android.SetHomebrewADB([]string{fakeProgram(t, filepath.Join(root, "hb", "adb"))}))
	lookup := func(string) (string, bool) { return "", false }
	if _, _, err := android.FindADBFor(lookup, t.TempDir(), "linux"); err == nil || !strings.Contains(err.Error(), "adb was not found") {
		t.Errorf("linux err = %v", err)
	}
	if _, _, err := android.FindADBFor(lookup, t.TempDir(), "darwin"); err != nil {
		t.Errorf("darwin err = %v", err)
	}
}

func TestFindADBWhereRefusesAProgramInTheRepository(t *testing.T) {
	repo := t.TempDir()
	p := fakeProgram(t, filepath.Join(repo, "tools", "adb"))
	env := map[string]string{"PATH": filepath.Dir(p)}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	if _, _, err := android.FindADBFor(lookup, repo, "linux"); err == nil {
		t.Error("an adb in the repository was accepted")
	}
}

// The fake adb is a script that writes what the test needs to the stdout of the stream.
func streamScript(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake adb is a shell script")
	}
	return fakeScript(t, "#!/bin/sh\n"+body+"\n")
}

func fakeScript(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "adb")
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStreamCutsLongLinesAndEndsAtTheTotalCap(t *testing.T) {
	bin := streamScript(t, `
printf 'first\n'
head -c 40000 /dev/zero | tr '\0' 'a'
printf '\nlast of the long\n'
yes bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb | head -c 9000000
`)
	rc, wait, err := android.ExecRunner{}.Stream(context.Background(), bin, "x")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()
	if err := wait(); err != nil {
		t.Errorf("wait = %v", err)
	}
	if len(data) > 4<<20 {
		t.Errorf("read %d bytes, the cap is 4 MiB", len(data))
	}
	if len(data) < 4<<20-300 {
		t.Errorf("read only %d bytes", len(data))
	}
	lines := strings.Split(string(data), "\n")
	if lines[0] != "first" {
		t.Errorf("first line = %q", lines[0])
	}
	if len(lines[1]) != 16<<10 {
		t.Errorf("the long line was cut to %d bytes, want %d", len(lines[1]), 16<<10)
	}
	if lines[2] != "last of the long" {
		t.Errorf("the line after the long one = %q", lines[2])
	}
}

func TestStreamCloseStopsTheChild(t *testing.T) {
	bin := streamScript(t, "echo hello\nexec sleep 60")
	rc, wait, err := android.ExecRunner{}.Stream(context.Background(), bin)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 6)
	if _, err := io.ReadFull(rc, buf); err != nil || string(buf) != "hello\n" {
		t.Fatalf("read %q, %v", buf, err)
	}
	start := time.Now()
	_ = rc.Close()
	if err := wait(); err != nil {
		t.Errorf("a child that was closed gave %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("Close did not stop the child")
	}
}

func TestStreamReportsAFailureThatNobodyAskedFor(t *testing.T) {
	bin := streamScript(t, "echo partial\necho 'device offline' >&2\nexit 3")
	rc, wait, err := android.ExecRunner{}.Stream(context.Background(), bin, "shell")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(rc)
	werr := wait()
	if werr == nil || !strings.Contains(werr.Error(), "device offline") {
		t.Errorf("wait = %v", werr)
	}
	if _, _, err := (android.ExecRunner{}).Stream(context.Background(), filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("a program that is not there started")
	}
}

func TestADBVersionRunsTheProgram(t *testing.T) {
	bin := streamScript(t, `printf 'Android Debug Bridge version 1.0.41\nVersion 35.0.2-12147458\n'`)
	v, err := android.ADBVersion(context.Background(), bin)
	if err != nil || v != "35.0.2" {
		t.Errorf("version = %q, %v", v, err)
	}
	if _, err := android.ADBVersion(context.Background(), streamScript(t, "echo nonsense")); err == nil {
		t.Error("nonsense gave a version")
	}
}
