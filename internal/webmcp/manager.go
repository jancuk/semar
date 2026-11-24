// Package webmcp is the web tool set of the Semar tool server: the web_* tools, which let an agent drive
// one Chrome window by references, record what it did, and save it as a web test. It never imports the
// command line: the browser and the writing of the files come in through two interfaces.
package webmcp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/hostrule"
	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/pagelist"
	"github.com/jancuk/semar/internal/record"
	"github.com/jancuk/semar/internal/takeover"
)

// Browser is one Chrome window that is recorded. The command line builds it from the engine's recorder
// and explorer, and the tests fake it.
type Browser interface {
	List(ctx context.Context) (pagelist.List, error)
	Click(ctx context.Context, ref string) error
	Fill(ctx context.Context, ref, text string) error
	Select(ctx context.Context, ref, label string) error
	SetChecked(ctx context.Context, ref string, on bool) error
	Press(ctx context.Context, ref, key string) error
	Hover(ctx context.Context, ref string) error
	Drag(ctx context.Context, from, to string) error
	Shows(ctx context.Context, ref string) (string, error)
	Check(ctx context.Context, ref string) (string, error)
	Open(ctx context.Context, rawURL string) error
	Screenshot(ctx context.Context) ([]byte, error)
	Location(ctx context.Context) (string, error)
	Mark() int
	EventsFrom(i int) []record.Event
	// Vars are the variables that texts of the recording used, such as a date, with the text of each.
	Vars() map[string]string
	Blocked() []string
	// Close ends the window and gives every event. It may be called more than once.
	Close() []record.Event
}

// OpenRequest is what the backend needs to start a window. URL is already checked against the hosts and
// the policy. ReadOnly says that the policy of the window must allow reads only.
type OpenRequest struct {
	Service  string
	URL      string
	Account  string
	Headed   bool
	ReadOnly bool
}

// OpenInfo says what the backend opened.
type OpenInfo struct {
	Service  string
	Account  string
	SignedIn string // the account that the window signed in as, or ""
	URL      string
}

// SaveRequest is what web_save asks for.
type SaveRequest struct {
	ID        string
	Ticket    string
	Title     string
	Replace   bool
	Verify    bool
	SecretEnv string // the variable that holds the password typed, or ""
	Info      OpenInfo
	// Client is the name that the client gave in initialize, for the author of the script.
	Client string
}

// SaveResult is what the backend wrote.
type SaveResult struct {
	ScenarioPath string
	ScreensPath  string
	Actions      int
	Checks       int
	Replay       string // the outcome of the replay, or "" when it was not asked for
	Notes        []string
	FailsNow     []string
}

// WebBackend opens windows and writes tests. The command line implements it.
type WebBackend interface {
	Open(ctx context.Context, r OpenRequest) (Browser, OpenInfo, error)
	// Save writes the test of the events and replays it once when asked. It returns an error that says why
	// when no test was kept, and then the repository is as it was.
	Save(ctx context.Context, r SaveRequest, events []record.Event, vars map[string]string) (SaveResult, error)
}

// Service is a web service of the environment that a window may open.
type Service struct {
	Name    string
	BaseURL string
	// Start is the path where the web page of the service starts. "/" when empty.
	Start string
}

// Options make a Manager.
type Options struct {
	Backend  WebBackend
	Services []Service
	// Hosts and Policy keep the window inside the environment. An address is opened only when
	// hostrule.Allowed lists its host and the policy admits a GET of it. With no Hosts nothing opens.
	Hosts  []string
	Policy *effects.Policy
	Guard  mcpkit.Guard
	// Scrub removes secrets from every text. Without it, a scrubber with no known secrets is used.
	Scrub *mcpkit.Scrubber
	// Outputs is the folder for screenshots. Without it, saveTo is refused.
	Outputs *mcpkit.Outputs
	// LookupEnv reads the environment for secretEnv. It is os.LookupEnv when nil.
	LookupEnv func(string) (string, bool)
	// OnSecret is told of each value that was typed through secretEnv, besides the scrubber, so that the
	// rest of the server can keep it out of its files too.
	OnSecret func(value string)
	// Baton is the baton of the studio, or nil without one. web_open and web_close only read as far as the
	// guard goes, so that an observing server may still browse and close the window; they are still the
	// agent acting on a window that a person may hold, so they enter the baton as writes.
	Baton *takeover.Baton
}

// Manager owns the one window of a tool server and runs the web tools on it, one call at a time.
type Manager struct {
	mu sync.Mutex // serializes every call that touches the window

	be       WebBackend
	services []Service
	hosts    []string
	policy   *effects.Policy
	guard    mcpkit.Guard
	scrub    *mcpkit.Scrubber
	out      *mcpkit.Outputs
	env      func(string) (string, bool)
	onSecret func(string)
	baton    *takeover.Baton

	br    Browser
	info  OpenInfo
	last  pagelist.List
	fresh bool // last is the list of the page that the window shows
	// savedAt is the mark after which a recording has not been saved yet.
	savedAt int
	// secretEnv is the one variable whose value was typed into this recording, and secrets are the values.
	secretEnv string
	secrets   []string

	defs   []*toolDef
	byName map[string]*toolDef
}

// New makes a Manager. It opens no window: web_open does.
func New(o Options) *Manager {
	m := &Manager{
		be: o.Backend, services: slices.Clone(o.Services), hosts: slices.Clone(o.Hosts), policy: o.Policy,
		guard: o.Guard, scrub: o.Scrub, out: o.Outputs, env: o.LookupEnv, onSecret: o.OnSecret, baton: o.Baton,
	}
	if m.scrub == nil {
		m.scrub = mcpkit.NewScrubber(nil)
	}
	if m.env == nil {
		m.env = os.LookupEnv
	}
	m.defs = m.tools()
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
	// write is true for a tool that the guard refuses while the server only observes.
	write       bool
	destructive bool
	openWorld   bool
	run         func(ctx context.Context, call mcp.Call) (mcp.Result, error)
}

// batonGated are the tools that are not writes for the guard but act on the window, so a person who
// holds control keeps them out.
var batonGated = map[string]bool{"web_open": true, "web_close": true}

// Tools returns the web_* tools in a fixed order. Each one checks the guard, takes the lock, runs, and
// scrubs what it gives back.
func (m *Manager) Tools() []mcp.Tool {
	tools := make([]mcp.Tool, 0, len(m.defs))
	for _, d := range m.defs {
		ann := mcp.Annotations{Title: d.title, ReadOnly: mcp.BoolPtr(!d.write), OpenWorld: mcp.BoolPtr(d.openWorld)}
		if d.write {
			ann.Destructive = mcp.BoolPtr(d.destructive)
		}
		tools = append(tools, mcp.Tool{
			Name: d.name, Title: d.title, Description: d.desc, Input: d.schema, Annotations: ann,
			Surface: string(mcpkit.SurfaceWeb), Write: d.write,
			Handler: func(ctx context.Context, call mcp.Call) (mcp.Result, error) {
				if m.baton != nil && batonGated[d.name] {
					// Before the lock of the window: a person's pause must not wait for a call in line.
					ctx2, leave, err := m.baton.Enter(ctx, takeover.Agent, true)
					if err != nil {
						var he *takeover.ErrHumanInControl
						if errors.As(err, &he) {
							return takeover.HumanInControl(he), nil
						}
						return mcp.Result{}, err
					}
					defer leave(true, "")
					ctx = ctx2
				}
				m.mu.Lock()
				defer m.mu.Unlock()
				return m.invoke(ctx, d, call), nil
			},
		})
	}
	return tools
}

// invoke runs one tool with the lock held: the guard, the tool, and the scrub.
func (m *Manager) invoke(ctx context.Context, d *toolDef, call mcp.Call) mcp.Result {
	if err := m.guard.Allow(mcpkit.SurfaceWeb, d.write); err != nil {
		return mcp.ErrorResult(m.scrub.Text(err.Error()))
	}
	res, err := d.run(ctx, call)
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

// InvalidateList forgets the list of the page, so that the next action must come after a new
// web_list_elements. The studio calls it when a person has used the window, because the page then is not
// the one that the agent last listed.
func (m *Manager) InvalidateList() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.last, m.fresh = pagelist.List{}, false
}

// Active reports whether a window is open.
func (m *Manager) Active() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.br != nil
}

// Info is what the backend said when it opened the window. It is the zero value without a window.
func (m *Manager) Info() OpenInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.info
}

// Mark is how many events the window has heard, and false when no window is open. A journey keeps it
// before a leg and asks EventsFrom for what the leg did.
func (m *Manager) Mark() (int, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.br == nil {
		return 0, false
	}
	return m.br.Mark(), true
}

// EventsFrom gives a copy of the events from index i on, or nil without a window.
func (m *Manager) EventsFrom(i int) []record.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.br == nil {
		return nil
	}
	return m.br.EventsFrom(i)
}

// Location is the address that the window shows.
func (m *Manager) Location(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.br == nil {
		return "", errNoBrowser
	}
	return m.br.Location(ctx)
}

// Vars are the variables that the recording has used, or nil without a window.
func (m *Manager) Vars() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.br == nil {
		return nil
	}
	return m.br.Vars()
}

// Close ends the window, if one is open, and drops what was recorded. It is safe to call more than once.
func (m *Manager) Close(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeLocked()
	return nil
}

func (m *Manager) closeLocked() []record.Event {
	var events []record.Event
	if m.br != nil {
		events = m.br.Close()
	}
	m.br, m.info = nil, OpenInfo{}
	m.last, m.fresh = pagelist.List{}, false
	m.savedAt, m.secretEnv, m.secrets = 0, "", nil
	return events
}

var errNoBrowser = errors.New("no browser is open: call web_open first")

// service finds the service that a call names, or the only one, or the one that holds u.
func (m *Manager) service(name string, u *url.URL) (Service, error) {
	switch {
	case name != "":
		for _, s := range m.services {
			if s.Name == name {
				return s, nil
			}
		}
		names := make([]string, len(m.services))
		for i, s := range m.services {
			names[i] = s.Name
		}
		return Service{}, fmt.Errorf("there is no web service %q; the services are %s", name, strings.Join(names, ", "))
	case u != nil:
		for _, s := range m.services {
			if b, err := url.Parse(s.BaseURL); err == nil && b.Scheme == u.Scheme && b.Host == u.Host &&
				(strings.TrimSuffix(b.Path, "/") == "" || u.Path == strings.TrimSuffix(b.Path, "/") || strings.HasPrefix(u.Path, strings.TrimSuffix(b.Path, "/")+"/")) {
				return s, nil
			}
		}
	}
	if len(m.services) == 1 {
		return m.services[0], nil
	}
	if len(m.services) == 0 {
		return Service{}, errors.New("the environment has no web service")
	}
	return Service{}, errors.New("name the service with service: the environment has several")
}

// resolve turns the arguments of web_open into one address of a service that the environment allows.
func (m *Manager) resolve(a openArgs) (Service, string, error) {
	if a.URL != "" && a.Path != "" {
		return Service{}, "", errors.New("give path or url, not both")
	}
	var svc Service
	var target string
	if a.URL != "" {
		u, err := url.Parse(a.URL)
		if err != nil {
			return Service{}, "", errors.New("the url does not parse")
		}
		svc, err = m.service(a.Service, u)
		if err != nil {
			return Service{}, "", err
		}
		target = a.URL
	} else {
		var err error
		svc, err = m.service(a.Service, nil)
		if err != nil {
			return Service{}, "", err
		}
		path := a.Path
		if path == "" {
			path = svc.Start
		}
		if path == "" {
			path = "/"
		}
		if path == "/" {
			target = svc.BaseURL
		} else {
			target = strings.TrimSuffix(svc.BaseURL, "/") + "/" + strings.TrimLeft(path, "/")
		}
		b, err := url.Parse(svc.BaseURL)
		t, terr := url.Parse(target)
		if err != nil || terr != nil || b.Host != t.Host {
			return Service{}, "", errors.New("the path leaves the service: give a path below its address, or a url")
		}
	}
	if err := m.admit(target); err != nil {
		return Service{}, "", err
	}
	return svc, target, nil
}

// admit checks an address before the window goes there: its host, and a GET of it by the policy.
func (m *Manager) admit(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("the address does not parse")
	}
	if u.User != nil {
		return errors.New("an address with a user name or a password in it is refused")
	}
	if !hostrule.Allowed(u, m.hosts) {
		return fmt.Errorf("the host %s is not one that the environment lists, so the browser does not go there", u.Host)
	}
	if m.policy != nil {
		if err := m.policy.AdmitPage(effects.Request{Method: "GET", URL: u}); err != nil {
			return fmt.Errorf("the policy of the environment refuses the address: %w", err)
		}
	}
	return nil
}
