package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/confirm"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/fixture"
)

const orderPaidFixture = `apiVersion: semar/v1
kind: Fixture
id: order.paid
description: A paid order of 1000 that belongs to the given customer
version: 1.0.0
owner: team-orders
params:
  customer: { default: "{{data.customers.default}}" }
find:
  - request: { service: orders, method: GET, path: /v1/orders, query: { status: paid, customer: "{{customer}}" } }
    captures: [{ name: id, jsonpath: "$.data[0].id" }]
create:
  - request: { service: orders, method: POST, path: /v1/orders, json: { amount: 1000, customer: "{{customer}}", ref: "semar-{{run.id}}" } }
    captures: [{ name: id, jsonpath: "$.data.id" }]
  - request: { service: payments, method: POST, path: /v1/payments, json: { orderId: "{{id}}" } }
  - request: { service: orders, method: GET, path: "/v1/orders/{{id}}" }
    until: { jsonpath: "$.data.status", equals: paid, timeout: 30s, every: 1s }
outputs: [id]
cleanup:
  - request: { service: orders, method: POST, path: "/v1/orders/{{id}}/cancel" }
`

func (r *repo) writeFixture(t *testing.T, name, text string) {
	t.Helper()
	dir := r.path(".semar", "fixtures")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// hashOfFixture is the hash of a fixture file, read as confirm --fixture reads it.
func hashOfFixture(t *testing.T, text string) domain.Hash {
	t.Helper()
	r := &repo{dir: t.TempDir()}
	r.writeFixture(t, "order.paid.yaml", text)
	h, err := cli.FixtureHashOf(r.dir, "order.paid")
	if err != nil || h == "" {
		t.Fatalf("hash = %q, %v", h, err)
	}
	return h
}

func (r *repo) approvals(t *testing.T) *confirm.Local { return confirm.NewLocal(r.path(".semar")) }

func TestConfirmFixtureApproves(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	r.writeFixture(t, "order.paid.yaml", orderPaidFixture)

	res := r.must(t, cli.ExitOK, "confirm", "--fixture", "order.paid", "--yes")
	h := hashOfFixture(t, orderPaidFixture)
	contains(t, "stdout", res.stdout,
		"Fixture order.paid 1.0.0 (owner team-orders), hash "+string(h)+":",
		"find 1: GET orders /v1/orders query customer={{customer}} status=paid, captures id=$.data[0].id\n",
		"create 1: POST orders /v1/orders json {\"amount\":1000,\"customer\":\"{{customer}}\",\"ref\":\"semar-{{run.id}}\"}, captures id=$.data.id\n",
		"create 2: POST payments /v1/payments json {\"orderId\":\"{{id}}\"}",
		"create 3: GET orders /v1/orders/{{id}} until $.data.status equals \"paid\" within 30s every 1s",
		"cleanup 1: POST orders /v1/orders/{{id}}/cancel",
		`customer (default "{{data.customers.default}}")`,
		"outputs: id",
		"approved fixture order.paid 1.0.0 for hash "+string(h)+" as tester (local, unverified identity)",
	)
	if ok, err := r.approvals(t).FixtureApproved("order.paid", h); !ok || err != nil {
		t.Fatalf("FixtureApproved = %v, %v", ok, err)
	}

	// A second approval changes nothing and asks nothing.
	r.opts.Terminal = func() bool { return false }
	res = r.must(t, cli.ExitOK, "confirm", "--fixture", "order.paid")
	contains(t, "stdout", res.stdout, "fixture order.paid 1.0.0 is already approved for hash "+string(h))

	// A change to the fixture lapses the approval: the new hash needs its own.
	r.writeFixture(t, "order.paid.yaml", strings.Replace(orderPaidFixture, "amount: 1000", "amount: 2000", 1))
	r.must(t, cli.ExitUndecided, "confirm", "--fixture", "order.paid")
	changed := hashOfFixture(t, strings.Replace(orderPaidFixture, "amount: 1000", "amount: 2000", 1))
	if changed == h {
		t.Fatal("the change did not change the hash")
	}
	if ok, _ := r.approvals(t).FixtureApproved("order.paid", changed); ok {
		t.Fatal("a change to a fixture was approved without a person")
	}
}

func TestConfirmFixtureNeedsYesWithoutTerminal(t *testing.T) {
	for _, tc := range []struct {
		name     string
		terminal bool
		answer   string
		want     int
		stored   bool
	}{
		{"no terminal", false, "yes\n", cli.ExitUndecided, false},
		{"answer no", true, "n\n", cli.ExitUndecided, false},
		{"no answer", true, "", cli.ExitUndecided, false},
		{"answer yes", true, "yes\n", cli.ExitOK, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRepo(t, repoOptions{offline: true})
			r.writeFixture(t, "order.paid.yaml", orderPaidFixture)
			r.opts.Stdin = strings.NewReader(tc.answer)
			r.opts.Terminal = func() bool { return tc.terminal }
			res := r.must(t, tc.want, "confirm", "--fixture", "order.paid")
			contains(t, "stdout", res.stdout, "Fixture order.paid 1.0.0")
			if tc.terminal {
				contains(t, "stdout", res.stdout, "Approve it? [y/N]")
			}
			if got := exists(t, r.path(".semar", "store", "approvals")); got != tc.stored {
				t.Errorf("stored = %v, want %v", got, tc.stored)
			}
		})
	}
}

func TestConfirmFixturePrintsStepsSafely(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	text := strings.Replace(orderPaidFixture, "path: /v1/orders, query", `path: "/v1/orders\u001b[2J", query`, 1)
	text = strings.Replace(text, "A paid order of 1000 that belongs to the given customer", `"x\n  find 9: GET orders /v1/fake\u202e"`, 1)
	r.writeFixture(t, "order.paid.yaml", text)
	res := r.must(t, cli.ExitOK, "confirm", "--fixture", "order.paid", "--yes")
	contains(t, "stdout", res.stdout, `/v1/orders\x1b[2J`, `description: x\n  find 9: GET orders /v1/fake`)
	for _, bad := range []string{"\x1b", "\u202e"} {
		if strings.Contains(res.stdout, bad) {
			t.Errorf("stdout holds %q", bad)
		}
	}
	for _, line := range strings.Split(res.stdout, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "find 9:") {
			t.Errorf("a description made a line that looks like a step: %q", line)
		}
	}
}

func TestConfirmFixtureAndScenarioTogetherRefused(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	r.writeFixture(t, "order.paid.yaml", orderPaidFixture)
	for _, args := range [][]string{
		{"confirm", "--fixture", "order.paid", scenarioID, "--yes"},
		{"confirm", "--fixture", "order.paid", "--all", "--yes"},
	} {
		res := r.must(t, cli.ExitError, args...)
		contains(t, "stderr", res.stderr, "pass --fixture, or the ID of one scenario, or --all, and not several of them")
	}
	if exists(t, r.path(".semar", "store")) {
		t.Error("something was stored")
	}
}

func TestConfirmFixtureRefusesWhatItCannotRead(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	r.writeFixture(t, "order.paid.yaml", orderPaidFixture)
	r.writeFixture(t, "order-other.yaml", strings.Replace(orderPaidFixture, "id: order.paid", "id: order.paid", 1))
	r.writeFixture(t, "bad-key.yaml", strings.Replace(orderPaidFixture, "id: order.paid", "id: bad-key\nretries: 3", 1))
	r.writeFixture(t, "bad-kind.yaml", strings.Replace(strings.Replace(orderPaidFixture, "id: order.paid", "id: bad-kind", 1), "kind: Fixture", "kind: Scenario", 1))
	r.writeFixture(t, "twice.yaml", strings.Replace(orderPaidFixture, "id: order.paid", "id: twice", 1))
	r.writeFixture(t, "twice.yml", strings.Replace(orderPaidFixture, "id: order.paid", "id: twice", 1))
	r.writeFixture(t, "huge.yaml", "apiVersion: semar/v1\nkind: Fixture\nid: huge\n#"+strings.Repeat("x", 257<<10)+"\n")
	if err := os.Symlink(r.path(".semar", "fixtures", "order.paid.yaml"), r.path(".semar", "fixtures", "linked.yaml")); err != nil {
		t.Skip("symlinks are not available:", err)
	}

	for _, tc := range []struct{ id, want string }{
		{"../order.paid", "invalid fixture ID"},
		{"order/paid", "invalid fixture ID"},
		{"Order.paid", "invalid fixture ID"},
		{"", "invalid fixture ID"},
		{"missing", "fixture missing was not found"},
		{"order-other", "id order.paid does not match the file name order-other.yaml"},
		{"bad-key", "retries"},
		{"bad-kind", "kind must be Fixture"},
		{"twice", "has two files"},
		{"huge", "larger than 256 KiB"},
		{"linked", "symbolic link"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			res := r.must(t, cli.ExitError, "confirm", "--fixture", tc.id, "--yes")
			contains(t, "stderr", res.stderr, tc.want)
			if exists(t, r.path(".semar", "store", "approvals")) {
				t.Fatal("an approval was stored")
			}
		})
	}
}

func TestConfirmFixtureRefusesALinkedFixturesDirectory(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "order.paid.yaml"), []byte(orderPaidFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, r.path(".semar", "fixtures")); err != nil {
		t.Skip("symlinks are not available:", err)
	}
	res := r.must(t, cli.ExitError, "confirm", "--fixture", "order.paid", "--yes")
	contains(t, "stderr", res.stderr, "symbolic link")
}

func TestConfirmFixtureSeveralAndRepeated(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	r.writeFixture(t, "order.paid.yaml", orderPaidFixture)
	r.writeFixture(t, "customer.verified.yaml", strings.NewReplacer("id: order.paid", "id: customer.verified", "outputs: [id]", "outputs: [id]").Replace(orderPaidFixture))
	r.opts.Stdin = strings.NewReader("y\n")
	r.opts.Terminal = func() bool { return true }
	res := r.must(t, cli.ExitOK, "confirm", "--fixture", "order.paid", "--fixture", "customer.verified", "--fixture", "order.paid")
	contains(t, "stdout", res.stdout, "Approve them? [y/N]", "approved fixture order.paid", "approved fixture customer.verified")
	if n := strings.Count(res.stdout, "approved fixture order.paid"); n != 1 {
		t.Errorf("order.paid was approved %d times in one run", n)
	}
}

func TestConfirmFixtureNeedsAnApprover(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	r.writeFixture(t, "order.paid.yaml", orderPaidFixture)
	r.opts.User = func() (string, error) { return " ", nil }
	res := r.must(t, cli.ExitError, "confirm", "--fixture", "order.paid", "--yes")
	contains(t, "stderr", res.stderr, "the approver has no name; set git config user.name")
	if exists(t, r.path(".semar", "store", "approvals")) {
		t.Error("an approval was stored")
	}
}

func TestConfirmWithoutFixtureStillNeedsAScenarioOrAll(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	res := r.must(t, cli.ExitError, "confirm", "--yes")
	contains(t, "stderr", res.stderr, "pass the ID of one scenario, or --all")
}

// The hash that confirm --fixture shows and stores is the hash that the catalogue gives for the same
// file, whatever form a number takes. A run asks the catalogue, so a difference would make every
// approval miss.
func TestConfirmFixtureHashIsTheCatalogueHash(t *testing.T) {
	body := func(amount string) string {
		return strings.Replace(orderPaidFixture, "amount: 1000", "amount: "+amount, 1)
	}
	for _, tc := range []struct{ name, text string }{
		{"as written", orderPaidFixture},
		{"exponent in a body", body("1e3")},
		{"two mebibytes", body("2097152")},
		{"one above two mebibytes", body("2097153")},
		{"two mebibytes as a float", body("2097152.0")},
		{"two mebibytes with an exponent", body("2.097152e6")},
		{"negative size", body("-2097152")},
		{"exponent in an until", strings.Replace(orderPaidFixture, "equals: paid", "equals: 1e3", 1)},
		{"a list and a nested number", strings.Replace(orderPaidFixture, "amount: 1000", "amount: 1000, sizes: [1e3, 2097152, {limit: 2e6}]", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRepo(t, repoOptions{offline: true})
			r.writeFixture(t, "order.paid.yaml", tc.text)

			c, err := fixture.Load(r.dir, nil)
			if err != nil || len(c.Problems) != 0 {
				t.Fatalf("Load = %v, %v", c.Problems, err)
			}
			f, ok := c.Get("order.paid")
			if !ok {
				t.Fatal("the catalogue has no order.paid")
			}
			want := domain.FixtureHash(f)

			res := r.must(t, cli.ExitOK, "confirm", "--fixture", "order.paid", "--yes")
			contains(t, "stdout", res.stdout, "hash "+string(want)+":", "approved fixture order.paid 1.0.0 for hash "+string(want))
			if ok, err := r.approvals(t).FixtureApproved("order.paid", want); !ok || err != nil {
				t.Fatalf("the hash of the catalogue is not approved: %v, %v", ok, err)
			}
			entries, err := os.ReadDir(r.path(".semar", "store", "approvals", "fixtures", "order.paid"))
			if err != nil || len(entries) != 1 || entries[0].Name() != string(want) {
				t.Fatalf("stored hashes = %v, %v, want only %s", entries, err, want)
			}
		})
	}
}

func TestConfirmFixtureRefusesAFixtureThatDoesNotValidate(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	r.writeFixture(t, "order.paid.yaml", strings.Replace(orderPaidFixture, "version: 1.0.0", "version: 1", 1))
	res := r.must(t, cli.ExitError, "confirm", "--fixture", "order.paid", "--yes")
	contains(t, "stderr", res.stderr, "version 1 must be MAJOR.MINOR.PATCH")
	if exists(t, r.path(".semar", "store", "approvals")) {
		t.Error("an approval was stored")
	}
}

// --env names the environment of a journey's legs. Anywhere else it would be ignored, so it is refused.
func TestConfirmEnvOnlyWithJourney(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	r.writeFixture(t, "order.paid.yaml", orderPaidFixture)
	for _, args := range [][]string{
		{"confirm", scenarioID, "--env", "uat", "--yes"},
		{"confirm", "--all", "--env", "uat", "--yes"},
		{"confirm", "--fixture", "order.paid", "--env", "uat", "--yes"},
	} {
		res := r.must(t, cli.ExitError, args...)
		contains(t, "stderr", res.stderr, "--env is for --journey only")
	}
	if exists(t, r.path(".semar", "store")) {
		t.Error("something was stored")
	}
}
