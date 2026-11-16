package export_test

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
)

var update = flag.Bool("update", false, "rewrite golden files")

var t0 = time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)

type actionSpec struct {
	scenario domain.ScenarioID
	step     domain.StepID
	pass     domain.Pass
	ms       int64
}

type runSpec struct {
	id      domain.RunID
	results []domain.ScenarioResult
	actions []actionSpec
}

// writeRun stores a closed run in <repo>/.semar and returns the store.
func writeRun(t *testing.T, repo string, spec runSpec) *evidence.Store {
	t.Helper()
	store, err := evidence.Open(filepath.Join(repo, ".semar"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := store.NewRun(spec.id)
	if err != nil {
		t.Fatal(err)
	}
	for i, a := range spec.actions {
		pass := a.pass
		if pass == "" {
			pass = domain.PassScenario
		}
		act := domain.Action{
			Schema: 1, ID: domain.ActionID("a-" + string(rune('a'+i))), Seq: i + 1, Run: spec.id,
			Surface: domain.SurfaceAPI, Op: "request",
			Use: domain.UseCase{Pass: pass, Scenario: a.scenario, Step: a.step},
			By:  domain.ActorReplay, At: t0, DurationMS: a.ms,
			Response: &domain.HTTPResponse{Status: 200},
		}
		rel, err := r.WriteAction(act)
		if err != nil {
			t.Fatal(err)
		}
		entry := domain.Activity{Seq: i + 1, At: t0, Actor: domain.ActorReplay, Use: act.Use, Surface: domain.SurfaceAPI,
			Verb: domain.VerbRead, Target: "GET /x", Result: "200", Evidence: rel}
		if err := r.AppendActivity(entry); err != nil {
			t.Fatal(err)
		}
	}
	rec := domain.Run{ID: spec.id, Environment: "uat", StartedAt: t0, EndedAt: t0.Add(12 * time.Second), Results: spec.results,
		BuildsStart: []domain.BuildRef{{Service: "orders", Commit: "9f3c2ab"}}, BuildsEnd: []domain.BuildRef{{Service: "orders", Commit: "9f3c2ab"}}}
	if err := r.Close(rec); err != nil {
		t.Fatal(err)
	}
	return store
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

const scenarioYAML = `apiVersion: semar/v1
kind: Scenario
id: SHOP-482-01
title: Refund above the order total is rejected
ticket: SHOP-482
surface: api
steps:
  - id: create-order
    setup: true
    action: Create a paid order
  - id: refund-too-much
    action: Request a refund of 150000
    expect:
      - check: { status: 422 }
        source: ticket
`

func writeScenario(t *testing.T, repo, subdir, id, content string) string {
	t.Helper()
	p := filepath.Join(repo, ".semar", "scenarios", subdir, id+".yaml")
	writeFile(t, p, content)
	return p
}

func result(id domain.ScenarioID, outcome domain.Outcome, reasons ...string) domain.ScenarioResult {
	res := domain.ScenarioResult{Scenario: id, Outcome: outcome, Reasons: []domain.Reason{}, Checks: []domain.CheckResult{}, Actions: []domain.ActionID{}}
	for _, text := range reasons {
		res.Reasons = append(res.Reasons, domain.Reason{Rule: outcome, Text: text})
	}
	return res
}

// rehash rewrites run.json with the hashes of the files as they are now, so a test can
// change a stored file and still have a run that verifies.
func rehash(t *testing.T, store *evidence.Store, id domain.RunID) {
	t.Helper()
	run, _, err := store.LoadRun(id)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(store.Root(), "runs", string(id))
	files := map[string]string{}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == "run.json" {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		files[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	run.Files = files
	if err := atomicfile.WriteJSON(filepath.Join(dir, "run.json"), run); err != nil {
		t.Fatal(err)
	}
}
