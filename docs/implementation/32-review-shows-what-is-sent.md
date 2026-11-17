# Plan 32: Review shows what a test sends and what the service answered

* **Status:** Planned. Not built.
* **Needs first:** Plans 16, 24, and 26.
* **Implements:** PRD decision D76 and FR-381 to FR-384.

## Goal

A person who approves a test sees what it sends and what the service answered the last time, next to each expected result.

Before this plan `semar review` and `plan.md` showed a step as a method, a route, and a sentence. The body that the step sends was in the stored script, which review never opened. A person could approve "create an order" without seeing the order, and could approve "expects 404" without knowing that the service answered 200.

## Decisions

| # | Decision | Reason |
| --- | --- | --- |
| D1 | `semar review` prints, under each step of a scenario, what the stored script sends. An API step shows its path when the path holds a variable, then `query`, `headers`, and `json`, in the form that a fixture step has in review today. A web step shows its action, its screen and element, and its text. A secret shows as `the value of $NAME` | The payload is part of what the test claims. One form for a fixture step and a scenario step means one thing to learn |
| D2 | Values show as the script holds them. A variable stays `{{name}}` | The script is what the person approves. A filled-in value belongs to one run |
| D3 | The script is the one stored for the scenario and its expectation hash. When none is stored, review prints `no script is stored for this scenario` once | A scenario with no script cannot run, and the person should know before approving |
| D4 | Next to each expected result, review prints what the last run observed: `last run <id>: observed <value>, met`, `not met`, or `not evaluated: <detail>`. The last run is the newest of the 30 newest runs whose result for the scenario has the same expectation hash | A run of a scenario that is not confirmed still sends its steps and stores what it observed. The same hash means the observation belongs to these expected results |
| D5 | When no such run exists, review prints one line for the scenario: `not run yet: semar run --scenario <id> sends it and stores what the service answers` | The person learns how to get the answer, and review does not send anything itself |
| D6 | The details view (`d`) adds, for each step, the stored exchange of that run: the request line, the request body, the status, and the response body. Each text is cut to 2000 characters with `… [cut: N more characters]` | A status alone does not show why a check was not met. The stored exchange is already redacted by the recorder |
| D7 | `plan.md` gains a `Sends` line under each step, with the same content as D1 and D2, cut to 600 characters. It gains no value of a response | `plan.md` is committed. D7 and D11 of Plan 16 keep every response value out of it, and they stand. The script is model output from the repository's own documents, which `plan.md` holds already |
| D8 | The expectation hash stays as it is. The payload is shown and is not part of the approval | A hash that covered the payload would lapse every stored confirmation. `semar plan` never writes over a stored script, so a payload does not change under an approval through a plan. A stricter binding is a later decision |
| D9 | Every text from a script or an exchange goes through `terminalSafe` and `oneLine` in the terminal, and through `shown` in `plan.md`. A line in the list view is cut to 600 characters | A script and a response are untrusted text, and a long body must not push the expected results off the screen |

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
