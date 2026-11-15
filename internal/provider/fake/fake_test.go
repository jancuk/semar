package fake_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/fake"
)

func write(t *testing.T, root, name, text string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFakeReturnsOutputsInOrderThenFails(t *testing.T) {
	root := t.TempDir()
	write(t, root, "fx/a.json", `{"n": 1}`)
	write(t, root, "fx/b.json", "{\"n\": 2}\n")
	f, err := fake.New(config.Provider{ID: "f", Kind: "fake", Outputs: []string{"fx/a.json", "fx/b.json"}}, root)
	if err != nil {
		t.Fatal(err)
	}
	if f.Kind() != "fake" || f.ID() != "f" {
		t.Fatal("identity")
	}
	for i, want := range []string{`{"n": 1}`, `{"n": 2}`} {
		res, err := f.Run(context.Background(), provider.Job{Prompt: "p" + string(rune('0'+i))})
		if err != nil || string(res.Output) != want || res.Model != "fake" || res.Turns != 1 {
			t.Fatalf("call %d: %+v %v", i, res, err)
		}
	}
	_, err = f.Run(context.Background(), provider.Job{Prompt: "p2"})
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Class != provider.ProviderError || pe.Detail != "the fake provider has no output left" {
		t.Fatalf("got %v", err)
	}
	jobs := f.Jobs()
	if len(jobs) != 3 || jobs[0].Prompt != "p0" || jobs[2].Prompt != "p2" {
		t.Fatalf("jobs = %+v", jobs)
	}
}

func TestFakeRefusals(t *testing.T) {
	root := t.TempDir()
	write(t, root, "arr.json", `[1]`)
	write(t, root, "null.json", `null`)
	write(t, root, "bad.json", `{`)
	t.Run("a path outside the repository", func(t *testing.T) {
		if _, err := fake.New(config.Provider{ID: "f", Outputs: []string{"../x.json"}}, root); err == nil {
			t.Fatal("want an error")
		}
	})
	for _, name := range []string{"arr.json", "null.json", "bad.json", "missing.json"} {
		t.Run(name, func(t *testing.T) {
			f, err := fake.New(config.Provider{ID: "f", Outputs: []string{name}}, root)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.Run(context.Background(), provider.Job{})
			var pe *provider.Error
			if !errors.As(err, &pe) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestFakeConcurrentUse(t *testing.T) {
	root := t.TempDir()
	var names []string
	for i := range 8 {
		n := "o" + string(rune('a'+i)) + ".json"
		write(t, root, n, `{}`)
		names = append(names, n)
	}
	f, _ := fake.New(config.Provider{ID: "f", Outputs: names}, root)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := f.Run(context.Background(), provider.Job{}); err != nil {
				t.Error(err)
			}
			f.Jobs()
		})
	}
	wg.Wait()
	if len(f.Jobs()) != 8 {
		t.Fatal("jobs")
	}
}

func TestFactory(t *testing.T) {
	in, err := fake.Factory(t.TempDir())(config.Provider{ID: "f", Kind: "fake"})
	if err != nil || in.Kind() != "fake" {
		t.Fatal(err)
	}
	r := provider.NewRegistry()
	r.Register("fake", fake.Factory(t.TempDir()))
	if _, err := r.New(config.Provider{ID: "x", Kind: "nope"}); err == nil || !strings.Contains(err.Error(), "fake") {
		t.Fatal(err)
	}
}
