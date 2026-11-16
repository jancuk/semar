# Plan 19: Obligations and the coverage ledger

Status: Specification, not implemented.

## Goal

Every use case that needs testing ends in one stated state, so that nothing is silently skipped. A use case is an obligation. Code gives each obligation one of five states from the outcomes of the scenarios that cover it.

1. `semar plan` writes the obligations of each ticket to `.semar/obligations/<ticket>.plan.yaml`, beside the scenario files, so the same merge request reviews both.
2. A person may add obligations by hand in any other file under `.semar/obligations/`, for example manual test cases copied from a spreadsheet.
3. `semar ledger` prints one state per obligation: `verified`, `failed`, `blocked`, `needs_a_person`, or `deferred`.
4. Every `needs_a_person` obligation has a manual checklist with exact steps and an expected result. A person records the result, and it is counted apart.
5. The report of `semar run` opens with one summary line, such as `42 use cases: 35 verified, 2 failed, 3 blocked, 2 need a person`.

The ledger sits above the six scenario outcomes and changes none of them.

## Needs first

* Plans 01, 12, and 14 (implemented).
* Tasks 1 to 4 touch only new packages and `internal/cli/planwrite.go`, which no other plan of 16 to 20 edits. They can run in parallel with Plan 17.
* Task 5 needs Plan 17 task 10, because both edit `internal/cli/run.go` and `internal/report`.
* Plan 18 task 6 wires journey outcomes into the ledger through the field `Results.Journeys` that task 3 defines.

## Implements

* TRD: [Test obligations and the coverage ledger](../TRD.md#test-obligations-and-the-coverage-ledger) (where obligations come from, the file, rules 1 to 7, how code derives a state, the manual checklist, failure modes), [Where the planned parts run](../TRD.md#where-the-planned-parts-run) item 7, and [Pipeline](../TRD.md#pipeline) (Ledger).
* PRD: decisions D53 (the part that nothing is silently skipped) and D54, product rule 14, goals 26 and 33, and FR-261 (in part, see D2), FR-262, FR-263, FR-264, FR-265, FR-266, FR-267, FR-268, FR-269, FR-270.

## Order and parallel safety

| Task | Must be merged before it starts | Can run in parallel with |
| --- | --- | --- |
| 1 The obligations file | Nothing | Every task of Plans 17 and 20 |
| 2 Deferrals and manual results | Nothing | 1, 3, Plans 17 and 20 |
| 3 Deriving the states, the summary, and the checklist | 1, 2 | Plans 17 and 20 |
| 4 The plan writes obligations | 1 | 2, 3, Plans 17 and 20 |
| 5 `semar ledger` and the report line | 3, 4, Plan 17 task 10 | Plan 16 tasks C to E, Plan 18 tasks 1 to 5 |
| 6 End-to-end test | 5 | Nothing |

Task 5 adds `newLedgerCommand(o)` to `internal/cli/root.go`, after the lines of Plans 20 and 17.

## Decisions

| # | Decision | Reason |
| --- | --- | --- |
| D1 | Obligations are files under `.semar/obligations/`, not `.semar/scenarios/<ticket>/obligations.yaml` as the TRD shows | `scenarioFiles` in `internal/cli/scenarios.go` reads every `.yaml` under `.semar/scenarios` as a scenario named by its file, so an `obligations.yaml` there would be read as scenario `obligations` and fail. A folder of its own avoids that and keeps every scenario command unchanged |
| D2 | In this version obligations come from what exists: one per drafted scenario, one per `notDrafted` entry, one per `uncovered` file, one per document difference of Plan 14, and the hand-written files. The planner does not propose obligations, and no criterion, feature map, or past defect is read | No tracker port reads acceptance criteria, and the feature map is not built. Each of these sources is in `draft.json` or on disk today, so code derives them with no model and no change to the planner |
| D3 | The plan writes one file per ticket, `<ticket>.plan.yaml`. A later plan of the same ticket adds entries and never removes or changes one | A person may have edited the file, and an obligation that disappears silently is the gap this plan exists to close. A person deletes an entry that no longer applies, in a reviewed commit |
| D4 | A `notDrafted` entry and an `uncovered` file become obligations with a `gap`: a declared reason with no cover. They derive `blocked` with that reason | FR-267: no cover and no declared reason is a defect. A gap is a declared reason. It still must not look like a pass |
| D5 | A document difference becomes `needs_a_person`, kind `judgement`, with steps written by code | Code cannot tell whether the document or the code is right (Plan 14). Only a person can, and the checklist says what to read |
| D6 | The types live in a new package `internal/obligation`, not in `internal/domain` as the TRD shows | Plan 17 owns the changes to `internal/domain`. A package of its own lets this plan run in parallel. Nothing outside the ledger needs the types |
| D7 | The package is named `obligation`, and the report calls the line "use cases" | `internal/report` already has a "ledger": the data ledger of records created and changed (`ledgerView`). Two meanings of one word in one report would confuse a reader |
| D8 | A deferral and a manual result are stored in the evidence store under `.semar/store/obligations/`, with the person's name and "local, unverified identity", as a confirmation is | TRD: a person records them with a command, and they are stored like a confirmation. One identity rule for every record a person makes |
| D9 | A result counts from the newest complete run that holds it. With `--env`, Semar reads the builds now, and a result on another build counts as `stale` | TRD: only results on the builds that UAT runs now count. Reading builds needs the environment, so it is an explicit flag. Without it, `semar ledger` says once that builds were not read |
| D10 | In the report of `semar run`, the builds of that run are the current builds | The run has just read them. No second probe is needed |
| D11 | Journey covers are read from `Results.Journeys`, which is nil until Plan 18 fills it. With nil, a journey cover is not `passed` and its reason says that journey results are not read | The ledger can be built before journeys, and the state never claims more than it saw |
| D12 | `semar ledger` exits 0 when every obligation is `verified` or `deferred` and every manual result is `passed`, 1 when one is `failed` or a manual result is `failed`, and 2 otherwise. With no obligation to list it prints `no use case: there is no obligation in .semar/obligations` and exits 2. A problem in an obligations file stops it with exit 3 and lists each problem, so that no state comes from a file that cannot be trusted | The exit codes of `semar run`, with the ledger's own facts |

## Trust

Untrusted: the repository, every file under `.semar/` including `.semar/obligations/` and `.semar/store/`, the draft of the planner, and stored runs. Trusted: the person's environment and the person who runs the command.

| Risk | Answer |
| --- | --- |
| A model marks a hard case as manual to skip it | The planner writes no `manual` reason in this version. Code writes `manual` only for a document difference, and a hand-written file is reviewed in the merge request |
| A committed deferral hides a gap | A deferral gives `deferred`, never `verified`. It shows the name, the reason, and the expiry. An expired one is ignored and says so. A `failed` cover shows even under a deferral |
| A committed manual result | A manual result never changes the state. It is shown as "checked by a person" and counted apart (FR-269). Its identity is local and not verified (FR-11) |
| Text in an obligation title or a cite holds instructions or terminal sequences | It is data. `semar ledger` prints it through `terminalSafe`, and the report escapes it |
| A cite that is not true | A cite is shown, not checked, for the sources that have no port: `testcase`, `criterion`, `defect`, `feature_map`. The checklist labels it as not checked |
| A large or repetitive file | 256 KiB per file, 100 files, 500 obligations per file, 5000 in all, and a fuzz test on `obligation.Parse` |

## Contract

### `.semar/obligations/*.yaml`

```yaml
apiVersion: semar/v1
kind: Obligations
ticket: SHOP-482                  # optional; when set, every id starts with SHOP-482/
obligations:
  - id: SHOP-482/scenario-SHOP-482-01
    title: Refund above the order total is rejected
    source: diff
    covers: [SHOP-482-01]
  - id: SHOP-482/not-drafted-1
    title: A refund of a paid order by another customer
    source: diff
    gap: "not drafted: needs fixture order.paid"
  - id: SHOP-482/uncovered-1
    title: Changes in internal/refund/notify.go
    source: diff
    gap: "no scenario covers internal/refund/notify.go: it only sends an email"
  - id: SHOP-482/difference-1
    title: "The document and the code differ: the status of a refund above the total"
    source: knowledge
    cite: "docs/refunds.md:14"
    manual:
      kind: judgement
      text: A person decides whether the document or the code is right
      steps:
        - "Read docs/refunds.md:14: the refusal is 409."
        - "Read internal/refund/handler.go:41: the refusal is 422."
        - Decide which one is right, and change the other, or the scenario.
      expected: The document and the code agree
      writtenBy: code
  - id: refunds/refund-email
    title: The customer gets an email about the rejection
    source: testcase
    cite: "TC-1042: the customer is told about the rejection by email"
    journeys: []
    manual:
      kind: other
      text: The email leaves the test environment
      steps: ["Sign in to the test mailbox of semar-qa-1.", "Find the email sent after the run."]
      expected: The customer is told about the rejection by email
      writtenBy: person
```

| # | Rule. A problem is `<file>:<line>: <path>: <message>` | Example message |
| --- | --- | --- |
| O1 | Strict decode (G10). One document. At most 256 KiB, 500 obligations | `unknown key state: a state is computed by code and cannot be written` |
| O2 | `apiVersion: semar/v1`, `kind: Obligations`. `ticket`, when set, passes `idcheck.Check` | `kind must be Obligations` |
| O3 | `id` is `<group>/<name>`, each part passes `idcheck.Check`, at most 128 bytes, unique across every file. With `ticket`, the group is the ticket | `id SHOP-482/AC-1 is also in .semar/obligations/refunds.yaml` |
| O4 | `title` is one line of 1 to 300 characters | `title is required` |
| O5 | `source` is `criterion`, `diff`, `knowledge`, `testcase`, `feature_map`, or `defect` | `source review is not a source; use one of criterion, diff, knowledge, testcase, feature_map, defect` |
| O6 | `cite` is required for `criterion`, `testcase`, and `defect`, at most 500 characters | `cite is required for the source testcase` |
| O7 | `covers` are scenario IDs that pass `idcheck.Check`. `journeys` are journey IDs that pass it | `covers[0]: invalid scenario ID` |
| O8 | `gap` is one line of at most 300 characters | `gap must be one line` |
| O9 | `manual.kind` is `physical_device`, `third_party_otp`, `judgement`, or `other`. `text` and `expected` are set. `steps` has 1 to 20 lines. `writtenBy` is `person`, `code`, or `ai` | `manual.steps needs at least one step` |
| O10 | `ticket` key at the top and in an entry are the same key. An entry may set `ticket` only in a file with no top-level ticket | `ticket SHOP-480 differs from the file's ticket SHOP-482` |

An obligation with no `covers`, no `journeys`, no `gap`, and no `manual` is valid. It derives `blocked` and is listed first (FR-267, TRD rule 2).

### `internal/obligation` (new)

```go
type Source string
type State string

const (
	Verified     State = "verified"
	Failed       State = "failed"
	Blocked      State = "blocked"
	NeedsAPerson State = "needs_a_person"
	Deferred     State = "deferred"
)

type Obligation struct {
	ID       string              `yaml:"id"`
	Title    string              `yaml:"title"`
	Source   Source              `yaml:"source"`
	Cite     string              `yaml:"cite,omitempty"`
	Ticket   string              `yaml:"ticket,omitempty"`
	Covers   []domain.ScenarioID `yaml:"covers,omitempty"`
	Journeys []string            `yaml:"journeys,omitempty"`
	Gap      string              `yaml:"gap,omitempty"`
	Manual   *Manual             `yaml:"manual,omitempty"`
}

type Manual struct {
	Kind      string   `yaml:"kind"`
	Text      string   `yaml:"text"`
	Steps     []string `yaml:"steps"`
	Expected  string   `yaml:"expected"`
	WrittenBy string   `yaml:"writtenBy"`
}

type File struct {
	APIVersion  string       `yaml:"apiVersion"`
	Kind        string       `yaml:"kind"`
	Ticket      string       `yaml:"ticket,omitempty"`
	Obligations []Obligation `yaml:"obligations"`
}

type Problem struct{ File, Path, Message string }

func Parse(data []byte, name string) (File, error)
func Validate(f File, name string) []Problem
func Load(repo string) (list []Located, problems []Problem, err error) // every file, O3 across files
type Located struct {
	Obligation
	File string // the path from the repository root
	Line int
}
```

`Load` applies the link rules of `scenarioFiles`: `.semar` and `.semar/obligations` are real directories when they exist, and a link or a special file under them is an error.

#### Records a person makes

```go
type Deferral struct {
	Schema     int       `json:"schema"` // 1
	Obligation string    `json:"obligation"`
	User       string    `json:"user"`
	Identity   string    `json:"identity"`
	At         time.Time `json:"at"`
	Reason     string    `json:"reason"`
	Until      time.Time `json:"until"` // the end of that day, UTC
}

type ManualResult struct {
	Schema     int       `json:"schema"` // 1
	Obligation string    `json:"obligation"`
	Result     string    `json:"result"` // passed | failed
	User       string    `json:"user"`
	Identity   string    `json:"identity"`
	At         time.Time `json:"at"`
	Notes      string    `json:"notes"`
}

func NewStore(root string) *Store // root is the evidence store root
func (s *Store) Defer(d Deferral) error
func (s *Store) Record(m ManualResult) error
func (s *Store) Deferrals() (map[string]Deferral, error)     // the newest per obligation
func (s *Store) Manual() (map[string]ManualResult, error)    // the newest per obligation
```

* Paths: `<root>/store/obligations/<group>/<name>/deferral/<user>-<unix seconds>.json` and `.../manual/<user>-<unix seconds>.json`. Both parts of the ID pass `idcheck.Check`. The user name goes through the cleaner of `confirm.fileName`. Every write and read goes through an `os.Root` with the checks of a confirmation: no link, a regular file, no swap during the read, no unknown field, schema 1, and the obligation in the record equals its directory.
* A new record never replaces an old one. The newest by `At` counts.

#### Deriving a state

```go
// Cover is what one covering item gave.
type Cover struct {
	Scenario domain.ScenarioID
	Journey  string
	Found    bool           // a result exists
	Outcome  string         // a scenario outcome or a journey outcome
	Run      domain.RunID
	Reason   string         // the first reason of the result
	Builds   []domain.BuildRef
}

type Results struct {
	Scenarios map[domain.ScenarioID]Cover
	Journeys  map[string]Cover // nil until Plan 18 wires it
	Current   []domain.BuildRef // nil when the builds were not read
}

type Entry struct {
	Obligation Located
	State      State
	Reasons    []string
	Covers     []Cover
	Manual     *ManualResult
	Deferral   *Deferral
	Expired    *Deferral // a deferral that was ignored because it expired
	NoReason   bool      // no cover, no gap, and no manual reason
}

func Derive(obs []Located, r Results, deferrals map[string]Deferral, manual map[string]ManualResult, now time.Time) []Entry
func SummaryLine(entries []Entry) string
func Checklist(w io.Writer, entries []Entry, group string) error
```

`Derive` is pure (G9). The first matching row gives the state (TRD, How code derives a state):

| Order | State | Condition | Reason text |
| --- | --- | --- | --- |
| 1 | `failed` | A covering scenario or journey is `failed`. This shows even under a deferral or a manual reason | `SHOP-482-01 failed in r-20261117T090000Z-1a2b: <its first reason>` |
| 2 | `deferred` | A deferral exists and `Until` is after `now` | `deferred by tester until 2026-12-01: <reason>` |
| 3 | `needs_a_person` | `manual` is set | `<manual.text>`, and with a manual result `passed, checked by a person` or `failed, checked by a person` |
| 4 | `verified` | At least one cover, and every covering scenario and journey is `passed` on a current build | none |
| 5 | `blocked` | Anything else | one per cover that is not `passed`: `SHOP-482-02 is inconclusive in r-...: an expected result is not confirmed`; `SHOP-482-03 has no result: run it`; `SHOP-482-04 was deleted: no scenario or journey covers this`; `SHOP-482-01 ran on orders 9f3c2ab, and the environment runs c0ffee1: rerun on the current build`; `journey checkout: journey results are not read yet`; the `gap` text; or `use case with no test and no reason` |

* A result is on a current build when `Current` is nil, or when every service of `Current` has the same commit and configuration hash in the result's end builds. Otherwise the cover counts as `stale`.
* A covering scenario whose file no longer exists is "deleted", and a file that exists with no result is "no result".
* An expired deferral sets `Expired`, adds `the deferral by tester expired on 2026-11-01`, and the state is derived again without it (TRD rule 4).
* A manual result never changes the state (FR-269, TRD rule 6).

`SummaryLine`:

* `<n> use cases: <v> verified` and then, in this order and only when above zero, `<f> failed`, `<b> blocked`, `<p> need a person`, `<d> deferred`. One obligation reads `1 use case`, and one person reads `1 needs a person`. With no obligation the line is empty.
* When manual results exist, a second sentence: `Checked by a person: 1 passed, 1 failed.`

`Checklist` writes the TRD format for every `needs_a_person` entry of the group, or of every group when `group` is empty:

```text
Manual checks for SHOP-482 (1)
1. SHOP-482/difference-1  The document and the code differ: the status of a refund above the total
   Why a person: A person decides whether the document or the code is right
   Steps (written by code):
     1. Read docs/refunds.md:14: the refusal is 409.
     2. Read internal/refund/handler.go:41: the refusal is 422.
     3. Decide which one is right, and change the other, or the scenario.
   Expected: The document and the code agree
   Cite: docs/refunds.md:14 (not checked by code)
   Record:   semar ledger record SHOP-482/difference-1 passed|failed --notes "<what you saw>"
```

The label is `written by code`, `written by a person`, or `written by AI`, from `writtenBy`. The cite of a `knowledge` source cited as `<file>:<line>` was checked by Plan 14 when the plan was made, and the line says `(checked when the plan was made)`.

#### What the plan writes

```go
// FromPlan returns the obligations files of a plan, one per ticket, with the entries of
// existing files kept and new entries added after them.
func FromPlan(cs domain.ChangeSet, d plan.Draft, scenarios []domain.Scenario, existing map[string]File) (map[string]File, error)
```

| Draft item | Obligation | ID |
| --- | --- | --- |
| Each written scenario | `title` from the scenario, `source: diff`, `covers: [<id>]` | `<ticket>/scenario-<scenario id>` |
| Each `notDrafted` entry | `title` from `what`, `source: diff`, `gap: "not drafted: needs <needs>"` | `<ticket>/not-drafted-<n>` |
| Each `uncovered` file | `title: Changes in <file>`, `source: diff`, `gap: "no scenario covers <file>: <reason>"`. The ticket is the first ticket, in key order, whose `TicketChange.Files` holds the file, else the first ticket of the change set | `<ticket>/uncovered-<n>` |
| Each difference of Plan 14 | as in the example: `source: knowledge`, `cite` the document place, a `judgement` manual reason with three steps written by code | `<ticket>/difference-<n>` |

* `<n>` continues after the highest number of that kind in the existing file.
* A `notDrafted`, `uncovered`, or difference entry whose title, with spaces folded and in lower case, equals the title of an existing entry of the same kind is not added again.
* A text from the draft is untrusted. It is cut to the limits of O4, O8, and O9 and written through the YAML encoder, so it cannot add a key.

`writePlan` in `internal/cli/planwrite.go` reads each existing `.semar/obligations/<ticket>.plan.yaml` through its root, calls `FromPlan`, and writes the files with the scenario files, all or none (R21 of Plan 12). `planWriter` gains `replaced map[string][]byte`, so that `undo` puts back a file that it replaced. An existing file that does not parse stops the plan before any write: `.semar/obligations/SHOP-482.plan.yaml: <problem>; fix or remove it, and plan again; nothing was written`.

### `internal/cli`

| Command | What it does | Exit |
| --- | --- | --- |
| `semar ledger [--ticket <key>]... [--env <name>] [--build ...] [--run <id>] [--checklist]` | Loads the obligations, the stored records, and the newest result of each covering scenario from the complete runs in `.semar/runs` with an empty purpose or `journey`. Prints the summary line, then the entries grouped by state in the order: no test and no reason, failed, blocked, needs a person, deferred, verified. Each line is the ID, the title, and the first reason. With `--checklist`, prints the checklist instead. With `--run <id>`, reads the results of that complete run only and prints the manual checklist of the `needs_a_person` entries after the table. A `--ticket` key matches the ticket of an obligation, or the group of its ID when it has no ticket. With `--env`, reads the builds now (D9), and a build that cannot be read is an error, because every result would otherwise count as current. `--build` needs `--env`. Without it, prints `semar: the builds that the environment runs now were not read; pass --env to count only results on the current builds` on standard error | D12 |
| `semar ledger defer <id> --reason <text> --until <YYYY-MM-DD>` | Stores a deferral. `--until` is after today and at most 180 days ahead. The reason is one line of 1 to 300 characters. The obligation must exist | 0, or 3 with the problem |
| `semar ledger record <id> passed\|failed --notes <text>` | Stores a manual result. The obligation must be `needs_a_person` now. Notes are at most 2000 characters | 0, or 3 |

The name comes from `options.User`, as for `semar confirm`, and a record is refused when it is empty.

`semar run` computes the entries of every obligation whose ticket or covers touch a scenario of the run, with the run's own results first and the builds of the run as `Current` (D10). `report.Data` gains `Coverage string`, the summary line, and `CoverageTickets []string`, the keys of those obligations. The report template prints the line first after the title, then ``Details: `semar ledger --ticket <key>` `` with one `--ticket` for each key (a key that is not safe as an ID is left out). With no such obligation, nothing is printed. `report.Summary` takes the line as a third argument and prints it after the counts. A run that did not finish prints no line. A problem in an obligations file, or an unreadable record, leaves the line out and says so on standard error, because a report must not fail for a ledger.

## Rules

| # | Rule |
| --- | --- |
| L1 | Each obligation has exactly one of the five states, computed by code. No file and no agent sets a state (FR-262, TRD rule 1) |
| L2 | `verified` only when every cover passed, on a current build (FR-263) |
| L3 | A `failed` cover shows as `failed` under a deferral or a manual reason (FR-264) |
| L4 | No cover, no gap, and no manual reason is a defect of the plan, listed first (FR-267) |
| L5 | A manual result is counted apart and labelled "checked by a person". It never counts as a scenario pass (FR-269) |
| L6 | The ledger never changes a scenario outcome or a journey outcome (TRD rule 7) |
| L7 | A deferral needs a name, a reason, and an expiry. An expired one is ignored and reported (FR-266) |

## Tasks

| # | Task | Files it creates or edits | Interfaces it implements | Tests that prove it |
| --- | --- | --- | --- | --- |
| 1 | The obligations file | Creates `internal/obligation/file.go`, `file_test.go`, `fuzz_test.go`, `internal/obligation/testdata/` | `Parse`, `Validate`, `Load` | `TestParseTheExample`. `TestValidateRules`: one case per rule O1 to O10 with its message, including `TestStateKeyRefused`. `TestDuplicateIDAcrossFiles` names both files. `TestLoadRefusesLinks`. `TestLoadLimits`. `TestTitleWithEscapeIsData`: a title with `\x1b[2J` loads and prints escaped. `FuzzParse`: never panics, and every accepted file validates the same after a round trip |
| 2 | Deferrals and manual results | Creates `internal/obligation/store.go`, `store_test.go` | `Store` | `TestDeferAndReadNewest`. `TestRecordNeverReplaces`: two records, the newest counts, both files stay. `TestStoreRefusesLinkSwapAndWrongDirectory`. `TestUserNameCleaned`: `../x` is stored as `.._x-<seconds>.json` inside the directory |
| 3 | Deriving the states | Creates `internal/obligation/derive.go`, `summary.go`, `checklist.go`, their tests and golden files | `Derive`, `SummaryLine`, `Checklist`, `Results`, `Cover` | `TestDeriveTable`: one case per row and the first-match order. `TestFailedShowsUnderDeferral`. `TestExpiredDeferralIgnored`. `TestManualResultDoesNotChangeState`: a recorded `failed` keeps `needs_a_person` with `failed, checked by a person`. `TestInconclusiveStaleNotRunAreBlocked`, each with its reason. `TestDeletedScenarioBlocked`. `TestStaleByCurrentBuilds`. `TestNoReasonListedFirst`. `TestJourneyCoverWithoutResults`. `TestSummaryLine`: the PRD example `42 use cases: 35 verified, 2 failed, 3 blocked, 2 need a person`, the singular forms, and the empty line. `TestChecklistGolden` with each `writtenBy` label |
| 4 | The plan writes obligations | Creates `internal/obligation/fromplan.go`, `fromplan_test.go`. Edits `internal/cli/planwrite.go`, `plan_write_test.go` | `FromPlan`, the file write in `writePlan` | `TestFromPlanOnePerItem`: a draft with two scenarios, one `notDrafted`, one `uncovered`, and one difference gives five entries with the IDs of the table. `TestFromPlanKeepsExisting`: an edited entry stays as edited, and new numbers continue. `TestFromPlanNoDuplicateGap`. `TestUncoveredTicketFromChangeSet`. `TestDraftTextCannotAddKey`: a `what` holding `"\nstate: verified"` is written as one string. `TestWritePlanRestoresReplacedFile`: a failure after the obligations file was replaced puts the old bytes back. `TestWritePlanStopsOnBadExistingFile`: nothing written |
| 5 | `semar ledger` and the report line | Creates `internal/cli/ledger.go`, `ledger_test.go`. Edits `internal/cli/run.go`, `root.go` (one line), `internal/report/markdown.go`, `report.md.tmpl`, `summary.go`, their tests and golden files | `semar ledger`, `ledger defer`, `ledger record`, `report.Data.Coverage` | `TestLedgerGroupsAndExitCodes`: one case per exit code. `TestLedgerWithoutEnvSaysSo`. `TestLedgerEnvMarksStale`. `TestDeferValidation`: a past date, 181 days, an empty reason, an unknown ID. `TestRecordOnlyForNeedsAPerson`. `TestRecordNeedsAName`. `TestLedgerSkipsTrialAndCheckRuns`: a run with purpose `trial` or `fixture-check` is never read. `TestReportOpensWithCoverageLine` (golden). `TestRunSummaryHasCoverageLine` |
| 6 | End-to-end test | Creates `internal/cli/ledger_e2e_test.go` | Nothing new | Through `cli.Run` on a copy of `testdata/e2e` with the sample app: the test writes `.semar/obligations/SHOP-482.plan.yaml` with the scenario entry, a gap entry, and a manual entry, and `.semar/obligations/refunds.yaml` with one entry that sets `ticket: SHOP-482` and has no cover, so that the run counts it. After `semar confirm SHOP-482-01 --yes`, `semar run --all` writes a report whose first line after the title is `4 use cases: 1 verified, 2 blocked, 1 needs a person`. `semar ledger` lists `refunds/...` first as "use case with no test and no reason" and exits 2. `semar ledger record SHOP-482/... failed --notes x` keeps the state and makes the exit 1. `semar ledger defer refunds/... --reason r --until <today+7>` makes it `deferred`. With `RefundOverTotal`, the scenario fails and its obligation is `failed`, also after a deferral is added for it. `semar ledger --checklist` prints the manual entry with its record command |

## Out of scope

| Item | Reason |
| --- | --- |
| Obligations from the ticket's acceptance criteria, the feature map, and past defects (part of FR-261) | No tracker port and no feature map exist. A hand-written file carries them until then |
| The planner proposing obligations and manual reasons | It needs a prompt and a schema change, and TRD rule 3 asks for a yes or no question for each manual proposal. It belongs with the tracker port |
| Importing manual test cases from a tracker or a spreadsheet (FR-339) | Fidelity is not verified, and an import is a draft that needs review. A person copies cases into a hand-written file |
| Readiness of a ticket that reads the ledger (TRD rule 8) | Readiness is not built |
| The random sample of passes and the seeded-defect check of D53 | They measure the false-pass rate, which is a separate feature with its own record |
| The dashboard view (FR-346) | `semar ledger` shows the same facts |

## Open points

1. That the obligations derived here are the ones a QA engineer would list is not verified (TRD). They are as good as the plan's scenarios and gaps. The shadow milestone compares them.
2. A deferral and a manual result are stored in the repository's evidence store with a local, unverified identity, as a confirmation is (FR-11).
3. An obligation that a later plan makes obsolete stays until a person removes it. A scenario that a person deleted makes its obligation `blocked`, which is the intended signal.
4. Results are matched to the newest complete run. A run on another environment can be the newest. `--env` with the builds narrows this, and a filter by environment may be needed.

`make check` must pass.
