package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/anthropic"
	"github.com/jancuk/semar/internal/provider/openai"
	"github.com/jancuk/semar/internal/userconfig"
)

// Why a provider plans.
const (
	reasonFlag    = "--provider"
	reasonYours   = "your file"
	reasonRepo    = "semar.yaml"
	reasonBuiltIn = "built-in"
)

// providerSources is every place that defines a provider: the person's own file, the
// configuration of the repository, and the built-in claude.
type providerSources struct {
	file       userconfig.File
	fileExists bool
	filePath   string
	repo       config.Config
	repoPath   string
}

// loadSources reads the person's file, which is an error when it is invalid and never skipped,
// and takes repo, the configuration of the repository, which may be empty.
func loadSources(o options, dir string, repo config.Config) (providerSources, error) {
	home, err := o.home()
	if err != nil {
		return providerSources{}, err
	}
	f, exists, err := userconfig.Load(home)
	if err != nil {
		return providerSources{}, err
	}
	return providerSources{file: f, fileExists: exists, filePath: filepath.Join(home, userconfig.FileName), repo: repo, repoPath: configPath(dir)}, nil
}

// find looks an ID up in the person's file first, so that a repository cannot replace the
// definition of a provider that the person has, then in semar.yaml, then in the built-in list.
// A provider of semar.yaml gets the default setup, because it can set no address, key, or
// switch of its own.
func (s providerSources) find(id string) (config.Provider, string, error) {
	if s.fileExists {
		if p, ok := s.file.Find(id); ok {
			return p, reasonYours, nil
		}
	}
	for _, p := range s.repo.Declared() {
		if p.ID != id {
			continue
		}
		if provider.IsAPI(p.Kind) {
			return config.Provider{}, "", fmt.Errorf("provider %q of %s calls a model API, and an address and a key are read from your own file only; define %q in %s", id, s.repoPath, id, s.filePath)
		}
		if provider.IsHarness(p.Kind) {
			p.Setup = config.Setup{MCP: true, Skills: true}
		}
		return p, reasonRepo, nil
	}
	if id == provider.KindClaude {
		p, _ := userconfig.Builtin().Find(id)
		p.User = false
		return p, reasonBuiltIn, nil
	}
	return config.Provider{}, "", s.unknown(id)
}

func (s providerSources) unknown(id string) error {
	var parts []string
	if s.fileExists {
		parts = append(parts, fmt.Sprintf("%s in %s", idList(s.file.All()), s.filePath))
	}
	if d := s.repo.Declared(); len(d) > 0 {
		parts = append(parts, fmt.Sprintf("%s in %s", idList(d), s.repoPath))
	}
	if !s.fileExists {
		parts = append(parts, "claude, built in")
	}
	return fmt.Errorf("unknown provider %q; the known providers are %s", id, strings.Join(parts, "; "))
}

func idList(ps []config.Provider) string {
	ids := make([]string, len(ps))
	for i, p := range ps {
		ids[i] = p.ID
	}
	return strings.Join(ids, ", ")
}

// choose picks the provider that plans: the flag, then the default of the person's file when it
// exists, then plan.provider of semar.yaml, then the built-in claude. The person's file comes
// before semar.yaml because semar.yaml is committed in the repository under test.
func (s providerSources) choose(flagID string) (config.Provider, string, error) {
	id, reason := reasonBuiltIn, reasonBuiltIn
	switch {
	case flagID != "":
		id, reason = flagID, reasonFlag
	case s.fileExists && s.file.Default != "":
		id, reason = s.file.Default, reasonYours
	case s.repo.PlanProviderID() != "":
		id, reason = s.repo.PlanProviderID(), reasonRepo
	default:
		id = provider.KindClaude
	}
	p, _, err := s.find(id)
	return p, reason, err
}

// planModel is the flag, then the model of the provider. A harness may have none and uses its
// own default. An API kind cannot.
func planModel(flagModel string, p config.Provider, filePath string) (string, error) {
	model := flagModel
	if model == "" {
		model = p.Model
	}
	if model == "" && provider.IsAPI(p.Kind) {
		return "", fmt.Errorf("provider %q (%s) has no model; pass --model, or set model for it in %s", p.ID, p.Kind, filePath)
	}
	return model, nil
}

// startLine says what the planner may use and, for an API kind, where the source goes.
func startLine(p config.Provider, head, model, reasoning string) string {
	var may string
	switch {
	case provider.IsHarness(p.Kind) && p.Setup.MCP && p.Setup.Skills:
		may = ", and can use your MCP servers and skills (--no-setup turns them off)"
	case provider.IsHarness(p.Kind) && p.Setup.MCP:
		may = ", and can use your MCP servers (--no-setup turns them off)"
	case provider.IsHarness(p.Kind) && p.Setup.Skills:
		may = ", and can use your skills (--no-setup turns them off)"
	case provider.IsHarness(p.Kind):
		may = ", and can only read the copy of the source"
	case provider.IsAPI(p.Kind):
		may = ", and sends the files it reads to " + terminalSafe(apiAddress(p))
	}
	with := terminalSafe(valueOr(model, "its default model"))
	if reasoning != "" {
		with += " at " + terminalSafe(reasoning) + " reasoning"
	}
	return fmt.Sprintf("semar: provider %s (%s) plans %s with %s%s; this can take several minutes",
		terminalSafe(p.ID), terminalSafe(p.Kind), terminalSafe(head), with, may)
}

func apiAddress(p config.Provider) string {
	if p.BaseURL != "" {
		return p.BaseURL
	}
	if p.Kind == provider.KindAnthropic {
		return anthropic.DefaultBase
	}
	return openai.DefaultBase
}

func newProvidersCommand(o options) *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "providers",
		Short: "List the model providers and the one that would plan here",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return listProviders(cmd.OutOrStdout(), o, dir)
		},
	}
	addDirFlag(cmd, &dir)
	return cmd
}

func listProviders(w io.Writer, o options, dir string) error {
	cfg, err := config.Load(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	s, err := loadSources(o, dir, cfg)
	if err != nil {
		return err
	}
	if s.fileExists {
		fmt.Fprintf(w, "your file: %s\n", s.filePath)
	} else {
		fmt.Fprintf(w, "your file: %s (it does not exist, so the built-in list applies)\n", s.filePath)
	}
	chosen, reason, chooseErr := s.choose("")

	type row struct {
		p      config.Provider
		source string
	}
	var rows []row
	if s.fileExists {
		for _, p := range s.file.All() {
			rows = append(rows, row{p, reasonYours})
		}
	}
	for _, p := range cfg.Declared() {
		if !slices.ContainsFunc(rows, func(r row) bool { return r.p.ID == p.ID }) {
			rows = append(rows, row{p, reasonRepo})
		}
	}
	if len(rows) == 0 || (!s.fileExists && !slices.ContainsFunc(rows, func(r row) bool { return r.p.ID == provider.KindClaude })) {
		p, _, _ := s.find(provider.KindClaude)
		rows = append(rows, row{p, reasonBuiltIn})
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "\tID\tKIND\tMODEL\tREASONING\tFROM")
	for _, r := range rows {
		mark := ""
		if chooseErr == nil && r.p.ID == chosen.ID {
			mark = "*"
		}
		model := r.p.Model
		switch {
		case model != "":
		case provider.IsAPI(r.p.Kind):
			model = "(none; set one)"
		default:
			model = "(its default)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", mark, terminalSafe(r.p.ID), terminalSafe(r.p.Kind), terminalSafe(model), terminalSafe(valueOr(r.p.Reasoning, "(its default)")), r.source)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if chooseErr != nil {
		return chooseErr
	}
	fmt.Fprintf(w, "* plans here: %s, chosen by %s\n", terminalSafe(chosen.ID), reason)
	return nil
}
