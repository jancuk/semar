# Plan 03: Planning from a change

> **For agentic workers:** This is a specification. Before you execute it, expand each task into steps in the format of [Plan 01](01-core-replay.md), against the code that exists by then.

* **Guide:** [implementation guide](README.md). **Spec:** TRD, [Planning from a change](../TRD.md#planning-from-a-change), [Knowledge and earlier testing](../TRD.md#knowledge-and-earlier-testing), [Every change has a ticket](../TRD.md#every-change-has-a-ticket)
* **Needs first:** Plan 02. Spikes S5, S10, S12.
* **Requirements:** FR-1 to 7, 53 to 61, 89, 90, 98 to 102, 115 to 128, 183, 217

**Goal.** For a ticket, code finds the code changes, and the planner writes a test plan that code validates.

**What works when it is done.** `semar changes --ticket <key>` prints the change table with no model. `semar plan --ticket <key>` writes scenario files and a plan with the risk level, the impact map, and both coverage tables.

## Architecture

This plan adds the adapter framework that every later port reuses, and the first two ports that a company will want to swap: the tracker and the code host.

```text
ticket.Tracker --+                                   +--> scenario files
codehost.Host ---+--> changeset.Build --> ready.Risk --> stage/plan (planner) --> plan.Validate --> plan.md
vcs.Repo --------+          |                                   ^
featuremap.Map -------------+-----------------------------------+
knowledge.Search -----------------------------------------------+
```

* Everything left of the planner is code with no model, and so is `plan.Validate` on its right. The planner is the only model call.
* The tracker and the code host are ports with a registry (X1, X2). The `file` tracker and the `git` code host need no outside system, so a company can run this plan on a laptop.
* Company habits are policy, not code (X4): the ticket key pattern, where a key may appear, and the risk paths.

**Ports added:** tracker, code host, knowledge source. **Shared runtime added:** `internal/adapter`.

## File structure

```text
internal/adapter/exec.go           runs an exec adapter: one JSON request in, one JSON response out
internal/adapter/protocol.go       Request, Response, error codes, the version
internal/adapter/adaptertest/      a fake command for tests
internal/ticket/ticket.go          the Tracker port, Ticket, Capabilities, the registry
internal/ticket/criteria.go        splits acceptance criteria into AC-1, AC-2
internal/ticket/file/              tickets as Markdown files under .semar/tickets/
internal/ticket/jira/              read a ticket and its development links
internal/ticket/exec/              the exec adapter for the port
internal/ticket/tickettest/        the conformance kit (X5)
internal/codehost/codehost.go      the Host port, MergeRequest, Capabilities, the registry
internal/codehost/git/             no host: merge commits and messages from git history
internal/codehost/codehosttest/    the conformance kit
internal/vcs/git.go                log, diff, show, merge-base, through the git command (G13)
internal/changeset/build.go        ticket to parts: requests, commits, hunks
internal/changeset/hunks.go        parse a unified diff into hunks with the enclosing symbol
internal/changeset/kinds.go        hunk kinds from path rules
internal/changeset/view.go         the build view: other tickets, commits with no ticket
internal/changeset/overlap.go      two tickets on the same lines or symbol
internal/changeset/coverage.go     hunks against scenarios
internal/featuremap/map.go         load, validate, look up entry points by source path
internal/featuremap/check.go       semar map --check
internal/knowledge/source.go       the Source port, Item, the registry
internal/knowledge/search.go       one search over every source, with a cache
internal/knowledge/semar/          past scenarios and results from the evidence store
internal/knowledge/files/          a folder of documents
internal/ready/risk.go             the risk level and the depth
internal/stage/plan/plan.go        the planner job
internal/stage/plan/validate.go    every check that code can make on a plan
internal/stage/plan/render.go      plan.md and the change table
internal/prompt/plan.md, map.md
internal/mcpserver/tools_plan.go   qa_submit_plan, kb_search, kb_get, kb_prior_tests
internal/cli/changes.go, plan.go, mapcmd.go
```

## Interfaces

```go
// internal/ticket
type Capabilities struct {
	DevelopmentLinks bool // can list merge requests linked to a ticket
	Comment          bool // can add and edit Semar's own comment. Used from Plan 08
}

type Ticket struct {
	Key       string
	Title     string
	Body      string
	Criteria  []Criterion // AC-1, AC-2, from the body's list markers
	URL       string
	UpdatedAt time.Time
}
type Criterion struct{ ID, Text string }
type ChangeLink struct{ Repo, Request string } // a merge request named by the tracker

type Tracker interface {
	Capabilities() Capabilities
	Ticket(ctx context.Context, key string) (Ticket, error) // ErrNotFound when it does not exist
	Links(ctx context.Context, key string) ([]ChangeLink, error)
}
var ErrNotFound = errors.New("ticket not found")

// internal/codehost
type MergeRequest struct {
	Repo, ID, Title, Description, SourceBranch, MergeCommit string
	Author   string
	MergedAt time.Time
}
type Host interface {
	Capabilities() Capabilities
	// Requests returns merged requests whose title, description, or source branch holds the key.
	Requests(ctx context.Context, repo string, key string) ([]MergeRequest, error)
}

// internal/vcs
type Repo interface {
	Log(ctx context.Context, from, to string, grep string) ([]Commit, error)
	Diff(ctx context.Context, base, head string) ([]byte, error)
	IsAncestor(ctx context.Context, commit, of string) (bool, error)
	Show(ctx context.Context, commit, path string) ([]byte, error)
}

// internal/changeset. Build needs I/O. The other three are pure (G9)
func Build(ctx context.Context, t ticket.Ticket, d Deps) (domain.ChangeSet, error)
func View(ctx context.Context, since, now []domain.BuildRef, d Deps) (BuildView, error)
func Overlaps(sets []domain.ChangeSet) []Overlap
func Coverage(cs domain.ChangeSet, scenarios []domain.Scenario) []domain.Coverage

// internal/ready
type Level string // "low" | "standard" | "high"
type Depth struct {
	MaxScenarios int
	ExploreMin   int
	Security     string // "access" | "probe"
	ConfirmAlso  []domain.Source
}
func Risk(cs domain.ChangeSet, overlaps []Overlap, p policy.Risk) (Level, []string)
func DepthFor(l Level, p policy.Depth) Depth

// internal/stage/plan
type Problem struct{ Path, Message string }
func Validate(p domain.Plan, in Inputs) []Problem // pure. Inputs: change set, feature map, ticket, OpenAPI paths

// internal/knowledge
type Item struct {
	Source, ID, Kind, Title, Excerpt, URL string
	Features []string
	UpdatedAt time.Time
}
type Source interface {
	ID() string
	Search(ctx context.Context, q Query) ([]Item, error)
	Get(ctx context.Context, id string) (Item, string, error) // the item and its text
}
```

`domain` gains `TicketRef`, `ChangeSet`, `ChangePart`, `Hunk`, `Coverage`, and `Plan`, as the TRD defines them. `Plan` holds its `Revision`, which is the hash of its content.

## Formats

**Configuration.**

```yaml
tickets:
  kind: file                         # file | jira | github | mcp | exec
  pattern: "[A-Z][A-Z0-9]+-[0-9]+"   # the company's ticket key
  required: true
  lookIn: [tracker_link, request_title, request_description, branch_name, commit_message]

change:
  kind: git                          # git | gitlab | github | exec

repos:
  - { name: orders, path: ., deployBranch: main }

policy:
  risk:
    paths: ["internal/payment/**", "internal/auth/**", "migrations/**"]
    manyEntryPoints: 10
  depth:
    low:      { maxScenarios: 4,  exploreMinutes: 0,  security: access }
    standard: { maxScenarios: 8,  exploreMinutes: 10, security: probe }
    high:     { maxScenarios: 12, exploreMinutes: 20, security: probe, confirmAlso: [ticket] }
```

**A ticket file,** for the `file` tracker: `.semar/tickets/SHOP-482.md`. The first heading is the title. A list under a heading named "Acceptance criteria" gives the criteria.

**Hunk kinds from paths.** Defaults that configuration can extend: `**/*_test.go`, `**/*.test.*`, `**/__tests__/**` are `test`. `**/*.md` and `docs/**` are `docs`. `**/*.pb.go`, `**/*_gen.go`, and files with a "Code generated" first line are `generated`. `migrations/**` is `migration`. `**/*.yaml`, `**/*.json`, and `**/*.toml` outside source directories are `config`. The planner labels every other hunk `behaviour` or `refactor`.

**The plan.** `qa_submit_plan` takes one object: `{behaviour, risk:{raise, reason}, impact:[{entry, reach, chain:[{file, line}]}], scenarios:[...], criteria:[{id, scenarios:[id]}], hunks:[{id, kind, scenarios:[id], flags:[...], reason}], earlier:[{item, decision, reason}], notInCode:[text], recommendations:[{kind, text}]}`. A scenario has the shape of a scenario file plus `hunks:[id]` and `criteria:[id]`.

### The exec adapter protocol

An `exec` adapter is a command that the company writes. Semar starts it once per call.

```text
<command> <port> <method>          for example: ./tracker-adapter ticket get
standard input:   {"version": 1, "params": {"key": "SHOP-482"}}
standard output:  {"version": 1, "ok": true, "data": { ... }}
              or  {"version": 1, "ok": false, "error": {"code": "not_found", "message": "..."}}
```

| Rule | Detail |
| --- | --- |
| Version | `1`. A response with another version is refused with a message that names both versions (X7) |
| Errors | `not_found`, `unauthorized`, `unavailable`, `invalid`, and `unsupported`. `unsupported` means that the adapter lacks the capability, and Semar treats it as a missing capability (X3) |
| Limits | A timeout of 30 seconds by default, a response of at most 4 MiB, and an exit code of 0 for every response that is valid JSON |
| Environment | The allowlist of R9 in Plan 02, plus the variables that the adapter's configuration names |
| First call | `<command> <port> capabilities` returns the capabilities of the port |
| Trust | The response is data. It passes through redaction before it reaches the agent or the evidence store |

```yaml
tickets:
  kind: exec
  command: [./tools/tracker-adapter]
  env: { TRACKER_TOKEN: "env:TRACKER_TOKEN" }
```

## Rules

| # | Rule | Source |
| --- | --- | --- |
| R1 | A change with no ticket key is not planned. The result is `blocked` with "the change has no ticket" | FR-90 |
| R2 | A ticket key found in a change is checked with `Tracker.Ticket`. A key that does not exist is treated as no ticket | FR-89 |
| R3 | Each part of a change set records how it was found: `tracker_link`, `request_title`, `request_description`, `branch_name`, or `commit_message` | FR-116 |
| R4 | With the `git` code host, a merge request is a merge commit on the deploy branch whose message holds the key. A squash merge is one commit | TRD, Limits of linking |
| R5 | A hunk ID is the file, the line range, and a hash of the changed lines. It is stable across runs on the same commit | TRD, From a ticket to its code changes |
| R6 | The enclosing symbol of a hunk comes from the diff's hunk header. Without one, overlap is decided by line ranges | TRD, Limits of linking |
| R7 | The risk level comes from code. The planner may raise it with a reason and cannot lower it. `--depth` sets it by hand | FR-217 |
| R8 | `Validate` rejects a plan when: a chain's first reference is outside the change set, a cited file or line does not exist at the deployed commit, an entry point is in neither the feature map nor an OpenAPI file and is not flagged `unlisted`, a `ticket` cite is not a sentence of the ticket, a source is `person` or `invariant`, a hunk ID does not exist, or a criterion ID does not exist | FR-4, 56, 61 |
| R9 | Every `behaviour` hunk has a scenario or the flag `uncovered` with a reason. Every criterion has a scenario or a reason | FR-123, 183 |
| R10 | A `refactor` hunk has a neighbour scenario | FR-126 |
| R11 | Scenarios past the depth's cap are listed as "not selected". A scenario for a `high_risk` hunk is never cut | FR-60 |
| R12 | A scenario that exists in the regression suite for the same entry point and steps is selected for rerun and not written again | FR-102 |
| R13 | Validation problems return to the planner as the tool result, for at most 3 submissions | A4 |
| R14 | Knowledge is read-only and is data. A source marked `sendToModel: false` is searched by code for citations and is never shown to the planner | FR-105, 109 |
| R15 | The planner job has the harness's read-only file tools, the `kb_*` tools, `qa_context`, and `qa_submit_plan`. It has no surface tool, so it cannot reach the test environment | TRD, Plan |

## Tasks

| # | Task | Files | Tests that prove it |
| --- | --- | --- | --- |
| 1 | The exec adapter runtime | `adapter/*` | A fake command: a valid response, each error code, a timeout, a response over the limit, a newer version, and invalid JSON. The child gets only the allowed environment |
| 2 | Tracker port, the `file` adapter, and the kit | `ticket/*`, `ticket/file`, `tickettest` | The kit passes for `file` and for an `exec` fake. Criteria are split from `-`, `*`, and `1.` lists. A missing ticket gives `ErrNotFound` |
| 3 | The Jira adapter, read-only | `ticket/jira` | The kit passes against a recorded fixture server. The adapter sends no request with a method other than `GET` |
| 4 | Git reader | `vcs/git.go` | A repository built in `t.TempDir()`: log with a grep, a diff, ancestry, and a file at a commit. A branch name that starts with `-` is not read as a flag (G13) |
| 5 | Code host port and the `git` adapter | `codehost/*` | In the fixture repository: a merge commit, a squash merge, and a loose commit are each found for their key |
| 6 | Change set | `changeset/build.go`, `hunks.go`, `kinds.go` | The fixture has a ticket in two repositories, a commit with no ticket, and a merge request for two tickets. Each link names how it was found (R3). Hunk IDs are stable (R5). Kinds follow the path rules |
| 7 | Build view and overlaps | `changeset/view.go`, `overlap.go` | A ticket that nobody asked for, a commit with no ticket, and two tickets on one function are each reported |
| 8 | Feature map | `featuremap/*` | A changed file maps to its entry points. `--check` reports an OpenAPI path that the map lacks and a pattern that matches no file. An unknown key is an error with the line (G10) |
| 9 | Risk and depth | `ready/risk.go` | One row per trigger: no behaviour hunk, a risk path, a migration, an overlap, many entry points. Raising is kept, and lowering is ignored (R7) |
| 10 | Plan validation | `stage/plan/validate.go` | One row per rejection of R8, and one row each for R9, R10, and R11 |
| 11 | Coverage and rendering | `changeset/coverage.go`, `stage/plan/render.go` | Golden files for the change table, the criteria table, the double-check list, and `plan.md` |
| 12 | Knowledge search | `knowledge/*` | Two sources give one ordered list. A cached item is served without a second read. A `sendToModel: false` item never appears in a `kb_search` result (R14) |
| 13 | Planner stage, tools, and commands | `stage/plan/plan.go`, `mcpserver/tools_plan.go`, `prompt/plan.md`, `cli/*` | With the fake provider: a valid plan writes scenario files under `.semar/scenarios/<ticket>/` and `plan.md`. A plan that cites a missing line is rejected, corrected on the second submission, and accepted. `semar changes` calls no provider |

## Done when

In the fixture repository with a `file` tracker, `semar changes --ticket SHOP-482` prints the change table of the TRD example, and `semar changes --build` prints the build view. `semar plan --ticket SHOP-482` with the fake provider writes scenario files and a plan whose risk level is `high` because a risk path changed. A scripted plan that labels a result `person` is rejected.

The same test passes with the tracker replaced by an `exec` adapter, with no change to any other configuration.

## Review focus

1. **A ticket key inside another word,** such as `SHOP-4820` when the key is `SHOP-482`. The match needs a word boundary. Task 6.
2. **A repository with no merge commits,** only squash merges or fast-forwards. The change set is built from commits and says so. Task 5.
3. **A diff with a renamed, a deleted, or a binary file.** Each becomes a hunk with a kind, and none causes a failure. Task 6.
4. **A ticket in a language other than English, or with no criteria.** Planning continues, and the plan says "the ticket has no acceptance criteria". Task 2, Task 11.
5. **An exec adapter that hangs or prints a secret on standard error.** The call times out with `unavailable`, and standard error goes to the debug log only after redaction. Task 1.

## Open points

* Spike S5 decides whether this plan is built at all.
* Spike S10 measures how well a model drafts the feature map. A poor result means `semar map` becomes a helper for a person and not a generator.
* Spike S12 measures how many commits carry a ticket key. A low number means the build view matters more than the ticket view.
