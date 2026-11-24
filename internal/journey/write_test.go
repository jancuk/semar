package journey

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/domain"
)

func TestMarshalRoundTripsTheExample(t *testing.T) {
	j := example(t)
	data, err := Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(data, "marshaled")
	if err != nil {
		t.Fatalf("Parse(Marshal(j)): %v\n%s", err, data)
	}
	again, err := Marshal(back)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, again) {
		t.Fatalf("Marshal is not stable:\n%s\n---\n%s", data, again)
	}
	if got, want := len(back.Legs), len(j.Legs); got != want {
		t.Fatalf("legs = %d, want %d", got, want)
	}
	if w := back.Legs[2].Wait; w == nil || w.Timeout != 30*time.Second || w.Every != time.Second {
		t.Fatalf("wait did not survive: %+v", w)
	}
	if ps := Validate(back, orderPaid); len(ps) > 0 {
		t.Fatalf("the written journey is not valid: %v", ps)
	}
}

func TestMarshalIsGolden(t *testing.T) {
	status200 := 200
	j := domain.Journey{
		ID: "tiny", Title: "A tiny journey", Owner: "team", Criticality: "standard",
		Legs: []domain.Leg{{
			ID: "one", Surface: domain.SurfaceAPI, Service: "orders",
			Steps: []domain.LegStep{{
				ID: "read", Action: "Read the order",
				Hint:    &domain.RequestHint{Method: "GET", Path: "/v1/orders/{id}"},
				Request: domain.APIRequest{Method: "GET", Path: "/v1/orders/1"},
				Expect:  []domain.Expectation{{Check: domain.Check{Status: &status200}, Source: "baseline", By: "model"}},
			}},
		}},
	}
	got, err := Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"apiVersion: semar/v1\nkind: Journey\nid: tiny\ntitle: A tiny journey\nowner: team\ncriticality: standard\nlegs:\n",
		"by: model",
		"source: baseline",
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("the journey lacks %q:\n%s", want, got)
		}
	}
	if strings.Index(string(got), "apiVersion") > strings.Index(string(got), "legs:") {
		t.Errorf("the keys are out of order:\n%s", got)
	}
}

func TestWriteIsAtomicAndUndoable(t *testing.T) {
	repo := t.TempDir()
	j := example(t)
	sv, err := Write(repo, j, false)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, ".semar", "journeys", "checkout.yaml")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}
	loaded, ps, err := Load(repo)
	if err != nil || len(loaded) != 1 {
		t.Fatalf("Load = %d journeys, %v, %v", len(loaded), ps, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("a temporary file is left: %s", e.Name())
		}
	}
	if err := sv.Undo(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the file is still there after Undo: %v", err)
	}
	if err := sv.Undo(); err == nil {
		t.Fatal("a second Undo must refuse")
	}
}

func TestWriteReplaceRules(t *testing.T) {
	repo := t.TempDir()
	j := example(t)
	if _, err := Write(repo, j, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(repo, j, false); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("a second write without replace = %v, want ErrExist", err)
	}
	path := filepath.Join(repo, ".semar", "journeys", "checkout.yaml")
	before, _ := os.ReadFile(path)
	j.Title = "Changed title"
	sv, err := Write(repo, j, true)
	if err != nil {
		t.Fatal(err)
	}
	if now, _ := os.ReadFile(path); !strings.Contains(string(now), "Changed title") {
		t.Fatal("replace did not write the new journey")
	}
	if err := sv.Undo(); err != nil {
		t.Fatal(err)
	}
	if now, _ := os.ReadFile(path); !bytes.Equal(now, before) {
		t.Fatal("Undo did not put the old journey back")
	}
}

func TestUndoLeavesALaterEdit(t *testing.T) {
	repo := t.TempDir()
	sv, err := Write(repo, example(t), false)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, ".semar", "journeys", "checkout.yaml")
	if err := os.WriteFile(path, []byte("edited by a person\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sv.Undo(); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("Undo = %v, want a refusal that the file changed", err)
	}
	if now, _ := os.ReadFile(path); string(now) != "edited by a person\n" {
		t.Fatal("Undo overwrote a later edit")
	}
}

func TestWriteRefusesAnEscapingID(t *testing.T) {
	repo := t.TempDir()
	for _, id := range []domain.JourneyID{"../evil", "a/b", "..", ".hidden", ""} {
		j := example(t)
		j.ID = id
		if _, err := Write(repo, j, true); err == nil {
			t.Errorf("Write accepted the ID %q", id)
		}
	}
}

func TestWriteRefusesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	outside := t.TempDir()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".semar"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, ".semar", "journeys")); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(repo, example(t), false); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Write through a linked directory = %v", err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) > 0 {
		t.Fatal("a file was written outside the repository")
	}

	repo2 := t.TempDir()
	dir := filepath.Join(repo2, ".semar", "journeys")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(outside, "target.yaml")
	if err := os.WriteFile(target, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "checkout.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(repo2, example(t), true); err == nil {
		t.Fatal("Write replaced a linked file")
	}
	if b, _ := os.ReadFile(target); string(b) != "keep\n" {
		t.Fatal("the target of the link was changed")
	}
}

func TestWriteRefusesASecondFileOfTheJourney(t *testing.T) {
	repo := repoWith(t, map[string]string{"checkout.yml": exampleText(t)})
	if _, err := Write(repo, example(t), true); err == nil || !strings.Contains(err.Error(), "checkout.yml") {
		t.Fatalf("Write beside a .yml = %v", err)
	}
}
