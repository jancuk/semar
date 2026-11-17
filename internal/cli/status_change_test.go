package cli_test

import (
	"os"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/vcs/gittest"
)

// git runs git in the directory of the repository under test, with fixed identities.
func (r *repo) git(t *testing.T, args ...string) string {
	t.Helper()
	return gittest.Run(t, r.dir, nil, args...)
}

// shopBranch puts the repository under test on a branch with the commits of the plan's example:
// one for SHOP-482, one for SHOP-490, and one with no ticket. Another ticket has use cases that
// the change does not touch.
func shopBranch(t *testing.T) *repo {
	t.Helper()
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482",
		entry("SHOP-482/a", "Refund limit", `gap: "not drafted"`)))
	r.writeRel(t, ".semar/obligations/other.yaml", obligations("SHOP-480", entry("SHOP-480/b", "Another ticket", `gap: "not drafted"`)))
	r.git(t, "init", "--quiet", "--initial-branch=main")
	r.git(t, "add", "--all")
	r.git(t, "commit", "--quiet", "-m", "base")
	r.git(t, "checkout", "--quiet", "-b", "feature/refund-limit")
	r.git(t, "commit", "--quiet", "--allow-empty", "-m", "SHOP-482 limit a refund to the paid amount")
	r.git(t, "commit", "--quiet", "--allow-empty", "-m", "SHOP-490 show the limit in the receipt")
	r.git(t, "commit", "--quiet", "--allow-empty", "-m", "tidy the logger")
	return r
}

func TestStatusShowsTheUseCasesOfTheChange(t *testing.T) {
	r := shopBranch(t)
	res := r.must(t, cli.ExitUndecided, "status")
	out := squash(res.stdout)
	contains(t, "the status", out,
		"On feature/refund-limit against main · local",
		"1 use case of SHOP-482, SHOP-490",
		"no test 1 SHOP-482/a not drafted → semar plan",
		"not planned 1 SHOP-490 → semar plan",
		`A commit has no ticket: `, `"tidy the logger"`,
		"Ship: no. 2 open.")
	if strings.Contains(out, "SHOP-480") {
		t.Errorf("a use case of a ticket that the change does not name is listed:\n%s", res.stdout)
	}

	all := r.must(t, cli.ExitUndecided, "status", "--all")
	contains(t, "every use case", squash(all.stdout), "2 use cases", "SHOP-480/b")

	one := r.must(t, cli.ExitUndecided, "status", "--ticket", "SHOP-480")
	contains(t, "one ticket", squash(one.stdout), "1 use case of SHOP-480", "SHOP-480/b")
}

func TestStatusMeasuresAgainstTheBaseThatIsNamed(t *testing.T) {
	r := shopBranch(t)
	r.git(t, "branch", "release", "HEAD~1")
	res := r.must(t, cli.ExitUndecided, "status", "--base", "release")
	contains(t, "the status", squash(res.stdout), "On feature/refund-limit against release", "no ticket")
	if strings.Contains(res.stdout, "SHOP-490") {
		t.Errorf("a ticket of a commit that the base already has is listed:\n%s", res.stdout)
	}

	r.rewriteConfig(t, func(s string) string { return s + "change:\n  base: release\n" })
	res = r.must(t, cli.ExitUndecided, "status")
	contains(t, "the configured base", res.stdout, "against release")

	res = r.must(t, cli.ExitError, "status", "--base", "nowhere")
	contains(t, "stderr", res.stderr, "nowhere")
}

func TestStatusOfAChangeWithNoTicketSaysSo(t *testing.T) {
	r := ledgerRepo(t)
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "Refund limit", `gap: "not drafted"`)))
	r.git(t, "init", "--quiet", "--initial-branch=main")
	r.git(t, "add", "--all")
	r.git(t, "commit", "--quiet", "-m", "base")
	r.git(t, "checkout", "--quiet", "-b", "chore/logger")
	r.git(t, "commit", "--quiet", "--allow-empty", "-m", "tidy the logger")

	res := r.must(t, cli.ExitUndecided, "status")
	contains(t, "stdout", res.stdout, "no ticket", "--all")
	r.must(t, cli.ExitUndecided, "status", "--all")
}

func TestStatusWritesOneHTMLFileThatNeedsNoNetwork(t *testing.T) {
	r := shopBranch(t)
	r.ledgerFile(t, obligations("SHOP-482",
		entry("SHOP-482/a", "Refund limit", `gap: "not drafted"`),
		entry("SHOP-482/p", "Receipt on a phone", manualEntryFields)))
	file := r.path("out.html")
	res := r.must(t, cli.ExitUndecided, "status", "--html", file)
	contains(t, "stderr", res.stderr, "wrote "+file)
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	contains(t, "the page", page, "On feature/refund-limit against main", "SHOP-482/a", "SHOP-482/p", "Read the document.", "Ship: no.", "SHOP-490")
	for _, outside := range []string{"http://", "https://", "<script", "<link", "src="} {
		if strings.Contains(page, outside) {
			t.Errorf("the page holds %q", outside)
		}
	}
	contains(t, "the text", res.stdout, "Ship: no.")
}
