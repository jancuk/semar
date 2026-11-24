package android

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/config"
)

// Crash is one crash, native crash, or ANR (app not responding) that the device recorded.
type Crash struct {
	// ID is <tag>@<unix milliseconds>. It is the same on every call, so a list and a later GetCrash
	// agree. The time is the clock of the device read as UTC, so it is the same wherever Semar runs.
	ID      string
	Tag     string
	Time    time.Time
	Process string
	Package string
	// Summary is the first line that says what happened, such as the exception.
	Summary string
	// Body is the report. A list leaves it empty, and GetCrash fills it.
	Body string
}

// The tags of dumpsys dropbox that hold crashes, and the name of the crash buffer of logcat.
var dropboxTags = []string{"data_app_crash", "data_app_native_crash", "data_app_anr", "system_app_crash", "system_app_anr"}

const (
	crashBufferTag = "crash"
	maxCrashBody   = 16 << 10
	defaultCrashes = 20
	maxCrashes     = 100
)

var dropboxHead = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}) ([A-Za-z0-9_.-]{1,64}) \([^)]*\)\s*$`)

// ParseDropbox reads the output of dumpsys dropbox --print <tag>. Each entry starts with a line of the
// form "2026-11-23 10:15:30 data_app_crash (text, 1234 bytes)", then the fields Process:, PID:, Package:,
// and Subject:, a blank line, and the report. The tag of the header is used, and tag is the tag that
// was asked for, for an entry whose header leaves it out. Entries come in the order of the output.
func ParseDropbox(out, tag string) []Crash {
	lines := strings.Split(out, "\n")
	var crashes []Crash
	seen := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		m := dropboxHead.FindStringSubmatch(strings.TrimRight(lines[i], "\r"))
		if m == nil {
			continue
		}
		t, err := time.Parse("2006-01-02 15:04:05", m[1])
		if err != nil {
			continue
		}
		j := i + 1
		for j < len(lines) && !dropboxHead.MatchString(strings.TrimRight(lines[j], "\r")) {
			j++
		}
		entry := lines[i+1 : j]
		// The line of equal signs before the next header belongs to the separator.
		for len(entry) > 0 && (strings.TrimSpace(entry[len(entry)-1]) == "" || strings.HasPrefix(strings.TrimSpace(entry[len(entry)-1]), "=====")) {
			entry = entry[:len(entry)-1]
		}
		i = j - 1
		tg := m[2]
		if tg == "" {
			tg = tag
		}
		c := crashFromEntry(tg, t, entry)
		for seen[c.ID] {
			t = t.Add(time.Millisecond)
			c.Time, c.ID = t, crashID(tg, t)
		}
		seen[c.ID] = true
		crashes = append(crashes, c)
	}
	return crashes
}

func crashID(tag string, t time.Time) string { return fmt.Sprintf("%s@%d", tag, t.UnixMilli()) }

// crashFromEntry reads the fields and the report of one entry of dropbox.
func crashFromEntry(tag string, t time.Time, entry []string) Crash {
	c := Crash{ID: crashID(tag, t), Tag: tag, Time: t}
	subject := ""
	body := 0
	for body < len(entry) && strings.TrimSpace(entry[body]) != "" {
		k, v, ok := strings.Cut(entry[body], ":")
		if ok {
			v = strings.TrimSpace(v)
			switch k {
			case "Process":
				c.Process = v
			case "Package":
				if f := strings.Fields(v); len(f) > 0 && config.ValidAndroidPackage(f[0]) {
					c.Package = f[0]
				}
			case "Subject":
				subject = v
			}
		}
		body++
	}
	c.Body = capBody(strings.TrimSpace(strings.Join(entry, "\n")))
	if c.Package == "" && config.ValidAndroidPackage(c.Process) {
		c.Package = c.Process
	}
	if subject != "" && strings.Contains(tag, "anr") {
		// An ANR says what happened in its subject.
		c.Summary = truncate(subject, 300)
	} else {
		c.Summary = summaryOf(entry[min(body, len(entry)):], subject)
	}
	return c
}

// summaryOf is the first line of a report that is not a stack frame: the exception, the abort message,
// or the subject of an ANR.
func summaryOf(report []string, subject string) string {
	for _, l := range report {
		t := strings.TrimSpace(l)
		switch {
		case t == "", strings.HasPrefix(t, "at "), strings.HasPrefix(t, "..."), strings.HasPrefix(t, "***"), strings.HasPrefix(t, "#"):
			continue
		case strings.HasPrefix(t, "Abort message:"):
			return truncate(strings.TrimSpace(strings.TrimPrefix(t, "Abort message:")), 300)
		}
		if subject != "" && (strings.HasPrefix(t, "ANR in") || strings.HasPrefix(t, "PID:") || strings.HasPrefix(t, "Reason:")) {
			continue
		}
		return truncate(t, 300)
	}
	return truncate(subject, 300)
}

func capBody(s string) string {
	if len(s) <= maxCrashBody {
		return s
	}
	return strings.ToValidUTF8(s[:maxCrashBody], "") + "\n... (cut)"
}

// ParseCrashBuffer reads the output of logcat -b crash -d -v threadtime: lines of one process that start
// at FATAL EXCEPTION (or at the banner of a native crash) become one crash. The log has no year, so year
// says it, and 0 is the current year.
func ParseCrashBuffer(out string, year int) []Crash {
	if year <= 0 {
		year = time.Now().UTC().Year()
	}
	type group struct {
		first LogEntry
		lines []string
		pid   int
	}
	var groups []*group
	var cur *group
	for _, l := range strings.Split(out, "\n") {
		e, ok := ParseLogcatLine(l)
		if !ok {
			continue
		}
		if strings.HasPrefix(e.Message, "FATAL EXCEPTION") || strings.HasPrefix(e.Message, "*** *** ***") {
			cur = &group{first: e, pid: e.PID}
			groups = append(groups, cur)
		}
		// The lines of one process that follow a banner are its report. Lines of another process, and
		// lines before any banner, are not.
		if cur != nil && e.PID == cur.pid {
			cur.lines = append(cur.lines, e.Message)
		}
	}
	var crashes []Crash
	seen := map[string]bool{}
	for _, g := range groups {
		t, err := time.Parse("2006 01-02 15:04:05.000", fmt.Sprintf("%04d %s", year, g.first.Time))
		if err != nil {
			continue
		}
		c := Crash{Tag: crashBufferTag, Time: t, ID: crashID(crashBufferTag, t)}
		for _, l := range g.lines {
			if rest, ok := strings.CutPrefix(l, "Process: "); ok {
				name, _, _ := strings.Cut(rest, ",")
				c.Process = strings.TrimSpace(name)
				break
			}
		}
		if config.ValidAndroidPackage(c.Process) {
			c.Package = c.Process
		}
		var report []string
		for _, l := range g.lines {
			if strings.HasPrefix(l, "FATAL EXCEPTION") || strings.HasPrefix(l, "Process: ") {
				continue
			}
			report = append(report, l)
		}
		c.Summary = summaryOf(report, "")
		c.Body = capBody(strings.Join(g.lines, "\n"))
		for seen[c.ID] {
			c.Time = c.Time.Add(time.Millisecond)
			c.ID = crashID(crashBufferTag, c.Time)
		}
		seen[c.ID] = true
		crashes = append(crashes, c)
	}
	return crashes
}

// ListCrashes lists the crashes and ANRs of the device, newest first: the entries of dropbox for the five
// tags, and the crash buffer of logcat for a device that keeps little in dropbox. pkg, when it is not "",
// keeps the crashes of one package. The bodies are left out: GetCrash gives one.
func (c *Controller) ListCrashes(ctx context.Context, pkg string, limit int) ([]Crash, error) {
	if pkg != "" && !config.ValidAndroidPackage(pkg) {
		return nil, fmt.Errorf("%q is not a package name", truncate(pkg, 80))
	}
	if limit <= 0 {
		limit = defaultCrashes
	}
	limit = min(limit, maxCrashes)
	a, err := c.adb()
	if err != nil {
		return nil, err
	}
	all, err := c.readCrashes(ctx, a, dropboxTags, true)
	if err != nil {
		return nil, err
	}
	var out []Crash
	for _, cr := range all {
		if pkg != "" && cr.Package != pkg {
			continue
		}
		cr.Body = ""
		out = append(out, cr)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// readCrashes reads the dropbox tags and, with buffer, the crash buffer. A read that fails is skipped
// unless every read failed. A crash of the buffer that dropbox has too, which is the same crash, is left
// out.
func (c *Controller) readCrashes(ctx context.Context, a *adb, tags []string, buffer bool) ([]Crash, error) {
	var all []Crash
	var errs []error
	ok := 0
	for _, tag := range tags {
		out, err := a.shell(ctx, "dumpsys", "dropbox", "--print", tag)
		if err != nil && strings.TrimSpace(out) == "" {
			errs = append(errs, err)
			continue
		}
		ok++
		all = append(all, ParseDropbox(out, tag)...)
	}
	if buffer {
		out, err := a.shell(ctx, "logcat", "-b", crashBufferTag, "-d", "-v", "threadtime")
		if err != nil && strings.TrimSpace(out) == "" {
			errs = append(errs, err)
		} else {
			ok++
			for _, cr := range ParseCrashBuffer(out, c.p.o.Now().UTC().Year()) {
				if !duplicateCrash(all, cr) {
					all = append(all, cr)
				}
			}
		}
	}
	if ok == 0 && len(errs) > 0 {
		return nil, fmt.Errorf("the crashes could not be read: %w", errors.Join(errs[:1]...))
	}
	return all, nil
}

// duplicateCrash is true when dropbox has the crash of the same process within five seconds.
func duplicateCrash(all []Crash, c Crash) bool {
	for _, o := range all {
		d := o.Time.Sub(c.Time)
		if o.Tag != crashBufferTag && o.Process == c.Process && o.Process != "" && d < 5*time.Second && d > -5*time.Second {
			return true
		}
	}
	return false
}

var crashIDExpr = regexp.MustCompile(`^([a-z_]{1,40})@(\d{1,16})$`)

// ErrCrashNotFound is the error of GetCrash for an id that the device no longer has.
var ErrCrashNotFound = errors.New("no crash has that id")

// GetCrash gives one crash with its report, by the id that ListCrashes gave.
func (c *Controller) GetCrash(ctx context.Context, id string) (Crash, error) {
	m := crashIDExpr.FindStringSubmatch(id)
	if m == nil || (m[1] != crashBufferTag && !slices.Contains(dropboxTags, m[1])) {
		return Crash{}, fmt.Errorf("%q is not a crash id", truncate(id, 60))
	}
	if _, err := strconv.ParseInt(m[2], 10, 64); err != nil {
		return Crash{}, fmt.Errorf("%q is not a crash id", truncate(id, 60))
	}
	a, err := c.adb()
	if err != nil {
		return Crash{}, err
	}
	var all []Crash
	if m[1] == crashBufferTag {
		all, err = c.readCrashes(ctx, a, nil, true)
	} else {
		all, err = c.readCrashes(ctx, a, []string{m[1]}, false)
	}
	if err != nil {
		return Crash{}, err
	}
	for _, cr := range all {
		if cr.ID == id {
			return cr, nil
		}
	}
	return Crash{}, ErrCrashNotFound
}
