package cli

import (
	"github.com/jancuk/semar/internal/apicheck"
	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/knowledge"
)

// responseChecker is the check of every API response of an environment against the API documents
// that semar.yaml names and against the time limit of the environment. It is nil when there is
// nothing to check. A document that cannot be read is left out: the plan reports it.
func responseChecker(repo string, cfg config.Config, env config.Environment) *apicheck.Checker {
	var docs []apicheck.Source
	if env.Checks.Schema != apicheck.Off {
		sources, err := knowledge.Resolve(repo, cfg.Knowledge)
		if err == nil {
			for _, s := range sources {
				if s.Kind == config.KnowledgeOpenAPI && s.Usable() && s.API != nil {
					docs = append(docs, apicheck.Source{Service: s.Service, Doc: *s.API})
				}
			}
		}
	}
	return apicheck.New(apicheck.Options{
		Env:     env.Name,
		Schema:  env.Checks.Schema,
		Limit:   env.Checks.ResponseTime.Limit,
		LimitOn: env.Checks.ResponseTime.On,
		Docs:    docs,
	})
}
