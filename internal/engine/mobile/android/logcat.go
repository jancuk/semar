package android

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// LogEntry is one line of logcat -v threadtime.
type LogEntry struct {
	// Time is as the device wrote it: MM-DD HH:MM:SS.mmm, with no year and no zone.
	Time    string
	PID     int
	TID     int
	Level   string // V, D, I, W, E, F, or S
	Tag     string
	Message string
}

var logcatHead = regexp.MustCompile(`^(\d\d-\d\d \d\d:\d\d:\d\d\.\d{3})\s+(\d+)\s+(\d+)\s+([VDIWEFAS])\s+(.*)$`)

// ParseLogcatLine reads one line of logcat -v threadtime: MM-DD HH:MM:SS.mmm PID TID L TAG: message. A
// line that has another form, such as a "--------- beginning of main" line, gives false.
func ParseLogcatLine(line string) (LogEntry, bool) {
	line = strings.TrimRight(line, "\r\n")
	if len(line) > maxStreamLine {
		line = strings.ToValidUTF8(line[:maxStreamLine], "")
	}
	m := logcatHead.FindStringSubmatch(line)
	if m == nil {
		return LogEntry{}, false
	}
	rest := m[5]
	tag, msg, found := strings.Cut(rest, ": ")
	if !found {
		t, ok := strings.CutSuffix(rest, ":")
		if !ok {
			return LogEntry{}, false
		}
		tag, msg = t, ""
	}
	pid, err1 := strconv.Atoi(m[2])
	tid, err2 := strconv.Atoi(m[3])
	if err1 != nil || err2 != nil {
		return LogEntry{}, false
	}
	return LogEntry{Time: m[1], PID: pid, TID: tid, Level: m[4], Tag: strings.TrimSpace(tag), Message: msg}, true
}

// levelRank orders the levels of logcat. A silent line ranks lowest.
func levelRank(l string) int {
	return strings.Index("SVDIWEF", l)
}

// LogFilter is one condition of a log query: key=value or key!=value. The keys are pid, process (the
// name of a process, which is turned into its pids), tag, level (V, D, I, W, E, or F: that level or
// above), and message (a substring). subsystem and category are accepted and ignored: Android has
// neither. All the filters of a query must hold.
type LogFilter struct {
	Key   string
	Value string
	// Not is true for key!=value.
	Not bool
}

var (
	processNameExpr = regexp.MustCompile(`^[A-Za-z0-9_.:/-]{1,256}$`)
	logKeys         = []string{"pid", "process", "tag", "level", "message", "subsystem", "category"}
)

// ParseLogFilters reads items of the form key=value and key!=value.
func ParseLogFilters(items []string) ([]LogFilter, error) {
	var out []LogFilter
	for _, item := range items {
		neg := strings.Index(item, "!=")
		eq := strings.Index(item, "=")
		var f LogFilter
		switch {
		case eq < 0:
			return nil, fmt.Errorf("the filter %q is key=value or key!=value, with a key of pid, process, tag, level (V, D, I, W, E, F), or message, such as tag=flutter or level=W", truncate(item, 40))
		case neg >= 0 && neg < eq:
			f = LogFilter{Key: item[:neg], Value: item[neg+2:], Not: true}
		default:
			f = LogFilter{Key: item[:eq], Value: item[eq+1:]}
		}
		f.Key = strings.ToLower(strings.TrimSpace(f.Key))
		if !slices.Contains(logKeys, f.Key) {
			return nil, fmt.Errorf("the filter key %q is not one of pid, process, tag, level, or message, such as tag=flutter", truncate(f.Key, 40))
		}
		if strings.ContainsRune(f.Value, 0) || len(f.Value) > 512 {
			return nil, fmt.Errorf("the value of the filter %s is not text of up to 512 characters", f.Key)
		}
		switch f.Key {
		case "pid":
			if n, err := strconv.Atoi(f.Value); err != nil || n < 0 {
				return nil, fmt.Errorf("the filter pid needs a number")
			}
		case "level":
			f.Value = strings.ToUpper(f.Value)
			if len(f.Value) != 1 || !strings.Contains("VDIWEF", f.Value) {
				return nil, errors.New("the filter level is V, D, I, W, E, or F")
			}
		case "process":
			if !processNameExpr.MatchString(f.Value) {
				return nil, errors.New("the filter process needs a process name")
			}
		case "tag", "message":
			if f.Value == "" {
				return nil, fmt.Errorf("the filter %s needs a value", f.Key)
			}
		}
		out = append(out, f)
	}
	return out, nil
}

// logMatcher applies the filters, with the pids that the process names resolved to.
type logMatcher struct {
	filters []LogFilter
	pids    map[string]map[int]bool
}

func (m *logMatcher) match(e LogEntry) bool {
	for _, f := range m.filters {
		var ok bool
		switch f.Key {
		case "pid":
			n, _ := strconv.Atoi(f.Value)
			ok = e.PID == n
		case "process":
			ok = m.pids[f.Value][e.PID]
		case "tag":
			ok = e.Tag == f.Value
		case "level":
			ok = levelRank(e.Level) >= levelRank(f.Value)
		case "message":
			ok = strings.Contains(e.Message, f.Value)
		default: // subsystem and category
			continue
		}
		if ok == f.Not {
			return false
		}
	}
	return true
}

// LogQuery asks for lines of logcat.
type LogQuery struct {
	Filters []LogFilter
	// Limit is how many entries at most: 100 when it is 0, and never more than 1000.
	Limit int
	// Follow waits for new lines, until Limit entries came, or Silence passed with no entry, or Cap
	// passed. Without it the entries that the device holds now are given.
	Follow bool
	// Silence and Cap are 30 and 60 seconds when they are 0.
	Silence, Cap time.Duration
}

const (
	defaultLogLimit = 100
	maxLogLimit     = 1000
	// filteredFetch is how many times the limit is read from the device when there are filters, because
	// the device cuts before the filters are applied.
	filteredFetch = 20
	maxLogFetch   = 20000
)

// Logs gives log entries, the newest last. A dump reads the end of the log, and with filters it reads
// more than the limit, because the filters are applied here and not on the device.
func (c *Controller) Logs(ctx context.Context, q LogQuery) ([]LogEntry, error) {
	a, err := c.adb()
	if err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultLogLimit
	}
	limit = min(limit, maxLogLimit)
	m := &logMatcher{filters: q.Filters, pids: map[string]map[int]bool{}}
	for _, f := range q.Filters {
		if f.Key != "process" || m.pids[f.Value] != nil {
			continue
		}
		pids, err := c.pidsOf(ctx, a, f.Value)
		if err != nil {
			return nil, err
		}
		m.pids[f.Value] = pids
	}
	if q.Follow {
		return c.followLogs(ctx, a, m, limit, q)
	}
	fetch := limit
	if len(q.Filters) > 0 {
		fetch = min(limit*filteredFetch, maxLogFetch)
	}
	out, err := a.shell(ctx, "logcat", "-d", "-v", "threadtime", "-t", strconv.Itoa(fetch))
	if err != nil && out == "" {
		return nil, fmt.Errorf("the log could not be read: %w", err)
	}
	var entries []LogEntry
	for _, l := range strings.Split(out, "\n") {
		if e, ok := ParseLogcatLine(l); ok && m.match(e) {
			entries = append(entries, e)
		}
	}
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}

// pidsOf resolves a process name to its pids with pidof. A name with no process gives none.
func (c *Controller) pidsOf(ctx context.Context, a *adb, name string) (map[int]bool, error) {
	out, err := a.shell(ctx, "pidof", name)
	pids := map[int]bool{}
	for _, f := range strings.Fields(out) {
		if n, err := strconv.Atoi(f); err == nil {
			pids[n] = true
		}
	}
	if err != nil && !isExit(err) && len(pids) == 0 {
		return nil, fmt.Errorf("the process %s could not be looked up: %w", truncate(name, 60), err)
	}
	return pids, nil
}

func (c *Controller) followLogs(ctx context.Context, a *adb, m *logMatcher, limit int, q LogQuery) ([]LogEntry, error) {
	silence, capTime := q.Silence, q.Cap
	if silence <= 0 {
		silence = 30 * time.Second
	}
	if capTime <= 0 {
		capTime = 60 * time.Second
	}
	rc, wait, err := a.run.Stream(ctx, a.bin, a.args("shell", shellCommand("logcat", "-v", "threadtime", "-T", "1"))...)
	if err != nil {
		return nil, fmt.Errorf("the log could not be followed: %w", err)
	}
	lines := make(chan string)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(lines)
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 0, 4096), maxStreamLine+2)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-stop:
				return
			}
		}
	}()
	idle := time.NewTimer(silence)
	total := time.NewTimer(capTime)
	defer idle.Stop()
	defer total.Stop()
	var entries []LogEntry
	var ctxErr error
loop:
	for len(entries) < limit {
		select {
		case l, ok := <-lines:
			if !ok {
				break loop
			}
			if e, ok := ParseLogcatLine(l); ok && m.match(e) {
				entries = append(entries, e)
				if !idle.Stop() {
					select {
					case <-idle.C:
					default:
					}
				}
				idle.Reset(silence)
			}
		case <-idle.C:
			break loop
		case <-total.C:
			break loop
		case <-ctx.Done():
			ctxErr = ctx.Err()
			break loop
		}
	}
	close(stop)
	_ = rc.Close()
	_ = wait()
	<-done
	return entries, ctxErr
}
