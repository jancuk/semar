package activity

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/jancuk/semar/internal/domain"
)

type Filter struct {
	WritesOnly bool
	Scenario   domain.ScenarioID
	Pass       domain.Pass
}

func Timeline(w io.Writer, entries []domain.Activity, f Filter) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "#\tTime\tPass\tScenario\tAction\tTarget\tResult\tWhy"); err != nil {
		return err
	}
	for _, e := range entries {
		if !f.keeps(e) {
			continue
		}
		scenario := string(e.Use.Scenario)
		if scenario == "" {
			scenario = "-"
		}
		line := fmt.Sprintf("%d\t%s\t%s\t%s\t%s\t%s\t%s", e.Seq, e.At.UTC().Format("15.04.05"), e.Use.Pass, scenario, e.Verb, e.Target, e.Result)
		if e.Why != "" {
			line += "\t" + e.Why
		}
		if _, err := fmt.Fprintln(tw, line); err != nil {
			return err
		}
	}
	return tw.Flush()
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
