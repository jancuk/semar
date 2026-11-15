package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/provider"
)

const statusTimeout = 10 * time.Second

var _ provider.Account = (*Driver)(nil)

var (
	emailPattern = regexp.MustCompile(`[^\s@]+@[^\s@]+`)
	plainValue   = regexp.MustCompile(`^[A-Za-z0-9._ -]{1,40}$`)
)

// Status runs the harness's own status command and keeps three of its fields. The command
// also prints the e-mail address and the organisation of the account, which are never read
// into the result. It runs in an empty directory, so that no settings of the directory that
// Semar was started in can take part.
func (d *Driver) Status(ctx context.Context) provider.AccountStatus {
	path, perr := d.binary()
	if perr == nil {
		perr = provider.RefuseInside(path, d.repoRoot, "")
	}
	if perr != nil {
		return provider.AccountStatus{Detail: perr.Detail}
	}
	dir, err := os.MkdirTemp("", "semar-claude-status-")
	if err != nil {
		return provider.AccountStatus{Detail: fmt.Sprintf("no empty directory for the status command: %v", errors.Unwrap(err))}
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	res, err := provider.RunProcess(ctx, provider.ProcessSpec{
		Path:      path,
		Args:      []string{"auth", "status", "--json"},
		Dir:       dir,
		Env:       d.childEnv(),
		StdoutCap: 1 << 20,
	})
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return provider.AccountStatus{Detail: fmt.Sprintf("claude auth status did not answer in %s", statusTimeout)}
	case err != nil:
		return provider.AccountStatus{Detail: "claude auth status could not be run: " + provider.Truncate(provider.Redact(scrub(err.Error())))}
	}
	var s struct {
		LoggedIn         bool   `json:"loggedIn"`
		AuthMethod       string `json:"authMethod"`
		SubscriptionType string `json:"subscriptionType"`
	}
	if json.Unmarshal(res.Stdout, &s) != nil {
		return provider.AccountStatus{Detail: fmt.Sprintf("claude auth status gave no answer that Semar can read, exit code %d: %s", res.ExitCode, provider.LastLine([]byte(scrub(string(res.StderrTail)))))}
	}
	st := provider.AccountStatus{SignedIn: s.LoggedIn, Method: method(s.AuthMethod), Plan: plain(s.SubscriptionType)}
	switch {
	case !st.SignedIn:
		st.Detail = "Claude Code is not signed in; sign in with claude auth login."
	case st.Plan != "":
		st.Detail = fmt.Sprintf("Signed in to Claude Code with the plan %s.", st.Plan)
	default:
		st.Detail = "Signed in to Claude Code."
	}
	return st
}

// LoginCommand is the harness's own sign-in command. It is nil when no binary may be started.
func (d *Driver) LoginCommand() []string {
	path, perr := d.binary()
	if perr == nil {
		perr = provider.RefuseInside(path, d.repoRoot, "")
	}
	if perr != nil {
		return nil
	}
	return []string{path, "auth", "login"}
}

func method(raw string) string {
	switch v := plain(raw); {
	case v == "claude.ai":
		return "subscription"
	case strings.Contains(strings.ToLower(v), "key"):
		return "api key"
	default:
		return v
	}
}

// plain keeps a short value of letters, digits, and a few marks, and drops anything else,
// so that no address or token in an unexpected field reaches a page.
func plain(v string) string {
	if !plainValue.MatchString(v) {
		return ""
	}
	return v
}

func scrub(s string) string { return emailPattern.ReplaceAllString(s, "[redacted]") }
