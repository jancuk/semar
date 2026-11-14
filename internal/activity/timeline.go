package activity

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/jancuk/semar/internal/domain"
)

type Filter struct {
	WritesOnly bool
	Scenario   domain.ScenarioID
	Pass       domain.Pass
}

func Timeline(w io.Writer, entries []domain.Activity, f Filter) error {
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tTime\tPass\tScenario\tAction\tTarget\tResult\tWhy\t")
	for _, e := range entries {
		if !f.keeps(e) {
			continue
		}
		scenario := string(e.Use.Scenario)
		if scenario == "" {
			scenario = "-"
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t\n", e.Seq, e.At.UTC().Format("15.04.05"), e.Use.Pass, scenario, e.Verb, e.Target, e.Result, e.Why)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n") {
		if _, err := fmt.Fprintln(w, strings.TrimRight(line, " ")); err != nil {
			return err
		}
	}
	return nil
}

func (f Filter) keeps(e domain.Activity) bool {
	if f.WritesOnly && e.Verb != domain.VerbCreate && e.Verb != domain.VerbUpdate && e.Verb != domain.VerbDelete {
		return false
	}
	if f.Scenario != "" && e.Use.Scenario != f.Scenario {
		return false
	}
	return f.Pass == "" || e.Use.Pass == f.Pass
}
