package cli

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/localhttp"
	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/mobilemcp"
	"github.com/jancuk/semar/internal/recorder"
	"github.com/jancuk/semar/internal/redact"
	"github.com/jancuk/semar/internal/secret"
	"github.com/jancuk/semar/internal/takeover"
)

// mcpCloseLimit bounds the end of the tool server: the surfaces, the device, and the run.
const mcpCloseLimit = 15 * time.Second

// mcpTokenEnv is the variable that holds the token of the HTTP server, for a person who wants a token
// that stays the same between starts. A token is never written to a file.
const mcpTokenEnv = "SEMAR_MCP_TOKEN"

// mcpMinToken is the shortest token that SEMAR_MCP_TOKEN may hold.
const mcpMinToken = 32

// mcpRunPurpose is the purpose of the one run that a tool server makes for the actions of the engines.
const mcpRunPurpose = "mcp"

// mcpStudioDefault is the address of --studio when it is given with no value.
const mcpStudioDefault = "127.0.0.1:0"

// mcpSurfaceOrder is the order of the surfaces, which is also the order of their tools.
var mcpSurfaceOrder = []string{"mobile", "web", "api", "journey"}

// mcpFlags are the flags of semar mcp. Every flag of every batch is defined here, so that a surface of a
// later batch never edits this file.
type mcpFlags struct {
	dir         string
	env         string
	http        string
	device      string
	out         string
	readOnly    bool
	allowUnsafe bool
	studio      string
	studioSet   bool
	surfaces    string
	surfacesSet bool
	headed      bool
}

func newMCPCommand(o options) *cobra.Command {
	var f mcpFlags
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve Semar's tools to an AI agent over MCP",
		Long: "Serve the mobile tools of Semar (and, in a repository, its record, flow, web, API, and journey tools) over the Model Context Protocol.\n" +
			"By default it speaks on standard input and output. With --http it serves http://127.0.0.1:<port>/mcp with a token that it prints on standard error.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f.studioSet = cmd.Flags().Changed("studio")
			f.surfacesSet = cmd.Flags().Changed("surfaces")
			return runMCP(cmd.Context(), cmd, o, f)
		},
	}
	addDirFlag(cmd, &f.dir)
	cmd.Flags().StringVar(&f.env, "env", "", "the environment whose android settings, effects policy, kind, and secrets apply (a repository only)")
	cmd.Flags().StringVar(&f.http, "http", "", "serve Streamable HTTP at http://<addr>/mcp instead of standard input and output; the address must be on this machine, and :8765 means 127.0.0.1:8765")
	cmd.Flags().StringVar(&f.device, "device", "", "the serial of the device to use first")
	cmd.Flags().StringVar(&f.out, "out", "", "the folder for the files that tools write")
	cmd.Flags().BoolVar(&f.readOnly, "read-only", false, "refuse every tool that changes something")
	cmd.Flags().BoolVar(&f.allowUnsafe, "allow-unsafe-urls", false, "let mobile_open_url open custom schemes (also "+mobilemcp.UnsafeURLsEnv+"=1)")
	cmd.Flags().StringVar(&f.studio, "studio", "", "also serve the studio, where a person can take over, at this address on this machine")
	cmd.Flags().Lookup("studio").NoOptDefVal = mcpStudioDefault
	cmd.Flags().StringVar(&f.surfaces, "surfaces", "", "the tool groups to serve, from mobile, web, api, journey, separated by commas (a repository serves every group its configuration can; otherwise mobile)")
	cmd.Flags().BoolVar(&f.headed, "headed", false, "show the browser window of the web tools (--studio implies it)")
	return cmd
}

// mcpSurfaceResult is what a surface gives: its tools, and what ends it.
type mcpSurfaceResult struct {
	Tools []mcp.Tool
	// Close ends what the surface started. It runs on a context that is not cancelled, with 15 seconds.
	Close func(context.Context) error
}

// mcpStudioResult is what the studio hook gives: its tools, the address it serves, and what ends it.
type mcpStudioResult struct {
	Tools []mcp.Tool
	// Addr is the address that the studio listens on, which is printed on standard error.
	Addr  string
	Close func(context.Context) error
}

// errMCPSurfaceUnavailable is what a build function wraps when its surface cannot serve this
// configuration. A surface that was not asked for by name is then left out, and one that was asked for
// is an error that says why.
var errMCPSurfaceUnavailable = errors.New("the surface cannot serve this configuration")

// mcpUnavailable makes the error that build functions return.
func mcpUnavailable(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errMCPSurfaceUnavailable, fmt.Sprintf(format, args...))
}

type mcpSurface struct {
	name  string
	build func(*mcpWiring) (mcpSurfaceResult, error)
}

var (
	mcpSurfaces     []mcpSurface
	mcpMobileExtras []func(*mcpWiring) (mcpSurfaceResult, error)
	mcpStudioHook   func(*mcpWiring) (mcpStudioResult, error)
)

// registerMCPSurface adds a tool group to semar mcp. name is web, api, or journey. A build function is
// called once, in repo mode only, after the mobile tools are made; it calls mcpUnavailable when the
// configuration cannot serve it. Each surface calls this from init() in its own cli/mcp_<name>.go.
func registerMCPSurface(name string, build func(*mcpWiring) (mcpSurfaceResult, error)) {
	mcpSurfaces = append(mcpSurfaces, mcpSurface{name: name, build: build})
}

// registerMCPMobileExtra adds tools to the mobile group, such as the record and flow tools. The function
// runs after the Manager is made, in both modes: it checks w.RepoMode itself when it needs a repository.
// It may give no tools.
func registerMCPMobileExtra(fn func(*mcpWiring) (mcpSurfaceResult, error)) {
	mcpMobileExtras = append(mcpMobileExtras, fn)
}

// registerMCPStudio sets the function that starts the studio. It runs only with --studio, after every
// surface, with w.Baton set and w.StudioAddr holding the address that was asked for.
func registerMCPStudio(fn func(*mcpWiring) (mcpStudioResult, error)) { mcpStudioHook = fn }

// mcpWiring is what every surface of the tool server shares. runMCP makes it, and the hooks read it.
type mcpWiring struct {
	Ctx context.Context
	// Opts are the options of the process, such as its clock and its way to read the environment.
	Opts options
	Dir  string
	// RepoMode is true when Dir has .semar/semar.yaml. Cfg, EnvName, and Env are set only then, and
	// EnvName is "" when the repository has several environments and --env was not given.
	RepoMode bool
	Cfg      config.Config
	EnvName  string
	Env      config.Environment

	Guard    mcpkit.Guard
	Scrubber *mcpkit.Scrubber
	Outputs  *mcpkit.Outputs
	// Secrets resolves the secrets of the repository and remembers what it gave.
	Secrets *secret.Env
	// Policy is the effects policy of Env, or nil without an environment.
	Policy          *effects.Policy
	AllowUnsafeURLs bool
	Headed          bool
	// StudioAddr is the address asked for with --studio, or "" without it.
	StudioAddr   string
	DeviceSerial string
	// Surfaces are the groups that were chosen, in order. A hook may read it.
	Surfaces []string
	// served are the groups whose tools are served, in order. collect fills it.
	served []string
	// Mobile runs the mobile_* tools.
	Mobile *mobilemcp.Manager
	// Baton passes control between the agent and a person. It is nil without --studio.
	Baton     *takeover.Baton
	LookupEnv func(string) (string, bool)
	Stderr    io.Writer
	// ExtraMiddleware is applied to every tool, inside the audit log and the guard, in this order. A
	// surface or the studio appends to it from its build function.
	ExtraMiddleware []mcp.Middleware

	device *mcpDevice

	smu     sync.Mutex
	extra   []string // secret values that arrived after the start
	runMu   sync.Mutex
	runID   domain.RunID
	runRec  *recorder.Recorder
	runFile *evidence.Run
	runAt   time.Time
}

// MobilePlatform is the Android platform that the device of the mobile tools runs on, or nil while no
// device is open. A flow that is run from a tool acts on it, so that the tools and the flow share one
// device and one session.
func (w *mcpWiring) MobilePlatform() *android.Platform { return w.device.current() }

// AddSecrets teaches the scrubber, and the redactor of the run, values that must never be shown.
func (w *mcpWiring) AddSecrets(values ...string) {
	w.Scrubber.Add(values...)
	w.smu.Lock()
	defer w.smu.Unlock()
	for _, v := range values {
		if len(v) >= 4 && !slices.Contains(w.extra, v) {
			w.extra = append(w.extra, v)
		}
	}
}

// known is every secret value that is known now.
func (w *mcpWiring) known() []string {
	w.smu.Lock()
	defer w.smu.Unlock()
	return append(w.Secrets.Loaded(), w.extra...)
}

// EnsureRun makes the one run of the process the first time that an engine acts, and gives it on every
// later call: .semar/runs/<runID>/, with the actions that the engines record, so that semar reports can
// show what the agent sent. It is an error without a repository and an environment. The run is closed
// when the server ends. A call that failed can be made again.
func (w *mcpWiring) EnsureRun() (domain.RunID, *recorder.Recorder, error) {
	w.runMu.Lock()
	defer w.runMu.Unlock()
	if w.runRec != nil {
		return w.runID, w.runRec, nil
	}
	switch {
	case !w.RepoMode:
		return "", nil, errors.New("there is no repository: the actions of an engine are recorded only in a repository, so start semar mcp in a directory that has .semar/semar.yaml")
	case w.EnvName == "":
		return "", nil, errors.New("no environment is bound: the repository has several, so start semar mcp with --env <name>")
	case w.Policy == nil:
		return "", nil, errors.New("the environment has no effects policy")
	}
	store, err := evidence.Open(semarDir(w.Dir))
	if err != nil {
		return "", nil, err
	}
	key, err := store.RedactKey()
	if err != nil {
		return "", nil, err
	}
	red, err := redact.New(w.Cfg.Redact, w.known, key)
	if err != nil {
		return "", nil, fmt.Errorf("semar.yaml: %w", err)
	}
	id := w.Opts.RunID()
	run, err := store.NewRun(id)
	if err != nil {
		return "", nil, err
	}
	policy := w.Policy
	classify := func(method string, u *url.URL) effects.Class {
		return policy.Classify(effects.Request{Method: method, URL: u})
	}
	w.runID, w.runFile, w.runAt = id, run, w.Opts.Clock()
	w.runRec = recorder.New(run, red, classify, id)
	return w.runID, w.runRec, nil
}

// RunFiles is the folder of the run, or nil before EnsureRun made it.
func (w *mcpWiring) RunFiles() *evidence.Run {
	w.runMu.Lock()
	defer w.runMu.Unlock()
	return w.runFile
}

// closeRun ends the run, if it was made.
func (w *mcpWiring) closeRun() error {
	w.runMu.Lock()
	defer w.runMu.Unlock()
	if w.runFile == nil {
		return nil
	}
	err := w.runFile.Close(domain.Run{
		ID: w.runID, Environment: w.EnvName, EnvironmentKind: w.Env.Kind, Purpose: mcpRunPurpose,
		StartedAt: w.runAt, EndedAt: w.Opts.Clock(),
	})
	w.runFile, w.runRec = nil, nil
	return err
}

func runMCP(ctx context.Context, cmd *cobra.Command, o options, f mcpFlags) error {
	errw := &lockedWriter{w: cmd.ErrOrStderr()}
	httpAddr := ""
	if f.http != "" {
		a, err := loopbackAddr("--http", f.http)
		if err != nil {
			return err
		}
		httpAddr = a
	}
	studioAddr := ""
	if f.studioSet {
		a, err := loopbackAddr("--studio", f.studio)
		if err != nil {
			return err
		}
		studioAddr = a
	}
	token := ""
	if httpAddr != "" {
		t, err := mcpToken(o.LookupEnv)
		if err != nil {
			return err
		}
		token = t
	}

	dir := f.dir
	repoMode := fileExists(filepath.Join(semarDir(dir), "semar.yaml"))
	surfaces, err := chooseSurfaces(f, repoMode)
	if err != nil {
		return err
	}
	if f.env != "" && !repoMode {
		return fmt.Errorf("--env needs a repository: there is no %s", filepath.Join(semarDir(dir), "semar.yaml"))
	}
	if f.studioSet && mcpStudioHook == nil {
		return errors.New("--studio is not available in this build")
	}

	w := &mcpWiring{
		Ctx: ctx, Opts: o, Dir: dir, RepoMode: repoMode, Stderr: errw, LookupEnv: o.LookupEnv,
		AllowUnsafeURLs: f.allowUnsafe || mobilemcp.AllowUnsafeFromEnv(o.LookupEnv),
		Headed:          f.headed || f.studioSet, StudioAddr: studioAddr, DeviceSerial: f.device,
		Surfaces: surfaces, Secrets: secret.NewEnv(o.LookupEnv),
	}
	if err := w.bind(f, errw); err != nil {
		return err
	}
	if f.studioSet {
		w.Baton = takeover.New(o.Clock)
	}
	if token != "" {
		w.AddSecrets(token)
	}

	home := ""
	if !repoMode || f.out == "" {
		h, err := o.home()
		if err != nil {
			return err
		}
		home = h
	}
	outRoot := f.out
	switch {
	case outRoot != "":
	case repoMode:
		outRoot = filepath.Join(semarDir(dir), "mcp")
	default:
		outRoot = filepath.Join(home, "mcp")
	}
	w.Outputs, err = mcpkit.NewOutputs(outRoot)
	if err != nil {
		return err
	}
	w.device = &mcpDevice{w: w, home: home}

	logADB(ctx, errw, w)

	var closers []func(context.Context) error
	finish := func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), mcpCloseLimit)
		defer cancel()
		for i := len(closers) - 1; i >= 0; i-- {
			if err := closers[i](cctx); err != nil {
				fmt.Fprintf(errw, "semar mcp: closing: %s\n", w.Scrubber.Text(err.Error()))
			}
		}
	}
	// The run is closed last, once nothing can record into it.
	closers = append(closers, func(context.Context) error { return w.closeRun() })

	w.Mobile = mobilemcp.New(mobilemcp.Options{
		Opener: w.device.open, Inspector: &lazyInspector{o: o, repo: w.adbRoot()},
		Guard: w.Guard, URLs: w.urlGate(), Outputs: w.Outputs, Scrub: w.Scrubber,
		Serial: f.device, RepoRoot: w.adbRoot(), LookupEnv: o.LookupEnv, Clock: o.Clock, Baton: w.Baton,
	})
	closers = append(closers, w.Mobile.Close)

	tools, err := w.collect(&closers)
	if err != nil {
		finish()
		return err
	}
	srv, err := mcp.NewServer(mcp.Info{Name: "semar", Version: Version, Instructions: w.instructions()}, tools, nil)
	if err != nil {
		finish()
		return err
	}
	srv.Use(auditMiddleware(errw, w.Scrubber, o.Clock), w.Guard.Middleware())
	srv.Use(w.ExtraMiddleware...)

	ctx, stop := signal.NotifyContext(ctx, StopSignals()...)
	defer stop()
	w.Ctx = ctx

	if httpAddr == "" {
		fmt.Fprintf(errw, "semar mcp: %s\n", w.modeLine("standard input and output"))
		err = mcp.ServeStdio(ctx, srv, o.Stdin, cmd.OutOrStdout())
		finish()
		return err
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", httpAddr)
	if err != nil {
		finish()
		return fmt.Errorf("listen on %s: %w", httpAddr, err)
	}
	printHTTPBanner(errw, ln.Addr().String(), token, w.modeLine("HTTP"))
	err = localhttp.Serve(ctx, ln, mcp.NewHTTPHandler(srv, mcp.HTTPOptions{Token: token}), nil)
	finish()
	return err
}

// collect makes the tools of every group, in order, and records what ends each in closers.
func (w *mcpWiring) collect(closers *[]func(context.Context) error) ([]mcp.Tool, error) {
	add := func(res mcpSurfaceResult, into *[]mcp.Tool) {
		*into = append(*into, res.Tools...)
		if res.Close != nil {
			*closers = append(*closers, res.Close)
		}
	}
	var tools []mcp.Tool
	w.served = nil
	if slices.Contains(w.Surfaces, "mobile") {
		tools = append(tools, w.Mobile.Tools()...)
		w.served = append(w.served, "mobile")
		for _, fn := range mcpMobileExtras {
			res, err := fn(w)
			if err != nil {
				if errors.Is(err, errMCPSurfaceUnavailable) {
					fmt.Fprintf(w.Stderr, "semar mcp: a mobile tool group is left out: %s\n", w.Scrubber.Text(err.Error()))
					continue
				}
				return nil, err
			}
			add(res, &tools)
		}
	}
	for _, name := range mcpSurfaceOrder[1:] {
		if !slices.Contains(w.Surfaces, name) {
			continue
		}
		var sf *mcpSurface
		for i := range mcpSurfaces {
			if mcpSurfaces[i].name == name {
				sf = &mcpSurfaces[i]
			}
		}
		if sf == nil {
			continue
		}
		res, err := sf.build(w)
		if err != nil {
			if errors.Is(err, errMCPSurfaceUnavailable) {
				fmt.Fprintf(w.Stderr, "semar mcp: the %s tools are left out: %s\n", name, w.Scrubber.Text(err.Error()))
				continue
			}
			return nil, fmt.Errorf("the %s tools: %w", name, err)
		}
		add(res, &tools)
		w.served = append(w.served, name)
	}
	if mcpStudioHook != nil && w.StudioAddr != "" {
		res, err := mcpStudioHook(w)
		if err != nil {
			return nil, fmt.Errorf("the studio: %w", err)
		}
		if res.Addr != "" {
			fmt.Fprintf(w.Stderr, "semar mcp: studio at %s\n", res.Addr)
		}
		add(mcpSurfaceResult{Tools: res.Tools, Close: res.Close}, &tools)
	}
	return tools, nil
}

// bind reads the repository: the configuration, the environment, the guard, and the scrubber. Without
// a repository it makes a guard from --read-only alone and a scrubber with no known secrets.
func (w *mcpWiring) bind(f mcpFlags, errw io.Writer) error {
	reasons := []string{}
	if w.RepoMode {
		cfg, err := config.Load(w.Dir)
		if err != nil {
			return err
		}
		noteIgnoredSettings(errw, cfg)
		w.Cfg = cfg
		switch {
		case f.env != "":
			name, env, err := cfg.Environment(f.env)
			if err != nil {
				return err
			}
			w.EnvName, w.Env = name, env
		case len(cfg.Environments) == 1:
			name, env, err := cfg.Environment("")
			if err != nil {
				return err
			}
			w.EnvName, w.Env = name, env
		default:
			fmt.Fprintf(errw, "semar mcp: the repository has %d environments and --env was not given, so no environment is bound: there is no effects policy, and the tools that need one are left out\n", len(cfg.Environments))
		}
		if w.Env.Kind == config.KindProduction && f.env == "" {
			return fmt.Errorf("the environment %q is a production environment; name it with --env %s to serve tools against it", w.EnvName, w.EnvName)
		}
		if w.EnvName != "" {
			pol, err := effects.New(w.Env)
			if err != nil {
				return fmt.Errorf("semar.yaml: %w", err)
			}
			w.Policy = pol
			switch {
			case w.Env.Kind == config.KindProduction:
				reasons = append(reasons, fmt.Sprintf("the environment %q is production", w.EnvName))
			case w.Env.Effects.Mode == "read_only":
				reasons = append(reasons, fmt.Sprintf("the environment %q is read only", w.EnvName))
			}
		}
		key := make([]byte, 32)
		_, _ = rand.Read(key)
		red, err := redact.New(cfg.Redact, w.known, key)
		if err != nil {
			return fmt.Errorf("semar.yaml: %w", err)
		}
		w.Scrubber = mcpkit.NewScrubber(red)
		for _, name := range w.Env.Android.SecretEnvNames() {
			if v, ok := w.LookupEnv(name); ok {
				w.AddSecrets(v)
			}
		}
	} else {
		w.Scrubber = mcpkit.NewScrubber(nil)
	}
	if f.readOnly {
		reasons = append(reasons, "--read-only was given")
	}
	if len(reasons) > 0 {
		w.Guard = mcpkit.Guard{Observe: true, Reason: reasons[0]}
	}
	return nil
}

// adbRoot is the repository that adb must not live in, or "".
func (w *mcpWiring) adbRoot() string {
	if w.RepoMode {
		return w.Dir
	}
	return ""
}

func (w *mcpWiring) urlGate() mobilemcp.URLGate {
	g := mobilemcp.URLGate{Policy: w.Policy, AllowUnsafe: w.AllowUnsafeURLs}
	for _, s := range w.Env.Services {
		if s.Android != nil {
			g.Schemes = append(g.Schemes, s.Android.Schemes...)
		}
	}
	return g
}

func (w *mcpWiring) modeLine(transport string) string {
	mode := "standalone mode, mobile tools only"
	if w.RepoMode {
		env := "no environment bound"
		if w.EnvName != "" {
			env = "environment " + w.EnvName
		}
		groups := "none"
		if len(w.served) > 0 {
			groups = strings.Join(w.served, ",")
		}
		mode = fmt.Sprintf("repository mode (%s), tool groups %s", env, groups)
	}
	if w.Guard.Observe {
		mode += ", observing only: " + w.Guard.Reason
	}
	return fmt.Sprintf("serving on %s, %s", transport, mode)
}

func (w *mcpWiring) instructions() string {
	text := "Semar tools. mobile_* tools drive an Android device: list elements with mobile_list_elements_on_screen, then act with the refs it gives."
	if w.Guard.Observe {
		text += " This server only observes: tools that change something are refused (" + w.Guard.Reason + ")."
	}
	return text
}

// chooseSurfaces reads --surfaces. The default is every group of a repository, and mobile alone without
// one; a group that the configuration cannot serve is left out later, when its build function says so.
func chooseSurfaces(f mcpFlags, repoMode bool) ([]string, error) {
	if !f.surfacesSet {
		if repoMode {
			return slices.Clone(mcpSurfaceOrder), nil
		}
		return []string{"mobile"}, nil
	}
	chosen := map[string]bool{}
	for _, part := range strings.Split(f.surfaces, ",") {
		name := strings.ToLower(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		if !slices.Contains(mcpSurfaceOrder, name) {
			return nil, fmt.Errorf("--surfaces %q: %q is not a tool group; choose from %s", f.surfaces, name, strings.Join(mcpSurfaceOrder, ", "))
		}
		chosen[name] = true
	}
	if len(chosen) == 0 {
		return nil, fmt.Errorf("--surfaces is empty; choose from %s", strings.Join(mcpSurfaceOrder, ", "))
	}
	var out []string
	for _, name := range mcpSurfaceOrder {
		if !chosen[name] {
			continue
		}
		if name != "mobile" && !repoMode {
			return nil, fmt.Errorf("--surfaces %s needs a repository: the %s tools use the hosts, policy, and screens of .semar/semar.yaml, and there is none in %s. Without a repository only mobile is served", name, name, f.dir)
		}
		if name != "mobile" && !mcpRegistered(name) {
			return nil, fmt.Errorf("--surfaces %s: the %s tools are not part of this build", name, name)
		}
		out = append(out, name)
	}
	return out, nil
}

func mcpRegistered(name string) bool {
	return slices.ContainsFunc(mcpSurfaces, func(s mcpSurface) bool { return s.name == name })
}

// loopbackAddr turns the value of a flag into an address on this machine: ":8765" is 127.0.0.1:8765, and
// a host that is not loopback is refused, because the server acts on a device and a person's repository.
func loopbackAddr(flag, value string) (string, error) {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return "", fmt.Errorf("%s %q is not an address: write it as 127.0.0.1:8765 or :8765", flag, value)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return "", fmt.Errorf("%s %q has no valid port", flag, value)
	}
	switch strings.ToLower(host) {
	case "", "localhost":
		host = "127.0.0.1"
	default:
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return "", fmt.Errorf("%s %q is refused: the address must be on this machine (127.0.0.1, ::1, or localhost), because anyone who reaches the server can act on your device", flag, value)
		}
	}
	return net.JoinHostPort(host, port), nil
}

// mcpToken gives the token of the HTTP server: SEMAR_MCP_TOKEN when it is set, and a new one if not.
func mcpToken(lookup func(string) (string, bool)) (string, error) {
	if v, ok := lookup(mcpTokenEnv); ok && v != "" {
		if len(v) < mcpMinToken {
			return "", fmt.Errorf("%s must be at least %d characters", mcpTokenEnv, mcpMinToken)
		}
		return v, nil
	}
	return localhttp.NewToken()
}

// printHTTPBanner tells a person where the server is, the token, and a client entry that works. It goes
// to standard error only, and the token is not written anywhere else.
func printHTTPBanner(w io.Writer, addr, token, mode string) {
	url := "http://" + addr + "/mcp"
	fmt.Fprintf(w, "semar mcp: %s\n", mode)
	fmt.Fprintf(w, "semar mcp: address: %s\n", url)
	fmt.Fprintf(w, "semar mcp: token: %s\n", token)
	entry, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"semar": map[string]any{
		"url": url, "headers": map[string]string{"Authorization": "Bearer " + token},
	}}})
	fmt.Fprintf(w, "semar mcp: client entry: %s\n", entry)
	fmt.Fprintln(w, "semar mcp: anyone who has this token can act on your device until the server ends; it is not saved")
}

// auditMiddleware writes one line to w for each tool call: the name, the milliseconds, and ok or error.
// It never writes the arguments or the result.
func auditMiddleware(w io.Writer, scrub *mcpkit.Scrubber, clock func() time.Time) mcp.Middleware {
	return func(t mcp.Tool, next mcp.Handler) mcp.Handler {
		return func(ctx context.Context, call mcp.Call) (mcp.Result, error) {
			start := clock()
			res, err := next(ctx, call)
			status := "ok"
			if err != nil || res.IsError {
				status = "error"
			}
			fmt.Fprintf(w, "semar mcp: %s %dms %s\n", scrub.Text(t.Name), clock().Sub(start).Milliseconds(), status)
			return res, err
		}
	}
}

// logADB says once which adb the server will use, where it was found, and its version, so that a person
// can see two programs of different versions that kill each other's server. A missing adb is a warning:
// the first tool that needs a device says it again.
func logADB(ctx context.Context, w io.Writer, mw *mcpWiring) {
	if mw.RepoMode && mw.Env.Android.Remote() {
		fmt.Fprintln(w, "semar mcp: adb: not used, the environment has android.serverUrl")
		return
	}
	path, source, err := android.FindADBWhere(mw.LookupEnv, mw.adbRoot())
	if err != nil {
		fmt.Fprintf(w, "semar mcp: adb: not found yet (%s); a tool that needs a device will say so again\n", mw.Scrubber.Text(err.Error()))
		return
	}
	vctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	version, err := android.ADBVersion(vctx, path)
	if err != nil || version == "" {
		fmt.Fprintf(w, "semar mcp: adb: %s (%s), version unknown\n", path, source)
		return
	}
	fmt.Fprintf(w, "semar mcp: adb: %s (%s), version %s\n", path, source, version)
}

// mcpDevice opens the device for the mobile tools through the Android platform, and remembers the
// platform that is open.
type mcpDevice struct {
	w    *mcpWiring
	home string

	mu       sync.Mutex
	platform *android.Platform
}

func (d *mcpDevice) current() *android.Platform {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.platform
}

// open is a mobilemcp.Opener. A serial that a tool names wins over ANDROID_SERIAL and android.device.
func (d *mcpDevice) open(ctx context.Context, serial string) (mobilemcp.Device, func(context.Context) error, error) {
	w := d.w
	env := w.Env.Android
	lookup := w.LookupEnv
	if serial != "" {
		env.Device = serial
		inner := lookup
		lookup = func(name string) (string, bool) {
			if name == "ANDROID_SERIAL" {
				return serial, true
			}
			return inner(name)
		}
	}
	var apps map[string]android.App
	if w.RepoMode && w.EnvName != "" {
		apps = androidApps(w.Dir, w.Env, declaredBuilds{})
	}
	p := android.New(android.Options{
		Home: d.home, Env: env, Apps: apps, RepoRoot: w.adbRoot(), LookupEnv: lookup,
		Now: w.Opts.Clock, Sleep: w.Opts.Sleep,
	})
	ctl, err := p.Controller(ctx)
	if err != nil {
		_ = p.Close(ctx)
		return nil, nil, err
	}
	d.mu.Lock()
	d.platform = p
	d.mu.Unlock()
	return ctl, func(cctx context.Context) error {
		d.mu.Lock()
		if d.platform == p {
			d.platform = nil
		}
		d.mu.Unlock()
		return p.Close(cctx)
	}, nil
}

// lockedWriter serializes the writes of the audit log, the banner, and the notices.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
