package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/changeset"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/confirm"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/fixture"
	"github.com/jancuk/semar/internal/journey"
	"github.com/jancuk/semar/internal/knowledge"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/stage/plan"
	"github.com/jancuk/semar/internal/vcs"
)

type planFlags struct {
	diff         diffFlags
	model        string
	reasoning    string
	provider     string
	withSetup    bool
	withSetupSet bool
	noTrial      bool
	pipeline     string
	ladder       string
	maxCost      float64
	maxCostSet   bool
	maxScenarios int
	env          string
	authProfile  string
	dir          string
	init         initFlags
	initGiven    bool
	given        map[string]bool
}

// agentFiles are read by an agent harness as instructions or tools, and a .git directory would
// make the copy a repository whose configuration the repository under test chose. They are
// removed from the copy at every depth before the planner starts (R5).
var agentFiles = []string{"CLAUDE.md", "CLAUDE.local.md", "AGENTS.md", ".mcp.json", ".claude", ".codex", ".agents", "AGENTS.override.md", ".cursor", ".git"}

func newPlanCommand(o options) *cobra.Command {
	var f planFlags
	cmd := &cobra.Command{
		Use:   "plan --diff <link or range>",
		Short: "Draft scenarios and scripts for the change between two branches",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f.withSetupSet = cmd.Flags().Changed("with-setup")
			f.maxCostSet = cmd.Flags().Changed("max-cost")
			f.initGiven = initFlagGiven(cmd)
			f.given = givenInitFlags(cmd)
			return planChange(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o, f)
		},
	}
	addDiffFlags(cmd, &f.diff)
	cmd.Flags().StringVar(&f.model, "model", "", "the model of the planner (default: the provider's model)")
	cmd.Flags().StringVar(&f.reasoning, "reasoning", "", "the reasoning level of the planner, such as high (default: the provider's reasoning, else its own default)")
	cmd.Flags().StringVar(&f.provider, "provider", "", "the provider that plans (default: see semar providers)")
	cmd.Flags().BoolVar(&f.withSetup, "with-setup", false, "plan with your MCP servers and skills, which add tokens to every turn (default: plan.withSetup of your providers file, else off)")
	cmd.Flags().StringVar(&f.pipeline, "pipeline", "", "how the plan is made: criteria, one job with no tools for each acceptance criterion, which suits small models; or single, one job that reads the repository (default: criteria when a ticket of the change has acceptance criteria, else single)")
	cmd.Flags().StringVar(&f.ladder, "ladder", "", "with --pipeline criteria, the models from the cheapest up, each as <provider id>:<model>, such as claude:haiku,claude:sonnet,claude:opus (default: plan.ladder of your providers file, else the provider and model of the plan)")
	cmd.Flags().Float64Var(&f.maxCost, "max-cost", 0, "the most this plan may spend, in dollars, as the providers report it: when the sum reaches it, no new job starts, the jobs that run finish, and the plan is written with what was answered, with exit code 2 (default: plan.maxCost of your providers file, else no limit)")
	cmd.Flags().BoolVar(&f.noTrial, "no-trial", false, "do not send the read-only steps of the draft to the environment before the plan is written, for this run")
	cmd.Flags().IntVar(&f.maxScenarios, "max-scenarios", 0, "the most scenarios to draft (default: plan.maxScenarios)")
	cmd.Flags().StringVar(&f.env, "env", "", "the environment of semar.yaml to plan for; needed when there are several. With --base-url, the name of the new environment (default uat, or production with --kind production)")
	cmd.Flags().StringVar(&f.authProfile, "auth-profile", "", "the auth profile of the scenarios; needed when there are several and none is named default")
	addInitFlags(cmd, &f.init)
	addDirFlag(cmd, &f.dir)
	return cmd
}

func planChange(ctx context.Context, stdout, stderr io.Writer, o options, f planFlags) error {
	if f.maxScenarios < 0 {
		return errors.New("--max-scenarios must be positive")
	}
	if err := checkPipelineFlags(f); err != nil {
		return err
	}
	if f.maxCostSet && (f.maxCost <= 0 || math.IsNaN(f.maxCost) || math.IsInf(f.maxCost, 0)) {
		return errors.New("--max-cost must be a positive number of dollars, such as 2.50")
	}
	if f.model != "" {
		if err := config.CheckModel(f.model); err != nil {
			return fmt.Errorf("--model: %w", err)
		}
	}
	if f.reasoning != "" {
		if err := config.CheckReasoning(f.reasoning); err != nil {
			return fmt.Errorf("--reasoning: %w", err)
		}
	}
	cfg, err := config.Load(f.dir)
	missing := errors.Is(err, fs.ErrNotExist)
	var initData []byte
	switch {
	case missing && f.init.baseURL == "":
		fmt.Fprintf(stderr, "semar: there is no %s; name the test environment, for example:\n", configPath(f.dir))
		fmt.Fprintln(stderr, "  semar plan --diff <link or range> --base-url https://<host of the test environment> --token-env <variable that holds the token>")
		return exitError{ExitError}
	case missing:
		in := f.init
		in.env = f.env
		if initData, err = buildInitConfig(f.dir, in); err != nil {
			return err
		}
		cfg = config.Config{Tickets: config.Tickets{Pattern: changeset.DefaultTicketPattern}}
	case err != nil:
		return err
	case f.initGiven:
		diffs, err := diffInitFlags(cfg, f.env, f.authProfile, f.init, f.given)
		if err != nil {
			return err
		}
		if len(diffs) > 0 {
			return initFlagsDiffer(diffs, "To plan with the file, run the command without the setup flags. To change the environment, edit "+configFileName+".")
		}
		fmt.Fprintf(stderr, "semar: %s already has these settings, so the setup flags are not needed\n", configFileName)
	}
	ticket, err := regexp.Compile(cfg.Tickets.Pattern)
	if err != nil {
		return fmt.Errorf("tickets.pattern: %w", err)
	}

	g, cs, err := readChange(ctx, stderr, f.dir, f.diff, ticket)
	if err != nil {
		return err
	}
	if len(cs.Commits) == 0 {
		fmt.Fprintf(stdout, "there is no difference between %s and %s\n", terminalSafe(cs.Base), terminalSafe(cs.Head))
		return exitError{ExitUndecided}
	}

	if missing {
		path, err := writeInitConfig(f.dir, initData)
		if err != nil {
			return err
		}
		printInitDone(stderr, path, f.init)
		if cfg, err = config.Load(f.dir); err != nil {
			return err
		}
	}
	if len(cs.Tickets) == 0 {
		fmt.Fprintf(stderr, "semar: no commit between %s and %s names a ticket (pattern %s); set tickets.pattern in %s\n",
			terminalSafe(cs.Base), terminalSafe(cs.Head), terminalSafe(cfg.Tickets.Pattern), configPath(f.dir))
		return exitError{ExitUndecided}
	}
	in, envName, env, err := planInput(cfg, f, cs)
	if err != nil {
		return err
	}
	var catalogue fixture.Catalogue
	if in.Fixtures, catalogue, err = planFixtures(stderr, f.dir, cfg); err != nil {
		return err
	}
	in.Journeys, in.Environment = planJourneys(stderr, f.dir), envName
	scs, err := loadAllScenarios(f.dir)
	if err != nil {
		return err
	}
	inChange := map[string]bool{}
	for _, t := range cs.Tickets {
		inChange[t.Key] = true
	}
	known := map[domain.ScenarioID]bool{}
	for _, sc := range scs {
		known[sc.ID] = true
		if inChange[sc.Ticket] {
			in.Existing[sc.Ticket] = append(in.Existing[sc.Ticket], sc)
		}
	}

	src, err := loadSources(o, f.dir, cfg)
	if err != nil {
		return err
	}
	root, err := sourceCopyRoot(o)
	if err != nil {
		return fmt.Errorf("create a directory for the copy of the source: %w", err)
	}
	tmp, err := os.MkdirTemp(root, sourceCopyPrefix)
	if err != nil {
		return fmt.Errorf("create a directory for the copy of the source: %w", err)
	}
	defer os.RemoveAll(tmp)
	var leftOut []vcs.Skipped
	if in.SourceDir, in.Diff, leftOut, err = prepareSource(ctx, g, cs, tmp); err != nil {
		return err
	}
	for _, s := range leftOut {
		in.LeftOut = append(in.LeftOut, plan.LeftOut{Path: s.Path, Reason: s.Reason})
	}
	if len(leftOut) > 0 {
		fmt.Fprintf(stderr, "semar: %s of %s %s left out of the copy that the planner reads; plan.md lists them\n",
			plural(len(leftOut), "file"), shortCommit(cs.HeadCommit), map[bool]string{true: "was", false: "were"}[len(leftOut) == 1])
	}
	if in.Knowledge, err = findKnowledge(stderr, in.SourceDir, folderInWorkTree(f.dir), cfg.Knowledge); err != nil {
		return err
	}
	in.Tickets = findTickets(stderr, in.SourceDir, in.Knowledge, cs)
	in.MaxCost = f.maxCost
	if !f.maxCostSet && src.fileExists {
		in.MaxCost = src.file.Plan.MaxCost
	}
	criteria, why, err := choosePipeline(f, in.Tickets)
	if err != nil {
		return err
	}
	if why != "" {
		fmt.Fprintln(stderr, why)
	}
	var pc config.Provider
	var model string
	var inst provider.Instance
	var ladder []plan.Rung
	var rungConfigs []config.Provider
	var rungInsts []provider.Instance
	if criteria {
		if ladder, rungConfigs, rungInsts, err = criteriaLadder(ctx, stderr, o, f, src, cfg); err != nil {
			return err
		}
		pc, model, inst = rungConfigs[0], ladder[0].Model, rungInsts[0]
	} else {
		pc, _, err = src.choose(f.provider)
		if err != nil {
			return err
		}
		pc.Setup = planSetup(pc, src.withSetup(f.withSetup, f.withSetupSet))
		if f.reasoning != "" {
			pc.Reasoning = f.reasoning
		}
		model, err = planModel(f.model, pc, src.filePath)
		if err != nil {
			return err
		}
		o.drop.set(append(secretEnvNames(cfg), keyEnvNames(src.file)...))
		inst, err = o.Providers(f.dir).New(pc)
		if err != nil {
			return err
		}
		if h := inst.Health(ctx); !h.Installed {
			fmt.Fprintf(stderr, "semar: provider %s (%s) cannot plan: %s\n", inst.ID(), inst.Kind(), terminalSafe(h.Detail))
			return exitError{ExitError}
		}
	}

	if criteria {
		// The jobs of the criteria pipeline read nothing, so they run in a directory of their own
		// that holds nothing: no job can see the copy of the source.
		in.JobDir = filepath.Join(tmp, "empty")
		if err := os.Mkdir(in.JobDir, 0o700); err != nil {
			return fmt.Errorf("create a directory for the jobs: %w", err)
		}
		in.Ladder, in.Previous = ladder, loadAnswers(f.dir)
	}

	in.BaseAPI = baseDocuments(ctx, g, cs, in.Knowledge)
	rig := planTrial(ctx, stderr, o, f, cfg, envName, env, &in, &catalogue)

	in.Model = model
	in.Reasoning = pc.Reasoning
	in.SetupOn = provider.IsHarness(pc.Kind) && (pc.Setup.MCP || pc.Setup.Skills)
	if criteria {
		if note := costLimitNote(in.MaxCost, rungConfigs); note != "" {
			fmt.Fprintln(stderr, note)
		}
	} else if note := costLimitNote(in.MaxCost, []config.Provider{pc}); note != "" {
		fmt.Fprintln(stderr, note)
	}
	var fxRig *fixtureRig
	if criteria {
		fxRig = planFixtureTrial(o, f, cfg, envName, env, in.AuthProfile)
		in.FixtureTrial = fxRig
		fmt.Fprintln(stderr, criteriaStartLine(ladder, rungConfigs, cs.Head))
	} else {
		fmt.Fprintln(stderr, startLine(pc, cs.Head, model, pc.Reasoning))
	}

	started := time.Now()
	rep := startProgress(stderr, o.Clock, o.StderrTerminal(), o.Tick)
	defer rep.Stop() // so a panic or an early return cannot leave the status line on the screen
	in.Progress = rep.Report
	var out plan.Output
	if criteria {
		out, err = plan.RunCriteria(ctx, in)
	} else {
		out, err = plan.Run(ctx, inst, in)
	}
	rep.Stop()
	if rig != nil {
		rig.close(stderr, o.Clock())
	}
	if fxRig != nil {
		fxRig.close(stderr)
	}
	in.Fixtures = append(slices.Clone(in.Fixtures), out.DraftedEntries()...)
	if err == nil {
		fmt.Fprintf(stderr, "semar: the planner finished in %s\n", formatElapsed(rep.Elapsed()))
	}
	record := func(outcome, ref string) {
		if criteria {
			o.recordPipelineUsage(stderr, rungInsts, out, outcome, ref)
			return
		}
		o.recordUsage(stderr, inst, model, pc.Reasoning, out, outcome, ref, time.Since(started))
	}
	if err != nil {
		record(usageOutcome(ctx, err), "")
		if line := useLine(out, in.SetupOn); line != "" {
			fmt.Fprintf(stderr, "semar: %s\n", line)
		}
		return planFailed(ctx, stderr, err)
	}
	planFile, err := writePlan(f.dir, in, out, known)
	if err != nil {
		record("failed:not_written", "")
		return err
	}
	record("ok", filepath.Base(filepath.Dir(planFile)))
	printPlanDone(stdout, out, len(cfg.Knowledge) > 0, trialSummary(in, out), useLine(out, in.SetupOn), planFile, nextRun(env, envName, f.env, cs.HeadCommit))
	if out.CostLimit {
		fmt.Fprintf(stderr, "semar: the cost limit of %s was reached: %s. Plan again to do the rest: the answers that exist are reused\n",
			plan.Dollars(in.MaxCost), stoppedWords(out))
		return exitError{ExitUndecided}
	}
	if len(out.Scenarios) == 0 {
		return exitError{ExitUndecided}
	}
	return nil
}

// findTickets reads the file of each ticket of the change from the tickets sources of the copy of
// the source. A ticket with no file is one line on standard error, and the plan goes on: its
// expectations come from the code. Nothing is said when no tickets source is listed.
func findTickets(stderr io.Writer, sourceDir string, sources []knowledge.Source, cs domain.ChangeSet) []knowledge.Ticket {
	listed := slices.ContainsFunc(sources, func(s knowledge.Source) bool { return s.Usable() && s.Kind == config.KnowledgeTickets })
	if !listed {
		return nil
	}
	keys := make([]string, len(cs.Tickets))
	for i, t := range cs.Tickets {
		keys[i] = t.Key
	}
	tickets := knowledge.ReadTickets(sourceDir, sources, keys)
	var without []string
	for _, key := range slices.Sorted(slices.Values(keys)) {
		if !slices.ContainsFunc(tickets, func(t knowledge.Ticket) bool { return t.Key == key }) {
			without = append(without, terminalSafe(key))
		}
	}
	if len(without) > 0 {
		fmt.Fprintf(stderr, "semar: no ticket file in the copy of the source for %s; the expectations of %s come from the code\n",
			strings.Join(without, ", "), map[bool]string{true: "this ticket", false: "these tickets"}[len(without) == 1])
	}
	return tickets
}

// planFixtures is what the planner may name under given: every valid fixture of the repository,
// marked approved when a person approved it as it is now. A file that cannot be used stays out,
// and one line for each of its problems says so.
func planFixtures(stderr io.Writer, repo string, cfg config.Config) ([]fixture.Entry, fixture.Catalogue, error) {
	cat, err := fixture.Load(repo, allServices(cfg))
	if err != nil {
		return nil, fixture.Catalogue{}, err
	}
	for _, p := range cat.Problems {
		fmt.Fprintf(stderr, "semar: fixture file %s: %s; the planner does not see it\n", terminalSafe(p.File), terminalSafe(oneLine(problemText(p))))
	}
	approvals := confirm.NewLocal(semarDir(repo))
	return cat.Entries(func(f domain.Fixture) bool {
		ok, err := approvals.FixtureApproved(f.ID, domain.FixtureHash(f))
		return err == nil && ok
	}), cat, nil
}

// planJourneys is the journeys that plan.md may list as flows to rerun: every valid file of
// .semar/journeys. A file that cannot be used stays out, and one line for each of its problems says
// so. A directory that cannot be read at all leaves the section out with one line, because the plan
// does not depend on journeys.
func planJourneys(stderr io.Writer, repo string) []domain.Journey {
	js, problems, err := journey.Load(repo)
	if err != nil {
		fmt.Fprintf(stderr, "semar: the journey files could not be read, so plan.md lists no journey: %s\n", terminalSafe(oneLine(err.Error())))
		return nil
	}
	for _, p := range problems {
		fmt.Fprintf(stderr, "semar: journey file %s; plan.md does not use it\n", problemLine(p))
	}
	return js
}

func valueOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// planInput takes the names the planner may use from the environment that run would pick.
func planInput(cfg config.Config, f planFlags, cs domain.ChangeSet) (plan.Input, string, config.Environment, error) {
	envName, env, err := cfg.Environment(f.env)
	if err != nil {
		return plan.Input{}, "", env, err
	}
	if len(env.Services) == 0 {
		return plan.Input{}, "", env, errors.New("the environment has no service; add one under services in semar.yaml")
	}
	var services []string
	for _, s := range env.Services {
		services = append(services, s.Name)
	}
	profile, err := pickAuthProfile(cfg, f.authProfile)
	if err != nil {
		return plan.Input{}, "", env, err
	}
	var header string
	if p := cfg.AuthProfiles[profile].Session; p != nil {
		header = p.Header
	}
	limits := cfg.Plan
	if f.maxScenarios > 0 {
		limits.MaxScenarios = f.maxScenarios
	}
	var accounts []plan.Account
	for _, name := range slices.Sorted(maps.Keys(cfg.AuthProfiles)) {
		accounts = append(accounts, plan.Account{Name: name, Description: cfg.AuthProfiles[name].Description})
	}
	return plan.Input{
		Change: cs, Services: services, Methods: env.Methods, AuthProfile: profile, SessionHeader: header,
		Existing: map[string][]domain.Scenario{}, Limits: limits, Accounts: accounts,
	}, envName, env, nil
}

func pickAuthProfile(cfg config.Config, name string) (string, error) {
	names := slices.Sorted(maps.Keys(cfg.AuthProfiles))
	list := strings.Join(names, ", ")
	if name != "" {
		if _, ok := cfg.AuthProfiles[name]; !ok {
			return "", fmt.Errorf("auth profile %q is not in semar.yaml; known profiles: %s", name, list)
		}
		return name, nil
	}
	switch {
	case len(names) == 0:
		return "", errors.New("semar.yaml defines no auth profile; add one under authProfiles")
	case len(names) == 1:
		return names[0], nil
	case slices.Contains(names, "default"):
		return "default", nil
	}
	return "", fmt.Errorf("choose the auth profile of the scenarios with --auth-profile: %s", list)
}

// prepareSource copies the head commit for the planner, without the files that instruct an
// agent (R5) and without Semar's own configuration, which names hosts (R19). It adds the change
// table and the diff under .semar-plan, and returns the diff and the files that the copy left out.
func prepareSource(ctx context.Context, g *vcs.Git, cs domain.ChangeSet, tmp string) (string, []byte, []vcs.Skipped, error) {
	src := filepath.Join(tmp, "src")
	leftOut, err := g.Archive(ctx, cs.HeadCommit, src)
	if err != nil {
		return "", nil, nil, err
	}
	if err := removeAgentFiles(src); err != nil {
		return "", nil, nil, err
	}
	for _, name := range []string{".semar", plan.PlanDir} {
		if err := os.RemoveAll(filepath.Join(src, name)); err != nil {
			return "", nil, nil, fmt.Errorf("prepare the copy of the source: %w", err)
		}
	}
	dir := filepath.Join(src, plan.PlanDir)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", nil, nil, fmt.Errorf("prepare the copy of the source: %w", err)
	}
	var table bytes.Buffer
	if err := changeset.Table(&table, cs); err != nil {
		return "", nil, nil, err
	}
	if len(leftOut) > 0 {
		table.WriteString("\n## Files left out of the copy\n\nThese files of the head commit are not in this copy, so they cannot be read.\n\n")
		for _, s := range leftOut {
			fmt.Fprintf(&table, "- %q: %s\n", s.Path, s.Reason)
		}
	}
	diff, err := g.Diff(ctx, cs.MergeBase, cs.HeadCommit)
	if err != nil {
		return "", nil, nil, err
	}
	for name, data := range map[string][]byte{"changes.md": table.Bytes(), "change.diff": diff} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return "", nil, nil, fmt.Errorf("prepare the copy of the source: %w", err)
		}
	}
	return src, diff, leftOut, nil
}

// removeAgentFiles matches names without regard to case, because a file system that ignores
// case serves CLAUDE.md for claude.md.
func removeAgentFiles(root string) error {
	var found []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root || !slices.ContainsFunc(agentFiles, func(n string) bool { return strings.EqualFold(n, d.Name()) }) {
			return nil
		}
		found = append(found, p)
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("prepare the copy of the source: %w", err)
	}
	for _, p := range found {
		if err := os.RemoveAll(p); err != nil {
			return fmt.Errorf("prepare the copy of the source: %w", err)
		}
	}
	return nil
}

// findKnowledge finds the listed sources in the copy of the source. It runs after the copy has
// lost the files that instruct an agent, so a source that names one is not in the copy. A source
// that cannot be used is reported, and the plan goes on without it. folder is where --dir lies
// below the root of the work tree, which is the root of the copy.
func findKnowledge(stderr io.Writer, sourceDir, folder string, list []config.Knowledge) ([]knowledge.Source, error) {
	sources, err := knowledge.Resolve(sourceDir, list)
	if err != nil {
		return nil, err
	}
	for _, s := range sources {
		switch {
		case s.Problem != "":
			fmt.Fprintf(stderr, "semar: knowledge source %s: %s; the planner is not told to read it\n", terminalSafe(s.ID), terminalSafe(s.Problem))
		case s.Note != "":
			fmt.Fprintf(stderr, "semar: knowledge source %s: %s cannot be read as an API document (%s); the planner reads it as a plain document, and no request is compared with it\n",
				terminalSafe(s.ID), terminalSafe(s.Path), terminalSafe(s.Note))
		}
	}
	if len(list) == 0 {
		if found := knowledge.ConventionalBelow(sourceDir, folder); len(found) > 0 {
			paths := make([]string, len(found))
			for i, k := range found {
				paths[i] = k.Path
			}
			them := "them"
			if len(found) == 1 {
				them = "it"
			}
			fmt.Fprintf(stderr, "semar: the repository has %s; list %s under knowledge in %s so that the planner reads %s first\n",
				terminalSafe(strings.Join(paths, " and ")), them, configFileName, them)
		}
	}
	return sources, nil
}

func planFailed(ctx context.Context, stderr io.Writer, err error) error {
	var rejected *plan.Rejected
	var perr *provider.Error
	var limit *plan.CostLimitError
	switch {
	case errors.As(err, &limit):
		fmt.Fprintf(stderr, "semar: %s; nothing was written\n", limit)
		return exitError{ExitUndecided}
	case ctx.Err() != nil:
		fmt.Fprintln(stderr, "semar: the plan was interrupted, and nothing was written")
		return exitError{ExitUndecided}
	case errors.As(err, &rejected):
		fmt.Fprintf(stderr, "semar: the planner's draft was rejected %d times, and nothing was written. The problems of the last draft:\n", rejected.Attempts)
		for _, p := range rejected.Problems {
			if p.Path == "" {
				fmt.Fprintf(stderr, "  - %s\n", terminalSafe(p.Message))
			} else {
				fmt.Fprintf(stderr, "  - %s: %s\n", terminalSafe(p.Path), terminalSafe(p.Message))
			}
		}
		return exitError{ExitError}
	case errors.As(err, &perr):
		fmt.Fprintf(stderr, "semar: the planner failed (%s): %s; nothing was written\n", perr.Class, terminalSafe(perr.Detail))
		return exitError{ExitError}
	}
	return err
}

// nextRun is the run command that can reach passed: a service with no build probe needs its
// build declared, and the head of the plan is what the test environment is meant to run.
func nextRun(env config.Environment, envName, envFlag, head string) string {
	cmd := "semar run"
	// A production environment is run only when it is named.
	if envFlag != "" || env.Kind == config.KindProduction {
		cmd += " --env " + terminalSafe(envName)
	}
	var unprobed []string
	for _, s := range env.Services {
		if s.Build == nil {
			unprobed = append(unprobed, s.Name)
		}
	}
	if len(unprobed) == 1 {
		return cmd + " --build " + shortCommit(head)
	}
	for _, name := range unprobed {
		cmd += " --build " + terminalSafe(name) + "=<commit>"
	}
	return cmd
}

func shortCommit(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func printPlanDone(w io.Writer, out plan.Output, listed bool, trial, modelUse, planFile, run string) {
	count := map[string]int{}
	for _, sc := range out.Scenarios {
		count[sc.Ticket]++
	}
	for _, t := range out.Draft.Tickets {
		fmt.Fprintf(w, "%s (%s): %s\n", terminalSafe(t.Key), terminalSafe(t.Kind), plural(count[t.Key], "scenario"))
	}
	fmt.Fprintf(w, "not drafted: %d\n", len(out.Draft.NotDrafted))
	for _, d := range out.FixtureDrafts {
		if d.Drafted {
			fmt.Fprintf(w, "fixture drafted: %s. %s Not approved: semar review %s\n", terminalSafe(d.Need.ID), terminalSafe(plan.FixtureRunWords(d)), terminalSafe(d.Need.ID))
			continue
		}
		why := d.Reason
		if why == "" && len(d.Problems) > 0 {
			why = d.Problems[0].Message
			if d.Problems[0].Path != "" {
				why = d.Problems[0].Path + ": " + why
			}
		}
		fmt.Fprintf(w, "fixture not drafted: %s: %s\n", terminalSafe(d.Need.ID), terminalSafe(oneLine(why)))
	}
	if st := out.Pipeline; st != nil && st.NotPlanned > 0 {
		fmt.Fprintf(w, "criteria not planned: the cost limit of %s was reached: %d\n", plan.Dollars(maxCostOf(out)), st.NotPlanned)
	}
	if st := out.Pipeline; st != nil && st.Unanswered > 0 {
		fmt.Fprintf(w, "criteria that no model answered: %d, listed as gaps in the plan\n", st.Unanswered)
	}
	if listed {
		fmt.Fprintf(w, "differs from the documents: %d\n", len(out.Notes)+len(out.Draft.Differences))
	}
	fmt.Fprintf(w, "plan: %s\n", planFile)
	if trial != "" {
		fmt.Fprintln(w, trial)
	}
	if modelUse != "" {
		fmt.Fprintln(w, modelUse)
	}
	if len(out.Scenarios) > 0 {
		fmt.Fprintf(w, "next:\n  semar review %s   read what the plan drafted, a fixture before the scenarios that use it, and approve it\n", terminalSafe(firstToReview(out)))
		fmt.Fprintf(w, "  %s   run the approved scenarios against the service\n  semar status   where things stand\n", run)
	}
}

// costLimitNote tells the person at the start what the limit does, and which provider it cannot bind
// because it reports no cost. It is empty when there is no limit.
func costLimitNote(limit float64, providers []config.Provider) string {
	if limit <= 0 {
		return ""
	}
	var blind []string
	seen := map[string]bool{}
	for _, p := range providers {
		if seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		if !provider.ReportsCost(p.Kind) {
			blind = append(blind, fmt.Sprintf("%s (%s)", terminalSafe(p.ID), terminalSafe(p.Kind)))
		}
	}
	switch {
	case len(blind) == 0:
		return fmt.Sprintf("semar: no new job starts once the providers have reported %s", plan.Dollars(limit))
	case len(blind) == len(seen):
		return fmt.Sprintf("semar: the cost limit of %s binds nothing in this plan: %s report no cost", plan.Dollars(limit), strings.Join(blind, ", "))
	}
	return fmt.Sprintf("semar: no new job starts once the providers have reported %s; it does not bind %s, which reports no cost", plan.Dollars(limit), strings.Join(blind, ", "))
}

// stoppedWords says what the cost limit left undone, for the line that follows a plan that it cut.
func stoppedWords(out plan.Output) string {
	switch st := out.Pipeline; {
	case st == nil:
		return "the plan stopped before the next attempt, and the draft that passed was written"
	case st.NotPlanned == 1:
		return "1 criterion was not planned, and plan.md lists it as not planned"
	case st.NotPlanned > 1:
		return fmt.Sprintf("%d criteria were not planned, and plan.md lists each as not planned", st.NotPlanned)
	}
	return "a fixture was not drafted, or a criterion was not planned again, as plan.md says"
}

// maxCostOf is the limit that a plan ran under, read back from its not drafted entries.
func maxCostOf(out plan.Output) float64 { return out.MaxCost }

// firstToReview is what a person reads first: a drafted fixture, then the first scenario.
func firstToReview(out plan.Output) string {
	for _, d := range out.FixtureDrafts {
		if d.Drafted {
			return d.Need.ID
		}
	}
	return string(out.Scenarios[0].ID)
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

const (
	sourceCopyPrefix = "semar-plan-"
	// Three drafts at the longest plan.timeout take 72 hours, so a copy that a running plan
	// uses is never this old.
	sourceCopyMaxAge = 7 * 24 * time.Hour
)

// sourceCopyRoot is the directory that holds the copy of the source. A harness profile may deny
// the temporary directory, so the default is the user cache directory. The system does not clean
// that directory, so a copy that a crash left is removed here once it is 7 days old. A root that a
// caller sets is used as it is and is never cleaned.
func sourceCopyRoot(o options) (string, error) {
	if o.TempRoot != "" {
		return o.TempRoot, nil
	}
	cacheDir := o.CacheDir
	if cacheDir == nil {
		cacheDir = os.UserCacheDir
	}
	cache, err := cacheDir()
	if err != nil || cache == "" {
		return "", nil
	}
	root := filepath.Join(cache, "semar", "work")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	now := time.Now
	if o.Clock != nil {
		now = o.Clock
	}
	removeOldCopies(root, now().Add(-sourceCopyMaxAge))
	return root, nil
}

// removeOldCopies ignores every error: a copy that stays is only disk space. RemoveAll on a link
// removes the link and not its target.
func removeOldCopies(root string, before time.Time) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), sourceCopyPrefix) {
			continue
		}
		p := filepath.Join(root, e.Name())
		if fi, err := os.Lstat(p); err == nil && fi.ModTime().Before(before) {
			_ = os.RemoveAll(p)
		}
	}
}
