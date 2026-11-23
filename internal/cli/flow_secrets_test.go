//go:build unix

package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/sampleapp"
)

const flowWithASecret = `apiVersion: semar/v1
kind: Flow
id: login
surface: android
service: shop-app
app: shop-app
steps:
  - step: start
    mobile: { do: launch }
  - step: password
    mobile: { do: type, screen: SignIn, element: password, secret: env:SHOP_FLOW_PASSWORD }
`

// A secret that only a flow names is a secret all the same: a harness never inherits it.
func TestSecretOfAFlowStaysOutOfThePlanHarness(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "env")
	bin := filepath.Join(dir, "claude-fake")
	if err := os.WriteFile(bin, []byte(fmt.Sprintf("#!/bin/sh\nenv > %q\n", envFile)), 0o755); err != nil {
		t.Fatal(err)
	}
	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, goodDraft)
	p.useProviders(t, "  - id: planner\n    kind: claude\n", "")
	p.writeRel(t, ".semar/flows/login.yaml", flowWithASecret)
	p.writeRel(t, ".semar/store/scripts/AND-1/abcd.yaml", `apiVersion: semar/v1
kind: Script
scenario: AND-1
expectHash: sha256:0000
steps:
  - step: start
    mobile: { service: shop-app, app: shop-app, do: launch }
  - step: password
    mobile: { service: shop-app, app: shop-app, do: type, screen: SignIn, element: password, secret: env:SHOP_SCRIPT_PASSWORD }
`)
	p.opts.LookupEnv = func(k string) (string, bool) {
		if k == "SEMAR_CLAUDE_BIN" {
			return bin, true
		}
		return os.LookupEnv(k)
	}
	p.opts.Environ = func() []string {
		return []string{"PATH=/usr/bin:/bin", "SHOP_FLOW_PASSWORD=flow-s3cret", "SHOP_SCRIPT_PASSWORD=script-s3cret", "ACME_REGION=eu"}
	}
	p.must(t, cli.ExitError, "plan", "--diff", "main..release", "--with-setup")
	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("the harness did not start: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "ACME_REGION=eu") {
		t.Errorf("the person's environment did not reach the harness:\n%s", got)
	}
	for _, secret := range []string{"SHOP_FLOW_PASSWORD", "flow-s3cret", "SHOP_SCRIPT_PASSWORD", "script-s3cret"} {
		if strings.Contains(got, secret) {
			t.Errorf("the harness inherited %s", secret)
		}
	}
}

// semar init in a set up repository says that a flow needs its secret on this machine.
func TestInitChecksTheSecretOfAFlow(t *testing.T) {
	r := newAndroidRig(t, androidOptions{})
	r.write(t, ".semar/flows/login.yaml", flowWithASecret)
	r.opts.LookupEnv = func(k string) (string, bool) {
		if k == "SHOP_FLOW_PASSWORD" {
			return "", false
		}
		return os.LookupEnv(k)
	}
	res := r.must(t, cli.ExitUndecided, "init")
	contains(t, "init", res.stdout, "SHOP_FLOW_PASSWORD is not set: a flow types it as a secret")
	r.opts.LookupEnv = func(k string) (string, bool) {
		if k == "SHOP_FLOW_PASSWORD" {
			return "x", true
		}
		return os.LookupEnv(k)
	}
	res = r.must(t, cli.ExitUndecided, "init")
	contains(t, "init", res.stdout, "SHOP_FLOW_PASSWORD is set")
}
