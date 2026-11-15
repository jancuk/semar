// Package bitbucket reads the compare link of Bitbucket Cloud.
package bitbucket

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/jancuk/semar/internal/codehost"
)

const host = "bitbucket.org"

// Parse reads https://bitbucket.org/<workspace>/<repo>/branches/compare/<head>%0D<base>.
func Parse(u *url.URL) (codehost.Compare, bool, error) {
	if !strings.EqualFold(u.Hostname(), host) {
		return codehost.Compare{}, false, nil
	}
	segs := strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 5)
	if len(segs) >= 3 && segs[2] == "pull-requests" {
		return codehost.Compare{}, true, codehost.ErrPullRequest
	}
	if len(segs) != 5 || segs[0] == "" || segs[1] == "" || segs[2] != "branches" || segs[3] != "compare" {
		return codehost.Compare{}, true, fmt.Errorf("%s link is not a compare link: expected /<workspace>/<repo>/branches/compare/<head>%%0D<base>", host)
	}
	// The decoded spec holds the head's slashes, so only the separator tells the sides apart.
	spec := segs[4]
	var head, base string
	if h, b, ok := strings.Cut(spec, "\r"); ok {
		head, base = h, b
		if err := checkBoth(head, base); err != nil {
			return codehost.Compare{}, true, err
		}
	} else {
		var err error
		head, base, err = codehost.ParseRange(spec)
		if err != nil {
			return codehost.Compare{}, true, fmt.Errorf("%s compare link: %w", host, err)
		}
	}
	return codehost.Compare{Host: host, Repo: segs[0] + "/" + segs[1], Base: base, Head: head}, true, nil
}

func checkBoth(head, base string) error {
	for _, side := range []string{head, base} {
		if err := codehost.CheckName(side); err != nil {
			return fmt.Errorf("%s compare link: %w", host, err)
		}
	}
	return nil
}
