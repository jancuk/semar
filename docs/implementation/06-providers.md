# Plan 06: Providers, agents, and assignments

> **For agentic workers:** This is a specification. Before you execute it, expand each task into steps in the format of [Plan 01](01-core-replay.md), against the code that exists by then.

* **Guide:** [implementation guide](README.md). **Spec:** TRD, [Provider layer](../TRD.md#provider-layer), [Decision models](../TRD.md#decision-models), [Agents and assignments](../TRD.md#agents-and-assignments), [Screening with a decision model](../TRD.md#screening-with-a-decision-model)
* **Needs first:** Plan 02. Spikes S2, S3, and S14.
* **Requirements:** FR-37, 206 to 212, 236 to 245

**Goal.** A company decides in YAML which model does which job: several providers, named agents with roles, backups, parallel sessions, and a fast decision model wherever a job is a choice.

**What works when it is done.** Adding a provider, defining an agent, giving a job to another agent, and running testers in parallel each need an edit to `.semar/semar.yaml` and nothing else. Semar gives the same outcomes with every assignment, including none of the optional ones.

## Architecture

Three blocks of configuration, each answering one question.

```text
providers   where a model comes from        anthropic, openai, deepseek, typesafe, ...
    |
agents      who does a job                  a name, a role, a provider and model, backups, instructions, limits
    |
assign      which agent does which job      plan, test, security, plan.review, script.check, decide, ...
```

* The job names are fixed in code, because each is a place where the workflow calls a model. Everything else is configuration: a company can define as many agents as it likes and move any job to any of them.
* Two classes of provider. An agent provider is a harness that Semar starts. A decision provider is an HTTP interface that answers fixed questions with a probability. One `providers` list holds both, and the `kind` says which class it is.
* A job falls back to a more general job when it has no agent of its own.
* An agent has ordered backups. Semar moves to the next only on a provider failure, and only to a backup that the configuration lists. It never picks a provider by itself.
* Decision-first: where a job is a choice among fixed options, Semar asks the decision provider first, because it is fast and cheap. It asks an agent only when the answer is unsure.

**Ports added:** decision provider. **Configuration added:** `agents`, `assign`, and custom decision jobs.

## File structure

```text
internal/provider/codex/           driver.go, args.go, parse.go, health.go, testdata/
internal/provider/acp/             driver.go, client.go
internal/provider/presets.go       named presets: deepseek, typesafe, openrouter-jev
internal/provider/data.go          the kinds of data a job sends, checked against the provider
internal/agents/agents.go          Agent, Route, load and validate the agents block
internal/agents/assign.go          Job, the fallback chain, "when" rules, Resolve
internal/agents/run.go             run a job on an agent: backups, limits, which one ran
internal/agents/pool.go            parallel sessions of one agent, with test accounts
internal/agents/panel.go           several agents on one job at once
internal/agents/instructions.go    read an agent's instruction file and add it to the prompt
internal/decide/decide.go          the decision port, Question, Answer
internal/decide/systemone/         client.go: the HTTP client. No SDK
internal/decide/decidetest/        the conformance kit and a fake decider
internal/decide/jobs.go            the built-in decision jobs, each with its limit and its fallback
internal/decide/custom.go          decision jobs defined in configuration
internal/stage/plan/review.go      the plan review panel
internal/stage/author/check.go     the second look at a script
internal/cli/egress.go, agentscmd.go
plugin/codex/                      the Codex plugin
```

## Interfaces

```go
// internal/agents
type Route struct {
	Provider string
	Model    string            // "" or "default": pass no model flag
	Options  map[string]string // for example effort. Passed when the harness supports the option
}

type Agent struct {
	Name         string
	Description  string
	Route        Route
	Backups      []Route // at most 5, tried in order
	Instructions string  // a file path, or ""
	Tools        []string // tool families. May only narrow what the job offers
	Parallel     int      // sessions at once. Default 1
	Limits       provider.Limits
	Data         []provider.DataKind // narrows the provider's list. Never widens it
}

type Job string // "plan" | "map" | "plan.review" | "test" | "author" | "explore" | "repair" | "notes" |
                // "security" | "script.check" | "decide" | "pilot" | "decide.<name>"

type Context struct {
	Risk    string         // low | standard | high
	Surface domain.Surface
	Feature string
}

// Resolve returns the agents for a job: one, or several for a panel.
// ok is false when the job and every job it falls back to are unassigned.
func (a *Assignments) Resolve(job Job, c Context) (agents []Agent, ok bool)

type Ran struct{ Agent, Provider, Model string; Backup bool; Class provider.FailureClass }
// Run starts the job on the agent's route and moves to a backup on a provider failure.
func Run(ctx context.Context, ag Agent, job provider.Job, reg *provider.Registry) (provider.Result, Ran, error)

// internal/agents: parallel sessions
type Pool struct{ /* unexported */ }
func NewPool(ag Agent, accounts []string, maxSessions int) *Pool
func (p *Pool) Size() int // min(agent.Parallel, len(accounts), maxSessions)
func (p *Pool) Each(ctx context.Context, items []domain.ScenarioID, fn func(ctx context.Context, account string, id domain.ScenarioID) error) error

// internal/decide
type Decider interface {
	ID() string
	Health(ctx context.Context) Health
	Decide(ctx context.Context, s State, qs []Question) ([]Answer, error)
}
type Hint struct {
	Job         string
	Value       string
	Probability float64
}
// Ask runs a built-in or a custom decision job. ok is false below the limit, on an error, or with no decider.
func (j *Jobs) Ask(ctx context.Context, job string, s State) (h Hint, ok bool)
```

## Formats

**Providers.** One list. A `preset` fills in the fields that a vendor needs.

```yaml
providers:
  - id: anthropic
    kind: claude                       # uses the sign-in of the installed Claude Code
  - id: openai
    kind: codex
  - id: deepseek
    kind: claude
    preset: deepseek                   # sets the compatible endpoint. The key comes from the secret below
    key: "env:DEEPSEEK_API_KEY"
    data: [ticket, responses, pages]   # this vendor does not receive source code
  - id: gemini
    kind: acp
    command: [gemini, --acp]
  - id: typesafe
    kind: systemone                    # a decision provider
    preset: typesafe
    key: "keychain:semar/typesafe"
    data: [responses, pages]
```

| To add | Write |
| --- | --- |
| Anthropic | `kind: claude` |
| OpenAI | `kind: codex` |
| DeepSeek, or another vendor with an Anthropic-compatible endpoint | `kind: claude` with `preset:`, or with `env:` values |
| A vendor with an OpenAI-compatible endpoint | `kind: codex` with `endpoint:` and `key:`. Confirmed against the Codex CLI in Task 3 |
| Gemini CLI, OpenCode, or another harness that speaks ACP. OpenCode reaches many vendors | `kind: acp` with `command:` |
| TypeSafe Jev | `kind: systemone` with `preset: typesafe` |
| Jev through OpenRouter | `kind: systemone` with `preset: openrouter-jev` |

**Agents and assignments.**

```yaml
agents:
  planner:
    description: Reads the ticket and the code, and writes the test plan
    provider: anthropic
    options: { effort: high }
    backups: [{ provider: openai }]
    instructions: .semar/agents/planner.md
    limits: { maxTurns: 40, timeout: 10m }
  tester:
    provider: openai
    parallel: 3                        # three sessions at once, each with its own test account
  tester-light:
    provider: deepseek
  security:
    provider: anthropic
    instructions: .semar/agents/security.md
  reader-a: { provider: deepseek }
  reader-b: { provider: openai }
  fast:     { provider: typesafe }

assign:
  plan: planner
  plan.review: [reader-a, reader-b]    # a panel. The agents run in parallel. Advice only
  test:
    use: tester
    when:
      - { risk: low, use: tester-light }
  security: security
  script.check: reader-a
  decide: fast
```

**Jobs and what they fall back to.**

| Job | What the agent does | Class | When unassigned |
| --- | --- | --- | --- |
| `plan` | Writes the test plan | Agent | An error. Planning needs it |
| `map` | Drafts the feature map | Agent | Uses `plan` |
| `plan.review` | Reads the plan and proposes missing scenarios. One agent or a panel | Agent | Skipped |
| `test` | The default for all testing | Agent | An error. Testing needs it |
| `author`, `explore`, `repair`, `notes` | Scripts, exploring, locator repair, QA notes | Agent | Uses `test` |
| `security` | Business-rule probes and "must never" rules | Agent | Uses `test` |
| `script.check` | A second look at each script, by another agent | Agent | Skipped |
| `decide` | The default for every decision job | Decision | Each decision job takes its code fallback |
| `pilot`, `decide.<name>` | One decision job | Decision | Uses `decide` |

**Built-in decision jobs.** Each is used automatically when `decide` is assigned.

| Job | Question | Used for | Without an answer |
| --- | --- | --- | --- |
| `pilot` | Which action comes next for this sub-goal | Pages and screens, in Plans 09 and 10 | The tester acts step by step |
| `likelyCause` | The product, the test script, the test data, or the environment | The hint on a failure | No hint |
| `oddResponse` | How unlike the baseline is this response | The order in which the tester reads probe responses | The tester reads all |
| `sameFinding` | Do two findings describe one defect | Merging findings, and merging a panel's proposals | Both are kept |
| `changeKind` | A behaviour change or a refactor | The kind of a hunk | Counts as behaviour |
| `relevance` | How much does this earlier item concern the feature | The order of earlier testing | By date |
| `sameScenario` | Does this drafted scenario repeat one in the regression suite | Reuse in the plan | Compared by entry point and steps only |
| `elementMatch` | Which element is the one the old locator named | A repair proposal | The tester repairs |
| `errorPage` | Is this response or screenshot an error or outage page | A note on the step, and the health check's message | Nothing is noted |
| `severity` | High, medium, or low for this failure | The suggested severity | The fixed rules alone |

**Custom decision jobs.** A company adds its own question at a fixed hook, in YAML.

```yaml
decisions:
  minConfidence: 0.8
  custom:
    - id: maintenance-page
      at: response                 # response | screenshot | finding | hunk
      question: Is this a maintenance or outage page?
      kind: probability
      above: 0.9
      then: note                   # note | finding | ask_tester
      text: The service showed a maintenance page
```

## Rules

| # | Rule | Source |
| --- | --- | --- |
| R1 | The contract kit of Plan 02 runs unchanged against the Codex driver and the ACP driver | H8 |
| R2 | Before a job starts, its data kinds are checked against the provider's `data` list and the agent's. A refusal names the job, the agent, the provider, and the kind, and the job does not start | FR-211 |
| R3 | An agent's `tools` and `data` may only narrow what the job and the provider allow. A value that would widen either is a configuration error | A6 |
| R4 | An agent's instruction file is added after Semar's own prompt, inside a marked block. It cannot change a tool, a limit, an expected result, or a source label, because code enforces those | A6, A7 |
| R5 | Semar moves to a backup only on `usage_limit`, `provider_error`, or `transport_error`, in the listed order, at most 5 times. It never uses a provider that the agent does not list | FR-240 |
| R6 | The report names, for every job, the agent, the provider, and the model that ran, and says when a backup was used | FR-30 |
| R7 | A pool's size is the smallest of the agent's `parallel`, the number of free test accounts of the auth profile, and `policy.limits.maxSessions`, which is 4 by default and at most 8. The report says when the size was cut and why | FR-241 |
| R8 | Each session of a pool has its own test account and its own request budget. Two sessions never share an account | FR-159 |
| R9 | A panel runs its agents at once. Each agent sees the plan and none sees another's answer. Their proposals are merged, and `sameFinding` removes repeats when a decider exists | FR-212, 242 |
| R10 | A `plan.review` proposal and a `script.check` objection are advice. Code never adds a proposed scenario, and never changes an outcome on an objection | Product rule 2 |
| R11 | A `script.check` objection becomes a yes or no question for a person, with the step text and the recorded request. With `policy.checks.objectionIsInconclusive: true`, the scenario is `inconclusive` until the question is answered | FR-243 |
| R12 | `script.check` and `plan.review` run only on an agent whose provider is another vendor than the one that did the work. Otherwise the job is skipped with a note | FR-212 |
| R13 | A decision job's answer is a hint, a ranking, a note, or a question to the tester. No function of `verdict`, `probe.Confirm`, or `ready` takes a hint | FR-209 |
| R14 | Below the confidence limit, on an error, on a timeout of 5 seconds, or with no decider, a decision job returns `ok == false`, and the caller takes the fallback in the table | FR-209, 210 |
| R15 | A custom decision job may `note`, raise a `finding` for a person, or `ask_tester`. It has no way to set an outcome | FR-244 |
| R16 | Everything sent to any provider passes through redaction first. `semar egress` lists, per provider, what a run would send, and sends nothing | NFR-5 |
| R17 | `semar agents` prints the effective assignment: each job, its agent, the route, the backups, and the fallback that applies. `semar doctor` adds the health of each provider with no model turn | FR-245, H6 |

## Tasks

| # | Task | Files | Tests that prove it |
| --- | --- | --- | --- |
| 1 | Providers list and presets | `provider/presets.go`, `config` | A preset fills the endpoint and the environment names. An unknown preset or kind is an error that lists the choices. A key is always a secret reference |
| 2 | Data limits and `semar egress` | `provider/data.go`, `cli/egress.go` | A plan job on a provider without `code` is refused before the harness starts (R2). `egress` starts nothing |
| 3 | Codex driver | `provider/codex/*` | The contract kit passes on recorded output. `Health` with a fake binary. A custom endpoint reaches the child as configuration |
| 4 | Agents block | `agents/agents.go`, `instructions.go` | An agent with an unknown provider is an error with the line. More than 5 backups is an error. `tools` that widen the job are an error (R3). The instruction file lands inside the marked block (R4) |
| 5 | Assignments | `agents/assign.go` | One row per job of the fallback table. A `when` rule on risk and on surface. A panel returns several agents. `plan` and `test` unassigned are errors |
| 6 | Running a job with backups | `agents/run.go` | With fake providers: a usage limit moves to the first backup, a validation error does not, and the sixth failure stops (R5). `Ran` names what ran (R6) |
| 7 | Pools | `agents/pool.go` | Parallel 3 with 2 accounts gives 2 sessions and a note (R7). No two sessions hold one account, under `-race` (R8). A cancelled context stops every session |
| 8 | The plan review panel | `agents/panel.go`, `stage/plan/review.go` | Two fake agents run at once and neither sees the other's answer (R9). Proposals appear under "Proposed by review" and are not in the scenario list (R10). Same vendor: skipped with a note (R12) |
| 9 | The script check | `stage/author/check.go` | An objection becomes a question. With the policy flag the scenario is `inconclusive`, and without it the outcome is unchanged (R11) |
| 10 | The decision port and the `systemone` client | `decide/*`, `decide/systemone` | The kit against a recorded fixture: a choice, a score, a timeout, and a 401. The key appears in no log and no error |
| 11 | Built-in and custom decision jobs | `decide/jobs.go`, `custom.go` | One row per job: a sure answer, an unsure one, and no decider (R14). A custom job with `then: note` adds the text, and nothing in configuration can make it set an outcome (R15) |
| 12 | Assignments never change an outcome | a test in `internal/workflow` | The verify test of Plan 04 runs with five configurations: the minimum, a panel, a pool of 3, a decider that is always sure, and a decider that is always wrong. Outcomes and readiness lines are identical (R10, R13) |
| 13 | ACP driver | `provider/acp/*` | The contract kit, when spike S3 succeeded. Otherwise the task is dropped and the reason is recorded |
| 14 | Commands | `cli/agentscmd.go`, `doctor.go` | `semar agents` prints the table of R17 for the sample configuration, as a golden file |

## Done when

Starting from a configuration with one provider and two assigned jobs, each of these needs a YAML edit only, and the verify test passes after each one: add DeepSeek as a second provider, move low-risk testing to it, run three tester sessions at once, add a review panel of two agents, and add a decision provider for every decision job.

Task 12 shows that the outcomes are identical in all of them.

## Review focus

1. **A wrong fast model.** A decider that always answers wrongly, with high confidence, changes no outcome. Task 12.
2. **Parallel sessions with one test account.** The pool shrinks to one and says so. It never shares the account. Task 7.
3. **A provider that policy forbids for code.** The job is refused before any data leaves. Task 2.
4. **An instruction file that tells the agent to mark results as confirmed.** The label is assigned by code, and plan validation rejects the plan. Task 4.
5. **Every listed provider is out of quota.** The job ends with `usage_limit`, the tickets are `blocked` with that reason, and nothing was sent to a provider that was not listed. Task 6.

## Open points

* Spike S14 measures whether `likelyCause` and `oddResponse` are right often enough to show. Below a useful rate they stay off by default.
* The decision provider's exact request fields are confirmed in Task 10 and pinned by a fixture.
* Whether the Codex CLI accepts a custom endpoint in the way Task 3 assumes is checked in that task. If it does not, a vendor with an OpenAI-compatible endpoint is reached through an ACP harness.
