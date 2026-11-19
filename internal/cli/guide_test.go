package cli

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	guideHeading = regexp.MustCompile("(?m)^#### `(semar [a-z ]+)`$")
	guideFlagRow = regexp.MustCompile("(?m)^\\| (?:`-[a-z]`, )?`--([a-z-]+)")
)

// guideCommands gives the text under each command heading of the command reference of the guide.
func guideCommands(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "GUIDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	out := map[string]string{}
	found := guideHeading.FindAllStringSubmatchIndex(text, -1)
	for i, m := range found {
		end := len(text)
		if i+1 < len(found) {
			end = found[i+1][0]
		}
		body := text[m[1]:end]
		if next := strings.Index(body, "\n## "); next >= 0 {
			body = body[:next]
		}
		out[text[m[2]:m[3]]] = body
	}
	return out
}

// leafCommands gives each command that a person can run, by its full name.
func leafCommands(root *cobra.Command) map[string]*cobra.Command {
	out := map[string]*cobra.Command{}
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			switch {
			case sub.Hidden, sub.Name() == "help", sub.Name() == "completion":
			case sub.HasSubCommands():
				walk(sub)
			default:
				out[sub.CommandPath()] = sub
			}
		}
	}
	walk(root)
	return out
}

// documentedFlags gives the flags that the guide must list for a command. --help and --dir are
// described once for every command.
func documentedFlags(c *cobra.Command) map[string]bool {
	out := map[string]bool{}
	c.Flags().VisitAll(func(f *pflag.Flag) {
		if !f.Hidden && f.Name != "help" && f.Name != "dir" {
			out[f.Name] = true
		}
	})
	return out
}

// awaitingTheGuide are flags that unit C1 of plan 35 writes into the guide. Remove an entry when it does.
var awaitingTheGuide = map[string][]string{"semar run": {"suite", "retries", "ci"}}

// guideDue names commands whose section of the guide is written by unit C1 of plan 35, after the merge.
var guideDue = map[string]bool{"semar import": true}

func TestTheGuideListsEveryCommandWithEveryFlag(t *testing.T) {
	guide := guideCommands(t)
	commands := leafCommands(newRoot(io.Discard, io.Discard, options{}.withDefaults()))
	if len(commands) == 0 {
		t.Fatal("found no command")
	}

	for name, c := range commands {
		body, ok := guide[name]
		if guideDue[name] && !ok {
			continue
		}
		if !ok {
			t.Errorf("docs/GUIDE.md has no heading #### `%s`", name)
			continue
		}
		flags := documentedFlags(c)
		for flag := range flags {
			if slices.Contains(awaitingTheGuide[name], flag) {
				continue
			}
			if !strings.Contains(body, "`--"+flag+"`") && !strings.Contains(body, "`--"+flag+" ") {
				t.Errorf("docs/GUIDE.md: %s does not list --%s", name, flag)
			}
		}
		for _, row := range guideFlagRow.FindAllStringSubmatch(body, -1) {
			if !flags[row[1]] {
				t.Errorf("docs/GUIDE.md: %s lists --%s, which the command does not have", name, row[1])
			}
		}
	}
	for name := range guide {
		if _, ok := commands[name]; !ok {
			t.Errorf("docs/GUIDE.md describes %s, which is not a command", name)
		}
	}
}
