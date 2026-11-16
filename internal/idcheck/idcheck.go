// Package idcheck decides whether an ID is safe to use as one path segment.
package idcheck

import (
	"fmt"
	"regexp"
	"strings"
)

var pattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Check accepts letters, digits, dot, underscore, and hyphen, starting with a letter or digit.
// So an ID is never ".", "..", hidden, or reserved by a leading underscore.
func Check(kind, id string) error {
	if !pattern.MatchString(id) {
		return fmt.Errorf("invalid %s %q: use letters, digits, dot, underscore, or hyphen, starting with a letter or digit", kind, id)
	}
	return nil
}

// Scenario is Check for the ID of a scenario that a person or the plan writes, and it refuses a
// dot as well. The scenario of a journey leg is <journey>.<leg>, so an ID with a dot could be the
// ID of a leg, and a scenario of that name would share the leg's results, labels, and
// confirmations. An ID that is read back from evidence, which may be a leg's, goes through Check.
func Scenario(id string) error {
	if err := Check("scenario ID", id); err != nil {
		return err
	}
	if strings.Contains(id, ".") {
		return fmt.Errorf("invalid scenario ID %q: a dot is kept for the scenario of a journey leg, so use letters, digits, underscore, or hyphen, starting with a letter or digit", id)
	}
	return nil
}
