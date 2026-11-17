package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

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
		Use:   "login <service> [--env <environment>] [--as <account>] [--user <user> --password-env <NAME>]",
		Short: "Sign in to the web application: in a Chrome window, or by itself with a user and a password variable",
		Long: "Open Chrome on the browser profile of an account of an environment, at the base URL of the service, and wait until the window is closed.\n" +
			"The profile is Semar's own, at browser/<environment>/<account> below the Semar home. It is never the profile that you browse with, and a later run of a web step starts on it, so the sign-in is still there.\n" +
			"Without --as, the account is the auth profile of the service, or default.\n" +
			"With --user and --password-env, login writes the web block of the account into semar.yaml, signs in once with no window, and says whether it worked. Semar then signs in by itself wherever a web step or a recording meets the sign-in form. The password stays in the environment variable. For a second factor, a captcha, or a sign-in through another site, use login with a window.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return login(cmd, o, f, args[0])
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
	i := slices.IndexFunc(env.Services, func(s config.Service) bool { return s.Name == service })
	if i < 0 {
		return fmt.Errorf("environment %s has no service %s", envName, service)
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
	err = web.Login(cmd.Context(), web.LoginOptions{
		Chrome: env.Web.Chrome, Home: home, Environment: envName, Account: account, URL: env.Services[i].BaseURL,
	})
	switch {
	case err == nil:
		fmt.Fprintf(out, "the window was closed: the profile of %s in %s is saved\n", account, envName)
		return nil
	case errors.Is(err, web.ErrChromeNotFound):
		text, _ := web.ChromeNotFoundText(err)
		return errors.New(text)
	}
	return err
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

// writeWebSignIn changes semar.yaml by the one block, through a root at the repository so that a link
// in .semar cannot carry the write elsewhere.
func writeWebSignIn(dir, account string, who config.WebSignIn) error {
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
	changed, err := config.SetWebSignIn(data, account, who)
	if err != nil {
		return err
	}
	if bytes.Equal(changed, data) {
		return nil
	}
	return atomicfile.WriteIn(root, rel, changed, atomicfile.FileMode)
}
