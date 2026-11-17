// Package provider is the contract between a stage and the agent harness that
// runs one job, and the helpers that every driver shares.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/config"
)

type FailureClass string

const (
	UsageLimit      FailureClass = "usage_limit"
	ProviderError   FailureClass = "provider_error"
	TransportError  FailureClass = "transport_error"
	PermissionError FailureClass = "permission_error"
	ValidationError FailureClass = "validation_error"
	NotInstalled    FailureClass = "not_installed"
	Unknown         FailureClass = "unknown"
)

// Error is the failure of a job. Detail never holds the prompt or the output of the model.
type Error struct {
	Class  FailureClass
	Detail string
}

func (e *Error) Error() string { return string(e.Class) + ": " + e.Detail }

type Health struct {
	Installed bool
	Version   string
	Detail    string
}

type Job struct {
	Prompt    string
	Workdir   string          // the only directory the harness may read
	Schema    json.RawMessage // the JSON Schema of the output
	Model     string          // "" means: pass no model flag
	Reasoning string          // "" means: the provider's own default level
	MaxTurns  int
	Timeout   time.Duration
	Progress  func(Progress) // nil for no reports
	// SchemaInPrompt asks a harness to give the schema to the model as text, and to check the output
	// afterwards, instead of enforcing it while the model writes. Codex enforces a schema through
	// strict structured output, which narrowed the choices of a small model on a large schema until it
	// answered "not drafted" to every question. A harness that cannot do this ignores it.
	SchemaInPrompt bool
	// NoTools gives the job no tool at all: it cannot read a file, search, or run a command, and
	// answers from its prompt. Workdir still names a directory, which the job never reads.
	NoTools bool
}

// Progress is what a running job has done so far. A provider reports it when it can, with
// the totals so far, one call at a time, and never after Run returns.
type Progress struct {
	Turns     int    // responses of the model so far
	ToolCalls int    // tool calls so far
	Tool      string // the name of the latest tool call, "" when there is none
}

// Result is what one job returned. InputTokens counts every input token, and the two cache
// fields say how many of them were read from or written to the provider's cache.
type Result struct {
	Output           json.RawMessage // one JSON object
	Model            string
	Reasoning        string // the level that was asked for; "" for the default
	Turns            int
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	CostUSD          float64   // 0 when the provider reports no cost
	Tools            []ToolUse // calls by tool name, sorted by name; the file tools included
	Denied           int       // tool calls that were refused
	Limits           []Limit   // subscription windows, when the provider reports them
	Loaded           Loaded    // what of the person's setup the job could use
}

// ToolUse counts the calls of one tool in a job.
type ToolUse struct {
	Name  string `json:"name"`
	Calls int    `json:"calls"`
}

// Limit is one usage window of a subscription, as the provider reported it after a job.
type Limit struct {
	Window      string    `json:"window"`      // for example "five_hour" or "seven_day"
	Utilization float64   `json:"utilization"` // 0 to 1
	ResetsAt    time.Time `json:"resetsAt"`
	Status      string    `json:"status"` // for example "allowed"
}

// Loaded names what a harness loaded from the person's setup for a job.
type Loaded struct {
	MCPServers     []string `json:"mcpServers,omitempty"` // sorted names
	Skills         int      `json:"skills,omitempty"`
	PermissionMode string   `json:"permissionMode,omitempty"`
}

type Instance interface {
	ID() string
	Kind() string
	Health(ctx context.Context) Health                // no model turn (H6)
	Run(ctx context.Context, job Job) (Result, error) // a failure is a *Error
}

// Account is implemented by an Instance whose sign-in another program owns, or whose key
// can be checked. No method starts a model turn, and no value holds an e-mail address, a
// key, or a token.
type Account interface {
	Status(ctx context.Context) AccountStatus
	// LoginCommand is the command a person runs to sign in. It is nil for a kind with a key.
	LoginCommand() []string
}

type AccountStatus struct {
	SignedIn bool   `json:"signedIn"`
	Method   string `json:"method,omitempty"` // for example "subscription" or "api key"
	Plan     string `json:"plan,omitempty"`   // for example "max"; "" when unknown
	Detail   string `json:"detail,omitempty"` // one line for a person
}

// ModelLister is implemented by an Instance that can name the models a person may choose.
// It starts no model turn.
type ModelLister interface {
	Models(ctx context.Context) ([]Model, error)
}

type Model struct {
	ID               string   `json:"id"`                         // what goes into the provider's model field
	Label            string   `json:"label,omitempty"`            // a name for a person
	Description      string   `json:"description,omitempty"`      // one sentence about the model, from the provider
	Reasoning        []string `json:"reasoning,omitempty"`        // the levels this model accepts, weakest first
	DefaultReasoning string   `json:"defaultReasoning,omitempty"` // the level the provider uses when none is set
}

// JobReasoning is the level of a job: the job's own, else the provider's.
func JobReasoning(job Job, cfg config.Provider) string {
	if job.Reasoning != "" {
		return job.Reasoning
	}
	return cfg.Reasoning
}

// RefuseReasoning is the error for a level that a kind cannot pass on. It lists the levels
// the kind knows.
func RefuseReasoning(kind, level string, known []string) error {
	return fmt.Errorf("the %s kind does not know the reasoning level %q; the levels are %s", kind, level, strings.Join(known, ", "))
}

// The kinds of provider. A harness kind starts a program that the person installed and
// signed in. An API kind calls a model API with a key.
const (
	KindClaude    = "claude"
	KindCodex     = "codex"
	KindAnthropic = "anthropic"
	KindOpenAI    = "openai"
	KindFake      = "fake"
)

// ReportsCost says whether a run of the kind reports what it cost. Claude Code does. Codex and the API
// kinds report tokens only, so a limit on money cannot bind them.
func ReportsCost(kind string) bool { return kind != KindCodex && !IsAPI(kind) }

func IsHarness(kind string) bool { return kind == KindClaude || kind == KindCodex }
func IsAPI(kind string) bool     { return kind == KindAnthropic || kind == KindOpenAI }

type Factory func(cfg config.Provider) (Instance, error)

type Registry struct {
	factories map[string]Factory
}

func NewRegistry() *Registry { return &Registry{factories: map[string]Factory{}} }

func (r *Registry) Register(kind string, f Factory) { r.factories[kind] = f }

func (r *Registry) New(cfg config.Provider) (Instance, error) {
	f, ok := r.factories[cfg.Kind]
	if !ok {
		kinds := make([]string, 0, len(r.factories))
		for k := range r.factories {
			kinds = append(kinds, k)
		}
		slices.Sort(kinds)
		return nil, fmt.Errorf("provider %q has the unknown kind %q, known kinds: %s", cfg.ID, cfg.Kind, strings.Join(kinds, ", "))
	}
	return f(cfg)
}
