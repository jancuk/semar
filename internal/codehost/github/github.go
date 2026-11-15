// Package github reads the compare link of GitHub.
package github

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/jancuk/semar/internal/codehost"
)

const host = "github.com"

// Parse reads https://github.com/<owner>/<repo>/compare/<base>...<head>.
func Parse(u *url.URL) (codehost.Compare, bool, error) {
	if !strings.EqualFold(u.Hostname(), host) {
		return codehost.Compare{}, false, nil
	}
	segs := strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 4)
	if len(segs) >= 3 && segs[2] == "pull" {
		return codehost.Compare{}, true, codehost.ErrPullRequest
	}
	if len(segs) != 4 || segs[0] == "" || segs[1] == "" || segs[2] != "compare" {
		return codehost.Compare{}, true, fmt.Errorf("%s link is not a compare link: expected /<owner>/<repo>/compare/<base>...<head>", host)
	}
	base, head, err := codehost.ParseRange(segs[3])
	if err != nil {
		return codehost.Compare{}, true, fmt.Errorf("%s compare link: %w", host, err)
	}
	return codehost.Compare{Host: host, Repo: segs[0] + "/" + segs[1], Base: base, Head: head}, true, nil
}
