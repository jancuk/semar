package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/knowledge"
)

// asker puts a question to a person at a terminal and reads one line. It reads from one buffered
// reader for the whole conversation, so a line that was sent early is not lost between questions.
type asker struct {
	ctx context.Context
	in  *bufio.Reader
	out io.Writer
}

func newAsker(ctx context.Context, in io.Reader, out io.Writer) *asker {
	return &asker{ctx: ctx, in: bufio.NewReader(in), out: out}
}

// ask prints the question and returns the answer without its line end. ok is false when the input
// ended before any text or the command was interrupted, which is the same as no answer.
func (a *asker) ask(question string) (answer string, ok bool) {
	fmt.Fprint(a.out, question+": ")
	line := make(chan string, 1)
	go func() {
		s, _ := a.in.ReadString('\n')
		line <- s
	}()
	select {
	case s := <-line:
		if s == "" {
			fmt.Fprintln(a.out)
			return "", false
		}
		return strings.TrimSpace(s), true
	case <-a.ctx.Done():
		return "", false
	}
}

// askInit completes the flags of init with what a person says. It asks only what no flag gave.
func askInit(a *asker, dir string, f *initFlags, given map[string]bool) error {
	for _, line := range foundLines(dir) {
		fmt.Fprintln(a.out, line)
	}
	addr, _ := a.ask("Address of the service in the test environment")
	if addr == "" {
		return fmt.Errorf("--base-url is required: the address of the service in the environment, such as https://orders.uat.example.com")
	}
	f.baseURL = addr

	if !given["token-env"] {
		name, _ := a.ask("Environment variable that holds the token [" + defaultTokenEnv + "]")
		if name != "" {
			f.tokenEnv = name
		}
	}
	if !describedAlready(f.describe, "default") {
		if d, _ := a.ask("Who is this account, in one line"); d != "" {
			f.describe = append(f.describe, "default="+d)
		}
	}
	if err := askWebSignIn(a, f, "default", ""); err != nil {
		return err
	}
	if given["account"] {
		return nil
	}
	for {
		name, _ := a.ask("Another account? Name, or Enter for none")
		if name == "" {
			return nil
		}
		if err := idcheck.Check("account name", name); err != nil {
			return fmt.Errorf("the account name %q: %w", name, err)
		}
		env, _ := a.ask("  Environment variable of its token")
		if env == "" {
			return fmt.Errorf("the account %s needs the environment variable that holds its token", name)
		}
		f.accounts = append(f.accounts, name+"="+env)
		if !describedAlready(f.describe, name) {
			if d, _ := a.ask("  Who is this account, in one line"); d != "" {
				f.describe = append(f.describe, name+"="+d)
			}
		}
		if err := askWebSignIn(a, f, name, "  "); err != nil {
			return err
		}
	}
}

// askWebSignIn asks which user an account signs in with on the web, and the variable of its
// password. It asks nothing of an account that a flag named.
func askWebSignIn(a *asker, f *initFlags, name, indent string) error {
	if describedAlready(f.webUsers, name) || describedAlready(f.webPasswordEnvs, name) {
		return nil
	}
	user, _ := a.ask(indent + "User that this account signs in with on the web, or Enter for none")
	if user == "" {
		return nil
	}
	env, _ := a.ask(indent + "Environment variable of its web password")
	if env == "" {
		return fmt.Errorf("the account %s needs the environment variable that holds its web password", name)
	}
	f.webUsers = append(f.webUsers, name+"="+user)
	f.webPasswordEnvs = append(f.webPasswordEnvs, name+"="+env)
	return nil
}

func describedAlready(describe []string, name string) bool {
	for _, d := range describe {
		if n, _, _ := strings.Cut(d, "="); n == name {
			return true
		}
	}
	return false
}

// foundLines says what init found in the repository: the API document, the documents, and the
// ticket files, as the knowledge of the plan would list them.
func foundLines(dir string) []string {
	var parts []string
	for _, k := range knowledge.Conventional(dir) {
		switch k.ID {
		case "api":
			parts = append(parts, k.Path+" (API document)")
		case "docs":
			parts = append(parts, fmt.Sprintf("%s/ (%s)", k.Path, plural(countFiles(dir, k.Path, "docs/tickets"), "document")))
		case "tickets":
			parts = append(parts, fmt.Sprintf("%s/ (%s)", k.Path, plural(countFiles(dir, k.Path, ""), "ticket")))
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return []string{"Found   " + strings.Join(parts, ", ")}
}

const maxCounted = 10000

// countFiles counts the regular files below rel, except those below skip. A link is never followed.
func countFiles(dir, rel, skip string) int {
	n := 0
	root := filepath.Join(dir, filepath.FromSlash(rel))
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil || n >= maxCounted:
			return filepath.SkipAll
		case d.IsDir():
			if r, e := filepath.Rel(dir, p); e == nil && skip != "" && filepath.ToSlash(r) == skip {
				return filepath.SkipDir
			}
		case d.Type().IsRegular():
			n++
		}
		return nil
	})
	return n
}

// printAskedDone ends an init that asked its questions. It lists the variables that the person
// still has to export, and the next command.
func printAskedDone(w io.Writer, f initFlags, lookup func(string) (string, bool)) {
	fmt.Fprintf(w, "Wrote %s.", configFileName)
	var missing []string
	need := func(env, what, who string) {
		if v, ok := lookup(env); !ok || v == "" {
			missing = append(missing, fmt.Sprintf("export %s=<the %s of %s>", env, what, who))
		}
	}
	need(f.tokenEnv, "token", "the default account")
	for _, a := range f.accounts {
		if name, env, ok := strings.Cut(a, "="); ok {
			need(env, "token", "the account "+name)
		}
	}
	for _, a := range f.webPasswordEnvs {
		if name, env, ok := strings.Cut(a, "="); ok {
			need(env, "web password", "the account "+name)
		}
	}
	if len(missing) == 0 {
		fmt.Fprintln(w, " Next: semar run")
		return
	}
	fmt.Fprintln(w)
	for _, m := range missing {
		fmt.Fprintln(w, m)
	}
	fmt.Fprintln(w, "Next: semar run")
}

const (
	versionPath     = "/version"
	probeTimeout    = 5 * time.Second
	maxProbeBody    = 64 << 10
	probeNotFoundTo = "To name one, add build: {probe: http, url: <address>, commit: <JSONPath of the commit>} to the service in " + configFileName
)

// findBuildProbe looks for the build probe of a service the way a team usually builds one: GET to
// /version on the address of the service, answered with JSON that holds the commit at $.commit.
// It sends that one request, with no credential, and follows no redirect, so it reaches only the
// host of the service. It returns the address when it found a probe.
func findBuildProbe(ctx context.Context, client *http.Client, baseURL string) (string, bool) {
	if client == nil {
		client = &http.Client{Timeout: probeTimeout}
	}
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	target := strings.TrimRight(baseURL, "/") + versionPath
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "semar/"+Version)
	resp, err := c.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProbeBody))
	if err != nil {
		return "", false
	}
	var doc struct {
		Commit any `json:"commit"`
	}
	if json.Unmarshal(body, &doc) != nil {
		return "", false
	}
	if s, ok := doc.Commit.(string); !ok || strings.TrimSpace(s) == "" {
		return "", false
	}
	return target, true
}
