package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/theory/jsonpath"
	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/hostrule"
	"github.com/jancuk/semar/internal/idcheck"
)

const (
	defaultEnvName    = "uat"
	defaultAuthHeader = "Authorization"
	tokenPlaceholder  = "{{secret.TOKEN}}"
	defaultAuthValue  = "Bearer " + tokenPlaceholder
	defaultTokenEnv   = "SEMAR_TOKEN"
	defaultBuildPath  = "$.commit"
)

// initFlags are the flags of semar init. plan takes the same flags.
type initFlags struct {
	baseURL, env, service, authHeader, authValue, tokenEnv string
	buildURL, buildCommit                                  string
}

var initFlagNames = []string{"base-url", "service", "auth-header", "auth-value", "token-env", "build-url", "build-commit"}

func addInitFlags(cmd *cobra.Command, f *initFlags) {
	cmd.Flags().StringVar(&f.baseURL, "base-url", "", "the address of the service in the test environment, such as https://orders.uat.example.com")
	cmd.Flags().StringVar(&f.service, "service", "", "the name of the service (default: the name of the repository directory)")
	cmd.Flags().StringVar(&f.authHeader, "auth-header", defaultAuthHeader, "the header that carries the session")
	cmd.Flags().StringVar(&f.authValue, "auth-value", defaultAuthValue, "the value of that header; it must hold "+tokenPlaceholder)
	cmd.Flags().StringVar(&f.tokenEnv, "token-env", defaultTokenEnv, "the environment variable that holds the token")
	cmd.Flags().StringVar(&f.buildURL, "build-url", "", "an address on the host of --base-url that answers with the commit the service runs, such as https://orders.uat.example.com/version")
	cmd.Flags().StringVar(&f.buildCommit, "build-commit", "", "the JSONPath of the commit in the answer of --build-url (default "+defaultBuildPath+")")
}

// initFlagGiven reports whether any flag that only init needs was set.
func initFlagGiven(cmd *cobra.Command) bool {
	for _, name := range initFlagNames {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
}

func newInitCommand() *cobra.Command {
	var f initFlags
	var dir string
	cmd := &cobra.Command{
		Use:   "init --base-url <url>",
		Short: "Write .semar/semar.yaml for one test environment",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if f.baseURL == "" {
				return errors.New("--base-url is required: the address of the service in the test environment")
			}
			data, err := buildInitConfig(dir, f)
			if err != nil {
				return err
			}
			path, err := writeInitConfig(dir, data)
			if err != nil {
				return err
			}
			printInitDone(cmd.OutOrStdout(), path, f)
			return nil
		},
	}
	addInitFlags(cmd, &f)
	cmd.Flags().StringVar(&f.env, "env", defaultEnvName, "the name of the test environment")
	addDirFlag(cmd, &dir)
	return cmd
}

func configPath(dir string) string { return filepath.Join(semarDir(dir), "semar.yaml") }

func printInitDone(w io.Writer, path string, f initFlags) {
	fmt.Fprintf(w, "wrote %s\n", path)
	fmt.Fprintf(w, "export %s=<the token of the test account> before semar run\n", f.tokenEnv)
}

var (
	envVarPattern     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	headerNamePattern = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]+$")
)

// buildInitConfig checks the flags and returns the configuration that init writes.
func buildInitConfig(dir string, f initFlags) ([]byte, error) {
	u, err := url.Parse(f.baseURL)
	switch {
	case err != nil:
		return nil, fmt.Errorf("--base-url %q is not a URL: %w", f.baseURL, err)
	case u.Scheme != "http" && u.Scheme != "https":
		return nil, fmt.Errorf("--base-url %q must start with http:// or https://", f.baseURL)
	case u.Hostname() == "":
		return nil, fmt.Errorf("--base-url %q names no host", f.baseURL)
	case u.User != nil:
		return nil, errors.New("--base-url must not hold a user name or a password; the token goes in the environment variable of --token-env")
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(f.baseURL, "#"):
		return nil, fmt.Errorf("--base-url %q must not hold a query or a fragment", f.baseURL)
	}
	env := f.env
	if env == "" {
		env = defaultEnvName
	}
	if err := idcheck.Check("environment name", env); err != nil {
		return nil, fmt.Errorf("--env: %w", err)
	}
	service := f.service
	if service == "" {
		service = defaultServiceName(dir)
	} else if err := idcheck.Check("service name", service); err != nil {
		return nil, fmt.Errorf("--service: %w", err)
	}
	if !headerNamePattern.MatchString(f.authHeader) {
		return nil, fmt.Errorf("--auth-header %q is not a header name", f.authHeader)
	}
	if !strings.Contains(f.authValue, tokenPlaceholder) || strings.Count(f.authValue, "{{") != 1 {
		return nil, fmt.Errorf("--auth-value must hold %s once and no other {{", tokenPlaceholder)
	}
	if strings.ContainsFunc(f.authValue, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return nil, errors.New("--auth-value must not hold a control character")
	}
	if !envVarPattern.MatchString(f.tokenEnv) {
		return nil, fmt.Errorf("--token-env %q is not the name of an environment variable", f.tokenEnv)
	}
	probe, err := buildProbe(u, f)
	if err != nil {
		return nil, err
	}

	flow := func(n *yaml.Node) *yaml.Node { n.Style = yaml.FlowStyle; return n }
	doc := mapping(
		"version", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"},
		"environments", mapping(
			env, mapping(
				"kind", text("test"),
				"hosts", flow(sequence(quoted(strings.ToLower(u.Host)))),
				"methods", flow(sequence(text("GET"), text("POST"), text("PUT"), text("PATCH"), text("DELETE"))),
				"effects", mapping("mode", text("test_data")),
				"services", sequence(serviceNode(service, f.baseURL, probe)),
			),
		),
		"authProfiles", mapping(
			"default", mapping(
				"session", flow(mapping("header", text(f.authHeader), "value", quoted(f.authValue))),
				"secrets", flow(mapping("TOKEN", quoted("env:"+f.tokenEnv))),
			),
		),
	)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{doc}}); err != nil {
		return nil, fmt.Errorf("write the configuration: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("write the configuration: %w", err)
	}
	return buf.Bytes(), nil
}

// buildProbe checks --build-url and --build-commit, and returns nil when no probe was asked for.
// The probe must be on the host of --base-url, the only host that init lists for the environment.
func buildProbe(base *url.URL, f initFlags) (*config.BuildProbe, error) {
	if f.buildURL == "" {
		if f.buildCommit != "" {
			return nil, errors.New("--build-commit needs --build-url: the address that answers with the commit the service runs")
		}
		return nil, nil
	}
	u, err := url.Parse(f.buildURL)
	switch {
	case err != nil:
		return nil, fmt.Errorf("--build-url %q is not a URL: %w", f.buildURL, err)
	case u.Scheme != "http" && u.Scheme != "https" || u.Hostname() == "":
		return nil, fmt.Errorf("--build-url %q must be an absolute http:// or https:// URL", f.buildURL)
	case u.User != nil:
		return nil, errors.New("--build-url must not hold a user name or a password")
	case !hostrule.Allowed(u, []string{strings.ToLower(base.Host)}):
		return nil, fmt.Errorf("--build-url %q must be on the host of --base-url, %s, because Semar reaches only the hosts of the environment", f.buildURL, strings.ToLower(base.Host))
	}
	commit := f.buildCommit
	if commit == "" {
		commit = defaultBuildPath
	}
	if _, err := jsonpath.Parse(commit); err != nil {
		return nil, fmt.Errorf("--build-commit %q is not a JSONPath: %w", commit, err)
	}
	return &config.BuildProbe{Probe: "http", URL: f.buildURL, Commit: commit}, nil
}

func serviceNode(name, baseURL string, probe *config.BuildProbe) *yaml.Node {
	if probe == nil {
		return mapping("name", text(name), "baseUrl", text(baseURL))
	}
	build := mapping("probe", text(probe.Probe), "url", quoted(probe.URL), "commit", quoted(probe.Commit))
	build.Style = yaml.FlowStyle
	return mapping("name", text(name), "baseUrl", text(baseURL), "build", build)
}

// writeInitConfig writes the configuration only when none exists, and keeps it only when it loads.
func writeInitConfig(dir string, data []byte) (string, error) {
	path := configPath(dir)
	if err := atomicfile.WriteNew(path, data); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("%s exists; edit it instead", path)
		}
		return "", err
	}
	if _, err := config.Load(dir); err != nil {
		if rerr := os.Remove(path); rerr != nil {
			return "", errors.Join(fmt.Errorf("the configuration that init wrote is not valid: %w", err), rerr)
		}
		return "", fmt.Errorf("the configuration that init wrote is not valid, so it was removed: %w", err)
	}
	return path, nil
}

// defaultServiceName is the repository directory's name, lowered, with every run of other
// characters than [a-z0-9] made one hyphen.
func defaultServiceName(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	var b strings.Builder
	gap := false
	for _, r := range strings.ToLower(filepath.Base(abs)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if gap && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			gap = false
			continue
		}
		gap = true
	}
	if b.Len() == 0 {
		return "service"
	}
	return b.String()
}

func text(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }

func quoted(s string) *yaml.Node {
	n := text(s)
	n.Style = yaml.DoubleQuotedStyle
	return n
}

func sequence(items ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Content: items}
}

// mapping takes keys and values in turn: a key is a string, and a value is a node.
func mapping(kv ...any) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode}
	for i := 0; i+1 < len(kv); i += 2 {
		n.Content = append(n.Content, text(kv[i].(string)), kv[i+1].(*yaml.Node))
	}
	return n
}
