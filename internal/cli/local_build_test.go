package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/vcs/gittest"
)

// localRepo is the repository of a service that has no build probe and runs on this machine, with
// its scenario confirmed and everything committed.
func localRepo(t *testing.T) (*repo, string) {
	t.Helper()
	r := newRepo(t, repoOptions{})
	r.rewriteConfig(t, func(s string) string {
		i, j := strings.Index(s, "        build:\n"), strings.Index(s, "authProfiles:")
		return s[:i] + s[j:]
	})
	r.addScript(t)
	if err := os.WriteFile(r.path("NOTES.md"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, r.dir, nil, "init", "--quiet")
	gittest.Run(t, r.dir, nil, "add", "--all")
	gittest.Run(t, r.dir, nil, "commit", "--quiet", "-m", "start")
	return r, gittest.Run(t, r.dir, nil, "rev-parse", "HEAD")
}

func (r *repo) startBuild(t *testing.T) domain.BuildRef {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.latestRun(t), "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var run domain.Run
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatal(err)
	}
	if len(run.BuildsStart) != 1 {
		t.Fatalf("builds at the start: %+v", run.BuildsStart)
	}
	return run.BuildsStart[0]
}

func TestALocalServiceRunsTheHeadCommit(t *testing.T) {
	r, head := localRepo(t)
	res := r.must(t, cli.ExitOK, "run", "--all")
	contains(t, "the header", res.stdout, "passed", "build: orders "+head+" (the head commit of the repository, not read from the service)")
	if got := r.startBuild(t); got != (domain.BuildRef{Service: "orders", Commit: head, Declared: true, Local: true}) {
		t.Errorf("build = %+v", got)
	}
	if n := r.app.Requests("GET", "/version"); n != 0 {
		t.Errorf("the service was asked for its build %d times", n)
	}
}

func TestTheBuildOfALocalServiceNamesTheChangesToTrackedFiles(t *testing.T) {
	r, head := localRepo(t)
	suffix := regexp.MustCompile(`^` + head + `\+[0-9a-f]{8}$`)
	build := func() string {
		r.must(t, cli.ExitOK, "run", "--all")
		return r.startBuild(t).Commit
	}
	edit := func(text string) {
		if err := os.WriteFile(r.path("NOTES.md"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	edit("two\n")
	first := build()
	if !suffix.MatchString(first) {
		t.Fatalf("build = %q, want the head commit, a plus sign, and 8 hexadecimal characters", first)
	}
	edit("three\n")
	if second := build(); !suffix.MatchString(second) || second == first {
		t.Errorf("a new change gave %q after %q", second, first)
	}
	edit("one\n")
	if back := build(); back != head {
		t.Errorf("with the change undone the build is %q, want %q", back, head)
	}
	res := r.must(t, cli.ExitOK, "run", "--all")
	if strings.Contains(res.stdout, "changes to its tracked files") {
		t.Errorf("a clean tree names changes:\n%s", res.stdout)
	}
	edit("two\n")
	res = r.must(t, cli.ExitOK, "run", "--all")
	contains(t, "the header", res.stdout, "(the head commit of the repository and the changes to its tracked files, not read from the service)")
}

func TestABuildOnTheCommandLineBeatsTheHeadCommit(t *testing.T) {
	r, _ := localRepo(t)
	r.must(t, cli.ExitOK, "run", "--all", "--build", "0123456789abcdef")
	if got := r.startBuild(t); got != (domain.BuildRef{Service: "orders", Commit: "0123456789abcdef", Declared: true}) {
		t.Errorf("build = %+v", got)
	}
}

func TestEveryCommandThatTakesBuildDefaultsToTheHeadCommit(t *testing.T) {
	r, head := localRepo(t)
	short := head[:7]
	r.must(t, cli.ExitOK, "run", "--all")
	r.ledgerFile(t, obligations("SHOP-482", entry("SHOP-482/a", "A", `gap: "not drafted"`)))
	for _, args := range [][]string{{"status", "--all"}, {"status", "--all", "--env", "local"}} {
		res := r.semar(t, t.Context(), args...)
		contains(t, strings.Join(args, " "), res.stdout, "orders at "+short)
	}
	if err := os.WriteFile(r.path("NOTES.md"), []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := r.semar(t, t.Context(), "status", "--all")
	if !regexp.MustCompile(`orders at ` + short + `\+[0-9a-f]{8}`).MatchString(res.stdout) {
		t.Errorf("status does not show the changes to tracked files:\n%s", res.stdout)
	}
}
