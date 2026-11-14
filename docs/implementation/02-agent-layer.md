# Plan 02: Agent layer

> **For agentic workers:** This is a specification. Before you execute it, expand each task into steps in the format of [Plan 01](01-core-replay.md), against the code that exists by then.

* **Guide:** [implementation guide](README.md). **Spec:** TRD, [Provider layer](../TRD.md#provider-layer), [QA tool layer](../TRD.md#qa-tool-layer), [Author](../TRD.md#author), [Recording and replay](../TRD.md#recording-and-replay)
* **Needs first:** Plan 01. Spike S1.
* **Requirements:** FR-16, 36, 38 to 41, 47, 50, 146, 165, 210

**Goal.** An agent writes a script for a scenario through Semar's tools. Code validates the script and replays it with no model.

**What works when it is done.** `semar author --scenario <id>` leaves a stored script that `semar run` replays. The same tools work inside a developer's own Claude Code session through the plugin.

## Architecture

```text
semar author  ->  provider.Instance.Run  ->  harness (claude)  --stdio-->  semar mcp  --unix socket-->  semard
                                                                                                          |
                                                    api engine, recorder, effects policy, evidence  <-----+
```

* The daemon, `semard`, owns everything that lasts: sessions, the engine, the recorder, and the run. One daemon serves one repository.
* `semar mcp` is a thin MCP server over stdio. It forwards each tool call to the daemon and holds no state and no credential.
* A provider is an adapter behind the agent port (X1). The pipeline never names a harness.
* The fake provider is a real adapter. It makes scripted tool calls through a real `semar mcp` process, so every stage is tested with no model (A11).

**Ports added:** the agent provider.

## File structure

```text
internal/daemon/daemon.go          accept loop, sessions, idle shutdown
internal/daemon/protocol.go        request and response types, one JSON object per line
internal/daemon/client.go          dial, start the daemon when it is not running, call
internal/daemon/session.go         one run: engine, recorder, scenario, attempts, limits
internal/mcpserver/server.go       MCP server over stdio. Registers tools by surface
internal/mcpserver/tools_api.go    api_request, api_capture
internal/mcpserver/tools_qa.go     qa_context, qa_submit_script, qa_flag_expectation, qa_finding, qa_submit_notes
internal/mcpserver/result.go       caps and shapes every tool result (A2, A3)
internal/provider/provider.go      Instance, Job, Session, Event, Result, FailureClass
internal/provider/registry.go      Register, New
internal/provider/env.go           builds the child's environment (H2)
internal/provider/process.go       start, stream lines, stop the process group (G13, H9)
internal/provider/claude/          driver.go, args.go, parse.go, health.go, testdata/
internal/provider/fake/            fake.go: replays a list of tool calls
internal/provider/providertest/    contract.go: the cases every driver must pass (H8, X5)
internal/prompt/prompt.go          embedded files and their versions
internal/prompt/author.md          the author prompt
internal/stage/author/author.go    one job per scenario without a script
internal/cli/author.go, mcp.go, daemon.go, doctor.go
plugin/                            the Claude Code plugin: plugin.json, .mcp.json, skills/semar/SKILL.md
```

## Interfaces

```go
// internal/provider
type InstanceID string
type Role string // the job the harness is started for: "plan" | "test" | "security" | ... See Plan 06
type FailureClass string // "" | "usage_limit" | "provider_error" | "transport_error" | "permission_error" | "validation_error" | "unknown"

type Capabilities struct {
	RestrictsTools bool // the harness can run with no shell, network, or write tool
	ReportsUsage   bool
}

type Health struct {
	Installed bool
	Version   string
	SignedIn  bool
	Tested    bool   // the version is on the tested list (H7)
	Detail    string
}

type Limits struct {
	MaxTurns int
	Timeout  time.Duration
}

type MCPConfig struct {
	Command []string // for example: semar mcp --session <id> --surfaces api
	Env     map[string]string
}

type Job struct {
	Role    Role
	Prompt  string
	Workdir string
	MCP     MCPConfig
	Limits  Limits
	Model   string // "" means: pass no model flag
}

type Event struct {
	Kind string // "started" | "text" | "tool_call" | "tool_result" | "usage" | "ended"
	Tool string
	Text string // never stored as evidence (A12)
	At   time.Time
}

type Result struct {
	Class        FailureClass
	Turns        int
	InputTokens  int
	OutputTokens int
	Detail       string
}

type Session interface {
	Events() <-chan Event
	Wait() (Result, error)
	Stop() // stops the whole process group
}

type Instance interface {
	ID() InstanceID
	Capabilities() Capabilities
	Health(ctx context.Context) Health // no model turn (H6)
	Run(ctx context.Context, job Job) (Session, error)
}

type Factory func(cfg config.Provider) (Instance, error)
func NewRegistry() *Registry
func (r *Registry) Register(kind string, f Factory)
func (r *Registry) New(cfg config.Provider) (Instance, error)

// internal/daemon
type Client struct{ /* unexported */ }
func Dial(ctx context.Context, repoRoot string) (*Client, error) // starts the daemon when no socket answers
func (c *Client) OpenSession(ctx context.Context, o SessionOptions) (SessionID, error)
func (c *Client) Call(ctx context.Context, s SessionID, tool string, args json.RawMessage) (json.RawMessage, error)
func (c *Client) CloseSession(ctx context.Context, s SessionID) (SessionSummary, error)

// internal/stage/author
func Run(ctx context.Context, d Deps, scenarios []domain.Scenario) ([]Result, error)
```

## Formats

**Configuration.**

```yaml
providers:
  - id: claude
    kind: claude              # the adapter. "driver" in the TRD
    binaryPath: ""            # empty: look on PATH
    env: {}                   # a value may be { secret: "env:NAME" }
    models: []
agents:
  tester: { provider: claude }
assign:
  test: tester            # Plan 06 adds more agents, backups, and parallel sessions
limits:
  author: { maxTurns: 40, timeout: 10m, requests: 200, attempts: 3 }
```

**The daemon socket.** `~/.semar/run/<first 12 hex of SHA-256 of the repository root>.sock`, mode 0600, in a directory with mode 0700. One JSON object per line: `{"id":1,"method":"tool.call","params":{...}}` and `{"id":1,"result":{...}}` or `{"id":1,"error":{"code":"...","message":"..."}}`. Methods: `health`, `session.open`, `tool.call`, `session.close`. The daemon exits after 15 minutes with no session.

**Tools.** Every result is a JSON object. `api_request` is the only tool that reaches the test environment.

| Tool | Input | Result |
| --- | --- | --- |
| `qa_context` | none | `{scenario, steps:[{id, setup, action, hint, expect}], services:[name], authProfile, secretNames:[name], limits:{requestsLeft, attemptsLeft}}`. No secret value, no base URL credentials |
| `api_request` | `{step, service, method, path, query, headers, json, why}`. `step` is a scenario step ID, or `""` for a read that belongs to none | `{action, status, headers:{content-type, location}, body, truncated, stored}`. `body` is redacted and cut at 20,000 characters. `stored` is the path of the full action |
| `api_capture` | `{action, name, jsonpath}` | `{name, found}`. The value itself is not returned when it matches a redaction rule |
| `qa_submit_script` | `{steps:[{step, action, captures:[{name, jsonpath}]}]}` | `{accepted, problems:[{step, message}], replay:{outcome, differences:[{step, message}]}, attemptsLeft}` |
| `qa_flag_expectation` | `{step, index, problem, proposal}` | `{recorded}` |
| `qa_finding` | `{title, description, actions:[id], proposedCheck}` | `{id}` |
| `qa_submit_notes` | `{tested:[text], notTested:[text], concerns:[text]}` | `{recorded}` |

## Rules

| # | Rule | Source |
| --- | --- | --- |
| R1 | A tool call is accepted only inside an open session, and a session belongs to one run and one scenario | TRD, Process model |
| R2 | `api_request` goes through `internal/engine/api`. The effects policy, redaction, the request budget, and the recorder apply with no second code path | Product rule 1 |
| R3 | The path template of a request is the hint of the named step. With `step: ""` it is the literal path | FR-146 |
| R4 | `qa_submit_script` builds the script from the recorded actions that the agent names. An action ID that the session did not record is a problem, not a request to send | TRD, Recording and replay |
| R5 | After `script.Validate` passes, the daemon replays the script with `replay.Runner`. The replay must give the same status and the same check state for every step. Bodies may differ | TRD, Author |
| R6 | A script is stored only when it is valid and its replay matched. After 3 rejected submissions the session ends, and the scenario is `blocked` with "no script was accepted" | A4 |
| R7 | When a bound request matches `effects.once`, the replay is skipped, the script is stored, and the result is marked "not replayed" | TRD, Author |
| R8 | The agent cannot change an expected result. `qa_flag_expectation` records a proposal, and the scenario is `inconclusive` until a person confirms the correction | TRD, Author |
| R9 | The child's environment starts from an allowlist: `PATH`, `HOME`, `LANG`, `TERM`, `TMPDIR`. Variables with the prefix `ANTHROPIC_`, `OPENAI_`, `DEEPSEEK_`, `OPENROUTER_`, or `TYPESAFE_` are passed only when the provider's configuration sets them | H2 |
| R10 | The Claude driver passes the flag set that spike S1 proved, and never `--bare`. It loads only Semar's MCP configuration | H3, TRD, Claude driver |
| R11 | A job stops at `maxTurns` or `timeout`. The stage records which limit was reached | A8 |
| R12 | In an interactive session the same tools run with `mode: interactive` on the run. The report then says that the user's own agent could act outside Semar | TRD, Two ways to start the agent |
| R13 | Every tool result passes through `mcpserver/result.go`. An error result has `{error:{code, message, fix}}`, and `fix` says what to change | A3 |

## Tasks

| # | Task | Files | Tests that prove it |
| --- | --- | --- | --- |
| 1 | Provider contract and registry | `provider/provider.go`, `registry.go` | An unknown kind is an error that lists the registered kinds. A registered factory is called with its configuration |
| 2 | Child process helper | `provider/process.go`, `env.go` | A child that ignores SIGTERM is killed with its own child (H9). The environment holds no `ANTHROPIC_API_KEY` from the parent (R9). A line of 2 MiB does not block the reader |
| 3 | Daemon and client | `daemon/*` | A second client reuses the running daemon. The socket has mode 0600. A call outside a session is refused (R1). The daemon exits after the idle time, with an injected clock |
| 4 | MCP server and the API tools | `mcpserver/server.go`, `tools_api.go`, `result.go` | With the sample application: `api_request` returns a redacted body, the stored action exists, a refused request returns `error.fix`, a body over the cap sets `truncated` (A2) |
| 5 | The QA tools | `mcpserver/tools_qa.go`, `daemon/session.go` | A valid submission is accepted and stored. A step bound to a request with another method is rejected with the step's ID (R4). An unknown action ID is rejected. The fourth submission ends the session (R6) |
| 6 | Replay inside the session | `daemon/session.go` | A script whose replay gives another status is rejected with the difference (R5). A `once` operation is not replayed (R7) |
| 7 | Fake provider and the contract kit | `provider/fake`, `provider/providertest` | The kit runs against the fake: events arrive in order, `Stop` ends the child, a scripted crash gives `provider_error`, a scripted limit gives `usage_limit` |
| 8 | Claude driver | `provider/claude/*` | The kit passes on recorded output in `testdata`. `Health` reports a missing binary, an untested version, and a signed-out state, with a fake binary on `PATH`. An unknown event type is skipped (H4) |
| 9 | Author stage and the prompt | `stage/author`, `prompt/*` | With the fake provider and the sample application: the stage stores a script, and a scenario that already has a script for its hash starts no job |
| 10 | Commands and the plugin | `cli/author.go`, `mcp.go`, `daemon.go`, `doctor.go`, `plugin/` | `semar doctor` prints one line per provider with its state and calls no model. `semar mcp --surfaces api` lists exactly the API and QA tools |

## Done when

One test starts the sample application and runs `semar author --scenario SHOP-482-01` with the fake provider and a scripted session. A script is stored, and `semar run --scenario SHOP-482-01` replays it to `passed`. A second scripted session binds the wrong request to a step. It is rejected three times, and the scenario ends `blocked`.

## Review focus

1. **The harness is killed or crashes mid-session.** The session closes, the recorded actions stay, and the scenario is `blocked` with the failure class. Task 2, Task 9.
2. **Two agents call tools at once in one session.** Sequence numbers stay unique and no action is lost, under `-race`. Task 3.
3. **A response tells the agent to call another host or to submit a different script.** The host is refused by policy, and the submission is validated like any other (A6, A7). Task 4, Task 5.
4. **A stale socket file from a crashed daemon.** The client detects that nothing answers, removes the file, and starts a new daemon. Task 3.
5. **The agent submits a script that passes only because the request is malformed.** The binding check ties the request to the hint, and the report shows the step text beside the request. Task 5.

## Open points

* The exact Claude Code flag set comes from spike S1. If no flag set removes the shell, the unattended mode cannot make its promise, and this plan stops until the design changes.
* The plugin for Codex is added in Plan 06.
