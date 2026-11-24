package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/yamlfile"
)

type Config struct {
	// TrustIgnored is set when the file still holds the policy.trust setting of Plan 35 and before.
	TrustIgnored bool                   `yaml:"-"`
	Version      int                    `yaml:"version"`
	Environments map[string]Environment `yaml:"environments"`
	AuthProfiles map[string]AuthProfile `yaml:"authProfiles"`
	Redact       Redact                 `yaml:"redact"`
	Replay       Replay                 `yaml:"replay"`
	Providers    []Provider             `yaml:"providers"`
	Tickets      Tickets                `yaml:"tickets"`
	Plan         Plan                   `yaml:"plan"`
	Knowledge    []Knowledge            `yaml:"knowledge"`
	Change       Change                 `yaml:"change"`

	providersDefaulted bool
}

// The kinds of environment that semar.yaml may declare.
const (
	KindTest       = "test"
	KindProduction = "production"
)

type Environment struct {
	// Name is the key of the environment in the file. Load sets it.
	Name     string    `yaml:"-"`
	Kind     string    `yaml:"kind"`
	Hosts    []string  `yaml:"hosts"`
	Methods  []string  `yaml:"methods"`
	Effects  Effects   `yaml:"effects"`
	Services []Service `yaml:"services"`
	// Web is how the browser of the web engine starts in this environment.
	Web Web `yaml:"web"`
	// Android is how a device is reached in this environment.
	Android Android `yaml:"android"`
	// Data is the set of values that tests need, keyed by a dotted name such as customers.default.
	Data map[string]string `yaml:"data"`
	// Checks says what every API response is judged by besides the expected results of its step.
	Checks Checks `yaml:"checks"`
}

// Checks is environments.<name>.checks. Schema is fail, warn, or off, and empty means fail.
type Checks struct {
	Schema       string       `yaml:"schema"`
	ResponseTime ResponseTime `yaml:"responseTime"`
}

// ResponseTime is the time limit of a response. With no Limit nothing is checked. On is warn or
// fail, and empty means warn.
type ResponseTime struct {
	Limit time.Duration `yaml:"limit"`
	On    string        `yaml:"on"`
}

// Web's Chrome is the path of the Chrome program. Empty finds one on the machine. Timeout is how long a
// check of a page waits for its condition before it fails. Zero means 5 seconds.
type Web struct {
	Chrome  string        `yaml:"chrome"`
	Timeout time.Duration `yaml:"timeout"`
}

// Android is environments.<name>.android: how the android engine reaches a device. Every field is
// optional. There is no key for the adb program: a repository cannot choose a host program.
type Android struct {
	// Device is the serial of the device to use. ANDROID_SERIAL wins. With neither, the one running
	// emulator is used, and a physical device only when it is named.
	Device string `yaml:"device"`
	// ServerURL is a W3C WebDriver server, such as Appium or a device farm. With it Semar does not use adb.
	ServerURL string `yaml:"serverUrl"`
	// ServerUser and ServerKey are secret references, env:NAME, for HTTP basic authentication.
	ServerUser string `yaml:"serverUser"`
	ServerKey  string `yaml:"serverKey"`
	// Capabilities are added to the session request of a server at ServerURL.
	Capabilities map[string]any `yaml:"capabilities"`
	// The two UiAutomator2 server packages that adb installs. Both or neither.
	UIAutomator2ServerAPK     string `yaml:"uiautomator2ServerApk"`
	UIAutomator2ServerTestAPK string `yaml:"uiautomator2ServerTestApk"`
	// Timeout is how long a check of a screen waits for its condition. Zero means 5 seconds.
	Timeout time.Duration `yaml:"timeout"`
	// StartTimeout is how long the start of the server on the device may take. Zero means 60 seconds.
	StartTimeout time.Duration `yaml:"startTimeout"`
	// LogcatOnFailure writes the last 200 log lines of the app to the run directory when a mobile step
	// fails. The crash report is written without it.
	LogcatOnFailure bool `yaml:"logcatOnFailure,omitempty"`
}

// The defaults of Android, which a zero value in the file stands for.
const (
	DefaultAndroidTimeout      = 5 * time.Second
	DefaultAndroidStartTimeout = 60 * time.Second
)

// CheckTimeout is how long a check of a screen waits for its condition.
func (a Android) CheckTimeout() time.Duration {
	if a.Timeout <= 0 {
		return DefaultAndroidTimeout
	}
	return a.Timeout
}

// StartLimit is how long the start of the server on the device may take.
func (a Android) StartLimit() time.Duration {
	if a.StartTimeout <= 0 {
		return DefaultAndroidStartTimeout
	}
	return a.StartTimeout
}

// SecretEnvNames lists the variables that serverUser and serverKey name.
func (a Android) SecretEnvNames() []string {
	var out []string
	for _, ref := range []string{a.ServerUser, a.ServerKey} {
		if name, ok := strings.CutPrefix(ref, "env:"); ok && name != "" {
			out = append(out, name)
		}
	}
	return out
}

type Effects struct {
	Mode  string      `yaml:"mode"`
	Deny  []OpPattern `yaml:"deny"`
	Once  []OpPattern `yaml:"once"`
	Reads []OpPattern `yaml:"reads"`
}

type OpPattern struct{ Method, Path string }

type Service struct {
	Name    string      `yaml:"name"`
	BaseURL string      `yaml:"baseUrl"`
	Build   *BuildProbe `yaml:"build"`
	// AuthProfile is the profile that a fixture step to this service uses. Empty means the
	// profile of the scenario.
	AuthProfile string `yaml:"authProfile"`
	// Web is set when the service has a web page whose address differs from the base URL.
	Web *ServiceWeb `yaml:"web"`
	// Android is set when the service has an app for android.
	Android *ServiceAndroid `yaml:"android"`
}

// ServiceAndroid is the app of a service on a device. AppPackage is required.
type ServiceAndroid struct {
	AppPackage string `yaml:"appPackage"`
	// AppActivity is the activity to start. Empty means the launcher activity.
	AppActivity string `yaml:"appActivity"`
	// APK is a file of the repository, or an absolute path. Semar installs it, and its digest is the build.
	APK string `yaml:"apk"`
	// App is what a server at a non-loopback serverUrl installs, such as bs://abc. It excludes APK.
	App string `yaml:"app"`
	// Reset is what happens before each scenario: restart (the default), clear, or none.
	Reset string `yaml:"reset"`
	// Schemes are the custom schemes that an open may use, besides http and https.
	Schemes []string `yaml:"schemes"`
	// Permissions are granted after each reset.
	Permissions []string `yaml:"permissions"`
}

// The values of ServiceAndroid.Reset.
const (
	ResetRestart = "restart"
	ResetClear   = "clear"
	ResetNone    = "none"
)

// ResetMode is the reset of the service, with the default applied.
func (a ServiceAndroid) ResetMode() string {
	if a.Reset == "" {
		return ResetRestart
	}
	return a.Reset
}

// ServiceWeb says where the web page of a service starts: a path below its base URL.
type ServiceWeb struct {
	Start string `yaml:"start"`
}

// WebStart is the path where the web page of the service starts, and / when none is named.
func (s Service) WebStart() string {
	if s.Web == nil {
		return "/"
	}
	return s.Web.Start
}

// BuildProbe's Probe is "http" and Commit is a JSONPath.
type BuildProbe struct{ Probe, URL, Commit string }

type AuthProfile struct {
	// Description is one optional line, at most 200 characters, that tells the planner what the
	// account can do. It is shown to the model, so it holds no control or format character.
	Description string            `yaml:"description"`
	Session     *SessionSpec      `yaml:"session"`
	Secrets     map[string]string `yaml:"secrets"`
	// Web is how the account signs in to the web application. Nil means Semar never signs in as
	// this account.
	Web *WebSignIn `yaml:"web"`
}

// HasToken reports whether an API step can send the account's session. An account with no session
// and no secrets has none: its web steps use the browser profile that semar login fills, and an API
// step as it is blocked.
func (p AuthProfile) HasToken() bool { return p.Session != nil || len(p.Secrets) > 0 }

// WebSignIn is the user and the password of an account on a sign-in form. Password is a secret
// reference, env:NAME, and never the password.
type WebSignIn struct {
	User     string `yaml:"user"`
	Password string `yaml:"password"`
}

// PasswordEnv is the name of the variable that holds the password.
func (w WebSignIn) PasswordEnv() string { return strings.TrimPrefix(w.Password, "env:") }

// SessionSpec's Value may hold {{secret.NAME}}.
type SessionSpec struct{ Header, Value string }

type Redact struct {
	JSONPaths []string `yaml:"jsonPaths"`
	Headers   []string `yaml:"headers"`
	Patterns  []string `yaml:"patterns"`
}

type Replay struct {
	Workers int `yaml:"workers"`
	// Retries is how many more times a scenario that failed or was inconclusive is replayed. 0 to 5.
	Retries int `yaml:"retries"`
}

// MaxRetries is the most that replay.retries and --retries may ask for.
const MaxRetries = 5

// Provider's Kind names a factory in the provider registry. Outputs is for the fake kind only.
//
// The repository's semar.yaml sets the first four fields only. The others come from the
// person's own providers file, because an address, a key, or a setup switch from a
// repository under test could send the source and the key elsewhere, or widen what the
// planner may call.
type Provider struct {
	ID      string   `yaml:"id"`
	Kind    string   `yaml:"kind"`
	Model   string   `yaml:"model"`
	Outputs []string `yaml:"outputs"`

	BaseURL string `yaml:"-"` // API kinds: the address of the model API
	APIKey  string `yaml:"-"` // API kinds: a reference such as env:NAME or keychain:NAME, never the key
	Setup   Setup  `yaml:"-"` // harness kinds: what of the person's own harness setup is loaded
	User    bool   `yaml:"-"` // true when the person's own file defines this provider

	Reasoning string `yaml:"-"` // the reasoning level; "" means the provider's own default
}

// Setup says which parts of the person's own harness setup a job may use. The zero value
// loads nothing.
type Setup struct {
	MCP    bool     // the person's MCP servers
	Skills bool     // the person's skills
	Deny   []string // tool names or patterns the job may never call
}

// Change says what the change of a branch is measured against. Base is the branch that a release
// goes into. Empty is the default branch of origin, else main, else master.
type Change struct {
	Base string `yaml:"base"`
}

type Tickets struct {
	Pattern string `yaml:"pattern"`
}

type Plan struct {
	Provider     string        `yaml:"provider"`
	MaxScenarios int           `yaml:"maxScenarios"`
	MaxTurns     int           `yaml:"maxTurns"`
	Timeout      time.Duration `yaml:"timeout"`
	// Trial is false when the person turned the trial run off. Nil means on.
	Trial *bool `yaml:"trial"`
	// MaxEdgeCases is the most edge cases that one plan drafts from the API document, and 0 turns them off.
	// Nil means DefaultMaxEdgeCases.
	MaxEdgeCases *int `yaml:"maxEdgeCases"`
}

// TrialOn reports whether semar plan sends the read-only steps of a draft once.
func (p Plan) TrialOn() bool { return p.Trial == nil || *p.Trial }

// DefaultMaxEdgeCases is the most edge cases that one plan drafts when plan.maxEdgeCases is not set.
const DefaultMaxEdgeCases = 20

// EdgeLimit is the most edge cases that one plan drafts. 0 turns them off.
func (p Plan) EdgeLimit() int {
	if p.MaxEdgeCases == nil {
		return DefaultMaxEdgeCases
	}
	return *p.MaxEdgeCases
}

const (
	fileName             = "semar.yaml"
	defaultWorkers       = 4
	defaultTicketPattern = `[A-Z][A-Z0-9]+-[0-9]+`
	defaultMaxScenarios  = 12
	defaultMaxTurns      = 60
	defaultPlanTimeout   = 15 * time.Minute
)

var topLevelKeys = []string{
	"version", "environments", "authProfiles", "redact", "replay",
	"providers", "tickets", "plan", "knowledge", "change",
}

// laterPlanKeys belong to plans that are not built yet. They are accepted and ignored.
var laterPlanKeys = []string{
	"observe", "web", "android", "access", "databases", "repos", "risk",
	"mcpServers", "mcpTools", "publish", "regression", "security",
	"explore", "notify", "decisions", "agents", "assign", "passes", "limits",
	"evidence",
}

func Load(repoRoot string) (Config, error) {
	path := filepath.Join(repoRoot, ".semar", fileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read configuration %s: %w", path, err)
	}
	return parse(data, fileName)
}

func parse(data []byte, name string) (Config, error) {
	var doc yaml.Node
	if err := yamlfile.Decode(data, name, &doc); err != nil {
		return Config{}, err
	}
	top := doc.Content[0]
	if top.Kind != yaml.MappingNode {
		return Config{}, fmt.Errorf("%s:%d: the configuration must be a mapping", name, top.Line)
	}
	var unknown []error
	trustIgnored := false
	known := make([]*yaml.Node, 0, len(top.Content))
	for i := 0; i+1 < len(top.Content); i += 2 {
		key := top.Content[i]
		switch {
		case key.Value == "policy":
			ignored, errs := oldPolicy(top.Content[i+1], name)
			trustIgnored = ignored
			unknown = append(unknown, errs...)
		case slices.Contains(laterPlanKeys, key.Value):
		case slices.Contains(topLevelKeys, key.Value):
			known = append(known, key, top.Content[i+1])
		default:
			unknown = append(unknown, fmt.Errorf("%s:%d: unknown key %s", name, key.Line, key.Value))
		}
	}
	for i := 0; i+1 < len(known); i += 2 {
		if known[i].Value == "providers" {
			unknown = append(unknown, binaryPathKeys(known[i+1], name)...)
		}
	}
	if len(unknown) > 0 {
		return Config{}, joinErrors(unknown)
	}
	top.Content = known
	var cfg Config
	if err := yamlfile.LoadNode(top, name, &cfg); err != nil {
		return Config{}, err
	}
	cfg.TrustIgnored = trustIgnored
	if cfg.Replay.Workers == 0 {
		cfg.Replay.Workers = defaultWorkers
	}
	applyPlanDefaults(&cfg)
	if err := validate(cfg, name); err != nil {
		return Config{}, err
	}
	for n, e := range cfg.Environments {
		e.Name = n
		cfg.Environments[n] = e
	}
	// Every later reader sees the clean path. validate has accepted it, so one trailing
	// slash is the only thing left to remove.
	for i := range cfg.Knowledge {
		cfg.Knowledge[i].Path = strings.TrimSuffix(cfg.Knowledge[i].Path, "/")
	}
	return cfg, nil
}

// TrustNotice is the one line that run and plan print when semar.yaml still sets policy.trust.confirm.
const TrustNotice = "policy.trust.confirm in .semar/semar.yaml is no longer used and can be removed"

// oldPolicy reads the key policy of a semar.yaml written before Plan 36. Nothing under it is used
// now, but a file that holds only the old trust setting still loads. It reports that the setting was
// there, and an unknown key under policy or policy.trust is an error as it was.
func oldPolicy(n *yaml.Node, name string) (ignored bool, errs []error) {
	n = resolveAlias(n)
	if n.Kind != yaml.MappingNode {
		if n.Tag == "!!null" {
			return true, nil
		}
		return false, []error{fmt.Errorf("%s:%d: policy must be a mapping", name, n.Line)}
	}
	ignored = true
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i], resolveAlias(n.Content[i+1])
		if key.Value != "trust" {
			errs = append(errs, fmt.Errorf("%s:%d: unknown key policy.%s", name, key.Line, key.Value))
			continue
		}
		if val.Kind != yaml.MappingNode {
			if val.Tag != "!!null" {
				errs = append(errs, fmt.Errorf("%s:%d: policy.trust must be a mapping", name, val.Line))
			}
			continue
		}
		for j := 0; j+1 < len(val.Content); j += 2 {
			k, v := val.Content[j], resolveAlias(val.Content[j+1])
			switch {
			case k.Value != "confirm":
				errs = append(errs, fmt.Errorf("%s:%d: unknown key policy.trust.%s", name, k.Line, k.Value))
			case v.Kind != yaml.SequenceNode && v.Tag != "!!null":
				errs = append(errs, fmt.Errorf("%s:%d: policy.trust.confirm must be a list", name, v.Line))
			}
		}
	}
	return ignored, errs
}

// binaryPathKeys refuses providers[].binaryPath: the configuration is committed in the
// repository under test, so a path there would let that repository choose the program
// that semar starts.
func binaryPathKeys(providers *yaml.Node, name string) []error {
	providers = resolveAlias(providers)
	if providers.Kind != yaml.SequenceNode {
		return nil
	}
	var errs []error
	for i, item := range providers.Content {
		item = resolveAlias(item)
		if item.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(item.Content); j += 2 {
			if key := item.Content[j]; key.Value == "binaryPath" {
				errs = append(errs, fmt.Errorf("%s:%d: providers[%d].binaryPath: the path of a provider binary is not read from the repository's configuration; set the environment variable SEMAR_CLAUDE_BIN to an absolute path instead", name, key.Line, i))
			}
		}
	}
	return errs
}

func resolveAlias(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		return n.Alias
	}
	return n
}

func applyPlanDefaults(cfg *Config) {
	if len(cfg.Providers) == 0 {
		cfg.Providers = []Provider{{ID: "claude", Kind: "claude"}}
		cfg.providersDefaulted = true
	}
	if cfg.Tickets.Pattern == "" {
		cfg.Tickets.Pattern = defaultTicketPattern
	}
	if cfg.Plan.MaxScenarios == 0 {
		cfg.Plan.MaxScenarios = defaultMaxScenarios
	}
	if cfg.Plan.MaxTurns == 0 {
		cfg.Plan.MaxTurns = defaultMaxTurns
	}
	if cfg.Plan.Timeout == 0 {
		cfg.Plan.Timeout = defaultPlanTimeout
	}
}

// PlanProvider returns the provider that plan.provider names, or the first one.
func (c Config) PlanProvider() Provider {
	for _, p := range c.Providers {
		if p.ID == c.Plan.Provider {
			return p
		}
	}
	return c.Providers[0]
}

// Declared returns the providers that the file lists, and none when it lists none.
func (c Config) Declared() []Provider {
	if c.providersDefaulted {
		return nil
	}
	return c.Providers
}

// PlanProviderID is the provider that the file asks to plan: plan.provider, else the first
// one it lists. It is empty when the file names none. The person's own file may define the
// provider, so the ID is not checked against Declared here.
func (c Config) PlanProviderID() string {
	if c.Plan.Provider != "" {
		return c.Plan.Provider
	}
	if d := c.Declared(); len(d) > 0 {
		return d[0].ID
	}
	return ""
}

// Environment returns the named environment. An empty name selects the only
// environment, and is an error when there are none or several.
func (c Config) Environment(name string) (string, Environment, error) {
	names := make([]string, 0, len(c.Environments))
	for n := range c.Environments {
		names = append(names, n)
	}
	slices.Sort(names)
	list := strings.Join(names, ", ")
	if name == "" {
		switch len(names) {
		case 0:
			return "", Environment{}, fmt.Errorf("the configuration defines no environment")
		case 1:
			return names[0], c.Environments[names[0]], nil
		}
		return "", Environment{}, fmt.Errorf("choose an environment: %s", list)
	}
	env, ok := c.Environments[name]
	if !ok {
		return "", Environment{}, fmt.Errorf("unknown environment %q, known environments: %s", name, list)
	}
	return name, env, nil
}
