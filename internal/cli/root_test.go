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
