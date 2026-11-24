// Package mobilemcp is the mobile tool set of the Semar tool server: the mobile_* tools, which drive an
// Android device the way an agent would with any mobile MCP server, and the safety around them. It
// imports the controller of the Android platform and never the command line.
package mobilemcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/takeover"
)

// Device is the part of android.Controller that the tools use. The tests fake it.
type Device interface {
	Serial() string
	Emulator() bool
	Remote() bool

	Source(ctx context.Context) (*mobile.Tree, error)
	Screenshot(ctx context.Context) ([]byte, error)
	ScreenSize(ctx context.Context) (android.ScreenInfo, error)
	Touch(ctx context.Context, g mobile.Gesture) error
	TypeFocused(ctx context.Context, text string, replace bool) error
	Key(ctx context.Context, code int) error
	InputMethod(ctx context.Context) (pkg string, shown bool, err error)

	Orientation(ctx context.Context) (string, error)
	SetOrientation(ctx context.Context, orientation string) error
	Fold(ctx context.Context, posture string) error
	SetLocation(ctx context.Context, lat, lon float64) error
	ClearLocation(ctx context.Context) error
	Clipboard(ctx context.Context) (string, error)
	SetClipboard(ctx context.Context, text string) error

	ListApps(ctx context.Context, includeSystem bool) ([]string, error)
	Foreground(ctx context.Context) (pkg, activity string, err error)
	LaunchApp(ctx context.Context, o android.LaunchOptions) error
	TerminateApp(ctx context.Context, pkg string) error
	InstallAPK(ctx context.Context, path string) error
	UninstallApp(ctx context.Context, pkg string) error
	ClearAppData(ctx context.Context, pkg string) error
	OpenURL(ctx context.Context, link string) error

	StartRecording(ctx context.Context, o android.RecOptions) error
	StopRecording(ctx context.Context) (string, error)
	Logs(ctx context.Context, q android.LogQuery) ([]android.LogEntry, error)
	ListCrashes(ctx context.Context, pkg string, limit int) ([]android.Crash, error)
	GetCrash(ctx context.Context, id string) (android.Crash, error)
}

var _ Device = (*android.Controller)(nil)

// Opener opens the device with the serial ("" for the one running emulator) and gives a function that
// releases it. The command line builds it from the Android platform.
type Opener func(ctx context.Context, serial string) (Device, func(context.Context) error, error)

// mode is how the tools act. In live mode the tools act on the device at once. In record mode a recording
// session owns the device, and the mobile_* tools act through it, so that each action is also a step.
type mode int

const (
	modeLive mode = iota
	modeRecord
)

// Options make a Manager.
type Options struct {
	Opener Opener
	// Inspector lists the devices that adb sees. Without it, mobile_list_available_devices names only
	// the device in use.
	Inspector mobile.AppInspector
	Guard     mcpkit.Guard
	URLs      URLGate
	// Outputs is the folder for screenshots, recordings, and saved logs. Without it, the tools that
	// write a file refuse.
	Outputs *mcpkit.Outputs
	// Scrub removes secrets from every result. Without it, a scrubber with no known secrets is used.
	Scrub *mcpkit.Scrubber
	// Serial is the device to use, or "" for the one running emulator.
	Serial string
	// RepoRoot is the repository that a relative path to an .apk is resolved against, or "".
	RepoRoot string
	// LookupEnv reads the environment for secretEnv. It is os.LookupEnv when nil.
	LookupEnv func(name string) (string, bool)
	// Clock is time.Now when nil.
	Clock func() time.Time
	// Sleep waits for d, or until the context ends. It is the real clock when nil. The Flutter warm-up
	// uses it, so that a test can run it with no wait.
	Sleep func(ctx context.Context, d time.Duration) error
	// Baton is the baton of the studio, or nil without one. A tool that only reads as far as the guard goes,
	// but writes on some arguments (mobile_clipboard with text), enters it as a write for those.
	Baton *takeover.Baton
}

// Manager owns the device of one tool server and runs the tools on it, one call at a time.
type Manager struct {
	mu sync.Mutex // serializes every call that touches the device or the refs

	open      Opener
	inspector mobile.AppInspector
	serial    string
	dev       Device
	closeDev  func(context.Context) error

	refs     *RefMap
	mode     mode
	guard    mcpkit.Guard
	urls     URLGate
	out      *mcpkit.Outputs
	scrub    *mcpkit.Scrubber
	clock    func() time.Time
	repoRoot string
	env      func(string) (string, bool)

	baton *takeover.Baton
	// client is the name that the client gave in initialize, as of the last tool call. A journey leg that
	// the Manager starts a recording for without a call of its own uses it as the author.
	client string

	recStart time.Time
	recOut   string

	// rb and rec are the record bridge: the backend that the command attaches, and the recording that is
	// going on, if any. Both are used with mu held.
	rb  RecordBackend
	rec *recording

	// sleep is Options.Sleep, and fl is the state of semar_run_flow: its backend, and the flows that ran.
	sleep func(ctx context.Context, d time.Duration) error
	fl    flowState

	defs   []*toolDef
	byName map[string]*toolDef
}

// New makes a Manager. It opens no device: the first tool that needs one does.
func New(o Options) *Manager {
	m := &Manager{
		open: o.Opener, inspector: o.Inspector, serial: o.Serial, refs: NewRefMap(), mode: modeLive,
		guard: o.Guard, urls: o.URLs, out: o.Outputs, scrub: o.Scrub, clock: o.Clock, repoRoot: o.RepoRoot, env: o.LookupEnv, baton: o.Baton,
	}
	if m.scrub == nil {
		m.scrub = mcpkit.NewScrubber(nil)
	}
	if m.clock == nil {
		m.clock = time.Now
	}
	if m.env == nil {
		m.env = os.LookupEnv
	}
	if m.sleep = o.Sleep; m.sleep == nil {
		m.sleep = sleepFor
	}
	for _, group := range [][]*toolDef{m.deviceTools(), m.appTools(), m.screenTools(), m.inputTools(), m.diagTools(), m.batchTools()} {
		m.defs = append(m.defs, group...)
	}
	m.byName = make(map[string]*toolDef, len(m.defs))
	for _, d := range m.defs {
		m.byName[d.name] = d
	}
	return m
}

// toolDef describes one tool and how to run it. run is called with the lock held.
type toolDef struct {
	name, title, desc string
	schema            mcp.Schema
	readOnly          bool
	destructive       bool
	openWorld         bool
	run               func(ctx context.Context, call mcp.Call) (mcp.Result, error)
}

// Tools returns the mobile_* tools in a fixed order. Each one checks the guard, takes the lock, runs, and
// scrubs what it gives back.
func (m *Manager) Tools() []mcp.Tool {
	tools := make([]mcp.Tool, 0, len(m.defs))
	for _, d := range m.defs {
		tools = append(tools, m.toolFor(d))
	}
	return tools
}

// toolFor makes the tool of a definition: it takes the lock, checks the guard, runs, and scrubs.
func (m *Manager) toolFor(d *toolDef) mcp.Tool {
	ann := mcp.Annotations{Title: d.title, ReadOnly: mcp.BoolPtr(d.readOnly), OpenWorld: mcp.BoolPtr(d.openWorld)}
	if !d.readOnly {
		ann.Destructive = mcp.BoolPtr(d.destructive)
	}
	return mcp.Tool{
		Name: d.name, Title: d.title, Description: d.desc, Input: d.schema, Annotations: ann,
		Surface: string(mcpkit.SurfaceMobile), Write: !d.readOnly,
		Handler: func(ctx context.Context, call mcp.Call) (mcp.Result, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			if call.Client.Name != "" {
				m.client = call.Client.Name
			}
			return m.invoke(ctx, d, call), nil
		},
	}
}

// invoke runs one tool with the lock held: the guard, the tool, and the scrub.
func (m *Manager) invoke(ctx context.Context, d *toolDef, call mcp.Call) mcp.Result {
	if err := m.guard.Allow(mcpkit.SurfaceMobile, !d.readOnly); err != nil {
		return mcp.ErrorResult(m.scrub.Text(err.Error()))
	}
	res, err := m.run(ctx, d, call)
	if err != nil {
		return mcp.ErrorResult(m.scrub.Text(err.Error()))
	}
	for i, c := range res.Content {
		if c.Type == "text" {
			res.Content[i].Text = m.scrub.Text(c.Text)
		}
	}
	return res
}

// Close releases the device. It is safe to call more than once.
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeLocked(ctx)
}

func (m *Manager) closeLocked(ctx context.Context) error {
	// An open recording is dropped first: its session holds the device, and its overlay holds secrets.
	var recErr error
	if m.rec != nil {
		recErr = m.endRecordingLocked(ctx)
	}
	m.refs.Reset("", nil)
	// A screen recording that is still going is pulled to its file before the device is released: the
	// release would delete it, and the agent asked for that video. The error, if any, is reported with the
	// others (the server writes it to standard error).
	var stopErr error
	if m.recOut != "" && m.dev != nil {
		if _, err := m.dev.StopRecording(ctx); err != nil {
			stopErr = fmt.Errorf("the screen recording could not be saved: %w", err)
		}
	}
	m.recStart, m.recOut = time.Time{}, ""
	recErr = errors.Join(recErr, stopErr)
	closeDev := m.closeDev
	m.dev, m.closeDev = nil, nil
	if closeDev == nil {
		return recErr
	}
	return errors.Join(recErr, closeDev(ctx))
}

// device gives the device in use, and opens it on the first call.
func (m *Manager) device(ctx context.Context) (Device, error) {
	if m.dev != nil {
		return m.dev, nil
	}
	if m.mode == modeRecord {
		return nil, errors.New("a recording holds the device: end it with semar_record_stop before you use this tool")
	}
	if m.open == nil {
		return nil, errors.New("this server has no way to open a device")
	}
	dev, closeDev, err := m.open(ctx, m.serial)
	if err != nil {
		return nil, err
	}
	m.dev, m.closeDev = dev, closeDev
	return dev, nil
}

// noArgs refuses any argument, for a tool that takes none.
func noArgs(call mcp.Call) error { return mcp.DecodeArgs(call.Args, &struct{}{}) }

// args decodes the arguments of a call into dst.
func args(call mcp.Call, dst any) error { return mcp.DecodeArgs(call.Args, dst) }

// textJSON is a result of one text block that holds v as JSON. It carries no structuredContent on purpose:
// the scrub works on text.
func textJSON(v any) (mcp.Result, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return mcp.Result{}, fmt.Errorf("the result could not be encoded: %w", err)
	}
	return mcp.TextResult(string(b)), nil
}

// needsConfirm refuses a destructive call on a device that is not an emulator unless confirm is set.
func needsConfirm(dev Device, what string, confirm bool) error {
	if dev.Emulator() || confirm {
		return nil
	}
	return fmt.Errorf("%s on the physical device %s cannot be undone: pass confirm: true to go ahead", what, nonEmpty(dev.Serial(), "in use"))
}

func nonEmpty(s, other string) string {
	if strings.TrimSpace(s) == "" {
		return other
	}
	return s
}
