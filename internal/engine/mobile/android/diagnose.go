package android

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/engine/mobile"
)

var (
	_ mobile.Diagnoser    = (*Platform)(nil)
	_ mobile.Foregrounder = (*Platform)(nil)
)

const (
	// maxDiagnosed is how many crashes Crashes reads in full.
	maxDiagnosed = 5
	// tailFallbackScan is how many lines of the log LogTail scans when the app has no process.
	tailFallbackScan = 3000
	maxTailLines     = 1000
)

// clockOffsets holds, for each platform, how far the clock of its device is from the clock of the host.
// It is read once, with the first call that needs it.
var clockOffsets sync.Map // *Platform -> *clockOffset

type clockOffset struct {
	once sync.Once
	d    time.Duration
}

// deviceOffset is the time to add to a time of the host to get the same moment on the clock that the
// crashes use: the clock of the device read as UTC, which is the wall time of the device and so includes
// its time zone. A device that cannot be read is taken to agree with the host.
func (p *Platform) deviceOffset(ctx context.Context, a *adb) time.Duration {
	v, _ := clockOffsets.LoadOrStore(p, &clockOffset{})
	o := v.(*clockOffset)
	o.once.Do(func() {
		before := p.o.Now()
		out, err := a.shell(ctx, "date", "+%s%3N %z")
		if err != nil {
			return
		}
		o.d = parseOffset(out, before, p.o.Now())
	})
	return o.d
}

// parseOffset reads the output of date "+%s%3N %z", such as "1763890530123 +0700", taken between two
// times of the host.
func parseOffset(out string, before, after time.Time) time.Duration {
	f := strings.Fields(out)
	if len(f) == 0 {
		return 0
	}
	ms, err := strconv.ParseInt(f[0], 10, 64)
	if err != nil || ms <= 0 {
		return 0
	}
	var zone time.Duration
	if len(f) > 1 {
		zone = parseZone(f[1])
	}
	mid := before.Add(after.Sub(before) / 2)
	return time.UnixMilli(ms).Add(zone).Sub(mid)
}

// parseZone reads +0700 or -0330.
func parseZone(s string) time.Duration {
	if len(s) != 5 || (s[0] != '+' && s[0] != '-') {
		return 0
	}
	h, err1 := strconv.Atoi(s[1:3])
	m, err2 := strconv.Atoi(s[3:5])
	if err1 != nil || err2 != nil {
		return 0
	}
	d := time.Duration(h)*time.Hour + time.Duration(m)*time.Minute
	if s[0] == '-' {
		d = -d
	}
	return d
}

// Crashes lists the crashes and ANRs of the app that the device recorded since a time of the host, the
// oldest first, with their reports. It is for a device with adb: a server at serverUrl gives an error.
func (p *Platform) Crashes(ctx context.Context, pkg string, since time.Time) ([]mobile.Crash, error) {
	c, err := p.Controller(ctx)
	if err != nil {
		return nil, err
	}
	a, err := c.adb()
	if err != nil {
		return nil, err
	}
	list, err := c.ListCrashes(ctx, pkg, maxCrashes)
	if err != nil {
		return nil, err
	}
	// A clock that reads before the crash by a second still shows it: the device and the host never
	// agree to the millisecond.
	floor := since.Add(p.deviceOffset(ctx, a)).Add(-time.Second)
	var fresh []Crash
	for _, cr := range list {
		if !cr.Time.Before(floor) {
			fresh = append(fresh, cr)
		}
	}
	slices.SortStableFunc(fresh, func(x, y Crash) int { return x.Time.Compare(y.Time) })
	if len(fresh) > maxDiagnosed {
		fresh = fresh[:maxDiagnosed]
	}
	out := make([]mobile.Crash, 0, len(fresh))
	for _, cr := range fresh {
		full, err := c.GetCrash(ctx, cr.ID)
		if err != nil {
			full = cr
		}
		out = append(out, mobile.Crash{ID: full.ID, Process: full.Process, Summary: full.Summary, Body: full.Body, Time: full.Time})
	}
	return out, nil
}

// LogTail gives the last lines of the log of the app. With a process it is the lines of that process.
// An app that crashed has none, so the lines of the recent log that name the package, or the process, are
// given instead.
func (p *Platform) LogTail(ctx context.Context, pkg string, lines int) (string, error) {
	if !config.ValidAndroidPackage(pkg) {
		return "", fmt.Errorf("%q is not a package name", truncate(pkg, 80))
	}
	if lines <= 0 {
		lines = 200
	}
	lines = min(lines, maxTailLines)
	c, err := p.Controller(ctx)
	if err != nil {
		return "", err
	}
	a, err := c.adb()
	if err != nil {
		return "", err
	}
	pids, err := c.pidsOf(ctx, a, pkg)
	if err != nil {
		return "", err
	}
	if len(pids) > 0 {
		pid := slices.Min(slices.Collect(mapKeys(pids)))
		out, err := a.shell(ctx, "logcat", "-d", "-v", "threadtime", "--pid", strconv.Itoa(pid), "-t", strconv.Itoa(lines))
		if err != nil && strings.TrimSpace(out) == "" {
			return "", fmt.Errorf("the log of the app could not be read: %w", err)
		}
		return tailLines(out, lines), nil
	}
	out, err := a.shell(ctx, "logcat", "-d", "-v", "threadtime", "-t", strconv.Itoa(tailFallbackScan))
	if err != nil && strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("the log could not be read: %w", err)
	}
	var kept []string
	for _, l := range strings.Split(out, "\n") {
		e, ok := ParseLogcatLine(l)
		if ok && (strings.Contains(e.Message, pkg) || strings.Contains(e.Tag, pkg)) {
			kept = append(kept, strings.TrimRight(l, "\r"))
		}
	}
	return tailLines(strings.Join(kept, "\n"), lines), nil
}

func mapKeys(m map[int]bool) func(yield func(int) bool) {
	return func(yield func(int) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// tailLines keeps the last n lines that are not empty.
func tailLines(s string, n int) string {
	var all []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimRight(l, "\r"); l != "" {
			all = append(all, l)
		}
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return strings.Join(all, "\n")
}

// Foreground gives the package of the app in front, or "" for the launcher or the system.
func (p *Platform) Foreground(ctx context.Context) (string, error) {
	c, err := p.Controller(ctx)
	if err != nil {
		return "", err
	}
	pkg, _, err := c.Foreground(ctx)
	return pkg, err
}
