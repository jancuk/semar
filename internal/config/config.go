package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/yamlfile"
)

type Config struct {
	Version      int                    `yaml:"version"`
	Environments map[string]Environment `yaml:"environments"`
	AuthProfiles map[string]AuthProfile `yaml:"authProfiles"`
	Redact       Redact                 `yaml:"redact"`
	Policy       Policy                 `yaml:"policy"`
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
	Kind     string    `yaml:"kind"`
	Hosts    []string  `yaml:"hosts"`
	Methods  []string  `yaml:"methods"`
	Effects  Effects   `yaml:"effects"`
	Services []Service `yaml:"services"`
	// Web is how the browser of the web engine starts in this environment.
	Web Web `yaml:"web"`
	// Data is the set of values that tests need, keyed by a dotted name such as customers.default.
	Data map[string]string `yaml:"data"`
}

// Web's Chrome is the path of the Chrome program. Empty finds one on the machine.
type Web struct {
	Chrome string `yaml:"chrome"`
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

type Policy struct {
	Trust Trust `yaml:"trust"`
}

type Trust struct {
	Confirm []domain.Source `yaml:"confirm"`
}

type Replay struct {
	Workers int `yaml:"workers"`
}

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
}

// TrialOn reports whether semar plan sends the read-only steps of a draft once.
func (p Plan) TrialOn() bool { return p.Trial == nil || *p.Trial }

const (
	fileName             = "semar.yaml"
	defaultWorkers       = 4
	defaultTicketPattern = `[A-Z][A-Z0-9]+-[0-9]+`
	defaultMaxScenarios  = 12
	defaultMaxTurns      = 60
	defaultPlanTimeout   = 15 * time.Minute
)

var topLevelKeys = []string{
	"version", "environments", "authProfiles", "redact", "policy", "replay",
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
	known := make([]*yaml.Node, 0, len(top.Content))
	for i := 0; i+1 < len(top.Content); i += 2 {
		key := top.Content[i]
		switch {
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
	if len(cfg.Policy.Trust.Confirm) == 0 {
		cfg.Policy.Trust.Confirm = []domain.Source{domain.SourceDiff, domain.SourceBaseline}
	}
	if cfg.Replay.Workers == 0 {
		cfg.Replay.Workers = defaultWorkers
	}
	applyPlanDefaults(&cfg)
	if err := validate(cfg, name); err != nil {
		return Config{}, err
	}
	// Every later reader sees the clean path. validate has accepted it, so one trailing
	// slash is the only thing left to remove.
	for i := range cfg.Knowledge {
		cfg.Knowledge[i].Path = strings.TrimSuffix(cfg.Knowledge[i].Path, "/")
	}
	return cfg, nil
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
