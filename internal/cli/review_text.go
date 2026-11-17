package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/jancuk/semar/internal/check"
	"github.com/jancuk/semar/internal/confirm"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/scenario"
)

// localIdentity says that nothing checked who the user is. FR-11
const localIdentity = "local, unverified identity"

// unconfirmed is a scenario with the expected results that need a person.
type unconfirmed struct {
	sc      domain.Scenario
	label   string // how the heading names it; "scenario <id>" when empty
	lines   []string
	needs   []domain.Expectation // the expected results that lines describe, in the same order
	sources []domain.Source
	file    string // the scenario file, from the repository, when the scenario has one
	gherkin string // the scenario as Gherkin text with its sources and cites, shown above the list
}

// needsConfirmation lists the expected results of a scenario that a person must confirm. A step
// that acts as another account says so. For a ticket expectation whose cite names a line of a file
// of the repository at dir, the line is printed as it is in the working tree, so that the person can
// compare it with the check.
func needsConfirmation(dir string, sc domain.Scenario, trust []domain.Source) unconfirmed {
	u := unconfirmed{sc: sc}
	for _, st := range sc.Steps {
		for _, e := range st.Expect {
			if !confirm.NeedsExpectation(e, trust) {
				continue
			}
			head := "step " + string(st.ID)
			if st.As != "" {
				head += " as " + st.As
			}
			line := fmt.Sprintf("%s: %s (source %s", head, check.DescribeFull(e.Check), e.Source)
			if e.Cite != "" {
				line += ", cite: " + e.Cite
			}
			line = terminalSafe(line + ")")
			if e.Source == domain.SourceTicket {
				if file, n, ok := citedLine(e.Cite); ok {
					line += "\n      " + citedText(dir, file, n)
				}
			}
			u.lines = append(u.lines, line)
			u.sources = append(u.sources, e.Source)
			u.needs = append(u.needs, e)
		}
	}
	return u
}

// scenarioText is the Gherkin text of a scenario for the person who confirms it, and the file it came from.
// It is the file as it is, because that is what the person reads and edits. When the file cannot be read,
// the scenario is written out from what Semar read, so that the person still sees Gherkin.
func scenarioText(dir string, sc domain.Scenario) (file, text string) {
	if files, err := scenarioFiles(dir); err == nil && len(files[sc.ID]) == 1 {
		if rel, err := filepath.Rel(dir, files[sc.ID][0]); err == nil && filepath.IsLocal(rel) {
			if data, ok := readCited(dir, rel); ok {
				return filepath.ToSlash(rel), terminalSafe(data)
			}
		}
	}
	data, err := scenario.Render(sc, "")
	if err != nil {
		return "", ""
	}
	return "scenario " + string(sc.ID), terminalSafe(string(data))
}

// readCited reads at most maxCitedFile bytes of a file below dir, through a root.
func readCited(dir, rel string) (string, bool) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", false
	}
	defer root.Close()
	f, err := root.Open(rel)
	if err != nil {
		return "", false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxCitedFile+1))
	if err != nil || len(data) > maxCitedFile {
		return "", false
	}
	return string(data), true
}

const maxCitedText = 300

// maxCitedFile bounds how much of a cited file is read.
const maxCitedFile = 1 << 20

var citeLine = regexp.MustCompile(`^([^\s:]+):([0-9]{1,9})(?:\s|$)`)

// citedLine reads <repo file>:<line> from the start of a cite.
func citedLine(cite string) (file string, n int, ok bool) {
	m := citeLine.FindStringSubmatch(cite)
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[2])
	return m[1], n, err == nil
}

// citedLineText is line n of a file below dir, as one escaped line of at most maxCitedText
// characters. ok is false when the file or the line is not there, or the path leaves the repository.
func citedLineText(dir, file string, n int) (text string, ok bool) {
	if n < 1 || !filepath.IsLocal(file) {
		return "", false
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", false
	}
	defer root.Close()
	f, err := root.Open(file)
	if err != nil {
		return "", false
	}
	defer f.Close()
	sc := bufio.NewScanner(io.LimitReader(f, maxCitedFile))
	sc.Buffer(nil, maxCitedFile)
	for i := 1; sc.Scan(); i++ {
		if i < n {
			continue
		}
		text := terminalSafe(strings.ReplaceAll(sc.Text(), "\t", " "))
		if r := []rune(text); len(r) > maxCitedText {
			text = string(r[:maxCitedText-3]) + "..."
		}
		return text, true
	}
	return "", false
}

// citedText is the text that the screen of a review prints for a cited line: the line of the file
// in the working tree, or "line not found" when the file or the line is not there. A path outside
// the repository is never read.
func citedText(dir, file string, n int) string {
	if text, ok := citedLineText(dir, file, n); ok {
		return fmt.Sprintf("%s line %d: %s", terminalSafe(file), n, text)
	}
	return fmt.Sprintf("%s line %d: line not found", terminalSafe(file), n)
}

// lineReader reads the answers of a person one line at a time from one buffered reader, so that
// answers typed ahead are kept for the next question.
type lineReader struct{ r *bufio.Reader }

func newLineReader(in io.Reader) *lineReader { return &lineReader{r: bufio.NewReader(in)} }

// read returns one line, or false at the end of the input or when ctx ends first. A read from a
// terminal cannot be cancelled, so on cancel the reader is left to end with the process.
func (l *lineReader) read(ctx context.Context) (string, bool) {
	type answer struct {
		s   string
		err error
	}
	got := make(chan answer, 1)
	go func() {
		s, err := l.r.ReadString('\n')
		got <- answer{s, err}
	}()
	select {
	case a := <-got:
		return a.s, a.err == nil || a.s != ""
	case <-ctx.Done():
		return "", false
	}
}

// terminalSafe makes text from a scenario file safe to print for a person who is about to confirm it.
// Format characters (category Cf, such as the bidi controls) are removed, because they reorder or hide text.
// A control character other than newline and tab, and the line and paragraph separators, show as an escape such as \x1b.
func terminalSafe(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Cf, r):
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == '\u2028' || r == '\u2029':
			fmt.Fprintf(&b, `\u%04x`, r)
		case unicode.IsControl(r):
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
