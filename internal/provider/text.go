package provider

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const maxDetailRunes = 300

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(bearer\s+)\S+`),
	regexp.MustCompile(`(?i)((?:api[_-]?key|token|secret|password|authorization)["']?\s*[=:]\s*)\S+`),
	regexp.MustCompile(`\b[A-Za-z0-9_-]{24,}\b`),
}

// Redact removes what looks like a credential from text that a provider or a harness wrote,
// before the text reaches an error, a log, or a file.
func Redact(s string) string {
	for i, re := range secretPatterns {
		if i < 2 {
			s = re.ReplaceAllString(s, "${1}[redacted]")
		} else {
			s = re.ReplaceAllString(s, "[redacted]")
		}
	}
	return s
}

func Truncate(s string) string {
	if r := []rune(s); len(r) > maxDetailRunes {
		return string(r[:maxDetailRunes]) + "..."
	}
	return s
}

// LastLine is the last line of a harness's standard error, redacted and cut, for an error.
func LastLine(stderr []byte) string {
	lines := strings.Split(strings.TrimSpace(string(stderr)), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	if line == "" {
		return "no message on standard error"
	}
	return Truncate(Redact(line))
}

// MaxToolName bounds the tool name of a progress report, so that a hostile stream cannot make
// the reports large.
const MaxToolName = 200

// ProgressTool is a tool name as a harness gave it, cut to MaxToolName bytes at the start of a
// character. The reporter makes it safe to show.
func ProgressTool(name string) string {
	if len(name) <= MaxToolName {
		return name
	}
	cut := MaxToolName
	for cut > 0 && !utf8.RuneStart(name[cut]) {
		cut--
	}
	return name[:cut]
}
