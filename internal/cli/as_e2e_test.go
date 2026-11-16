package cli_test

import (
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/scenario"
)

const bobToken = "tok-bob"

// actAs rewrites the scenario so that step order-unchanged acts as the named account, re-binds
// the script to it, and adds the account bob to the configuration.
func (r *repo) actAs(t *testing.T, account string) {
	t.Helper()
	t.Setenv("SEMAR_SAMPLE_TOKEN_BOB", bobToken)
	r.rewriteConfig(t, func(s string) string {
		return s + "  bob:\n    session: {header: Authorization, value: \"Bearer {{secret.TOKEN}}\"}\n    secrets: {TOKEN: \"env:SEMAR_SAMPLE_TOKEN_BOB\"}\n"
	})
	file := r.path(".semar", "scenarios", "SHOP-482", scenarioID+".yaml")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	oldHash := func() domain.Hash {
		sc, err := scenario.Parse(data, file)
		if err != nil {
			t.Fatal(err)
		}
		return domain.ExpectHash(sc)
	}()
	changed := strings.Replace(string(data), "    action: Read the order again\n", "    action: Read the order again\n    as: "+account+"\n", 1)
	if changed == string(data) {
		t.Fatal("the scenario did not change")
	}
	if err := os.WriteFile(file, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	sc, err := scenario.Load(file)
	if err != nil {
		t.Fatal(err)
	}
	sf, err := os.ReadFile(r.scriptFile())
	if err != nil {
		t.Fatal(err)
	}
	fixed := strings.Replace(string(sf), string(oldHash), string(domain.ExpectHash(sc)), 1)
	if fixed == string(sf) {
		t.Fatal("the script hash did not change")
	}
	if err := os.WriteFile(r.scriptFile(), []byte(fixed), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunStepActsAsAnotherAccount(t *testing.T) {
	var mu sync.Mutex
	got := map[string][]string{}
	r := newRepo(t, repoOptions{wrap: func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			mu.Lock()
			got[req.Method] = append(got[req.Method], req.Header.Values("Authorization")...)
			mu.Unlock()
			next.ServeHTTP(w, req)
		})
	}})
	r.actAs(t, "bob")
	r.addScriptAndConfirm(t)
	r.semar(t, t.Context(), "run", "--scenario", scenarioID)

	mu.Lock()
	defer mu.Unlock()
	if len(got["GET"]) == 0 || got["GET"][len(got["GET"])-1] != "Bearer "+bobToken {
		t.Errorf("the reads sent %v, want bob's session for the step that acts as bob", got["GET"])
	}
	for _, v := range got["POST"] {
		if v != "Bearer "+token {
			t.Errorf("a write sent %q, want the scenario's own profile", v)
		}
	}
	r.assertNoSecret(t, []string{token, bobToken})
}

func TestRunStepActsAsAnAccountThatIsNotConfigured(t *testing.T) {
	r := newRepo(t, repoOptions{})
	r.actAs(t, "dave")
	r.addScriptAndConfirm(t)
	res := r.must(t, cli.ExitUndecided, "run", "--scenario", scenarioID)
	contains(t, "the output", res.stdout+res.stderr, "account dave is not in the configuration")
	if n := r.app.Requests("", "/v1/"); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}
