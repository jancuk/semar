package changeset_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/changeset"
	"github.com/jancuk/semar/internal/vcs"
	"github.com/jancuk/semar/internal/vcs/gittest"
)

// shop has main with one commit and a branch feature/refund-limit that adds two commits, one of
// them with no ticket.
func shop(t *testing.T) (*gittest.Repo, *vcs.Git) {
	t.Helper()
	r := gittest.New(t)
	r.Commit("base", map[string]*string{"README.md": gittest.S("shop\n")})
	r.Git("checkout", "--quiet", "-b", "feature/refund-limit")
	r.Commit("SHOP-482 limit a refund to the paid amount", map[string]*string{"refund.go": gittest.S("package shop\n")})
	r.Commit("tidy the logger", map[string]*string{"log.go": gittest.S("package shop\n")})
	g, err := vcs.Open(r.Dir)
	if err != nil {
		t.Fatal(err)
	}
	return r, g
}

func TestLocalReadsTheBranchAgainstTheBaseRule(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(r *gittest.Repo)
		opts     changeset.LocalOptions
		wantBase string
	}{
		{name: "main when nothing else is set", wantBase: "main"},
		{name: "master when there is no main", setup: func(r *gittest.Repo) { r.Git("branch", "-m", "main", "master") }, wantBase: "master"},
		{name: "the configured base wins", setup: func(r *gittest.Repo) { r.Git("branch", "release", "main") }, opts: changeset.LocalOptions{ConfigBase: "release"}, wantBase: "release"},
		{name: "the flag wins over the configuration", setup: func(r *gittest.Repo) { r.Git("branch", "release", "main") }, opts: changeset.LocalOptions{ConfigBase: "nowhere", FlagBase: "release"}, wantBase: "release"},
		{
			name: "the default branch of origin comes before main",
			setup: func(r *gittest.Repo) {
				r.Git("branch", "trunk", "main")
				bare := r.Bare()
				r.Git("remote", "add", "origin", bare)
				r.Git("fetch", "--quiet", "origin")
				r.Git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
			},
			wantBase: "trunk",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, g := shop(t)
			if c.setup != nil {
				c.setup(r)
			}
			cs, err := changeset.Local(context.Background(), g, c.opts)
			if err != nil {
				t.Fatal(err)
			}
			if cs.Base != c.wantBase || cs.Head != "feature/refund-limit" {
				t.Errorf("base %q head %q, want %q and feature/refund-limit", cs.Base, cs.Head, c.wantBase)
			}
			if len(cs.Tickets) != 1 || cs.Tickets[0].Key != "SHOP-482" {
				t.Errorf("tickets %+v, want SHOP-482", cs.Tickets)
			}
			if len(cs.NoTicket) != 1 {
				t.Errorf("%d commits with no ticket, want 1", len(cs.NoTicket))
			}
		})
	}
}

func TestLocalNeverFetches(t *testing.T) {
	r, g := shop(t)
	bare := r.Bare()
	r.Git("remote", "add", "origin", bare)
	r.Git("fetch", "--quiet", "origin")
	before := r.Git("rev-parse", "refs/remotes/origin/main")

	other := t.TempDir() + "/other"
	gittest.Run(t, t.TempDir(), nil, "clone", "--quiet", bare, other)
	gittest.Run(t, other, nil, "commit", "--quiet", "--allow-empty", "-m", "SHOP-901 later")
	gittest.Run(t, other, nil, "push", "--quiet", "origin", "HEAD:main")

	if _, err := changeset.Local(context.Background(), g, changeset.LocalOptions{}); err != nil {
		t.Fatal(err)
	}
	if after := r.Git("rev-parse", "refs/remotes/origin/main"); after != before {
		t.Errorf("origin/main moved from %s to %s: the read fetched", before, after)
	}
}

func TestLocalNamesTheProblem(t *testing.T) {
	t.Run("no base branch", func(t *testing.T) {
		r, g := shop(t)
		r.Git("branch", "-m", "main", "develop")
		_, err := changeset.Local(context.Background(), g, changeset.LocalOptions{})
		if err == nil || !strings.Contains(err.Error(), "change.base") || !strings.Contains(err.Error(), "--base") {
			t.Fatalf("error %v, want one that names change.base and --base", err)
		}
	})
	t.Run("the named base does not exist", func(t *testing.T) {
		_, g := shop(t)
		_, err := changeset.Local(context.Background(), g, changeset.LocalOptions{FlagBase: "nowhere"})
		if err == nil || !strings.Contains(err.Error(), "nowhere") {
			t.Fatalf("error %v, want one that names nowhere", err)
		}
	})
	t.Run("a detached head", func(t *testing.T) {
		r, g := shop(t)
		r.Git("checkout", "--quiet", "--detach")
		cs, err := changeset.Local(context.Background(), g, changeset.LocalOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if cs.Head != "HEAD" || len(cs.Tickets) != 1 {
			t.Errorf("head %q tickets %+v, want HEAD and one ticket", cs.Head, cs.Tickets)
		}
	})
}

func TestLocalOnTheBaseBranchItselfIsEmpty(t *testing.T) {
	r, g := shop(t)
	r.Git("checkout", "--quiet", "main")
	cs, err := changeset.Local(context.Background(), g, changeset.LocalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs.Commits) != 0 || len(cs.NoTicket) != 0 {
		t.Errorf("commits %d no-ticket %d, want none on the base branch", len(cs.Commits), len(cs.NoTicket))
	}
}
