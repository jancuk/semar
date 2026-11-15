package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/changeset"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/evidence"
	"github.com/jancuk/semar/internal/idcheck"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/scenario"
	"github.com/jancuk/semar/internal/stage/plan"
	"github.com/jancuk/semar/internal/vcs"
)

type planFlags struct {
	diff         diffFlags
	model        string
	maxScenarios int
	env          string
	authProfile  string
	dir          string
	init         initFlags
	initGiven    bool
}

// agentFiles are read by an agent harness as instructions or tools. They are removed from the
// copy at every depth before the planner starts (R5).
var agentFiles = []string{"CLAUDE.md", "CLAUDE.local.md", "AGENTS.md", ".mcp.json", ".claude", ".codex", ".cursor"}

func newPlanCommand(o options) *cobra.Command {
	var f planFlags
	cmd := &cobra.Command{
		Use:   "plan --diff <link or range>",
		Short: "Draft scenarios and scripts for the change between two branches",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f.initGiven = initFlagGiven(cmd)
			return planChange(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), o, f)
		},
	}
	addDiffFlags(cmd, &f.diff)
	cmd.Flags().StringVar(&f.model, "model", "", "the model of the planner (default: the provider's model)")
	cmd.Flags().IntVar(&f.maxScenarios, "max-scenarios", 0, "the most scenarios to draft (default: plan.maxScenarios)")
	cmd.Flags().StringVar(&f.env, "env", "", "the environment of semar.yaml to plan for; needed when there are several. With --base-url, the name of the new environment (default uat)")
	cmd.Flags().StringVar(&f.authProfile, "auth-profile", "", "the auth profile of the scenarios; needed when there are several and none is named default")
	addInitFlags(cmd, &f.init)
	addDirFlag(cmd, &f.dir)
	return cmd
}

func planChange(ctx context.Context, stdout, stderr io.Writer, o options, f planFlags) error {
	if f.maxScenarios < 0 {
		return errors.New("--max-scenarios must be positive")
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
		return fmt.Errorf("the configuration exists; edit %s", configPath(f.dir))
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
	in, err := planInput(cfg, f, cs)
	if err != nil {
		return err
	}
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

	pc := cfg.PlanProvider()
	inst, err := o.Providers(f.dir).New(pc)
	if err != nil {
		return err
	}
	if h := inst.Health(ctx); !h.Installed {
		fmt.Fprintf(stderr, "semar: provider %s (%s) cannot plan: %s\n", inst.ID(), inst.Kind(), terminalSafe(h.Detail))
		return exitError{ExitError}
	}

	tmp, err := os.MkdirTemp(o.TempRoot, "semar-plan-")
	if err != nil {
		return fmt.Errorf("create a directory for the copy of the source: %w", err)
	}
	defer os.RemoveAll(tmp)
	if in.SourceDir, err = prepareSource(ctx, g, cs, tmp); err != nil {
		return err
	}

	model := f.model
	if model == "" {
		model = pc.Model
	}
	in.Model = f.model
	fmt.Fprintf(stderr, "semar: provider %s (%s) plans %s with %s; this can take several minutes\n",
		inst.ID(), inst.Kind(), terminalSafe(cs.Head), terminalSafe(valueOr(model, "its default model")))

	out, err := plan.Run(ctx, inst, in)
	if err != nil {
		return planFailed(ctx, stderr, err)
	}
	planFile, err := writePlan(f.dir, in, out, known)
	if err != nil {
		return err
	}
	printPlanDone(stdout, out, planFile)
	if len(out.Scenarios) == 0 {
		return exitError{ExitUndecided}
	}
	return nil
}

func valueOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// planInput takes the names the planner may use from the environment that run would pick.
func planInput(cfg config.Config, f planFlags, cs domain.ChangeSet) (plan.Input, error) {
	_, env, err := cfg.Environment(f.env)
	if err != nil {
		return plan.Input{}, err
	}
	if len(env.Services) == 0 {
		return plan.Input{}, errors.New("the environment has no service; add one under services in semar.yaml")
	}
	var services []string
	for _, s := range env.Services {
		services = append(services, s.Name)
	}
	profile, err := pickAuthProfile(cfg, f.authProfile)
	if err != nil {
		return plan.Input{}, err
	}
	var header string
	if p := cfg.AuthProfiles[profile].Session; p != nil {
		header = p.Header
	}
	limits := cfg.Plan
	if f.maxScenarios > 0 {
		limits.MaxScenarios = f.maxScenarios
	}
	return plan.Input{
		Change: cs, Services: services, Methods: env.Methods, AuthProfile: profile, SessionHeader: header,
		Existing: map[string][]domain.Scenario{}, Limits: limits,
	}, nil
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
// table and the diff under .semar-plan.
func prepareSource(ctx context.Context, g *vcs.Git, cs domain.ChangeSet, tmp string) (string, error) {
	src := filepath.Join(tmp, "src")
	if err := g.Archive(ctx, cs.HeadCommit, src); err != nil {
		return "", err
	}
	if err := removeAgentFiles(src); err != nil {
		return "", err
	}
	for _, name := range []string{".semar", plan.PlanDir} {
		if err := os.RemoveAll(filepath.Join(src, name)); err != nil {
			return "", fmt.Errorf("prepare the copy of the source: %w", err)
		}
	}
	dir := filepath.Join(src, plan.PlanDir)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", fmt.Errorf("prepare the copy of the source: %w", err)
	}
	var table bytes.Buffer
	if err := changeset.Table(&table, cs); err != nil {
		return "", err
	}
	diff, err := g.Diff(ctx, cs.MergeBase, cs.HeadCommit)
	if err != nil {
		return "", err
	}
	for name, data := range map[string][]byte{"changes.md": table.Bytes(), "change.diff": diff} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return "", fmt.Errorf("prepare the copy of the source: %w", err)
		}
	}
	return src, nil
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

func planFailed(ctx context.Context, stderr io.Writer, err error) error {
	var rejected *plan.Rejected
	var perr *provider.Error
	switch {
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

type planFile struct {
	path string
	data []byte
}

// writePlan writes every file or none (R21). Every byte is made, and every target is checked,
// before the first write. Scenario files come before scripts, and a failure removes the
// scenario files and the plan directory that this call created.
func writePlan(dir string, in plan.Input, out plan.Output, known map[domain.ScenarioID]bool) (string, error) {
	var scenarioFiles []planFile
	var scripts []domain.Script
	if len(out.Scripts) != len(out.Scenarios) {
		return "", fmt.Errorf("the plan has %d scenarios and %d scripts", len(out.Scenarios), len(out.Scripts))
	}
	for i, sc := range out.Scenarios {
		if err := idcheck.Check("ticket", sc.Ticket); err != nil {
			return "", fmt.Errorf("scenario %s: %w", sc.ID, err)
		}
		if err := idcheck.Check("scenario ID", string(sc.ID)); err != nil {
			return "", err
		}
		if known[sc.ID] {
			return "", fmt.Errorf("scenario %s exists already; nothing was written", sc.ID)
		}
		p := filepath.Join(scenariosDir(dir), sc.Ticket, string(sc.ID)+".yaml")
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%s exists already; nothing was written", p)
		}
		data, err := scenario.Marshal(sc)
		if err != nil {
			return "", err
		}
		scenarioFiles = append(scenarioFiles, planFile{p, data})
		s := out.Scripts[i]
		s.ExpectHash = domain.ExpectHash(sc)
		s.AuthoredBy = domain.ProviderRef{Instance: out.PlannerID, Model: out.Result.Model, Harness: out.PlannerKind}
		scripts = append(scripts, s)
	}
	var md bytes.Buffer
	if err := plan.Render(&md, in, out); err != nil {
		return "", err
	}
	draft, err := atomicfile.EncodeJSON(out.Draft)
	if err != nil {
		return "", fmt.Errorf("encode the draft: %w", err)
	}
	store, err := evidence.Open(semarDir(dir))
	if err != nil {
		return "", err
	}

	var created []string
	var planDir string
	undo := func(cause error) (string, error) {
		errs := []error{cause}
		for _, p := range created {
			if err := os.Remove(p); err != nil {
				errs = append(errs, err)
			}
		}
		if planDir != "" {
			if err := os.RemoveAll(planDir); err != nil {
				errs = append(errs, err)
			}
		}
		return "", fmt.Errorf("the plan was not written, and the scenario files it had written were removed: %w", errors.Join(errs...))
	}
	for _, f := range scenarioFiles {
		if err := writeNewFile(f.path, f.data); err != nil {
			return undo(err)
		}
		created = append(created, f.path)
	}
	for _, s := range scripts {
		if err := store.PutScript(s); err != nil {
			return undo(err)
		}
	}
	if planDir, err = newPlanDir(dir, in.Change.HeadCommit); err != nil {
		return undo(err)
	}
	for _, f := range []planFile{{filepath.Join(planDir, "plan.md"), md.Bytes()}, {filepath.Join(planDir, "draft.json"), draft}} {
		if err := writeNewFile(f.path, f.data); err != nil {
			return undo(err)
		}
	}
	return filepath.Join(planDir, "plan.md"), nil
}

// writeNewFile never replaces a file. A plan is read and committed by people, so it is not private.
func writeNewFile(path string, data []byte) error {
	if err := atomicfile.WriteNew(path, data); err != nil {
		return err
	}
	return os.Chmod(path, 0o644)
}

// newPlanDir claims .semar/plans/<first 12 characters of the head>, or the first free -2, -3, ...
func newPlanDir(dir, head string) (string, error) {
	if len(head) > 12 {
		head = head[:12]
	}
	if err := idcheck.Check("commit", head); err != nil {
		return "", err
	}
	parent := filepath.Join(semarDir(dir), "plans")
	if err := os.MkdirAll(parent, atomicfile.DirMode); err != nil {
		return "", fmt.Errorf("create %s: %w", parent, err)
	}
	for n := 1; n <= 1000; n++ {
		p := filepath.Join(parent, head)
		if n > 1 {
			p = fmt.Sprintf("%s-%d", p, n)
		}
		err := os.Mkdir(p, atomicfile.DirMode)
		if err == nil {
			return p, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("create %s: %w", p, err)
		}
	}
	return "", fmt.Errorf("%s holds 1000 plans of commit %s already", parent, head)
}

func printPlanDone(w io.Writer, out plan.Output, planFile string) {
	count := map[string]int{}
	for _, sc := range out.Scenarios {
		count[sc.Ticket]++
	}
	for _, t := range out.Draft.Tickets {
		fmt.Fprintf(w, "%s (%s): %s\n", terminalSafe(t.Key), terminalSafe(t.Kind), plural(count[t.Key], "scenario"))
	}
	fmt.Fprintf(w, "not drafted: %d\n", len(out.Draft.NotDrafted))
	fmt.Fprintf(w, "plan: %s\n", planFile)
	if len(out.Scenarios) > 0 {
		fmt.Fprintln(w, "next: semar run --all, then semar confirm --all")
	}
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
