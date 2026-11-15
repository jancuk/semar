// Package anthropic runs Semar's agent loop against the Anthropic Messages API with a key.
package anthropic

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/agentloop"
	"github.com/jancuk/semar/internal/provider/agentloop/httpapi"
)

const (
	Kind        = provider.KindAnthropic
	DefaultBase = "https://api.anthropic.com"
	apiVersion  = "2026-10-21"
	// maxTokens bounds one response. The submit call holds the whole result, so it is large.
	maxTokens = 32000
)

type Options struct {
	ResolveKey func(ref string) (string, error)
	HTTP       *http.Client
}

type Driver struct {
	cfg      config.Provider
	endpoint httpapi.Endpoint
	resolve  func(string) (string, error)
	http     *http.Client
	// retryWait replaces the wait before a retry in tests.
	retryWait time.Duration
}

func New(cfg config.Provider, o Options) (*Driver, error) {
	if o.ResolveKey == nil {
		return nil, errors.New("anthropic: no key resolver")
	}
	ep, err := httpapi.ParseEndpoint(cfg, DefaultBase)
	if err != nil {
		return nil, errors.New("provider " + cfg.ID + ": " + err.Error())
	}
	return &Driver{cfg: cfg, endpoint: ep, resolve: o.ResolveKey, http: httpapi.Client(o.HTTP)}, nil
}

func Factory(o Options) provider.Factory {
	return func(cfg config.Provider) (provider.Instance, error) {
		d, err := New(cfg, o)
		if err != nil {
			return nil, err
		}
		return d, nil
	}
}

func (d *Driver) ID() string   { return d.cfg.ID }
func (d *Driver) Kind() string { return Kind }

func (d *Driver) Health(context.Context) provider.Health { return d.endpoint.Health(d.resolve) }

func (d *Driver) Status(context.Context) provider.AccountStatus { return d.endpoint.Status(d.resolve) }

func (d *Driver) LoginCommand() []string { return nil }

func (d *Driver) Run(ctx context.Context, job provider.Job) (provider.Result, error) {
	model := job.Model
	if model == "" {
		model = d.cfg.Model
	}
	if model == "" {
		return provider.Result{}, &provider.Error{Class: provider.ValidationError, Detail: "the anthropic kind needs a model: set model in the provider or pass --model"}
	}
	if err := config.CheckModel(model); err != nil {
		return provider.Result{}, &provider.Error{Class: provider.ValidationError, Detail: err.Error()}
	}
	key, perr := d.endpoint.Key(d.resolve)
	if perr != nil {
		return provider.Result{}, perr
	}
	header := http.Header{}
	header.Set("anthropic-version", apiVersion)
	if key != "" {
		header.Set("x-api-key", key)
	}
	c := &client{
		model:  model,
		url:    d.endpoint.URL("/v1/messages"),
		caller: &httpapi.Caller{HTTP: d.http, Header: header, Secret: key, DefaultWait: d.retryWait},
		// Automatic caching of the growing conversation is a newer field that other servers
		// of this format may refuse, so it is sent to the vendor's own address only.
		autoCache: d.endpoint.Base.Host == "api.anthropic.com",
	}
	return agentloop.Run(ctx, c, job)
}

type client struct {
	model     string
	url       string
	caller    *httpapi.Caller
	autoCache bool
}

func (c *client) Next(ctx context.Context, req agentloop.Request) (agentloop.Response, error) {
	body, err := httpapi.Encode(buildRequest(c.model, req, c.autoCache))
	if err != nil {
		return agentloop.Response{}, &provider.Error{Class: provider.ProviderError, Detail: "the request could not be encoded"}
	}
	data, err := c.caller.Post(ctx, c.url, body)
	if err != nil {
		return agentloop.Response{}, err
	}
	return parseResponse(data)
}
