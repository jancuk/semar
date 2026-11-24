package apimcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/api"
	"github.com/jancuk/semar/internal/mcp"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/openapi"
)

func readTool(name, title, desc string, in mcp.Schema, run mcp.Handler) mcp.Tool {
	return mcp.Tool{
		Name: name, Title: title, Description: desc, Input: in,
		Annotations: mcp.Annotations{Title: title, ReadOnly: mcp.BoolPtr(true), OpenWorld: mcp.BoolPtr(false)},
		Surface:     string(mcpkit.SurfaceAPI), Write: false, Handler: run,
	}
}

func (m *Manager) textJSON(v any) (mcp.Result, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return mcp.Result{}, fmt.Errorf("the result could not be encoded: %w", err)
	}
	return mcp.TextResult(m.scrub(string(b))), nil
}

func (m *Manager) listServicesTool() mcp.Tool {
	return readTool("api_list_services", "List API services",
		"List the services of the environment: name, base URL, the hosts that the environment allows, the names of the auth profiles that api_request may use with as (never their tokens), how many operations the OpenAPI documents describe, the effects mode, and how many deny and once rules apply. Start here.",
		mcp.Object(nil),
		func(context.Context, mcp.Call) (mcp.Result, error) {
			services := m.o.Services
			if services == nil {
				services = []ServiceInfo{}
			}
			out := struct {
				Services  []ServiceInfo `json:"services"`
				Observing bool          `json:"observing"`
				Note      string        `json:"note,omitempty"`
			}{Services: services, Observing: m.o.Guard.Observe}
			if m.o.Guard.Observe {
				out.Note = "This server only observes (" + m.o.Guard.Reason + "): api_request sends reads (GET, HEAD) and refuses everything else."
			}
			return m.textJSON(out)
		})
}

func (m *Manager) listOperationsTool() mcp.Tool {
	return readTool("api_list_operations", "List API operations",
		"List the operations that the OpenAPI documents of a service describe: method, path template, operationId, summary, class (read or write, as the effects policy judges it), and refused (why the policy would refuse it, if it would). Use match to filter by words in the method, path, operationId, or summary.",
		mcp.Object(map[string]mcp.Schema{
			"service": mcp.String("The service name from api_list_services."),
			"match":   mcp.String("Keep operations whose method, path, operationId, or summary holds this text, ignoring case."),
			"limit":   mcp.Int("Most operations to give. 50 by default, 200 at most.").With("minimum", 1).With("maximum", MaxOperations),
		}).Required("service"),
		m.listOperations)
}

func (m *Manager) service(name string) (ServiceInfo, bool) {
	for _, s := range m.o.Services {
		if s.Name == name {
			return s, true
		}
	}
	return ServiceInfo{}, false
}

func (m *Manager) serviceNames() string {
	names := make([]string, 0, len(m.o.Services))
	for _, s := range m.o.Services {
		names = append(names, s.Name)
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

func (m *Manager) listOperations(_ context.Context, call mcp.Call) (mcp.Result, error) {
	var a struct {
		Service string `json:"service"`
		Match   string `json:"match"`
		Limit   int    `json:"limit"`
	}
	if err := mcp.DecodeArgs(call.Args, &a); err != nil {
		return mcp.Result{}, err
	}
	svc, ok := m.service(a.Service)
	if !ok {
		return mcp.ErrorResult(m.scrub(fmt.Sprintf("there is no service %q; the services are %s", a.Service, m.serviceNames()))), nil
	}
	limit := a.Limit
	switch {
	case limit == 0:
		limit = DefaultOperations
	case limit < 0 || limit > MaxOperations:
		return mcp.ErrorResult(fmt.Sprintf("limit must be from 1 to %d", MaxOperations)), nil
	}
	needle := strings.ToLower(strings.TrimSpace(a.Match))
	docs := append(append([]openapi.Document(nil), m.o.Docs[svc.Name]...), m.o.Docs[""]...)
	type key struct{ method, path string }
	seen := map[key]bool{}
	var ops []OperationInfo
	total := 0
	for _, d := range docs {
		for _, op := range d.Operations {
			k := key{op.Method, op.Path}
			if seen[k] {
				continue
			}
			seen[k] = true
			if needle != "" && !strings.Contains(strings.ToLower(op.Method+" "+op.Path+" "+op.ID+" "+op.Summary), needle) {
				continue
			}
			total++
			info := OperationInfo{Method: op.Method, Path: op.Path, OperationID: op.ID, Summary: op.Summary, Class: "read"}
			if u, err := api.JoinURL(svc.BaseURL, placeholderPath(op.Path), nil); err == nil && m.o.Policy != nil {
				r := effects.Request{Method: op.Method, URL: u}
				if m.o.Policy.Classify(r) == effects.Write {
					info.Class = "write"
				}
				if err := m.o.Policy.Admit(r); err != nil {
					info.Refused = err.Error()
				}
			} else if err != nil {
				info.Refused = "the path does not make a request path"
			}
			ops = append(ops, info)
		}
	}
	sort.SliceStable(ops, func(i, j int) bool {
		if ops[i].Path != ops[j].Path {
			return ops[i].Path < ops[j].Path
		}
		return ops[i].Method < ops[j].Method
	})
	more := 0
	if len(ops) > limit {
		more = len(ops) - limit
		ops = ops[:limit]
	}
	if ops == nil {
		ops = []OperationInfo{}
	}
	out := struct {
		Service    string          `json:"service"`
		Operations []OperationInfo `json:"operations"`
		More       int             `json:"more,omitempty"`
		Note       string          `json:"note,omitempty"`
	}{Service: svc.Name, Operations: ops, More: more}
	if len(docs) == 0 {
		out.Note = "the service has no OpenAPI document in the knowledge sources of semar.yaml"
	} else if total == 0 {
		out.Note = "no operation matches"
	}
	return m.textJSON(out)
}

// placeholderPath sets every {param} of an OpenAPI path to x, so that the policy sees a request path.
func placeholderPath(p string) string {
	var b strings.Builder
	depth := 0
	for _, r := range p {
		switch {
		case r == '{':
			if depth == 0 {
				b.WriteByte('x')
			}
			depth++
		case r == '}' && depth > 0:
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}
