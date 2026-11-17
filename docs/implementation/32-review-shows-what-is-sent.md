# Plan 32: Review shows what a test sends and what the service answered

* **Status:** Implemented.
* **Needs first:** Plans 16, 24, and 26.
* **Implements:** PRD decision D76 and FR-381 to FR-384.

## Goal

A person who approves a test sees what it sends and what the service answered the last time, next to each expected result.

Before this plan `semar review` and `plan.md` showed a step as a method, a route, and a sentence. The body that the step sends was in the stored script, which review never opened. A person could approve "create an order" without seeing the order, and could approve "expects 404" without knowing that the service answered 200.

## Decisions

| # | Decision | Reason |
| --- | --- | --- |
| D1 | `semar review` prints, under each step of a scenario, what the stored script sends. An API step shows its method and path, then `query`, `headers`, and `json`, in the form that a fixture step has in review. A web step shows its action, element, and screen, and then its text. A secret shows as `the value of $NAME`. The fixture view builds its query, headers, and JSON from the same function | The payload is part of what the test claims. One form for a fixture step and a scenario step means one thing to learn. Changed: the method and path are always shown, not only when the path holds a variable, because the contract shows them and the planner's hint can differ from the script. The service name is not shown |
| D2 | Values show as the script holds them. A variable stays `{{name}}` | The script is what the person approves. A filled-in value belongs to one run |
| D3 | The script is the one stored for the scenario and its expectation hash. When none is stored, review prints `no script is stored for this scenario` once. When the store cannot be read, review prints that instead | A scenario with no script cannot run, and the person should know before approving. Changed: a store that cannot be read has its own line, so that it is not mistaken for a missing script |
| D4 | Next to each expected result, review prints what the last run observed: `last run <id>: observed <value>, met`, `not met`, or `not evaluated: <detail>`. The value is the observed text that the run stored, for example `status 201`. The last run is the newest of the 30 newest runs whose result for the scenario has the same expectation hash. A trial run, a fixture check, and a recording are not taken, and a run that cannot be read is skipped | A run of a scenario that is not confirmed still sends its steps and stores what it observed. The same hash means the observation belongs to these expected results. Changed: runs with a purpose are excluded, and a check that the run has no result for reads `not evaluated: the run stored no result for this check` |
| D5 | When no such run exists, review prints one line for the scenario: `not run yet: semar run --scenario <id> sends it and stores what the service answers` | The person learns how to get the answer, and review does not send anything itself |
| D6 | The details view (`d`) adds, for each step, the stored exchange of that run: `request:` with the method and URL, `request body:`, `status:`, and `response body:`. A step that was refused or got no answer says so. Each text is cut to 2000 characters with `… [cut: N more characters]` | A status alone does not show why a check was not met. The stored exchange is already redacted by the recorder. Changed: the request headers are not shown |
| D7 | `plan.md` gains a `Sends` line under each step, with the same content as D1 and D2, cut to 600 characters, and joined with `; `. The route of the script is in it only when it differs from the route that the step line shows. It gains no value of a response | `plan.md` is committed. D7 and D11 of Plan 16 keep every response value out of it, and they stand. The script is model output from the repository's own documents, which `plan.md` holds already. Changed: the route is added only when it differs, so the line is not repeated |
| D8 | The expectation hash stays as it is. The payload is shown and is not part of the approval | A hash that covered the payload would lapse every stored confirmation. `semar plan` never writes over a stored script, so a payload does not change under an approval through a plan. A stricter binding is a later decision |
| D9 | Every text from a script or an exchange goes through `terminalSafe` and `oneLine` in the terminal, and through `shown` in `plan.md`. A line in the list view is cut to 600 characters | A script and a response are untrusted text, and a long body must not push the expected results off the screen |

## As built

* `internal/sends/sends.go`: `Lines(step)` gives the lines of what a script step sends, `Details(request)` the query, headers, and JSON of a request, and `CompactJSON` one line of JSON. Nothing in it is cut or escaped.
* `internal/evidence/lastrun.go`: `LastRunOf(scenario, hash)` and `Exchanges(run, scenario)`.
* `internal/scenario/gherkin_lines.go`: a `Line` names its step and, for an expected result, its position.
* `internal/cli/review_sends.go`: `scenarioView` reads the script and the last run of a scenario, and prints the lines of D1 to D6. `review_scenario.go` and `review.go` call it in the list and the details view. `review_fixture.go` uses `sends.Details`.
* `internal/stage/plan/render.go`: `writeScenario` takes the script of the scenario, found by its ID, and writes the `Sends` line. The golden files of `plan.md` gained that line and nothing else.

## Tests

* `internal/sends/sends_test.go`: an API step, a step with no payload, each web action, a secret, a step that sends nothing.
* `internal/evidence/lastrun_test.go`: the newest run with the same hash, other hashes, trial and fixture runs, the limit of 30, an interrupted run, and the last exchange of each step.
* `internal/cli/review_sends_test.go`: the list view before and after a run, a check that was not evaluated, no script, a run of other expected results, cut and escaped text, and the details view.
* `internal/cli/review_sends_e2e_test.go`: plan, review with no run, run, review again, with a check that is not met.
* `internal/stage/plan/render_sends_test.go`: the `Sends` line, its cut and escaping, and a plan with no script.

## Contract

The list view of one scenario:

```text
Test SHOP-7-02 · API · as default
  When Create an order for the customer.
       POST /v1/orders
       json {"customerId":"{{customer.id}}","dueAt":"{{datetime.plus7d}}"}
  Then the status is 201
       last run r-20261118T071502Z-9c1e: observed 201, met
  And $.data.status equals "open"
       last run r-20261118T071502Z-9c1e: observed "draft", not met
```

With no run:

```text
  not run yet: semar run --scenario SHOP-7-02 sends it and stores what the service answers
```

A step of a scenario in `plan.md`:

```text
1. POST /v1/orders (setup): Create an order for the customer.
   - Sends json {"customerId":"{{customer.id}}","dueAt":"{{datetime.plus7d}}"}
   - Expects status 201. Source: ticket. Cite: …
```

## Trust

Untrusted: the scenario files, the stored scripts, the stored runs, and every body in them. Review reads a run only through `evidence.Store`, which is safe against links and bounds what it reads. Nothing that review prints is sent anywhere. A stored exchange holds no session header and no secret that the recorder knows.

## Tasks

Each task is one commit, with its test written first.

1. One function gives the lines of what a script step sends, for an API step and a web step, shared by review and by `plan.md` (D1, D2, D9). The fixture view uses it too.
2. `evidence.Store` finds the last run of a scenario with a given expectation hash, with its check results and the exchange of each step (D4, D6).
3. `semar review` loads the script and that run, and prints D1 to D5 in the list view and D6 in the details view.
4. `plan.md` prints the `Sends` line (D7).
5. End-to-end test in `internal/cli`: plan, review with no run, run, review again, and the second review shows the observed values and a check that is not met.

## Not built

* Review does not run a test.
* The payload is not part of the expectation hash.
* The value that a web check found on the page is shown as the run stored it, and no screenshot is shown.
* `semar show` still prints no exchange.
* Journey legs are not shown with a script or a run: review of a journey is unchanged.
* The details view shows no request header and no screenshot.
* A step capture is not shown.
