package changeset

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/vcs"
	"github.com/jancuk/semar/internal/vcs/gittest"
)

type fixture struct {
	repo                              *vcs.Git
	main, c1, c2, side, c3, merge, c4 string
	release                           string
}

func build(t *testing.T) fixture {
	t.Helper()
	r := gittest.New(t)
	var fx fixture
	fx.main = r.Commit("base", map[string]*string{"README.md": gittest.S("readme\n")})
	r.Git("checkout", "--quiet", "-b", "release")
	fx.c1 = r.Commit("SHOP-1 add refund", map[string]*string{
		"refund.go":      gittest.S("package refund\n"),
		"refund_test.go": gittest.S("package refund\n"),
	})
	fx.c2 = r.Commit("PAY-7: audit rows\n\nAlso part of SHOP-1.\n", map[string]*string{
		"audit.go":  gittest.S("package refund\n"),
		"refund.go": gittest.S("package refund\n\nvar x = 1\n"),
	})
	r.Git("checkout", "--quiet", "-b", "side")
	fx.side = r.Commit("side tweak", map[string]*string{"side.go": gittest.S("package refund\n")})
	r.Git("checkout", "--quiet", "release")
	fx.c3 = r.Commit("SHOP-4820 rename things", map[string]*string{"rename.go": gittest.S("package refund\n")})
	fx.merge = r.MergeNoFF("side", "Merge side for SHOP-9")
	fx.c4 = r.Commit("tidy notes", map[string]*string{"docs/notes.md": gittest.S("notes\n")})
	fx.release = r.Git("rev-parse", "HEAD")
	r.Git("checkout", "--quiet", "main")
	fx.main = r.Commit("main moves on", map[string]*string{"main_only.txt": gittest.S("x\n")})
	r.Git("checkout", "--quiet", "release")
	g, err := vcs.Open(r.Dir)
	if err != nil {
		t.Fatal(err)
	}
	fx.repo = g
	return fx
}

func TestBuild(t *testing.T) {
	fx := build(t)
	cs, err := Build(context.Background(), fx.repo, Options{Base: "main", Head: "release", BaseCommit: fx.main, HeadCommit: fx.release})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("range", func(t *testing.T) {
		if cs.Base != "main" || cs.Head != "release" || cs.BaseCommit != fx.main || cs.HeadCommit != fx.release {
			t.Fatalf("names: %+v", cs)
		}
		if cs.MergeBase == "" || cs.MergeBase == fx.main {
			t.Fatalf("merge base %q: main moved on, so it must differ from the base commit", cs.MergeBase)
		}
		var ids []string
		for _, c := range cs.Commits {
			ids = append(ids, c.ID)
		}
		if len(ids) != 6 || ids[0] != fx.c1 || ids[1] != fx.c2 || ids[5] != fx.c4 || ids[4] != fx.merge {
			t.Fatalf("commits: %v", ids)
		}
	})

	t.Run("commits", func(t *testing.T) {
		by := map[string]domain.ChangeCommit{}
		for _, c := range cs.Commits {
			by[c.ID] = c
		}
		cases := []struct {
			name    string
			id      string
			merge   bool
			tickets []string
			files   []string
		}{
			{"one key", fx.c1, false, []string{"SHOP-1"}, []string{"refund.go", "refund_test.go"}},
			{"a key in the subject and one in the body", fx.c2, false, []string{"PAY-7", "SHOP-1"}, []string{"audit.go", "refund.go"}},
			{"no key", fx.side, false, nil, []string{"side.go"}},
			{"a key inside a longer key is the longer key", fx.c3, false, []string{"SHOP-4820"}, []string{"rename.go"}},
			{"a merge names a ticket and holds no files", fx.merge, true, []string{"SHOP-9"}, nil},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				got := by[c.id]
				if got.Merge != c.merge || !reflect.DeepEqual(got.Tickets, c.tickets) || !reflect.DeepEqual(got.Files, c.files) {
					t.Fatalf("got merge=%v tickets=%v files=%v", got.Merge, got.Tickets, got.Files)
				}
			})
		}
		if c := by[fx.c2]; c.Subject != "PAY-7: audit rows" || c.Body != "Also part of SHOP-1." || c.Author != "Ada Author" || c.At.IsZero() {
			t.Fatalf("commit fields: %+v", c)
		}
	})

	t.Run("tickets", func(t *testing.T) {
		want := []domain.TicketChange{
			{Key: "PAY-7", Commits: []string{fx.c2}, Files: []string{"audit.go", "refund.go"}},
			{Key: "SHOP-1", Commits: []string{fx.c1, fx.c2}, Files: []string{"audit.go", "refund.go", "refund_test.go"}},
			{Key: "SHOP-4820", Commits: []string{fx.c3}, Files: []string{"rename.go"}},
			{Key: "SHOP-9", Commits: []string{fx.merge}},
		}
		if !reflect.DeepEqual(cs.Tickets, want) {
			t.Fatalf("got %+v\nwant %+v", cs.Tickets, want)
		}
	})

	t.Run("commits with no ticket", func(t *testing.T) {
		if want := []string{fx.side, fx.c4}; !reflect.DeepEqual(cs.NoTicket, want) {
			t.Fatalf("got %v, want %v", cs.NoTicket, want)
		}
	})

	t.Run("files are the net difference from the merge base", func(t *testing.T) {
		type row struct {
			path, status, kind string
			tickets            []string
		}
		var got []row
		for _, f := range cs.Files {
			got = append(got, row{f.Path, f.Status, f.Kind, f.Tickets})
		}
		want := []row{
			{"audit.go", "added", "source", []string{"PAY-7", "SHOP-1"}},
			{"docs/notes.md", "added", "docs", nil},
			{"refund.go", "added", "source", []string{"PAY-7", "SHOP-1"}},
			{"refund_test.go", "added", "test", []string{"SHOP-1"}},
			{"rename.go", "added", "source", []string{"SHOP-4820"}},
			{"side.go", "added", "source", nil},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v\nwant %+v", got, want)
		}
		if cs.Files[2].Added != 3 {
			t.Fatalf("refund.go added %d lines, want 3", cs.Files[2].Added)
		}
	})
}

func TestBuildTicketPattern(t *testing.T) {
	fx := build(t)
	cs, err := Build(context.Background(), fx.repo, Options{
		BaseCommit: fx.main, HeadCommit: fx.release,
		Ticket: regexp.MustCompile(`SHOP-[0-9]{3}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs.Tickets) != 0 {
		t.Fatalf("SHOP-4820 and SHOP-1 must not match SHOP-[0-9]{3}: %+v", cs.Tickets)
	}
	if len(cs.NoTicket) != 5 {
		t.Fatalf("got %d commits with no ticket, want the 5 that are not merges", len(cs.NoTicket))
	}
}

type fakeRepo struct {
	base    string
	baseErr error
	log     []vcs.Commit
	stat    []vcs.FileStat
	gotBase string
}

func (f *fakeRepo) MergeBase(context.Context, string, string) (string, error) {
	return f.base, f.baseErr
}
func (f *fakeRepo) Log(context.Context, string, string) ([]vcs.Commit, error) { return f.log, nil }
func (f *fakeRepo) DiffStat(_ context.Context, base, _ string) ([]vcs.FileStat, error) {
	f.gotBase = base
	return f.stat, nil
}

func TestBuildRenameAndMerge(t *testing.T) {
	repo := &fakeRepo{
		base: "mb",
		log: []vcs.Commit{
			{ID: "c1", Subject: "AB-1 edit old", Parents: []string{"p"}, Files: []string{"old.go"}},
			{ID: "c2", Subject: "AB-2 move", Parents: []string{"c1"}, Files: []string{"new.go", "old.go"}},
			{ID: "m", Subject: "merge AB-3", Parents: []string{"c2", "x"}, Files: []string{"new.go"}},
		},
		stat: []vcs.FileStat{{Path: "new.go", OldPath: "old.go", Status: "renamed", Added: 1}},
	}
	cs, err := Build(context.Background(), repo, Options{BaseCommit: "b", HeadCommit: "h"})
	if err != nil {
		t.Fatal(err)
	}
	if repo.gotBase != "mb" {
		t.Fatalf("diff base %q, want the merge base", repo.gotBase)
	}
	want := []domain.TicketChange{
		{Key: "AB-1", Commits: []string{"c1"}, Files: []string{"new.go"}},
		{Key: "AB-2", Commits: []string{"c2"}, Files: []string{"new.go"}},
		{Key: "AB-3", Commits: []string{"m"}},
	}
	if !reflect.DeepEqual(cs.Tickets, want) {
		t.Fatalf("got %+v", cs.Tickets)
	}
	if got := cs.Files[0].Tickets; !reflect.DeepEqual(got, []string{"AB-1", "AB-2"}) {
		t.Fatalf("file tickets %v: a merge must not add to them", got)
	}
	if len(cs.Commits[2].Files) != 0 {
		t.Fatalf("a merge holds files: %v", cs.Commits[2].Files)
	}
}

func TestBuildErrors(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name string
		repo Repo
		o    Options
		want string
	}{
		{"no commits", &fakeRepo{}, Options{}, "required"},
		{"merge base", &fakeRepo{baseErr: boom}, Options{BaseCommit: "b", HeadCommit: "h"}, "boom"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Build(context.Background(), c.repo, c.o)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
		})
	}
}

func TestFindKeys(t *testing.T) {
	short := regexp.MustCompile(`SHOP-[0-9]{3}`)
	cases := []struct {
		name string
		re   *regexp.Regexp
		text string
		want []string
	}{
		{"one key", defaultTicket, "SHOP-482 fix", []string{"SHOP-482"}},
		{"a key inside a longer one", short, "SHOP-4820 rename", nil},
		{"the longer key by the default pattern", defaultTicket, "SHOP-4820 rename", []string{"SHOP-4820"}},
		{"letters before the key", short, "ASHOP-482", nil},
		{"digits before the key", short, "1SHOP-482", nil},
		{"letters after the key", short, "SHOP-482x", nil},
		{"a dash after the key", short, "SHOP-482-fix", []string{"SHOP-482"}},
		{"punctuation around", defaultTicket, "(SHOP-482), [PAY-7]: done", []string{"PAY-7", "SHOP-482"}},
		{"underscore before", defaultTicket, "x_SHOP-482", []string{"SHOP-482"}},
		{"same key twice", defaultTicket, "SHOP-1 and SHOP-1", []string{"SHOP-1"}},
		{"a rejected match hides no later one", short, "ASHOP-482 SHOP-123", []string{"SHOP-123"}},
		{"lower case is no key", defaultTicket, "shop-482", nil},
		{"key at the end", defaultTicket, "see\nSHOP-482", []string{"SHOP-482"}},
		{"empty", defaultTicket, "", nil},
		{"a pattern that matches the empty string", regexp.MustCompile(`x*`), "abc", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := findKeys(c.re, c.text); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("findKeys(%q) = %v, want %v", c.text, got, c.want)
			}
		})
	}
}

func FuzzFindKeys(f *testing.F) {
	for _, s := range []string{"SHOP-482 fix", "SHOP-4820", "ASHOP-1 B-2", "", "\x00SHOP-1\xff", "AB-1AB-2"} {
		f.Add(s)
	}
	re := regexp.MustCompile(`SHOP-[0-9]{3}|[A-Z][A-Z0-9]+-[0-9]+`)
	f.Fuzz(func(t *testing.T, text string) {
		if !utf8.ValidString(text) {
			return
		}
		keys := findKeys(re, text)
		for i, k := range keys {
			if i > 0 && keys[i-1] >= k {
				t.Fatalf("keys not sorted and distinct: %v", keys)
			}
			if !re.MatchString(k) {
				t.Fatalf("key %q does not match the pattern", k)
			}
			ok := false
			for from := 0; ; {
				at := strings.Index(text[from:], k)
				if at < 0 {
					break
				}
				start, end := from+at, from+at+len(k)
				if (start == 0 || !isAlnum(text[start-1])) && (end == len(text) || !isAlnum(text[end])) {
					ok = true
					break
				}
				from = start + 1
			}
			if !ok {
				t.Fatalf("key %q has no bounded occurrence in %q", k, text)
			}
		}
	})
}
