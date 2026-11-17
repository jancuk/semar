# Plan 24: Scenario files are Gherkin

* **Status:** Implemented. This text describes the system as built.
* **Needs first:** Plans 01, 12, 17, 19, 20, and 22.
* **Implements:** FR-6 and PRD decision D69. TRD decision T8 in its refined form, and the TRD section "Scenario file".

## Goal

Scenario files used a YAML format that only Semar had. A QA engineer who opens one reads `check: { jsonpath: ..., equals: ... }` and `source: ticket`, and has to learn the format before they can confirm what a scenario expects. QA engineers already know a standard for this: Gherkin, the `.feature` files of Cucumber and BDD, with Feature, Scenario, Given, When, Then, and tags.

This plan makes a scenario a Gherkin file. Product and QA read the confirmed part as Gherkin. Requests and selectors stay in the YAML script, so Semar keeps "what people confirm" (the Scenario) apart from "how it runs" (the Script), as decision T8 says.

The change is of the file format only. The internal model, `domain.Scenario`, did not change, and neither did the hash of the expected results, so no confirmation and no stored script lapses when a repository converts its files.

## Decisions

| # | Decision | Reason |
| --- | --- | --- |
| D1 | A scenario is one file, `.semar/scenarios/<ticket>/<id>.feature`, in valid Gherkin. It is read with the official parser, `github.com/cucumber/gherkin/go` (MIT). The Go module path of that directory carries its major version, so the import is `github.com/cucumber/gherkin/go/v43`, pinned to `v43.0.0` in `go.mod`, with `github.com/cucumber/messages/go/v35` and `github.com/google/uuid` that it needs. A file holds one `Scenario`. The `Feature:` line names the ticket's subject. `domain.Scenario` stays the model inside Semar, and only the file format changes. Journeys, fixtures, screens, and scripts stay YAML | A format that QA already knows needs no teaching. A parser that Cucumber maintains reads every edge of Gherkin the way other tools do. One Scenario in a file keeps a file, an ID, a confirmation, and a script one-to-one, as they were |
| D2 | The scenario is written like this. Tags on the Scenario carry `@id:<id>`, `@ticket:<key>`, `@surface:api\|web\|android`, and `@auth:<profile>` (nothing when the profile is `default`). The intent is the description lines. `Given the fixture <id> as <name>` is one line for each `given` entry, with its parameters as a table of two columns under it. A setup step is a `Given` line, an action step is a `When` line, and each expected result is a `Then` line (`And` for the later ones) directly under the `When` step that it belongs to. A step that acts as another account starts `as <account>, `, as in `When as bob, Bob moves card A to Doing`. A repeated keyword is `And` | This is the smallest mapping that is regular: each part of the model has one place, and a person who knows Gherkin finds each part where they expect it |
| D3 | What Gherkin has no place for goes in a comment line directly under the line that it describes, and Semar reads it through the comment locations of the parser. Under a Given or When step: `# step: <id>; <METHOD> <path template>`, or `# step: <id>` for a step with no request. Under a Then line: `# <source> <cite>`, and `, by model` at the end when a model wrote it. A comment elsewhere is a note and is ignored | The step ID is the name that reports, scripts, and the ledger use, so it is kept and never counted. A comment is the one part of Gherkin that every tool leaves alone |
| D4 | A `Then` line has one form for each subject and operator, and a value is a JSON literal: `the status is 422`; `$.error.code equals "cross_board_move"`; `$.a does not equal 1`; `$.a contains "x"`; `$.data.id exists`; `$.data.id does not exist`; `$.a is greater than 3`; `$.a is less than 3`; `$.data.items has 3 items`; `$.a equals the value at $.b`; `the header Location contains "/v1/"`; and `the element todo-first equals "{{b_title}}"`. A header and an element take the operators of a path, and the validator refuses the operator that an element cannot have. A line that is none of these is an error that names the line and lists the forms in plain words | A fixed grammar is checkable and has the same meaning for every reader. JSON for values needs no new quoting rules, and the number rules of the YAML reader carry over |
| D5 | The file and the YAML file give the identical `domain.Scenario`, and so the same `ExpectHash`. `Render` proves it for each file that it writes: it reads its own output back with `Parse` and refuses to write the file unless the scenario, field by field, and its hash are the same. A property test writes 4000 generated scenarios and every scenario in the testdata, reads each back, and compares. A fuzz test reads arbitrary text, and another writes arbitrary field values | Every confirmation and every stored script is keyed by the hash. A converted repository must keep them all |
| D6 | `semar scenarios convert` converts every `.yaml` and `.yml` scenario file under `.semar/scenarios` to a `.feature` file beside it, deletes the YAML file, and prints each file with its hash. It makes every file in memory first and writes none unless all can be written. It refuses to overwrite a `.feature` file. After this plan the loader reads only `.feature`, and a YAML scenario file is an error: "scenario files are Gherkin now: run semar scenarios convert". Nothing reads both formats. The YAML reader stays in `internal/scenario/legacy.go` for `convert` alone | Migrate the files and then delete the old format, so that no repository sits between two formats. A command that stops halfway leaves a repository that nothing can read |
| D7 | Everything that writes a scenario writes `.feature`: the plan's writer, the plan's check of a drafted scenario, and the testdata and tests. The Feature line of a planned file is the planner's sentence on what the ticket changes, cut at a word at 100 characters, and the ticket key when there is none. `semar confirm` prints the scenario file as it is, with the source and the cite of each result in the comment under it, and then the results that need a person. `semar script add` writes no scenario, so it did not change. `semar init` and the sample service write none either | One format, in each place that touches a scenario. A person confirms the file that they would read in the merge request |
| D8 | An intent that ends with one line break is written with the tag `@intent-newline`. Gherkin drops the blank lines at the end of a description, and a YAML `>` or `\|` block ends its text with a line break. The documented example is such a block, and the hash covers the intent exactly, so without the tag the conversion would change the hash of every scenario that used the documented form. The planner's own intents are trimmed, so a new file has no such tag. An intent with two line breaks at the end is refused with a sentence that says so | The hash must stay valid across the conversion, and a tag is the one place that Gherkin lets a flag live without changing the text. The tag is visible to a person, who can remove it when they next edit the intent, and then a new confirmation is correct |
| D9 | A line of the intent that Gherkin would read as part of the scenario is written with a backslash in front, and the reader takes the backslash off. These are the lines that start with `#`, `@`, `\|`, `"""`, a backtick fence, a step keyword, or a keyword with a colon, and a line that already starts with a backslash is written with two. The refused alternative is to reject the scenario | A natural intent starts with `Given a paid order`, and one scenario of the test data does. A scenario that the YAML reader accepted must convert |
| D10 | A scenario with no auth profile, which the engines read as "send no session", has the tag `@noauth`. A scenario with no `@auth` tag has the profile `default` | The two values differ in the hash and in what a run sends. `default` is a profile name, and `""` is none |
| D11 | The lease of a fixture entry ends its line with `with an exclusive lease` | `lease` is in the hash and has to be in the text. The words say what it does |
| D12 | The reader is strict, as the YAML reader was (rule G10). Every error starts with `<file>:<line>:` and says in plain words what is wrong there. It refuses a tag that is not in D2, a repeated tag, a second Scenario, a Background, a Rule, Examples, a doc string, a table anywhere but under a fixture line, a step with no `# step:` comment, a result with no source comment, a `Then` with no `When` above it, a language other than English, and a number that a 64-bit float or integer cannot hold exactly. At most five parser errors are shown, then a count | A typo must never change a test silently. A message that names tokens of a parser is of no use to a QA engineer |
| D13 | The line of a step in a report, a SARIF location for example, is the line of its `Given` or `When`. Before this plan it was the line of the `id` key | The ID is in a comment now. The line of the step is the line that a person reads |
| D14 | The plan's check of a drafted scenario writes it with `Render` only when `scenario.Validate` found nothing. A scenario that fails the rules has said why already, and the writer's complaint about the same field would be a second problem for the model to answer | The planner repairs from the list of problems. It should get each once |

## Not built

* `Scenario Outline` and `Examples`. Write each case as its own file. A table of cases is planned (see [Plan 11](11-configuration.md)).
* `Background` and `Rule`. Put the `Given` lines in the Scenario.
* Gherkin in another language than English.
* `semar import gherkin`, which would read a team's own feature files with their own step sentences.
* A tag on a Feature or a Scenario that is not in D2, such as `@smoke`. The model has no field for it, and the reader refuses what it would drop.

## Contract

### The file

```gherkin
Feature: Refunds of an order

  @id:SHOP-482-01 @ticket:SHOP-482 @surface:api @auth:support-agent
  Scenario: Refund above the order total is rejected
    Before: a refund larger than the order total was accepted.
    Now: the API rejects it with 422 and leaves the order unchanged.

    Given the fixture order.paid as order
      | customer | c-1 |
    And Create a paid order for 100000
    # step: create-order; POST /v1/orders
    When as bob, Request a refund of 150000 for that order
    # step: refund-too-much; POST /v1/orders/{id}/refunds
    Then the status is 422
    # ticket SHOP-482: a refund above the order total must be rejected, by model
    And $.error.code equals "REFUND_EXCEEDS_TOTAL"
    # diff
    When Read the order again
    # step: order-unchanged; GET /v1/orders/{id}
    Then $.data.refunded_total equals 0
    # ticket SHOP-482: the order must stay unchanged
```

### The sentences of a Then line

| Check | Sentence |
| --- | --- |
| `status` | `the status is <int>` |
| `jsonpath` and `equals` | `<$path> equals <value>` |
| `jsonpath` and `notEquals` | `<$path> does not equal <value>` |
| `jsonpath` and `contains` | `<$path> contains <value>` |
| `jsonpath` and `exists` | `<$path> exists`, `<$path> does not exist` |
| `jsonpath` and `greater`, `less` | `<$path> is greater than <number>`, `<$path> is less than <number>` |
| `jsonpath` and `count` | `<$path> has <n> items`, and `has 1 item` |
| `jsonpath` and `equalsPath` | `<$path> equals the value at <$path>` |
| `header` and any operator of a path | `the header <Name> <operator>` |
| `element` and `equals`, `notEquals`, `contains`, `exists`, `count` | `the element <id> <operator>` |

A `<value>` is one JSON value. A string is in double quotes, and a number is read exactly. A `<$path>` starts with `$` and may hold spaces: the reader takes the first split of the line that leaves a sentence that reads.

### The Go interface

```go
// internal/scenario
func Render(s domain.Scenario, subject string) ([]byte, error)   // writes the file, and reads it back to prove it
func Parse(data []byte, name string) (domain.Scenario, error)    // reads a file, and names <name>:<line> in an error
func Load(path string) (domain.Scenario, error)
func ReadOutline(data []byte) (Outline, bool)                    // the ID, the title, the ticket, and the line of each step, for a report
func ParseYAML(data []byte, name string) (domain.Scenario, error) // the older format, for semar scenarios convert only
```

An error of `Render` is `<field>: <what is wrong>`, such as `steps[1].expect[0].cite: must be one line`, so that the plan can send it back to the planner with the draft's field.

## Trust

| Risk | Answer |
| --- | --- |
| A YAML file is left, and a plan reuses its ID | Every command that reads scenarios stops with "scenario files are Gherkin now: run semar scenarios convert" and names the files |
| Conversion changes what a confirmation covers | `Render` refuses a file that does not read back to the same scenario and hash, and `convert` prints each hash. The proof on a real repository is under Verification |
| A comment in the file is read as an expected result | A comment is read in two places only, directly under a step and directly under a `Then`. A source that is not one of the five is refused by `scenario.Validate` when the file loads, as it always was |
| A hand edit puts an unknown tag or step in the file | The reader refuses it with the line, so a typo cannot silently change a test |
| A hostile character in a scenario file reaches the terminal at confirm time | The file text goes through `terminalSafe`, as the list of results does |

## Verification

* Unit tests: every sentence of D4, tags, `as`, fixtures with tables, element checks, every error of D12 with its line, the round-trip property, the escape of D9, the tags of D8 and D10, `convert` (including the refusal to overwrite, all-or-nothing, nested folders and `.yml`), and the loader's error on YAML. `go vet ./...`, `gofmt -l .`, and `go test -race ./...` pass.
* On a copy of the mock service that the web plan used, with its confirmations and its scripts: ten scenario files, one of them a web scenario, were run before the conversion and after it. The ten results are the same, none is `inconclusive` for a missing confirmation, and each expectation hash that `convert` printed is the name of the directory under `.semar/store/confirmations` that holds its confirmation.
