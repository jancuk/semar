package anthropic

import (
	"regexp"
	"slices"

	"github.com/jancuk/semar/internal/provider"
)

// Levels are the values of output_config.effort, weakest first. The API answers 400 for a level
// that the model does not take, so a model's own list is a hint for the person and not a check.
var Levels = []string{"low", "medium", "high", "xhigh", "max"}

type effortSpec struct {
	levels       []string
	defaultLevel string
}

var (
	lowToHigh = Levels[:3]
	noXHigh   = []string{"low", "medium", "high", "max"}
)

// efforts are the models that take effort, from the effort page of the Messages API. The levels
// that a model lacks are xhigh (the 4.6 models and the Mythos preview) and max (Opus 4.5). The
// API default is high except on Opus 5.5 and Haiku 5.5, where it is medium.
var efforts = map[string]effortSpec{
	"claude-fable-5-2":      {Levels, "high"},
	"claude-mythos-5-2":     {Levels, "high"},
	"claude-fable-5-1":      {Levels, "high"},
	"claude-mythos-5-1":     {Levels, "high"},
	"claude-fable-5":        {Levels, "high"},
	"claude-mythos-5":       {Levels, "high"},
	"claude-mythos-preview": {noXHigh, "high"},
	"claude-opus-5-5":       {Levels, "medium"},
	"claude-opus-5":         {Levels, "high"},
	"claude-sonnet-5-5":     {Levels, "high"},
	"claude-sonnet-5":       {Levels, "high"},
	"claude-haiku-5-5":      {Levels, "medium"},
	"claude-opus-4-8":       {Levels, "high"},
	"claude-opus-4-7":       {Levels, "high"},
	"claude-opus-4-6":       {noXHigh, "high"},
	"claude-sonnet-4-6":     {noXHigh, "high"},
	"claude-opus-4-5":       {lowToHigh, "high"},
}

var datedSuffix = regexp.MustCompile(`-[0-9]{8}$`)

// withEffort fills the levels of a model that the documentation lists as taking effort.
func withEffort(m provider.Model) provider.Model {
	if spec, ok := efforts[datedSuffix.ReplaceAllString(m.ID, "")]; ok {
		m.Reasoning = slices.Clone(spec.levels)
		m.DefaultReasoning = spec.defaultLevel
	}
	return m
}
