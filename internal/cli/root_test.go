package cli_test

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/cli"
)

func TestVersion(t *testing.T) {
	var out, errw bytes.Buffer
	code := cli.Run(context.Background(), []string{"version"}, &out, &errw)
	if code != cli.ExitOK {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	if !strings.HasPrefix(out.String(), "semar ") {
		t.Fatalf("got %q", out.String())
	}
}

func TestUnknownCommandIsAUsageError(t *testing.T) {
	var out, errw bytes.Buffer
	if code := cli.Run(context.Background(), []string{"nope"}, &out, &errw); code != cli.ExitError {
		t.Fatalf("exit %d", code)
	}
}

func TestHelpListsTheCommandsOfANormalDay(t *testing.T) {
	var out, errw bytes.Buffer
	if code := cli.Run(context.Background(), []string{"--help"}, &out, &errw); code != cli.ExitOK {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	help := out.String()
	every, also := strings.Index(help, "\nEvery day\n"), strings.Index(help, "\nAlso\n")
	if every < 0 || also < every {
		t.Fatalf("the help has no Every day group before Also:\n%s", help)
	}
	listed := regexp.MustCompile(`(?m)^  ([a-z]+) `).FindAllStringSubmatchIndex(help, -1)
	var daily, more []string
	for _, m := range listed {
		name := help[m[2]:m[3]]
		if m[0] < every {
			continue
		}
		if m[0] < also {
			daily = append(daily, name)
		} else if m[0] < strings.Index(help, "\nFlags:") {
			more = append(more, name)
		}
	}
	if got := strings.Join(daily, " "); got != "init login plan run show" {
		t.Errorf("Every day lists %q, want init login plan run show:\n%s", got, help)
	}
	if got := strings.Join(more, " "); got != "check import log record rm" {
		t.Errorf("Also lists %q, want check import log record rm:\n%s", got, help)
	}
}

func TestACommandThatTheHelpHidesStillRuns(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"status", "--help"}, {"defer", "--help"}, {"help", "run"}, {"completion", "bash"}} {
		var out, errw bytes.Buffer
		if code := cli.Run(context.Background(), args, &out, &errw); code != cli.ExitOK || out.Len() == 0 {
			t.Errorf("semar %v: exit %d, stdout %d bytes, stderr %q", args, code, out.Len(), errw.String())
		}
	}
}

func TestRemovedCommandsNameTheNewOneAndExitWithThree(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"review"}, "semar review was removed: a test counts as soon as it is written; edit the test file to change what it expects"},
		{[]string{"review", "SHOP-482-01", "--yes"}, "semar review was removed: a test counts as soon as it is written; edit the test file to change what it expects"},
		{[]string{"confirm", "SHOP-482-01", "--yes"}, "semar confirm was removed: a test counts as soon as it is written; edit the test file to change what it expects"},
		{[]string{"ledger"}, "semar ledger was removed: use semar status, semar check, or semar defer"},
		{[]string{"ledger", "record", "SHOP-482/a", "passed"}, "semar ledger was removed: use semar status, semar check, or semar defer"},
		{[]string{"activity", "latest"}, "semar activity was removed: use semar show --activity"},
	} {
		var out, errw bytes.Buffer
		code := cli.Run(context.Background(), tc.args, &out, &errw)
		if code != cli.ExitError || out.Len() != 0 || strings.TrimSpace(errw.String()) != "semar: "+tc.want {
			t.Errorf("semar %v: exit %d, stdout %q, stderr %q; want exit 3 and one line %q", tc.args, code, out.String(), errw.String(), tc.want)
		}
	}
}
