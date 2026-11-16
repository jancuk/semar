package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/export"
)

// exportFileMode is the mode of a report file. An export is not secret: the evidence it
// reads was redacted, and a CI system has to read it.
const exportFileMode os.FileMode = 0o644

type exportFlags struct {
	out   string
	force bool
	dir   string
}

func newExportCommand() *cobra.Command {
	var f exportFlags
	cmd := &cobra.Command{
		Use:   "export (ctrf | junit | sarif) <run id>",
		Short: "Write one stored run as a CTRF, JUnit XML, or SARIF report",
		Long: "Write one stored run in a common format. ctrf and junit list every scenario, and sarif lists the\n" +
			"confirmed defects of the run. An export reads stored evidence, computes no outcome, and refuses a\n" +
			"run that is interrupted or whose evidence changed. The exit code is 0 when the report was written\n" +
			"and 3 when it was not; it does not reflect the outcomes.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExport(cmd.OutOrStdout(), cmd.ErrOrStderr(), f, args[0], domain.RunID(args[1]))
		},
	}
	cmd.Flags().StringVar(&f.out, "out", "", "write the report to this new file instead of standard output")
	cmd.Flags().BoolVar(&f.force, "force", false, "replace the file named by --out when it exists")
	addDirFlag(cmd, &f.dir)
	return cmd
}

func exportWriter(format string) (func(io.Writer, export.Run) error, error) {
	switch format {
	case "ctrf":
		return export.CTRF, nil
	case "junit":
		return export.JUnit, nil
	case "sarif":
		return export.SARIF, nil
	}
	return nil, fmt.Errorf("unknown export format %q: use ctrf, junit, or sarif", format)
}

func runExport(stdout, stderr io.Writer, f exportFlags, format string, id domain.RunID) error {
	write, err := exportWriter(format)
	if err != nil {
		return err
	}
	if err := requireSemarDir(f.dir); err != nil {
		return err
	}
	store, err := evidence.Open(semarDir(f.dir))
	if err != nil {
		return err
	}
	run, err := export.Load(store, f.dir, id, Version)
	if err != nil {
		return err
	}
	// the whole report is built before anything is written, so a failure leaves no partial file
	var buf bytes.Buffer
	if err := write(&buf, run); err != nil {
		return err
	}
	if f.out == "" {
		_, err := stdout.Write(buf.Bytes())
		return err
	}
	if err := writeReport(f.out, buf.Bytes(), f.force); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stderr, "semar: wrote %s for run %s to %s\n", format, run.Run.ID, f.out)
	return err
}

// writeReport writes a new file, or replaces one when force is set. The name is resolved
// inside its directory, so a link swapped in after the check cannot carry the write elsewhere.
func writeReport(out string, data []byte, force bool) error {
	dir, name := filepath.Dir(out), filepath.Base(out)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create the directory of %s: %w", out, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open the directory of %s: %w", out, err)
	}
	defer root.Close()
	if force {
		return atomicfile.WriteIn(root, name, data, exportFileMode)
	}
	err = atomicfile.WriteNewIn(root, name, data, exportFileMode)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s already exists: name another file, or use --force to replace it", out)
	}
	return err
}
