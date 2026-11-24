// Package android runs the steps of an app on an Android device. It starts the UiAutomator2 server of
// Appium on the device with adb, or talks to a W3C WebDriver server at serverUrl, and it gives the mobile
// engine a Platform. adb is used only to prepare, reset, launch, and clean up: everything that polls goes
// over HTTP.
package android

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
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
	// maxStreamLine and maxStreamTotal bound what a Stream gives: a line of a log is cut at 16 KiB, and
	// the stream ends after 4 MiB.
	maxStreamLine  = 16 << 10
	maxStreamTotal = 4 << 20
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
	// Stream starts adb and gives its stdout as it is written, for a command that runs on, such as
	// logcat. Reads are bounded: a line is cut at 16 KiB, and the reader ends after 4 MiB. Closing the
	// reader stops the child. wait gives the end of the child, and it is nil for a child that Close or
	// the context stopped.
	Stream(ctx context.Context, adb string, args ...string) (rc io.ReadCloser, wait func() error, err error)
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

func (ExecRunner) Stream(ctx context.Context, adb string, args ...string) (io.ReadCloser, func() error, error) {
	sctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(sctx, adb, args...)
	cmd.WaitDelay = adbWaitDelay
	var errOut limitedBuffer
	errOut.max = 8 << 10
	cmd.Stderr = &errOut
	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, nil, commandError(args, err, "", "")
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, nil, commandError(args, err, "", "")
	}
	st := &stream{br: bufio.NewReaderSize(out, maxStreamLine), cancel: cancel}
	var once sync.Once
	var werr error
	var asked bool
	wait := func() error {
		once.Do(func() {
			werr = cmd.Wait()
			// Whether the child was stopped on purpose is known before cancel makes the context done.
			asked = st.stopped.Load() || sctx.Err() != nil
			cancel()
		})
		if werr == nil || asked {
			return nil
		}
		return commandError(args, werr, errOut.String(), "")
	}
	return st, wait, nil
}

// stream is the bounded reader of Stream: it hands out whole lines, cuts a line that is longer than
// maxStreamLine, and ends after maxStreamTotal bytes.
type stream struct {
	br      *bufio.Reader
	cancel  context.CancelFunc
	stopped atomic.Bool
	pending []byte
	total   int
}

func (s *stream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if s.total >= maxStreamTotal {
		return 0, io.EOF
	}
	for len(s.pending) == 0 {
		line, err := s.br.ReadSlice('\n')
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			// A line over the cap: keep its start, and drop the rest of it.
			s.pending = append(bytes.Clone(line), '\n')
			for {
				_, e := s.br.ReadSlice('\n')
				if !errors.Is(e, bufio.ErrBufferFull) {
					break
				}
			}
		case len(line) > 0:
			s.pending = bytes.Clone(line)
		case err != nil:
			return 0, err
		}
	}
	n := copy(p, s.pending)
	if room := maxStreamTotal - s.total; n > room {
		n = room
	}
	s.pending = s.pending[n:]
	s.total += n
	return n, nil
}

func (s *stream) Close() error {
	s.stopped.Store(true)
	s.cancel()
	return nil
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

// The places adb is found, as FindADBWhere names them.
const (
	ADBFromEnv         = "SEMAR_ADB_BIN"
	ADBFromPath        = "PATH"
	ADBFromAndroidHome = "ANDROID_HOME"
	ADBFromSDKRoot     = "ANDROID_SDK_ROOT"
	ADBFromSDKDefault  = "sdk-default"
	ADBFromHomebrew    = "homebrew"
)

// homebrewADB is where Homebrew puts adb (the android-platform-tools cask) on Apple silicon and on Intel.
// isProgram follows the link that Homebrew makes.
var homebrewADB = []string{"/opt/homebrew/bin/adb", "/usr/local/bin/adb"}

// FindADB finds the adb program. See FindADBWhere for the order.
func FindADB(lookup func(string) (string, bool), repoRoot string) (string, error) {
	path, _, err := FindADBWhere(lookup, repoRoot)
	return path, err
}

// FindADBWhere finds the adb program and says where it was found. The repository under test cannot choose
// it, so there is no key for it in the configuration. The order is SEMAR_ADB_BIN (an absolute path),
// PATH, $ANDROID_HOME, $ANDROID_SDK_ROOT, the place where Android Studio installs the SDK (sdk-default),
// and on macOS the two places of Homebrew. The SDK places also serve a program that was started by a
// window and not by a shell, whose PATH has none of the shell setup. A program inside the repository
// under test is refused. source is one of SEMAR_ADB_BIN, PATH, ANDROID_HOME, ANDROID_SDK_ROOT,
// sdk-default, and homebrew.
func FindADBWhere(lookup func(string) (string, bool), repoRoot string) (path, source string, err error) {
	return findADB(lookup, repoRoot, runtime.GOOS)
}

// findADB is FindADBWhere for the operating system goos, so that a test can try each one.
func findADB(lookup func(string) (string, bool), repoRoot, goos string) (path, source string, err error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	fail := func(e error) (string, string, error) { return "", "", e }
	found := func(bin, from string) (string, string, error) {
		bin, err := refuseInside(bin, repoRoot)
		if err != nil {
			return fail(err)
		}
		return bin, from, nil
	}
	if p, ok := lookup("SEMAR_ADB_BIN"); ok && p != "" {
		bin, perr := provider.FindBinary(lookup, "SEMAR_ADB_BIN", "adb")
		if perr != nil {
			return fail(perr)
		}
		return found(bin, ADBFromEnv)
	}
	name := "adb"
	if goos == "windows" {
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
				return found(candidate, ADBFromPath)
			}
		}
	}
	for _, d := range sdkDirs(lookup, goos) {
		if candidate := filepath.Join(d.dir, "platform-tools", name); isProgram(candidate) {
			return found(candidate, d.source)
		}
	}
	if goos == "darwin" {
		for _, candidate := range homebrewADB {
			if isProgram(candidate) {
				return found(candidate, ADBFromHomebrew)
			}
		}
	}
	return fail(errors.New("adb was not found: install the Android SDK platform tools, then put adb on PATH, set ANDROID_HOME, or set SEMAR_ADB_BIN to its absolute path"))
}

// isProgram reports whether a path is a regular file that can be run. A link is followed.
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

type sdkDir struct{ dir, source string }

// sdkDirs lists where the Android SDK may be, from the environment and from the default of Android Studio.
func sdkDirs(lookup func(string) (string, bool), goos string) []sdkDir {
	var dirs []sdkDir
	for _, v := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if p, ok := lookup(v); ok && p != "" && filepath.IsAbs(p) {
			dirs = append(dirs, sdkDir{p, v})
		}
	}
	home, _ := lookup("HOME")
	switch goos {
	case "darwin":
		if home != "" {
			dirs = append(dirs, sdkDir{filepath.Join(home, "Library", "Android", "sdk"), ADBFromSDKDefault})
		}
	case "windows":
		if p, ok := lookup("LOCALAPPDATA"); ok && p != "" {
			dirs = append(dirs, sdkDir{filepath.Join(p, "Android", "Sdk"), ADBFromSDKDefault})
		}
	default:
		if home != "" {
			dirs = append(dirs, sdkDir{filepath.Join(home, "Android", "Sdk"), ADBFromSDKDefault})
		}
	}
	return dirs
}

// ADBVersion runs adb version and gives the version of the platform tools, such as 36.0.0. Two adb
// programs of different versions kill the server of each other, so a person needs to see which one
// Semar uses.
func ADBVersion(ctx context.Context, adb string) (string, error) {
	return adbVersion(ctx, ExecRunner{}, adb)
}

func adbVersion(ctx context.Context, run Runner, adb string) (string, error) {
	out, err := run.Output(ctx, adb, "version")
	if err != nil {
		return "", err
	}
	if v := ParseADBVersion(string(out)); v != "" {
		return v, nil
	}
	return "", errors.New("adb version gave no version")
}

// ParseADBVersion reads the version of the platform tools from the output of adb version: the line
// "Version 36.0.0-13206524" gives 36.0.0. Without that line it gives the version of the protocol, such as
// 1.0.41, and "" when there is neither.
func ParseADBVersion(out string) string {
	proto := ""
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if v, ok := strings.CutPrefix(l, "Version "); ok {
			if i := strings.IndexAny(v, "- \t"); i >= 0 {
				v = v[:i]
			}
			if versionPattern.MatchString(v) {
				return v
			}
		}
		if v, ok := strings.CutPrefix(l, "Android Debug Bridge version "); ok && versionPattern.MatchString(v) {
			proto = v
		}
	}
	return proto
}

var versionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+){1,3}$`)
