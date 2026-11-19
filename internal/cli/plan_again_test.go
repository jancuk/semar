package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jancuk/semar/internal/cli"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/claude"
	"github.com/jancuk/semar/internal/provider/fake"
	"github.com/jancuk/semar/internal/sampleapp"
	"github.com/jancuk/semar/internal/vcs/gittest"
)

func planDirs(t *testing.T, p *planRepo) []string {
	t.Helper()
	entries, err := os.ReadDir(p.path(".semar", "plans"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestASecondPlanOfAKeptCriterionCallsNoModelAndWritesNothing(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.commitCriterionTicket(t)
	p.configure(t, criterionAnswerJSON)
	p.withCriterionKnowledge(t)
	made := p.recordJobs()
	args := []string{"plan", "--diff", "main..release", "--pipeline", "criteria", "--ladder", "planner:small", "--no-trial"}
	p.must(t, cli.ExitOK, args...)
	plansBefore := planDirs(t, p)
	scenario := p.path(".semar", "scenarios", "SHOP-482", "SHOP-482-01.feature")
	before, err := os.ReadFile(scenario)
	if err != nil {
		t.Fatal(err)
	}

	res := p.must(t, cli.ExitOK, args...)
	want := "Every criterion of SHOP-482 has a test or a manual check already. Nothing was planned. To plan one again, remove its test with semar rm.\n"
	if res.stdout != want {
		t.Errorf("stdout = %q, want %q", res.stdout, want)
	}
	if len(*made) != 1 || len((*made)[0].Jobs()) != 1 {
		t.Errorf("a model was called again: %d providers", len(*made))
	}
	if got := planDirs(t, p); strings.Join(got, ",") != strings.Join(plansBefore, ",") {
		t.Errorf("plan directories %v, were %v", got, plansBefore)
	}
	after, err := os.ReadFile(scenario)
	if err != nil || string(after) != string(before) {
		t.Errorf("the scenario changed: %v", err)
	}
	if matches, _ := filepath.Glob(p.path(".semar", "scenarios", "SHOP-482", "*.feature")); len(matches) != 1 {
		t.Errorf("scenario files: %v", matches)
	}
	p.assertNoTempCopy(t)
}

// countingModel answers every job with a scenario of its own title, and counts the jobs and keeps the
// prompts. It never reports a cost.
func (p *planRepo) countingModel() (jobs *atomic.Int64, prompts func() []string) {
	var n atomic.Int64
	var mu sync.Mutex
	var seen []string
	p.opts.Providers = func(root string) *provider.Registry {
		r := provider.NewRegistry()
		factory := func(cfg config.Provider) (provider.Instance, error) {
			return fake.NewFunc(cfg.ID, func(j provider.Job) (provider.Result, error) {
				mu.Lock()
				seen = append(seen, j.Prompt)
				mu.Unlock()
				out := strings.Replace(criterionAnswerJSON, "is refused", fmt.Sprintf("is refused, case %d", n.Add(1)), 1)
				return provider.Result{Output: []byte(out), Model: "fake", Turns: 1, InputTokens: 10}, nil
			}), nil
		}
		r.Register(fake.Kind, factory)
		r.Register(claude.Kind, factory)
		return r
	}
	return &n, func() []string { mu.Lock(); defer mu.Unlock(); return slices.Clone(seen) }
}

func ticketOfCriteria(criteria ...string) string {
	var b strings.Builder
	b.WriteString("# SHOP-482 Refund above the total\n\n## Acceptance criteria\n\n")
	for i, c := range criteria {
		fmt.Fprintf(&b, "%d. %s\n", i+1, c)
	}
	return b.String()
}

func TestASecondPlanWritesATestOnlyForACriterionThatHasNone(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	const (
		one     = "A refund above the rest of the total is refused with 422."
		two     = "A refund of the whole total is accepted."
		twoEdit = "A refund of the whole total is accepted and the order is closed."
		three   = "A refund of a cancelled order is refused with 409."
	)
	p.git.Commit("SHOP-482 write the ticket and the API", map[string]*string{
		"docs/tickets/SHOP-482.md": gittest.S(ticketOfCriteria(one, two)),
		"docs/openapi.yaml":        gittest.S(criterionAPI),
	})
	p.configure(t, criterionAnswerJSON)
	p.withCriterionKnowledge(t)
	jobs, prompts := p.countingModel()
	args := []string{"plan", "--diff", "main..release", "--pipeline", "criteria", "--ladder", "planner:small", "--no-trial"}
	feature := func(id string) string {
		data, err := os.ReadFile(p.path(".semar", "scenarios", "SHOP-482", id+".feature"))
		if err != nil {
			t.Fatalf("scenario %s: %v", id, err)
		}
		return string(data)
	}

	first := p.must(t, cli.ExitOK, args...)
	if jobs.Load() != 2 {
		t.Fatalf("the first plan ran %d jobs, want 2", jobs.Load())
	}
	const firstEnd = "  semar run   run the tests against the service\n"
	if !strings.HasSuffix(first.stdout, firstEnd) {
		t.Errorf("the first plan ends with\n%s", lastLines(first.stdout, 3))
	}
	kept := feature("SHOP-482-01")

	// Nothing changed: no job, no plan, no new file.
	plans := planDirs(t, p)
	second := p.must(t, cli.ExitOK, args...)
	if want := "Every criterion of SHOP-482 has a test or a manual check already. Nothing was planned. To plan one again, remove its test with semar rm.\n"; second.stdout != want {
		t.Errorf("the second plan prints %q, want %q", second.stdout, want)
	}
	if jobs.Load() != 2 || strings.Join(planDirs(t, p), ",") != strings.Join(plans, ",") || exists(t, p.path(".semar", "scenarios", "SHOP-482", "SHOP-482-03.feature")) {
		t.Errorf("the second plan did work: %d jobs, plans %v", jobs.Load(), planDirs(t, p))
	}

	// A criterion is edited and another one is added: two jobs, two tests, and the old test is named.
	p.git.Commit("SHOP-482 edit the ticket", map[string]*string{"docs/tickets/SHOP-482.md": gittest.S(ticketOfCriteria(one, twoEdit, three))})
	third := p.must(t, cli.ExitOK, args...)
	if jobs.Load() != 4 {
		t.Fatalf("the third plan ran %d jobs in all, want 4", jobs.Load()-2)
	}
	var places []string
	for _, pr := range prompts()[2:] {
		_, rest, _ := strings.Cut(pr, "## The criterion\n\n```text\n")
		place, _, _ := strings.Cut(rest, "\n")
		places = append(places, place)
	}
	slices.Sort(places)
	if want := "docs/tickets/SHOP-482.md:6,docs/tickets/SHOP-482.md:7"; strings.Join(places, ",") != want {
		t.Errorf("the jobs were for %v, want the edited criterion and the new one", places)
	}
	const thirdEnd = "criteria: 3, 1 kept, 2 planned (1 changed), 0 removed\n" +
		"SHOP-482-02 was written for an earlier text of criterion 2. Read both tests. To remove the old one: semar rm SHOP-482-02 --reason \"criterion 2 changed\"\n"
	if !strings.HasSuffix(third.stdout, thirdEnd) {
		t.Errorf("the third plan ends with\n%s\nwant\n%s", lastLines(third.stdout, 4), thirdEnd)
	}
	for _, id := range []string{"SHOP-482-03", "SHOP-482-04"} {
		if !exists(t, p.path(".semar", "scenarios", "SHOP-482", id+".feature")) {
			t.Errorf("scenario %s was not written", id)
		}
	}
	if exists(t, p.path(".semar", "scenarios", "SHOP-482", "SHOP-482-05.feature")) {
		t.Error("a fifth scenario was written")
	}
	if feature("SHOP-482-01") != kept {
		t.Error("the test of the kept criterion was rewritten, so its confirmation would lapse")
	}
	if !exists(t, p.path(".semar", "scenarios", "SHOP-482", "SHOP-482-02.feature")) {
		t.Error("the plan removed the old test: only a person may")
	}
	md, err := os.ReadFile(p.path(".semar", "plans", p.head(t)[:12], "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	contains(t, "plan.md", string(md),
		"- Kept, criterion 1 of SHOP-482. Tests: SHOP-482-01. No job ran for it.\n",
		"- Changed, criterion 2 of SHOP-482. It was edited, so it was planned again. Tests of the earlier text: SHOP-482-02.")
	ob, err := os.ReadFile(p.path(".semar", "obligations", "SHOP-482.plan.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(ob), "criterion: {number: "); n != 4 {
		t.Errorf("%d entries name their criterion, want 4:\n%s", n, ob)
	}

	// A criterion is deleted from the ticket. Every one that is left has a test, so nothing is planned,
	// and the tests of the texts that the ticket no longer has are named, for a person to remove.
	p.git.Commit("SHOP-482 drop a criterion", map[string]*string{"docs/tickets/SHOP-482.md": gittest.S(ticketOfCriteria(one, twoEdit))})
	fourth := p.must(t, cli.ExitOK, args...)
	const fourthWant = "Every criterion of SHOP-482 has a test or a manual check already. Nothing was planned. To plan one again, remove its test with semar rm.\n" +
		"SHOP-482-02 was written for a criterion that SHOP-482 no longer has (it was criterion 2). To remove it: semar rm SHOP-482-02 --reason \"criterion 2 was removed\"\n" +
		"SHOP-482-04 was written for a criterion that SHOP-482 no longer has (it was criterion 3). To remove it: semar rm SHOP-482-04 --reason \"criterion 3 was removed\"\n"
	if fourth.stdout != fourthWant || jobs.Load() != 4 {
		t.Errorf("the fourth plan prints\n%s\nwant\n%s\nafter %d jobs", fourth.stdout, fourthWant, jobs.Load())
	}
	p.assertNoTempCopy(t)
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func TestASecondPlanKeepsTheTestsOfAPlanThatWroteNoKey(t *testing.T) {
	p := newPlanRepo(t, sampleapp.Options{})
	p.git.Commit("SHOP-482 write the ticket and the API", map[string]*string{
		"docs/tickets/SHOP-482.md": gittest.S(ticketOfCriteria("A refund above the rest of the total is refused with 422.", "A refund of the whole total is accepted.")),
		"docs/openapi.yaml":        gittest.S(criterionAPI),
	})
	p.configure(t, criterionAnswerJSON)
	p.withCriterionKnowledge(t)
	jobs, _ := p.countingModel()
	args := []string{"plan", "--diff", "main..release", "--pipeline", "criteria", "--ladder", "planner:small", "--no-trial"}
	p.must(t, cli.ExitOK, args...)

	// A repository that was planned before the plan wrote keys has the same files without them.
	file := p.path(".semar", "obligations", "SHOP-482.plan.yaml")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	old := regexp.MustCompile(`(?m)^ +criterion: \{.*\}\n`).ReplaceAllString(string(data), "")
	if old == string(data) {
		t.Fatal("the obligations file holds no criterion to strip")
	}
	if err := os.WriteFile(file, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	res := p.must(t, cli.ExitOK, args...)
	contains(t, "stdout", res.stdout, "Every criterion of SHOP-482 has a test or a manual check already.")
	if jobs.Load() != 2 {
		t.Errorf("%d jobs ran in all, want the 2 of the first plan", jobs.Load())
	}
}
