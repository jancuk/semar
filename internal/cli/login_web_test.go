package cli_test

import (
	"os"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/config"
)

// portalLoginRepo is the repository of the portal page, with an account that has no web sign-in yet
// and a comment in its file that a rewrite must keep.
func portalLoginRepo(t *testing.T) *repo {
	t.Helper()
	r := portalRepoWith(t, false)
	r.rewriteConfig(t, func(s string) string {
		return strings.Replace(s, "authProfiles:\n", "authProfiles:\n  # the account that the portal tests use\n", 1)
	})
	return r
}

func TestLoginWithAUserAndAPasswordVariableWritesTheBlockAndSignsInOnce(t *testing.T) {
	r := portalLoginRepo(t)
	before, err := os.ReadFile(r.path(".semar", "semar.yaml"))
	must(t, err)
	res := r.must(t, cli.ExitOK, "login", "orders", "--as", "support-agent", "--user", "ana@example.com", "--password-env", portalPassVar)
	contains(t, "the output", res.stdout, "Signed in as support-agent (ana@example.com)")

	after, err := os.ReadFile(r.path(".semar", "semar.yaml"))
	must(t, err)
	added := "    web: {user: ana@example.com, password: \"env:" + portalPassVar + "\"}\n"
	if want := string(before) + added; string(after) != want {
		t.Errorf("the file is not the old file and one line:\n%s", after)
	}
	contains(t, "the file", string(after), "# the account that the portal tests use")
	cfg, err := config.Load(r.dir)
	must(t, err)
	if w := cfg.AuthProfiles["support-agent"].Web; w == nil || w.User != "ana@example.com" {
		t.Errorf("web = %+v", w)
	}
	if strings.Contains(res.stdout+res.stderr, portalPassword) {
		t.Error("an output holds the password")
	}
	r.assertNoSecret(t, []string{portalPassword})
	r.assertNoSecretInHome(t, portalPassword)
}

func TestLoginWithAWrongPasswordSaysSoAndExitsWithTwo(t *testing.T) {
	r := portalLoginRepo(t)
	t.Setenv(portalPassVar, "not-the-password-5")
	res := r.must(t, cli.ExitUndecided, "login", "orders", "--as", "support-agent", "--user", "ana@example.com", "--password-env", portalPassVar)
	contains(t, "the output", res.stdout+res.stderr, "did not finish", "Wrong email or password", portalPassVar)
	if strings.Contains(res.stdout+res.stderr, "not-the-password-5") {
		t.Error("an output holds the password")
	}
	data, err := os.ReadFile(r.path(".semar", "semar.yaml"))
	must(t, err)
	contains(t, "the file", string(data), "web: {user: ana@example.com")
}

func TestLoginWithAPasswordVariableThatIsNotSetSaysWhichOne(t *testing.T) {
	r := portalLoginRepo(t)
	os.Unsetenv(portalPassVar)
	res := r.must(t, cli.ExitUndecided, "login", "orders", "--as", "support-agent", "--user", "ana@example.com", "--password-env", portalPassVar)
	contains(t, "the output", res.stdout+res.stderr, portalPassVar, "not set")
}

func TestLoginTakesTheUserAndThePasswordVariableTogether(t *testing.T) {
	r := portalLoginRepo(t)
	before, err := os.ReadFile(r.path(".semar", "semar.yaml"))
	must(t, err)
	for name, flags := range map[string][]string{
		"a user alone":       {"--user", "ana@example.com"},
		"a variable alone":   {"--password-env", portalPassVar},
		"a bad variable":     {"--user", "ana", "--password-env", "1X"},
		"a user with a tab":  {"--user", "a\tb", "--password-env", portalPassVar},
		"an unknown account": {"--as", "nobody", "--user", "ana", "--password-env", portalPassVar},
	} {
		args := append([]string{"login", "orders", "--as", "support-agent"}, flags...)
		res := r.must(t, cli.ExitError, args...)
		if res.stderr == "" {
			t.Errorf("%s: no message", name)
		}
		after, err := os.ReadFile(r.path(".semar", "semar.yaml"))
		must(t, err)
		if string(after) != string(before) {
			t.Errorf("%s: the file changed", name)
		}
	}
}
