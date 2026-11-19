package cli_test

import (
	"bytes"
	"context"
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

func TestHelpGroupsTheEverydayCommandsFirst(t *testing.T) {
	var out, errw bytes.Buffer
	if code := cli.Run(context.Background(), []string{"--help"}, &out, &errw); code != cli.ExitOK {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	help := out.String()
	every, more := strings.Index(help, "Every day"), strings.Index(help, "More")
	if every < 0 || more < every {
		t.Fatalf("the help has no Every day group before More:\n%s", help)
	}
	for _, name := range []string{"init", "login", "plan", "run", "check", "defer", "record", "log", "show", "rm"} {
		at := strings.Index(help, "\n  "+name+" ")
		if at < every || at > more {
			t.Errorf("%s is not in the Every day group:\n%s", name, help)
		}
	}
	for _, name := range []string{"review", "status", "changes", "export", "fixture", "journey", "providers", "dashboard", "scenarios", "screens", "script", "version", "completion"} {
		if at := strings.Index(help, "\n  "+name+" "); at < more {
			t.Errorf("%s is not in the More group:\n%s", name, help)
		}
	}
	for _, gone := range []string{"confirm", "ledger", "activity"} {
		if strings.Contains(help, "\n  "+gone+" ") {
			t.Errorf("the removed command %s is in the help", gone)
		}
	}
}

func TestRemovedCommandsNameTheNewOneAndExitWithThree(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"confirm", "SHOP-482-01", "--yes"}, "semar confirm was removed: use semar review"},
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
