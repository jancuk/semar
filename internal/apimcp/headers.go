package apimcp

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// allowedHeaders are the headers that a request may carry besides the X- headers. Their names are
	// lower case. Idempotency-Key is allowed on purpose, although it holds "key".
	allowedHeaders = map[string]bool{
		"accept": true, "content-type": true, "if-match": true, "if-none-match": true,
		"idempotency-key": true, "x-request-id": true,
	}
	xHeader = regexp.MustCompile(`^X-[A-Za-z0-9-]{1,64}$`)
	// credentialWords make a header name look like a credential.
	credentialWords = []string{"auth", "token", "key", "secret", "cookie", "session"}
)

// CheckHeaders says why the headers of a request cannot be sent, or returns nil. Authentication goes
// through `as` and never through a header.
func CheckHeaders(h map[string]string) error {
	for name, value := range h {
		lower := strings.ToLower(name)
		if !allowedHeaders[lower] {
			for _, w := range credentialWords {
				if strings.Contains(lower, w) {
					return fmt.Errorf("header %q looks like a credential: authentication goes through the as parameter, which names an auth profile of semar.yaml", name)
				}
			}
			if !xHeader.MatchString(name) {
				return fmt.Errorf("header %q is not allowed: use Accept, Content-Type, If-Match, If-None-Match, Idempotency-Key, X-Request-Id, or another X- header", name)
			}
		}
		if strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 && r != '\t' || r == 0x7f }) {
			return fmt.Errorf("header %q holds a control character", name)
		}
	}
	return nil
}
