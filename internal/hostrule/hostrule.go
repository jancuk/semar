// Package hostrule decides whether a URL points at a host the configuration lists.
package hostrule

import (
	"fmt"
	"net/url"
	"strings"
)

// Allowed reports whether u is an http or https URL whose real host is in hosts.
// A host entry without a port allows only the default port of u's scheme, and an
// entry with a port allows only that port. User info in u is ignored: the host
// is the one a client connects to.
func Allowed(u *url.URL, hosts []string) bool {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := normalize(u.Hostname())
	if host == "" {
		return false
	}
	port := u.Port()
	if port == "" {
		port = defaultPort(u.Scheme)
	}
	for _, entry := range hosts {
		eh, ep := splitEntry(entry)
		if eh != "" && eh == host && (ep == "" && port == defaultPort(u.Scheme) || ep == port) {
			return true
		}
	}
	return false
}

// Entry is the hosts entry that would allow u: its host, and its port when that is not the default
// of its scheme. It gives "" for a URL that is not http or https, and for a host that Allowed
// would never match.
func Entry(u *url.URL) string {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	host := normalize(u.Hostname())
	if host == "" {
		return ""
	}
	port := u.Port()
	if port == "" || port == defaultPort(u.Scheme) {
		return host
	}
	if strings.Contains(host, ":") {
		return "[" + host + "]:" + port
	}
	return host + ":" + port
}

// normalize lower-cases ASCII letters only and removes one trailing dot. A host
// with a byte above 0x7F gives "", which matches nothing: Unicode folding turns
// U+0130 and U+212A into i and k, which would admit a host the list does not name.
func normalize(host string) string {
	b := make([]byte, len(host))
	for i := 0; i < len(host); i++ {
		c := host[i]
		if c > 0x7f {
			return ""
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return strings.TrimSuffix(string(b), ".")
}

// CheckEntry reports a hosts entry that cannot be matched as written: one with a
// colon and an empty port, such as "api.uat.example.com:".
func CheckEntry(entry string) error {
	entry = strings.TrimSpace(entry)
	if strings.HasPrefix(entry, "[") {
		if _, tail, found := strings.Cut(entry, "]"); found && tail == ":" {
			return fmt.Errorf("host entry %q ends with a colon and no port", entry)
		}
		return nil
	}
	if strings.Count(entry, ":") == 1 && strings.HasSuffix(entry, ":") {
		return fmt.Errorf("host entry %q ends with a colon and no port", entry)
	}
	return nil
}

func defaultPort(scheme string) string {
	if scheme == "http" {
		return "80"
	}
	return "443"
}

// splitEntry splits a hosts entry into a normalized host and an optional port.
// A bare IPv6 address has several colons and no port.
func splitEntry(entry string) (host, port string) {
	entry = strings.TrimSpace(entry)
	if rest, ok := strings.CutPrefix(entry, "["); ok {
		h, tail, found := strings.Cut(rest, "]")
		if !found {
			return "", ""
		}
		return normalize(h), strings.TrimPrefix(tail, ":")
	}
	if strings.Count(entry, ":") == 1 {
		h, p, _ := strings.Cut(entry, ":")
		return normalize(h), p
	}
	return normalize(entry), ""
}
