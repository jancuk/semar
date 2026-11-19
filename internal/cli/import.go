package cli

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/importer"
	"github.com/jancuk/semar/internal/redact"
	"github.com/jancuk/semar/internal/scenario"
	"github.com/jancuk/semar/internal/stage/plan"
)

const maxLeftShown = 20

type importFlags struct {
	ticket string
	env    string
	dir    string
}

func newImportCommand(o options) *cobra.Command {
	var f importFlags
	cmd := &cobra.Command{
		Use:   "import <file> --ticket <key> [--env <environment>]",
		Short: "Start the regression set from a Postman collection or a HAR file",
		Long: "Read a Postman collection (format 2.1) or a HAR file and write one scenario and one script for each request that goes to a host of the environment. The format is found from the content.\n" +
			"The session is never imported: Authorization, Cookie, and every header that the redaction rules name are dropped, and the scenario uses the account of the service. A request that holds a password or a token in its address or its body is left out. A HAR file gives only the requests whose response is JSON, and a repeated request once.\n" +
			"The expected status is the status of the recorded response, or of the first saved example of Postman. When there is none, a read is tried once when the rules of the environment allow it. Test scripts of Postman are not read.\n" +
			"A write request is imported and never sent by the import. Nothing that import writes is approved: each scenario goes through a review, and the rules of the environment decide at semar run whether a write is sent.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return importFile(cmd, o, f, args[0])
		},
	}
	cmd.Flags().StringVar(&f.ticket, "ticket", "", "the ticket of the scenarios that the file becomes")
	cmd.Flags().StringVar(&f.env, "env", "", "the environment whose hosts and services the requests must match, when there are several")
	_ = cmd.MarkFlagRequired("ticket")
	addDirFlag(cmd, &f.dir)
	return cmd
}

func importFile(cmd *cobra.Command, o options, f importFlags, path string) error {
	out := cmd.OutOrStdout()
	if err := idcheck.Check("ticket", f.ticket); err != nil {
		return err
	}
	data, err := readImportFile(path)
	if err != nil {
		return err
	}
	cfg, err := config.Load(f.dir)
	if err != nil {
		return err
	}
	envName, env, err := cfg.Environment(f.env)
	if err != nil {
		return err
	}
	env = withServiceAuth(cfg, env)
	screen, err := redact.NewScreen(cfg.Redact)
	if err != nil {
		return err
	}
	file, err := importer.Read(data, importer.DropHeader(screen))
	if err != nil {
		return err
	}

	existing, err := loadAllScenarios(f.dir)
	if err != nil {
		return err
	}
	base := filepath.Base(path)
	built := importer.Build(file, importer.Options{
		File: base, Ticket: f.ticket, Env: env, Screen: screen, Known: importer.Fingerprints(existing),
	})
	fmt.Fprintf(out, "Read a %s with %s.\n", file.Format, plural(len(file.Requests)+len(file.Left), "request"))

	statuses, left := importStatuses(cmd, o, f, cfg, envName, env, built.Drafts)
	built.Left = append(built.Left, left...)

	next := nextImportNumber(f.ticket, existing, f.dir)
	var scs []domain.Scenario
	var scripts []domain.Script
	for _, d := range built.Drafts {
		status, ok := statuses[d.Fingerprint]
		if !ok {
			continue
		}
		id := domain.ScenarioID(fmt.Sprintf("%s-I%02d", f.ticket, next))
		sc, s, err := d.Finalize(id, status)
		if err != nil {
			built.Left = append(built.Left, importer.Left{Name: shortName(d), Reason: err.Error()})
			continue
		}
		next++
		scs, scripts = append(scs, sc), append(scripts, s)
	}
	if err := writeImported(f.dir, f.ticket, scs, scripts); err != nil {
		return err
	}

	if len(scs) > 0 {
		fmt.Fprintf(out, "Wrote %s: %s.\n", plural(len(scs), "scenario"), idRange(scs))
	}
	printLeft(out, built.Left)
	if len(built.OtherHosts) > 0 {
		fmt.Fprintf(out, "Hosts of the file that the environment %s does not list: %s\n", envName, terminalSafe(strings.Join(built.OtherHosts, ", ")))
	}
	if file.WithTests > 0 {
		fmt.Fprintf(out, "%s had a test script. Semar does not read the test scripts of Postman.\n", plural(file.WithTests, "request"))
	}
	if len(scs) > 0 {
		fmt.Fprintln(out, "Nobody approved these scenarios: each needs a review before its expected result counts.")
	}
	fmt.Fprintf(out, "Wrote %s, left out %d.\n", plural(len(scs), "scenario"), len(built.Left))
	if len(scs) > 0 {
		fmt.Fprintln(out, "Next: semar run")
	}
	return nil
}

func readImportFile(path string) ([]byte, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read the file: %w", err)
	}
	defer fh.Close()
	info, err := fh.Stat()
	if err != nil {
		return nil, fmt.Errorf("read the file: %w", err)
	}
	if info.IsDir() {
		return nil, errors.New("the path is a folder, and semar import reads a file")
	}
	if info.Size() > importer.MaxFileBytes {
		return nil, importer.ErrTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(fh, importer.MaxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read the file: %w", err)
	}
	if len(data) > importer.MaxFileBytes {
		return nil, importer.ErrTooLarge
	}
	return data, nil
}

func shortName(d importer.Draft) string {
	if d.Scenario.Title != "" {
		return oneLine(d.Scenario.Title)
	}
	return d.Imported.Method
}

// importStatuses gives the expected status of each draft, by fingerprint. A draft that has none from
// the file is sent once when the rules of the environment allow a read, and the status that came is
// used. A draft that has no status then is left out with its reason.
func importStatuses(cmd *cobra.Command, o options, f importFlags, cfg config.Config, envName string, env config.Environment, drafts []importer.Draft) (map[string]int, []importer.Left) {
	statuses := map[string]int{}
	var left []importer.Left
	byProfile := map[string][]importer.Draft{}
	for _, d := range drafts {
		if d.Imported.Status > 0 {
			statuses[d.Fingerprint] = d.Imported.Status
			continue
		}
		byProfile[d.Scenario.AuthProfile] = append(byProfile[d.Scenario.AuthProfile], d)
	}
	for _, profile := range slices.Sorted(maps.Keys(byProfile)) {
		group := byProfile[profile]
		in := plan.Input{AuthProfile: profile}
		rig := planTrial(cmd.Context(), cmd.ErrOrStderr(), o, planFlags{env: f.env, dir: f.dir}, cfg, envName, env, &in, nil)
		noStatus := func(d importer.Draft, why string) {
			left = append(left, importer.Left{Name: shortName(d), Reason: "the file gives no status, and " + why})
		}
		if rig == nil {
			why := strings.TrimPrefix(in.TrialOff, trialSkipped+": ")
			if why == "" {
				why = "the request was not tried"
			}
			for _, d := range group {
				noStatus(d, "it was not tried: "+why)
			}
			continue
		}
		var scs []domain.Scenario
		var scripts []domain.Script
		tryID := map[domain.ScenarioID]importer.Draft{}
		for i, d := range group {
			sc, s, err := d.Finalize(domain.ScenarioID(fmt.Sprintf("IMPORT-TRY-%03d", i+1)), 200)
			if err != nil {
				noStatus(d, "it was not tried: "+err.Error())
				continue
			}
			tryID[sc.ID] = d
			scs, scripts = append(scs, sc), append(scripts, s)
		}
		tr, err := rig.runner.Try(cmd.Context(), scs, scripts)
		rig.close(cmd.ErrOrStderr(), o.Clock())
		got := map[domain.ScenarioID]plan.TrialStep{}
		for _, ts := range tr.Steps {
			if _, dup := got[ts.Scenario]; !dup {
				got[ts.Scenario] = ts
			}
		}
		for _, sc := range scs {
			d, ts := tryID[sc.ID], got[sc.ID]
			switch {
			case ts.Result == plan.TrialSent && ts.Status > 0 && ts.Status != 401 && ts.Status != 403 && ts.Status < 500:
				statuses[d.Fingerprint] = ts.Status
			case ts.Result == plan.TrialSent && ts.Status > 0:
				noStatus(d, fmt.Sprintf("the try came back with %d, which says nothing about the expected result", ts.Status))
			case ts.Why != "":
				noStatus(d, "it was not tried: "+oneLine(terminalSafe(ts.Why)))
			case err != nil:
				noStatus(d, "the try stopped: "+oneLine(terminalSafe(err.Error())))
			default:
				noStatus(d, "it was not tried")
			}
		}
	}
	return statuses, left
}

var importedID = regexp.MustCompile(`-I([0-9]+)$`)

// nextImportNumber is one more than the highest number of an imported ID of the ticket, in a scenario
// that is read or in a file name that is not.
func nextImportNumber(ticket string, scs []domain.Scenario, dir string) int {
	n := 0
	note := func(id string) {
		if rest, ok := strings.CutPrefix(id, ticket); ok {
			if m := importedID.FindStringSubmatch(rest); m != nil && m[0] == rest {
				if v, err := strconv.Atoi(m[1]); err == nil {
					n = max(n, v)
				}
			}
		}
	}
	for _, sc := range scs {
		note(string(sc.ID))
	}
	if files, err := scenarioFiles(dir); err == nil {
		for id := range files {
			note(string(id))
		}
	}
	return n + 1
}

func idRange(scs []domain.Scenario) string {
	if len(scs) == 1 {
		return string(scs[0].ID)
	}
	return fmt.Sprintf("%s to %s", scs[0].ID, scs[len(scs)-1].ID)
}

func printLeft(out io.Writer, left []importer.Left) {
	if len(left) == 0 {
		return
	}
	fmt.Fprintf(out, "Left out %d:\n", len(left))
	for i, l := range left {
		if i == maxLeftShown {
			fmt.Fprintf(out, "  and %d more\n", len(left)-maxLeftShown)
			break
		}
		fmt.Fprintf(out, "  %s: %s\n", terminalSafe(oneLine(l.Name)), terminalSafe(oneLine(l.Reason)))
	}
}

// writeImported writes every scenario file and every script, or none: a failure removes the files and
// the folders that this call made. A script that is stored stays, because it is named by its hash and
// no scenario reads it without the file.
func writeImported(dir, ticket string, scs []domain.Scenario, scripts []domain.Script) error {
	if len(scs) == 0 {
		return nil
	}
	rendered := make([][]byte, len(scs))
	for i, sc := range scs {
		data, err := scenario.Render(sc, "")
		if err != nil {
			return fmt.Errorf("scenario %s: %w", sc.ID, err)
		}
		rendered[i] = data
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	folder := filepath.Join(".semar", "scenarios", ticket)
	if err := atomicfile.CheckDirIn(root, folder); err != nil {
		return err
	}
	madeDirs, err := atomicfile.MkdirIn(root, folder)
	var madeFiles []string
	undo := func(cause error) error {
		for _, p := range madeFiles {
			_ = root.Remove(p)
		}
		for i := len(madeDirs) - 1; i >= 0; i-- {
			_ = root.Remove(madeDirs[i])
		}
		return cause
	}
	if err != nil {
		return undo(err)
	}
	for i, sc := range scs {
		rel := filepath.Join(folder, string(sc.ID)+featureExt)
		if err := atomicfile.WriteNewIn(root, rel, rendered[i], planFileMode); err != nil {
			return undo(fmt.Errorf("write %s: %w", filepath.ToSlash(rel), err))
		}
		madeFiles = append(madeFiles, rel)
	}
	for _, s := range scripts {
		_, _, problems, err := storeScript(dir, s)
		if err == nil && len(problems) > 0 {
			err = fmt.Errorf("the script of %s does not fit its scenario: %s", s.Scenario, strings.Join(problems, "; "))
		}
		if err != nil {
			return undo(err)
		}
	}
	return nil
}
