# Plan 11: Configuration over code

> **For agentic workers:** This is a specification. Before you execute it, expand each task into steps in the format of [Plan 01](01-core-replay.md), against the code that exists by then.

* **Guide:** [implementation guide](README.md). **Spec:** TRD, [Configuration over code](../TRD.md#configuration-over-code)
* **Needs first:** Plan 04. Tasks 7 and 8 also need Plan 05, and Task 9 needs Plan 06.
* **Requirements:** FR-246 to 260

**Goal.** A developer or a QA engineer changes what Semar tests, and how, by editing YAML files. Nobody reads or changes Go code for a new use case.

**What works when it is done.** A person can add a scenario, run it with many inputs, share steps, add a rule that every response must meet, add a security probe, add a testing pass, and move a job to another model, all in files under `.semar/`. `semar validate` tells them what is wrong, with the file and the line, before anything runs.

## Architecture

Every file under `.semar/` is data that code reads. Code holds the mechanisms. The files hold the use cases.

```text
.semar/semar.yaml          hosts, accounts, limits, policy, providers, agents, assignments, adapters
.semar/map.yaml            features, entry points, "must never" rules
.semar/scenarios/          one file per scenario
.semar/fragments/          steps that several scenarios share
.semar/data/<env>.yaml     named test data per environment
.semar/checks.yaml         named checks
.semar/probes/             security probes
.semar/agents/<name>.md    extra guidance for one agent
.semar/prompts/<job>.md    a replacement for one of Semar's prompts
.semar/templates/          the layout of the ticket comment
```

* **Expansion comes first.** A scenario with cases, fragments, data, and named checks is expanded to the plain scenario of Plan 01 before anything else sees it. The verdict engine, the script binding, and the expectation hash all work on the expanded form. So a change in a fragment or a data set changes the hash of every scenario that uses it, and their confirmations and scripts lapse, as they should.
* **Built-ins are data too.** Semar's own security probes ship as embedded YAML in the same format that a company writes. A company copies one and changes it.
* **Three things stay in code,** because they are what makes a result mean the same everywhere: the verdict rules, the fixed invariants, and who may label an expected result. A YAML file can add rules. It cannot remove these.

**Ports added:** none. **Extension points added:** scenario expansion, the check library, company invariants, declared probes, custom passes, prompt and template overrides.

## What a user changes, and where

| You want to | Edit | Needs Go |
| --- | --- | --- |
| Add or change a test | A file in `.semar/scenarios/` | No |
| Run one test with many inputs | `cases:` in the scenario | No |
| Share steps between tests | A file in `.semar/fragments/` | No |
| Use other test data on another environment | `.semar/data/<env>.yaml` | No |
| Name a check once and reuse it | `.semar/checks.yaml` | No |
| Add a rule that every response must meet | `policy.invariants` | No |
| Say what must never happen in a feature | `rules:` in `.semar/map.yaml` | No |
| Add or change a security probe | A file in `.semar/probes/` | No |
| Add a testing pass, such as a wording review | `passes:` | No |
| Change which model does which job | `providers`, `agents`, `assign` | No |
| Give an agent extra guidance | `.semar/agents/<name>.md` | No |
| Ask a fast model a new yes or no question | `decisions.custom` | No |
| Change depth, confirmation, exit criteria, or who may decide | `policy:` | No |
| Connect another tracker, code host, or log system | `kind:` with a built-in, an `mcp`, or an `exec` adapter | No. An `exec` adapter is a script in any language |
| Change the layout of the ticket comment | `.semar/templates/ticket-comment.md` | No |
| Test a new protocol, such as gRPC | A new surface engine | Yes |
| Add a new kind of check, beyond status, JSONPath, header, text, element, URL, and data store | A change to `internal/check` | Yes |
| Change what `passed` means | Not possible per company | It is the fixed core |

## File structure

```text
internal/scenario/expand.go        cases, fragments, data, and named checks to plain scenarios
internal/scenario/fragments.go     load .semar/fragments/
internal/scenario/data.go          load .semar/data/<env>.yaml
internal/check/library.go          .semar/checks.yaml
internal/check/company.go          policy.invariants
internal/probe/declared.go         probes from YAML: variations and confirmation
internal/probe/builtin/*.yaml      Semar's own probes, embedded
internal/stage/pass/pass.go        a pass defined in configuration
internal/prompt/override.go        .semar/prompts/ and .semar/agents/
internal/publish/template.go       .semar/templates/ticket-comment.md
internal/schema/schema.go          JSON Schema for every file kind, generated from the Go types
internal/schema/suggest.go         "did you mean" for an unknown key
internal/cli/validate.go, explain.go, newcmd.go, migrate.go
schemas/*.json                     the published schemas
```

## Interfaces

```go
// internal/scenario
type Sources struct {
	Fragments map[string]Fragment
	Data      map[string]any // the data set of the chosen environment
	Checks    map[string]domain.Check
}
// Expand returns one plain scenario per case. A scenario with no cases gives one.
// The ID of a case is "<scenario id>[<case name>]".
func Expand(raw Raw, s Sources) ([]domain.Scenario, []Problem) // pure
// UsedBy lists the scenarios that a fragment, a data key, or a named check affects.
func UsedBy(ref string, all []Raw) []domain.ScenarioID // pure

// internal/check
type CompanyInvariant struct {
	ID    string
	Text  string
	When  Filter       // surface, method, path pattern, status range
	Check domain.Check
}
func Company(invs []CompanyInvariant, step domain.Step, a domain.Action) []Result // pure

// internal/probe
type Declared struct {
	ID       string
	Text     string
	Applies  map[string]any // conditions on feature map fields
	Vary     []Mutation     // remove_header, set_header, as_account, replace_path_param, add_field, set_field
	Confirm  []Condition    // checks on the attempt, on the readback, or against the baseline
	Readback bool
}
func FromDeclared(d Declared) (Kind, error) // registers a declared probe as a kind

// internal/schema
func For(kind string) ([]byte, error) // "config" | "scenario" | "script" | "map" | "fragment" | "probe" | ...
func Validate(root string) []Finding   // every file under .semar/, with file, line, message, and a suggestion
```

## Formats

**A scenario with cases, a fragment, data, and a named check.** This sketch shows the YAML that scenario files had before [Plan 24](24-gherkin-scenarios.md). Scenario files are Gherkin now, and `Examples` tables, fragments, and named checks are not built. The reader refuses `Examples`, and a built form of cases would be a table of cases in the Gherkin file.

```yaml
apiVersion: semar/v1
kind: Scenario
id: SHOP-482-02
title: Refund amounts at the boundary
ticket: SHOP-482
surface: api
authProfile: support-agent
cases:
  - { name: one-below, amount: 99999,  status: 201 }
  - { name: equal,     amount: 100000, status: 201 }
  - { name: one-above, amount: 100001, status: 422 }
steps:
  - use: paid-order                          # a fragment. It adds its steps here
    with: { amount: "{{data.orders.standardAmount}}" }
  - id: refund
    action: Request a refund of {{case.amount}}
    hint: { method: POST, path: "/v1/orders/{id}/refunds" }
    expect:
      - check: { status: "{{case.status}}" }
        source: ticket
        cite: "SHOP-482: a refund above the order total must be rejected"
      - check: { ref: error-has-code }       # a named check
        source: person
```

**A fragment,** `.semar/fragments/paid-order.yaml`:

```yaml
apiVersion: semar/v1
kind: Fragment
id: paid-order
params: [amount]
steps:
  - id: create-order
    setup: true
    action: Create a paid order for {{amount}}
    hint: { method: POST, path: /v1/orders }
```

**A company invariant,** in `semar.yaml`:

```yaml
policy:
  invariants:
    - id: request-id
      text: Every API response carries a request ID
      when: { surface: api }
      check: { header: X-Request-Id, exists: true }
    - id: no-password-field
      text: No response returns a password field
      when: { surface: api }
      check: { jsonpath: "$..password", exists: false }
```

**A declared probe,** `.semar/probes/tenant.yaml`:

```yaml
apiVersion: semar/v1
kind: Probe
id: other-tenant
text: A request with another tenant's ID in the header
applies: { auth: required }
vary:
  - { set_header: { X-Tenant-Id: "{{access.otherTenant}}" } }
confirm:
  - { on: attempt, check: { status: 200 } }
  - { on: attempt, check: { jsonpath: "$.data.tenant_id", equals: "{{access.otherTenant}}" } }
```

**A pass.**

```yaml
passes:
  - id: wording
    text: Read every message the change shows to a user, and report unclear or misleading wording
    agent: tester
    surfaces: [api, web]
    limits: { maxActions: 30, timeout: 8m }
    when: { risk: [standard, high] }
```

## Rules

| # | Rule | Source |
| --- | --- | --- |
| R1 | Expansion is pure and runs before validation. Every later stage sees plain scenarios | G9 |
| R2 | The expectation hash is computed on the expanded scenario. A change to a fragment, a data value, or a named check that a scenario uses lapses its confirmation and its script | FR-10 |
| R3 | `semar validate --changed` lists the scenarios that a change to a YAML file affects, before the change is merged | FR-250 |
| R4 | A variable is `{{case.x}}`, `{{data.x}}`, a fragment parameter, or a capture. An unknown variable is an error with the file and the line. A variable never expands inside a `cite` | G10 |
| R5 | A fragment may use another fragment, to a depth of 3. A cycle is an error that names the files | FR-248 |
| R6 | A source label written in a file follows the rules of Plan 01. An agent that writes a scenario with cases or fragments is held to the same rules as for a plain one | FR-4 |
| R7 | A company invariant is reported under "company rules", apart from Semar's fixed invariants. A broken one makes the scenario `failed`, like a fixed one | FR-251 |
| R8 | A company invariant can add a rule. Nothing in configuration turns a fixed invariant off, or changes a verdict rule | TRD, Verdict rules |
| R9 | Files under `.semar/` are changed through the repository's own review. No agent job has a tool that writes there. A plan's scenario files are written by code, from a validated plan | Product rule 10 |
| R10 | A declared probe is confirmed by its `confirm` conditions, which are checks that code evaluates. A probe with no `confirm` gives suspected findings only | FR-175 |
| R11 | A declared probe obeys every limit of Plan 05: the deny list, the request budget, and the scope of objects that the run created | FR-176 |
| R12 | Semar's built-in probes are YAML files in the same format, embedded in the binary. `semar probes show <id>` prints one, so that a company can copy it | FR-253 |
| R13 | A pass produces findings and QA notes. It never produces an outcome | D11 |
| R14 | A file in `.semar/agents/` is added to an agent's prompt. A file in `.semar/prompts/` replaces one of Semar's prompts, and the report then says "custom prompt for <job>" | A9 |
| R15 | The ticket comment template may arrange and word the comment. The outcome counts and the readiness line are always written by code, above the template's text | FR-91, 222 |
| R16 | Every file kind has a JSON Schema, generated from the Go types and checked by a test. Each file may start with a schema comment, so that an editor completes and checks it | FR-255 |
| R17 | `semar validate` checks every file under `.semar/`, reports all problems at once, each with the file, the line, the message, and a suggestion for an unknown key, and exits with 3 when it found one | FR-256 |
| R18 | Every command that reads configuration validates it first, and stops with the same messages | G10 |
| R19 | `semar explain <key>` prints what a setting does, its allowed values, its default, and its current value | FR-257 |
| R20 | `semar new scenario`, `fragment`, `probe`, or `pass` writes a file with comments that explain each field | FR-258 |
| R21 | Configuration is read at the start of every run. No rebuild and no restart is needed after an edit | FR-246 |
| R22 | A file with an older `apiVersion` is read as long as Semar supports it, and `semar migrate` rewrites it. A newer one is refused with a message that says to upgrade | X7 |

## Tasks

| # | Task | Files | Tests that prove it |
| --- | --- | --- | --- |
| 1 | Cases | `scenario/expand.go` | Three cases give three scenarios with stable IDs. A case value in a status check is a number after expansion. An unknown `case.x` is an error with the line (R4) |
| 2 | Fragments | `scenario/fragments.go`, `expand.go` | A fragment's steps land in place with their parameters filled. Depth 4 and a cycle are errors (R5). Step IDs stay unique when a fragment is used twice |
| 3 | Data sets | `scenario/data.go` | The same scenario expands with other values on another environment. A missing key is an error that names the environment |
| 4 | Named checks | `check/library.go` | A `ref` expands to the check. An unknown `ref` is an error with a suggestion |
| 5 | Hash and impact | `scenario/expand.go`, `cli/validate.go` | Changing a fragment changes the hash of each scenario that uses it and of no other (R2). `validate --changed` lists them (R3) |
| 6 | Company invariants | `check/company.go`, `policy` | A response without the header fails the scenario under "company rules" (R7). No setting disables "no server error" (R8) |
| 7 | Declared probes | `probe/declared.go` | The tenant probe above runs against the sample application with a tenant switch and is confirmed. With no `confirm` it is suspected (R10). It cannot touch an object outside the run's ledger (R11) |
| 8 | Built-in probes as YAML | `probe/builtin/*.yaml` | Every test of Plan 05 passes with the built-ins loaded from YAML. `semar probes show` prints one (R12) |
| 9 | Passes | `stage/pass/pass.go` | With the fake provider: a configured pass runs after the scenarios, inside its limits, and its findings appear in the report. The outcomes are unchanged (R13) |
| 10 | Prompt and template overrides | `prompt/override.go`, `publish/template.go` | An agent file is added inside the marked block. A replaced prompt adds the line to the report (R14). A template that leaves out the readiness line still gets it (R15) |
| 11 | Schemas | `schema/schema.go`, `schemas/` | A test regenerates each schema and fails when the committed file differs. Every example in the documentation validates against its schema (R16) |
| 12 | `semar validate` and `explain` | `cli/validate.go`, `explain.go`, `schema/suggest.go` | A repository with five mistakes gets five messages with lines, and "did you mean `expect`" for `expects` (R17). Every other command stops on the same repository with the same messages (R18) |
| 13 | `semar new` and `semar migrate` | `cli/newcmd.go`, `migrate.go` | Each generated file validates. A file of a newer version is refused (R22) |

## Done when

A test starts from a repository with one plain scenario and makes six changes, each by editing a file under `.semar/` only: three cases, a shared fragment, a second data set, a company invariant, a declared probe, and a wording pass. After each change, `semar validate` passes and `semar verify` against the sample application gives the expected result. The Semar binary is the same file throughout, and its checksum is compared at the end.

## Review focus

1. **A typo in a file.** The person sees the file, the line, and a suggestion. The run does not start with a test that silently checks nothing. Task 12.
2. **A fragment edited by one person breaks twenty scenarios.** `validate --changed` names all twenty before the merge, and their confirmations lapse visibly. Task 5.
3. **A company rule that contradicts a fixed one.** Both apply. Configuration cannot weaken the fixed rule. Task 6.
4. **A probe copied from the built-ins and made destructive.** The deny list and the object scope still apply, because the engine enforces them and not the probe. Task 7.
5. **A replaced prompt that makes results worse.** The report says which prompt was replaced, so the cause is visible. Task 10.

## Open points

* The schema generator is chosen in Task 11. A hand-written schema that a test checks against the Go types is the fallback.
* A pass on the web or on Android needs Plans 09 and 10.
