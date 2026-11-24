// Package apimcp serves the API tools of semar mcp: it lists the services and the operations of a
// repository and sends one HTTP request at a time through the engine of the API, which applies the
// effects policy and records the action. It keeps a log of the requests that went out, with their
// templates, captures, and checks, so that a journey can be written from them.
//
// The package never imports internal/cli. The command wires it with an Options value.
package apimcp

import (
	"context"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/effects"
	"github.com/jancuk/semar/internal/engine/api"
	"github.com/jancuk/semar/internal/mcpkit"
	"github.com/jancuk/semar/internal/openapi"
	"github.com/jancuk/semar/internal/takeover"
)

// Limits of the tools.
const (
	// MaxBody is the most bytes of a response body that a result may hold.
	MaxBody = 256 << 10
	// DefaultBody is the size of the body that a result holds when maxBody is not given.
	DefaultBody = 32 << 10
	// MaxWhy is the most characters of why.
	MaxWhy = 200
	// MaxOperations is the most operations that api_list_operations gives.
	MaxOperations = 200
	// DefaultOperations is the number of operations given when limit is not.
	DefaultOperations = 50
	// MinLift is the shortest value that is lifted back to a variable.
	MinLift = 4
)

// APIStep is one request that was sent, as a journey leg would write it. Request is the template as the
// model gave it, with literals lifted to {{name}} variables, never the rendered text.
type APIStep struct {
	Service      string
	Request      domain.APIRequest
	Captures     []domain.Capture
	Expect       []domain.Expectation // By: model, Source: baseline
	As           string               // the auth profile, by name
	Status       int                  // the status that came
	Write        bool                 // the request is not a read
	Action       string               // why, as the model wrote it
	PathTemplate string               // the path with its variables, as logged
}

// Log is what a journey reads. DropLast removes the last step and the variables it captured.
type Log interface {
	Seq() int
	Since(i int) []APIStep
	DropLast() bool
}

// ServiceInfo is what api_list_services says about a service. It never holds a token or a secret.
type ServiceInfo struct {
	Name    string `json:"name"`
	BaseURL string `json:"baseUrl"`
	// Hosts are the hosts that the environment lists.
	Hosts []string `json:"hosts"`
	// Profiles are the names of the auth profiles that the repository defines.
	Profiles []string `json:"authProfiles"`
	// DefaultProfile is the profile that fixtures use for the service. A request never uses it by itself.
	DefaultProfile string `json:"defaultProfile,omitempty"`
	// OpenAPI is the number of operations that the documents of the service describe, 0 when there is none.
	OpenAPI int `json:"openApiOperations"`
	// Mode is the effects mode of the environment, test_data or read_only.
	Mode string `json:"policyMode"`
	Kind string `json:"environmentKind,omitempty"`
	Deny int    `json:"denyRules"`
	Once int    `json:"onceRules"`
}

// OperationInfo is one operation that api_list_operations gives.
type OperationInfo struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	OperationID string `json:"operationId,omitempty"`
	Summary     string `json:"summary,omitempty"`
	// Class is read or write, as the effects policy judges the method and the path.
	Class string `json:"class"`
	// Refused is why the effects policy would refuse the operation, or "".
	Refused string `json:"refused,omitempty"`
}

// RequestBackend sends one request. *api.Engine is one.
type RequestBackend interface {
	Do(ctx context.Context, c api.Call) (api.Result, error)
}

// Options is what a Manager needs from the command.
type Options struct {
	// Guard refuses a write while the server only observes.
	Guard mcpkit.Guard
	// Scrub removes secrets from every text that goes back to the model. Nil scrubs nothing.
	Scrub *mcpkit.Scrubber
	// Baton is set with --studio. A write is then refused while a person is in control.
	Baton *takeover.Baton
	// Policy is the effects policy of the environment.
	Policy *effects.Policy
	// Services are the services of the environment, in order.
	Services []ServiceInfo
	// Docs are the API documents of each service. A document under "" applies to every service.
	Docs map[string][]openapi.Document
	// Backend makes the engine when the first request needs it. It may be called many times and must
	// give the same engine. It is not called for a request that is refused before it is sent.
	Backend func(ctx context.Context) (RequestBackend, error)
	// RunID gives the id of the run of the process, which {{run.id}} stands for. Nil, or an error,
	// leaves the variable unset. It is called only for a request that holds {{run.id}}.
	RunID func(ctx context.Context) (string, error)
	// Profiles are the names of the auth profiles that `as` may name.
	Profiles []string
	// By is who the actions are recorded as. The default is the agent.
	By domain.Actor
}
