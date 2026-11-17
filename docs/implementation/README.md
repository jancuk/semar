# Semar implementation guide

* **Status:** Draft 1. Plans 01, 12 to 22, and 24 to 28 are implemented. The other plans are specifications.
* **Date:** 2026-11-14
* **Spec:** [product requirements](../PRD.md), [technical requirements](../TRD.md), [test workflow](../WORKFLOW.md), [user scenarios](../USER_SCENARIOS.md)

This guide is for the engineer or agent who builds Semar. It holds what every plan shares: the decisions still open, the engineering rules, the package map, and the order of the plans. Each plan is a separate file with tasks that can be executed one at a time.

Read this guide once, then open the plan you were given. A plan cites rules from this guide by their number, for example G4 or A2.

## How to use the plans

1. Check [Open decisions](#open-decisions). Decision 1 is made, so Plan 01 can start.
2. Take the lowest-numbered plan whose prerequisites are met. See [Plan sequence](#plan-sequence).
3. [Plan 01](01-core-replay.md) is written step by step. It is implemented, and its text describes the code as built.
4. Plans 00 and 02 to 11 are specifications. Each one fixes the files, the interfaces, the formats, the rules that code must enforce, and the tasks with the tests that prove them. Before you execute one, expand each task into steps in the format of Plan 01, against the code that exists by then. An interface in a specification is a decision. A signature may gain a parameter when the earlier code requires it, and the specification is then updated in the same change.

## Open decisions

The owner decides these. Each one blocks the plan named.

| # | Decision | Blocks | Default if the owner has no preference |
| --- | --- | --- | --- |
| 1 | The Go module path | Plan 01, Task 1 | Decided on 2026-11-14: `github.com/jancuk/semar` |
| 2 | The license | The first public push | Apache-2.0. Semar copies no third-party code |
| 3 | The code host: GitLab is assumed | Plan 08 | GitLab |
| 4 | Which browser engine the web engine uses. See TRD, [Web engine](../TRD.md#web-engine) | Plan 09 | Decided on 2026-11-17: an in-process Chrome DevTools Protocol client built on chromedp ([Plan 22](22-web-engine.md), D1) |

## Engineering rules

Every task's requirements include these rules. A reviewer rejects a change that breaks one.

### Go

| # | Rule | Why |
| --- | --- | --- |
| G1 | Go 1.26 is the floor: `go 1.26.0` in `go.mod`. Build with the newest stable toolchain. On 2026-11-14 that is go1.27.4, and the MCP SDK needs at least 1.25 | Two supported releases, and one known floor |
| G2 | `cmd/semar/main.go` only calls `cli.Run` and exits with its code. Commands live in `internal/cli`, and all other logic in its own package under `internal/` | A testable program, and no public API to keep stable |
| G3 | A package is named for what it provides: `verdict`, `redact`, `recorder`. No `util`, `common`, `helpers`, or `types` package | A name that says nothing collects everything |
| G4 | An interface is declared in the package that uses it, and it has the methods that user needs. A constructor returns a concrete type | Small interfaces, and fakes that are a few lines long |
| G5 | Dependencies arrive through a constructor. No package-level variable that changes, and no `init()` with a side effect | Tests run in parallel and in any order |
| G6 | Every function that does I/O or can block takes `ctx context.Context` first and stops when it is cancelled. Timeouts are set at the edge: the CLI, the HTTP client, a child process | A run can always be stopped |
| G7 | Wrap an error with `fmt.Errorf("read scenario %s: %w", id, err)`. Compare with `errors.Is` and `errors.As`. Error text is lower case with no final period. No `panic` for a failure that input can cause | A message that says where it failed, and callers that can branch |
| G8 | Time and IDs are injected: `Clock func() time.Time` and `IDs func() string`. No call to `time.Now()` or `rand` inside logic | The same input gives the same output in a test |
| G9 | The core is pure. `verdict`, `check`, `script` validation, `activity` derivation, and `ready` take values and return values. They do no I/O | Product rule 2: the same evidence always gives the same outcome |
| G10 | Decode files strictly. An unknown field in a scenario, a script, or the configuration is an error that names the file and the line | A misspelled key must not silently change a test |
| G11 | Every stored file has `apiVersion: semar/v1`, or `"schema": 1` in JSON. Write a file to a temporary name in the same directory, then rename it | Old evidence stays readable, and a crash leaves no half-written file |
| G12 | Log with `log/slog` through an injected logger. Never log a secret, a request body, or a response body. Evidence goes through the recorder, not the log | One place decides what is stored |
| G13 | Start a child process with `exec.CommandContext` and an argument list. Never build a shell string. Put the child in its own process group and stop the group on cancel | No injection through a ticket or a file name, and no orphaned harness |
| G14 | Goroutines are owned. Use `errgroup.Group` with `SetLimit` for workers. No goroutine outlives the function that started it unless a type with a `Close` method owns it | No leaks, and errors that surface |
| G15 | Tests are table-driven with `t.Run`, use `httptest` for HTTP, keep fixtures in `testdata/`, and compare with `go-cmp`, or with `reflect.DeepEqual` where a diff is not needed. Fakes are written by hand. Golden files are rewritten only with `-update` | Tests that a reader can follow, without a mocking library |
| G16 | A parser or a gate that reads untrusted text has a fuzz test: redaction, the path matcher, and later the statement gate | These are the places where a strange input causes harm |
| G17 | `make check` runs `gofmt -l`, `go vet`, `staticcheck`, `govulncheck`, and `go test -race ./...`. A task is not done until it passes | One command decides |
| G18 | Build with `CGO_ENABLED=0` and `-trimpath`. Set the version with `-ldflags`. A new dependency needs a sentence in the plan that says why the standard library is not enough | NFR-3: one static binary |
| G19 | Files that hold a secret or a session are created with mode 0600, and their directories with 0700. A path that comes from a file or a tool call is checked with `filepath.IsLocal` before use | The profile directory holds a live session |

### AI agent

| # | Rule | Why |
| --- | --- | --- |
| A1 | The tools are the contract. Each tool does one thing, has a JSON Schema for its input, and has a description of at most three sentences that says when to use it | An agent chooses tools by their descriptions |
| A2 | A tool result is small and structured. Cap it at 20,000 characters, say when it was cut, and give the path of the full stored result | A large result pushes the task out of the model's context |
| A3 | A tool error tells the agent what to do next: what was wrong, which value, and the valid choices. It never returns a stack trace | The agent reads the error and tries again |
| A4 | Structured output goes through a submit tool that code validates: `qa_submit_plan`, `qa_submit_script`. Validation errors are the tool result. Three attempts, then the job ends | One mechanism for every harness, and a bounded loop |
| A5 | The agent gets names, never values: secret names, database names, auth profile names. Code resolves them | Product rule 6 |
| A6 | Every limit is enforced by code: hosts, methods, the effects policy, budgets, and the source labels the agent may use. A prompt may repeat a limit. It never is the limit | A prompt can be ignored or overridden by text in a page |
| A7 | Text from a ticket, a page, a response, a log, or a document is data. It reaches the agent inside a tool result, and no tool lets it change an expected result, an outcome, or a permission | Product rule 10 |
| A8 | Every agent job has a turn limit, a time limit, and a request budget. A job that reaches one ends with a reason that the report shows | NFR-6 |
| A9 | Prompts are files under `internal/prompt/`, embedded with `embed`, each with a version line. A prompt change needs a run of the plan fixtures | A prompt is code, and it regresses like code |
| A10 | Give an agent job only the tool families it needs. `semar mcp --surfaces api,observe` keeps the others out of the context | Fewer tools, better choices |
| A11 | Every agent stage has a test that runs with a scripted fake provider and no network. Live runs against a real model are a separate, opt-in suite | The pipeline must be testable in CI at no cost |
| A12 | An agent's text is never evidence and never an outcome. Only what the recorder stored counts | Product rules 1 and 2 |

### Harness

| # | Rule | Why |
| --- | --- | --- |
| H1 | Semar starts the installed harness binary. It never reads, copies, or changes the harness's credentials | The vendor terms, and decision D7 |
| H2 | Remove inherited vendor keys from the child's environment unless the provider instance sets them. Give the child a working directory and a configuration that Semar controls | A stray key must not change who pays or which rules apply |
| H3 | Pass Semar's MCP configuration explicitly, and stop the harness from loading project hooks, project MCP servers, and project settings | The repository under test is not trusted |
| H4 | Read the harness's output as a stream of JSON lines. Skip a line that does not parse and an event type that is not known, and log both at debug level | Output formats change between harness versions |
| H5 | Each driver maps harness events to one set of canonical events, and failures to one set of classes: `usage_limit`, `provider_error`, `transport_error`, `permission_error`, `validation_error`, `unknown` | Stages do not know which harness ran |
| H6 | `Health` never starts a model turn. It checks the binary, the version, and the sign-in state | `semar doctor` must be free and fast |
| H7 | State the tested harness versions in one file, and warn when another version is installed | NFR-7 |
| H8 | One contract test suite runs against every driver, on recorded output in `testdata`. A fake harness binary covers a crash, a timeout, and a usage limit | A new driver proves itself on the same cases |
| H9 | On cancel or timeout, stop the child's whole process group, wait for it, and return what was recorded so far | A harness starts children of its own |

### Testing practices

| # | Practice | In Semar |
| --- | --- | --- |
| Q1 | Arrange, act, assert | A scenario has setup steps, action steps, and expected results. A setup failure is `blocked`, not `failed` |
| Q2 | Each test owns its data and can run alone or in parallel | A scenario creates what it needs, tags it with the run ID, and never reads another scenario's data |
| Q3 | Wait for a condition with a deadline. Never sleep for a fixed time | `eventually` on a check, and polling in the observer |
| Q4 | Find an element by its role and its name, not by position or a generated ID | `Locator` in a script |
| Q5 | Chain requests through named captures | `Capture` in a script step |
| Q6 | A retry that passes is reported as flaky, not as passed | "The script needed a retry" gives `inconclusive` |
| Q7 | A check that could not be evaluated is its own state | `not_evaluated` on a check, and `inconclusive` on the scenario |
| Q8 | A report a pipeline can read | `semar export junit`, and exit codes 0, 1, and 2 |
| Q9 | One action, one piece of evidence | One recorded `Action` per tool call, and the activity log |
| Q10 | Tag a few tests as smoke tests and run them first | The health check before a session |
| Q11 | A known failure has an owner and an end date | `.semar/known-failures.yaml` |
| Q12 | A result belongs to a version | Every run names its builds, and a sign-off names them too |
| Q13 | Validate a response against the schema | The OpenAPI invariant |
| Q14 | The framework tests itself on seeded defects | `internal/sampleapp` with one switch per defect |

## Architecture for adoption

Semar must fit a company that uses other tools than the first adopter does. The design has three layers, and each one answers a different question.

| Layer | What it holds | Who changes it |
| --- | --- | --- |
| The fixed core | Outcomes, verdict rules, the recorder, redaction, the effects policy, the evidence format, the activity log, and the product rules of the PRD | Nobody, per company. A change here is a change to Semar |
| Policy | Risk rules, test depth, which sources need confirmation, exit criteria, who may confirm and sign off, link rules for tickets, and limits | The company, in `.semar/semar.yaml` |
| Adapters | Every outside system: the tracker, the code host, observability, test management, data stores, sign-in, secrets, model providers, the evidence store, and notifications | The company, by choosing an adapter, or by adding one |

The core is fixed because it is what makes a result comparable between two companies. A "passed" means the same thing everywhere.

### Ports and adapters

Each outside system is reached through a port: a small Go interface in the package that uses it. An adapter implements the port. Configuration names the adapter with `kind:`.

| Port | Package | Built-in adapters | Plan |
| --- | --- | --- | --- |
| Agent provider | `internal/provider` | `claude`, `codex`, `acp`, `fake` | 02, 06 |
| Decision provider | `internal/decide` | `systemone` | 06 |
| Tracker | `internal/ticket` | `file`, `jira`, `github`, `mcp`, `exec` | 03, 08 |
| Code host | `internal/codehost` | `git` (no host), `gitlab`, `github`, `exec` | 03, 08 |
| Knowledge source | `internal/knowledge` | `semar`, `files`, `tracker`, `testcases`, `mcp` | 03, 08 |
| Test management | `internal/testcase` | `testrail`, `exec` | 08 |
| Observability | `internal/observe` | `grafana`, `mcp`, `exec` | 04 |
| Build probe | `internal/build` | `http`, `command`, `static` | 01, 04 |
| Secret store | `internal/secret` | `env`, `keychain`, `command` | 01, 04 |
| OTP source | `internal/auth` | `fixed`, `totp`, `http`, `command`, `mcp`, `prompt` | 04 |
| Data store | `internal/engine/db` | `postgres`, `mysql`, `elastic` | 07 |
| Surface engine | `internal/engine` | `api`, `web`, `android` | 01, 09, 10 |
| Evidence store | `internal/evidence` | `local`, `git-branch`, `object` (S3-compatible) | 01, 08 |
| Notifier | `internal/notify` | `webhook`, `exec` | 08 |
| Report format | `internal/report` | `markdown`, `json`, `junit` | 01, 08 |

### Three ways to add an adapter

| Way | What the company writes | When it fits |
| --- | --- | --- |
| Built in | Go code in a subpackage, plus one line in `internal/cli/wire.go` | A widely used system. The adapter is contributed to Semar |
| `mcp` | Configuration only: which tool of an MCP server serves which method of the port | The company already runs an MCP server for that system |
| `exec` | A command in any language that reads one JSON request on standard input and writes one JSON response on standard output. See [Plan 03](03-planning.md#the-exec-adapter-protocol) | An internal system that nobody else has. No fork of Semar is needed |

### Rules for every port

| # | Rule | Why |
| --- | --- | --- |
| X1 | No package outside an adapter names an outside product. The pipeline says "the tracker", never "Jira" | Swapping a system must touch one adapter |
| X2 | Adapters are registered by explicit calls in `internal/cli/wire.go`. No `init()` registers anything (G5) | The set of adapters in a binary is one readable list |
| X3 | An adapter declares its capabilities. When one is missing, the feature that needs it is skipped, and the report says so. Semar never fails because an optional system is absent | A company can start with what it has |
| X4 | No company fact is a constant in code: ticket key patterns, branch names, environment names, role names, status names, and thresholds all come from configuration | The first adopter's habits must not leak into the code |
| X5 | Every port has a conformance kit, a package named `<port>test`, that runs the same cases against any adapter, including an `exec` one | An adapter author can prove an adapter without reading Semar's code |
| X6 | An adapter never decides an outcome and never writes outside its own system. It returns data, and the core decides | The trust rules must not depend on which adapter runs |
| X7 | File formats and the `exec` protocol carry a version. A reader accepts its own version and refuses a newer one with a message that says to upgrade | Files and adapters outlive releases |
| X8 | Every adapter that can write states what it writes. `semar doctor` lists it. Writes are limited to the table in the TRD, [What Semar may write](../TRD.md#what-semar-may-write) | A company reviewing Semar must be able to see every write |

### An adoption ladder

Each level works alone. A company stops at the level it needs.

| Level | What the company connects | What it gets | Plans |
| --- | --- | --- | --- |
| 0 | An HTTP API and hand-written scenarios | Replay with no AI, evidence, an activity log, and a report | 01 |
| 1 | An agent harness | The agent writes scripts and explores | 02 |
| 2 | A git repository and tickets, from a tracker or from files | Plans from a change, with coverage tables | 03 |
| 3 | Build probes, test accounts, and optionally observability | `semar verify` with a readiness line, and security testing | 04, 05 |
| 4 | A code host, a tracker that accepts comments, and a pipeline | Results on the merge request and the ticket, with no command | 08 |
| 5 | A browser, an emulator | Web and Android scenarios | 09, 10 |

The `file` tracker adapter reads tickets from Markdown files in the repository. It lets a company try levels 2 and 3 before it grants Semar any access to its tracker.

### Policy presets

`semar init --preset <name>` writes a starting `policy` block. The presets differ only in values that a company can edit afterwards.

| Preset | For | Confirmation | Depth |
| --- | --- | --- | --- |
| `light` | A solo developer or a trial | `diff` and `baseline`. The author may confirm, and the report says so | Low and standard |
| `standard` | A team with code review | `diff` and `baseline`, by a person who is not the author | Low, standard, and high |
| `strict` | A regulated team | Every source except `person`, by a person who is not the author. A sign-off needs a named role | High for every ticket under a risk path |

### Configuration over code

A developer or a QA engineer never needs the Go code. Code holds mechanisms. Files under `.semar/` hold the use cases: scenarios, shared steps, test data, checks, company rules, security probes, testing passes, agents, assignments, and policy. [Plan 11](11-configuration.md#what-a-user-changes-and-where) lists what a user changes and where.

| # | Rule | Why |
| --- | --- | --- |
| C1 | A new use case is a file, not a code change. When a feature would need a constant or a branch per use case, it gets a field in a file instead | The people who know what to test are not the people who build Semar |
| C2 | Semar's own defaults ship in the same format that a user writes, embedded in the binary: probes, presets, and prompts | A user learns the format by reading a working example, and can copy it |
| C3 | Every file kind has a schema, `semar validate` reports every problem with the file and the line, and each command validates before it runs | A user without the code needs the tool to say what is wrong |
| C4 | Files are expanded to plain values before the core sees them. The verdict engine, the script binding, and the expectation hash know nothing of cases, fragments, or presets | The core stays small, and the same rules hold however a file was written |
| C5 | Configuration can add a rule. It cannot turn off a fixed invariant, change a verdict rule, or let an agent label an expected result | Flexibility must not weaken what a result means |
| C6 | Configuration is read at the start of each run. No rebuild and no restart follows an edit | An edit must take effect at once |

### Configuration keys

The specifications use two conventions throughout. `kind:` names the adapter of a port, and everything a company may tune is under `policy:`. The configuration example in the TRD was written before these conventions and uses older key names in places, such as `driver:`, `tracker:`, and a top-level `trust:`. Where the two differ, the specification is right, and the TRD example is updated in the task that implements the block.

## Package map

Dependencies point down this list. A package may import the ones above its own group and `internal/domain`. Nothing imports `cmd`. An adapter subpackage imports its port's package, never another adapter.

```text
cmd/semar/                 calls cli.Run and exits with its code
internal/cli/              the commands, and wire.go, which registers every adapter

internal/domain/           types only: Scenario, Script, Run, Outcome, Action. Imports no Semar package

internal/config/           semar.yaml to a typed Config
internal/policy/           the company's policy block: risk, depth, trust, exit criteria, roles      Plan 04
internal/secret/           the secret port: env, keychain, command, and the expansion of {{secret.NAME}}
internal/yamlfile/         strict YAML decoding with the file and line in each error, and JSONable
internal/hostrule/         the one rule for a listed host, used by effects and config
internal/idcheck/          whether an ID is safe as one path segment
internal/atomicfile/       writes a whole file or none, and encodes JSON for storage
internal/scenario/         scenario files: write and read Gherkin, load, and validate. legacy.go reads the older YAML for convert
internal/script/           script files: load, and validate the binding to a scenario
internal/effects/          what a test may change: admit or refuse one request
internal/redact/           removes secrets before storage and before the agent sees a result
internal/check/            evaluates one check against one recorded result
internal/verdict/          evidence and confirmations in, one outcome with every reason out
internal/activity/         activity entries and the data ledger

internal/evidence/         the evidence port: local, git-branch, object
internal/confirm/          confirmation records
internal/recorder/         the single path to a surface. Stores one Action per call
internal/build/            the build probe port: http, command, static
internal/engine/           the surface port, and api/, db/, web/, android/
internal/replay/           runs scripts through an engine with no model
internal/report/           markdown, json, junit, and the terminal summary
internal/sampleapp/        an HTTP service with seeded defects, for tests

internal/adapter/          the exec adapter runtime and the MCP tool mapper, shared by every port    Plan 03
internal/daemon/           semard: owns sessions and connections                                     Plan 02
internal/mcpserver/        semar mcp: the thin MCP server                                            Plan 02
internal/provider/         the agent port: claude, codex, acp, fake                                  Plans 02, 06
internal/decide/           the decision port: systemone, built-in and custom decision jobs           Plan 06
internal/agents/           agents, assignments, backups, pools, panels                               Plan 06
internal/schema/           a JSON Schema per file kind, and semar validate                           Plan 11
internal/prompt/           embedded prompt files                                                     Plan 02
internal/stage/            plan, author, explore, security                                           Plans 02 to 05
internal/workflow/         the phases of the test workflow, in order                                 Plan 04
internal/vcs/              reads a git repository through the git command                            Plan 03
internal/changeset/        ticket to merge requests, commits, and hunks                              Plan 03
internal/featuremap/       .semar/map.yaml                                                           Plan 03
internal/ticket/           the tracker port: file, jira, github, mcp, exec                           Plans 03, 08
internal/codehost/         the code host port: git, gitlab, github, exec                             Plans 03, 08
internal/knowledge/        the knowledge port, search, cache                                         Plans 03, 08
internal/testcase/         the test management port: testrail, exec                                  Plan 08
internal/gate/             deploy, revert, mixed builds, health check                                Plan 04
internal/auth/             auth profiles, the OTP port, sign-in                                      Plans 04, 09, 10
internal/observe/          the observability port: grafana, mcp, exec                                Plan 04
internal/ready/            risk levels, exit criteria, readiness, defect drafts                      Plan 04
internal/probe/            access checks and security probe variations                               Plan 05
internal/publish/          plan, report, and ticket comments, and decisions by reply                 Plan 08
internal/regression/       suite, coverage, quarantine, known failures                               Plan 08
internal/notify/           the notifier port: webhook, exec                                          Plan 08
```

## Dependencies

Checked on the Go module proxy on 2026-11-14. Pin the exact version in `go.mod`.

| Module | Version | Used for | First plan |
| --- | --- | --- | --- |
| `github.com/spf13/cobra` | v1.10.2 | Commands and flags | 01 |
| `go.yaml.in/yaml/v4` | v4.0.0 | Script and configuration files, decoded strictly. Scenario files too until Plan 24, and the older scenario format afterwards, for `semar scenarios convert` | 01 |
| `github.com/cucumber/gherkin/go/v43` | v43.0.0 | Reads a scenario file, which is Gherkin. The standard library has no Gherkin parser, and the official one reads every edge of the language as other tools do. Its messages module and `github.com/google/uuid` come with it | 24 |
| `github.com/theory/jsonpath` | v0.12.3 | JSONPath as in RFC 9535, for checks and captures | 01 |
| `github.com/bmatcuk/doublestar/v4` | v4.10.4 | `**` patterns for paths in the deny list and the risk rules | 01 |
| `github.com/google/go-cmp` | v0.7.0 | Comparisons in tests | 01 |
| `golang.org/x/sync` | v0.25.0 | `errgroup` for bounded workers | 01 |
| `github.com/modelcontextprotocol/go-sdk` | v1.8.0 | The MCP server and the MCP client | 02 |
| `github.com/coder/acp-go-sdk` | v0.13.5 | The ACP driver. Spike S3 decides | 06 |
| An OpenAPI validator: `github.com/pb33f/libopenapi` or `github.com/getkin/kin-openapi` | To choose in Plan 04 | The schema invariant | 04 |

`go.yaml.in/yaml/v4` was released on 2026-11-04. If it shows a defect during Plan 01, fall back to `go.yaml.in/yaml/v3` v3.0.6, which has the same decoding interface.

## Plan sequence

| # | Plan | What works when it is done | Needs first | Requirements |
| --- | --- | --- | --- | --- |
| 00 | [Spikes](00-spikes.md) | A written result for each spike | The owner's machine, accounts, and data | TRD, [Spikes before Milestone 1](../TRD.md#spikes-before-milestone-1) |
| 01 | [Core and replay](01-core-replay.md) (implemented) | `semar run` replays a scenario on the API with no model, and gives an outcome, evidence, an activity log, and a report | Decision 1 | FR-11, 15, 17 to 19, 22, 26 to 30, 48, 140 to 148, 151, 167 to 170, 172, 173, 186 |
| 02 | [Agent layer](02-agent-layer.md) | An agent writes a script through Semar's tools, and code validates and replays it: `semar author` | Plan 01. Spike S1 | FR-16, 36, 38 to 41, 47, 50, 146, 165, 210 |
| 03 | [Planning from a change](03-planning.md) | `semar changes` and `semar plan` give a validated test plan for a ticket. The adapter framework and the tracker port | Plan 02. Spikes S5, S10, S12 | FR-1 to 7, 53 to 61, 89, 90, 98 to 102, 115 to 128, 183, 217 |
| 04 | [Verify](04-verify.md) | `semar verify --ticket` runs the workflow from intake to the readiness line on the API | Plan 03. Spike S4 | FR-12 to 14, 23 to 25, 42, 43, 49, 77, 78, 149, 150, 152, 164, 184 to 186, 205, 218, 221, 222, 224 |
| 05 | [Security testing](05-security.md) | Access checks and probe variations run in `semar verify`, with confirmed and suspected findings | Plan 04 | FR-153 to 157, 174 to 179, 219, 220 |
| 06 | [Providers, agents, and assignments](06-providers.md) | Several providers, named agents with backups and parallel sessions, jobs assigned in YAML, and a decision provider used first where a job is a choice | Plan 02. Spikes S2, S3, S14 | FR-37, 206 to 212, 236 to 245 |
| 07 | [Data stores](07-data-stores.md) | Read-only checks on PostgreSQL, MySQL, and Elasticsearch | Plan 04. Spike S9 | FR-81 to 88, 180 to 182 |
| 08 | [Team mode](08-team-mode.md) | Results on the merge request and the ticket, decisions by reply, pipeline jobs, regression upkeep, and MCP import | Plan 04. Spike S11. Decision 3 | FR-8 to 10, 31 to 35, 62 to 76, 79, 80, 91 to 97, 103 to 114, 129, 158 to 163, 187 to 191, 213 to 216, 223, 225 to 227 |
| 09 | [Web](09-web.md) (superseded by [Plan 22](22-web-engine.md)) | Web scenarios, a Chrome profile of Semar's own, `semar login`, and the pilot | Plan 06. Spikes S6, S8, S13. Decision 4 | FR-20, 44, 46, 51, 52, 192 to 202, 204 |
| 10 | [Android](10-android.md) | Android scenarios on an emulator or a device | Plan 09. Spike S7 | FR-21, 45, 136, 203 |
| 12 | [A test plan from a diff](12-plan-from-a-diff.md) (implemented) | `semar plan --diff <compare link>` reads the difference of two branches, finds the tickets, and writes scenarios and scripts that `semar run` replays. It builds the parts of Plans 02 and 03 that this path needs | Plan 01 | FR-1 to 4, 16, 115, 116 |
| 13 | [Model providers and the dashboard](13-model-providers-and-dashboard.md) (implemented) | `semar plan` uses Claude Code, Codex, the Anthropic API, or any server with the OpenAI chat format. Providers live in the person's own file. `semar dashboard` manages them, starts a sign-in, and shows usage. A harness provider loads the person's MCP servers and skills | Plan 12 | None yet |
| 14 | [Documents the planner reads](14-knowledge-documents.md) (implemented) | `semar.yaml` names the API document and the documents of the repository. The planner reads them first, code compares every drafted request with the API document, and `plan.md` lists the differences. `semar init` finds the conventional paths | Plan 12 | None yet |
| 15 | [Progress while a plan runs](15-progress-while-planning.md) (implemented) | `semar plan` shows a status line on a terminal while the planner works, and one plain line every 30 seconds elsewhere. Every provider reports its responses and tool calls | Plan 13 | None yet |
| 16 | [Trial run after a plan](16-trial-run.md) (implemented) | `semar plan` sends the read-only steps of the accepted draft once and returns request problems to the planner inside its three attempts. A trial result never counts as a pass. `--no-trial` and `plan.trial: false` turn it off, and `plan.md` shows each result beside its step | Plans 12, 14, 15, and 17 | FR-283 to 287 |
| 17 | [Fixtures](17-fixtures.md) (implemented) | A scenario names the state it needs under `given`, and a reviewed fixture in `.semar/fixtures/` makes it with `find`, `create`, `until`, and `cleanup`. A person approves a fixture with `semar confirm --fixture`, `semar fixture check` records its health, and a file lease keeps two runs on one machine apart | Plans 01, 12, and 14 | FR-271 to 282 |
| 18 | [API journeys](18-journeys.md) (implemented) | `.semar/journeys/` holds business flows across services. `semar journey list` and `semar journey run` run the legs in order on one target manifest and give one of five outcomes. `plan.md` lists the journeys to rerun | Plan 17, and Plans 16 and 19 for the tasks that share files | FR-288 to 298, 300 (API legs) |
| 19 | [Obligations and the coverage ledger](19-coverage-ledger.md) (implemented) | `.semar/obligations/` holds the use cases of a change, and `semar plan` writes them. `semar ledger` gives each one a state, with `defer` and `record` for a person, and the run report opens with one use case line | Plans 01, 12, and 14, and Plan 17 for task 5 | FR-261 in part, FR-262 to 270 |
| 20 | [Exports](20-exports.md) (implemented) | `semar export ctrf`, `junit`, and `sarif` write one stored run in a common format, from the stored evidence and with no new outcome | Plan 01 | FR-188, 335, 336 |
| 21 | [Findings from a real delivery](21-field-findings.md) (implemented) | Ticket files are requirements: the planner expects what an acceptance criterion says, and every numbered criterion is accounted for by a check, a manual check, or a stated gap. Accounts with `as` on steps and journey legs, variables in expected values, `count` on arrays, plain unknown-key errors, `.semar/.gitignore`, and the problems of refused drafts in `plan.md` | Plans 12 to 20 | FR-123, FR-261 to FR-270 |
| 22 | [The web engine and web legs](22-web-engine.md) (implemented) | `surface: web` journey legs and web scenarios drive Chrome on a profile of Semar's own through one in-process engine. Steps name elements through screen objects in `.semar/screens/` that a person approves with `semar confirm --screens`. A broken locator ends the leg `inconclusive`, and `semar screens repair` proposes a fix with no model. `semar login` signs in once. Supersedes Plan 09 | Plans 17, 18, 19, and 21 | FR-20 in part, FR-192 to FR-202 in part, FR-288 to 298 (web legs) |
| 23 | [Tokens of a plan](23-plan-tokens.md) (implemented) | A plan costs far fewer tokens at the same quality. It runs without the person's MCP servers and skills unless `--with-setup` or `plan.withSetup` asks for them, the prompt holds the change evidence the planner always needs, a rejected draft is repaired in a short job, and `semar plan` ends with one line of turns, tokens, and cost | Plans 12, 13, and 21 | None |
| 24 | [Scenario files are Gherkin](24-gherkin-scenarios.md) (implemented) | A scenario is a `.feature` file that product and QA read and confirm as Gherkin, with the requests in the YAML script apart from it. `semar scenarios convert` moves older YAML files over and keeps every confirmation and script. `semar confirm` shows the file with the cites | Plans 01, 12, 17, 19, 20, and 22 | FR-6 and PRD decision D69 |
| 25 | [Planning with small models](25-small-models.md) (implemented) | `semar plan --pipeline criteria` plans one acceptance criterion at a time with a job that has no tools: code builds a packet of the criterion, its operations with their fields, the diff of the ticket's files, the fixtures, and the accounts, the answer is a closed schema, code checks it and says what is wrong in plain words, one correction goes to the same model, and a failing answer goes to the next model of `--ladder`. The draft, the files, and the ledger are the same as for `single`. Plan 27 makes it the default when a ticket has criteria | Plans 12, 17, 21, 23, and 24 | FR-353 to 355 and PRD decision D70 |
| 26 | [The daily loop](26-daily-loop.md) (implemented) | The everyday commands are `init`, `run`, `review`, `status`, `check`, `log`, `show`, and `rm`. `semar run` runs the tests of the change and the critical journeys, `semar review` is the one place for what needs a person, and `semar status` gives the state of a release with the next command. `confirm`, `ledger`, and `activity` are removed | Plans 12, 17, 18, 19, 21, 22, and 24 | None yet |
| 27 | [A first run with no hand-written files](27-first-run.md) (implemented) | `semar init` asks for what it cannot find, finds the build probe at `/version`, and in a repository that is set up checks the machine. A plan uses the criteria pipeline by default when a ticket has criteria, with a haiku and sonnet ladder for Claude Code, drafts and runs the fixtures that its criteria need, and stops at a cost limit | Plans 13, 14, 16, 17, 23, and 25 | FR-356 to 360 and PRD decision D71 |
| 28 | [Record a web test](28-record-a-web-test.md) (implemented) | `semar record` opens Chrome on Semar's profile, records what a person does and what the person points at as a check, and writes the screens, the script, and the scenario | Plans 17, 22, and 24 | FR-361 to 364 |
| 29 | [Semar signs in to a web application](29-web-sign-in.md) (planned) | An account names its web user and the variable of its password once. The engine signs in after any `open` that meets the sign-in form, and `semar record` starts signed in |
| 30 | [The plan writes web tests](30-plan-writes-web-tests.md) (planned) | `semar plan` explores a web criterion with a model in the seat of the person of `semar record`, and writes a web test that replays with no model |
| 11 | [Configuration over code](11-configuration.md) | Cases, fragments, data sets, named checks, company rules, declared probes, passes, schemas, and `semar validate`. Every use case is a YAML edit | Plan 04. Parts need Plans 05 and 06 | FR-246 to 260 |

Plans 01 to 04 are the first usable version: one API ticket from a plan to a report. Plans 05, 06, 07, and 11 do not depend on each other, apart from the tasks that Plan 11 names. They can be built in parallel after Plan 04, and Plan 06 can start after Plan 02. Tasks 1 to 5 and 11 to 13 of Plan 11 are worth doing early, because they are what a first user touches.

## The shape of a specification

Plans 02 to 11 all have the same sections, so that a reader finds the same thing in the same place.

| Section | What it fixes |
| --- | --- |
| Goal, and what works when it is done | The one result that the plan exists for |
| Architecture | How the plan fits the layers above, and which ports it adds |
| File structure | Every file, with its one responsibility |
| Interfaces | The Go signatures that other plans rely on |
| Formats | Configuration blocks, file formats, and tool inputs and results |
| Rules | The behaviour that code must enforce, numbered so that a test can cite a rule |
| Tasks | The order of work. Each task names its files and the tests that prove it |
| Done when | The end-to-end test of the plan |
| Review focus | The conditions most likely to hurt a user, each tied to a task |
| Open points | What a spike or the owner must settle |
