package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
)

// importSecrets are the values of the fixtures that must never reach a file that Semar writes or a
// line that it prints.
var importSecrets = []string{
	"tok-collection-secret-7f3a91", "cookie-secret-abc123", "hunter2-login-pw",
	"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJxYSJ9.c2lnbmF0dXJl",
	"har-token-secret-9d2c41", "har-cookie-secret-77", "cdn-query-secret",
}

// importFixture copies a fixture into the repository, with the host of the fixture replaced by the
// address of the local service, and returns its path.
func (r *repo) importFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "import", name))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if r.ordersURL != "" {
		text = strings.ReplaceAll(text, "http://shop.example", r.ordersURL)
	}
	p := r.path("in", name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func (r *repo) feature(t *testing.T, id string) string {
	t.Helper()
	data, err := os.ReadFile(r.path(".semar", "scenarios", "SHOP-77", id+".feature"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestImportPostman(t *testing.T) {
	r := newRepo(t, repoOptions{})
	file := r.importFixture(t, "shop.postman_collection.json")

	res := r.must(t, cli.ExitOK, "import", file, "--ticket", "SHOP-77")
	contains(t, "the import summary", res.stdout,
		"Read a Postman collection with 7 requests.",
		"Wrote 3 scenarios: SHOP-77-I01 to SHOP-77-I03.",
		"the variable {{orderId}} has no value in the collection",
		"the body is not JSON",
		"the body holds a password, a token, or another secret",
		"Hosts of the file that the environment local does not list: other.example",
		"1 request had a test script.",
		"Wrote 3 scenarios, left out 4.",
		"Next: semar run",
	)
	if strings.Contains(res.stdout, "semar review") || strings.Contains(res.stdout, "Nobody approved") {
		t.Errorf("the import points to a review:\n%s", res.stdout)
	}
	if r.app.Requests("POST", "/v1/orders") != 0 {
		t.Error("the import sent a write")
	}

	list := r.feature(t, "SHOP-77-I01")
	contains(t, "the first scenario", list, "@id:SHOP-77-I01", "Scenario: Orders / List orders", "Then the status is 200", "# import", "GET /v1/orders")
	contains(t, "the write", r.feature(t, "SHOP-77-I02"), "Then the status is 201", "POST /v1/orders")
	contains(t, "the scenario that had no example", r.feature(t, "SHOP-77-I03"), "Then the status is 200")

	r.assertNoSecret(t, importSecrets)
	scriptDir := r.path(".semar", "store", "scripts")
	if !exists(t, scriptDir) {
		t.Fatal("no script was stored")
	}
}

func TestImportedScenariosRunWithNoReview(t *testing.T) {
	r := newRepo(t, repoOptions{})
	r.must(t, cli.ExitOK, "import", r.importFixture(t, "shop.postman_collection.json"), "--ticket", "SHOP-77")

	var args []string
	for _, id := range []string{"SHOP-77-I01", "SHOP-77-I02", "SHOP-77-I03"} {
		args = append(args, "--scenario", id)
	}
	res := r.must(t, cli.ExitOK, append([]string{"run"}, args...)...)
	contains(t, "the run", res.stdout, "SHOP-77-I01  passed", "SHOP-77-I02  passed", "SHOP-77-I03  passed")
	if got := r.app.Requests("POST", "/v1/orders"); got != 1 {
		t.Errorf("a run sent %d writes of the imported scenarios, want 1", got)
	}
	r.assertNoSecret(t, importSecrets)
}

func TestImportHAR(t *testing.T) {
	r := newRepo(t, repoOptions{})
	file := r.importFixture(t, "shop.har")

	res := r.must(t, cli.ExitOK, "import", file, "--ticket", "SHOP-77")
	contains(t, "the import summary", res.stdout,
		"Read a HAR file with 3 requests.",
		"Wrote 2 scenarios: SHOP-77-I01 to SHOP-77-I02.",
		"Hosts of the file that the environment local does not list: cdn.example",
		"Wrote 2 scenarios, left out 1.",
	)
	contains(t, "the read", r.feature(t, "SHOP-77-I01"), "Then the status is 200", "# import")
	contains(t, "the write", r.feature(t, "SHOP-77-I02"), "Then the status is 201")
	r.assertNoSecret(t, importSecrets)
}

func TestImportTwiceAddsNothing(t *testing.T) {
	r := newRepo(t, repoOptions{})
	file := r.importFixture(t, "shop.har")
	r.must(t, cli.ExitOK, "import", file, "--ticket", "SHOP-77")

	again := r.must(t, cli.ExitOK, "import", file, "--ticket", "SHOP-77")
	contains(t, "the second import", again.stdout, "it is imported already, as SHOP-77-I01", "Wrote 0 scenarios, left out 3.")
	if strings.Contains(again.stdout, "Next: semar run") {
		t.Error("an import that wrote nothing names a next step")
	}
}

func TestImportRefusesAFileThatIsNotAFormat(t *testing.T) {
	r := newRepo(t, repoOptions{})
	p := r.path("in", "other.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"openapi":"3.0.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	res := r.must(t, cli.ExitError, "import", p, "--ticket", "SHOP-77")
	contains(t, "the error", res.stderr, "semar: the file is neither a Postman collection (format 2.1) nor a HAR file")
	if exists(t, r.path(".semar", "scenarios", "SHOP-77")) {
		t.Error("a refused file wrote a scenario")
	}
}

func TestImportOutsideTheEnvironmentWritesNothing(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	file := r.importFixture(t, "shop.har")
	res := r.must(t, cli.ExitOK, "import", file, "--ticket", "SHOP-77")
	contains(t, "the summary", res.stdout, "Wrote 0 scenarios, left out 3.", "shop.example")
}
