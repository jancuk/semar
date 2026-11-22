// Package android runs the steps of an app on an Android device. It starts the UiAutomator2 server of
// Appium on the device with adb, or talks to a W3C WebDriver server at serverUrl, and it gives the mobile
// engine a Platform. adb is used only to prepare, reset, launch, and clean up: everything that polls goes
// over HTTP.
package android

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/jancuk/semar/internal/provider"
)

const (
	// adbWaitDelay is how long a command is given to close its pipes after it was told to stop. The adb
	// daemon that a command starts inherits the pipes, and Wait would otherwise hang until it exits.
	adbWaitDelay = 2 * time.Second
	// maxOutput bounds what is read of the output of one command: a dumpsys is large, and nothing a step
	// needs is more than this.
	maxOutput = 4 << 20
	tailSize  = 4 << 10
)

// Runner starts the adb program. The default runs it with os/exec, and a test passes its own.
type Runner interface {
	// Output runs adb with the arguments and gives what it wrote to stdout. A failure is an error whose
	// text is the first line of what adb wrote to stderr, and never the arguments.
	Output(ctx context.Context, adb string, args ...string) ([]byte, error)
	// Quiet runs adb with its output sent to the null device, for a command that starts a daemon.
	Quiet(ctx context.Context, adb string, args ...string) error
	// Start starts adb as a child that runs until it is killed or ends by itself.
	Start(ctx context.Context, adb string, args ...string) (Child, error)
}

// Child is a program that runs in the background.
type Child interface {
	// Tail is the last bytes that the child wrote, for the reason that it stopped.
	Tail() string
	// Done is closed when the child has exited.
	Done() <-chan struct{}
	// Kill stops the child and waits for it. It is safe to call twice.
	Kill()
}

// ExecRunner is the Runner that starts real processes.
type ExecRunner struct{}

func (ExecRunner) Output(ctx context.Context, adb string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, adb, args...)
	cmd.WaitDelay = adbWaitDelay
	var out, errOut limitedBuffer
	out.max, errOut.max = maxOutput, 8<<10
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return out.Bytes(), ctx.Err()
		}
		return out.Bytes(), commandError(args, err, errOut.String(), out.String())
	}
	return out.Bytes(), nil
}

func (ExecRunner) Quiet(ctx context.Context, adb string, args ...string) error {
	cmd := exec.CommandContext(ctx, adb, args...)
	cmd.WaitDelay = adbWaitDelay
	// A nil Stdout and Stderr are the null device.
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return commandError(args, err, "", "")
	}
	return nil
}

func (ExecRunner) Start(_ context.Context, adb string, args ...string) (Child, error) {
	// The child belongs to the platform and not to the step that started it, so it has a context of its own.
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, adb, args...)
	cmd.WaitDelay = adbWaitDelay
	c := &execChild{cancel: cancel, done: make(chan struct{})}
	c.tail.max = tailSize
	cmd.Stdout, cmd.Stderr = &c.tail, &c.tail
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, commandError(args, err, "", "")
	}
	go func() {
		_ = cmd.Wait()
		close(c.done)
	}()
	return c, nil
}

type execChild struct {
	cancel context.CancelFunc
	done   chan struct{}
	tail   limitedBuffer
}

func (c *execChild) Tail() string          { return strings.TrimSpace(c.tail.tailString()) }
func (c *execChild) Done() <-chan struct{} { return c.done }
func (c *execChild) Kill() {
	c.cancel()
	<-c.done
}

// commandError names the adb command by its first words, and gives the first line that adb wrote. It
// never holds the arguments: a link or a path may hold a value.
func commandError(args []string, err error, stderr, stdout string) error {
	name := "adb"
	for _, a := range args {
		if !strings.HasPrefix(a, "-") && a != "" {
			name += " " + a
			break
		}
	}
	line := firstLine(stderr)
	if line == "" {
		line = firstLine(stdout)
	}
	var ee *exec.ExitError
	switch {
	case line != "":
		return fmt.Errorf("%s failed: %s", name, line)
	case errors.As(err, &ee):
		return fmt.Errorf("%s failed: %s", name, ee.ProcessState.String())
	}
	if inner := errors.Unwrap(err); inner != nil {
		return fmt.Errorf("%s failed: %w", name, inner)
	}
	return fmt.Errorf("%s failed: %w", name, err)
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if len(l) > 300 {
				l = l[:300] + "..."
			}
			return l
		}
	}
	return ""
}

// limitedBuffer keeps the last max bytes that were written.
type limitedBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.max {
		// Keep the tail: a child that is still running shows what it wrote last.
		b.buf = append([]byte(nil), b.buf[len(b.buf)-b.max:]...)
	}
	return len(p), nil
}

func (b *limitedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buf)
}

func (b *limitedBuffer) String() string     { return string(b.Bytes()) }
func (b *limitedBuffer) tailString() string { return b.String() }

var _ io.Writer = (*limitedBuffer)(nil)

// shellQuote quotes one word for the shell of the device. adb shell joins its arguments with spaces and
// the shell of the device parses the result, so a value with a quote, a semicolon, or a space would
// become more than one word, or a second command. Inside single quotes nothing is special except the
// quote itself, which is written as '\”.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellCommand quotes every word and joins them, as the one argument that adb shell gets.
func shellCommand(words ...string) string {
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = shellQuote(w)
	}
	return strings.Join(q, " ")
}

// adb is one adb program, for one device once it is chosen.
type adb struct {
	bin    string
	run    Runner
	serial string
}

// args puts the serial first, as adb wants it.
func (a *adb) args(rest ...string) []string {
	if a.serial == "" {
		return rest
	}
	return append([]string{"-s", a.serial}, rest...)
}

func (a *adb) out(ctx context.Context, rest ...string) (string, error) {
	b, err := a.run.Output(ctx, a.bin, a.args(rest...)...)
	return string(b), err
}

// shell runs a command on the device. Every word goes through shellQuote.
func (a *adb) shell(ctx context.Context, words ...string) (string, error) {
	return a.out(ctx, "shell", shellCommand(words...))
}

// FindADB finds the adb program. The repository under test cannot choose it, so there is no key for it
// in the configuration. The order is SEMAR_ADB_BIN (an absolute path), PATH, $ANDROID_HOME and
// $ANDROID_SDK_ROOT, and the place where Android Studio installs the SDK. A program inside the
// repository under test is refused.
func FindADB(lookup func(string) (string, bool), repoRoot string) (string, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if p, ok := lookup("SEMAR_ADB_BIN"); ok && p != "" {
		bin, perr := provider.FindBinary(lookup, "SEMAR_ADB_BIN", "adb")
		if perr != nil {
			return "", perr
		}
		return refuseInside(bin, repoRoot)
	}
	name := "adb"
	if runtime.GOOS == "windows" {
		name = "adb.exe"
	}
	// PATH is read from the lookup, so that a test can change it. A relative entry is skipped: it would
	// start a program of the directory that the run happens to be in.
	if path, ok := lookup("PATH"); ok {
		for _, dir := range filepath.SplitList(path) {
			if dir == "" || !filepath.IsAbs(dir) {
				continue
			}
			if candidate := filepath.Join(dir, name); isProgram(candidate) {
				return refuseInside(candidate, repoRoot)
			}
		}
	}
	for _, dir := range sdkDirs(lookup) {
		if candidate := filepath.Join(dir, "platform-tools", name); isProgram(candidate) {
			return refuseInside(candidate, repoRoot)
		}
	}
	return "", errors.New("adb was not found: install the Android SDK platform tools, then put adb on PATH, set ANDROID_HOME, or set SEMAR_ADB_BIN to its absolute path")
}

// isProgram reports whether a path is a regular file that can be run.
func isProgram(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && (runtime.GOOS == "windows" || info.Mode().Perm()&0o111 != 0)
}

func refuseInside(bin, repoRoot string) (string, error) {
	if err := provider.RefuseInside(bin, repoRoot, ""); err != nil {
		return "", err
	}
	return bin, nil
}

// sdkDirs lists where the Android SDK may be, from the environment and from the default of Android Studio.
func sdkDirs(lookup func(string) (string, bool)) []string {
	var dirs []string
	for _, v := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if p, ok := lookup(v); ok && p != "" && filepath.IsAbs(p) {
			dirs = append(dirs, p)
		}
	}
	home, _ := lookup("HOME")
	switch runtime.GOOS {
	case "darwin":
		if home != "" {
			dirs = append(dirs, filepath.Join(home, "Library", "Android", "sdk"))
		}
	case "windows":
		if p, ok := lookup("LOCALAPPDATA"); ok && p != "" {
			dirs = append(dirs, filepath.Join(p, "Android", "Sdk"))
		}
	default:
		if home != "" {
			dirs = append(dirs, filepath.Join(home, "Android", "Sdk"))
		}
	}
	return dirs
}
