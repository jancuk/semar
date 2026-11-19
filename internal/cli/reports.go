package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/export"
	"github.com/jancuk/semar/internal/report"
)

const (
	reportHTMLFile = "report.html"
	junitFile      = "junit.xml"
	summaryFile    = "summary.md"
	// stepSummaryVar names the file that a GitHub Actions job shows as the summary of the job.
	stepSummaryVar = "GITHUB_STEP_SUMMARY"
	// publishedFileMode is the mode of a file in --report-dir: a pipeline reads it, and it holds
	// stored evidence, which is redacted.
	publishedFileMode os.FileMode = 0o644
)

// runFiles lets the HTML report read the stored evidence of one run.
type runFiles struct {
	store *evidence.Store
	id    domain.RunID
}

func (f runFiles) Action(rel string) (domain.Action, error) { return f.store.ReadAction(f.id, rel) }

func (f runFiles) File(rel string, limit int64) ([]byte, error) {
	return f.store.ReadFile(f.id, rel, limit)
}

// reportSet is the four files of a run, as written.
type reportSet struct {
	md, html, junit, summary []byte
	data                     report.Data
}

// writeReports renders report.md, report.html, junit.xml and summary.md from one report.Data and stores
// them in the run before it closes. report.md is the one that must be written. A failure of any other
// file leaves it out and says so on standard error, because the run has its outcome without it.
func writeReports(stderr io.Writer, store *evidence.Store, repo string, run *evidence.Run, d report.Data) (reportSet, error) {
	d.Files = runFiles{store: store, id: d.Run.ID}
	d.RunDir = filepath.Join(store.Root(), "runs", string(d.Run.ID))
	set := reportSet{data: d}
	var md bytes.Buffer
	if err := report.Markdown(&md, d); err != nil {
		return set, err
	}
	set.md = md.Bytes()
	if err := run.WriteFile(reportFile, set.md); err != nil {
		return set, err
	}
	skip := func(name string, err error) {
		fmt.Fprintf(stderr, "semar: %s was not written: %s\n", name, oneLine(terminalSafe(err.Error())))
	}
	var page bytes.Buffer
	if err := report.HTML(&page, d); err != nil {
		skip(reportHTMLFile, err)
	} else {
		set.html = page.Bytes()
	}
	if ex, err := export.Build(store, repo, d.Run, d.Activity, Version); err != nil {
		skip(junitFile, err)
	} else {
		var buf bytes.Buffer
		if err := export.JUnit(&buf, ex); err != nil {
			skip(junitFile, err)
		} else {
			set.junit = buf.Bytes()
		}
	}
	var sum bytes.Buffer
	if err := report.PipelineSummary(&sum, d, filepath.Join(d.RunDir, reportHTMLFile)); err != nil {
		skip(summaryFile, err)
	} else {
		set.summary = sum.Bytes()
	}
	for _, f := range []struct {
		name string
		data []byte
	}{{reportHTMLFile, set.html}, {junitFile, set.junit}, {summaryFile, set.summary}} {
		if f.data == nil {
			continue
		}
		if err := run.WriteFile(f.name, f.data); err != nil {
			skip(f.name, err)
		}
	}
	return set, nil
}

// deliverReports copies the four files to the directory of --report-dir, when there is one, and
// appends the summary to the file that GITHUB_STEP_SUMMARY names, when it is set. A failure says in one
// sentence what was not written and changes no exit code.
func deliverReports(stdout, stderr io.Writer, o options, reportDir, sub string, fin finishedRun) {
	set := fin.reports
	htmlPath := filepath.Join(fin.reportDir(), reportHTMLFile)
	if reportDir != "" {
		dir := reportDir
		if sub != "" {
			dir = filepath.Join(dir, sub)
		}
		abs, err := filepath.Abs(dir)
		if err == nil {
			htmlPath = filepath.Join(abs, reportHTMLFile)
			err = publishReports(abs, set, htmlPath)
		}
		if err != nil {
			fmt.Fprintf(stderr, "semar: the report files were not written to %s: %s\n", terminalSafe(oneLine(dir)), terminalSafe(oneLine(err.Error())))
		} else {
			fmt.Fprintf(stdout, "reports: %s\n", abs)
		}
	}
	if path, ok := o.LookupEnv(stepSummaryVar); ok && path != "" {
		if err := appendSummary(path, set, htmlPath); err != nil {
			fmt.Fprintf(stderr, "semar: the summary was not added to %s: %s\n", stepSummaryVar, terminalSafe(oneLine(err.Error())))
		}
	}
}

func (f finishedRun) reportDir() string { return filepath.Dir(f.reportPath) }

// summaryFor renders the summary of a run with the path where its HTML report is for the reader.
func summaryFor(set reportSet, htmlPath string) ([]byte, error) {
	var buf bytes.Buffer
	if err := report.PipelineSummary(&buf, set.data, htmlPath); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// publishReports creates dir when it is missing and replaces the four files in it. The names are
// resolved inside the directory, so a link in it cannot carry a write elsewhere.
func publishReports(dir string, set reportSet, htmlPath string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	summary, err := summaryFor(set, htmlPath)
	if err != nil {
		return err
	}
	for _, f := range []struct {
		name string
		data []byte
	}{{reportFile, set.md}, {reportHTMLFile, set.html}, {junitFile, set.junit}, {summaryFile, summary}} {
		if f.data == nil {
			return fmt.Errorf("%s was not made for this run", f.name)
		}
		if err := atomicfile.WriteIn(root, f.name, f.data, publishedFileMode); err != nil {
			return fmt.Errorf("%s: %w", f.name, err)
		}
	}
	return nil
}

func appendSummary(path string, set reportSet, htmlPath string) error {
	summary, err := summaryFor(set, htmlPath)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, publishedFileMode)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(summary, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
