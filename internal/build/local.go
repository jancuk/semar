package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"net/netip"
	"net/url"
	"strings"

	"github.com/jancuk/semar/internal/config"
)

// IsLocal reports whether a base URL is on this machine: localhost, an address in 127.0.0.0/8, or ::1.
func IsLocal(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// Repo is the repository that a local service runs from.
type Repo interface {
	Head(ctx context.Context) (string, error)
	DiffHead(ctx context.Context) ([]byte, error)
}

// LocalCommit names what a service that runs from the working copy runs: the head commit, and, when
// tracked files differ from it, a plus sign and 8 hexadecimal characters of the SHA-256 of that
// difference. A result of an earlier state of the files then differs from the current build.
func LocalCommit(ctx context.Context, r Repo) (string, error) {
	head, err := r.Head(ctx)
	if err != nil {
		return "", fmt.Errorf("the head commit of the repository could not be read: %w", err)
	}
	diff, err := r.DiffHead(ctx)
	if err != nil {
		return "", fmt.Errorf("the changes to the tracked files could not be read: %w", err)
	}
	if len(diff) == 0 {
		return head, nil
	}
	sum := sha256.Sum256(diff)
	return head + "+" + hex.EncodeToString(sum[:4]), nil
}

// WithLocal adds, for each service that has no build probe and no declared commit and whose base URL
// is on this machine, the build of the working copy. It returns the commits, and the services whose
// commit it added. A nil repo, as when the directory is not a git work tree, adds nothing.
func WithLocal(ctx context.Context, declared map[string]string, services []config.Service, repo Repo) (commits map[string]string, local []string, err error) {
	commits = maps.Clone(declared)
	if commits == nil {
		commits = map[string]string{}
	}
	var wanted []string
	for _, s := range services {
		if _, named := commits[s.Name]; !named && s.Build == nil && IsLocal(s.BaseURL) {
			wanted = append(wanted, s.Name)
		}
	}
	if len(wanted) == 0 || repo == nil {
		return commits, nil, nil
	}
	commit, err := LocalCommit(ctx, repo)
	if err != nil {
		return nil, nil, err
	}
	for _, name := range wanted {
		commits[name] = commit
	}
	return commits, wanted, nil
}
