# Plan 05: Security testing

> **For agentic workers:** This is a specification. Before you execute it, expand each task into steps in the format of [Plan 01](01-core-replay.md), against the code that exists by then.

* **Guide:** [implementation guide](README.md). **Spec:** TRD, [Access checks](../TRD.md#access-checks), [Security testing by the agent](../TRD.md#security-testing-by-the-agent)
* **Needs first:** Plan 04.
* **Requirements:** FR-153 to 157, 174 to 179, 219, 220

**Goal.** Access checks and security probes run inside `semar verify`, on the entry points that a change affects.

**What works when it is done.** The report has a security section: the probe kinds that ran, confirmed findings that count as failures, suspected findings for a person, and the kinds that did not run with the reason.

## Architecture

```text
recorded action of a scenario step (the baseline)
        |
        v
probe.Variations (pure)  -->  api engine (policy, budget, recorder)  -->  probe.Confirm (pure)  -->  finding
                                                                                 ^
tester: business-rule probes and "must never" rules  ----------------------------+
```

* A probe is a variation of a request that already worked. Code builds it, code sends it, and code decides whether it shows a defect. No model is needed for probes 1 to 6, 8, and 9.
* `Variations` and `Confirm` are pure functions, so every probe kind is tested with no network (G9).
* The tester handles probe 7 and the feature's "must never" rules, through the same tools as in Plan 02. Its findings are suspected unless a rule's check, which code runs, confirms them.
* A company can add a probe kind without touching the core: a kind is a value with two functions, registered in `internal/cli/wire.go` (X2).

**Ports added:** none. **Extension point added:** probe kinds.

## File structure

```text
internal/probe/probe.go            Kind, Variation, Finding, the registry of kinds
internal/probe/access.go           the three access checks, with no model
internal/probe/variations.go       one function per kind that builds requests from a baseline
internal/probe/confirm.go          one function per kind that decides from recorded actions
internal/probe/payloads.go         the fixed input markers of probe 5
internal/probe/scope.go            which objects a probe may change: the run's own ledger
internal/probe/rules.go            "must never" rules from the feature map
internal/stage/security/security.go  the phase: order, budgets, the tester job for probe 7
internal/prompt/security.md
internal/report/security.go        the security section
internal/sampleapp/                one defect switch per probe kind
```

## Interfaces

```go
// internal/probe
type Kind string
const (
	AnotherUsersObject Kind = "another_users_object" // 1
	FunctionAboveRole  Kind = "function_above_role"  // 2
	SignInHandling     Kind = "sign_in_handling"     // 3
	ExtraFields        Kind = "extra_fields"         // 4
	InputHandling      Kind = "input_handling"       // 5
	TooMuchData        Kind = "too_much_data"        // 6
	BusinessRules      Kind = "business_rules"       // 7. The tester drives it
	ErrorText          Kind = "error_text"           // 8
	BrowserProtections Kind = "browser_protections"  // 9. Web, Plan 09
	RequestLimits      Kind = "request_limits"       // 10. Off unless security.burst is set
)

type Context struct {
	Entry       featuremap.Entry  // auth, owner, minRole, privateFields
	OtherUser   string            // an auth profile, or ""
	LowerRole   string            // an auth profile, or ""
	OwnObjects  []domain.ObjectRef // from the run's data ledger
	OtherObjects []domain.ObjectRef // objects the run created as the other user
	Budget      int
}

type Variation struct {
	Kind        Kind
	Label       string   // "no token", "extra field role"
	Call        api.Call // with its UseCase set to the security pass
	NeedsReadback bool
}

type Status string // "confirmed" | "suspected" | "none"
type Finding struct {
	Kind     Kind
	Status   Status
	Entry    string
	Label    string
	Reason   string            // "a 200 response returned order o-7 of the other account"
	Actions  []domain.ActionID // the baseline, the attempt, and the readback
	Rule     string            // the ID of a "must never" rule, or ""
}

// Both are pure (G9)
func Variations(k Kind, baseline domain.Action, c Context) ([]Variation, []Skipped)
func Confirm(k Kind, baseline, attempt domain.Action, readback *domain.Action, c Context) Finding

type Skipped struct{ Kind Kind; Reason string } // "no second test account"

// internal/stage/security
func Run(ctx context.Context, d Deps, baselines []Baseline, depth ready.Depth) (findings []probe.Finding, skipped []probe.Skipped, err error)
```

## Formats

**Feature map fields used here.**

```yaml
entries:
  - surface: api
    method: POST
    path: "/v1/orders/{id}/refunds"
    auth: required            # probe 3 and the "no sign-in" access check apply
    owner: true               # probe 1 and the "another user's object" access check apply
    minRole: support          # probe 2 applies
    privateFields: ["$.data.customer.national_id"]   # probe 6
rules:
  - id: refund-total
    mustNever: The refunded total of an order exceeds its paid total
    check: { entry: "GET /v1/orders/{id}", jsonpath: "$.data.refunded_total", lessOrEqualPath: "$.data.amount" }
```

A rule with a `check` is confirmed by code. A rule with no `check` gives a suspected finding at most.

**Configuration.**

```yaml
access:
  otherUser: support-agent-2
  lowerRole: viewer
security:
  level: probe            # access | probe
  budgetPerEntry: 40
  burst: 0
  kinds: []               # empty: every kind that the level allows. Or a list, to turn kinds off
```

**The report section.**

```text
Security  9 probe kinds on 4 entry points, 212 requests
          1 confirmed: GET /v1/orders/{id} returns another user's order   FAILED
          2 suspected, for a person to check
          1 kind not run: request limits (turned off)
```

## Rules

| # | Rule | Source |
| --- | --- | --- |
| R1 | Probes run only on a `kind: test` environment, on listed hosts, as configured accounts. Every probe request goes through the API engine, so `effects.deny` and the budget apply with no second code path | TRD, limits of security testing |
| R2 | A probe request that would change an object is built only for an object in `OwnObjects` or `OtherObjects`. Both lists come from the run's data ledger | FR-176 |
| R3 | On a `read_only` environment only kinds 3, 6, 8, and 9 produce variations, and only on reads | TRD |
| R4 | An access check needs no model. It is a variation of kind 1, 2, or 3 with a fixed expected result, and it runs at the security level `access` | FR-153 |
| R5 | `Confirm` returns `confirmed` only under these conditions. Kind 1: a status under 300 whose body holds the ID of the other account's object, or a readback that shows the object changed. Kind 2: a status under 300 on an entry point with `minRole` above the account's role. Kind 3: a status under 300 on an entry point with `auth: required`. Kind 4: a readback in which the extra field holds the sent value. Kind 5: a status of 500 or above, a database error text, or the marker returned unescaped in an HTML body. Kind 6: a body value that matches a secret pattern or a `privateFields` path with a value. Kind 8: a stack trace, a file path, or a query text in the body. Kind 10: no refusal after `burst` tries | TRD, the probe table |
| R6 | A refusal status alone does not clear a write. For a variation with `NeedsReadback`, a readback that shows a change is `confirmed` even when the attempt answered 403 | FR-154 |
| R7 | A confirmed finding becomes a scenario result with the outcome `failed`, and it counts in readiness. A suspected finding never changes an outcome | FR-175 |
| R8 | A variation that cannot be built is recorded as skipped with a reason, and the report lists it | FR-155 |
| R9 | Kind 10 produces no variation unless `security.burst` is above 0. Its requests have their own count and never use the budget of another kind | FR-177 |
| R10 | The input markers of kind 5 are a fixed list in `payloads.go`: one quote character, one pair of angle brackets with a unique token, one path fragment, one long string of 10,000 characters, one negative number, and one value of the wrong type. None is destructive | TRD |
| R11 | Every recorded response of the run is scanned once for secret patterns, whether or not a probe produced it | FR-157 |
| R12 | Each confirmed finding stores a script that replays the baseline and the attempt. After the defect is fixed and the script passes, it is offered for the regression suite in Plan 08 | FR-179 |
| R13 | The report states counts and never the word "secure" | FR-156, 178 |

## Tasks

| # | Task | Files | Tests that prove it |
| --- | --- | --- | --- |
| 1 | Kinds, findings, and the registry | `probe/probe.go` | An unknown kind in configuration is an error that lists the kinds. A kind turned off produces a `Skipped` with "turned off" |
| 2 | Scope from the data ledger | `probe/scope.go` | A write variation for an object outside the ledger is never built (R2) |
| 3 | Variations, one kind at a time | `probe/variations.go`, `payloads.go` | For each kind: the variations built from one baseline, as a golden list of method, path, and label. With no second account, kinds 1 and 2 are skipped with the reason (R8). On `read_only`, only the allowed kinds build (R3) |
| 4 | Confirmation, one kind at a time | `probe/confirm.go` | One table per kind with a row for each condition of R5, a row that is `none`, and for writes the row of R6 |
| 5 | Access checks | `probe/access.go` | With the sample application: no token gives a refusal and `none`. With the "other user reads" switch, the check is `confirmed` |
| 6 | "Must never" rules | `probe/rules.go` | A rule with a `check` that fails is `confirmed` with the rule's ID. A rule with no `check` gives `suspected` at most |
| 7 | Sample application switches | `sampleapp/` | One switch per kind, each covered by a route test |
| 8 | The security phase | `stage/security`, `prompt/security.md` | With the fake provider: probes run after the scenarios pass, stay inside the budget, and the tester's scripted double refund is confirmed by the rule `refund-total` |
| 9 | Report and readiness | `report/security.go`, `ready/readiness.go` | A golden file for the section. A confirmed finding makes the ticket "not ready". The text holds no "secure" (R13) |

## Done when

For each probe kind, `semar verify` against the sample application with that kind's switch ends with one confirmed finding of that kind and exit code 1. Against the correct service it ends with none. One test asserts that no request of the security pass touched an object outside the run's ledger.

## Review focus

1. **A probe that deletes the other test account's real data.** Scope is the ledger, so an object the run did not create is never a target. Task 2.
2. **A service that answers 200 with an error body.** Kind 1 confirms on the other account's ID in the body, not on the status alone. Task 4.
3. **A burst that locks the only test account.** Kind 10 is off by default and uses its own count. Task 3.
4. **A marker that the service stores and shows to other testers.** Each marker carries the run ID, so it can be found and removed. Task 3.
5. **No second account.** The security section says which kinds did not run, and readiness lists the gap. Task 3, Task 9.

## Open points

* Kind 9 needs the web engine and arrives with Plan 09.
* Whether the security pass runs by default on the owner's test environment is open question 28 of the PRD.
