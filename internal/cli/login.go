package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/engine/web"
	"github.com/jancuk/semar/internal/secret"
)

type loginFlags struct {
	env         string
	as          string
	dir         string
	user        string
	passwordEnv string
}

func newLoginCommand(o options) *cobra.Command {
	var f loginFlags
	cmd := &cobra.Command{
		Use:   "login [<service>] [--env <environment>] [--as <account>] [--user <user> --password-env <NAME>]",
		Short: "Sign in to the web application: in a Chrome window, or by itself with a user and a password variable",
		Long: "Open Chrome on the browser profile of an account of an environment, at the base URL of the service, and wait until the last page of the window is closed. Without a service, login uses the only service of the environment.\n" +
			"The profile is Semar's own, at browser/<environment>/<account> below the Semar home. It is never the profile that you browse with, and a later run of a web step starts on it, so the sign-in is still there.\n" +
			"Without --as, the account is the auth profile of the service, or default.\n" +
			"While the window is open, login notes the hosts that its pages ask for and that environments.<environment>.hosts does not list. It blocks nothing. After the window is closed it prints them, and in a terminal it asks whether to add them to semar.yaml.\n" +
			"With --user and --password-env, login writes the web block of the account into semar.yaml, signs in once with no window, and says whether it worked. Semar then signs in by itself wherever a web step or a recording meets the sign-in form. The password stays in the environment variable. For a second factor, a captcha, or a sign-in through another site, use login with a window.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			service := ""
			if len(args) == 1 {
				service = args[0]
			}
			return login(cmd, o, f, service)
		},
	}
	cmd.Flags().StringVar(&f.env, "env", "", "the environment of semar.yaml; needed when there are several, and always for a production environment")
	cmd.Flags().StringVar(&f.as, "as", "", "the account whose profile to sign in to: an auth profile of semar.yaml")
	cmd.Flags().StringVar(&f.user, "user", "", "the user that the account signs in with on the web; needs --password-env")
	cmd.Flags().StringVar(&f.passwordEnv, "password-env", "", "the environment variable that holds the web password of the account; needs --user")
	addDirFlag(cmd, &f.dir)
	return cmd
}

func login(cmd *cobra.Command, o options, f loginFlags, service string) error {
	cfg, err := config.Load(f.dir)
	if err != nil {
		return err
	}
	envName, env, err := cfg.Environment(f.env)
	if err != nil {
		return err
	}
	if env.Kind == config.KindProduction && f.env == "" {
		return fmt.Errorf("the environment %q is a production environment; name it with --env %s to sign in to it", envName, envName)
	}
	env = withServiceAuth(cfg, env)
	i, err := loginService(envName, env, service)
	if err != nil {
		return err
	}
	account := f.as
	switch {
	case account == "":
		account = env.Services[i].AuthProfile
	default:
		if _, ok := cfg.AuthProfiles[account]; !ok {
			return fmt.Errorf("account %s is not in the configuration", account)
		}
	}
	if account == "" {
		account = "default"
	}
	home, err := o.home()
	if err != nil {
		return err
	}
	if f.user != "" || f.passwordEnv != "" {
		return loginByItself(cmd, o, f, cfg, envName, env, env.Services[i], account, home)
	}
	dir, err := web.ProfileDir(home, envName, account)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "opening Chrome on the profile of %s in %s at %s\n", account, envName, env.Services[i].BaseURL)
	fmt.Fprintf(out, "sign in, then close the window. The profile is %s\n", dir)
	hooks := loginOf(cmd.Context())
	unlisted, err := web.Login(cmd.Context(), web.LoginOptions{
		Chrome: env.Web.Chrome, Home: home, Environment: envName, Account: account, URL: env.Services[i].BaseURL,
		Hosts: env.Hosts, Hidden: hooks.hidden, AfterOpen: hooks.drive,
	})
	switch {
	case err == nil:
		fmt.Fprintf(out, "the window was closed: the profile of %s in %s is saved\n", account, envName)
		return offerHosts(cmd.Context(), o, out, f.dir, envName, unlisted)
	case errors.Is(err, web.ErrChromeNotFound):
		text, _ := web.ChromeNotFoundText(err)
		return errors.New(text)
	}
	return err
}

// loginService is the index of the service to sign in to. With no name it is the only service of the
// environment.
func loginService(envName string, env config.Environment, name string) (int, error) {
	if name != "" {
		i := slices.IndexFunc(env.Services, func(s config.Service) bool { return s.Name == name })
		if i < 0 {
			return 0, fmt.Errorf("environment %s has no service %s", envName, name)
		}
		return i, nil
	}
	switch len(env.Services) {
	case 0:
		return 0, fmt.Errorf("environment %s has no service", envName)
	case 1:
		return 0, nil
	}
	names := make([]string, len(env.Services))
	for i, s := range env.Services {
		names[i] = s.Name
	}
	return 0, fmt.Errorf("environment %s has several services, %s: name one, as in semar login %s", envName, strings.Join(names, ", "), names[0])
}

// loginHooks let a test stand where the person stands: it hides the window and closes its pages.
type loginHooks struct {
	hidden bool
	drive  func(tab context.Context)
}

type loginKey struct{}

func withLogin(ctx context.Context, h loginHooks) context.Context {
	return context.WithValue(ctx, loginKey{}, h)
}

func loginOf(ctx context.Context) loginHooks {
	h, _ := ctx.Value(loginKey{}).(loginHooks)
	return h
}

// offerHosts says which hosts the window asked for that the environment does not list. In a terminal
// it asks whether to add them to semar.yaml: all, none, or the numbers of some. With no terminal it
// prints the lines to add and changes nothing.
func offerHosts(ctx context.Context, o options, out io.Writer, dir, envName string, hosts []string) error {
	if len(hosts) == 0 {
		return nil
	}
	list := fmt.Sprintf("environments.%s.hosts", envName)
	fmt.Fprintf(out, "the window asked for %s that %s does not list, so a run blocks them and a page that needs one can miss data:\n", plural(len(hosts), "host"), list)
	for i, h := range hosts {
		fmt.Fprintf(out, "  %d. %s\n", i+1, h)
	}
	if !o.Terminal() {
		fmt.Fprintf(out, "%s was not changed. To allow them, add these lines to the list under %s in %s:\n", configFileName, list, configFileName)
		for _, h := range hosts {
			fmt.Fprintf(out, "      - %s\n", config.HostEntry(h))
		}
		return nil
	}
	in := newLineReader(o.Stdin)
	chosen := askHosts(ctx, out, in, list, hosts)
	if len(chosen) == 0 {
		fmt.Fprintf(out, "nothing was added. To allow them later, add the hosts to %s in %s\n", list, configFileName)
		return nil
	}
	if err := writeHosts(dir, envName, chosen); err != nil {
		return fmt.Errorf("%w; add these hosts to %s in %s by hand: %s", err, list, configFileName, strings.Join(chosen, ", "))
	}
	fmt.Fprintf(out, "added %s to %s in %s\n", plural(len(chosen), "host"), list, configFileName)
	return nil
}

// askHosts asks which of the hosts to add, and gives them. An empty answer, none, and a stream that
// ends add none. An answer that is not understood is asked again, twice at most.
func askHosts(ctx context.Context, out io.Writer, in *lineReader, list string, hosts []string) []string {
	for range 3 {
		fmt.Fprintf(out, "Add them to %s in %s? Type all, none, or numbers such as 1,3 [none]: ", list, configFileName)
		line, ok := in.read(ctx)
		if !ok {
			fmt.Fprintln(out)
			return nil
		}
		chosen, err := chooseHosts(strings.TrimSpace(line), hosts)
		if err == nil {
			return chosen
		}
		fmt.Fprintf(out, "%s\n", err)
	}
	return nil
}

// chooseHosts reads an answer: all, none, or numbers of the list, apart by commas or spaces.
func chooseHosts(answer string, hosts []string) ([]string, error) {
	switch strings.ToLower(answer) {
	case "", "n", "no", "none":
		return nil, nil
	case "a", "all":
		return hosts, nil
	}
	picked := make([]bool, len(hosts))
	for _, field := range strings.FieldsFunc(answer, func(r rune) bool { return r == ',' || r == ' ' }) {
		n, err := strconv.Atoi(field)
		if err != nil || n < 1 || n > len(hosts) {
			return nil, fmt.Errorf("%q is not all, none, or a number from 1 to %d", field, len(hosts))
		}
		picked[n-1] = true
	}
	var chosen []string
	for i, h := range hosts {
		if picked[i] {
			chosen = append(chosen, h)
		}
	}
	return chosen, nil
}

// loginByItself writes the web block of the account and signs in once with no window.
func loginByItself(cmd *cobra.Command, o options, f loginFlags, cfg config.Config, envName string, env config.Environment, svc config.Service, account, home string) error {
	who, err := webSignInFlags(f)
	if err != nil {
		return err
	}
	if _, ok := cfg.AuthProfiles[account]; !ok {
		return fmt.Errorf("account %s is not in the configuration: name one of its accounts with --as", account)
	}
	if _, err := web.FindChrome(env.Web.Chrome); err != nil {
		if text, ok := web.ChromeNotFoundText(err); ok {
			return errors.New(text)
		}
		return err
	}
	if err := writeWebSignIn(f.dir, account, who); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Wrote the web sign-in of %s to %s.\n", account, configFileName)
	signed, err := web.SignIn(cmd.Context(), web.SignInOptions{
		Chrome: env.Web.Chrome, Home: home, Environment: envName, Account: account, URL: svc.BaseURL,
		Who: who, Secrets: secret.NewEnv(o.LookupEnv), Hidden: true,
	})
	var failed web.SignInError
	switch {
	case errors.As(err, &failed):
		fmt.Fprintf(out, "Sign-in failed: %s\n", terminalSafe(oneLine(failed.Text)))
		return exitError{ExitUndecided}
	case err != nil:
		return err
	case !signed:
		fmt.Fprintf(out, "The page at %s shows no sign-in form, so the user and the password were not tried. If the application keeps its session in the profile, it may be signed in already.\n", svc.BaseURL)
		return exitError{ExitUndecided}
	}
	fmt.Fprintf(out, "Signed in as %s (%s) at %s. The profile of %s in %s is saved.\n", account, terminalSafe(who.User), svc.BaseURL, account, envName)
	return nil
}

// webSignInFlags reads --user and --password-env, which come together.
func webSignInFlags(f loginFlags) (config.WebSignIn, error) {
	switch {
	case f.user == "":
		return config.WebSignIn{}, errors.New("--password-env needs --user: the user that the account signs in with on the web")
	case f.passwordEnv == "":
		return config.WebSignIn{}, errors.New("--user needs --password-env: the environment variable that holds the web password")
	case !envVarPattern.MatchString(f.passwordEnv):
		return config.WebSignIn{}, fmt.Errorf("--password-env %q is not the name of an environment variable", f.passwordEnv)
	}
	if problem := config.DescriptionProblem(f.user); problem != "" {
		return config.WebSignIn{}, fmt.Errorf("--user: the user %s", problem)
	}
	return config.WebSignIn{User: f.user, Password: "env:" + f.passwordEnv}, nil
}

// writeWebSignIn changes semar.yaml by the one block.
func writeWebSignIn(dir, account string, who config.WebSignIn) error {
	return editConfig(dir, func(data []byte) ([]byte, error) { return config.SetWebSignIn(data, account, who) })
}

// writeHosts lists more hosts under hosts of the environment in semar.yaml.
func writeHosts(dir, envName string, hosts []string) error {
	return editConfig(dir, func(data []byte) ([]byte, error) { return config.AddHosts(data, envName, hosts) })
}

// editConfig changes semar.yaml through a root at the repository, so that a link in .semar cannot
// carry the write elsewhere, and writes it in one step.
func editConfig(dir string, change func([]byte) ([]byte, error)) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	rel := filepath.Join(".semar", "semar.yaml")
	data, err := root.ReadFile(rel)
	if err != nil {
		return err
	}
	changed, err := change(data)
	if err != nil {
		return err
	}
	if bytes.Equal(changed, data) {
		return nil
	}
	return atomicfile.WriteIn(root, rel, changed, atomicfile.FileMode)
}
