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
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/theory/jsonpath"
	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/hostrule"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/knowledge"
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
	buildURL, buildCommit, kind                            string
	accounts                                               []string // name=ENV, one for each extra account
}

var initFlagNames = []string{"base-url", "service", "auth-header", "auth-value", "token-env", "build-url", "build-commit", "kind", "account"}

func addInitFlags(cmd *cobra.Command, f *initFlags) {
	cmd.Flags().StringVar(&f.baseURL, "base-url", "", "the address of the service in the environment, such as https://orders.uat.example.com")
	cmd.Flags().StringVar(&f.service, "service", "", "the name of the service (default: the name of the repository directory)")
	cmd.Flags().StringVar(&f.authHeader, "auth-header", defaultAuthHeader, "the header that carries the session")
	cmd.Flags().StringVar(&f.authValue, "auth-value", defaultAuthValue, "the value of that header; it must hold "+tokenPlaceholder)
	cmd.Flags().StringVar(&f.tokenEnv, "token-env", defaultTokenEnv, "the environment variable that holds the token")
	cmd.Flags().StringVar(&f.kind, "kind", config.KindTest, "the kind of the environment, test or production; production is read only and allows GET and HEAD")
	cmd.Flags().StringArrayVar(&f.accounts, "account", nil, "another account as <name>=<ENV>: an auth profile with the same session header and the token in the environment variable ENV; repeatable")
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
		Short: "Write .semar/semar.yaml for one environment",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if f.baseURL == "" {
				return errors.New("--base-url is required: the address of the service in the environment")
			}
			data, err := buildInitConfig(dir, f)
			if err != nil {
				return err
			}
			if cfg, err := config.Load(dir); err == nil {
				diffs, err := diffInitFlags(cfg, f.env, "", f, givenInitFlags(cmd))
				if err != nil {
					return fmt.Errorf("%s exists: %w; to change the environment, edit it", configFileName, err)
				}
				if len(diffs) > 0 {
					return initFlagsDiffer(diffs, "To change the environment, edit "+configFileName+".")
				}
				if err := ensureGitignore(dir); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s already has these settings; nothing was written\n", configFileName)
				return nil
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
	cmd.Flags().StringVar(&f.env, "env", "", "the name of the environment (default uat, or production with --kind production)")
	addDirFlag(cmd, &dir)
	return cmd
}

func configPath(dir string) string { return filepath.Join(semarDir(dir), "semar.yaml") }

func printInitDone(w io.Writer, path string, f initFlags) {
	fmt.Fprintf(w, "wrote %s\n", path)
	// The line says what the file holds, so it is read back from the file: plan prints
	// this after it has written the file, and it does not pass the entries on.
	if cfg, err := config.Load(filepath.Dir(filepath.Dir(path))); err == nil && len(cfg.Knowledge) > 0 {
		parts := make([]string, len(cfg.Knowledge))
		for i, k := range cfg.Knowledge {
			parts[i] = k.ID + " (" + k.Path + ")"
		}
		fmt.Fprintf(w, "added knowledge: %s\n", strings.Join(parts, ", "))
	}
	if f.kind == config.KindProduction {
		fmt.Fprintln(w, "the production environment is read only; edit the file to allow more")
		fmt.Fprintf(w, "export %s=<the token of a read only account> before semar run --env <the environment name>\n", f.tokenEnv)
		printAccounts(w, f)
		return
	}
	fmt.Fprintf(w, "export %s=<the token of the test account> before semar run\n", f.tokenEnv)
	printAccounts(w, f)
}

// printAccounts tells the person which variable each extra account needs.
func printAccounts(w io.Writer, f initFlags) {
	for _, a := range f.accounts {
		if name, env, ok := strings.Cut(a, "="); ok {
			fmt.Fprintf(w, "export %s=<the token of the account %s>\n", env, name)
		}
	}
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
	kind := f.kind
	if kind == "" {
		kind = config.KindTest
	}
	if kind != config.KindTest && kind != config.KindProduction {
		return nil, fmt.Errorf("--kind %q must be %s or %s", f.kind, config.KindTest, config.KindProduction)
	}
	env := f.env
	if env == "" {
		env = defaultEnvName
		if kind == config.KindProduction {
			env = config.KindProduction
		}
	}
	methods := []*yaml.Node{text("GET"), text("POST"), text("PUT"), text("PATCH"), text("DELETE")}
	mode := "test_data"
	if kind == config.KindProduction {
		methods = []*yaml.Node{text("GET"), text("HEAD")}
		mode = "read_only"
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
	accounts, err := parseAccounts(f.accounts)
	if err != nil {
		return nil, err
	}

	flow := func(n *yaml.Node) *yaml.Node { n.Style = yaml.FlowStyle; return n }
	profiles := mapping(
		"default", mapping(
			"session", flow(mapping("header", text(f.authHeader), "value", quoted(f.authValue))),
			"secrets", flow(mapping("TOKEN", quoted("env:"+f.tokenEnv))),
		),
	)
	for _, a := range accounts {
		profiles.Content = append(profiles.Content, text(a.name), mapping(
			"session", flow(mapping("header", text(f.authHeader), "value", quoted(f.authValue))),
			"secrets", flow(mapping("TOKEN", quoted("env:"+a.env))),
		))
	}
	doc := mapping(
		"version", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"},
		"environments", mapping(
			env, mapping(
				"kind", text(kind),
				"hosts", flow(sequence(quoted(strings.ToLower(u.Host)))),
				"methods", flow(sequence(methods...)),
				"effects", mapping("mode", text(mode)),
				"services", sequence(serviceNode(service, f.baseURL, probe)),
			),
		),
		"authProfiles", profiles,
	)
	if found := knowledge.WithFolder(knowledge.Conventional(dir), folderInWorkTree(dir)); len(found) > 0 {
		list := make([]*yaml.Node, len(found))
		for i, k := range found {
			list[i] = mapping("id", text(k.ID), "kind", text(k.Kind), "path", quoted(k.Path))
		}
		doc.Content = append(doc.Content, text("knowledge"), sequence(list...))
	}
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

type account struct{ name, env string }

// parseAccounts reads the --account values, <name>=<ENV>. A name is an auth profile name that is
// not default, and a name may be given once.
func parseAccounts(values []string) ([]account, error) {
	var out []account
	for _, v := range values {
		name, env, ok := strings.Cut(v, "=")
		switch {
		case !ok:
			return nil, fmt.Errorf("--account %q must be <name>=<ENV>, such as bob=ORDERS_TOKEN_BOB", v)
		case name == "default":
			return nil, fmt.Errorf("--account %q: the name default is the account of --token-env", v)
		case !envVarPattern.MatchString(env):
			return nil, fmt.Errorf("--account %q: %q is not the name of an environment variable", v, env)
		}
		if err := idcheck.Check("account name", name); err != nil {
			return nil, fmt.Errorf("--account %q: %w", v, err)
		}
		if slices.ContainsFunc(out, func(a account) bool { return a.name == name }) {
			return nil, fmt.Errorf("--account: the name %s is given more than once", name)
		}
		out = append(out, account{name, env})
	}
	return out, nil
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
// It writes through a root at the repository, so a .semar that the repository commits as a link
// cannot carry the file elsewhere.
func writeInitConfig(dir string, data []byte) (path string, err error) {
	path = configPath(dir)
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	rel := filepath.Join(".semar", "semar.yaml")
	created, err := atomicfile.MkdirIn(root, filepath.Dir(rel))
	defer func() {
		if err != nil {
			for _, d := range slices.Backward(created) {
				_ = root.Remove(d)
			}
		}
	}()
	if err != nil {
		return "", err
	}
	if err := atomicfile.WriteNewIn(root, rel, data, atomicfile.FileMode); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("%s exists; edit it instead", path)
		}
		return "", err
	}
	if _, err := config.Load(dir); err != nil {
		if rerr := root.Remove(rel); rerr != nil {
			return "", errors.Join(fmt.Errorf("the configuration that init wrote is not valid: %w", err), rerr)
		}
		return "", fmt.Errorf("the configuration that init wrote is not valid, so it was removed: %w", err)
	}
	if err := ensureGitignore(dir); err != nil {
		return "", err
	}
	return path, nil
}

// gitignoreText keeps on the machine what must not be published: the evidence, the key that makes a
// redacted value unguessable, and the health of fixtures. Approvals and confirmations stay in the
// repository, so the team shares them.
const gitignoreText = `# Written by semar init. These stay on this machine.
runs/
store/redact.key
store/fixture-health/
semar.local.yaml
`

// ensureGitignore writes .semar/.gitignore when there is none, and never changes one that exists.
func ensureGitignore(dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	rel := filepath.Join(".semar", ".gitignore")
	if _, err := atomicfile.MkdirIn(root, filepath.Dir(rel)); err != nil {
		return err
	}
	if err := atomicfile.WriteNewIn(root, rel, []byte(gitignoreText), atomicfile.FileMode); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return nil
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
