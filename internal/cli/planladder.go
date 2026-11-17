package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/stage/plan"
)

// The pipelines of semar plan.
const (
	pipelineSingle   = "single"
	pipelineCriteria = "criteria"
)

// checkPipelineFlags refuses the combinations that mean nothing. The ladder names the models, so it
// replaces --provider, --model, and the reasoning that a provider has; and the jobs of the criteria
// pipeline have no tools, so there is no setup for them to load.
func checkPipelineFlags(f planFlags) error {
	switch f.pipeline {
	case "", pipelineSingle, pipelineCriteria:
	default:
		return fmt.Errorf("--pipeline %q must be %s or %s", f.pipeline, pipelineSingle, pipelineCriteria)
	}
	criteria := f.pipeline == pipelineCriteria
	switch {
	case f.ladder != "" && !criteria:
		return errors.New("--ladder needs --pipeline criteria: the single pipeline has one job, and it runs on one model")
	case f.ladder != "" && (f.provider != "" || f.model != ""):
		return errors.New("--ladder names the provider and the model of each step; do not pass --provider or --model with it")
	case criteria && f.withSetupSet && f.withSetup:
		return errors.New("--with-setup is for the single pipeline: the jobs of the criteria pipeline have no tools, so there is nothing of your setup for them to use")
	}
	return nil
}

// ladderSteps is the ladder that was asked for: --ladder, else plan.ladder of the person's own
// file. A repository never sets it. The result is empty when neither names one.
func (s providerSources) ladderSteps(flag string) ([]string, error) {
	if flag != "" {
		var steps []string
		for _, part := range strings.Split(flag, ",") {
			if strings.TrimSpace(part) == "" {
				return nil, errors.New("--ladder has an empty step; write <provider id>:<model> steps separated by commas")
			}
			steps = append(steps, strings.TrimSpace(part))
		}
		return steps, nil
	}
	if s.fileExists {
		return s.file.Plan.Ladder, nil
	}
	return nil, nil
}

// criteriaLadder opens the providers of the ladder and returns its rungs, the cheapest first. Without
// a ladder the one rung is the provider and the model that the single pipeline would use. Every
// provider is checked to be installed before the first job. Each job runs with no tools, so no
// setup is loaded, and a provider is opened once however many steps name it.
func criteriaLadder(ctx context.Context, stderr io.Writer, o options, f planFlags, src providerSources, cfg config.Config) ([]plan.Rung, []config.Provider, []provider.Instance, error) {
	steps, err := src.ladderSteps(f.ladder)
	if err != nil {
		return nil, nil, nil, err
	}
	type step struct{ id, model string }
	var list []step
	for _, text := range steps {
		id, model, err := config.ParseRung(text)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("--ladder: %w", err)
		}
		list = append(list, step{id, model})
	}
	if len(list) == 0 {
		pc, _, err := src.choose(f.provider)
		if err != nil {
			return nil, nil, nil, err
		}
		model, err := planModel(f.model, pc, src.filePath)
		if err != nil {
			return nil, nil, nil, err
		}
		list = []step{{pc.ID, model}}
	}
	o.drop.set(append(secretEnvNames(cfg), keyEnvNames(src.file)...))
	opened := map[string]provider.Instance{}
	var rungs []plan.Rung
	var configs []config.Provider
	var insts []provider.Instance
	for _, st := range list {
		pc, _, err := src.find(st.id)
		if err != nil {
			return nil, nil, nil, err
		}
		pc.Setup = config.Setup{}
		if f.reasoning != "" {
			pc.Reasoning = f.reasoning
		}
		if st.model == "" {
			if st.model, err = planModel("", pc, src.filePath); err != nil {
				return nil, nil, nil, err
			}
		}
		inst, ok := opened[st.id]
		if !ok {
			if inst, err = o.Providers(f.dir).New(pc); err != nil {
				return nil, nil, nil, err
			}
			if h := inst.Health(ctx); !h.Installed {
				fmt.Fprintf(stderr, "semar: provider %s (%s) cannot plan: %s\n", inst.ID(), inst.Kind(), terminalSafe(h.Detail))
				return nil, nil, nil, exitError{ExitError}
			}
			opened[st.id] = inst
		}
		rungs = append(rungs, plan.Rung{Planner: inst, Model: st.model, Reasoning: pc.Reasoning})
		configs = append(configs, pc)
		insts = append(insts, inst)
	}
	return rungs, configs, insts, nil
}

// criteriaStartLine says which models answer a criterion, and where the source goes when a model
// is an API: the jobs carry the diff of the tickets' files and the operations of the API document.
func criteriaStartLine(rungs []plan.Rung, configs []config.Provider, head string) string {
	var names []string
	for i, r := range rungs {
		name := fmt.Sprintf("%s (%s) %s", r.Planner.ID(), r.Planner.Kind(), terminalSafe(valueOr(r.Model, "its default model")))
		if provider.IsAPI(configs[i].Kind) {
			name += " via " + terminalSafe(apiAddress(configs[i]))
		}
		names = append(names, name)
	}
	how := "plans each acceptance criterion of " + terminalSafe(head) + " on " + names[0]
	if len(names) > 1 {
		how += ", and when its answer fails the checks twice, on " + strings.Join(names[1:], ", then ")
	}
	return "semar: " + how + "; the models get no tools, only the text of the criterion and what Semar found for it; this can take a few minutes"
}

// answersFile is the file of a plan directory that holds the answers of its jobs.
const answersFile = "answers.json"

// maxAnswerFiles and maxAnswerBytes bound what an earlier plan hands to the next one.
const (
	maxAnswerFiles = 20
	maxAnswerBytes = 8 << 20
)

// loadAnswers reads the stored answers of the earlier plans of the repository, the newest plan
// first. A file that cannot be read is skipped: an answer is only a saving, and every answer that is
// reused is checked again.
func loadAnswers(dir string) []plan.StoredAnswer {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil
	}
	defer root.Close()
	plansDir := filepath.Join(".semar", "plans")
	entries, err := fs.ReadDir(root.FS(), filepath.ToSlash(plansDir))
	if err != nil {
		return nil
	}
	type dated struct {
		name string
		at   int64
	}
	var dirs []dated
	for _, e := range entries {
		if info, err := e.Info(); err == nil && e.IsDir() {
			dirs = append(dirs, dated{e.Name(), info.ModTime().UnixNano()})
		}
	}
	slices.SortFunc(dirs, func(a, b dated) int {
		switch {
		case a.at > b.at:
			return -1
		case a.at < b.at:
			return 1
		}
		return strings.Compare(b.name, a.name)
	})
	var out []plan.StoredAnswer
	for _, d := range dirs[:min(len(dirs), maxAnswerFiles)] {
		rel := filepath.Join(plansDir, d.name, answersFile)
		info, err := root.Lstat(rel)
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxAnswerBytes {
			continue
		}
		data, err := fs.ReadFile(root.FS(), filepath.ToSlash(rel))
		if err != nil {
			continue
		}
		var answers []plan.StoredAnswer
		if json.Unmarshal(data, &answers) == nil {
			out = append(out, answers...)
		}
	}
	return out
}
