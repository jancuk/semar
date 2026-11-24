package mobiletest

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// ADBConfig says what the fake adb answers.
type ADBConfig struct {
	// Devices are the lines of adb devices -l, such as "emulator-5554 device product:sdk model:Pixel".
	Devices []string
	// Port is what adb forward prints: the port of the fake server.
	Port int
	// Installed gives the path of the APK of each package that is on the device, as pm path prints it.
	Installed map[string]string
	// DeviceSHA gives the sha256 that the device reports for the file at a path.
	DeviceSHA map[string]string
	// InstallSeq gives, for the base name of an APK, what each adb install answers in turn: "rc|text".
	// After the list is used up, an install succeeds.
	InstallSeq map[string][]string
	// InstrumentExits makes am instrument end at once, as a server that crashes does.
	InstrumentExits bool
	// NotBooted makes sys.boot_completed say 0.
	NotBooted bool
	// StartOutput is what am start prints. The default is a success.
	StartOutput string
	// GrantOutput is what pm grant prints.
	GrantOutput string
	// Dumpsys is what dumpsys input_method prints.
	Dumpsys string
	// SDK is what getprop ro.build.version.sdk prints. The default is 34.
	SDK string
	// Canned are replies to shell commands, tried in order before the built-in ones.
	Canned []Canned
}

// Canned is a reply to the shell commands whose quoted text holds Match, such as "'wm' 'size'" or
// "'dumpsys' 'dropbox' '--print' 'data_app_crash'".
type Canned struct {
	Match string
	// Out is written to stdout, and Err to stderr.
	Out, Err string
	// RC is the exit status.
	RC int
	// Hold makes the command keep running after it wrote Out, as logcat -T does, until it is killed.
	Hold bool
}

// ADB is a fake adb: a shell script in a directory of its own, outside any repository, that logs what it
// is asked and answers from files.
type ADB struct {
	// Path is the program, for SEMAR_ADB_BIN.
	Path string

	t   testing.TB
	dir string
	mu  sync.Mutex
	// canned is how many canned replies were added.
	canned int
}

const adbScript = `#!/bin/sh
D="$(dirname "$0")/state"
line=""
for a in "$@"; do line="$line$a	"; done
printf '%s\n' "$line" >> "$D/log"
if [ "$1" = "-s" ]; then shift 2; fi
cmd="$1"
[ $# -gt 0 ] && shift
case "$cmd" in
  start-server) exit 0 ;;
  devices)
    echo "List of devices attached"
    cat "$D/devices"
    echo
    exit 0 ;;
  forward)
    if [ "$1" = "--remove" ]; then exit 0; fi
    cat "$D/port"
    exit 0 ;;
  install)
    for last; do :; done
    f="$D/install.$(basename "$last")"
    if [ -s "$f" ]; then
      line="$(head -n 1 "$f")"
      sed '1d' "$f" > "$f.tmp"
      mv "$f.tmp" "$f"
      rc="${line%%|*}"
      out="${line#*|}"
      if [ "$rc" != 0 ]; then printf '%s\n' "$out" >&2; exit "$rc"; fi
      printf '%s\n' "$out"
      exit 0
    fi
    echo "Performing Streamed Install"
    echo "Success"
    exit 0 ;;
  uninstall) echo Success; exit 0 ;;
  emu) exit 0 ;;
  pull)
    for last; do :; done
    printf 'FAKE-MP4' > "$last"
    echo "1 file pulled"
    exit 0 ;;
  shell)
    c="$1"
    for kf in "$D"/canned.*.key; do
      [ -f "$kf" ] || continue
      key="$(cat "$kf")"
      case "$c" in
        *"$key"*)
          base="${kf%.key}"
          [ -f "$base.out" ] && cat "$base.out"
          [ -f "$base.err" ] && cat "$base.err" >&2
          if [ -f "$base.hold" ]; then exec sleep 120; fi
          rc=0
          [ -f "$base.rc" ] && rc="$(cat "$base.rc")"
          exit "$rc" ;;
      esac
    done
    case "$c" in
      *"'getprop' 'ro.build.version.sdk'"*) if [ -f "$D/sdk" ]; then cat "$D/sdk"; else echo 34; fi ;;
      *"'screenrecord' "*)
        rm -f "$D/pkill"
        touch "$D/recording"
        while [ ! -f "$D/pkill" ]; do sleep 0.02; done
        rm -f "$D/recording"
        echo "Recording finished" ;;
      *"'pkill' "*) touch "$D/pkill" ;;
      *"'getprop' 'sys.boot_completed'"*)
        if [ -f "$D/notbooted" ]; then echo 0; else echo 1; fi ;;
      *"'getprop' 'ro.build.version.release'"*) echo 14 ;;
      *"'pm' 'path' "*)
        pkg=$(printf '%s' "$c" | sed "s/.*'pm' 'path' '\([^']*\)'.*/\1/")
        if [ -s "$D/pm.$pkg" ]; then cat "$D/pm.$pkg"; else exit 1; fi ;;
      *"'sha256sum' "*)
        p=$(printf '%s' "$c" | sed "s/.*'sha256sum' '\([^']*\)'.*/\1/")
        f="$D/sha.$(printf '%s' "$p" | tr '/' '_')"
        if [ -f "$f" ]; then printf '%s  %s\n' "$(cat "$f")" "$p"; else echo "sha256sum: $p: No such file" >&2; exit 1; fi ;;
      *"'am' 'instrument'"*)
        if [ -f "$D/instrument_exits" ]; then echo "INSTRUMENTATION_RESULT: shortMsg=Process crashed."; exit 1; fi
        exec sleep 120 ;;
      *"'resolve-activity'"*)
        echo "priority=0 preferredOrder=0 match=0x108000 specificIndex=-1 isDefault=true"
        echo "com.example.shop/.MainActivity" ;;
      *"'dumpsys' 'input_method'"*) cat "$D/dumpsys" 2>/dev/null ;;
      *"'pm' 'clear'"*) echo Success ;;
      *"'pm' 'grant'"*) cat "$D/grant" 2>/dev/null ;;
      *"'am' 'start'"*)
        if [ -s "$D/start" ]; then cat "$D/start"; else echo "Starting: Intent { }"; echo "Status: ok"; fi ;;
    esac
    exit 0 ;;
  *) echo "adb: unknown command" >&2; exit 1 ;;
esac
`

// NewADB writes the fake adb. The test is skipped on Windows, which has no sh.
func NewADB(t testing.TB, cfg ADBConfig) *ADB {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake adb is a shell script")
	}
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	if err := os.Mkdir(state, 0o755); err != nil {
		t.Fatal(err)
	}
	a := &ADB{Path: filepath.Join(dir, "adb"), t: t, dir: state}
	if err := os.WriteFile(a.Path, []byte(adbScript), 0o755); err != nil {
		t.Fatal(err)
	}
	a.write("devices", strings.Join(cfg.Devices, "\n"))
	a.write("port", fmt.Sprint(cfg.Port))
	for pkg, path := range cfg.Installed {
		a.write("pm."+pkg, "package:"+path)
	}
	for path, sum := range cfg.DeviceSHA {
		a.write("sha."+strings.ReplaceAll(path, "/", "_"), sum)
	}
	for base, seq := range cfg.InstallSeq {
		a.write("install."+base, strings.Join(seq, "\n")+"\n")
	}
	if cfg.InstrumentExits {
		a.write("instrument_exits", "1")
	}
	if cfg.NotBooted {
		a.write("notbooted", "1")
	}
	if cfg.StartOutput != "" {
		a.write("start", cfg.StartOutput)
	}
	if cfg.GrantOutput != "" {
		a.write("grant", cfg.GrantOutput)
	}
	if cfg.Dumpsys != "" {
		a.write("dumpsys", cfg.Dumpsys)
	}
	if cfg.SDK != "" {
		a.write("sdk", cfg.SDK)
	}
	for _, c := range cfg.Canned {
		a.Can(c)
	}
	return a
}

// Can adds a canned reply after the fake was made. A reply that was added first is tried first.
func (a *ADB) Can(c Canned) {
	a.mu.Lock()
	n := a.canned
	a.canned++
	a.mu.Unlock()
	base := fmt.Sprintf("canned.%04d", n)
	a.write(base+".out", c.Out)
	if c.Err != "" {
		a.write(base+".err", c.Err)
	}
	if c.RC != 0 {
		a.write(base+".rc", fmt.Sprint(c.RC))
	}
	if c.Hold {
		a.write(base+".hold", "1")
	}
	// The key is written last: the script looks for keys, and finds a reply only when it is whole.
	a.write(base+".key", c.Match)
}

// Recording reports whether a fake screenrecord is running.
func (a *ADB) Recording() bool {
	_, err := os.Stat(filepath.Join(a.dir, "recording"))
	return err == nil
}

func (a *ADB) write(name, content string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := os.WriteFile(filepath.Join(a.dir, name), []byte(content), 0o644); err != nil {
		a.t.Fatal(err)
	}
}

// Set changes one answer after the fake was made: name is dumpsys, start, grant, or notbooted.
func (a *ADB) Set(name, content string) { a.write(name, content) }

// Unset removes an answer.
func (a *ADB) Unset(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	_ = os.Remove(filepath.Join(a.dir, name))
}

// Calls gives every call so far: the arguments of each, with the -s serial included.
func (a *ADB) Calls() [][]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	data, err := os.ReadFile(filepath.Join(a.dir, "log"))
	if err != nil {
		return nil
	}
	var out [][]string
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		out = append(out, strings.Split(strings.TrimSuffix(line, "\t"), "\t"))
	}
	return out
}

// Log is the calls as lines, with the -s serial left out, for a test that compares an order.
func (a *ADB) Log() []string {
	var out []string
	for _, c := range a.Calls() {
		if len(c) >= 2 && c[0] == "-s" {
			c = c[2:]
		}
		out = append(out, strings.Join(c, " "))
	}
	return out
}

// Shell gives the commands that were sent to the shell of the device, each as it was sent: every word
// in single quotes.
func (a *ADB) Shell() []string {
	var out []string
	for _, c := range a.Calls() {
		if len(c) >= 2 && c[0] == "-s" {
			c = c[2:]
		}
		if len(c) == 2 && c[0] == "shell" {
			out = append(out, c[1])
		}
	}
	return out
}

// Count is how many calls start with the words.
func (a *ADB) Count(words ...string) int {
	n := 0
	for _, l := range a.Log() {
		if l == strings.Join(words, " ") || strings.HasPrefix(l, strings.Join(words, " ")+" ") {
			n++
		}
	}
	return n
}

// Contains reports whether any call is at least the words, in order.
func (a *ADB) Contains(words ...string) bool { return a.Count(words...) > 0 }

// QuotedWords splits a command that was sent to the shell of the device into its words. It gives false
// unless every word is in single quotes, as shellCommand writes it, with a quote inside a word written
// as '\”.
func QuotedWords(cmd string) ([]string, bool) {
	var words []string
	i := 0
	for i < len(cmd) {
		if cmd[i] != '\'' {
			return nil, false
		}
		var w strings.Builder
		for {
			i++ // past the opening quote
			j := strings.IndexByte(cmd[i:], '\'')
			if j < 0 {
				return nil, false
			}
			w.WriteString(cmd[i : i+j])
			i += j + 1
			if strings.HasPrefix(cmd[i:], `\''`) { // '\'' is a quote in the word
				w.WriteByte('\'')
				i += 2
				continue
			}
			break
		}
		words = append(words, w.String())
		switch {
		case i == len(cmd):
		case cmd[i] == ' ':
			i++
			if i == len(cmd) {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	return words, len(words) > 0
}

// AssertQuoted fails the test for every command sent to the shell of the device that has a word that is
// not in single quotes.
func (a *ADB) AssertQuoted(t testing.TB) {
	t.Helper()
	for _, c := range a.Shell() {
		if _, ok := QuotedWords(c); !ok {
			t.Errorf("a shell command is not made of quoted words: %s", c)
		}
	}
}

// ShellWords gives the shell commands as their words, for a test that checks one word.
func (a *ADB) ShellWords() [][]string {
	var out [][]string
	for _, c := range a.Shell() {
		if w, ok := QuotedWords(c); ok {
			out = append(out, w)
		}
	}
	return out
}
