package android_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/mobiletest"
)

// deviceDate is what date "+%s%3N %z" prints on a device whose wall clock reads 2026-11-23 10:15:00 in
// +0700. The crashes of the test data are on that wall clock, and the rig clock is far from it.
func deviceDate() string {
	wall := time.Date(2026, 11, 23, 10, 15, 0, 0, time.UTC)
	return strconv.FormatInt(wall.Add(-7*time.Hour).UnixMilli(), 10) + " +0700\n"
}

func diagRig(t *testing.T, extra ...mobiletest.Canned) *ctlRig {
	return newCtlRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) {
		cfg.Canned = append([]mobiletest.Canned{
			{Match: "'--print' 'data_app_crash'", Out: testdata(t, "dropbox_crash.txt")},
			{Match: "'--print' 'data_app_anr'", Out: testdata(t, "dropbox_anr.txt")},
			{Match: "'--print' 'data_app_native_crash'", RC: 1, Err: "Can't find service: dropbox"},
			{Match: "'logcat' '-b' 'crash'", Out: testdata(t, "crash_buffer.txt")},
			{Match: "'date' '+%s%3N %z'", Out: deviceDate()},
		}, extra...)
	})
}

func TestPlatformCrashesSinceTheRunStart(t *testing.T) {
	r := diagRig(t)
	p, _ := r.start(t)
	var _ mobile.Diagnoser = p
	ctx := context.Background()
	start := r.clk.Now()

	got, err := p.Crashes(ctx, pkgShop, start)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("%d crashes: %+v", len(got), got)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Time.Before(got[i-1].Time) {
			t.Error("not oldest first")
		}
	}
	if got[0].Summary != "java.lang.IllegalStateException: Cart is empty" || got[0].Process != pkgShop || !strings.Contains(got[0].Body, "CartActivity.checkout") {
		t.Errorf("first = %+v", got[0])
	}
	// The clock of the device is read once, however many times crashes are asked for.
	if _, err := p.Crashes(ctx, pkgShop, start); err != nil {
		t.Fatal(err)
	}
	if n := countShell(r, "date"); n != 1 {
		t.Errorf("the device clock was read %d times", n)
	}

	// A run that started after the first crash does not see it.
	later, err := p.Crashes(ctx, pkgShop, start.Add(6*time.Minute))
	if err != nil || len(later) != 1 || !strings.HasPrefix(later[0].Summary, "Input dispatching timed out") {
		t.Errorf("later = %+v, %v", later, err)
	}
	none, err := p.Crashes(ctx, pkgShop, start.Add(24*time.Hour))
	if err != nil || len(none) != 0 {
		t.Errorf("none = %+v, %v", none, err)
	}
	other, err := p.Crashes(ctx, "com.nobody.here", start)
	if err != nil || len(other) != 0 {
		t.Errorf("other = %+v, %v", other, err)
	}
	r.adb.AssertQuoted(t)
}

func TestPlatformCrashesWithAnUnreadableClock(t *testing.T) {
	r2 := newCtlRig(t, func(_ *rig, cfg *mobiletest.ADBConfig) {
		cfg.Canned = []mobiletest.Canned{
			{Match: "'--print' 'data_app_crash'", Out: testdata(t, "dropbox_crash.txt")},
			{Match: "'date' ", RC: 1, Err: "date: bad"},
		}
	})
	p, _ := r2.start(t)
	// With no offset the host time is used as it is: the rig clock is 2026-01-01, before every crash.
	got, err := p.Crashes(context.Background(), pkgShop, r2.clk.Now())
	if err != nil || len(got) != 2 {
		t.Errorf("got %d crashes, %v", len(got), err)
	}
}

func TestPlatformLogTail(t *testing.T) {
	line := func(pid int, tag, msg string) string {
		return "11-23 10:15:30.120  " + strconv.Itoa(pid) + "  " + strconv.Itoa(pid) + " I " + tag + ": " + msg + "\n"
	}
	r := diagRig(t,
		mobiletest.Canned{Match: "'pidof' 'com.example.shop'", Out: "4321 9000\n"},
		mobiletest.Canned{Match: "'--pid' '4321'", Out: line(4321, "Shop", "one") + line(4321, "Shop", "two")},
	)
	p, _ := r.start(t)
	out, err := p.LogTail(context.Background(), pkgShop, 200)
	if err != nil || !strings.Contains(out, "one") || !strings.Contains(out, "two") {
		t.Errorf("tail = %q, %v", out, err)
	}
	if !shellHas(r, "logcat", "-d", "-v", "threadtime", "--pid", "4321", "-t", "200") {
		t.Errorf("no logcat of the pid: %v", r.adb.Shell())
	}
	if _, err := p.LogTail(context.Background(), "bad pkg; reboot", 10); err == nil {
		t.Error("a package that is not a name was accepted")
	}
	r.adb.AssertQuoted(t)
}

func TestPlatformLogTailOfAnAppThatIsGone(t *testing.T) {
	line := func(pid int, tag, msg string) string {
		return "11-23 10:15:30.120  " + strconv.Itoa(pid) + "  " + strconv.Itoa(pid) + " I " + tag + ": " + msg + "\n"
	}
	all := line(1, "Other", "noise") + line(2, "ActivityManager", "Process "+pkgShop+" (pid 4321) has died") + line(3, "Other", "more noise") + line(4, pkgShop, "last words")
	r := diagRig(t,
		mobiletest.Canned{Match: "'pidof' ", RC: 1},
		mobiletest.Canned{Match: "'logcat' '-d' '-v' 'threadtime' '-t' '3000'", Out: all},
	)
	p, _ := r.start(t)
	out, err := p.LogTail(context.Background(), pkgShop, 1)
	if err != nil || strings.Contains(out, "noise") || !strings.Contains(out, "last words") || strings.Contains(out, "has died") {
		t.Errorf("tail = %q, %v", out, err)
	}
	out, err = p.LogTail(context.Background(), pkgShop, 200)
	if err != nil || !strings.Contains(out, "has died") || strings.Contains(out, "noise") {
		t.Errorf("tail = %q, %v", out, err)
	}
}

func TestPlatformForeground(t *testing.T) {
	r := diagRig(t, mobiletest.Canned{Match: "'dumpsys' 'activity' 'activities'", Out: "  topResumedActivity=ActivityRecord{1 u0 com.example.shop/.MainActivity t1}\n"})
	p, _ := r.start(t)
	var _ mobile.Foregrounder = p
	pkg, err := p.Foreground(context.Background())
	if err != nil || pkg != pkgShop {
		t.Errorf("Foreground = %q, %v", pkg, err)
	}
	l := diagRig(t, mobiletest.Canned{Match: "'dumpsys' 'activity' 'activities'", Out: "  topResumedActivity=ActivityRecord{1 u0 com.google.android.apps.nexuslauncher/.NexusLauncherActivity t1}\n"})
	lp, _ := l.start(t)
	if pkg, err := lp.Foreground(context.Background()); err != nil || pkg != "" {
		t.Errorf("launcher: Foreground = %q, %v", pkg, err)
	}
}
