package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/usage"
	"github.com/jancuk/semar/internal/userconfig"
)

const (
	listTimeout  = 10 * time.Second
	usedDays     = 90
	maxUsedNames = 10
)

type modelsBody struct {
	Models []provider.Model `json:"models"`
	Used   []string         `json:"used"`
	Error  string           `json:"error,omitempty"`
}

// getModels names the models to choose from. A lister that fails is not a failed request: the
// page shows the sentence and keeps its text field.
func (s *Server) getModels(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	f, _, err := userconfig.Load(s.d.Home)
	if err != nil {
		fail(w, http.StatusConflict, "The providers file has an error: "+scrub(err.Error()))
		return
	}
	cfg, found := f.Find(id)
	if !found {
		fail(w, http.StatusNotFound, fmt.Sprintf("There is no provider with the ID %s.", id))
		return
	}
	body := modelsBody{Models: []provider.Model{}, Used: []string{}}
	inst, err := s.d.NewInstance(cfg)
	switch {
	case err != nil:
		body.Error = sentence("This provider cannot list its models: " + err.Error())
	default:
		if lister, ok := inst.(provider.ModelLister); ok {
			ctx, cancel := context.WithTimeout(r.Context(), s.listTimeout)
			models, err := lister.Models(ctx)
			cancel()
			if err != nil {
				body.Error = sentence("The models could not be listed: " + err.Error())
			} else if models != nil {
				body.Models = models
			}
		}
	}
	body.Used = s.usedModels(id, body.Models)
	reply(w, http.StatusOK, body)
}

// usedModels are the models that a provider ran in the last 90 days and the list lacks,
// newest first.
func (s *Server) usedModels(id string, listed []provider.Model) []string {
	recs, err := usage.Read(s.d.Home, s.d.Now().AddDate(0, 0, -usedDays))
	if err != nil {
		return []string{}
	}
	slices.SortStableFunc(recs, func(a, b usage.Record) int { return b.At.Compare(a.At) })
	known := map[string]bool{}
	for _, m := range listed {
		known[m.ID] = true
	}
	out := []string{}
	for _, rec := range recs {
		if rec.Provider != id || rec.Model == "" || known[rec.Model] {
			continue
		}
		known[rec.Model] = true
		out = append(out, rec.Model)
		if len(out) == maxUsedNames {
			break
		}
	}
	return out
}

// sentence cleans the text of an error and ends it as one sentence.
func sentence(msg string) string {
	msg = strings.Join(strings.Fields(scrub(provider.Truncate(provider.Redact(msg)))), " ")
	if !strings.HasSuffix(msg, ".") {
		msg += "."
	}
	return msg
}
