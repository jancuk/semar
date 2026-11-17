// Package userconfig reads and writes the person's own providers.yaml, which defines the
// model providers. Only this file may set an address, a key reference, or the setup
// switches, because the repository under test must not be able to.
package userconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/keychain"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/yamlfile"
)

const FileName = "providers.yaml"

const header = `# Semar model providers.
# The dashboard rewrites this file when you change a provider, and comments are not kept.
# A key is never written here: apiKey is env:NAME or keychain:NAME.
`

// Home returns the directory of the file: $SEMAR_HOME, else $XDG_CONFIG_HOME/semar,
// else ~/.config/semar.
func Home(lookupEnv func(string) (string, bool)) (string, error) {
	if v, ok := lookupEnv("SEMAR_HOME"); ok && v != "" {
		return absolute("SEMAR_HOME", v)
	}
	if v, ok := lookupEnv("XDG_CONFIG_HOME"); ok && v != "" {
		p, err := absolute("XDG_CONFIG_HOME", v)
		if err != nil {
			return "", err
		}
		return filepath.Join(p, "semar"), nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find the home directory: %w: set SEMAR_HOME to the directory for %s", err, FileName)
	}
	return filepath.Join(h, ".config", "semar"), nil
}

func absolute(name, v string) (string, error) {
	if !filepath.IsAbs(v) {
		return "", fmt.Errorf("%s must be an absolute path, got %q", name, v)
	}
	return filepath.Clean(v), nil
}

// File is the content of providers.yaml.
type File struct {
	Version   int        `yaml:"version"`
	Default   string     `yaml:"default"`
	Plan      Plan       `yaml:"plan,omitempty"`
	Providers []Provider `yaml:"providers"`
}

// Plan holds what the person decides about planning for every provider. It is read from this
// file only, because the repository under test must not be able to widen what the planner loads.
type Plan struct {
	// WithSetup lets a plan job of a harness kind load the MCP servers and skills that the
	// provider's setup allows. They add tokens to every turn, so a plan runs without them
	// unless this is true or the run passes --with-setup.
	WithSetup bool `yaml:"withSetup,omitempty"`
	// Ladder is the models of a plan that runs with --pipeline criteria, the cheapest first, each
	// as <provider id>:<model>. A job goes up a step when the answer of the step below it fails
	// its checks twice. It is the person's choice and never the repository's, because it decides
	// which provider receives the repository's code.
	Ladder []string `yaml:"ladder,omitempty"`
	// MaxCost is the most that one plan may spend, in dollars, as the providers report it. When the
	// sum reaches it, no new job starts. 0 sets no limit. It is the person's choice and never the
	// repository's, because it decides what the person spends.
	MaxCost float64 `yaml:"maxCost,omitempty"`
}

// Provider is one entry. BaseURL and APIKey belong to the API kinds and Setup to the
// harness kinds.
type Provider struct {
	ID        string `yaml:"id"`
	Kind      string `yaml:"kind"`
	Model     string `yaml:"model,omitempty"`
	Reasoning string `yaml:"reasoning,omitempty"`
	BaseURL   string `yaml:"baseUrl,omitempty"`
	APIKey    string `yaml:"apiKey,omitempty"`
	Setup     *Setup `yaml:"setup,omitempty"`
}

// Setup holds the switches of a harness kind. A nil switch means true.
type Setup struct {
	MCP    *bool    `yaml:"mcp,omitempty"`
	Skills *bool    `yaml:"skills,omitempty"`
	Deny   []string `yaml:"deny,omitempty"`
}

// Builtin is what a missing file means: Claude Code, as the default.
func Builtin() File {
	return File{Version: 1, Default: "claude", Providers: []Provider{{ID: "claude", Kind: provider.KindClaude}}}
}

// Load reads <home>/providers.yaml. A missing file gives Builtin() and exists false.
func Load(home string) (f File, exists bool, err error) {
	path := filepath.Join(home, FileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Builtin(), false, nil
	}
	if err != nil {
		return File{}, true, fmt.Errorf("read %s: %w", path, err)
	}
	if err := yamlfile.Decode(data, path, &f); err != nil {
		return File{}, true, err
	}
	if err := f.Validate(); err != nil {
		return File{}, true, fmt.Errorf("%s: %w", path, err)
	}
	return f, true, nil
}

// Save validates f and writes it atomically, with mode 0600 in a directory of mode 0700.
// It refuses a file or directory that is a symbolic link instead of writing through it.
func Save(home string, f File) error {
	if err := f.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(f.normalized())
	if err != nil {
		return fmt.Errorf("encode %s: %w", FileName, err)
	}
	if err := ensureHome(home); err != nil {
		return err
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return fmt.Errorf("open %s: %w", home, err)
	}
	defer root.Close()
	path := filepath.Join(home, FileName)
	switch info, err := root.Lstat(FileName); {
	case err == nil && info.Mode()&fs.ModeSymlink != 0:
		return fmt.Errorf("%s is a symbolic link, and Semar writes only a real file: replace the link with a file", path)
	case err == nil && !info.Mode().IsRegular():
		return fmt.Errorf("%s is not a regular file", path)
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("check %s: %w", path, err)
	}
	return atomicfile.WriteIn(root, FileName, append([]byte(header), data...), atomicfile.FileMode)
}

// ensureHome creates the directory with mode 0700 when it is missing. An existing directory
// keeps its mode, because it may be one the person shares with other tools.
func ensureHome(home string) error {
	info, err := os.Lstat(home)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(home, atomicfile.DirMode); err != nil {
			return fmt.Errorf("create directory %s: %w", home, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("check %s: %w", home, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		if info, err = os.Stat(home); err != nil {
			return fmt.Errorf("check %s: %w", home, err)
		}
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", home)
	}
	return nil
}

// normalized returns a copy in which a harness kind states both switches.
func (f File) normalized() File {
	out := File{Version: f.Version, Default: f.Default, Plan: f.Plan, Providers: make([]Provider, len(f.Providers))}
	for i, p := range f.Providers {
		if provider.IsHarness(p.Kind) {
			s := resolveSetup(p.Setup)
			p.Setup = &Setup{MCP: &s.MCP, Skills: &s.Skills, Deny: slices.Clone(s.Deny)}
		}
		out.Providers[i] = p
	}
	return out
}

func resolveSetup(s *Setup) config.Setup {
	out := config.Setup{MCP: true, Skills: true}
	if s == nil {
		return out
	}
	if s.MCP != nil {
		out.MCP = *s.MCP
	}
	if s.Skills != nil {
		out.Skills = *s.Skills
	}
	out.Deny = slices.Clone(s.Deny)
	return out
}

// Validate checks rules F2 to F7. Errors name the provider and never quote an apiKey value,
// because a person may have pasted a key there.
func (f File) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if f.Version != 1 {
		add("%s: version must be 1", FileName)
	}
	seen := map[string]bool{}
	// A keychain may ignore case in its names, so IDs that differ only in case would share
	// the entry keychain:<id>.
	folded := map[string]string{}
	for i, p := range f.Providers {
		where := fmt.Sprintf("%s: provider %d", FileName, i+1)
		if err := idcheck.Check("provider id", p.ID); err != nil {
			add("%s: %v", where, err)
			continue
		}
		where = fmt.Sprintf("%s: provider %q", FileName, p.ID)
		other, clash := folded[strings.ToLower(p.ID)]
		switch {
		case seen[p.ID]:
			add("%s: the id is used twice", where)
		case clash:
			add("%s: the id differs from %q only in case; give one of them another id", where, other)
		default:
			folded[strings.ToLower(p.ID)] = p.ID
		}
		seen[p.ID] = true
		errs = append(errs, validateProvider(where, p)...)
	}
	switch {
	case f.Default == "":
		add("%s: default must name a provider", FileName)
	case !seen[f.Default]:
		add("%s: default %q is not one of the providers", FileName, f.Default)
	}
	if c := f.Plan.MaxCost; c < 0 || math.IsNaN(c) || math.IsInf(c, 0) {
		add("%s: plan.maxCost must be a positive number of dollars, or left out", FileName)
	}
	for _, step := range f.Plan.Ladder {
		id, _, err := config.ParseRung(step)
		switch {
		case err != nil:
			add("%s: plan.ladder: %v", FileName, err)
		case !seen[id]:
			add("%s: plan.ladder: %q names the provider %q, which is not one of the providers", FileName, step, id)
		}
	}
	return errors.Join(errs...)
}

func validateProvider(where string, p Provider) []error {
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: "+format, append([]any{where}, args...)...))
	}
	switch {
	case p.Kind == provider.KindFake:
		add("the kind %q is for tests and cannot be used here", p.Kind)
		return errs
	case !provider.IsHarness(p.Kind) && !provider.IsAPI(p.Kind):
		add("kind %q is not one of claude, codex, anthropic, openai", p.Kind)
		return errs
	}
	if p.Model != "" {
		if err := config.CheckModel(p.Model); err != nil {
			add("model: %v", err)
		}
	}
	if p.Reasoning != "" {
		if err := config.CheckReasoning(p.Reasoning); err != nil {
			add("reasoning: %v", err)
		}
	}
	if provider.IsHarness(p.Kind) {
		if p.BaseURL != "" {
			add("baseUrl is for the anthropic and openai kinds, and %s signs in by itself", p.Kind)
		}
		if p.APIKey != "" {
			add("apiKey is for the anthropic and openai kinds, and %s signs in by itself", p.Kind)
		}
		if p.Setup != nil {
			if err := config.CheckDeny(p.Setup.Deny); err != nil {
				add("%v", err)
			}
		}
		return errs
	}
	if p.Setup != nil {
		add("setup is for the claude and codex kinds, and an API provider has no MCP servers or skills")
	}
	loopback := false
	if p.BaseURL != "" {
		var err error
		if loopback, err = checkBaseURL(p.BaseURL); err != nil {
			add("baseUrl: %v", err)
		}
	}
	switch {
	case p.APIKey != "":
		if err := keychain.CheckRef(p.APIKey); err != nil {
			add("apiKey: %v", err)
		}
	case !loopback:
		add("apiKey is required: write env:NAME or keychain:NAME")
	}
	return errs
}

// checkBaseURL returns whether the host is a loopback address.
func checkBaseURL(raw string) (loopback bool, err error) {
	u, perr := url.Parse(raw)
	if perr != nil || u.Host == "" || u.Hostname() == "" {
		return false, errors.New("the address is not a URL such as https://api.example.com")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return false, errors.New("the address must not hold user info, a query, or a fragment")
	}
	loopback = isLoopback(u.Hostname())
	switch u.Scheme {
	case "https":
	case "http":
		if !loopback {
			return false, errors.New("http is allowed only for a loopback address such as localhost; use https")
		}
	default:
		return false, errors.New("the address must start with https://")
	}
	return loopback, nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Find returns the provider with the id as a config.Provider with User true.
func (f File) Find(id string) (config.Provider, bool) {
	for _, p := range f.Providers {
		if p.ID == id {
			return p.toConfig(), true
		}
	}
	return config.Provider{}, false
}

// All returns every provider in file order.
func (f File) All() []config.Provider {
	out := make([]config.Provider, len(f.Providers))
	for i, p := range f.Providers {
		out[i] = p.toConfig()
	}
	return out
}

func (p Provider) toConfig() config.Provider {
	c := config.Provider{ID: p.ID, Kind: p.Kind, Model: p.Model, Reasoning: p.Reasoning, BaseURL: p.BaseURL, APIKey: p.APIKey, User: true}
	if provider.IsHarness(p.Kind) {
		c.Setup = resolveSetup(p.Setup)
	}
	return c
}
