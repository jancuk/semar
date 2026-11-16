# Plan 20: Exports

Status: Implemented. The text describes the code as built.

## Goal

The results of a run reach the tools a team already runs. `semar export` writes one stored run in a common format:

1. `semar export ctrf <run>` writes a CTRF JSON report.
2. `semar export junit <run>` writes JUnit XML.
3. `semar export sarif <run>` writes the confirmed defects of the run as SARIF 2.1.0 results.

An export reads the stored, redacted evidence and computes nothing new. It never changes an outcome.

## Needs first

Plan 01 (implemented): the evidence store, `run.json`, the activity log, and the action files. Nothing of Plans 16 to 19. Every task touches only new files, apart from one line in `internal/cli/root.go`.

## Implements

* TRD: [Standards and interchange](../TRD.md#standards-and-interchange), the rows Reports and Findings.
* PRD: decision D68 (the export part), goal 31 (the export part), testing practice Q8, and FR-188 (JUnit XML), FR-335 (CTRF), FR-336 (SARIF).

## Order and parallel safety

| Task | Must be merged before it starts | Can run in parallel with |
| --- | --- | --- |
| 1 Read an action file | Nothing | Every task of Plans 17 and 19 |
| 2 The export model | 1 | Plans 17 and 19 |
| 3 CTRF | 2 | 4, 5 |
| 4 JUnit XML | 2 | 3, 5 |
| 5 SARIF | 2 | 3, 4 |
| 6 The command | 3, 4, 5 | Plans 17 and 19 |
| 7 End-to-end test | 6 | Nothing |

Task 6 adds `newExportCommand()` to `internal/cli/root.go` first, before the lines of Plans 17, 19, and 18.

## Decisions

| # | Decision | Reason |
| --- | --- | --- |
| D1 | Each format writes only the fields this plan names, and the tests check them against a strict Go mirror of the required shape. No JSON Schema or XML Schema validator is added | G18: a validator is a new dependency. The fields used are few, and a mirror decoded with unknown fields refused proves that Semar writes exactly them |
| D2 | CTRF: the object `results` with `tool`, `summary`, and `tests`. The keys `reportFormat` and `specVersion` are not written | The required core of CTRF as this plan knows it is `results.tool.name`, the seven counts with `start` and `stop`, and per test `name`, `status`, and `duration`. Whether the current CTRF schema requires a format and a version key, and which value, is not verified (D68). Writing a version that may be wrong is worse than leaving it out |
| D3 | JUnit XML is the common dialect with `testsuites`, `testsuite`, `testcase`, `failure`, `error`, `skipped`, `properties`, and `system-out`. No XML Schema is claimed | JUnit XML has no single published schema that every reader follows. This dialect is not verified against a reader in this plan (D68) |
| D4 | SARIF version `2.1.0`. The `$schema` key is not written | SARIF 2.1.0 is the OASIS standard version. `$schema` is optional in a SARIF log, and the exact address of the published schema is not verified here |
| D5 | Outcome mapping: CTRF `passed` to `passed`, `failed` to `failed`, `not_run` to `skipped`, and `blocked`, `inconclusive`, `stale` to `other`. JUnit `failed` to `failure`, `not_run` to `skipped`, and `blocked`, `inconclusive`, `stale` to `error`. Every export also carries the Semar outcome itself | A blocked or inconclusive scenario is not a pass. In JUnit, `error` keeps a pipeline from showing it as green. CTRF's `other` exists for a state it does not name. The Semar outcome is kept so that nothing of the six is lost |
| D6 | SARIF holds only `failed` scenarios: one result per false check on a confirmed expected result, and one per broken invariant. Nothing else | SARIF is for findings. A `blocked` or `inconclusive` scenario is not a finding about the product. The security findings and the exploration findings of the TRD are not built |
| D7 | An export refuses a run that is interrupted, a run whose evidence changed after it was written, and a run with no scenario result | An export must not present evidence that `semar run` itself would not trust. A run with no result is a trial or a fixture check (Plans 16 and 17), which has nothing to export |
| D8 | The name of a test is `<scenario id> <title>`, with the title read from the scenario file now. A scenario file that is gone gives the ID only | `run.json` does not store titles. A title is a label, not evidence |
| D9 | The duration of a scenario is the sum of `durationMs` of every action recorded for it, in any pass | It is the time Semar spent on that scenario, including its setup |
| D10 | An export writes to standard output, or with `--out <file>` to a new file with mode 0644. An existing file is refused unless `--force` | Exports go to CI systems and are not secret: the evidence they read is redacted. Overwriting a file by mistake loses another report |
| D11 | The exit code of `semar export` is 0 when the file was written and 3 on an error. It does not reflect outcomes | An export is not a gate. `semar run` already exits 0, 1, or 2 from the outcomes |
| D12 | Every text is cut: a message to 1000 characters and a detail to 8000, with the marker `[cut: 123 more characters]` | A report that a CI system shows must stay readable, and an observed value can be large |
| D13 | A run with journeys also exports one test per journey, after the tests of its legs, named `journey <id>`, in the suite `semar journeys` of JUnit and CTRF. The mapping is D5 with the journey outcome: `passed` to `passed`, `failed` to `failed` (JUnit `failure`), `not_run` to `skipped`, and `blocked` and `stale` to `other` (JUnit `error`). The reasons of the journey are the message. SARIF has one result of rule `semar/journey-failed` for each failed journey, which names the first failing leg and the reasons, among them each false invariant | Legs are scenario results, and every leg can pass while an invariant is false, so a gate that reads only the legs goes green on a journey that `semar journey run` fails. Blocked and stale keep the mapping of a blocked or stale scenario, so that neither reads as green |

## Trust

Untrusted: every stored file of the run, the scenario files, and every value that a service returned. The person who runs the command is trusted.

| Risk | Answer |
| --- | --- |
| A stored value carries a secret | Every action was redacted by the recorder before it was stored. The export reads only stored files. It adds no request and no header value. Reasons and check results come from `run.json`, which holds redacted values |
| Evidence edited after the run | `Store.Verify` runs first. A changed file refuses the export and names the file (D7) |
| A title or a value holds a control character, invalid XML, or a terminal sequence | XML text is written with `encoding/xml` escaping, and every character that XML 1.0 forbids becomes U+FFFD. JSON is written with `encoding/json`. Neither output is a terminal, and the command prints no stored text to standard error |
| A path in the activity log leaves the run directory | An action is read only through an `os.Root` at the run directory, with `filepath.IsLocal` on the path, as G19 says |
| A SARIF location points outside the repository | The URI is the scenario file's path from the repository root, as `scenarioFiles` found it. It is relative, uses `/`, and passes `filepath.IsLocal` |
| A very large run | The cuts of D12. An action file above 2 MiB is not read, and its duration counts as 0 with a property `semar.durationUnknown` |

## Contract

### `internal/evidence`

New file `internal/evidence/action.go`:

```go
// ReadAction reads one stored action of a run. rel is the path of an activity entry's
// Evidence, relative to the run directory.
func (s *Store) ReadAction(id domain.RunID, rel string) (domain.Action, error)
```

It refuses a path that is not local, a link, a file that is not regular, a file above 2 MiB, and an unknown field.

### `internal/export` (new)

```go
// Run is what every format reads. Load builds it, and it is the only I/O of the package.
type Run struct {
	Run       domain.Run
	Scenarios []Scenario // in the order of Run.Results
	Version   string     // the Semar version
}

type Scenario struct {
	Result     domain.ScenarioResult
	Title      string // "" when the scenario file is gone
	File       string // the scenario file from the repository root, "" when gone
	StepLines  map[domain.StepID]int // the line of each step's id key in the file
	DurationMS int64
	Unknown    bool   // an action was too large to read
}

func Load(store *evidence.Store, repo string, id domain.RunID, version string) (Run, error)

func CTRF(w io.Writer, r Run) error
func JUnit(w io.Writer, r Run) error
func SARIF(w io.Writer, r Run) error
```

`Load` refuses, in this order:

| Case | Error |
| --- | --- |
| The run ID fails `idcheck.Check` | the `idcheck` message |
| `LoadRun` returns `evidence.ErrInterrupted` | `run r-... is interrupted: it has no run.json, so it has no outcome to export` |
| `Verify` names changed files | `the evidence of run r-... changed after it was written: runs/r-.../exchanges/SHOP-482-01/0002-refund.json; nothing was exported` |
| `Results` is empty | `run r-... has no scenario result: a trial run or a fixture check has nothing to export` |

The three writers are pure: the same `Run` gives the same bytes. Maps are written in key order and times in UTC.

### CTRF

```json
{
  "results": {
    "tool": { "name": "semar", "version": "0.9.0" },
    "summary": {
      "tests": 3, "passed": 1, "failed": 1, "pending": 0, "skipped": 0, "other": 1,
      "start": 1794902400000, "stop": 1794902412000
    },
    "tests": [
      {
        "name": "SHOP-482-01 Refund above the order total is rejected",
        "status": "failed",
        "duration": 412,
        "message": "step refund-too-much: expected status 422, observed status 200 (source ticket)",
        "trace": "<every reason, one per line>",
        "suite": "SHOP-482",
        "extra": { "semarOutcome": "failed", "semarRun": "r-20261117T090000Z-1a2b", "semarEnvironment": "uat" }
      }
    ],
    "environment": { "testEnvironment": "uat" },
    "extra": { "semarRun": "r-20261117T090000Z-1a2b", "semarBuilds": "orders 9f3c2ab" }
  }
}
```

| Field | Value | Verified |
| --- | --- | --- |
| `results.tool.name`, `version` | `semar`, the build's version | Required core: yes, as known to this plan |
| `results.summary` counts | From the mapping of D5. `tests` is their sum | `tests`, `passed`, `failed`, `pending`, `skipped`, `other`, `start`, `stop` as required: as known to this plan, not checked against the current schema |
| `start`, `stop` | `StartedAt` and `EndedAt` as milliseconds since 1970 | As above |
| `tests[].name`, `status`, `duration` | D8, D5, D9 | As above |
| `tests[].message`, `trace`, `suite`, `extra` | The first reason, every reason, the ticket, the Semar fields | Optional fields: not verified |
| `results.environment.testEnvironment`, `results.extra` | The environment name, the run and its builds | Optional fields: not verified |

`status` is never `pending`. `message` is left out for a `passed` test.

### JUnit XML

```xml
<?xml version="1.0" encoding="UTF-8"?>
<testsuites name="semar r-20261117T090000Z-1a2b" tests="3" failures="1" errors="1" skipped="0" time="12.000">
  <testsuite name="SHOP-482" tests="3" failures="1" errors="1" skipped="0" time="12.000" timestamp="2026-11-17T09:00:00">
    <properties>
      <property name="semar.run" value="r-20261117T090000Z-1a2b"/>
      <property name="semar.environment" value="uat"/>
      <property name="semar.build.orders" value="9f3c2ab"/>
    </properties>
    <testcase name="SHOP-482-01 Refund above the order total is rejected" classname="SHOP-482" time="0.412">
      <failure message="step refund-too-much: expected status 422, observed status 200 (source ticket)" type="failed">every reason, one per line</failure>
      <system-out>semar outcome: failed</system-out>
    </testcase>
    <testcase name="SHOP-482-02 ..." classname="SHOP-482" time="0.000">
      <error message="setup failed: create-order answered 500" type="blocked">every reason</error>
      <system-out>semar outcome: blocked</system-out>
    </testcase>
  </testsuite>
</testsuites>
```

* One `testsuite` per ticket, in the order the ticket first appears in the results. A scenario with no file goes in the suite `semar`.
* `failure` for `failed`, `error` for `blocked`, `inconclusive`, and `stale`, with `type` set to the Semar outcome, `skipped` with a `message` for `not_run`, and nothing for `passed`.
* `time` is seconds with three decimals. `timestamp` is `StartedAt` in UTC as `2006-01-02T15:04:05`.

### SARIF 2.1.0

```json
{
  "version": "2.1.0",
  "runs": [
    {
      "tool": {
        "driver": {
          "name": "Semar",
          "version": "0.9.0",
          "rules": [
            { "id": "semar/expected-result-not-met", "shortDescription": { "text": "A confirmed expected result was not met" } },
            { "id": "semar/invariant-broken", "shortDescription": { "text": "A built-in invariant was broken" } }
          ]
        }
      },
      "automationDetails": { "id": "semar/r-20261117T090000Z-1a2b" },
      "results": [
        {
          "ruleId": "semar/expected-result-not-met",
          "ruleIndex": 0,
          "level": "error",
          "message": { "text": "SHOP-482-01 step refund-too-much: expected status 422, observed status 200 (source ticket)" },
          "locations": [
            { "physicalLocation": { "artifactLocation": { "uri": ".semar/scenarios/SHOP-482/SHOP-482-01.yaml" }, "region": { "startLine": 14 } } }
          ],
          "partialFingerprints": { "semar/v1": "<hex SHA-256 of scenario, step, check index, and rule>" },
          "properties": { "semarRun": "r-20261117T090000Z-1a2b", "semarScenario": "SHOP-482-01", "semarStep": "refund-too-much" }
        }
      ]
    }
  ]
}
```

* One result per `CheckResult` of a `failed` scenario with `State: false` and either `Confirmed: true` and `Index >= 0` (rule 0), or `Source: invariant` (rule 1). The message is the check's expected and observed text, cut as D12 says.
* `region` is left out when the step line is not known. `locations` is left out when the scenario file is gone, and the scenario ID stays in the message.
* `partialFingerprints` lets a reader match the same finding across runs. It is stable when the run changes.
* With no failed scenario, `results` is an empty list.
* The field names follow the SARIF 2.1.0 standard as known to this plan. They are not checked against the published schema (D4).

### `internal/cli`

```text
semar export (ctrf | junit | sarif) <run id> [--out <file>] [--force] [--dir <repo>]
```

* An unknown format is an error that names the three. The command opens the store with `evidence.Open(semarDir(dir))`, calls `export.Load`, and writes.
* With `--out`, the file is written with `atomicfile.WriteNew` when it does not exist, or with `atomicfile.Write` when `--force` is set. Then one line on standard error: `semar: wrote ctrf for run r-... to report.json`.

## Rules

| # | Rule |
| --- | --- |
| E1 | An export reads stored evidence only. It sends nothing and computes no outcome |
| E2 | Every one of the six outcomes is visible in every format, as the mapped status and as the Semar outcome |
| E3 | `blocked`, `inconclusive`, and `stale` are never written as a pass |
| E4 | An export of an interrupted, changed, or empty run is refused |
| E5 | The same run gives the same bytes |

## Tasks

| # | Task | Files it creates or edits | Interfaces it implements | Tests that prove it |
| --- | --- | --- | --- | --- |
| 1 | Read an action file | Creates `internal/evidence/action.go`, `action_read_test.go` | `(*Store).ReadAction` | `TestReadActionRoundTrip`. `TestReadActionRefusesPath`: `../run.json`, an absolute path. `TestReadActionRefusesLink`. `TestReadActionRefusesLarge` above 2 MiB. `TestReadActionRefusesUnknownField` |
| 2 | The export model | Creates `internal/export/load.go`, `load_test.go`, `text.go` (the cuts of D12 and the XML character filter), `text_test.go` | `Load`, `Run`, `Scenario` | `TestLoadRefusesInterrupted`, `TestLoadRefusesChangedEvidence` (one byte of an action file changed), `TestLoadRefusesNoResults` (a `run.json` with an empty list), each with its message. `TestLoadDurationSumsActions`. `TestLoadTitleFromScenarioFile` and `TestLoadScenarioGone`. `TestLoadStepLines`. `TestCutMarker` |
| 3 | CTRF | Creates `internal/export/ctrf.go`, `ctrf_test.go`, golden files under `internal/export/testdata/` | `CTRF` | `TestCTRFShape`: the output decodes with `DisallowUnknownFields` into a mirror struct of exactly the fields above; `results.tool.name` is set; the seven counts are present and their sum of five equals `tests`; every test has `name`, `status` in `passed failed skipped pending other`, and an integer `duration`. `TestCTRFMapping`: one case per Semar outcome, and `extra.semarOutcome` holds it. `TestCTRFNoFormatVersionKeys`. `TestCTRFGolden`. `TestCTRFDeterministic` |
| 4 | JUnit XML | Creates `internal/export/junit.go`, `junit_test.go`, golden files | `JUnit` | `TestJUnitShape`: the output decodes with `encoding/xml` into a mirror of the elements above, and the counts on `testsuites` and each `testsuite` equal the children. `TestJUnitMapping`: one case per outcome; `blocked`, `inconclusive`, and `stale` are `error`, never empty. `TestJUnitEscapes`: a title with `<`, `&`, `"`, and `\x1b` gives well-formed XML and U+FFFD for `\x1b`. `TestJUnitGolden`. `TestJUnitDeterministic` |
| 5 | SARIF | Creates `internal/export/sarif.go`, `sarif_test.go`, golden files | `SARIF` | `TestSARIFShape`: decodes into a strict mirror; `version` is `2.1.0`; every `ruleId` is one of the two rules and `ruleIndex` matches; every `level` is `error`; every URI is relative and local. `TestSARIFOnlyFailed`: a run with only `blocked` and `inconclusive` gives an empty `results`. `TestSARIFUnconfirmedFalseNotAFinding`: a false check with `Confirmed: false` gives no result. `TestSARIFInvariant`: a 5xx gives rule 1. `TestSARIFFingerprintStable` across two runs of the same failure. `TestSARIFNoSchemaKey`. `TestSARIFGolden` |
| 6 | The command | Creates `internal/cli/export.go`, `export_cmd_test.go`. Edits `internal/cli/root.go` (one line) | `semar export` | `TestExportUnknownFormat`. `TestExportToStdout`. `TestExportOutRefusesExisting` and `TestExportOutForce`. `TestExportExitCodes`: 0 for a failed run that was written, 3 for an interrupted run |
| 7 | End-to-end test | Creates `internal/cli/export_e2e_test.go` | Nothing new | Through `cli.Run` on a copy of `testdata/e2e` with the sample app: `semar confirm SHOP-482-01 --yes`, then `semar run --all` with `RefundOverTotal`, which fails. `semar export ctrf <run>` gives one test with status `failed`, and `semar export junit <run>` one `failure`. `semar export sarif <run>` gives one result whose URI is `.semar/scenarios/SHOP-482/SHOP-482-01.yaml` and whose line is the line of `refund-too-much`. A second run with `RefundServerError` gives a SARIF result of rule `semar/invariant-broken`. A run before the confirmation is `inconclusive`: CTRF `other`, JUnit `error`, and an empty SARIF `results`. After one byte of an action file is changed, every export exits 3 and names the file. No export output holds the token `tok-alice` |

## Out of scope

| Item | Reason |
| --- | --- |
| Publishing Semar's own formats as JSON Schemas with a conformance suite (FR-333) | M2, and a larger piece of work than an export |
| Imports: Postman collections, OpenAPI documents, HAR files, and manual test cases (FR-71, FR-338, FR-339) | Fidelity is not verified (spike S19). An import is a draft that needs a review flow |
| Security and exploration findings in SARIF | Neither is built. The rules list grows when they are |
| Signed evidence bundles (FR-340) | Not designed, M4 |

## Open points

1. The exact versions are not verified: whether the current CTRF schema needs `reportFormat` and `specVersion`, which JUnit XML dialect a team's CI reads, and the published address of the SARIF 2.1.0 schema (D68). A spike runs each export through the reader a team uses and through the official schema, and adds the keys that are missing.
2. CTRF's optional fields `message`, `trace`, `suite`, `extra`, and `environment` are written as this plan knows them, and are not verified.
3. A title read from the scenario file now can differ from the title at run time. The ID is the stable part of the name.
4. SARIF regions point at the scenario file, which is the only file Semar knows. A reader that expects source files of the service shows the finding on the scenario.

`make check` must pass.
