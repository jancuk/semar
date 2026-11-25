package fixture

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func openLeases(t *testing.T) *Leases {
	t.Helper()
	l, err := OpenLeases(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestLeaseExcludesSecondHolder(t *testing.T) {
	l := openLeases(t)
	release, ok, err := l.TryLock("uat", "order.paid", "o-1")
	if err != nil || !ok {
		t.Fatalf("first TryLock = %v, %v", ok, err)
	}

	// Each goroutine opens the file on its own, as a parallel scenario of the same run does.
	const n = 8
	var wg sync.WaitGroup
	got := make([]bool, n)
	for i := range n {
		wg.Go(func() {
			r, ok, err := l.TryLock("uat", "order.paid", "o-1")
			if err != nil {
				t.Error(err)
				return
			}
			if ok {
				r()
			}
			got[i] = ok
		})
	}
	wg.Wait()
	for i, ok := range got {
		if ok {
			t.Errorf("goroutine %d took a lease that is held", i)
		}
	}

	if r, ok, err := l.TryLock("uat", "order.paid", "o-2"); err != nil || !ok {
		t.Fatalf("another record is not held: %v, %v", ok, err)
	} else {
		r()
	}
	if r, ok, err := l.TryLock("local", "order.paid", "o-1"); err != nil || !ok {
		t.Fatalf("another environment is not held: %v, %v", ok, err)
	} else {
		r()
	}

	release()
	release() // safe twice
	r, ok, err := l.TryLock("uat", "order.paid", "o-1")
	if err != nil || !ok {
		t.Fatalf("TryLock after release = %v, %v", ok, err)
	}
	r()
}

func TestLeaseTellsWhoHoldsIt(t *testing.T) {
	l := openLeases(t)
	release, ok, holder, err := l.TryLockHolder("android", "device", "emulator-5554")
	if err != nil || !ok || holder != nil {
		t.Fatalf("first TryLockHolder = %v, %v, %v", ok, holder, err)
	}
	defer release()
	r, ok, holder, err := l.TryLockHolder("android", "device", "emulator-5554")
	if err != nil || ok {
		if ok {
			r()
		}
		t.Fatalf("second TryLockHolder = %v, %v", ok, err)
	}
	if holder == nil || holder.PID != os.Getpid() || holder.Command != commandOf(os.Args) || time.Since(holder.Since) > time.Minute || time.Since(holder.Since) < 0 {
		t.Errorf("holder = %+v", holder)
	}
	// A holder that left the lease file with other content is no holder.
	f, err := os.OpenFile(filepath.Join(l.dir, leaseName("android", "device", "emulator-5554")), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("not json"), holderOffset); err != nil {
		t.Fatal(err)
	}
	if _, ok, holder, err := l.TryLockHolder("android", "device", "emulator-5554"); err != nil || ok || holder != nil {
		t.Errorf("a lease file with no holder: %v, %v, %v", ok, holder, err)
	}
}

func TestCommandOfNamesTheSubcommandOnly(t *testing.T) {
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"no arguments":        {[]string{"/usr/bin/semar"}, "semar"},
		"a subcommand":        {[]string{"/usr/bin/semar", "run"}, "semar run"},
		"the ui of a record":  {[]string{"semar", "record", "--ui"}, "semar record --ui"},
		"values are left out": {[]string{"semar", "run", "--auth-token", "s3cret", "--ui", "AND-1"}, "semar run --ui"},
		"a flag first":        {[]string{"semar", "--ui", "record"}, "semar --ui"},
		"the test binary":     {[]string{"/tmp/fixture.test", "-test.run=X"}, "semar"},
		"nothing":             {nil, "semar"},
	} {
		if got := commandOf(c.args); got != c.want {
			t.Errorf("%s: commandOf(%q) = %q, want %q", name, c.args, got, c.want)
		}
	}
}

func TestLeaseFileNameAndMode(t *testing.T) {
	home := t.TempDir()
	l, err := OpenLeases(home)
	if err != nil {
		t.Fatal(err)
	}
	release, ok, err := l.TryLock("uat", "order.paid", "../../etc/passwd")
	if err != nil || !ok {
		t.Fatalf("TryLock = %v, %v", ok, err)
	}
	defer release()
	entries, err := os.ReadDir(filepath.Join(home, "leases"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("leases holds %d files", len(entries))
	}
	name := entries[0].Name()
	if len(name) != 64+len(".lock") || !strings.HasSuffix(name, ".lock") {
		t.Errorf("file name %q is not a hex SHA-256 with .lock", name)
	}
	info, err := entries[0].Info()
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 && !isWindows() {
		t.Errorf("lease file mode %v, want 0600", mode)
	}
	dir, err := os.Stat(filepath.Join(home, "leases"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := dir.Mode().Perm(); mode&0o077 != 0 && !isWindows() {
		t.Errorf("leases directory mode %v, want 0700", mode)
	}
}

func TestLeasesDirRefusesLink(t *testing.T) {
	home := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(home, "leases")); err != nil {
		t.Skip("no symbolic links:", err)
	}
	_, err := OpenLeases(home)
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("err = %v", err)
	}

	file := t.TempDir()
	if err := os.WriteFile(filepath.Join(file, "leases"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenLeases(file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("a file at leases: err = %v", err)
	}
}

const helperEnv = "SEMAR_LEASE_HELPER_HOME"

// TestLeaseHelperProcess is not a test: TestLeaseHeldByAnotherProcess starts the test binary
// with it, so that a second process holds the lease until it is killed.
func TestLeaseHelperProcess(t *testing.T) {
	home := os.Getenv(helperEnv)
	if home == "" {
		t.Skip("only run as a helper process")
	}
	l, err := OpenLeases(home)
	if err != nil {
		os.Exit(3)
	}
	if _, ok, err := l.TryLock("uat", "order.paid", "o-1"); err != nil || !ok {
		os.Exit(4)
	}
	os.Stdout.WriteString("locked\n")
	select {} // held until the parent kills the process
}

func TestLeaseHeldByAnotherProcess(t *testing.T) {
	home := t.TempDir()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestLeaseHelperProcess$")
	cmd.Env = append(os.Environ(), helperEnv+"="+home)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line != "locked\n" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("helper said %q: %v", line, err)
	}

	l, err := OpenLeases(home)
	if err != nil {
		t.Fatal(err)
	}
	r, ok, holder, err := l.TryLockHolder("uat", "order.paid", "o-1")
	if err != nil || ok {
		if ok {
			r()
		}
		t.Fatalf("TryLock while another process holds it = %v, %v", ok, err)
	}
	if holder == nil || holder.PID != cmd.Process.Pid {
		t.Errorf("holder = %+v, want pid %d", holder, cmd.Process.Pid)
	}

	// A crash releases the lock: the operating system drops it with the process.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	r, ok, err = l.TryLock("uat", "order.paid", "o-1")
	if err != nil || !ok {
		t.Fatalf("TryLock after the holder died = %v, %v", ok, err)
	}
	r()
}

func isWindows() bool { return runtime.GOOS == "windows" }
