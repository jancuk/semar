// Package hostrule decides whether a URL points at a host the configuration lists.
package hostrule

import (
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
		if eh == host && (ep == "" && port == defaultPort(u.Scheme) || ep == port) {
			return true
		}
	}
	return false
}

func normalize(host string) string {
	return strings.TrimSuffix(strings.ToLower(host), ".")
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
