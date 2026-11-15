package openai

import (
	"slices"

	"github.com/jancuk/semar/internal/provider"
)

// Levels are the values that reasoning_effort of chat completions takes, weakest first. A model
// takes some of them.
var Levels = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

type effortSpec struct {
	levels       []string
	defaultLevel string // "" when the model page names none
}

var (
	lowToMax  = []string{"low", "medium", "high", "xhigh", "max"}
	noneToMax = []string{"none", "low", "medium", "high", "xhigh", "max"}
)

// efforts are the models whose page names their levels. A model that is not here may take a
// level, and the documentation does not say which, so its list stays empty.
var efforts = map[string]effortSpec{
	"gpt-6-astra":   {lowToMax, ""},
	"gpt-6.1-sol":   {lowToMax, "medium"},
	"gpt-6.1-terra": {noneToMax, "medium"},
	"gpt-6-luna":    {noneToMax, "medium"},
}

func withReasoning(m provider.Model) provider.Model {
	if spec, ok := efforts[m.ID]; ok {
		m.Reasoning = slices.Clone(spec.levels)
		m.DefaultReasoning = spec.defaultLevel
	}
	return m
}
