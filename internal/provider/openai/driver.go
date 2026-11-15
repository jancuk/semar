// Package openai runs Semar's agent loop against an API in the OpenAI chat completions format,
// which OpenAI and other servers such as DeepSeek or a local server speak.
package openai

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/provider/agentloop"
	"github.com/jancuk/semar/internal/provider/agentloop/httpapi"
)

const (
	Kind        = provider.KindOpenAI
	DefaultBase = "https://api.openai.com/v1"
	vendorHost  = "api.openai.com"
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
		return nil, errors.New("openai: no key resolver")
	}
	ep, err := httpapi.ParseEndpoint(cfg, DefaultBase)
	if err != nil {
		return nil, errors.New("provider " + cfg.ID + ": " + err.Error())
	}
	d := &Driver{cfg: cfg, endpoint: ep, resolve: o.ResolveKey, http: httpapi.Client(o.HTTP)}
	if err := d.checkLevel(cfg.Reasoning); err != nil {
		return nil, errors.New("provider " + cfg.ID + ": " + err.Error())
	}
	return d, nil
}

// checkLevel refuses a level that OpenAI's own API does not know. Which of these a model takes
// is for the API to say. Another server of this format may take other values, so it is asked.
func (d *Driver) checkLevel(level string) error {
	if level == "" || d.endpoint.Base.Host != vendorHost || slices.Contains(Levels, level) {
		return nil
	}
	return provider.RefuseReasoning(Kind, level, Levels)
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
		return provider.Result{}, &provider.Error{Class: provider.ValidationError, Detail: "the openai kind needs a model: set model in the provider or pass --model"}
	}
	if err := config.CheckModel(model); err != nil {
		return provider.Result{}, &provider.Error{Class: provider.ValidationError, Detail: err.Error()}
	}
	level := provider.JobReasoning(job, d.cfg)
	if err := d.checkLevel(level); err != nil {
		return provider.Result{}, &provider.Error{Class: provider.ValidationError, Detail: err.Error()}
	}
	key, perr := d.endpoint.Key(d.resolve)
	if perr != nil {
		return provider.Result{}, perr
	}
	header := http.Header{}
	if key != "" {
		header.Set("Authorization", "Bearer "+key)
	}
	c := &client{
		model:  model,
		url:    d.endpoint.URL("/chat/completions"),
		caller: &httpapi.Caller{HTTP: d.http, Header: header, Secret: key, DefaultWait: d.retryWait},
		effort: level,
	}
	res, err := agentloop.Run(ctx, c, job)
	res.Reasoning = level
	return res, err
}

type client struct {
	model  string
	url    string
	caller *httpapi.Caller
	effort string
	turn   int
}

func (c *client) Next(ctx context.Context, req agentloop.Request) (agentloop.Response, error) {
	r := buildRequest(c.model, req)
	r.ReasoningEffort = c.effort
	body, err := httpapi.Encode(r)
	if err != nil {
		return agentloop.Response{}, &provider.Error{Class: provider.ProviderError, Detail: "the request could not be encoded"}
	}
	data, err := c.caller.Post(ctx, c.url, body)
	if err != nil {
		return agentloop.Response{}, err
	}
	c.turn++
	resp, err := parseResponse(data, c.turn)
	var perr *provider.Error
	if errors.As(err, &perr) && c.caller.Secret != "" {
		perr.Detail = strings.ReplaceAll(perr.Detail, c.caller.Secret, "[redacted]")
	}
	return resp, err
}
