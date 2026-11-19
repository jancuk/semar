package cli

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/words"
)

// reviewTarget is a scenario of a run that ended needing a person, with the IDs that semar review
// takes for what it needs: the scenario itself, the fixtures, and the screens files.
type reviewTarget struct {
	scenario domain.ScenarioID
	ids      []string
}

var (
	unapprovedFixture = regexp.MustCompile(`fixture (\S+) is not approved`)
	unapprovedScreens = regexp.MustCompile(`the screens of (\S+) are not approved`)
)

func reviewTargets(results []domain.ScenarioResult) []reviewTarget {
	var out []reviewTarget
	for _, r := range results {
		texts := make([]string, len(r.Reasons))
		for i, reason := range r.Reasons {
			texts[i] = reason.Text
		}
		if words.Of(string(r.Outcome), texts) != "needs review" {
			continue
		}
		t := reviewTarget{scenario: r.Scenario, ids: []string{string(r.Scenario)}}
		for _, text := range texts {
			for _, re := range []*regexp.Regexp{unapprovedFixture, unapprovedScreens} {
				for _, m := range re.FindAllStringSubmatch(text, -1) {
					if !slices.Contains(t.ids, m[1]) {
						t.ids = append(t.ids, m[1])
					}
				}
			}
		}
		out = append(out, t)
	}
	return out
}

// reviewAfterRun offers the review of what the scenarios of a run needed, and runs the same review
// as semar review for those items. It returns the scenarios that the person settled, which are the
// ones to run again. It returns none when nothing needs review, when the person declines, and when
// the review cannot start; the last is said on standard error and never fails the run.
func reviewAfterRun(ctx context.Context, stdout, stderr io.Writer, o options, f runFlags, envName string, in *lineReader, results []domain.ScenarioResult) []domain.ScenarioID {
	rf := reviewFlags{env: envName, dir: f.dir}
	var pending []reviewTarget
	for _, t := range reviewTargets(results) {
		items, _, err := collectReview(stderr, rf, t.ids)
		if err != nil {
			fmt.Fprintf(stderr, "semar: %s is left out of the review: %s\n", t.scenario, oneLine(terminalSafe(err.Error())))
			continue
		}
		if len(items) > 0 {
			pending = append(pending, t)
		}
	}
	if len(pending) == 0 {
		return nil
	}

	fmt.Fprintf(stdout, "\n%s your review. Review %s now? [Y/n] ",
		plural(len(pending), "test")+map[bool]string{true: " needs", false: " need"}[len(pending) == 1],
		map[bool]string{true: "it", false: "them"}[len(pending) == 1])
	line, ok := in.read(ctx)
	if !ok {
		fmt.Fprintln(stdout)
		return nil
	}
	if a := strings.ToLower(strings.TrimSpace(line)); a != "" && a != "y" && a != "yes" {
		return nil
	}

	var ids []string
	for _, t := range pending {
		ids = append(ids, t.ids...)
	}
	items, _, err := collectReview(stderr, rf, ids)
	if err != nil {
		fmt.Fprintf(stderr, "semar: the review could not start: %s\n", oneLine(terminalSafe(err.Error())))
		return nil
	}
	if _, err := approveItems(ctx, stdout, o, rf, in, items); err != nil {
		fmt.Fprintf(stderr, "semar: the review stopped: %s\n", oneLine(terminalSafe(err.Error())))
		return nil
	}

	var again []domain.ScenarioID
	for _, t := range pending {
		if left, _, err := collectReview(stderr, rf, t.ids); err == nil && len(left) == 0 {
			again = append(again, t.scenario)
		}
	}
	return again
}

// mergeResults is the results of the first run, with those of the second in place of the scenarios
// that ran again.
func mergeResults(first, second []domain.ScenarioResult) []domain.ScenarioResult {
	out := slices.Clone(first)
	for i, r := range out {
		if j := slices.IndexFunc(second, func(s domain.ScenarioResult) bool { return s.Scenario == r.Scenario }); j >= 0 {
			out[i] = second[j]
		}
	}
	return out
}
