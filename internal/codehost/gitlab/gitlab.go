// Package gitlab reads the compare link of GitLab, on gitlab.com or on a host of its own.
package gitlab

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/jancuk/semar/internal/codehost"
)

// Parse reads https://<host>/<group>/<project>/-/compare/<base>...<head>. The
// project path may have any number of groups. GitLab marks its own links with
// "/-/", and that marker is what claims a link of any other host.
func Parse(u *url.URL) (codehost.Compare, bool, error) {
	repo, rest, ok := strings.Cut(strings.TrimPrefix(u.Path, "/"), "/-/")
	if !ok {
		return codehost.Compare{}, false, nil
	}
	if strings.HasPrefix(rest, "merge_requests/") || rest == "merge_requests" {
		return codehost.Compare{}, true, codehost.ErrPullRequest
	}
	spec, isCompare := strings.CutPrefix(rest, "compare/")
	if !isCompare || repo == "" {
		return codehost.Compare{}, false, nil
	}
	base, head, err := codehost.ParseRange(spec)
	if err != nil {
		return codehost.Compare{}, true, fmt.Errorf("gitlab compare link: %w", err)
	}
	return codehost.Compare{Host: strings.ToLower(u.Hostname()), Repo: strings.Trim(repo, "/"), Base: base, Head: head}, true, nil
}
