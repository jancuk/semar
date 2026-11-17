package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"

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
	}
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
	need := func(env, who string) {
		if v, ok := lookup(env); !ok || v == "" {
			missing = append(missing, fmt.Sprintf("export %s=<the token of %s>", env, who))
		}
	}
	need(f.tokenEnv, "the default account")
	for _, a := range f.accounts {
		if name, env, ok := strings.Cut(a, "="); ok {
			need(env, "the account "+name)
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
