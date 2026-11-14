// Package idcheck decides whether an ID is safe to use as one path segment.
package idcheck

import (
	"fmt"
	"regexp"
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
