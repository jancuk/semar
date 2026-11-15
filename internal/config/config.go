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
}

type Environment struct {
	Kind     string    `yaml:"kind"`
	Hosts    []string  `yaml:"hosts"`
	Methods  []string  `yaml:"methods"`
	Effects  Effects   `yaml:"effects"`
	Services []Service `yaml:"services"`
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
}

// BuildProbe's Probe is "http" and Commit is a JSONPath.
type BuildProbe struct{ Probe, URL, Commit string }

type AuthProfile struct {
	Session *SessionSpec      `yaml:"session"`
	Secrets map[string]string `yaml:"secrets"`
}

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
type Provider struct {
	ID      string   `yaml:"id"`
	Kind    string   `yaml:"kind"`
	Model   string   `yaml:"model"`
	Outputs []string `yaml:"outputs"`
}

type Tickets struct {
	Pattern string `yaml:"pattern"`
}

type Plan struct {
	Provider     string        `yaml:"provider"`
	MaxScenarios int           `yaml:"maxScenarios"`
	MaxTurns     int           `yaml:"maxTurns"`
	Timeout      time.Duration `yaml:"timeout"`
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
	"version", "environments", "authProfiles", "redact", "policy", "replay",
	"providers", "tickets", "plan",
}

// laterPlanKeys belong to plans that are not built yet. They are accepted and ignored.
var laterPlanKeys = []string{
	"observe", "web", "android", "access", "databases", "repos", "risk",
	"knowledge", "mcpServers", "mcpTools", "publish", "regression", "security",
	"explore", "notify", "decisions", "agents", "assign", "passes", "limits",
	"evidence", "change",
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
