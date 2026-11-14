# Plan 04: Verify

> **For agentic workers:** This is a specification. Before you execute it, expand each task into steps in the format of [Plan 01](01-core-replay.md), against the code that exists by then.

* **Guide:** [implementation guide](README.md). **Spec:** [test workflow](../WORKFLOW.md). TRD, [Gate](../TRD.md#gate), [Observer](../TRD.md#observer), [The agent's test session](../TRD.md#the-agents-test-session), [Readiness of a ticket](../TRD.md#readiness-of-a-ticket), [Defect drafts](../TRD.md#defect-drafts)
* **Needs first:** Plan 03. Spike S4.
* **Requirements:** FR-12 to 14, 23 to 25, 42, 43, 49, 77, 78, 149, 150, 152, 164, 184 to 186, 205, 218, 221, 222, 224

**Goal.** `semar verify --ticket <key>` runs the workflow from intake to the readiness line on the API.

**What works when it is done.** The first usable version of Semar: one command, one ticket, a report with outcomes, an activity log, defect drafts, and "ready for sign-off" or "not ready".

## Architecture

The workflow is a fixed list of phases. Each phase reads and extends one state value. What a phase does is fixed. Whether an optional pass runs, and how deep, comes from policy.

```text
intake -> risk -> plan -> entry check -> session (scenarios, explore) -> independent check -> triage -> report
            ^        ^          ^                      ^                        ^                         ^
         policy   planner     gate                  tester                 replay, observe            ready
```

* `internal/workflow` owns the order. A phase is a small type with one method. Plan 05 adds the security pass as one more phase, with no change to the others.
* The entry check runs before any provider is started (FR-218).
* Observability is a port (X1). When no adapter is configured, or the adapter cannot answer, the report says "logs and traces not checked" (X3).
* Policy is one typed block. This plan moves the risk and depth settings of Plan 03 into `internal/policy` and adds trust, exit criteria, and limits.

**Ports added:** observability, OTP source. **Adapters added:** `keychain` and `command` for secrets, `command` and `static` for build probes.

## File structure

```text
internal/policy/policy.go          the typed policy block and its defaults
internal/policy/presets.go         light, standard, strict
internal/workflow/workflow.go      State, Phase, Run
internal/workflow/phases.go        intake, risk, plan, entry, session, check, triage, report
internal/gate/gate.go              deployed, reverted, mixed builds, configuration hash
internal/gate/health.go            the smoke scenarios
internal/build/command.go, static.go
internal/secret/keychain.go, command.go
internal/auth/profile.go           sign-in for an API auth profile
internal/auth/otp.go               the OTP port and fixed, totp, http, command, prompt
internal/auth/session.go           sessions held in the daemon, per profile
internal/observe/observe.go        the Observer port, Findings, attribution levels
internal/observe/grafana/          Loki and Tempo through Grafana's HTTP interface
internal/observe/observetest/      the conformance kit
internal/check/schema.go           the OpenAPI invariant
internal/check/observed.go         the log and trace invariants
internal/stage/explore/explore.go  a time-boxed job with a charter
internal/ready/readiness.go        exit criteria to one line
internal/ready/defect.go           the defect draft
internal/ready/severity.go         the suggested severity
internal/report/bundle.go          one folder per scenario
internal/report/json.go            the same data as the report, as JSON
internal/cli/verify.go, doctor.go, ready.go, defect.go, bundle.go, initcmd.go
```

## Interfaces

```go
// internal/workflow
type State struct {
	Run       domain.Run
	Tickets   []ticket.Ticket
	Changes   map[string]domain.ChangeSet
	Risk      map[string]ready.Level
	Plans     map[string]domain.Plan
	Entry     gate.Result
	Results   []domain.ScenarioResult
	Findings  []domain.Finding
	Notes     []domain.Notes
	Readiness []ready.Line
}
type Phase interface {
	Name() string
	Run(ctx context.Context, s *State) error // a blocked ticket is state, not an error
}
func Run(ctx context.Context, phases []Phase, s *State) error

// internal/gate
type Result struct {
	Builds  []domain.BuildRef
	Tickets map[string]TicketGate // deployed | partly | not_deployed, with reasons
	Healthy bool
	Reason  string
}
func Check(ctx context.Context, d Deps, sets []domain.ChangeSet) (Result, error)

// internal/auth
type OTPSource interface {
	Code(ctx context.Context) (string, error)
}
type Session struct{ Header, Value string; ExpiresAt time.Time }
func SignIn(ctx context.Context, p config.AuthProfile, d Deps) (Session, error)

// internal/observe
type Level string // "trace" | "header" | "window"
type Finding struct {
	Level   Level
	Kind    string // "error_log" | "error_span"
	Service string
	Text    string // redacted
	At      time.Time
}
type Findings struct {
	Checked bool // false: the trace did not arrive, or the adapter could not answer
	Reason  string
	Items   []Finding
}
type StepWindow struct{ TraceID string; From, To time.Time; Services []string }
type Observer interface {
	Capabilities() Capabilities
	Findings(ctx context.Context, w StepWindow) (Findings, error)
}

// internal/ready. Both are pure (G9)
type Line struct {
	Ticket string
	Ready  bool
	Open   []OpenItem // kind, count, text
	Gaps   []Gap      // accepted or not
}
func Readiness(in Inputs, p policy.Exit) Line
func Defect(r domain.ScenarioResult, sc domain.Scenario, entries []domain.Activity, run domain.Run) Draft
func Severity(r domain.ScenarioResult, hunks []domain.Hunk, p policy.Risk) (level string, reason string)
```

## Formats

**Configuration.**

```yaml
environments:
  uat:
    services:
      - name: orders
        build:
          probe: http               # http | command | static
          url: https://api.uat.example.com/orders/version
          commit: "$.commit"
          config: { url: https://api.uat.example.com/orders/flags }   # optional
          samples: 3                # how often the probe is read to detect mixed builds
        openapi: api/openapi.yaml

authProfiles:
  support-agent:
    steps:
      - request: { method: POST, url: "{{orders}}/login", json: { user: "{{secret.user}}", password: "{{secret.password}}" } }
      - otp: { source: fixed, code: "123456" }
      - request: { method: POST, url: "{{orders}}/otp", json: { code: "{{otp}}" } }
    session: { header: Authorization, value: "Bearer {{last.json.token}}" }
    secrets: { user: "env:SEMAR_UAT_USER", password: "keychain:semar/uat-password" }

observe:
  kind: grafana                     # grafana | mcp | exec. Leave the block out to run with none
  required: false
  ingestWait: 30s
  url: https://grafana.uat.example.com
  token: "env:SEMAR_GRAFANA_TOKEN"

policy:
  trust:
    confirm: [diff, baseline]
    authorMayConfirm: false
  exit:
    requireRiskCovered: true
    requireCriteriaCovered: true
    requireSecurityPass: true
  health:
    tag: smoke
```

**The readiness line.** The text of the TRD, [Readiness of a ticket](../TRD.md#readiness-of-a-ticket), with one line per open item.

**The defect draft.** The fields of the TRD, [Defect drafts](../TRD.md#defect-drafts): title, build, steps, expected with its source, actual, evidence, severity, cause, and the command that reproduces it.

**`semar doctor`.** One line per model role, connection, and environment item, and a last line that says which surfaces the setup is ready for. See the sample in the [user scenarios](../USER_SCENARIOS.md#what-must-be-ready).

## Rules

| # | Rule | Source |
| --- | --- | --- |
| R1 | The entry check runs these in order and stops at the first failure: each merge is an ancestor of the deployed commit, every `behaviour` hunk's lines still exist at that commit, the probe gives one build on every sample, the environment sets `effects.mode`, every needed profile signs in, and the smoke scenarios pass | WORKFLOW, phase 5 |
| R2 | A failed entry check gives `blocked` for the affected tickets. A test asserts that no provider was started | FR-218 |
| R3 | The build and the configuration hash are read again after the session. A difference makes every result `stale` | TRD, Gate |
| R4 | A sign-in step may use `{{secret.NAME}}`, `{{otp}}`, and `{{last...}}`. The recorded action of a sign-in step stores no body | Product rule 6 |
| R5 | A fixed OTP code may be written in the configuration. Every other secret is a reference | FR-14, 201 |
| R6 | A session that the service answers with 401 is signed in again once. A second 401 gives `blocked` with "sign-in failed" | TRD, verdict rule 2 |
| R7 | The observer returns `Checked: false` when the trace does not arrive within `ingestWait` or the adapter fails. It never returns an empty list as proof of no errors | FR-149 |
| R8 | With `Checked: false`, the step's log and trace invariants are `not_evaluated`. The scenario is `inconclusive` only when `observe.required` is true. Otherwise the report says "logs and traces not checked" | FR-149 |
| R9 | A finding with the level `window` never fails a scenario | FR-24 |
| R10 | The log and trace invariants do not apply to a step whose confirmed expected result is a status from 400 to 499. The lines are listed in the report | FR-150 |
| R11 | The explore pass has a charter from the plan, a time limit, and an action limit. It produces findings and never an outcome | D11 |
| R12 | Readiness is computed by code from the results, the coverage, and the policy. An accepted gap is shown as accepted. A `failed` result cannot be accepted | FR-222, 223 |
| R13 | The steps of a defect draft come from the activity entries of the scenario, in order. No model writes them | FR-221 |
| R14 | Exit codes follow Plan 01. A ticket that is `blocked` at the entry check gives 2 | FR-186 |
| R15 | `semar doctor` calls no model, sends no write, and reads each credential's permissions. It warns when a credential allows more than Semar needs | FR-78, H6 |
| R16 | `semar init` detects what it can, writes the configuration with a preset, and lists what it could not detect as questions. It asks for `effects.mode` and never chooses it | FR-77, 140 |

## Tasks

| # | Task | Files | Tests that prove it |
| --- | --- | --- | --- |
| 1 | Policy block and presets | `policy/*` | Each preset loads and validates. An unknown key is an error with the line. The risk and depth settings of Plan 03 read from the new block |
| 2 | Secret and probe adapters | `secret/keychain.go`, `command.go`, `build/command.go`, `static.go` | The command adapters run an argument list with no shell (G13). A keychain miss is an error that names the reference and not the value |
| 3 | OTP sources | `auth/otp.go` | A fixed code, a TOTP code for a known secret and an injected clock, an HTTP lookup, a command, and a prompt that fails when no terminal is attached |
| 4 | API sign-in | `auth/profile.go`, `session.go` | Against the sample application with a login route: the session header is set, the recorded sign-in actions hold no body (R4), and a second 401 gives `blocked` (R6) |
| 5 | Gate | `gate/gate.go` | One row per check of R1: not an ancestor, a reverted hunk, two builds across three samples, and a changed configuration hash |
| 6 | Health check | `gate/health.go` | With the sample application's server-error switch, the gate is unhealthy, and the fake provider's start counter is 0 (R2) |
| 7 | Observer port and the kit | `observe/observe.go`, `observetest` | The kit: a trace with an error span, a log line with the trace ID, no trace within the wait (`Checked: false`), and an adapter error (`Checked: false`) |
| 8 | Grafana adapter | `observe/grafana` | The kit passes against a recorded fixture server. Every request is a `GET` or a query `POST` to a read endpoint |
| 9 | Observed and schema invariants | `check/observed.go`, `schema.go` | An error log fails a normal step and does not fail a step that expects 422 (R10). A `window` finding never fails (R9). A response that breaks the OpenAPI schema fails the schema invariant |
| 10 | Explore stage | `stage/explore` | With the fake provider: findings are stored with their action IDs, and the job stops at the action limit (R11) |
| 11 | Readiness, severity, and defect drafts | `ready/*` | One row per exit criterion. An accepted gap stays visible. A golden file for a defect draft, whose steps equal the activity entries (R13) |
| 12 | Workflow phases | `workflow/*` | Phases run in order. A ticket blocked in one phase skips the later phases for that ticket only. A cancelled context stops between phases |
| 13 | Commands | `cli/verify.go`, `doctor.go`, `ready.go`, `defect.go`, `bundle.go`, `initcmd.go` | The end-to-end test below. `semar doctor` against a setup with one missing piece names the setting to add |

## Done when

Against the sample application, `semar verify --ticket SHOP-482` with the fake provider and no observability adapter:

* gives `passed` and "ready for sign-off" on a correct service, after `semar confirm`.
* gives `failed`, a defect draft, and "not ready" with the refund defect switch.
* gives `blocked` and starts no provider with the server-error switch on the health check.
* says "logs and traces not checked" in every report.

The same run with a fake observer that returns an error log gives `failed` for a normal step and `passed` for the step that expects 422.

## Review focus

1. **The test environment is redeployed in the middle of a session.** Every result is `stale`, and failed checks are still listed. Task 5, Task 12.
2. **The observability system is slow or down.** The run finishes, and the report says what was not checked. It does not hang past `ingestWait` per step. Task 7.
3. **A sign-in that needs a person, on a machine with no terminal.** The prompt source fails at once with "this OTP source cannot run unattended". Task 3.
4. **A ticket with one part deployed and one not.** The deployed part is tested, the ticket is `partly` deployed, and readiness says which part is missing. Task 5, Task 11.
5. **A policy file that turns every exit criterion off.** Readiness still requires that nothing failed and nothing is undecided. Those two are not policy. Task 11.

## Open points

* Spike S4 decides whether the log and trace invariants can fail a scenario on the owner's services, or whether `observe.required` stays false there.
* The OpenAPI validator is chosen in Task 9 from the two candidates in the guide.
