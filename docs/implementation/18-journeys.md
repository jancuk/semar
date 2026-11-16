# Plan 18: API journeys

Status: Specification, not implemented.

## Goal

A journey is a business flow that crosses services, such as "an order is placed, paid, and shown as paid". It belongs to the product, a named owner owns it, and it runs against one target manifest. This plan builds journeys whose legs are all on the API.

1. A file `.semar/journeys/<id>.yaml` holds `kind: Journey`: an owner, a criticality, `given` fixtures, legs, and invariants.
2. A leg names its service, may need earlier legs, may wait on a condition, and exposes outputs that later legs read as `{{<leg>.<name>}}`.
3. Code forms the target manifest from the build probes, runs the legs one at a time in `needs` order, checks the invariants, and reads the manifest again at the end.
4. A journey ends in one of five outcomes: `passed`, `failed`, `blocked`, `stale`, or `not_run`. A journey has no `inconclusive`.
5. `semar journey list` and `semar journey run` exist. A stored result shows as `stale` when a later read of the manifest differs.

Each leg runs through the same API engine, effects policy, recorder, and verdict engine as a scenario.

## Needs first

Plan 17, all tasks: `given`, the fixture resolver, the second service of the sample app, and the dotted variables. Task 6 of this plan needs Plan 19 task 5, and task 7 needs Plan 16 task E, because they edit the same files.

## Implements

* TRD: [Journeys](../TRD.md#journeys) (the file, rules 1 to 7, the target manifest, scheduling and waits, outcome, impact, failure modes), [Planned types for obligations, fixtures, journeys, and engines](../TRD.md#planned-types-for-obligations-fixtures-journeys-and-engines) (Journeys), [Where the planned parts run](../TRD.md#where-the-planned-parts-run) item 6, and the `.semar/journeys/<id>.yaml` row of [Planned files and keys in the repository](../TRD.md#planned-files-and-keys-in-the-repository).
* PRD: decisions D58 and D59, goal 28, and FR-288, FR-289, FR-290, FR-291, FR-292 (the commands; the schedule is the team's), FR-293, FR-294, FR-295, FR-296, FR-297, FR-298, FR-300 (API legs).

## Order and parallel safety

| Task | Must be merged before it starts | Can run in parallel with |
| --- | --- | --- |
| 1 Domain types | Plan 17 task 11 | 3 |
| 2 Journey files | 1 | 3, 4 |
| 3 Exact comparison of two values | Nothing | Every task |
| 4 One scenario with given variables | 1 | 2, 3 |
| 5 The journey runner | 2, 3, 4 | Plan 19 tasks |
| 6 Commands, confirmation, and the report | 5, Plan 19 task 5 | 7 |
| 7 Journeys to rerun in a plan | 2, Plan 16 task E | 6 |
| 8 End-to-end test | 6, 7 | Nothing |

## Decisions

| # | Decision | Reason |
| --- | --- | --- |
| D1 | `surface` must be `api`. Validation refuses `web` and `android` with a message that names the milestone | The user's scope for this plan is API-only. The TRD gives `not_run` to a leg with no engine. A clear refusal when the file loads is easier to act on than a journey that never runs. When the web engine exists, the refusal becomes that `not_run` rule with no change to the format (FR-300) |
| D2 | An API leg's steps carry their request and captures inline: a step is a scenario step (`id`, `action`, `setup`, `hint`, `expect`) plus `request` and `captures` | A leg needs a script, and no author stage exists for API scripts. A person writes a journey by hand, so the request is part of what the owner reviews |
| D3 | Code turns each leg into a `domain.Scenario` with ID `<journey>.<leg>` and a `domain.Script` bound to its `ExpectHash`. `script.ValidateWith` checks the binding, and `verdict.Compute` decides the leg | TRD rule 5: a leg is judged like a scenario. Reusing both keeps one set of verdict rules |
| D4 | A leg's expected results are confirmed with the existing confirmation store, under the leg's scenario ID. `semar confirm --journey <id>` confirms every leg | TRD rule 5 says confirmation works as usual. The record and the trust labels stay the same |
| D5 | A journey file needs no approval of its own | A journey sends requests only through the effects policy, its fixtures need approval (Plan 17), and its expected results need confirmation. A third approval would add a step that proves nothing new |
| D6 | `given` is on the journey only. A leg's `given` is refused | The TRD type has `Leg.Given`. Fixtures that a whole flow shares are the common case, and one resolution with one cleanup is simpler to reason about |
| D7 | Each leg's session comes from its service's `authProfile` (Plan 17), else the journey's `authProfile` | A journey crosses services, and each service has its own account (FR-280). The journey-level key covers a service with none |
| D8 | A leg's provisional outcome is computed with the start builds as the end builds, to decide whether the journey goes on. The final outcome of every leg is computed again with the builds read at the end | The end of the journey is not known while it runs. The rule "a leg starts only when every leg it needs has passed" needs an outcome at once |
| D9 | `wait` repeats the leg's last step, which must be a `GET` or `HEAD`, with the intervals of a fixture's `until`. A timeout makes a leg whose verdict passed `failed`. A worse verdict stays | TRD: a timeout is `failed` with each attempt in the evidence. A state that never arrives is a defect of the flow |
| D10 | Invariant values are the raw captured values in memory. Two values are equal when both are decimals with the same value, or when both are the same text. A value that holds a redaction marker cannot be compared | TRD rule 6: values compare exactly, as decimals. A redacted value is not evaluated, as for a check. Raw values are never stored |
| D11 | A journey run is a run directory with `Purpose: journey`. Its leg results are in `Run.Results`, and its journey results in `Run.Journeys` | Exports, the activity log, and evidence checks then work on legs without new code. Plan 20 exports a leg as one test |
| D12 | The manifest holds the services only. `Web` and `Mobile` stay nil | No web or Android leg exists in this plan |
| D13 | Staleness of a stored result is computed on demand by `semar journey list --env`, which reads the build probes now | A result is valid only for its manifest (FR-289, FR-296). Reading builds needs the environment, so it is not done by a plain listing |
| D14 | Journeys to rerun are found by matching each leg step's hint with the hints of the drafted scenarios of a plan | The impact map of the TRD is not built. A drafted hint is a route that the change touches, as code has already checked it against the source |

## Trust

Untrusted: the journey file and every other file of the repository, every response, and every engine result. Trusted: the person's environment and home.

| Risk | Answer |
| --- | --- |
| A leg reads an output of a leg that it does not need, and so depends on an order that the file does not state | Validation refuses any reference that is not an output of a leg it needs, directly or through other legs (TRD rule 3) |
| A cycle in `needs` makes the run loop | Validation finds it and names the legs |
| A leg sends a write that the environment does not allow | The engine admits every request through the policy. A refused step gives a `blocked` leg, and the journey is `blocked` |
| A captured value changes an endpoint of a later leg | `script.Render` inserts it into a path as one segment of unreserved characters |
| An invariant compares a secret | Values holding a redaction marker are not compared. The report shows the redacted values only |
| A `wait` that never ends | `timeout` is required, at most 15 minutes, and the engine's request budget bounds it |
| A journey on another manifest is read as a pass of the product | Every result carries its manifest and its hash. `journey list --env` marks it `stale` when any part differs |
| A large journey file | 256 KiB, 12 legs, 20 steps per leg, 8 invariants, and a fuzz test on `journey.Parse` |

## Contract

### `internal/domain`

New file `internal/domain/journey.go`:

```go
type JourneyID string
type LegID string
type JourneyOutcome string

const (
	JourneyPassed  JourneyOutcome = "passed"
	JourneyFailed  JourneyOutcome = "failed"
	JourneyBlocked JourneyOutcome = "blocked"
	JourneyStale   JourneyOutcome = "stale"
	JourneyNotRun  JourneyOutcome = "not_run"
)

const PurposeJourney = "journey"

type Journey struct {
	APIVersion  string      `yaml:"apiVersion"`
	Kind        string      `yaml:"kind"`
	ID          JourneyID   `yaml:"id"`
	Title       string      `yaml:"title"`
	Owner       string      `yaml:"owner"`
	Criticality string      `yaml:"criticality"` // critical | standard
	AuthProfile string      `yaml:"authProfile,omitempty"`
	Given       []Given     `yaml:"given,omitempty"`
	Legs        []Leg       `yaml:"legs"`
	Invariants  []Invariant `yaml:"invariants,omitempty"`
}

type Leg struct {
	ID      LegID     `yaml:"id"`
	Surface Surface   `yaml:"surface"`
	Service string    `yaml:"service"`
	Needs   []LegID   `yaml:"needs,omitempty"`
	Wait    *LegWait  `yaml:"wait,omitempty"`
	Steps   []LegStep `yaml:"steps"`
	Outputs []string  `yaml:"outputs,omitempty"`
}

type LegWait struct {
	Until   LegUntil      `yaml:"until"`
	Timeout time.Duration `yaml:"timeout"`
	Every   time.Duration `yaml:"every,omitempty"`
}

type LegUntil struct {
	JSONPath string `yaml:"jsonpath"`
	Equals   any    `yaml:"equals"`
}

// LegStep is a scenario step with the request that carries it out.
type LegStep struct {
	ID       StepID        `yaml:"id"`
	Setup    bool          `yaml:"setup,omitempty"`
	Action   string        `yaml:"action"`
	Hint     *RequestHint  `yaml:"hint"`
	Expect   []Expectation `yaml:"expect,omitempty"`
	Request  APIRequest    `yaml:"request"`
	Captures []Capture     `yaml:"captures,omitempty"`
}

type Invariant struct {
	Equal []string `yaml:"equal"`
}

type Manifest struct {
	Services []BuildRef `json:"services"` // sorted by service
	Hash     Hash       `json:"hash"`
}

type LegResult struct {
	Leg      LegID      `json:"leg"`
	Scenario ScenarioID `json:"scenario"` // <journey>.<leg>
	Outcome  Outcome    `json:"outcome"`
	Reasons  []Reason   `json:"reasons"`
}

type InvariantResult struct {
	Index    int        `json:"index"`
	State    CheckState `json:"state"`
	Values   []string   `json:"values"` // redacted, in the order of Equal
	Detail   string     `json:"detail,omitempty"`
}

type JourneyResult struct {
	Journey      JourneyID         `json:"journey"`
	Manifest     Manifest          `json:"manifest"`
	ManifestEnd  *Manifest         `json:"manifestEnd,omitempty"`
	Outcome      JourneyOutcome    `json:"outcome"`
	FirstFailing LegID             `json:"firstFailing,omitempty"`
	Legs         []LegResult       `json:"legs"`
	Invariants   []InvariantResult `json:"invariants"`
	Fixtures     []FixtureRecord   `json:"fixtures,omitempty"`
	Reasons      []Reason          `json:"reasons"`
}

func ManifestHash(services []BuildRef) Hash
```

`Run` gains `Journeys []JourneyResult \`json:"journeys,omitempty"\``. `ManifestHash` is SHA-256 of the JSON of the build references sorted by service, with the keys `service`, `commit`, `configHash`.

### `.semar/journeys/<id>.yaml`

```yaml
apiVersion: semar/v1
kind: Journey
id: checkout
title: An order is placed, paid, and shown as paid
owner: team-commerce
criticality: critical
legs:
  - id: place
    surface: api
    service: orders
    steps:
      - id: create
        action: Create an order of 1000
        hint: { method: POST, path: /v1/orders }
        request: { method: POST, path: /v1/orders, json: { amount: 1000, ref: "semar-{{run.id}}" } }
        captures: [{ name: id, jsonpath: "$.data.id" }, { name: amount, jsonpath: "$.data.amount" }]
        expect: [{ check: { status: 201 }, source: person }]
    outputs: [id, amount]
  - id: pay
    surface: api
    service: payments
    needs: [place]
    steps:
      - id: pay
        action: Pay the order
        hint: { method: POST, path: /v1/payments }
        request: { method: POST, path: /v1/payments, json: { orderId: "{{place.id}}" } }
        captures: [{ name: amount, jsonpath: "$.data.amount" }]
        expect: [{ check: { status: 202 }, source: person }]
    outputs: [amount]
  - id: settled
    surface: api
    service: orders
    needs: [pay]
    steps:
      - id: read
        action: Read the order until it is paid
        hint: { method: GET, path: "/v1/orders/{id}" }
        request: { method: GET, path: "/v1/orders/{{place.id}}" }
        captures: [{ name: amount, jsonpath: "$.data.amount" }]
        expect: [{ check: { jsonpath: "$.data.status", equals: paid }, source: person }]
    wait: { until: { jsonpath: "$.data.status", equals: paid }, timeout: 30s, every: 1s }
    outputs: [amount]
invariants:
  - equal: ["{{place.amount}}", "{{pay.amount}}", "{{settled.amount}}"]
```

| # | Rule. A problem is `<file>:<line>: <path>: <message>` | Example message |
| --- | --- | --- |
| J1 | Strict decode with `yamlfile.Decode` and `yamlfile.ExactNumbers` on `equals` and check operands. One document. At most 256 KiB | `unknown key schedule` |
| J2 | `apiVersion: semar/v1`, `kind: Journey`. `id` passes `idcheck.Check`, has no dot, and equals the file name | `id checkout does not match the file name check-out.yaml` |
| J3 | `title` is one line. `owner` is set and passes `idcheck.Check` | `owner is required: a named product or QA owner owns a journey` |
| J4 | `criticality` is `critical` or `standard` | `criticality must be critical or standard` |
| J5 | `given` follows the scenario rules of Plan 17. An alias is not a leg ID | `given[0].as: place is also a leg ID` |
| J6 | 1 to 12 legs. A leg ID matches `^[a-z][a-z0-9_]*$` and is unique. A hyphen is refused, because `{{<leg>.<output>}}` cannot hold one | `legs[2].id: pay is used twice` |
| J7 | `surface` is `api`. Any other value is refused | `legs[0].surface: web legs are not supported yet: this version runs api legs only; web legs come in M2 and android legs in M3` |
| J8 | `service` is set. A step's `request.service` is empty or equals it | `legs[1].steps[0].request.service: billing differs from the leg's service payments` |
| J9 | A leg's `given` is refused | `legs[0].given: put the fixtures of a journey under its given` |
| J10 | `needs` names earlier or later legs of the file, never itself. The graph has no cycle | `needs has a cycle: pay, settled, pay` |
| J11 | 1 to 20 steps per leg. Each step follows `scenario.Validate` for a step, has a hint, and its request matches the hint by `script.ValidateWith` | the existing messages, with the leg's path |
| J12 | A variable is a capture of an earlier step of the same leg, `run.id`, `<alias>.<output>` of the journey's `given`, or `<leg>.<output>` of a leg that this leg needs, directly or through other legs | `legs[1].steps[0].request.json.orderId: {{settled.id}} is an output of leg settled, which leg pay does not need` |
| J13 | `outputs` are capture names of the leg's own steps | `legs[0].outputs: total is not captured by any step of leg place` |
| J14 | `wait` needs `timeout` (above 0, at most 15m). `every` is at least 100ms and at most `timeout`. The leg's last step is `GET` or `HEAD`. `until.jsonpath` parses, and `equals` is a scalar | `legs[2].wait: the last step of leg settled is POST; a wait repeats the last step, which must read` |
| J15 | At most 8 invariants. `equal` has 2 to 6 entries. Each entry is exactly one `{{<leg>.<output>}}` or `{{<alias>.<output>}}`, and nothing else | `invariants[0].equal[1]: "total {{pay.amount}}" must be one variable and nothing else` |
| J16 | The leg scenario `<journey>.<leg>` passes `idcheck.Check` and is at most 128 bytes | `the leg scenario checkout.settled is too long` |

### `internal/journey` (new)

```go
type Problem struct{ File, Path, Message string }

func Parse(data []byte, name string) (domain.Journey, error)
func Validate(j domain.Journey, outputs func(domain.FixtureID) ([]string, bool)) []Problem // J2 to J16
func Load(repo string) ([]domain.Journey, []Problem, error) // .semar/journeys, the same link rules as Plan 17's catalogue

// Order is a topological order of needs. File order breaks ties.
func Order(j domain.Journey) ([]domain.LegID, error)

// LegScenario is the scenario and the script of one leg.
func LegScenario(j domain.Journey, leg domain.LegID, authProfile string) (domain.Scenario, domain.Script)
```

* `LegScenario` sets `ID: <journey>.<leg>`, `Ticket: <journey>`, `Title: <journey title>: <leg>`, `Intent: "Leg <leg> of journey <id>: <title>"`, `Surface: api`, `AuthProfile`, and the steps. The script has `ExpectHash: domain.ExpectHash(scenario)` and `AuthoredBy{Instance: "journey", Harness: "file"}`. Each request's service is the leg's service.

```go
type Runner struct{ /* deps */ }

type Deps struct {
	Replay    Single            // *replay.Runner
	Fixtures  Fixtures          // *fixture.Resolver; nil blocks a journey with given
	Catalogue Catalogue         // fixture.Catalogue, for the services that fixture steps call
	Builds    build.Prober
	Services  map[string]string // service name to auth profile, "" when none
	Clock     func() time.Time
	Sleep     func(ctx context.Context, d time.Duration) error
}

// Single is *replay.Runner. Its engine applies the effects policy, and it reads the
// confirmations with its own store and trust list, so the runner needs neither.
type Single interface {
	RunOne(ctx context.Context, in replay.Single) (replay.Replayed, error)
	Resend(ctx context.Context, c api.Call, attempt int, step domain.Step) (verdict.StepEvidence, map[string]string, error)
}

type Catalogue interface {
	Get(id domain.FixtureID) (domain.Fixture, bool)
}

type Fixtures interface {
	Resolve(ctx context.Context, req fixture.Request) (*fixture.Resolution, error)
	Release(ctx context.Context, res *fixture.Resolution) []domain.FixtureRecord
}

func New(d Deps) *Runner

// Run runs one journey and returns its result and the results of its legs, in run order.
func (r *Runner) Run(ctx context.Context, j domain.Journey) (domain.JourneyResult, []domain.ScenarioResult, error)

// Decide is the outcome table. It is pure.
func Decide(in Outcome) (domain.JourneyOutcome, domain.LegID, []domain.Reason)

type Outcome struct {
	ManifestProblem string
	FixtureProblem  []string
	Legs            []domain.LegResult // in run order; legs that did not start are not_run
	Invariants      []domain.InvariantResult
	Start           domain.Manifest
	End             *domain.Manifest // nil when the end could not be read
	Filtered        bool
}
```

`Run`, in order:

1. **Manifest.** For every leg service and every service that a step of a `given` fixture names, call `Builds.Probe`. A failure ends the journey `blocked` with `the build of payments could not be read`, and nothing is sent.
2. **Given.** `Fixtures.Resolve` with `Owner: <journey ID>`, the journey's `given`, and `Pass: setup`. A blocked text ends the journey `blocked`. `Release` is deferred with `context.WithoutCancel` and a 2 minute limit.
3. **Legs.** In `Order`. A leg starts only when every leg it needs has a provisional outcome of `passed`. For a leg, call `Replay.RunOne` with the leg scenario, its script, and the variables: `run.id`, the given outputs, and `<leg>.<name>` for each output of every leg it needs.
4. **Wait.** After the leg's steps, when `wait` is set and the last step has a response: repeat the last step's request with `Use.Attempt` 2, 3, and on, until the value at `until.jsonpath` equals `until.equals` or the timeout passes. The step's evidence is the last attempt. On a timeout the reason is `leg settled: waited 30s and $.data.status was never paid (6 attempts)`.
5. **Provisional outcome.** `verdict.Compute` with `BuildsEnd` set to the start builds (D8), then the wait rule of D9. When it is not `passed`, every leg that has not started is `not_run` with `stopped after leg <id>`, and the run goes to step 7.
6. **Outputs.** The captured values of the leg's outputs become `<leg>.<name>`. A missing output makes the leg `blocked` with `leg place: output id has no value`.
7. **Invariants.** Evaluated only when every leg passed. Otherwise `Invariants` is empty, so a failed leg is never turned into a `blocked` journey by an invariant that could not be read. A value that is not captured gives `not_evaluated` with `{{pay.amount}} was not captured`. A value with a redaction marker gives `not_evaluated` with `a value holds a redaction marker`. Otherwise `check.Same` decides.
8. **End.** Read every manifest service again. Compute every leg's final outcome with the end builds. Then `Decide`.

`Decide`, the first matching row wins and every matching reason is kept (TRD, Outcome):

| Order | Outcome | Condition | Reason text |
| --- | --- | --- | --- |
| 1 | `not_run` | `Filtered` | `the journey was not selected` |
| 2 | `blocked` | `ManifestProblem`, a `FixtureProblem`, the first leg that did not pass ended `blocked` or `inconclusive`, or an invariant is `not_evaluated` | the problem text; `leg pay ended blocked: <its first reason>`; `invariant 1 could not be evaluated: <detail>` |
| 3 | `stale` | A leg ended `stale`, `End` is nil after the journey started, or the hash of `End` differs from that of `Start` | `a build changed during the journey: orders 9f3c2ab then c0ffee1`; `the manifest could not be read at the end` |
| 4 | `failed` | The first leg that did not pass ended `failed`, or an invariant is false | `leg settled failed: <its first reason>`; `invariant 1 is false: 1000, 1000, 999` |
| 5 | `passed` | Every leg passed, every invariant is true, and the manifest held | none |

* `FirstFailing` is the first leg in run order whose outcome is not `passed`. It is set for every outcome other than `passed`.
* A leg outcome `not_run` from step 5 is never the first failing leg.
* A journey ends in exactly one of the five. `inconclusive` never appears in a `JourneyResult.Outcome`.
* When no row matches and a leg did not run, the journey is `not_run` with `leg pay was not run: <its reason>`, so a leg that did not run never reads as a pass.
* A leg's own reasons do not repeat its ID: `waited 30s and $.data.status was never paid (6 attempts)` and `output id has no value`. The journey reason adds it: `leg settled failed: waited 30s ...`.

### `internal/replay`

New file `internal/replay/single.go`. The body of `replay` is split so that both share it:

```go
type Single struct {
	Scenario domain.Scenario
	Script   domain.Script
	Vars     map[string]string // preset variables, such as <leg>.<name>; never stored
	Pass     domain.Pass       // scenario, as today
}

type Replayed struct {
	Steps     map[domain.StepID]verdict.StepEvidence
	Captured  map[string]string // every capture of every step; raw, never stored
	Blocked   []string
	Notes     []domain.Reason
	Confirmed func(domain.Expectation) bool
	Last      *api.Call         // the call of the last step sent, for a wait
}

func (r *Runner) RunOne(ctx context.Context, in Single) (Replayed, error)
func (r *Runner) Resend(ctx context.Context, c api.Call, attempt int, step domain.Step) (verdict.StepEvidence, map[string]string, error)
```

* `RunOne` validates the given script with `script.ValidateWith`, with every preset variable counted as known, and sends the steps as `replay` does. It does not probe builds and does not resolve fixtures.
* `Resend` sends the same call with `Use.Attempt` set, and evaluates the step's checks and invariants on the new action.
* `Run` keeps its behaviour. Every existing test of the package passes unchanged.

### `internal/check`

New file `internal/check/same.go`:

```go
// Same compares raw captured values exactly. Two decimals are equal when their values are
// equal, whatever their text. Otherwise the texts must be equal. ok is false when a value
// holds a redaction marker.
func Same(values []string) (equal, ok bool)
```

It reuses `ratOf` and `hasMarker`. `1000`, `1000.0`, and `1e3` are equal. `9007199254740993` and `9007199254740992` are not.

### Commands (`internal/cli`)

| Command | What it does | Exit |
| --- | --- | --- |
| `semar journey list [--env <name>] [--build ...]` | One line per journey file: ID, criticality, owner, the number of legs, and its last stored outcome with the run ID and the manifest hash prefix. Then each invalid file with its problems. With `--env`, Semar reads every manifest service now and shows `stale` for a result whose manifest differs, with the part that changed | 0, or 2 when a file is invalid |
| `semar journey run (<id>... \| --critical \| --all) --env <name> [--build ...]` | Creates a run with `Purpose: journey` and runs the journeys one after the other. Writes `report.md` and `run.json` with the leg results in `Results` and the journey results in `Journeys`. A production environment needs `--env` named | 0 every journey passed, 1 one failed, 2 otherwise |
| `semar confirm --journey <id>` (repeatable) | Shows the expected results of every leg that need a person and stores one confirmation per leg, under the leg scenario ID and its hash | as `semar confirm` |

* `--critical` selects every journey with `criticality: critical`. `--all` selects every valid journey. With a list of IDs, an unknown ID is an error before anything is sent.
* `semar journey run` prints one line per journey, such as `checkout  failed  first failing leg settled: waited 30s and $.data.status was never paid (6 attempts)`, then one line per leg.

### The report (`internal/report`)

`report.Data` gains `Journeys []domain.JourneyResult`. A section `## Journeys` comes first when the run has journeys: per journey, its outcome, the first failing leg, the manifest with each service and commit, each leg with its outcome and first reason, and each invariant with its redacted values. Every text is escaped.

### `semar plan` (FR-298)

* `plan.Input` gains `Journeys []domain.Journey`, loaded from the repository by `journey.Load`. An invalid file prints one line and is left out.
* After the draft is accepted, code lists every journey with a leg step whose service is a service of a drafted step and whose hint has the same method and the same path template as that drafted step's hint (`script.MatchPath` in both directions).
* `plan.md` gains `## Journeys to rerun` after "Fixtures needed": `- checkout (critical, owner team-commerce): leg settled calls GET /v1/orders/{id}, which SHOP-482-05 tests. Run: semar journey run checkout --env local`. With no journey file, the section is left out. With files and no match: `No journey calls a route that this plan tests.`

## Rules

| # | Rule |
| --- | --- |
| R1 | A journey ends in one of `passed`, `failed`, `blocked`, `stale`, `not_run`, and never `inconclusive` |
| R2 | Legs run one at a time, in a topological order of `needs`, with file order for ties |
| R3 | A leg reads only outputs of the legs it needs, the journey's `given` outputs, its own captures, and `run.id` |
| R4 | A leg is judged by `verdict.Compute`, and its expected results need confirmation as a scenario's do |
| R5 | A fixture failure blocks the journey and never fails it |
| R6 | A result is valid only for its manifest. A changed part shows it as `stale` |
| R7 | The report names the first failing leg in every outcome other than `passed` |
| R8 | Only `api` legs. Another surface is refused when the file loads |

## Tasks

| # | Task | Files it creates or edits | Interfaces it implements | Tests that prove it |
| --- | --- | --- | --- | --- |
| 1 | Domain types | Creates `internal/domain/journey.go`, `journey_test.go`. Edits `internal/domain/run.go` | The types, `ManifestHash`, `Run.Journeys` | `TestManifestHashSortsServices`: the same builds in another order give the same hash. `TestManifestHashChangesWithCommitAndConfig`. `TestRunJSONOmitsEmptyJourneys` |
| 2 | Journey files | Creates `internal/journey/parse.go`, `validate.go`, `order.go`, `leg.go`, their tests, `fuzz_test.go`, `internal/journey/testdata/` | `Parse`, `Validate`, `Load`, `Order`, `LegScenario` | `TestParseTheExample`. `TestValidateRules`: one case per rule J1 to J16 with its message, including `TestWebSurfaceRefused` and `TestAndroidSurfaceRefused` with the milestone text. `TestOrderTopologicalWithFileTies`. `TestCycleNamesLegs`. `TestBindingThroughNeeds`: a leg may read an output two needs away, and not a sibling's. `TestInvariantTemplateOneVariable`. `TestLegScenarioHashStable`: the hash of the example's legs does not change between two loads. `TestLoadRefusesLinks`. `FuzzParse`: never panics, and an accepted journey has an order |
| 3 | Exact comparison | Creates `internal/check/same.go`, `same_test.go` | `check.Same` | `TestSameDecimals`: `1000` and `1e3` equal; two integers above 2^53 that differ by one are not equal. `TestSameText`. `TestSameMarker`: `ok` false |
| 4 | One scenario with given variables | Creates `internal/replay/single.go`, `single_test.go`. Edits `internal/replay/runner.go` (the shared body) | `RunOne`, `Resend`, `Single`, `Replayed` | `TestRunOnePresetVars`: a path uses `{{place.id}}`. `TestRunOneRefusesUnknownVar`. `TestResendRecordsAttempt`: `Use.Attempt` 2 on the action. `TestRunUnchanged`: every test of `runner_test.go` passes |
| 5 | The journey runner | Creates `internal/journey/run.go`, `decide.go`, `run_test.go`, `decide_test.go` | `Runner.Run`, `Decide` | `TestDecideTable`: one case per row and the first-match order, with every reason kept. `TestNoInconclusiveJourney`: a leg that ends `inconclusive` gives `blocked`. Against the sample app with its payments service and an injected sleep: `TestPassed`. `TestFailedFirstLegStopsLater`: later legs are `not_run` with `stopped after leg pay`. `TestWaitTimeoutFails` (`PaymentNeverSettles`), with each attempt recorded. `TestInvariantFalse` (`PaymentAmountWrong`) names the three values. `TestInvariantMarkerBlocks`: a redacted capture gives `blocked`. `TestManifestProbeFailsBlocks`, with no request sent. `TestBuildChangesStale` (`BuildChanges`). `TestFixtureBlocks`, never `failed`. `TestPerServiceSession`: the payments leg carries the payments session. `TestCleanupAfterFailedLeg` |
| 6 | Commands, confirmation, and the report | Creates `internal/cli/journey.go`, `journey_test.go`. Edits `internal/cli/confirm.go`, `root.go` (one line), `internal/report/markdown.go`, `report.md.tmpl`, golden files, and `internal/cli/ledger.go` (Plan 19) to pass the journey results to the ledger | `semar journey list`, `semar journey run`, `semar confirm --journey`, the report section | `TestJourneyListShowsLastOutcome`. `TestJourneyListStaleWithEnv`: a stored result whose commit differs shows `stale` and the part. `TestJourneyRunUnknownIDSendsNothing`. `TestJourneyRunCritical` selects only critical journeys. `TestJourneyRunExitCodes`. `TestConfirmJourneyStoresOnePerLeg`. `TestReportJourneySection` (golden) with an escaped title. `TestLedgerReadsJourneyOutcome`: an obligation that names the journey takes its outcome |
| 7 | Journeys to rerun in a plan | Edits `internal/stage/plan/validate.go` (`Input.Journeys`), `render.go`, `render_test.go` and golden files, `internal/cli/plan.go` | The `plan.md` section | `TestJourneysToRerunMatchesHint`. `TestJourneysToRerunOtherServiceDoesNotMatch`. `TestNoJourneyFilesNoSection`. `TestInvalidJourneyFileOneLine` |
| 8 | End-to-end test | Creates `internal/cli/journey_e2e_test.go` and `testdata/e2e-journeys/` (`.semar/semar.yaml` with `orders` and `payments`, each with a build probe and an auth profile, and `.semar/journeys/checkout.yaml` from the example) | Nothing new | Through `cli.Run` against the sample app and its payments service: `semar journey run checkout --env local` is `passed`, exit 0, and `run.json` has `purpose: journey`, three leg results, and one journey result with its manifest. The source `person` needs no confirmation. With one expected result rewritten to `source: diff`, the journey is `blocked` with `leg place ended inconclusive`, and after `semar confirm --journey checkout --yes` it is `passed`. With `PaymentAmountWrong`: `failed`, the invariant line, exit 1. With `PaymentNeverSettles`: `failed`, first failing leg `settled`. A journey file with `surface: web` is refused by `semar journey list` with the J7 message, and `semar journey run --all` skips it with that message. `semar journey list --env local` after a run with `BuildChanges` shows `stale` |

## Out of scope

| Item | Reason |
| --- | --- |
| Web and Android legs, and screens | The engines are not built (FR-300 places them at M2 and M3). D1 |
| The owner of the failing leg's service, from the registry (FR-299) | M2. The registry does not exist |
| A schedule for nightly journeys (FR-292) | Semar has no scheduler. A team runs `semar journey run --all` from its own |
| Journeys in the organisation repository through packs | Packs are M2 (FR-323 to FR-332). A single-repository product keeps journeys in its own `.semar/journeys/` |
| The dashboard view of journeys per manifest (FR-345) | `semar journey list --env` shows the same facts |
| A leg's own `given` | D6 |

## Open points

1. That journeys across real services repeat reliably enough to gate a release is not verified (TRD).
2. That every service in a journey has a build probe is not verified (TRD). A service without one blocks every journey that uses it, unless `--build` declares its commit.
3. Matching journeys to a plan by drafted hints misses a route that the change touches and that no drafted step calls. The impact map of the TRD would close this.
4. The confirmation of a leg is keyed by its scenario ID. Renaming a journey or a leg lapses every confirmation of it.
5. Journey evidence goes to the run directory of the repository that holds the journey. The publish stage of the TRD does not exist yet.

`make check` must pass.
