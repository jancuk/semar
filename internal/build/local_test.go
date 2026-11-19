package build_test

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/jancuk/semar/internal/build"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
)

func TestIsLocal(t *testing.T) {
	for url, want := range map[string]bool{
		"http://localhost:3000":         true,
		"http://LocalHost/app":          true,
		"http://127.0.0.1:8080":         true,
		"http://127.4.5.6":              true,
		"http://[::1]:8080":             true,
		"http://0.0.0.0:8080":           false,
		"http://10.0.0.5":               false,
		"http://localhost.example.com":  false,
		"https://orders.example.com":    false,
		"http://[2001:db8::1]":          false,
		"not a url":                     false,
		"http://128.0.0.1":              false,
		"http://127.0.0.1.example.com/": false,
	} {
		if got := build.IsLocal(url); got != want {
			t.Errorf("IsLocal(%q) = %v, want %v", url, got, want)
		}
	}
}

type fakeRepo struct {
	head string
	diff string
	err  error
}

func (f fakeRepo) Head(context.Context) (string, error)     { return f.head, f.err }
func (f fakeRepo) DiffHead(context.Context) ([]byte, error) { return []byte(f.diff), f.err }

const sha = "4f2a91c0d34f2a91c0d34f2a91c0d34f2a91c0d3"

func TestLocalCommit(t *testing.T) {
	ctx := context.Background()
	clean, err := build.LocalCommit(ctx, fakeRepo{head: sha})
	if err != nil || clean != sha {
		t.Fatalf("a clean tree = %q, %v, want the head commit", clean, err)
	}
	one, err := build.LocalCommit(ctx, fakeRepo{head: sha, diff: "+one\n"})
	if err != nil {
		t.Fatal(err)
	}
	two, _ := build.LocalCommit(ctx, fakeRepo{head: sha, diff: "+two\n"})
	again, _ := build.LocalCommit(ctx, fakeRepo{head: sha, diff: "+one\n"})
	if !regexp.MustCompile(`^` + sha + `\+[0-9a-f]{8}$`).MatchString(one) {
		t.Errorf("a changed tree = %q, want <commit>+<8 hex>", one)
	}
	if one == two || one != again {
		t.Errorf("one = %q, two = %q, again = %q: the suffix must follow the difference", one, two, again)
	}
	if _, err := build.LocalCommit(ctx, fakeRepo{err: errors.New("no commit")}); err == nil {
		t.Error("an error of the repository was dropped")
	}
}

func TestWithLocalFillsOnlyALocalServiceWithNoProbe(t *testing.T) {
	services := []config.Service{
		{Name: "web", BaseURL: "http://127.0.0.1:3000"},
		{Name: "probed", BaseURL: "http://127.0.0.1:3001", Build: &config.BuildProbe{Probe: "http", URL: "http://127.0.0.1:3001/version", Commit: "$.commit"}},
		{Name: "remote", BaseURL: "https://remote.example.com"},
		{Name: "named", BaseURL: "http://localhost:3002"},
	}
	got, local, err := build.WithLocal(context.Background(), map[string]string{"named": "abcdef0"}, services, fakeRepo{head: sha})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"web": sha, "named": "abcdef0"}
	if len(got) != len(want) || got["web"] != sha || got["named"] != "abcdef0" {
		t.Errorf("commits = %v, want %v", got, want)
	}
	if len(local) != 1 || local[0] != "web" {
		t.Errorf("local = %v, want [web]", local)
	}

	p := build.NewDeclared(got, nil, local...)
	ref, err := p.Probe(context.Background(), "web")
	if err != nil || ref != (domain.BuildRef{Service: "web", Commit: sha, Declared: true, Local: true}) {
		t.Errorf("web = %+v, %v", ref, err)
	}
	ref, _ = p.Probe(context.Background(), "named")
	if ref.Local {
		t.Errorf("a build that a person named is local: %+v", ref)
	}
}

func TestWithLocalReadsNothingWhenNoServiceNeedsIt(t *testing.T) {
	services := []config.Service{{Name: "remote", BaseURL: "https://remote.example.com"}}
	got, local, err := build.WithLocal(context.Background(), nil, services, fakeRepo{err: errors.New("must not be read")})
	if err != nil || len(got) != 0 || len(local) != 0 {
		t.Errorf("= %v, %v, %v", got, local, err)
	}
}
