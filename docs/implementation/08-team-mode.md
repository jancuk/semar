# Plan 08: Team mode

> **For agentic workers:** This is a specification. Before you execute it, expand each task into steps in the format of [Plan 01](01-core-replay.md), against the code that exists by then. This plan is large. Its tasks are in four groups, and each group can be its own plan file.

* **Guide:** [implementation guide](README.md). **Spec:** TRD, [Evidence store, report, and sign-off](../TRD.md#evidence-store-report-and-sign-off), [Tickets and result comments](../TRD.md#tickets-and-result-comments), [Regression](../TRD.md#regression), [Running with little effort](../TRD.md#running-with-little-effort), [MCP configurations](../TRD.md#mcp-configurations)
* **Needs first:** Plan 04. Spike S11. Open decision 3.
* **Requirements:** FR-8 to 10, 31 to 35, 62 to 76, 79, 80, 91 to 97, 103 to 114, 129, 158 to 163, 187 to 191, 213 to 216, 223, 225 to 227

**Goal.** A team uses Semar with no command: results appear on the merge request and the ticket, people decide by reply, and pipeline jobs start every run.

**What works when it is done.** A ticket goes from an opened merge request to a signed-off report and one ticket comment. A nightly regression run publishes its report.

## Architecture

```text
pipeline event  -->  semar plan | verify | regression run | sync
                          |
          evidence.Store (git-branch | object)   <--  the shared record
                          |
   publish:  codehost.Host (comment)   ticket.Tracker (comment)   testcase.Manager (results)   notify.Notifier
                          ^
   decisions: approvals and replies, read by "semar sync", stored as records beside the run
```

* This plan is where most company differences live, so almost everything in it is a port: the code host, the tracker's comment, test management, the shared evidence store, and notifications.
* Each port has a native adapter, the `mcp` adapter, and the `exec` adapter of Plan 03. A company with an internal tool writes an `exec` command and no Go (X1).
* Writes are few and listed (X8): Semar's own comments, the evidence branch, a scenario merge request, and test results when turned on. Code makes each one. No agent holds a credential for any of them.
* Pipeline templates are examples per host. They call the same commands a person can type.

**Ports added:** test management, notifier. **Capabilities added:** comments on the tracker and the code host, the shared evidence store, MCP-backed adapters.

## File structure

```text
Group A: the shared record and publishing
internal/evidence/gitbranch/       the evidence port on an orphan branch
internal/evidence/object/          the evidence port on S3-compatible storage
internal/evidence/evidencetest/    the conformance kit
internal/codehost/gitlab/, github/ merge requests, approvals, comments, pipelines
internal/ticket/jira/comment.go    add and edit Semar's own comment
internal/publish/plan.go           the plan comment
internal/publish/report.go         the report comment
internal/publish/ticket.go         the ticket comment, built from a stored run
internal/publish/marker.go         a hidden marker that identifies Semar's own comments
internal/publish/runkey.go         ticket, builds, plan revision

Group B: decisions
internal/decision/decision.go      Record: confirm, accept, signoff, accept-gap, mark
internal/decision/parse.go         "/semar ..." replies
internal/decision/rules.go         who may decide what: author, roles, plan revision
internal/decision/sync.go          read approvals and replies, store records
internal/ready/trust.go            stages of trust from marks and samples
internal/cli/sync.go, status.go, accept.go, signoff.go, mark.go, sample.go, rollup.go

Group C: regression
internal/regression/suite.go       .semar/regression/<feature>/
internal/regression/promote.go     on sign-off, with replace-not-add
internal/regression/quarantine.go  three inconclusive runs in a row
internal/regression/known.go       .semar/known-failures.yaml, both ways
internal/regression/coverage.go    entry points with and without a scenario
internal/regression/compare.go     newly failing, still failing, needs repair, newly passing
internal/regression/bootstrap.go   baseline scenarios
internal/report/junit.go
internal/importer/postman.go, gherkin.go
internal/notify/notify.go, webhook/, exec/
internal/cli/regression.go, export.go, importcmd.go

Group D: knowledge and MCP
internal/adapter/mcp.go            maps a port's methods to tools of an MCP server
internal/knowledge/gateway.go      the MCP client, deny by default
internal/knowledge/mcpimport.go    read .mcp.json and a Codex config.toml
internal/knowledge/notes.go        memory notes that need approval
internal/testcase/testcase.go      the Manager port, the registry
internal/testcase/testrail/        read cases, write results when turned on
internal/cli/mcpimport.go, kb.go
templates/gitlab-ci.yml, templates/github-actions.yml
```

## Interfaces

```go
// internal/evidence. Plan 01's local store already satisfies this port.
type Store interface {
	NewRun(id domain.RunID) (RunWriter, error)
	LoadRun(id domain.RunID) (domain.Run, []domain.Activity, error)
	Runs(filter Filter) ([]domain.RunID, error)
	Verify(id domain.RunID) ([]string, error)
	PutRecord(kind string, key string, data []byte) error // confirmations, decisions, marks
	Records(kind string, prefix string) ([][]byte, error)
	Delete(id domain.RunID) error // ErrCannotDelete on the git branch
}

// internal/codehost, added to the port of Plan 03
type Capabilities struct {
	Comments, Approvals, ScenarioRequests, PipelineFromComment bool
}
type Comment struct{ ID, Author, Body string; At time.Time; InReplyTo string }
type Host interface {
	// ... the methods of Plan 03
	Approvals(ctx context.Context, repo, request string) ([]Approval, error)
	Comments(ctx context.Context, repo, request string) ([]Comment, error)
	UpsertComment(ctx context.Context, repo, request, marker, body string) (id string, err error)
	OpenRequest(ctx context.Context, repo, branch, title, body string) (id string, err error)
}

// internal/ticket, added to the port of Plan 03
type Commenter interface {
	UpsertComment(ctx context.Context, key, marker, body string) error
}

// internal/testcase
type Case struct{ ID, Title, Section, Steps, Expected string; UpdatedAt time.Time }
type Manager interface {
	Capabilities() Capabilities // Results bool
	Cases(ctx context.Context, sections []string) ([]Case, error)
	AddResults(ctx context.Context, run string, results []CaseResult) error
}

// internal/notify
type Notifier interface {
	Notify(ctx context.Context, m Message) error // title, lines, link
}

// internal/decision. Parse and Allowed are pure (G9)
type Kind string // "confirm" | "accept" | "signoff" | "accept_gap" | "mark"
type Record struct {
	Schema   int
	Kind     Kind
	Actor    string // the code-host or tracker identity of the author of the reply
	Ticket   string
	Run      domain.RunID
	Plan     string // the plan revision the reply was made on
	Items    []int
	Value    string // yes | no | false-pass | false-failure
	Reason   string
	Builds   []domain.BuildRef
	At       time.Time
}
func Parse(body string) (Record, bool)
func Allowed(r Record, ctx Context, p policy.Roles) error // the author, the role, the plan revision

// internal/publish
func RunKey(ticket string, builds []domain.BuildRef, planRevision string) string
func TicketComment(run domain.Run, results []domain.ScenarioResult, in Inputs) string // pure. No request or response body
```

## Formats

**Replies.**

| Reply | Where | Effect |
| --- | --- | --- |
| `/semar yes 1 2`, `/semar no 3` | Under the plan comment | Confirms or rejects expected results of that plan revision |
| `/semar accept` | Under the report comment | Accepts the evidence of that run |
| `/semar accept-gap 2 "email leaves the test environment"` | Under the report comment | Accepts one gap with a reason |
| `/semar signoff`, `/semar signoff release 2026.11.3` | The merge request, or the release ticket | Signs off the named builds |
| `/semar mark SHOP-482-01 false-pass "refund of 0 was accepted in production"` | Anywhere on the ticket | Records a wrong result |

**The ticket comment.** The text of the TRD, [The result comment](../TRD.md#the-result-comment): the build, the outcomes, the targets, what was done in each scenario, what was not tested, the readiness line, and a link. It begins with a hidden marker so that a later run finds and edits it.

**Configuration.**

```yaml
evidence:
  kind: git-branch              # local | git-branch | object
  branch: semar-evidence
change:
  kind: gitlab                  # git | gitlab | github | exec
  token: "env:SEMAR_CODEHOST_TOKEN"
publish:
  ticket: { comment: true, mode: update }
  testcases: { results: false }
notify:
  kind: webhook
  url: "env:SEMAR_WEBHOOK"
policy:
  roles:
    confirm: [reviewer, lead]   # names the company maps to code-host groups or users
    acceptGap: [lead]
    signoff: [release-owner]
  trust:
    sample: 5
    shadowTickets: 30
```

**An MCP-backed adapter.** A port's methods map to tools by name. The mapping is configuration (X1).

```yaml
tickets:
  kind: mcp
  server: atlassian
  tools: { ticket: getJiraIssue, links: getJiraIssue, comment: addOrEditJiraIssueComment }
  args: { cloudId: "env:ATLASSIAN_CLOUD_ID" }
```

## Rules

| # | Rule | Source |
| --- | --- | --- |
| R1 | Code makes every write to another system. The plan, the report, and the ticket comment are built from stored records, not from an agent's text | Product rule 11 |
| R2 | A Semar comment carries a hidden marker with the ticket and the kind. `UpsertComment` edits the comment with that marker and never another | FR-92 |
| R3 | Before it edits a comment, Semar compares the deploy time of the stored run's builds with the run named in the existing comment. An older run never replaces a newer one | FR-160 |
| R4 | The ticket comment holds outcomes and step descriptions. It holds no request body and no response body | FR-94 |
| R5 | A run is started only when no finished run has the same run key | FR-158 |
| R6 | A decision is accepted when its author is not the author of the change, holds the role that policy names, and replied to the current plan revision. A rejected reply gets one answer that says why | FR-8, 161 |
| R7 | An approval of the merge request that carries the scenario files confirms the expected results of that revision. A later change to a scenario file lapses the confirmation | FR-8, 10 |
| R8 | A sign-off names its builds. `semar status`, `semar rollup`, and a release reply compare them with the builds on the test environment each time, and a difference lapses the sign-off | FR-34 |
| R9 | An accepted gap never changes an outcome. A `failed` result cannot be accepted | FR-223 |
| R10 | On sign-off, a ticket's scenarios and scripts move to `.semar/regression/<feature>/`. A scenario with the same entry point and step hints as an existing one replaces it | FR-63, 163 |
| R11 | A scenario that is `inconclusive` in three runs in a row is quarantined. It still runs, appears under "quarantined" with the feature's owners, and counts neither as a pass nor as coverage | FR-162 |
| R12 | A scenario on the known-failures list with a ticket and an end date is reported as "known failure" when it fails, and as "passes now, remove the waiver" when it passes. A waiver past its date is an error | FR-187 |
| R13 | A regression run calls no model. A test asserts that no provider is constructed | FR-66 |
| R14 | A baseline pass is reported as "unchanged since the baseline" and counted apart | FR-65 |
| R15 | A tool of an MCP server is denied until it is listed with its use and its effect. An imported configuration copies server definitions and no secret value | FR-213, 214 |
| R16 | A tool with `effect: write` obeys the environment's effects policy and is recorded in the activity log as a write. A `tracker` tool is called by code only and is never offered to the agent | FR-215, 216 |
| R17 | Test results are written to the test management tool only when `publish.testcases.results` is true. `inconclusive`, `blocked`, and `stale` never map to a passing status | FR-112, 113 |
| R18 | A memory note is stored as a file in a merge request and joins the knowledge only after that request is merged | FR-108 |
| R19 | `semar sample` picks passed scenarios with a seed that is stored with the sample, so that the pick can be checked | FR-226 |
| R20 | A mark is a new record. It never edits a run | TRD, Marking a wrong result |

## Tasks

**Group A: the shared record and publishing**

| # | Task | Tests that prove it |
| --- | --- | --- |
| A1 | Evidence port and the kit | The local store of Plan 01 passes the kit unchanged |
| A2 | The git-branch store | The kit passes. Two runs pushed at once both land. `Delete` returns `ErrCannotDelete` |
| A3 | The object store | The kit passes against a local S3-compatible server. `Delete` removes the run and leaves a record that says when |
| A4 | Code host adapters | The kit against recorded fixture servers for GitLab and GitHub. Each adapter lists the requests it may send, and a test asserts it sends no other method and path |
| A5 | Plan, report, and ticket comments | Golden files. A second run edits the first comment (R2). An older run does not replace a newer one (R3). No body appears (R4) |
| A6 | The run key | A repeated deploy event starts nothing (R5) |

**Group B: decisions**

| # | Task | Tests that prove it |
| --- | --- | --- |
| B1 | Parse replies | One row per reply form, and rows for text that only looks like a command |
| B2 | Who may decide | The author is refused. A role that policy does not name is refused. A reply to an old plan revision is refused (R6) |
| B3 | `semar sync` | Approvals and replies become records. Running it twice adds nothing |
| B4 | Recompute after a decision | A confirmation turns `inconclusive` into `passed` or `failed` from stored evidence, with no request sent |
| B5 | Sign-off, status, and roll-up | A redeploy lapses a sign-off with no new run (R8). The roll-up reads only the evidence store |
| B6 | Marks, samples, and stages of trust | A false pass moves a feature back one stage. A sample is the same for the same seed (R19) |

**Group C: regression**

| # | Task | Tests that prove it |
| --- | --- | --- |
| C1 | Suite and promotion | A signed-off ticket's scenarios appear under the feature. A matching scenario replaces the older one (R10) |
| C2 | Regression run and comparison | No provider is constructed (R13). Newly failing scenarios come first |
| C3 | Quarantine and known failures | Three inconclusive runs quarantine a scenario (R11). A waived scenario that passes is reported (R12) |
| C4 | Coverage and bootstrap | Entry points with and without a scenario. A baseline pass is counted apart (R14) |
| C5 | JUnit, Postman, and Gherkin | A golden JUnit file. A Postman collection and a feature file each become scenarios with `source: person`, and what cannot be converted is listed |
| C6 | Notifier | Newly failing scenarios are posted once per run. An `exec` notifier passes the kit |

**Group D: knowledge and MCP**

| # | Task | Tests that prove it |
| --- | --- | --- |
| D1 | The MCP tool mapper | A tracker served by a fake MCP server passes the tracker kit of Plan 03 |
| D2 | The gateway and the import | An unlisted tool cannot be called. An import copies no secret (R15). A write tool is logged as a write (R16) |
| D3 | Test management port and TestRail | The kit against a fixture. With results off, no write is sent. An `inconclusive` result never maps to a pass (R17) |
| D4 | Tracker and test cases as knowledge sources | Earlier tickets and cases appear in `kb_prior_tests` with a source, a link, and a date |
| D5 | Memory notes | A note is a file in a merge request (R18) |
| D6 | Pipeline templates | Each template is linted with the host's own checker in CI |

## Done when

With a fake code host, a fake tracker, and the sample application, one test goes from an opened merge request to a plan comment, an approval, a deploy event, a report comment, one ticket comment, a sign-off reply, and a promoted regression scenario. No step of the test calls a command by hand except the ones a pipeline would.

The same test passes with the code host and the tracker replaced by `exec` adapters.

## Review focus

1. **Two pipeline jobs for the same deploy.** The run key makes the second one a no-op. Task A6.
2. **A reply edited or deleted after it was recorded.** The record keeps what was said and when. A deleted confirmation lapses at the next sync. Task B3.
3. **A code host with no pipeline trigger from a comment.** The scheduled `semar sync` picks the reply up, and the delay is stated in the answer. Task B3.
4. **A credential that can do more than comment.** `semar doctor` lists what each credential allows and warns. Task A4.
5. **A company with no test management tool and no tracker comments.** Each missing capability is skipped with a line in the report, and nothing fails (X3). Tasks A5, D3.

## Open points

* Open decision 3 picks the first code host adapter. Both are specified, and the second can follow.
* Spike S11 checks the owner's tracker and test management editions. An edition that the adapter cannot serve is reached with the `mcp` or the `exec` adapter.
* `semar ui`, the local page of FR-190, is left for a plan of its own after this one.
