// Package codehost turns a compare link of a code host, or a plain range, into
// the two branches it names.
package codehost

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Compare names the two sides of a comparison.
type Compare struct {
	Host string // "" for a plain range
	Repo string // the path of the repository on the host, for example "acme/orders". "" for a plain range
	Base string // what is deployed to production
	Head string // what is deployed to the environment under test
}

// LinkParser reads a link of one host. It returns false when the link is not
// one of its host.
type LinkParser func(u *url.URL) (Compare, bool, error)

// ErrPullRequest is returned for a link to one pull request or merge request.
var ErrPullRequest = errors.New("this is a link to one pull request; pass the compare link of the two branches, or a range such as base..head")

const acceptedForms = "a range such as base..head or base...head, or an https compare link of Bitbucket Cloud, GitHub, or GitLab"

// ParseCompare reads a plain range, or a link that one of the parsers accepts.
func ParseCompare(arg string, parsers ...LinkParser) (Compare, error) {
	arg = strings.TrimSpace(arg)
	if !strings.Contains(arg, "://") {
		base, head, err := ParseRange(arg)
		if err != nil {
			return Compare{}, fmt.Errorf("cannot read %q: %w; accepted: %s", arg, err, acceptedForms)
		}
		return Compare{Base: base, Head: head}, nil
	}
	u, err := url.Parse(arg)
	if err != nil {
		return Compare{}, fmt.Errorf("cannot read the link: %w", err)
	}
	if u.Scheme != "https" {
		return Compare{}, fmt.Errorf("the link must use https; accepted: %s", acceptedForms)
	}
	if u.Hostname() == "" {
		return Compare{}, fmt.Errorf("the link has no host; accepted: %s", acceptedForms)
	}
	for _, p := range parsers {
		c, ok, err := p(u)
		if !ok {
			continue
		}
		if err != nil {
			return Compare{}, err
		}
		return c, nil
	}
	return Compare{}, fmt.Errorf("no code host recognises %q; accepted: %s", u.Host, acceptedForms)
}

// ParseRange splits "base..head" or "base...head" at its one separator. Both
// sides must be non-empty and valid as a name that git cannot take for an option.
func ParseRange(s string) (base, head string, err error) {
	sep := ".."
	if strings.Contains(s, "...") {
		sep = "..."
	}
	parts := strings.Split(s, sep)
	if len(parts) != 2 {
		return "", "", errors.New("both branches are required, with exactly one .. or ... between them")
	}
	for _, side := range parts {
		if err := checkSide(side); err != nil {
			return "", "", err
		}
	}
	return parts[0], parts[1], nil
}

// CheckName refuses an empty name, a name that starts with a dash, and a name
// with a control character, a range separator, or a colon.
func CheckName(s string) error { return checkSide(s) }

func checkSide(s string) error {
	switch {
	case s == "":
		return errors.New("both branches are required")
	case s[0] == '-':
		return fmt.Errorf("name %q starts with a dash", s)
	case s[0] == '.' || s[len(s)-1] == '.':
		return fmt.Errorf("name %q starts or ends with a dot", s)
	case strings.Contains(s, ".."):
		return fmt.Errorf("name %q holds a range separator", s)
	case strings.Contains(s, ":"):
		return fmt.Errorf("name %q holds a colon; a comparison across forks is not supported", s)
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return fmt.Errorf("name %q holds a control character", s)
		}
	}
	return nil
}

// SameRepo reports whether the remote URL names the repository of c. A plain
// range names none, so it matches every remote.
func SameRepo(c Compare, remoteURL string) bool {
	if c.Host == "" {
		return true
	}
	host, repo, ok := splitRemote(remoteURL)
	if !ok {
		return false
	}
	return strings.EqualFold(host, c.Host) && strings.EqualFold(repo, trimRepo(c.Repo))
}

func splitRemote(raw string) (host, repo string, ok bool) {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return "", "", false
		}
		return u.Hostname(), trimRepo(u.Path), true
	}
	// scp-like: [user@]host:path
	rest := raw
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	h, p, found := strings.Cut(rest, ":")
	if !found || h == "" {
		return "", "", false
	}
	return h, trimRepo(p), true
}

func trimRepo(p string) string {
	p = strings.Trim(p, "/")
	p = strings.TrimSuffix(p, ".git")
	return strings.Trim(p, "/")
}
