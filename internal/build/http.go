// Package build asks a service which commit it runs.
package build

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/theory/jsonpath"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
)

const (
	defaultTimeout = 10 * time.Second
	maxBody        = 64 << 10
	maxQuoted      = 200
)

type Prober interface {
	Probe(ctx context.Context, service string) (domain.BuildRef, error)
}

// HTTP probes with one GET. It does not use the API engine, because a probe is not a test action.
type HTTP struct {
	services map[string]config.Service
	client   *http.Client
}

func NewHTTP(services []config.Service, client *http.Client) *HTTP {
	if client == nil {
		client = &http.Client{}
	}
	m := make(map[string]config.Service, len(services))
	for _, s := range services {
		m[s.Name] = s
	}
	return &HTTP{services: m, client: client}
}

func (h *HTTP) Probe(ctx context.Context, service string) (domain.BuildRef, error) {
	svc, ok := h.services[service]
	if !ok {
		return domain.BuildRef{}, fmt.Errorf("build probe for service %q: the service is not configured", service)
	}
	if svc.Build == nil {
		return domain.BuildRef{}, fmt.Errorf("build probe for service %q: the service has no build probe configured", service)
	}
	if svc.Build.Probe != "http" {
		return domain.BuildRef{}, fmt.Errorf("build probe for service %q: probe type %q is not supported, want http", service, svc.Build.Probe)
	}
	path, err := jsonpath.Parse(svc.Build.Commit)
	if err != nil {
		return domain.BuildRef{}, fmt.Errorf("build probe for service %q: commit path %q is not valid JSONPath: %w", service, svc.Build.Commit, err)
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, svc.Build.URL, nil)
	if err != nil {
		return domain.BuildRef{}, fmt.Errorf("build probe for service %q: build the request: %w", service, hideURL(err))
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return domain.BuildRef{}, fmt.Errorf("build probe for service %q: %w", service, hideURL(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return domain.BuildRef{}, fmt.Errorf("build probe for service %q: read the response: %w", service, hideURL(err))
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return domain.BuildRef{}, fmt.Errorf("build probe for service %q: status %d, body %s", service, resp.StatusCode, quote(body))
	}
	if len(body) > maxBody {
		return domain.BuildRef{}, fmt.Errorf("build probe for service %q: the response is larger than %d bytes", service, maxBody)
	}
	var doc any
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return domain.BuildRef{}, fmt.Errorf("build probe for service %q: the response is not JSON (%v), body %s", service, err, quote(body))
	}
	nodes := path.Select(doc)
	if len(nodes) != 1 {
		return domain.BuildRef{}, fmt.Errorf("build probe for service %q: %s selects %d values, want exactly one", service, svc.Build.Commit, len(nodes))
	}
	var commit string
	switch v := nodes[0].(type) {
	case string:
		commit = v
	case json.Number:
		commit = v.String()
	default:
		return domain.BuildRef{}, fmt.Errorf("build probe for service %q: %s is not a string or a number", service, svc.Build.Commit)
	}
	if commit == "" {
		return domain.BuildRef{}, fmt.Errorf("build probe for service %q: %s is empty", service, svc.Build.Commit)
	}
	return domain.BuildRef{Service: service, Commit: commit}, nil
}

// hideURL drops the URL from a transport error, because it may carry a token in its query.
func hideURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func quote(body []byte) string {
	s := string(body)
	if r := []rune(s); len(r) > maxQuoted {
		s = string(r[:maxQuoted]) + "..."
	}
	return fmt.Sprintf("%q", s)
}
