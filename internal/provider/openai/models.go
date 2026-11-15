package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/agentloop/httpapi"
)

const maxModels = 300

var _ provider.ModelLister = (*Driver)(nil)

// Models asks the server for its model list with the same client as a job, so a redirect is
// refused and the key goes to the configured host only. A server on a loopback address may
// need no key.
func (d *Driver) Models(ctx context.Context) ([]provider.Model, error) {
	key, perr := d.endpoint.Key(d.resolve)
	if perr != nil {
		return nil, errors.New(perr.Detail)
	}
	header := http.Header{}
	if key != "" {
		header.Set("Authorization", "Bearer "+key)
	}
	caller := &httpapi.Caller{HTTP: d.http, Header: header, Secret: key, DefaultWait: d.retryWait}
	data, err := caller.Get(ctx, d.endpoint.URL("/models"))
	if err != nil {
		var pe *provider.Error
		if errors.As(err, &pe) {
			return nil, errors.New(pe.Detail)
		}
		return nil, err
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &body) != nil {
		return nil, errors.New("the server answered with a model list that cannot be read")
	}
	out := make([]provider.Model, 0, len(body.Data))
	for _, m := range body.Data {
		if config.CheckModel(m.ID) == nil {
			model := provider.Model{ID: m.ID}
			if d.endpoint.Base.Host == vendorHost {
				model = withReasoning(model)
			}
			out = append(out, model)
		}
	}
	slices.SortFunc(out, func(a, b provider.Model) int { return strings.Compare(a.ID, b.ID) })
	if len(out) > maxModels {
		out = out[:maxModels]
	}
	return out, nil
}
