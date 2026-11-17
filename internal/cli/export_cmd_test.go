package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
)

// exportRepo is a repository with one stored run of one failed scenario and its scenario file.
func exportRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	scenarioDir := filepath.Join(repo, ".semar", "scenarios", "SHOP-482")
	if err := os.MkdirAll(scenarioDir, 0o700); err != nil {
		t.Fatal(err)
	}
	file := "Feature: Refunds\n\n  @id:SHOP-482-01 @ticket:SHOP-482 @surface:api\n  Scenario: Refund above the order total is rejected\n    When Request a refund of 150000\n    # step: refund-too-much\n"
	if err := os.WriteFile(filepath.Join(scenarioDir, "SHOP-482-01.feature"), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := evidence.Open(filepath.Join(repo, ".semar"))
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.NewRun("r-1")
	if err != nil {
		t.Fatal(err)
	}
	res := domain.ScenarioResult{
		Scenario: "SHOP-482-01", Outcome: domain.OutcomeFailed,
		Reasons: []domain.Reason{{Rule: domain.OutcomeFailed, Step: "refund-too-much", Text: "step refund-too-much: expected status 422, observed status 200 (source ticket)"}},
		Checks: []domain.CheckResult{{Step: "refund-too-much", Index: 0, Source: "ticket", State: domain.CheckFalse,
			Expected: "status 422", Observed: "status 200", Confirmed: true}},
		Actions: []domain.ActionID{},
	}
	start := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	if err := run.Close(domain.Run{ID: "r-1", Environment: "uat", StartedAt: start, EndedAt: start.Add(time.Second), Results: []domain.ScenarioResult{res}}); err != nil {
		t.Fatal(err)
	}
	return repo
}

func runExport(t *testing.T, repo string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errw bytes.Buffer
	code = cli.Run(t.Context(), append([]string{"export"}, append(args, "--dir", repo)...), &out, &errw)
	return code, out.String(), errw.String()
}

func TestExportUnknownFormat(t *testing.T) {
	repo := exportRepo(t)
	code, out, errw := runExport(t, repo, "csv", "r-1")
	if code != cli.ExitError {
		t.Errorf("exit %d, want %d", code, cli.ExitError)
	}
	for _, want := range []string{`"csv"`, "ctrf", "junit", "sarif"} {
		if !strings.Contains(errw, want) {
			t.Errorf("stderr lacks %q: %s", want, errw)
		}
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing", out)
	}
}

func TestExportToStdout(t *testing.T) {
	repo := exportRepo(t)
	for _, format := range []string{"ctrf", "junit", "sarif"} {
		t.Run(format, func(t *testing.T) {
			code, out, errw := runExport(t, repo, format, "r-1")
			if code != cli.ExitOK {
				t.Fatalf("exit %d: %s", code, errw)
			}
			if errw != "" {
				t.Errorf("stderr = %q, want nothing when the report goes to stdout", errw)
			}
			switch format {
			case "junit":
				if !strings.HasPrefix(out, "<?xml") || !strings.Contains(out, `<failure message="step refund-too-much`) {
					t.Errorf("stdout = %s", out)
				}
			case "ctrf":
				if !json.Valid([]byte(out)) || !strings.Contains(out, `"status": "failed"`) || !strings.Contains(out, "SHOP-482-01 Refund above the order total is rejected") {
					t.Errorf("stdout = %s", out)
				}
			case "sarif":
				if !json.Valid([]byte(out)) || !strings.Contains(out, `"startLine": 5`) || !strings.Contains(out, ".semar/scenarios/SHOP-482/SHOP-482-01.feature") {
					t.Errorf("stdout = %s", out)
				}
			}
		})
	}
}

func TestExportOutRefusesExisting(t *testing.T) {
	repo := exportRepo(t)
	out := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(out, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, errw := runExport(t, repo, "ctrf", "r-1", "--out", out)
	if code != cli.ExitError {
		t.Errorf("exit %d, want %d", code, cli.ExitError)
	}
	if !strings.Contains(errw, "--force") || !strings.Contains(errw, out) {
		t.Errorf("stderr = %q, want the file and --force named", errw)
	}
	if got, _ := os.ReadFile(out); string(got) != "keep me" {
		t.Errorf("the existing file now holds %q", got)
	}
	if stdout != "" {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestExportOutWritesANewFile(t *testing.T) {
	repo := exportRepo(t)
	out := filepath.Join(t.TempDir(), "reports", "report.xml")
	code, stdout, errw := runExport(t, repo, "junit", "r-1", "--out", out)
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, errw)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing when --out is set", stdout)
	}
	if want := "semar: wrote junit for run r-1 to " + out + "\n"; errw != want {
		t.Errorf("stderr = %q, want %q", errw, want)
	}
	data, err := os.ReadFile(out)
	if err != nil || !strings.HasPrefix(string(data), "<?xml") {
		t.Fatalf("file = %q, %v", data, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(out)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o644 {
			t.Errorf("mode = %v, want 0644", info.Mode().Perm())
		}
	}
	entries, err := os.ReadDir(filepath.Dir(out))
	if err != nil || len(entries) != 1 {
		t.Errorf("the directory holds %v, want only the report (no temporary file): %v", entries, err)
	}
}

func TestExportOutForce(t *testing.T) {
	repo := exportRepo(t)
	out := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(out, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errw := runExport(t, repo, "sarif", "r-1", "--out", out, "--force")
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, errw)
	}
	data, err := os.ReadFile(out)
	if err != nil || !json.Valid(data) {
		t.Errorf("file = %q, %v", data, err)
	}
	if !strings.Contains(errw, "wrote sarif for run r-1 to "+out) {
		t.Errorf("stderr = %q", errw)
	}
}

func TestExportExitCodes(t *testing.T) {
	repo := exportRepo(t)
	if code, _, errw := runExport(t, repo, "ctrf", "r-1"); code != cli.ExitOK {
		t.Errorf("a failed run that was written: exit %d, want 0: %s", code, errw)
	}

	store, err := evidence.Open(filepath.Join(repo, ".semar"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewRun("r-2"); err != nil {
		t.Fatal(err)
	}
	code, out, errw := runExport(t, repo, "ctrf", "r-2")
	if code != cli.ExitError {
		t.Errorf("an interrupted run: exit %d, want 3", code)
	}
	if out != "" || !strings.Contains(errw, "run r-2 is interrupted") {
		t.Errorf("stdout %q, stderr %q", out, errw)
	}

	if code, _, errw := runExport(t, repo, "ctrf", "r-missing"); code != cli.ExitError || !strings.Contains(errw, "r-missing") {
		t.Errorf("an unknown run: exit %d, stderr %q", code, errw)
	}
	if code, _, _ := runExport(t, repo, "ctrf", "../r-1"); code != cli.ExitError {
		t.Errorf("a run ID that leaves the directory: exit %d", code)
	}
}

func TestExportChangedEvidenceWritesNothing(t *testing.T) {
	repo := exportRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".semar", "runs", "r-1", "extra.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "report.json")
	code, _, errw := runExport(t, repo, "ctrf", "r-1", "--out", out)
	if code != cli.ExitError || !strings.Contains(errw, "extra.txt") {
		t.Errorf("exit %d, stderr %q", code, errw)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a file was written for a run that changed")
	}
}

func TestExportNeedsARepository(t *testing.T) {
	empty := t.TempDir()
	code, _, errw := runExport(t, empty, "ctrf", "r-1")
	if code != cli.ExitError || !strings.Contains(errw, "no Semar repository") {
		t.Errorf("exit %d, stderr %q", code, errw)
	}
	if _, err := os.Stat(filepath.Join(empty, ".semar")); err == nil {
		t.Error("export created .semar")
	}
}

func TestExportNeedsTwoArguments(t *testing.T) {
	repo := exportRepo(t)
	if code, _, _ := runExport(t, repo, "ctrf"); code != cli.ExitError {
		t.Errorf("exit %d, want %d", code, cli.ExitError)
	}
}
