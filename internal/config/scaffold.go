package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/hostrule"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/yamlfile"
)

// AndroidService is what a guided setup knows of the app that a person wants to record: its name for
// Semar, its package, and where it comes from. It holds no secret and no path of adb.
type AndroidService struct {
	// Environment is the environment to add the service to. Empty means the only test environment, or
	// uat in a new file.
	Environment string
	// Name is the name of the service, and the default name of its screens file.
	Name string
	// BaseURL is the address of the backend of the app, and may be empty: an app on a device need not
	// have one.
	BaseURL string
	// Package is the application ID, such as com.example.shop.
	Package string
	// APK is a file that Semar installs, as a path of the repository. Empty installs nothing.
	APK string
	// Reset is clear, restart, or none. Empty writes nothing, and the default is restart.
	Reset string
}

// gitignoreText keeps on the machine what must not be published: the evidence, the key that makes a
// redacted value unguessable, and the health of fixtures.
const gitignoreText = `# Written by semar init. These stay on this machine.
runs/
store/redact.key
store/fixture-health/
semar.local.yaml
`

func (s AndroidService) check() error {
	if err := idcheck.Check("service name", s.Name); err != nil {
		return err
	}
	if !ValidAndroidPackage(s.Package) {
		return fmt.Errorf("%q is not a package name such as com.example.shop", s.Package)
	}
	switch s.Reset {
	case "", ResetClear, ResetRestart, ResetNone:
	default:
		return fmt.Errorf("the reset must be %s, %s, or %s, got %q", ResetRestart, ResetClear, ResetNone, s.Reset)
	}
	if strings.ContainsFunc(s.APK, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return errors.New("the apk path holds a control character")
	}
	if s.BaseURL != "" {
		u, err := url.Parse(s.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
			return fmt.Errorf("the base URL %q must be an absolute http or https URL with no user name", s.BaseURL)
		}
	}
	return nil
}

// scalar writes a string as a YAML scalar, quoted when it needs to be.
func scalar(v string) string {
	b, err := yaml.Marshal(&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
	if err != nil {
		return fmt.Sprintf("%q", v)
	}
	return strings.TrimSpace(string(b))
}

// block is the service as lines of a block list, with the dash at indent.
func (s AndroidService) block(indent string) string {
	var b strings.Builder
	b.WriteString(indent + "- name: " + scalar(s.Name) + "\n")
	in := indent + "  "
	if s.BaseURL != "" {
		b.WriteString(in + "baseUrl: " + scalar(s.BaseURL) + "\n")
	}
	b.WriteString(in + "android:\n")
	// appActivity is left out on purpose: the launcher activity is found on the device, and the foreground
	// activity is often not the one that can be started.
	b.WriteString(in + "  appPackage: " + scalar(s.Package) + "\n")
	if s.APK != "" {
		b.WriteString(in + "  apk: " + scalar(s.APK) + "\n")
	}
	if s.Reset != "" {
		b.WriteString(in + "  reset: " + s.Reset + "\n")
	}
	return b.String()
}

// AddAndroidService puts an app on a device in semar.yaml. With no file, data is nil, and it gives a new
// one: version 1, an environment uat of the kind test, and the service. With a file, it adds the service
// to the services of one environment, and changes no other line, so comments and settings stay as they
// were. It refuses a production environment and a read only one, a service of the same name, and a result
// that the configuration would not accept.
func AddAndroidService(data []byte, s AndroidService) ([]byte, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return newConfig(s)
	}
	var doc yaml.Node
	if err := yamlfile.Decode(data, fileName, &doc); err != nil {
		return nil, err
	}
	cfg, err := parse(data, fileName)
	if err != nil {
		return nil, err
	}
	envName, err := chooseEnvironment(cfg, s.Environment)
	if err != nil {
		return nil, err
	}
	env := cfg.Environments[envName]
	if i := slices.IndexFunc(env.Services, func(x Service) bool { return x.Name == s.Name }); i >= 0 {
		return nil, fmt.Errorf("the environment %s has a service %s already", envName, s.Name)
	}
	node := valueOf(valueOf(doc.Content[0], "environments"), envName)
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s has no environment %s", fileName, envName)
	}
	list := valueOf(node, "services")
	lines := strings.SplitAfter(string(data), "\n")
	switch {
	case list != nil && list.Kind == yaml.SequenceNode && list.Style&yaml.FlowStyle == 0 && len(list.Content) > 0:
		first := list.Content[0]
		indent := strings.Repeat(" ", max(first.Column-3, 0))
		last := lastLine(list)
		if !strings.HasSuffix(lines[last-1], "\n") {
			lines[last-1] += "\n"
		}
		lines = slices.Concat(lines[:last], []string{s.block(indent)}, lines[last:])
	case list == nil && node.Style&yaml.FlowStyle == 0 && len(node.Content) > 0:
		indent := strings.Repeat(" ", node.Content[0].Column-1)
		last := lastLine(node)
		if !strings.HasSuffix(lines[last-1], "\n") {
			lines[last-1] += "\n"
		}
		lines = slices.Concat(lines[:last], []string{indent + "services:\n", s.block(indent + "  ")}, lines[last:])
	default:
		return nil, fmt.Errorf("the services of the environment %s are written in a way that Semar cannot add to; add the service to %s by hand", envName, fileName)
	}
	out := []byte(strings.Join(lines, ""))
	if s.BaseURL != "" {
		if u, _ := url.Parse(s.BaseURL); u != nil && !hostrule.Allowed(u, env.Hosts) {
			if out, err = AddHosts(out, envName, []string{strings.ToLower(u.Host)}); err != nil {
				return nil, err
			}
		}
	}
	got, err := parse(out, fileName)
	if err != nil {
		return nil, errors.New("the service is not valid: " + strings.TrimPrefix(err.Error(), fileName+": "))
	}
	i := slices.IndexFunc(got.Environments[envName].Services, func(x Service) bool { return x.Name == s.Name })
	if i < 0 || got.Environments[envName].Services[i].Android == nil || got.Environments[envName].Services[i].Android.AppPackage != s.Package {
		return nil, fmt.Errorf("the service did not land in %s; add it by hand", fileName)
	}
	return out, nil
}

// chooseEnvironment is the environment that gets the service: the one named, else the only test one.
func chooseEnvironment(cfg Config, name string) (string, error) {
	check := func(n string, e Environment) error {
		switch {
		case e.Kind == KindProduction:
			return fmt.Errorf("the environment %s is a production environment, and the app on a device makes requests that Semar cannot see", n)
		case e.Effects.Mode == "read_only":
			return fmt.Errorf("the environment %s is read only, and a recording taps and types, so it needs an environment that allows test data", n)
		}
		return nil
	}
	if name != "" {
		e, ok := cfg.Environments[name]
		if !ok {
			return "", fmt.Errorf("%s has no environment %s", fileName, name)
		}
		return name, check(name, e)
	}
	var tests []string
	for n, e := range cfg.Environments {
		if e.Kind == KindTest && e.Effects.Mode != "read_only" {
			tests = append(tests, n)
		}
	}
	slices.Sort(tests)
	switch len(tests) {
	case 0:
		for n, e := range cfg.Environments {
			if len(cfg.Environments) == 1 {
				return "", check(n, e)
			}
		}
		return "", errors.New("no environment allows test data: a recording needs one")
	case 1:
		return tests[0], nil
	}
	return "", fmt.Errorf("choose the environment: %s", strings.Join(tests, ", "))
}

func newConfig(s AndroidService) ([]byte, error) {
	var b strings.Builder
	b.WriteString("version: 1\nenvironments:\n  uat:\n    kind: test\n")
	if s.BaseURL != "" {
		u, _ := url.Parse(s.BaseURL)
		b.WriteString("    hosts: [" + scalar(strings.ToLower(u.Host)) + "]\n")
	}
	b.WriteString("    methods: [GET]\n    effects:\n      mode: test_data\n    services:\n")
	b.WriteString(s.block("      "))
	b.WriteString("authProfiles:\n  default: {}\n")
	out := []byte(b.String())
	if _, err := parse(out, fileName); err != nil {
		return nil, errors.New("the new configuration is not valid: " + strings.TrimPrefix(err.Error(), fileName+": "))
	}
	return out, nil
}

// WriteAndroidService adds the service to .semar/semar.yaml of the repository, or writes the file when
// there is none, and writes .semar/.gitignore when it is missing. It writes through a root at the
// repository, so a link that the repository commits cannot carry the file elsewhere. created says that the
// file did not exist.
func WriteAndroidService(dir string, s AndroidService) (path string, created bool, err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", false, err
	}
	defer root.Close()
	rel := filepath.ToSlash(filepath.Join(".semar", fileName))
	path = filepath.Join(dir, ".semar", fileName)
	if err := atomicfile.CheckDirIn(root, ".semar"); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", false, err
	}
	old, err := root.ReadFile(rel)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		created = true
	case err != nil:
		return "", false, err
	}
	out, err := AddAndroidService(old, s)
	if err != nil {
		return "", false, err
	}
	if _, err := atomicfile.MkdirIn(root, ".semar"); err != nil {
		return "", false, err
	}
	if created {
		err = atomicfile.WriteNewIn(root, rel, out, atomicfile.FileMode)
	} else {
		err = atomicfile.WriteIn(root, rel, out, atomicfile.FileMode)
	}
	if err != nil {
		return "", false, err
	}
	if _, err := Load(dir); err != nil {
		if created {
			_ = root.Remove(rel)
		} else {
			_ = atomicfile.WriteIn(root, rel, old, atomicfile.FileMode)
		}
		return "", false, fmt.Errorf("the configuration is not valid, so it was put back: %w", err)
	}
	if err := EnsureGitignore(dir); err != nil {
		return "", false, err
	}
	return path, created, nil
}

// EnsureGitignore writes .semar/.gitignore when there is none, and never changes one that exists.
func EnsureGitignore(dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	rel := filepath.ToSlash(filepath.Join(".semar", ".gitignore"))
	if _, err := atomicfile.MkdirIn(root, ".semar"); err != nil {
		return err
	}
	if err := atomicfile.WriteNewIn(root, rel, []byte(gitignoreText), atomicfile.FileMode); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return nil
}
