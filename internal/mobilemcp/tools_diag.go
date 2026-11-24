package mobilemcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/mcp"
)

const (
	defaultLogs     = 100
	maxLogs         = 10000
	maxRecordSecond = 180
	// maxCrashText bounds the report that mobile_get_crash gives.
	maxCrashText = 256 << 10
)

func (m *Manager) diagTools() []*toolDef {
	return []*toolDef{
		{
			name: "mobile_start_screen_recording", title: "Start a screen recording",
			desc: "Start recording the screen to an .mp4 file in the output folder of the server. It stops at timeLimit (180 seconds at most), or when you call mobile_stop_screen_recording. One recording runs at a time.",
			schema: mcp.Object(map[string]mcp.Schema{
				"output":    mcp.String("File name in the output folder, ending in .mp4. A name with the time is made when it is left out."),
				"timeLimit": mcp.Int("Most seconds to record, 1 to 180. 180 by default.").With("minimum", 1).With("maximum", maxRecordSecond),
			}),
			run: m.startRecording,
		},
		{
			name: "mobile_stop_screen_recording", title: "Stop the screen recording",
			desc:   "Stop the recording and give the path, the size in bytes, and the length in seconds.",
			schema: mcp.Object(nil),
			run:    m.stopRecording,
		},
		{
			name: "mobile_get_device_logs", title: "Get device logs", readOnly: true,
			desc: "Read the log of the device (logcat), as JSON lines with the time, pid, tid, level, tag, and message. By default it waits for new lines until limit lines came, 30 seconds passed with none, or 60 seconds passed in all; with recent it gives the lines that the device holds now. Secrets are scrubbed. filter items are key=value or key!=value, with the keys pid, process, tag, level, and message.",
			schema: mcp.Object(map[string]mcp.Schema{
				"limit":  mcp.Int("Most lines to give. 100 by default; the device gives at most 1000.").With("minimum", 1).With("maximum", maxLogs),
				"filter": mcp.Array("Filters that all must hold. Each is key=value or key!=value with a key of pid, process, tag, level (that level and above), or message (a substring). Example: [\"tag=flutter\", \"level=W\"].", mcp.String("One filter.")),
				"saveTo": mcp.String("File name in the output folder, ending in .log, .txt, or .jsonl. The lines go to the file and not to the answer."),
				"recent": mcp.Bool("Give what the device holds now instead of waiting for new lines."),
			}),
			run: m.deviceLogs,
		},
		{
			name: "mobile_list_crashes", title: "List crashes", readOnly: true,
			desc: "List the crashes and ANRs that the device recorded, newest first, with an id, the time, the process, the package, and a one-line summary. Use mobile_get_crash for the report.",
			schema: mcp.Object(map[string]mcp.Schema{
				"package": mcp.String("Keep only the crashes of this package."),
				"since":   mcp.String("Keep only the crashes after this time: a duration such as 30m or 2h, or an RFC 3339 time."),
			}),
			run: m.listCrashes,
		},
		{
			name: "mobile_get_crash", title: "Get a crash report", readOnly: true,
			desc: "Get the report of one crash or ANR by its id from mobile_list_crashes. Secrets are scrubbed and the report is cut at 256 KiB.",
			schema: mcp.Object(map[string]mcp.Schema{
				"id": mcp.String("The id of the crash."),
			}).Required("id"),
			run: m.getCrash,
		},
	}
}

func (m *Manager) startRecording(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Output    string `json:"output"`
		TimeLimit int    `json:"timeLimit"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	if m.out == nil {
		return mcp.Result{}, errors.New("this server has no output folder, so it cannot save a recording")
	}
	if a.TimeLimit < 0 || a.TimeLimit > maxRecordSecond {
		return mcp.Result{}, fmt.Errorf("timeLimit is 1 to %d seconds", maxRecordSecond)
	}
	if m.recOut != "" {
		return mcp.Result{}, errors.New("a screen recording is already going on: stop it with mobile_stop_screen_recording")
	}
	name := a.Output
	if strings.TrimSpace(name) == "" {
		name = "recording-" + m.clock().UTC().Format("20060102-150405") + ".mp4"
	}
	path, err := m.out.Resolve(name, []string{".mp4"})
	if err != nil {
		return mcp.Result{}, fmt.Errorf("output is refused: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return mcp.Result{}, errors.New("the folder of the recording could not be made")
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	if err := dev.StartRecording(ctx, android.RecOptions{TimeLimit: time.Duration(a.TimeLimit) * time.Second, Output: path}); err != nil {
		return mcp.Result{}, err
	}
	m.recOut, m.recStart = path, m.clock()
	return textJSON(map[string]any{"recording": true, "output": path})
}

func (m *Manager) stopRecording(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	if err := noArgs(call); err != nil {
		return mcp.Result{}, err
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	started := m.recStart
	m.recOut, m.recStart = "", time.Time{}
	path, err := dev.StopRecording(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	var size int64
	if info, err := os.Stat(path); err == nil {
		size = info.Size()
	}
	seconds := 0.0
	if !started.IsZero() {
		seconds = m.clock().Sub(started).Seconds()
	}
	return textJSON(map[string]any{"path": path, "bytes": size, "seconds": float64(int(seconds*10+0.5)) / 10})
}

type logLine struct {
	Time    string `json:"time"`
	PID     int    `json:"pid"`
	TID     int    `json:"tid"`
	Level   string `json:"level"`
	Tag     string `json:"tag"`
	Message string `json:"message"`
}

func (m *Manager) deviceLogs(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Limit  int      `json:"limit"`
		Filter []string `json:"filter"`
		SaveTo string   `json:"saveTo"`
		Recent bool     `json:"recent"`
	}
	if err := args(call, &a); err != nil {
		if strings.Contains(err.Error(), "filter") {
			err = fmt.Errorf("%w: give an array such as [\"tag=flutter\"]", err)
		}
		return mcp.Result{}, err
	}
	if a.Limit == 0 {
		a.Limit = defaultLogs
	}
	if a.Limit < 1 || a.Limit > maxLogs {
		return mcp.Result{}, fmt.Errorf("limit is 1 to %d", maxLogs)
	}
	filters, err := android.ParseLogFilters(a.Filter)
	if err != nil {
		return mcp.Result{}, err
	}
	var target string
	if a.SaveTo != "" {
		if m.out == nil {
			return mcp.Result{}, errors.New("this server has no output folder, so it cannot save files")
		}
		if target, err = m.out.Resolve(a.SaveTo, []string{".log", ".txt", ".jsonl"}); err != nil {
			return mcp.Result{}, fmt.Errorf("saveTo is refused: %w", err)
		}
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	entries, err := dev.Logs(ctx, android.LogQuery{Filters: filters, Limit: a.Limit, Follow: !a.Recent})
	if err != nil {
		return mcp.Result{}, err
	}
	lines := make([]logLine, len(entries))
	for i, e := range entries {
		lines[i] = logLine{Time: e.Time, PID: e.PID, TID: e.TID, Level: e.Level, Tag: m.scrub.Text(e.Tag), Message: m.scrub.Text(e.Message)}
	}
	if target == "" {
		return textJSON(map[string]any{"count": len(lines), "entries": lines})
	}
	var buf strings.Builder
	for _, l := range lines {
		b, err := json.Marshal(l)
		if err != nil {
			return mcp.Result{}, err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	saved, err := m.out.Write(target, []string{".log", ".txt", ".jsonl"}, []byte(buf.String()))
	if err != nil {
		return mcp.Result{}, err
	}
	return textJSON(map[string]any{"count": len(lines), "path": saved})
}

type crashRow struct {
	ID      string `json:"id"`
	Tag     string `json:"tag"`
	Time    string `json:"time"`
	Process string `json:"process,omitempty"`
	Package string `json:"package,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// parseSince reads a duration back from now, or an RFC 3339 time.
func parseSince(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return now.Add(-d), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, errors.New("since is a duration such as 30m or 2h, or an RFC 3339 time")
}

func (m *Manager) listCrashes(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Package string `json:"package"`
		Since   string `json:"since"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	pkg := strings.TrimSpace(a.Package)
	if pkg != "" {
		var err error
		if pkg, err = checkPackage(pkg); err != nil {
			return mcp.Result{}, err
		}
	}
	cutoff, err := parseSince(a.Since, m.clock())
	if err != nil {
		return mcp.Result{}, err
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	crashes, err := dev.ListCrashes(ctx, pkg, 0)
	if err != nil {
		return mcp.Result{}, err
	}
	rows := []crashRow{}
	for _, c := range crashes {
		if !cutoff.IsZero() && c.Time.Before(cutoff) {
			continue
		}
		rows = append(rows, crashRow{ID: c.ID, Tag: c.Tag, Time: c.Time.UTC().Format(time.RFC3339), Process: c.Process, Package: c.Package, Summary: m.scrub.Text(c.Summary)})
	}
	return textJSON(map[string]any{"crashes": rows})
}

func (m *Manager) getCrash(ctx context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		ID string `json:"id"`
	}
	if err := args(call, &a); err != nil {
		return mcp.Result{}, err
	}
	if strings.TrimSpace(a.ID) == "" {
		return mcp.Result{}, errors.New("id needs a crash id from mobile_list_crashes")
	}
	dev, err := m.device(ctx)
	if err != nil {
		return mcp.Result{}, err
	}
	c, err := dev.GetCrash(ctx, strings.TrimSpace(a.ID))
	if errors.Is(err, android.ErrCrashNotFound) {
		return mcp.Result{}, errors.New("no crash has that id: list the crashes again, because the device keeps only the recent ones")
	}
	if err != nil {
		return mcp.Result{}, err
	}
	body, cut := c.Body, false
	if len(body) > maxCrashText {
		body, cut = strings.ToValidUTF8(body[:maxCrashText], ""), true
	}
	return textJSON(map[string]any{
		"id": c.ID, "tag": c.Tag, "time": c.Time.UTC().Format(time.RFC3339), "process": c.Process, "package": c.Package,
		"summary": m.scrub.Text(c.Summary), "report": m.scrub.Text(body), "truncated": cut,
	})
}
