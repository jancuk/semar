# Plan 16: Trial run after a plan

Status: Implemented. The text describes the code as built.

## Goal

Today `semar plan` sends no request, so a wrong path or an empty capture shows up only at `semar run`. After a draft passes validation, code now sends its read-only steps once, records them, and returns the request problems to the planner inside the existing three attempts.

1. Code sends each step that the effects policy classes as a read. A write and a fixture `create` are never sent.
2. A 405, a 400 or 422, a 401 or 403, a capture that found nothing, and a 404 that code can tell is not on a new route go back to the planner.
3. A 5xx, and a 404 on a route that may be new, go on a "needs a second look" list for a person.
4. `plan.md` shows each trial result beside its step.
5. A trial result never counts as a pass and never changes an expected result.

## Needs first

* Plans 12, 14, and 15 (implemented): the plan stage, the API documents, and the progress reporter.
* Plan 17 task 1 for `domain.PassTrial` and `domain.Run.Purpose`. Plan 17 task 9 before task C of this plan, because both edit `internal/stage/plan` and `internal/prompt`. Plan 17 task 11 before task E.
* Fixtures are used only through the `FindOnly` method that Plan 17 fixes. With no fixture resolver, the trial still runs and every step that needs a fixture output is "not tried".

## Implements

* TRD: [Trial run](../TRD.md#trial-run), [Pipeline](../TRD.md#pipeline) stage 1a, [Where the planned parts run](../TRD.md#where-the-planned-parts-run) item 2, and [Plan](../TRD.md#plan) (the one exception to "nothing is executed"). The key `plan.trial` of [Planned files and keys in the repository](../TRD.md#planned-files-and-keys-in-the-repository).
* PRD: decision D57, goal 27, and FR-283, FR-284, FR-285, FR-286, FR-287.

## Order and parallel safety

| Task | Must be merged before it starts | Can run in parallel with |
| --- | --- | --- |
| A Read a file at a commit | Nothing | Every task of Plans 17, 19, and 20 |
| B The trial runner | Plan 17 task 1 | A, Plan 17 tasks 2 to 9 |
| C The plan stage and the prompt | B, Plan 17 task 9 | D |
| D The `plan.trial` key | Plan 17 task 3 | C |
| E The command and `plan.md` wiring | C, D, Plan 17 task 11 | Plan 18 tasks 1 to 5 |
| F End-to-end test | E | Nothing |

Plan 18 task 7 edits `internal/stage/plan` and `internal/cli/plan.go` after task E.

## Decisions

| # | Decision | Reason |
| --- | --- | --- |
| D1 | The trial uses its own `effects.Policy`, built from the plan's environment with `Effects.Mode` set to `read_only`. The runner checks `Classify` before it sends, and the engine's `Admit` refuses any write that slips past | Two code checks, and the second is the existing one. No new rule decides what a read is (FR-284) |
| D2 | A step that matches `effects.once` is not tried | A read that must not repeat is rare, and the run of record must stay its first send |
| D3 | The trial sends at most 50 requests, each with a 10 second timeout, and stops after 2 minutes in all. The 50 requests and the 2 minutes are for the whole trial, across the attempts. Only the time inside `Try` counts, not the time the planner drafts between attempts, and a context with that deadline bounds every call of `Try`, a fixture find included. A draft whose every request was kept back by the limit or the budget is reported with `Skipped` set to `the trial budget was reached before any request of this draft was sent`, so `plan.md` and the summary say the trial was skipped instead of showing a draft with no problems | The trial checks requests. It is not a run, and a plan must not wait on a slow environment |
| D4 | A trial is skipped with one line when sign-in cannot be prepared, when the environment is `production` and `--env` did not name it, or when the first request gets no connection | FR-287 and the TRD failure modes. A plan never fails because of its trial |
| D5 | 405 always goes back. A 404 goes back only when code can show that the route is not one the change adds: the route is in an API document at the merge base (an ID in the path is likely wrong), or the route is in no API document at the merge base or at the head (the route is likely wrong). A route that only the head document has is a second look. With no API document, every 404 is a second look. When the API document file is not in the change, the head document is the base document. When it changed, it is read at the merge base with `git cat-file` | A route that the change adds is usually not deployed at merge request time. Sending that 404 back would make the planner "repair" a correct path |
| D6 | A step whose expected results include a status from 400 to 499 sends no status problem and no capture problem back. This reads the scenario, never the response | A refusal is often the behaviour under test. Telling the planner that the environment refused would push it to break a correct scenario. No response is compared with an expected result |
| D7 | The planner is told the status and the capture name only. No body, header, or value of a response reaches it | Rule 2: a trial cannot change an expected result. A planner that never sees a response value cannot copy one into an expectation |
| D8 | After a trial, every later draft must keep the expected results of each scenario that it keeps, matched by ticket and title. A change is a validation problem, and so is a new step with expected results in a kept scenario. A scenario added after a trial is marked in `plan.md` | Code enforces what it can of rule 2. A renamed scenario escapes the match. That is stated in `plan.md` and in open point 2 |
| D9 | When a later draft fails validation, the plan falls back to the last draft that passed, with its trial results | A trial must never turn an accepted plan into a rejected one. Validation problems still reject when no draft ever passed |
| D10 | All trial requests of one plan go to one run directory with `Purpose: trial` and no scenario result. It is closed with `run.json` even when the plan is rejected | Every action on the environment is in the activity log (product rule 13). `semar activity <run>` lists them. A run with no result is never exported or counted |
| D11 | `plan.md` shows the route template and the status, never the rendered path | `plan.md` is committed. A rendered path can hold an ID or a value of the environment |
| D12 | The trial is part of `plan.Run`, through a `Trialer` interface in `internal/stage/plan`. A nil `Trialer` is the trial turned off | The three attempts live in `plan.Run`. A nil-safe interface keeps every existing test of the plan stage unchanged |

## Trust

Untrusted: the repository and its `semar.yaml`, the planner's draft, and every response of the environment. Trusted: the person's environment variables, which hold the secrets of the auth profile.

| Risk | Answer |
| --- | --- |
| A drafted step writes to the environment during planning | D1. The policy is read-only, and the engine records a refused action instead of sending |
| A `GET` that changes state | The team lists it under `effects.deny`, as today. The trial obeys the deny list. Semar cannot detect it (TRD failure modes) |
| A response holds instructions for the planner | D7. The planner sees a fixed sentence with a status number and a capture name from its own draft |
| A response value becomes an expected result | D7 and D8 |
| The trial reaches a host that the environment does not list | The engine admits only hosts of `environments.<env>.hosts`. A drafted request has no host field |
| A secret appears in `plan.md` or a problem | Problems and `plan.md` hold templates and statuses. Recorded actions are redacted by the recorder as in a run |
| A slow or hostile environment stalls the plan | D3, and the request budget of the engine |
| The base API document is read from git with a crafted path | The path is a configured `knowledge` path that `config.CheckKnowledgePath` accepted. `Show` refuses a path that is not local and a commit that is not a full hex ID |

## Contract

### `internal/stage/plan`

New file `internal/stage/plan/trial.go`:

```go
// Trialer sends the read-only steps of an accepted draft once. A nil Trialer turns the trial off.
type Trialer interface {
	Try(ctx context.Context, scenarios []domain.Scenario, scripts []domain.Script) (Trial, error)
}

type Trial struct {
	Skipped string            // why nothing was sent, as one line; "" when the trial ran
	Run     domain.RunID      // the run that holds the actions
	Env     string            // the environment name
	Builds  []domain.BuildRef // read once, before the first request
	Steps   []TrialStep
}

type TrialResult string

const (
	TrialSent     TrialResult = "sent"
	TrialNotTried TrialResult = "not_tried"
)

type TrialStep struct {
	Scenario domain.ScenarioID
	Step     domain.StepID // "" for a given entry
	Given    string        // the alias of a given entry
	Result   TrialResult
	Why      string        // for not_tried, a fixed reason, see the table below
	Method   string
	Route    string        // the step's hint, or the fixture's path template
	Status   int           // 0 when no response
	Missing  []string      // capture names that found no value
	Err      string        // a transport error, from the recorded action
	Found    bool          // for a given entry: find gave a record
	Action   domain.ActionID
}

// Input gains:
//   Trial     Trialer
//   TrialOff  string               // why the trial is off, shown in plan.md; "" when not set by the person
//   BaseAPI   []openapi.Document   // the API documents at the merge base
// Output gains:
//   Trial        *Trial  // the trial of the accepted draft; nil when none ran
//   UsedAttempt  int     // the attempt whose draft was written; equals Attempts unless D9 applied
//   AfterTrial   []domain.ScenarioID // scenarios first drafted after a trial

func trialProblems(t Trial, scenarios []domain.Scenario, base []openapi.Document, profile string) (back []Problem, secondLook []TrialStep)
```

`trialProblems` is pure. Its table, where `<M>` is the method and `<route>` the hint:

| Trial result | Back to the planner | Problem message |
| --- | --- | --- |
| 405 | Yes | `trial: the environment answered 405 for <M> <route>: the method is likely wrong for this route` |
| 404, and the route is in a base API document | Yes | `trial: the environment answered 404 for <M> <route>, a route that the API document had before the change: an ID in the path is likely wrong` |
| 404, and the route is in no API document, before or after the change | Yes | `trial: the environment answered 404 for <M> <route>, and no API document has this route: the route is likely wrong` |
| 404, and only the head API document has the route, or there is no API document | No. Second look: `404: the route is not on the environment yet, or the path is wrong` | none |
| 400 or 422 | Yes | `trial: the environment answered <status> for <M> <route>: a query parameter or a header is likely wrong` |
| 401 or 403 | Yes | `trial: the environment answered <status> for <M> <route> as auth profile <profile>: the route may need another account, or a header is wrong` |
| 2xx or 3xx with a capture that found nothing | Yes | `trial: capture <name> found no value in the response of <M> <route>: the JSONPath is likely wrong, or the environment holds no such record and the scenario needs a fixture` |
| 5xx | No. Second look: `<status>: the service failed; this may be a defect` | none |
| Not tried | No | none |
| The step expects a 4xx status (D6) | No | none |

The path of a problem is `scenarios[i].steps[j]`, from the scenario's position in the draft. At most 20 problems go back. The rest are counted in one more problem: `trial: and 7 more problems of the same kinds`.

Reasons for "not tried", fixed texts:

| `Why` | When |
| --- | --- |
| `writes` | The policy classes the request as a write |
| `matches effects.once` | D2 |
| `denied: <refusal kind>` | `Admit` refused it, for example a host or a deny pattern |
| `needs {{<name>}}, which step <id> captures, and that step was not tried` | A variable comes from a step that was not sent or failed |
| `needs {{<as>.<output>}}, and fixture <id> found no record; a trial never creates one` | `FindOnly` found nothing |
| `needs {{<as>.<output>}}, and fixtures are not resolved in this trial` | The `Trialer` has no fixture finder |
| `the request could not be built` | `script.Render` failed. The error is not shown, because it can name a value |
| `the trial budget was reached` | D3 |

`Run` changes, in order, for each attempt:

1. Validate as today. With problems, keep them and go to the next attempt.
2. When an earlier draft was tried, check D8 against the expected results of that draft. A changed expected result is a problem: `scenarios[0].steps[1].expect: the expected results of "Refund above the order total is rejected" changed after the trial run; an expected result comes from the code, never from what the environment answered: keep the expected results of the earlier draft`. With problems, go to the next attempt.
3. Remember this draft as the last that passed.
4. When `Trial` is nil, accept the draft.
5. Call `Trial.Try`. A context error ends the plan as today. Any other error makes `Trial.Skipped` read `the trial stopped: <error>`, and the draft is accepted.
6. Compute `trialProblems`. With none, or on the last attempt, accept the draft with its trial.
7. Otherwise the problems go back with the previous draft, exactly as validation problems do, and the next attempt starts.

When the last attempt fails validation and a draft passed earlier, `Run` returns that draft with its trial and `UsedAttempt` set (D9). `Rejected` is returned only when no draft passed.

`Progress` is unchanged. While a trial runs, the status line keeps its elapsed time.

### `internal/trial` (new)

```go
type Doer interface {
	Do(ctx context.Context, c api.Call) (api.Result, error)
}

type Policy interface {
	Classify(r effects.Request) effects.Class
	Admit(r effects.Request) error
	Once(r effects.Request) bool
}

// Finder is the FindOnly method of fixture.Resolver (Plan 17), declared here (G4).
type Finder interface {
	FindOnly(ctx context.Context, owner domain.ScenarioID, given []domain.Given, authProfile string, pass domain.Pass) ([]domain.GivenFind, error)
}

type Deps struct {
	Engine      Doer              // an api.Engine built with Budget 50 and Timeout 10s
	Policy      Policy            // the read-only policy of D1
	Finder      Finder            // nil: no fixture is resolved
	Builds      build.Prober
	Probed      []string          // services with a build probe
	Services    map[string]string // service name to base URL, to classify before sending
	AuthProfile string
	Env         string
	Run         domain.RunID
	Clock       func() time.Time
	Limit       time.Duration     // 2m when zero
}

func New(d Deps) *Runner
func (r *Runner) Try(ctx context.Context, scenarios []domain.Scenario, scripts []domain.Script) (plan.Trial, error)
```

* `Try` reads the builds once, on its first call, and keeps them.
* For each scenario in order: call `Finder.FindOnly` with `PassTrial` when the scenario has `given` entries. Then, for each script step in order, render it with `script.Render` and the variables known so far, build the URL with `api.JoinURL`, and decide with the table above. A sent request is an `api.Call` with `Use{Pass: trial, Scenario, Step}`, `By: replay`, and the scenario's auth profile. A captured raw value is kept in memory for later steps of the same scenario only.
* The first sent request of the trial that has a transport error and no connection makes the trial stop with `Skipped: "the environment did not answer"`. The steps recorded so far are kept.
* `Try` returns an error only for a recorder failure or a cancelled context.

`internal/engine/api` exports one function, the existing `joinURL`:

```go
func JoinURL(base, path string, query map[string]string) (*url.URL, error)
```

### `internal/vcs`

New file `internal/vcs/show.go`:

```go
// Show returns the content of path at commit, at most limit bytes. It runs
// git cat-file blob <commit>:<path>. A path that is not in the commit is fs.ErrNotExist.
func (g *Git) Show(ctx context.Context, commit, path string, limit int64) ([]byte, error)
```

`commit` must be 40 or 64 lower-case hex characters. `path` must pass `filepath.IsLocal` after `filepath.FromSlash`, hold no `:`, and not start with `-`. More than `limit` bytes is an error that names the limit.

### `internal/config`

```go
// Plan gains:
//   Trial *bool `yaml:"trial"` // nil is true
func (p Plan) TrialOn() bool
```

`plan.trial` is decoded by the strict loader as a boolean. Any other value is the loader's type error, which names the file and the line.

### `internal/cli`

* `semar plan` gains `--no-trial`.
* After the copy of the source exists and the knowledge sources are resolved, `planChange` decides the trial:

| Case | Line on standard error | `Input.TrialOff` |
| --- | --- | --- |
| `--no-trial` | none | `turned off with --no-trial` |
| `plan.trial: false` | none | `turned off by plan.trial in semar.yaml` |
| The environment is `production` and `--env` did not name it | `semar: trial run skipped: uat is a production environment; name it with --env to try the reads against it` | the same text |
| `loadSessions` fails for the auth profile | `semar: trial run skipped: no account for support-agent: <error>` (the error names a reference, never a value) | the same text |
| The evidence store or the run cannot be created | `semar: trial run skipped: <error>` | the same text |

* Otherwise `planChange` builds the read-only policy, the recorder on a new run (`o.RunID()`), an `api.Engine` with `Budget: 50` and `Timeout: 10*time.Second`, a `fixture.Resolver` when the catalogue of Plan 17 is not empty, and a `trial.Runner`. After `plan.Run` returns, with or without an error, it closes the run with `domain.Run{Purpose: trial, Environment, EnvironmentKind, StartedAt, EndedAt, BuildsStart: trial builds, Results: []}`.
* `BaseAPI`: for each usable `openapi` knowledge source, the head document when its path is not in `ChangeSet.Files`; else `Show(ctx, cs.MergeBase, path, 5<<20)` parsed with `openapi.Parse`. A document that cannot be read or parsed is left out, with no line.
* The summary after the plan gains `tried: <sent> of <steps> steps, <back> problems returned, <second> for a second look`. When the trial was skipped: `trial: skipped`.

### `plan.md`

A section `## Trial run` follows "Files that no scenario covers" and comes before "Documents":

```
## Trial run

Code sent the read-only steps once to local before the plan was written. The environment ran orders 9f3c2ab. A trial result is evidence about a request. It is not a test result, and no expected result was compared. Run r-20261117T081500Z-1a2b holds every request: semar activity r-20261117T081500Z-1a2b.

### SHOP-482-05 A paid order can be read

- given order (order.paid): find found a record
- read-order: GET /v1/orders/{id} answered 200, capture status found
- refund: not tried: writes

### Needs a second look

- SHOP-482-06, step summary: GET /v1/orders/{id}/summary answered 404: the route is not on the environment yet, or the path is wrong
```

* A skipped trial reads `The trial run was skipped: <reason>.` A trial that is off reads `The trial run was <TrialOff>.`
* When D9 applied: `The last draft had problems, so this plan is draft 2 of 3, which passed.`
* A scenario of `AfterTrial` carries the line `First drafted after the trial run. Check that its expected results come from the code.`
* "How this plan was made" keeps the line `The requests were drafted from the source and were not sent before the first run.` only when no trial ran. With a trial it reads `The read-only requests were tried once. The others were not sent before the first run.`
* Every text is escaped as the rest of `plan.md`.

### The prompt

The prompt version becomes 5. "Your previous draft was rejected" gains one sentence after its first paragraph:

```
A problem that starts with `trial:` comes from one request that a program sent to the environment. It tells you that a path, a method, a parameter, or a capture is likely wrong. It never tells you what to expect: keep every expected result as the code says, even when the environment answered something else.
```

## Rules

| # | Rule |
| --- | --- |
| T1 | Only requests that the read-only policy admits are sent. A step that writes and a fixture `create` are "not tried" (FR-284) |
| T2 | A trial action is in the `trial` pass. No verdict reads the `trial` pass, because a verdict reads only the pass of a step (TRD, Verdict rules) |
| T3 | Trial problems and validation problems share the three attempts (FR-285). Problems left after the last attempt are in `plan.md`, and the plan is written |
| T4 | No response value reaches the planner. Expected results stay fixed after a trial (FR-286) |
| T5 | The planner has no network. Code sends the trial (TRD rule 5) |
| T6 | A trial never makes `semar plan` fail. A skip is one line |

## Tasks

| # | Task | Files it creates or edits | Interfaces it implements | Tests that prove it |
| --- | --- | --- | --- | --- |
| A | Read a file at a commit | Creates `internal/vcs/show.go`, `show_test.go` | `(*Git).Show` | In a test repository made with `git init` in `t.TempDir()`: `TestShowReadsFileAtCommit` (the old content after a later commit changed it). `TestShowMissingPath` is `fs.ErrNotExist`. `TestShowRefusesPath`: `../x`, `a:b`, `-x`, an absolute path. `TestShowRefusesCommit`: `HEAD`, a short ID, `--upload-pack=x`. `TestShowLimit` |
| B | The trial runner | Creates `internal/trial/trial.go`, `trial_test.go`. Edits `internal/engine/api/engine.go` (exports `JoinURL`), `engine_test.go` | `trial.Runner.Try`, `plan.Trialer` | Against the sample app of Plan 17 with a real engine and recorder: `TestOnlyReadsAreSent`: a POST step is "not tried: writes" and the app counts no POST. `TestWriteRefusedByEngineToo`: a policy fake that calls a write a read; the engine still refuses it and the app receives nothing. `TestReadsListAllowsPost`: a POST under `effects.reads` is sent. `TestOnceNotTried`. `TestDeniedNotTried`. `TestDependentStepNotTried`: a GET that needs a capture of a POST names that step. `TestCaptureFromReadFeedsNextRead`. `TestGivenFindOnly`: a fake `Finder` that found a record, one that found nothing, and a nil `Finder`. `TestBudgetStops` at 50. `TestNoAnswerSkips`: a closed port gives `Skipped`. `TestActionsInTrialPass`. `TestRenderErrorHidesValue` |
| C | The plan stage and the prompt | Creates `internal/stage/plan/trial.go`, `trial_test.go`. Edits `internal/stage/plan/plan.go`, `validate.go`, `render.go`, `plan_test.go`, `render_test.go` and golden files, `internal/prompt/plan.md`, `prompt.go`, `prompt_test.go` | `Trialer`, `Trial`, `trialProblems`, the `Run` loop, the `plan.md` section, prompt version 5 | `TestTrialProblemsTable`: one case per row, with the exact message. `TestNotFoundRules`: a route in the base document goes back, a route in no document goes back, a route only in the head document does not, and with no document nothing goes back. `TestFourXXExpectedSendsNothing`. `TestProblemCap` at 20. `TestNilTrialerUnchanged`: every existing `Run` test passes. `TestTrialProblemsReturnToPlanner`: a fake planner gets the problem text in its second prompt and no response body. `TestExpectationFrozenAfterTrial`: a second draft that changes a status check is rejected with the D8 message. `TestFallbackToLastPassingDraft`: attempt 1 passes with trial problems, attempts 2 and 3 fail validation; the output is draft 1 and nothing is rejected. `TestTrialErrorDoesNotFailPlan`. `TestPlanMarkdownTrial` (golden): sent, not tried, skipped, off, second look, after-trial marks, and an escaped route |
| D | The `plan.trial` key | Edits `internal/config/config.go`, `config_test.go` | `Plan.Trial`, `TrialOn` | `TestPlanTrialDefaultsOn`. `TestPlanTrialFalse`. `TestPlanTrialNotBoolean`: the error names the file and the line |
| E | The command and the wiring | Edits `internal/cli/plan.go`, `plan_internal_test.go` | `--no-trial`, the skip lines, the trial run, `BaseAPI` | `TestTrialSkipLines`: one case per row of the table. `TestTrialRunClosedWhenPlanRejected`: `run.json` exists with `purpose: trial`. `TestBaseAPIReadAtMergeBaseWhenChanged`. `TestTrialSummaryLine` |
| F | End-to-end test | Creates `internal/cli/trial_e2e_test.go` and `testdata/e2e-trial/` (a git repository built by the test from these files: a service source with a route `GET /v1/orders/{id}`, `docs/openapi.yaml`, `.semar/semar.yaml` with `knowledge`, and a fake provider whose outputs are two drafts) | Nothing new | Through `cli.Run` with the fake provider and the sample app: the first draft has a `given` of `order.paid` and a step with the hint `/v1/order/{id}`, a typo that no API document has; the trial finds the order, gets 404, and the "route is likely wrong" problem reaches the second prompt; the second draft fixes the path and is accepted. The sample app received exactly the trial GETs and no POST. `plan.md` shows the trial section. `semar activity <trial run>` lists the requests in the `trial` pass. A run with `--no-trial` sends nothing. A drafted 500 route appears under "Needs a second look" and not in the second prompt |

## Out of scope

| Item | Reason |
| --- | --- |
| A trial of web or Android steps | Those engines are not built |
| Fixture `create` in a trial | FR-284 and TRD rule 1 |
| Showing the trial on the status line | The trial is bounded at 2 minutes. Plan 15 open point 1 covers progress outside the planner |

## Open points

1. That trial problems lead the planner to a correct repair is not verified (TRD). The shadow milestone measures it.
2. D8 matches scenarios by ticket and title. A planner that renames a scenario can change its expected results after a trial. `plan.md` marks every scenario first drafted after a trial, and a person reviews it.
3. Telling a new route from a wrong path works only with an API document (TRD). Without one, every 404 is a second look and none goes back. A change that adds a route without adding it to the API document makes that route's 404 go back as "likely wrong". Plan 14 already reports such a route as missing from the document.
4. A trial meets the build that the environment runs now, which usually lacks the change. A 2xx on an old build says nothing about the new one, and `plan.md` names the build.
5. The trial uses the plan's one auth profile. A scenario that needs another account is tried as that profile, and a 403 may be the reason.

`make check` must pass.
