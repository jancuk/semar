package android_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile/android"
)

func TestShellQuoteHoldsEveryWordAsOne(t *testing.T) {
	for in, want := range map[string]string{
		"plain":               `'plain'`,
		"":                    `''`,
		"a b":                 `'a b'`,
		"it's":                `'it'\''s'`,
		"'; rm -rf / #":       `''\''; rm -rf / #'`,
		"$(reboot)":           `'$(reboot)'`,
		"`id`":                "'`id`'",
		"a;b|c&d>e<f":         `'a;b|c&d>e<f'`,
		"line\nbreak":         "'line\nbreak'",
		`back\slash "double"`: `'back\slash "double"'`,
		"''":                  `''\'''\'''`,
	} {
		if got := android.ShellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
	if got := android.ShellCommand("am", "start", "-d", "'; rm -rf / #"); got != `'am' 'start' '-d' ''\''; rm -rf / #'` {
		t.Errorf("command = %s", got)
	}
}

// A link goes to the shell of the device only through shellQuote: the fake adb logs the one argument
// that adb shell gets.
func TestALinkThatHoldsAShellCommandIsOneArgument(t *testing.T) {
	r := newRig(t)
	p := r.platform()
	d := r.driver(p)
	link := "shop://x'; rm -rf / #"
	if err := d.Open(context.Background(), link); err != nil {
		t.Fatal(err)
	}
	var sent string
	for _, c := range r.adb.Shell() {
		if strings.Contains(c, "'-d'") {
			sent = c
		}
	}
	want := `'am' 'start' '-W' '-a' 'android.intent.action.VIEW' '-d' 'shop://x'\''; rm -rf / #' '-p' 'com.example.shop'`
	if sent != want {
		t.Errorf("sent\n%s\nwant\n%s", sent, want)
	}
	// Every shell command of a whole run is made of quoted words only.
	for _, c := range r.adb.Shell() {
		if !strings.HasPrefix(c, "'") {
			t.Errorf("a shell command starts with a bare word: %s", c)
		}
	}
}

func TestFindADB(t *testing.T) {
	repo := t.TempDir()
	sdk := t.TempDir()
	home := t.TempDir()
	mk := func(dir string) string {
		t.Helper()
		p := filepath.Join(dir, "adb")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if runtime.GOOS == "windows" {
		t.Skip("the fake programs are scripts")
	}
	env := func(m map[string]string) func(string) (string, bool) {
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	bin := mk(t.TempDir())
	onPath := mk(t.TempDir())
	fromHome := mk(filepath.Join(home, "Library", "Android", "sdk", "platform-tools"))
	if runtime.GOOS == "linux" {
		fromHome = mk(filepath.Join(home, "Android", "Sdk", "platform-tools"))
	}
	fromSDK := mk(filepath.Join(sdk, "platform-tools"))
	other := t.TempDir()
	fromRoot := mk(filepath.Join(other, "platform-tools"))

	cases := []struct {
		name string
		env  map[string]string
		want string
		err  string
	}{
		{"SEMAR_ADB_BIN wins over everything", map[string]string{"SEMAR_ADB_BIN": bin, "PATH": filepath.Dir(onPath), "ANDROID_HOME": sdk, "HOME": home}, bin, ""},
		{"a relative SEMAR_ADB_BIN is refused", map[string]string{"SEMAR_ADB_BIN": "adb", "PATH": filepath.Dir(onPath)}, "", "not an absolute path"},
		{"a SEMAR_ADB_BIN that is not there is refused, and PATH is not tried", map[string]string{"SEMAR_ADB_BIN": filepath.Join(t.TempDir(), "nope"), "PATH": filepath.Dir(onPath)}, "", "cannot be read"},
		{"PATH", map[string]string{"PATH": filepath.Dir(onPath), "ANDROID_HOME": sdk}, onPath, ""},
		{"ANDROID_HOME", map[string]string{"PATH": "", "ANDROID_HOME": sdk, "ANDROID_SDK_ROOT": other, "HOME": home}, fromSDK, ""},
		{"ANDROID_SDK_ROOT", map[string]string{"PATH": "", "ANDROID_SDK_ROOT": other, "HOME": home}, fromRoot, ""},
		{"the default place of Android Studio", map[string]string{"PATH": "", "HOME": home}, fromHome, ""},
		{"a relative ANDROID_HOME is not used", map[string]string{"PATH": "", "ANDROID_HOME": "sdk"}, "", "adb was not found"},
		{"nothing", map[string]string{"PATH": "", "HOME": t.TempDir()}, "", "adb was not found: install the Android SDK platform tools"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := android.FindADB(env(c.env), repo)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("got %q, %v; want an error with %q", got, err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

func TestFindADBRefusesAProgramInsideTheRepository(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("scripts")
	}
	repo := t.TempDir()
	inside := filepath.Join(repo, "tools", "adb")
	if err := os.MkdirAll(filepath.Dir(inside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, env := range map[string]map[string]string{
		"SEMAR_ADB_BIN": {"SEMAR_ADB_BIN": inside},
		"PATH":          {"PATH": filepath.Dir(inside)},
		"ANDROID_HOME":  {"PATH": "", "ANDROID_HOME": filepath.Join(repo, "tools", "..")},
	} {
		if name == "ANDROID_HOME" {
			if err := os.MkdirAll(filepath.Join(repo, "platform-tools"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repo, "platform-tools", "adb"), []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
		if got, err := android.FindADB(lookup, repo); err == nil || !strings.Contains(err.Error(), "inside the repository under test") {
			t.Errorf("%s: got %q, %v", name, got, err)
		}
	}
}

func TestExecRunnerOutputQuietAndStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("scripts")
	}
	dir := t.TempDir()
	prog := filepath.Join(dir, "prog")
	script := `#!/bin/sh
case "$1" in
  out) echo hello; exit 0 ;;
  fail) echo "boom: the first line" >&2; echo "second" >&2; exit 3 ;;
  failout) echo "only stdout" ; exit 4 ;;
  daemon) (sleep 30 &) ; exit 0 ;;
  child) echo "starting"; echo "last words"; exec sleep 30 ;;
  die) echo "it crashed" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(prog, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var run android.ExecRunner
	out, err := run.Output(ctx, prog, "out")
	if err != nil || strings.TrimSpace(string(out)) != "hello" {
		t.Errorf("out = %q, %v", out, err)
	}
	_, err = run.Output(ctx, prog, "fail", "SECRET-ARG")
	if err == nil || err.Error() != "adb fail failed: boom: the first line" || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("error = %v", err)
	}
	if _, err = run.Output(ctx, prog, "failout"); err == nil || !strings.Contains(err.Error(), "only stdout") {
		t.Errorf("error = %v", err)
	}
	// A command that leaves a daemon behind that holds the pipes does not hang.
	start := time.Now()
	if err := run.Quiet(ctx, prog, "daemon"); err != nil || time.Since(start) > 10*time.Second {
		t.Errorf("quiet: %v after %s", err, time.Since(start))
	}
	// A child that runs: Tail holds what it wrote, and Kill stops it, twice without harm.
	child, err := run.Start(ctx, prog, "child")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(child.Tail(), "last words") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(child.Tail(), "last words") {
		t.Errorf("tail = %q", child.Tail())
	}
	select {
	case <-child.Done():
		t.Error("the child has ended")
	default:
	}
	child.Kill()
	child.Kill()
	select {
	case <-child.Done():
	default:
		t.Error("Kill did not end the child")
	}
	// A child that dies by itself.
	die, err := run.Start(ctx, prog, "die")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-die.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the child did not end")
	}
	if die.Tail() != "it crashed" {
		t.Errorf("tail = %q", die.Tail())
	}
	if _, err := run.Start(ctx, filepath.Join(dir, "missing")); err == nil {
		t.Error("a program that is not there started")
	}
	// A cancelled context stops a command.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := run.Output(cctx, prog, "out"); err == nil {
		t.Error("a cancelled context ran a command")
	}
}
