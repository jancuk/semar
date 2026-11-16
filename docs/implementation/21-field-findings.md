# Plan 21: Findings from a real delivery

* **Status:** Specification, not implemented.
* **Needs first:** Plans 12 to 20.
* **Implements:** PRD product rules 3, 4, and 14, decisions D25 and D29, FR-123, FR-261 to FR-270. TRD sections "Planning from a change", "Fixtures", and "Test obligations and the coverage ledger".

## Goal

A team delivered a feature across a Go backend, a web app, and an Android app, and tested it with Semar from the plan to the report. The delivery showed eight defects in Semar. This plan fixes them. The most serious one: a ticket said that a move to another board is refused with 422, the code accepted it with 200, and Semar drafted a scenario that expected 200. It reported the conflict as a difference, but a person who confirms the plan quickly makes the defect the expected result, and every later run passes.

## Decisions

| # | Decision | Reason |
| --- | --- | --- |
| D1 | A ticket file in the repository is a requirement. Where the code does not meet an acceptance criterion, the planner expects what the criterion says, with the source `ticket`, so that the run fails and shows the defect | A test that expects what the code does cannot find a defect in the code. The ticket is what the team agreed |
| D2 | Every expected result that the planner drafts carries `by: model` and always needs a person's confirmation, whatever its source | The cited line is checked by code, but the translation of a sentence into a check is a model's work (PRD product rule 4) |
| D3 | A scenario step may act as another account with `as: <auth profile>`. The planner sees the accounts and their descriptions, and drafts access checks with them | An access check needs a second account. Without it the planner could not draft one (D29) |
| D4 | Variables in the operand of `equals`, `notEquals`, and `contains` are filled in from the run's variables before the check. An unknown variable is refused when the file is loaded | "The card is now on board {{o.board}}" is a natural check, and comparing the literal text gave a false failure |
| D5 | `semar init` writes `.semar/.gitignore` so that `runs/`, `store/redact.key`, `store/fixture-health/`, and `semar.local.yaml` stay on the machine. Approvals and confirmations are committed | The redaction key turns a short redacted value into a guessable one when it is published next to the evidence |
| D6 | `plan.md` lists the problems of every draft that was refused, and the summary says why a trial tried nothing | A person cannot judge a plan whose rejected drafts are invisible |
| D7 | The planner drafts a manual check for each acceptance criterion that only a person can check on a screen or a device. It becomes an obligation that needs a person, with steps and an expected result | "Not covered" is a gap with no next step. A checklist is something a person can do |
| D8 | `semar init --account <name>=<ENV>` adds an auth profile per account. It can repeat | The first thing a team needs after the first account is a second one |

## Trust

| Risk | Answer |
| --- | --- |
| A ticket file is repository text and may hold instructions | It is data in the prompt, fenced, escaped, numbered by line, and cut to 64 KiB. It cannot change the schema, the rules, or a verdict. D2 keeps a person in the loop |
| The model cites a line that does not say what it claims | Code checks that the cited file is the ticket file of the scenario's ticket and that the line exists. The confirm screen prints that line next to the check, so a person compares them |
| A scenario acts as an account that has more rights than the test needs | `as` names an auth profile of the person's configuration. Semar never invents an account. The confirm screen shows `as` |
| A variable in an expected value reads a secret | A `secret.` variable is refused in an operand, as in a request |

## Contract

### Configuration

* `knowledge` gains the kind `tickets`: `{id: tickets, kind: tickets, path: docs/tickets}`. The folder holds `<ticket key>.md` files. `semar init` adds it when `docs/tickets/` exists. A tickets folder is read from the copy at the head commit, like every knowledge source.
* `authProfiles.<name>.description`: optional, one line, at most 200 characters, no control character.
* `semar init --account <name>=<ENV>` (repeatable) adds `authProfiles.<name>` with the same session header and value as the default profile and `secrets: {TOKEN: "env:<ENV>"}`. A name passes `idcheck.Check`, and a repeated name is a usage error.
* `semar init` writes `.semar/.gitignore` when the file does not exist, and never changes an existing one.

### Scenario and script files

* `steps[].as` (optional): the name of an auth profile. Every request of that step, including a setup step, is sent with that profile's session. Unknown at run time: the scenario is `blocked` with "account <name> is not in the configuration". The expectation hash covers `as` only when it is set, so existing hashes do not change.
* `expect[].by` (optional): `model` or absent. `by: model` always needs confirmation (D2). The hash covers `by` only when it is set.
* `equals`, `notEquals`, and `contains` operands that are strings may hold `{{name}}` variables: a capture of an earlier step, an output of a `given` alias, or `run.id`. `secret.` is refused. An unknown name is a load error with the path of the check. At run time the operand is filled in from the variables; a missing value makes the check `not_evaluated`. The report shows the filled-in value next to the template.

### Plan stage and prompt (version 6)

* Input gains `Tickets` (the ticket files of the tickets in the change, with path and text) and `Accounts` (names and descriptions of the auth profiles).
* The prompt gains:
  * "The tickets' acceptance criteria": each ticket file, numbered by line, as data, with the rule of D1. Each criterion that a caller of the API can observe gets at least one expectation with `source: ticket` and a cite of the form `<ticket file>:<line> <one sentence>`. Where the code does not meet it, the planner still expects what the criterion says, adds a `differences` entry, and names the scenario after the requirement, not after the code.
  * "Accounts you can act as": the names and descriptions, and the rule of D3: for each changed entry point, add a scenario for the most likely access mistake (an account outside the board, a member without the owner's rights) with `as`.
  * The rule of D7: a criterion that only a person can check (a screen, a device, a gesture) goes under `manualChecks` with the ticket, a title, 1 to 20 steps, the expected result, and the cite of the criterion.
* Draft schema: `ExpectDraft.source` is `diff` or `ticket` (default `diff`). `StepDraft.as`. `manualChecks: [{ticket, title, steps, expected, cite}]`.
* Validation adds:
  * A `ticket` expectation's cite names the ticket file of the scenario's ticket and a line that exists in it. Otherwise a problem goes back to the planner.
  * `as` names an account of `Input.Accounts`.
  * Variables in operands follow the rule above.
  * A manual check names a ticket of the change and cites a line of its ticket file.
* Every drafted expectation is written with `by: model`.
* The obligations of a ticket gain one obligation per manual check, with `manual: {kind: judgement, text, steps, expected, writtenBy: ai}`, so it derives `needs_a_person` and its checklist.
* `plan.md`:
  * "How this plan was made" lists the problems of each refused draft, at most 30 per draft, escaped.
  * Each ticket section shows its manual checks.
  * A scenario with an expectation that the code is known not to meet (a `differences` entry for the same ticket and the cited criterion line) is marked "expected to fail on this build: the code does not meet <ticket file>:<line>".
* The summary line of the trial names the main reason when it tried nothing, for example `tried: 0 of 27 steps (every step needs a write or a fixture create, which a trial never sends)`.

### Confirm

The confirm screen prints, for a `ticket` expectation whose cite names a file of the repository, the cited line as it is in the working tree (one line, escaped, at most 300 characters), and `as` for each step that has it.

### Small

`semar fixture check` with neither IDs nor `--all` says `name the fixtures to check, or use --all` and exits 3.

## Tasks

| # | Task | Owner | Files |
| --- | --- | --- | --- |
| A | Runtime: `as`, `by`, operand variables, accounts in config, `init --account`, `.semar/.gitignore`, confirm screen, fixture check message | Agent B | `internal/domain`, `internal/scenario`, `internal/script`, `internal/check`, `internal/replay`, `internal/engine/api`, `internal/config`, `internal/confirm`, `internal/cli/init.go`, `confirm*.go`, `fixture.go` |
| B | Planner: tickets kind, prompt version 6, schema, validation, `by: model`, manual checks to obligations, `plan.md` additions, trial reason | Agent A | `internal/knowledge`, `internal/stage/plan`, `internal/prompt`, `internal/obligation/fromplan.go`, `internal/cli/plan.go`, `planwrite.go`, `plantrial.go` |

Task A commits the domain fields (`Step.As`, `Expectation.By`) first, so that task B can use them.

`make check` must pass.
