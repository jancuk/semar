package cli

import (
	"errors"
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/engine/web"
)

type loginFlags struct {
	env string
	as  string
	dir string
}

func newLoginCommand(o options) *cobra.Command {
	var f loginFlags
	cmd := &cobra.Command{
		Use:   "login <service> [--env <environment>] [--as <account>]",
		Short: "Open a Chrome window on Semar's own profile so that a person signs in once",
		Long: "Open Chrome on the browser profile of an account of an environment, at the base URL of the service, and wait until the window is closed.\n" +
			"The profile is Semar's own, at browser/<environment>/<account> below the Semar home. It is never the profile that you browse with, and a later run of a web step starts on it, so the sign-in is still there.\n" +
			"Without --as, the account is the auth profile of the service, or default.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return login(cmd, o, f, args[0])
		},
	}
	cmd.Flags().StringVar(&f.env, "env", "", "the environment of semar.yaml; needed when there are several, and always for a production environment")
	cmd.Flags().StringVar(&f.as, "as", "", "the account whose profile to sign in to: an auth profile of semar.yaml")
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
