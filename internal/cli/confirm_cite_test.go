package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func ticketScenario(cite, as string) domain.Scenario {
	status := 422
	return domain.Scenario{
		ID: "SHOP-482-01",
		Steps: []domain.Step{{
			ID: "move", As: as,
			Expect: []domain.Expectation{{Check: domain.Check{Status: &status}, Source: domain.SourceTicket, Cite: cite, By: "model"}},
		}},
	}
}

func writeRepoFile(t *testing.T, dir, name, text string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func screen(t *testing.T, dir string, sc domain.Scenario) string {
	t.Helper()
	u := needsConfirmation(dir, sc, nil)
	if len(u.lines) != 1 {
		t.Fatalf("want one line, got %q", u.lines)
	}
	return u.lines[0]
}

func TestConfirmScreenShowsTheCitedLineAndAs(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, dir, "docs/tickets/SHOP-482.md", "# SHOP-482\n\nA move to another board is refused with 422.\nthird\n")
	got := screen(t, dir, ticketScenario("docs/tickets/SHOP-482.md:3 a move to another board is refused", "carol"))
	for _, want := range []string{
		"step move as carol:", "cite: docs/tickets/SHOP-482.md:3 a move to another board is refused",
		"docs/tickets/SHOP-482.md line 3: A move to another board is refused with 422.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the screen lacks %q:\n%s", want, got)
		}
	}
}

func TestConfirmScreenSaysWhenTheLineIsNotFound(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, dir, "docs/tickets/SHOP-482.md", "one\ntwo\n")
	for name, cite := range map[string]string{
		"a line past the end":    "docs/tickets/SHOP-482.md:9 x",
		"line zero":              "docs/tickets/SHOP-482.md:0 x",
		"a missing file":         "docs/tickets/NOPE.md:1 x",
		"a path out of the tree": "../outside.md:1 x",
		"an absolute path":       "/etc/hostname:1 x",
	} {
		t.Run(name, func(t *testing.T) {
			if got := screen(t, dir, ticketScenario(cite, "")); !strings.Contains(got, "line not found") {
				t.Errorf("want \"line not found\":\n%s", got)
			}
		})
	}
}

func TestConfirmScreenShowsNoLineWithoutAFileCite(t *testing.T) {
	dir := t.TempDir()
	for _, cite := range []string{"SHOP-482: a refund must be rejected", "see the ticket"} {
		got := screen(t, dir, ticketScenario(cite, ""))
		if strings.Contains(got, "line not found") || strings.Contains(got, "\n") {
			t.Errorf("cite %q: unexpected citation text:\n%s", cite, got)
		}
		if strings.Contains(got, " as ") {
			t.Errorf("a step without as shows one:\n%s", got)
		}
	}
}

func TestConfirmScreenEscapesAndCutsTheCitedLine(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, dir, "t.md", "bell\x07 \u202eevil\t"+strings.Repeat("y", 400)+"\n")
	got := screen(t, dir, ticketScenario("t.md:1 x", ""))
	if strings.ContainsAny(got, "\x07\u202e") {
		t.Errorf("the line holds a control character:\n%q", got)
	}
	if !strings.Contains(got, `\x07`) {
		t.Errorf("the control character is not shown as an escape:\n%q", got)
	}
	_, cited, _ := strings.Cut(got, "t.md line 1: ")
	if n := len([]rune(cited)); n > 300 {
		t.Errorf("the cited line is %d characters, want at most 300", n)
	}
	if strings.Count(got, "\n") != 1 {
		t.Errorf("the citation is not one extra line:\n%q", got)
	}
}

func TestConfirmScreenCitesOnlyTicketExpectations(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, dir, "t.md", "x\n")
	sc := ticketScenario("t.md:1 x", "")
	sc.Steps[0].Expect[0].Source = domain.SourceDiff
	sc.Steps[0].Expect[0].Cite = "t.md:1 x"
	if got := screen(t, dir, sc); strings.Contains(got, "line 1:") {
		t.Errorf("a diff expectation shows a cited line:\n%s", got)
	}
}
