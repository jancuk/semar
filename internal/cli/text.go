package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"unicode"
)

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

// terminalSafe makes text from a scenario file safe to print.
// Format characters (category Cf, such as the bidi controls) are removed, because they reorder or hide text.
// A control character other than newline and tab, and the line and paragraph separators, show as an escape such as \x1b.
func terminalSafe(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Cf, r):
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == ' ' || r == ' ':
			fmt.Fprintf(&b, `\u%04x`, r)
		case unicode.IsControl(r):
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// oneLine shows a line feed or a tab as an escape, so that a value cannot end its line and start
// another that looks like a step.
func oneLine(s string) string {
	return strings.NewReplacer("\n", `\n`, "\t", `\t`).Replace(s)
}
