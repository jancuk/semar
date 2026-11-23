# Semar

Semar gives testing by an AI agent one standard workflow. It goes from the ticket and the change to tests, evidence, a verdict, and a report, and the steps are the same whoever runs them and whichever model drafts. A model drafts each test from the ticket and the API document, and then the test replays with no model, on your machine or in a pipeline. Semar tests an HTTP API, a web application, and an Android app.

Code decides every result from recorded evidence. A model never says whether a test passed. A test counts as soon as it is written, and every result that a model wrote says `by model`.

| What is standard | How it is the same every time |
| --- | --- |
| The test | A Gherkin file in the repository, one for each scenario |
| The source of each expected result | `ticket`, `diff`, and the other sources, with `, by model` when a model wrote it |
| The evidence | Every request and every response, with secrets redacted |
| The verdict | Code decides it from the evidence. A pass is a pass |
| The report | `report.md`, `report.html`, `junit.xml`, and `summary.md` |
| The rerun | `semar run`, with no model |

## A day with Semar

Five commands do the work of a normal day. `semar --help` lists them under "Every day".

| Command | What it does |
| --- | --- |
| `semar init` | Sets up a repository once |
| `semar login` | Signs in to a web application once |
| `semar plan` | Drafts tests for the change of this branch |
| `semar run` | Runs the tests and says whether the change can ship |
| `semar show` | Prints the report of a run |

The samples below test the ticket `PROJ-12` of a service named `orders`. Semar reads the ticket from `docs/tickets/PROJ-12.md`, and finds the tickets of a change in its commit messages.

```markdown
# PROJ-12 Limit a refund

`POST /v1/orders/{orderId}/refunds` refunds a part of a paid order.

## Acceptance criteria

1. A refund larger than the paid amount is refused with 422 and the error code REFUND_EXCEEDS_TOTAL.
```

### Set up the repository

Run `semar init` once, inside a clone of the repository of the service. It asks for the address of the service and for the environment variable that holds the token of a test account. The token itself is never written to a file.

```text
$ semar init
Found   docs/openapi.yaml (API document), docs/ (1 document), docs/tickets/ (1 ticket)
Address of the service in the test environment: http://localhost:8080
Environment variable that holds the token, or Enter for none: ORDERS_TOKEN
Who is this account, in one line: a support agent who can refund any order
User that this account signs in with on the web, or Enter for none:
Another account? Name, or Enter for none:
No build probe was found at http://localhost:8080/version. The service is on this machine, so the head commit of the repository is its build. To name one, add build: {probe: http, url: <address>, commit: <JSONPath of the commit>} to the service in .semar/semar.yaml.
Wrote .semar/semar.yaml. Next: semar run
```

When the address answers with a web page, the account needs no token, and `init` ends with `Next: semar login`.

### Sign in to a web application

A web application needs a sign-in in Semar's own browser profile. `semar login` with no flag opens a Chrome window. Sign in once in it, in any way that the application allows, and close the window. With a user and a password variable, Semar signs in by itself, with no window, here and in every later run.

```text
$ export SHOP_PASSWORD='<the password of a test account>'
$ semar login --user ana@shop.example --password-env SHOP_PASSWORD
Wrote the web sign-in of default to .semar/semar.yaml.
Signed in as default (ana@shop.example) at http://localhost:3000. The profile of default in uat is saved.
```

### Plan the tests of the change

`semar plan` gives each acceptance criterion to a small model, and code checks every answer. It also drafts edge cases from the API document with no model: a missing field, a wrong type, a value out of range, an unknown ID. A drafted test counts as soon as the plan writes it.

```text
$ semar plan
PROJ-12 (behaviour): 1 scenario
not drafted: 0
differs from the documents: 0
plan: .semar/plans/77c1a72ed689/plan.md
tried: 0 of 2 steps (every step needs a write or a fixture create, which a trial never sends)
edge cases: 4 drafted, 1 left out (1 because the API document declares no fitting status)
model use: 1 job (1 haiku), 0 corrections, 12k input (6.4k cached), 1.5k output, $0.00
next:
  semar run   run the tests against the service
```

### Run the tests

`semar run` runs the tests of the change and ends with whether the change can ship. A test counts as soon as it is written. A result that a model wrote counts too, and the line of the test says how many of its results a model wrote. This sample runs the tests that the plan above wrote. The service answers 201 where the model expected 422, so `PROJ-12-01` fails. The sample of the report below is a later run, after the fix.

```text
$ semar run
semar: running against uat (test, test data)
Change    feature/refund-limit against main: PROJ-12
Target    uat (test), orders at 77c1a72
Selected  5 scenarios

PROJ-12-01   failed  step request-refund: expected status equals 422, observed 201 (source ticket, by model)  (2 of 2 results by model)
PROJ-12-E01  passed
PROJ-12-E02  passed
PROJ-12-E03  passed
PROJ-12-E04  passed
build: orders 77c1a72ed68977b683d94cee3b75ab28aedba448 (the head commit of the repository, not read from the service)
report: .semar/runs/r-20261119T003257Z-f019/report.md

On feature/refund-limit against main · uat · orders at 77c1a72
1 use case of PROJ-12

results written by a model: 2 of 2
Ship: no.
```

The failure line names the source of the expected result and says `by model` when a model wrote it. The model expected a result that the service does not give, and Semar reports that as a failure. Read the test, and decide which one is wrong. To take a result over, delete `, by model` from the comment under its `Then` line. A pull request reviews that edit like any other.

The exit code is 0 when everything passed, 1 when a test failed, and 2 when a test could not be judged. From then on, `semar run` replays the tests with no model call.

### Read the report

`semar show` prints the report of the newest run. Every run also writes `report.html`, one file with the screenshots of the web and Android steps inside, for a person with no command line.

```text
$ semar show
# Semar report r-20261119T003301Z-f910

1 use case: 1 verified
Details: `semar status --ticket PROJ-12`

- Environment: uat (test)
- Started: 2026-11-19T00:33:01Z
- Ended: 2026-11-19T00:33:01Z
- Builds at the start: orders 77c1a72ed68977b683d94cee3b75ab28aedba448 (the head commit of the repository, not read from the service)
- Builds at the end: orders 77c1a72ed68977b683d94cee3b75ab28aedba448 (the head commit of the repository, not read from the service)
- Outcomes: failed 0, blocked 0, inconclusive 0, stale 0, not_run 0, passed 5

A declared build cannot show that the service was redeployed during the run.

<details>
<summary>Passed (5)</summary>

- PROJ-12-01 Refund larger than the paid amount is refused with 422 and REFUND_EXCEEDS_TOTAL
- PROJ-12-E01 Edge case: POST /v1/orders/{orderId}/refunds without the required field amount
- PROJ-12-E02 Edge case: POST /v1/orders/{orderId}/refunds with amount as a string, and the document says integer
- PROJ-12-E03 Edge case: POST /v1/orders/{orderId}/refunds with amount below the minimum 1
- PROJ-12-E04 Edge case: POST /v1/orders/{orderId}/refunds with an unknown orderId

</details>

## Data touched

- create order o_1023, by PROJ-12-01 step create-paid-order: left in place
- create order o_1022, by PROJ-12-E02 step create-paid-order: left in place
- create order o_1024, by PROJ-12-E01 step create-paid-order: left in place
- create order o_1025, by PROJ-12-E03 step create-paid-order: left in place

HTML report: .semar/runs/r-20261119T003301Z-f910/report.html
```

### Also in the help

`semar --help` lists five more commands under "Also".

| Command | What it does |
| --- | --- |
| `semar check <id> pass` | Records what a person checked by hand |
| `semar record` | Records a web test in Chrome, or an Android flow or scenario in the terminal or a local web studio |
| `semar import <file> --ticket <key>` | Starts the regression set from a Postman collection or a HAR file |
| `semar rm <id> --reason <text>` | Retires a test, and keeps a record of why |
| `semar log` | Lists the stored runs |

Every other command, such as `semar status` and `semar journey`, still works and is left out of the help. The [command reference](docs/GUIDE.md#command-reference) lists every command with every flag.

## Run the regression set in a pipeline

Put the tag `@suite:smoke` on the Scenario line of the tests that a pipeline runs. One command then runs them, asks no question, tries a failed test again, and copies the report files to a directory that the pipeline keeps.

```bash
semar run --suite smoke --retries 2 --ci --report-dir semar-report
```

```text
semar: running against uat (test, test data)
Change    the smoke suite
Target    uat (test), orders at 77c1a72
Selected  2 scenarios

PROJ-12-01  passed
PROJ-12-02  passed  flaky, passed on attempt 2 of 3
build: orders 77c1a72ed68977b683d94cee3b75ab28aedba448 (the head commit of the repository, not read from the service)
response checks: 1 warning from environments.uat.checks, no outcome changed
  PROJ-12-02 step read-stats: GET /v1/stats: the API document does not declare status 503 for this operation
report: .semar/runs/r-20261119T003310Z-5b49/report.md
```

The run then prints the path of the report directory and whether the tests can ship. The directory gets `report.md`, `report.html`, `junit.xml`, and `summary.md`. A test that passed on a later attempt counts as passed, and the reports mark it as flaky. The [guide](docs/GUIDE.md#run-in-a-pipeline) has a complete example for GitHub Actions and for GitLab CI.

## What a test can check

A test of an HTTP API can check:

* the status, a header, and a value at a JSON path, with equals, contains, exists, a count, and a comparison of numbers;
* that every response fits the API document, which is on by default when the repository has one;
* that a response came within a time limit that you set.

A test of a web application can do a click, a fill, a key press, a drag, a choice in a select list, a hover, a check of a checkbox, and a file upload. It can check:

* the text of an element, a count of elements, and that an element exists or does not;
* that an element is visible, hidden, enabled, disabled, checked, or not checked;
* an attribute of an element, such as a placeholder;
* the path and the title of the page.

A web check waits for its condition, 5 seconds by default. Every web step leaves a screenshot in the run.

A test of an Android app runs directly over `adb` with the UiAutomator2 server APKs (no Node.js or Appium server required), or against any W3C WebDriver `serverUrl`. An app with no backend needs no `baseUrl` or `hosts`. It can do a tap, a long press, typing (with secrets kept in environment variables), a swipe, a scroll, a key press (including `back`, `enter`, and `hide-keyboard`), a deep link, and a wait, across native views, Jetpack Compose, Flutter, WebView accessibility trees, soft keyboards (IMEs), and system dialogs. It can check:

* element text (`equals` or `contains`), existence, count, attributes, and states (`visible`, `hidden`, `enabled`, `disabled`, `checked`, `unchecked`).

Every Android step leaves a screenshot in the run, and broken locators can be repaired with no model using `semar screens repair latest --apply`.

### Record Android flows and scenarios

`semar record` also records an Android app live on an emulator or physical device, either in a local web studio or in the terminal:

```bash
semar record --ui                       # local web studio (guided setup, live mirror, verify, undo)
semar record --flow login               # record a reusable flow into .semar/flows/login.yaml
semar record SHOP-12 --use login        # record a scenario starting from a saved flow
```

Reusable flows live in `.semar/flows/<id>.yaml` and are invoked from scenarios with `use:` and `with:`. Password fields and secrets stay in memory only for the session and are written to files as `env:NAME`. Finishing a recording saves the files and immediately verifies them on a clean device session, with one-shot `undo` if you want to roll back. See [Test an Android app](docs/GUIDE.md#test-an-android-app) for setup and details.

## Install

Semar is one static binary. You need Go 1.26 or later and `git`.

```bash
go install github.com/jancuk/semar/cmd/semar@latest
semar version
```

Only `semar plan` needs a model. The default provider is Claude Code, installed and signed in. Codex, the Anthropic API, and any server with the OpenAI chat format work too. Every other command runs with no model.

## What leaves your machine

`semar plan` sends the text of the acceptance criteria, parts of your API document and of the diff, and the names and descriptions of your fixtures and accounts to the model provider that you chose, through your own account or key. A plan sends no token and no address of your environment. When a plan explores a web page, the model gets a list of what the page shows, and never the value of a password field.

Every other command sends nothing to a model. A run sends requests from your machine to your own environment, and stores the evidence in `.semar/runs/` with secrets removed. A screenshot is not redacted, so use a test account.

## Documents

| File | What it holds |
| --- | --- |
| [docs/GUIDE.md](docs/GUIDE.md) | Every feature with its file formats and its limits, the settings, a pipeline, and every command with every flag |
| [docs/ROADMAP.md](docs/ROADMAP.md) | What is built, what comes next and in which order, and what Semar does not do |

## License

Apache-2.0 is the intended license. The repository has no `LICENSE` file yet.
