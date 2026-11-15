package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/agentloop/httpapi"
)

const maxModels = 100

var _ provider.ModelLister = (*Driver)(nil)

// Models asks the server for its model list, with the headers of a job and the same client,
// so a redirect is refused and the key never leaves the configured host. The first page of
// 100 is enough for a choice.
func (d *Driver) Models(ctx context.Context) ([]provider.Model, error) {
	key, perr := d.endpoint.Key(d.resolve)
	if perr != nil {
		return nil, errors.New(perr.Detail)
	}
	header := http.Header{}
	header.Set("anthropic-version", apiVersion)
	if key != "" {
		header.Set("x-api-key", key)
	}
	caller := &httpapi.Caller{HTTP: d.http, Header: header, Secret: key, DefaultWait: d.retryWait}
	data, err := caller.Get(ctx, d.endpoint.URL("/v1/models?limit=100"))
	if err != nil {
		return nil, listError(err)
	}
	var body struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &body) != nil {
		return nil, errors.New("the server answered with a model list that cannot be read")
	}
	out := make([]provider.Model, 0, len(body.Data))
	for _, m := range body.Data {
		if len(out) == maxModels {
			break
		}
		if config.CheckModel(m.ID) == nil {
			out = append(out, provider.Model{ID: m.ID, Label: m.DisplayName})
		}
	}
	return out, nil
}

// listError keeps the text of a provider error, which the caller already cleaned, and the
// plain context errors.
func listError(err error) error {
	var perr *provider.Error
	if errors.As(err, &perr) {
		return errors.New(perr.Detail)
	}
	return err
}
