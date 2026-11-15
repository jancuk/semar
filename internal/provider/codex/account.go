package codex

import (
	"context"
	"fmt"
	"strings"

	"github.com/jancuk/semar/internal/provider"
)

// Status runs `codex login status`, which prints one line such as "Logged in using ChatGPT" on
// standard error. The line can hold a masked key, so only the known words are kept.
func (d *Driver) Status(ctx context.Context) provider.AccountStatus {
	path, perr := d.binary("")
	if perr != nil {
		return provider.AccountStatus{Detail: perr.Detail}
	}
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	res, err := provider.RunProcess(ctx, provider.ProcessSpec{Path: path, Args: []string{"login", "status"}, Env: d.env()})
	if err != nil {
		return provider.AccountStatus{Detail: "codex login status did not finish"}
	}
	text := strings.ToLower(string(res.Stdout) + "\n" + string(res.StderrTail))
	switch {
	case strings.Contains(text, "not logged in"):
		return provider.AccountStatus{Detail: "not signed in; run codex login"}
	case res.ExitCode != 0:
		return provider.AccountStatus{Detail: fmt.Sprintf("codex login status ended with exit code %d", res.ExitCode)}
	case !strings.Contains(text, "logged in using"):
		return provider.AccountStatus{Detail: "codex login status gave an answer that is not known"}
	case strings.Contains(text, "chatgpt"):
		return provider.AccountStatus{SignedIn: true, Method: "subscription", Detail: "signed in with ChatGPT"}
	case strings.Contains(text, "api key"), strings.Contains(text, "access keys"):
		return provider.AccountStatus{SignedIn: true, Method: "api key", Detail: "signed in with an API key"}
	case strings.Contains(text, "access token"):
		return provider.AccountStatus{SignedIn: true, Method: "access token", Detail: "signed in with an access token"}
	}
	return provider.AccountStatus{SignedIn: true, Detail: "signed in"}
}

// LoginCommand is nil when the binary is not found, because no command could sign in then.
func (d *Driver) LoginCommand() []string {
	path, perr := d.binary("")
	if perr != nil {
		return nil
	}
	return []string{path, "login"}
}
