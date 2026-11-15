package codehost_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/codehost"
	"github.com/jancuk/semar/internal/codehost/bitbucket"
	"github.com/jancuk/semar/internal/codehost/github"
	"github.com/jancuk/semar/internal/codehost/gitlab"
)

func parsers() []codehost.LinkParser {
	return []codehost.LinkParser{bitbucket.Parse, github.Parse, gitlab.Parse}
}

func TestParseCompare(t *testing.T) {
	cases := []struct {
		name string
		arg  string
		want codehost.Compare
	}{
		{"range two dots", "master..release/week-02", codehost.Compare{Base: "master", Head: "release/week-02"}},
		{"range three dots", "master...release/week-02", codehost.Compare{Base: "master", Head: "release/week-02"}},
		{"range with surrounding space", " main..dev ", codehost.Compare{Base: "main", Head: "dev"}},
		{"range of tags with dots", "v1.2.0..v1.3.0", codehost.Compare{Base: "v1.2.0", Head: "v1.3.0"}},
		{"bitbucket encoded slash and carriage return",
			"https://bitbucket.org/acme/orders/branches/compare/release%2Fweek-02%0Dmaster",
			codehost.Compare{Host: "bitbucket.org", Repo: "acme/orders", Base: "master", Head: "release/week-02"}},
		{"bitbucket lower case escape",
			"https://bitbucket.org/acme/orders/branches/compare/release%2Fweek-02%0dmaster",
			codehost.Compare{Host: "bitbucket.org", Repo: "acme/orders", Base: "master", Head: "release/week-02"}},
		{"bitbucket slashes on both sides",
			"https://bitbucket.org/acme/orders/branches/compare/feature%2Fa%2Fb%0Drelease%2Fx?w=1#diff",
			codehost.Compare{Host: "bitbucket.org", Repo: "acme/orders", Base: "release/x", Head: "feature/a/b"}},
		{"bitbucket dots instead of the carriage return",
			"https://bitbucket.org/acme/orders/branches/compare/release%2Fweek-02..master",
			codehost.Compare{Host: "bitbucket.org", Repo: "acme/orders", Base: "master", Head: "release/week-02"}},
		{"github three dots",
			"https://github.com/acme/orders/compare/master...release/week-02",
			codehost.Compare{Host: "github.com", Repo: "acme/orders", Base: "master", Head: "release/week-02"}},
		{"github encoded slash and two dots",
			"https://github.com/acme/orders/compare/master..release%2Fweek-02",
			codehost.Compare{Host: "github.com", Repo: "acme/orders", Base: "master", Head: "release/week-02"}},
		{"gitlab encoded slash",
			"https://gitlab.example.com/group/sub/orders/-/compare/master...release%2Fweek-02",
			codehost.Compare{Host: "gitlab.example.com", Repo: "group/sub/orders", Base: "master", Head: "release/week-02"}},
		{"gitlab with a query",
			"https://gitlab.com/group/orders/-/compare/main...dev?from_project_id=7&straight=false",
			codehost.Compare{Host: "gitlab.com", Repo: "group/orders", Base: "main", Head: "dev"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := codehost.ParseCompare(c.arg, parsers()...)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestParseCompareRefusals(t *testing.T) {
	cases := []struct {
		name    string
		arg     string
		wantErr string
	}{
		{"bitbucket pull request", "https://bitbucket.org/acme/orders/pull-requests/42", codehost.ErrPullRequest.Error()},
		{"bitbucket pull request diff", "https://bitbucket.org/acme/orders/pull-requests/42/diff", codehost.ErrPullRequest.Error()},
		{"github pull request", "https://github.com/acme/orders/pull/42", codehost.ErrPullRequest.Error()},
		{"github pull request files", "https://github.com/acme/orders/pull/42/files", codehost.ErrPullRequest.Error()},
		{"gitlab merge request", "https://gitlab.example.com/group/orders/-/merge_requests/42", codehost.ErrPullRequest.Error()},
		{"bitbucket one branch", "https://bitbucket.org/acme/orders/branches/compare/release%2Fweek-02", "both branches"},
		{"bitbucket empty base", "https://bitbucket.org/acme/orders/branches/compare/release%0D", "both branches"},
		{"bitbucket not a compare link", "https://bitbucket.org/acme/orders/src/master", "not a compare link"},
		{"github one branch", "https://github.com/acme/orders/compare/main", "both branches"},
		{"github fork comparison", "https://github.com/acme/orders/compare/main...other:feature", "across forks"},
		{"github not a compare link", "https://github.com/acme/orders", "not a compare link"},
		{"gitlab one branch", "https://gitlab.com/g/o/-/compare/main", "both branches"},
		{"unknown host", "https://example.com/a/b/compare/x...y", "accepted:"},
		{"http link", "http://github.com/acme/orders/compare/a...b", "https"},
		{"ssh link", "ssh://git@github.com/acme/orders", "accepted:"},
		{"plain word", "master", "accepted:"},
		{"no host", "https:///a/-/compare/x...y", "no host"},
		{"empty", "", "accepted:"},
		{"empty head", "master..", "both branches"},
		{"empty base", "..head", "both branches"},
		{"two separators", "a..b..c", "both branches"},
		{"separator run", "a....b", "dot"},
		{"option as base", "--upload-pack=x..head", "starts with a dash"},
		{"option as head", "main..--output=x", "starts with a dash"},
		{"control character", "main..he\nad", "control character"},
		{"bitbucket option as head", "https://bitbucket.org/acme/orders/branches/compare/%2D%2Dx%0Dmaster", "starts with a dash"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := codehost.ParseCompare(c.arg, parsers()...)
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("got %+v, %v, want an error with %q", got, err, c.wantErr)
			}
		})
	}
}

func TestPullRequestMessage(t *testing.T) {
	const want = "this is a link to one pull request; pass the compare link of the two branches, or a range such as base..head"
	if codehost.ErrPullRequest.Error() != want {
		t.Fatalf("message is %q", codehost.ErrPullRequest)
	}
}

func TestSameRepo(t *testing.T) {
	c := codehost.Compare{Host: "bitbucket.org", Repo: "acme/orders", Base: "a", Head: "b"}
	cases := []struct {
		name   string
		c      codehost.Compare
		remote string
		want   bool
	}{
		{"scp-like with .git", c, "git@bitbucket.org:acme/orders.git", true},
		{"scp-like without .git", c, "git@bitbucket.org:acme/orders", true},
		{"ssh url with .git", c, "ssh://git@bitbucket.org/acme/orders.git", true},
		{"ssh url with a port", c, "ssh://git@bitbucket.org:7999/acme/orders.git", true},
		{"https with .git", c, "https://bitbucket.org/acme/orders.git", true},
		{"https without .git", c, "https://bitbucket.org/acme/orders", true},
		{"https with credentials", c, "https://user:token@bitbucket.org/acme/orders.git", true},
		{"https with trailing slash", c, "https://bitbucket.org/acme/orders/", true},
		{"case differs", c, "git@BitBucket.org:Acme/Orders.git", true},
		{"other repository", c, "git@bitbucket.org:acme/payments.git", false},
		{"other workspace", c, "https://bitbucket.org/other/orders", false},
		{"other host", c, "https://github.com/acme/orders", false},
		{"prefix of a longer name", c, "https://bitbucket.org/acme/orders-v2.git", false},
		{"nested group", codehost.Compare{Host: "gitlab.example.com", Repo: "g/sub/orders"}, "git@gitlab.example.com:g/sub/orders.git", true},
		{"nested group, shorter remote", codehost.Compare{Host: "gitlab.example.com", Repo: "g/sub/orders"}, "git@gitlab.example.com:sub/orders.git", false},
		{"plain range matches every remote", codehost.Compare{Base: "a", Head: "b"}, "git@anywhere:x/y.git", true},
		{"plain range, no remote", codehost.Compare{Base: "a", Head: "b"}, "", true},
		{"empty remote", c, "", false},
		{"a local path", c, "/srv/git/orders.git", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := codehost.SameRepo(tc.c, tc.remote); got != tc.want {
				t.Fatalf("SameRepo(%+v, %q) = %v, want %v", tc.c, tc.remote, got, tc.want)
			}
		})
	}
}

func FuzzParseCompare(f *testing.F) {
	for _, s := range []string{
		"master..release", "a...b", "https://bitbucket.org/a/b/branches/compare/x%0Dy",
		"https://github.com/a/b/compare/x...y", "https://gitlab.com/a/b/-/compare/x...y",
		"https://github.com/a/b/pull/1", "--x..y", "a..b..c", "", "https://%zz",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, arg string) {
		c, err := codehost.ParseCompare(arg, parsers()...)
		if err != nil {
			return
		}
		for _, side := range []string{c.Base, c.Head} {
			if codehost.CheckName(side) != nil {
				t.Fatalf("accepted %q with the side %q", arg, side)
			}
		}
		if (c.Host == "") != (c.Repo == "") {
			t.Fatalf("host and repo disagree: %+v", c)
		}
	})
}
