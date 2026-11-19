package cli_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/sampleapp"
)

func TestLogListsTheRunsNewestFirst(t *testing.T) {
	r := newRepo(t, repoOptions{app: sampleapp.Options{Defects: defects(sampleapp.RefundOverTotal)}})
	r.addScript(t)
	r.must(t, cli.ExitFailed, "run", "--scenario", scenarioID)
	r.putRun(t, "r-test-0001-x-unfinished", "", nil, false)
	r.must(t, cli.ExitFailed, "run", "--scenario", scenarioID)

	res := r.must(t, cli.ExitOK, "log")
	lines := strings.Split(strings.TrimRight(squash(res.stdout), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("%d lines, want one for each of 3 runs:\n%s", len(lines), res.stdout)
	}
	if !strings.HasPrefix(lines[0], "r-test-0002 ") || !strings.HasPrefix(lines[2], "r-test-0001 ") {
		t.Errorf("the runs are not newest first:\n%s", res.stdout)
	}
	contains(t, "the newest run", lines[0], "2026-11-14", "local", "orders 9f3c2ab", "failed 1")
	contains(t, "the run that did not finish", lines[1], "r-test-0001-x-unfinished", "did not finish")

	one := r.must(t, cli.ExitOK, "log", "-n", "1")
	if n := strings.Count(strings.TrimRight(one.stdout, "\n"), "\n") + 1; n != 1 {
		t.Errorf("-n 1 printed %d lines:\n%s", n, one.stdout)
	}
	r.must(t, cli.ExitError, "log", "-n", "0")
}

func TestLogWithNoRunSaysSo(t *testing.T) {
	r := newRepo(t, repoOptions{offline: true})
	res := r.must(t, cli.ExitOK, "log")
	contains(t, "stdout", res.stdout, "no run yet: semar run makes one")
}

func TestShowPrintsTheReportOfARun(t *testing.T) {
	r := newRepo(t, repoOptions{})
	r.addScript(t)
	r.must(t, cli.ExitOK, "run", "--scenario", scenarioID)
	r.must(t, cli.ExitOK, "run", "--scenario", scenarioID)

	newest := r.must(t, cli.ExitOK, "show")
	if !strings.HasPrefix(newest.stdout, "# Semar report r-test-0002\n") {
		t.Errorf("show does not print the newest report:\n%.200s", newest.stdout)
	}
	named := r.must(t, cli.ExitOK, "show", "r-test-0001")
	if !strings.HasPrefix(named.stdout, "# Semar report r-test-0001\n") {
		t.Errorf("show r-test-0001 prints:\n%.200s", named.stdout)
	}
	if latest := r.must(t, cli.ExitOK, "show", "latest"); latest.stdout != newest.stdout {
		t.Error("latest is not the newest run")
	}

	acts := r.must(t, cli.ExitOK, "show", "--activity")
	contains(t, "the activity", acts.stdout, "POST", "/v1/orders")
	writes := r.must(t, cli.ExitOK, "show", "r-test-0001", "--activity", "--writes")
	if strings.Contains(writes.stdout, "GET") {
		t.Errorf("--writes shows a read:\n%s", writes.stdout)
	}
	res := r.must(t, cli.ExitError, "show", "--writes")
	contains(t, "stderr", res.stderr, "--writes", "--activity")
	res = r.must(t, cli.ExitError, "show", "r-nope")
	contains(t, "stderr", res.stderr, "r-nope")
	r.must(t, cli.ExitError, "show", "../x")
}
