package cli_test

import (
	"os"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/sampleapp"
)

const (
	oldConfirmation = ".semar/store/confirmations/SHOP-482-01/abc/record.json"
	oldApproval     = ".semar/store/approvals/fixtures/order.paid/abc.json"
	oldConfirmText  = `{"scenario": "SHOP-482-01", "hash": "abc", "user": "tester", "sources": ["diff"]}` + "\n"
	oldApprovalText = `{"fixture": "order.paid", "hash": "abc", "user": "tester"}` + "\n"
	oldTrust        = "\npolicy:\n  trust:\n    confirm: [diff]\n"
)

func TestAnOldRepositoryLoadsRunsAndPrintsTheNoticeOnce(t *testing.T) {
	r := newRepo(t, repoOptions{})
	r.rewriteConfig(t, func(s string) string { return s + oldTrust })
	r.writeRel(t, oldConfirmation, oldConfirmText)
	r.writeRel(t, oldApproval, oldApprovalText)

	r.must(t, cli.ExitOK, "script", "add", r.scriptFile())
	res := r.must(t, cli.ExitOK, "run", "--ci", "--all")
	if n := strings.Count(res.stderr, config.TrustNotice); n != 1 {
		t.Errorf("run printed the notice %d times:\n%s", n, res.stderr)
	}
	for _, args := range [][]string{{"status"}, {"show", "latest"}} {
		res := r.semar(t, t.Context(), args...)
		if strings.Contains(res.stderr+res.stdout, config.TrustNotice) {
			t.Errorf("semar %v printed the notice", args)
		}
	}
	for rel, want := range map[string]string{oldConfirmation: oldConfirmText, oldApproval: oldApprovalText} {
		got, err := os.ReadFile(r.path(rel))
		if err != nil || string(got) != want {
			t.Errorf("%s was changed: %q, %v", rel, got, err)
		}
	}
}

func TestPlanPrintsTheOldTrustNoticeOnce(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.configure(t, goodDraft, goodDraft)
	p.rewriteConfig(t, func(s string) string { return s + oldTrust })
	res := p.must(t, cli.ExitOK, "plan", "--diff", "main..release")
	if n := strings.Count(res.stderr, config.TrustNotice); n != 1 {
		t.Errorf("plan printed the notice %d times:\n%s", n, res.stderr)
	}
}
