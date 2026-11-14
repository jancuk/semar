package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

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

const (
	fileName       = "semar.yaml"
	defaultWorkers = 4
)

var topLevelKeys = []string{"version", "environments", "authProfiles", "redact", "policy", "replay"}

// laterPlanKeys belong to plans that are not built yet. They are accepted and ignored.
var laterPlanKeys = []string{
	"observe", "web", "android", "access", "databases", "repos", "risk", "tickets",
	"knowledge", "mcpServers", "mcpTools", "publish", "plan", "regression", "security",
	"explore", "notify", "decisions", "providers", "agents", "assign", "passes", "limits",
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
	if err := validate(cfg, name); err != nil {
		return Config{}, err
	}
	return cfg, nil
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
