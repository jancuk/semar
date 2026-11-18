# Semar

Semar is an open-source tool in which an AI agent tests a service the way a QA engineer does. It reads a change and its ticket, writes the tests, runs them against a UAT or a production environment, and says whether the change is ready to ship. It tests an HTTP API and a web application.

Three rules make its results ones that you can trust:

* **A model drafts, and code decides.** A model writes the tests. Code checks every draft and computes every outcome from recorded evidence. No model is asked whether a test passed.
* **A person approves.** An expected result counts only after a person confirmed it.
* **Replay needs no model.** A test is a stored script. To run it again costs no model call, and gives a result that you can repeat.

## What Semar is for

Semar has two aims.

1. **Depend less on QA for manual testing.** A change should not wait in a queue for a person to test it by hand. Semar does not replace the QA engineer. It moves the QA engineer's work to the scenarios, the rules, and the review of what the agent drafted.
2. **Give AI-agent testing one standard.** An agent that tests on its own is hard to trust and hard to compare, because each run tests something different and reports it differently. Semar makes every test follow the same path, whoever starts it and whichever model runs it.

| What is standard | How |
| --- | --- |
| The test | A Gherkin scenario with steps and expected results, and a script with the exact requests |
| The source of each expected result | A label on every expected result: the ticket, a test case, a person, the diff, or a baseline |
| The evidence | Every request and response, recorded by Semar and not by the agent, with secrets removed |
| The verdict | Computed by code from the evidence, with the same rules and the same six outcomes everywhere |
| The report | One layout, tied to the build that was tested |
| The rerun | A stored script that replays with no model |

| Role | What Semar gives them |
| --- | --- |
| QA engineer | Automation in place of manual passes. The QA engineer owns the scenarios, the rules, and the configuration, and reviews what the agent drafted |
| Developer | A change tested from its ticket and its diff, with evidence, without waiting for a manual pass |
| Product manager | A record of what was tested for each ticket, and the questions about intent that only a person can answer |

## What it tests today

| Surface | What Semar does | State |
| --- | --- | --- |
| An HTTP API | Plans tests from a change, prepares data across services, runs business flows across services | Built |
| A web application | Drives Chrome, signs in by itself, records a test while you do the flow, and writes web tests from a plan | Built |
| An Android application | The plan writes the steps of a manual check, and a person records the result | Manual checks only |
| Logs, traces, and databases | Nothing yet | Not built |

The [roadmap](docs/ROADMAP.md) says what comes next.

## Install

Semar is one static binary. You need Go 1.26 or later, `git`, and `make`.

```bash
git clone https://github.com/jancuk/semar.git
cd semar
make build                                         # writes bin/semar
sudo install -m 0755 bin/semar /usr/local/bin/semar
semar version
```

Only `semar plan` needs a model. The default provider is [Claude Code](https://claude.com/claude-code), installed and signed in. Codex, the Anthropic API, and any server with the OpenAI chat format work too. Every other command runs with no model.

## A first run

This walk-through tests one ticket of a service named `orders`. Run the commands inside a clone of the repository of that service.

**1. Write the ticket as a file.** Semar reads `docs/tickets/<ticket key>.md`, and finds the tickets of a change in its commit messages.

```markdown
# SHOP-482 Refund limit

## Acceptance criteria

1. A refund above the order total is rejected with 422 and the error code `REFUND_EXCEEDS_TOTAL`.
2. A refund of the full total sets the status of the order to `refunded`.
3. A refund of an order that does not exist answers 404.
4. Web: the order page shows the refunded amount after a refund.
5. Android: the order screen shows the refunded amount.
```

**2. Set up the repository, once.** `init` asks for the address of the service and for the environment variable that holds the token of a test account. The token itself is never written to a file.

```bash
semar init
export ORDERS_UAT_TOKEN='<the token of a test account>'
```

**3. Plan.** One small model job answers each criterion, and code checks every answer.

```bash
semar plan
```

```text
SHOP-482 (behaviour): 4 scenarios
not drafted: 0
web: 1 criterion explored, 1 test written, 0 left for a person, 9 turns, $0.03
plan: .semar/plans/9f3c2ab41d07/plan.md
model use: 5 jobs (5 haiku), 0 corrections, 9 web turns, 120k input (48k cached), 9.1k output, $0.05
```

The plan wrote three API tests and one web test, and left the Android criterion as a manual check with its steps. Nothing is approved yet.

**4. Run.** A test that is not approved still runs, so that the review can show what the service answered.

```bash
semar run
```

```text
Change    feature/refund-limit against main: SHOP-482
Target    uat (test), orders at 9f3c2ab
Selected  4 scenarios

SHOP-482-01  needs review  an expected result is not confirmed → semar review SHOP-482-01
SHOP-482-02  needs review  an expected result is not confirmed → semar review SHOP-482-02
...
```

**5. Review.** Each item shows the line of the ticket, the test, what the test sends, and what the last run observed. Answer `y` to approve.

```bash
semar review
```

```text
2 of 4 · SHOP-482 · docs/tickets/SHOP-482.md:5
  "A refund above the order total is rejected with 422 and the error code `REFUND_EXCEEDS_TOTAL`."

  Test SHOP-482-02 · API · as default
    A refund above the order total is rejected
    Given Create a paid order of 100000.
          POST /v1/orders
          json {"amount":100000,"dueAt":"{{datetime.plus7d}}"}
    When  Request a refund of 150000 for that order.
          POST /v1/orders/{{order_id}}/refunds
          json {"amount":150000}
    Then  the status is 422 (by model)
          last run r-20261118T004400Z-70f5: observed status 201, not met
    And   $.error.code equals "REFUND_EXCEEDS_TOTAL" (by model)
          last run r-20261118T004400Z-70f5: observed no value, not met

  [y] approve  [n] skip  [d] details  [q] stop
>
```

The test expects what the ticket says, and the service answers 201. That is a defect, so the test is right and you approve it.

**6. Run again, and read where the change stands.**

```bash
semar run
```

```text
SHOP-482-02  failed  step refund-too-much: expected status 422, observed status 201 (source ticket)
SHOP-482-01  passed
SHOP-482-03  passed
SHOP-482-04  passed
report: .semar/runs/r-20261118T010232Z-666f/report.md

On feature/refund-limit against main · uat · orders at 9f3c2ab
5 use cases of SHOP-482

  verified      3
  failing       1
    SHOP-482/scenario-SHOP-482-02  A refund above the order total is rejected
      SHOP-482-02 failed in r-20261118T010232Z-666f: step refund-too-much: expected status 422, observed status 201 (source ticket)
  for a person  1   → semar check <id> pass
    SHOP-482/manual-1  Android: the order screen shows the refunded amount

Ship: no. 1 failing, 1 open.
```

When the fix is deployed, `semar run` replays the same four tests with no model. Do the Android check by hand and record it with `semar check SHOP-482/manual-1 pass`. `semar status` then ends with `Ship: yes`.

## More samples

Each sample has a full section in the [guide](docs/GUIDE.md).

### A test is two files

The scenario is a Gherkin file that product and QA can read. It says what must be true, and where each expected result comes from.

```gherkin
Feature: Refunds of an order

  @id:SHOP-482-02 @ticket:SHOP-482 @surface:api
  Scenario: A refund above the order total is rejected
    Given Create a paid order of 100000.
    # step: create-order; POST /v1/orders
    When Request a refund of 150000 for that order.
    # step: refund-too-much; POST /v1/orders/{id}/refunds
    Then the status is 422
    # ticket docs/tickets/SHOP-482.md:5 A refund above the order total is rejected with 422
    And $.error.code equals "REFUND_EXCEEDS_TOTAL"
    # ticket docs/tickets/SHOP-482.md:5 A refund above the order total is rejected with 422
```

The script holds the exact request of each step. `{{order_id}}` is a value that an earlier step captured. `{{datetime.plus7d}}` is computed at each run, so the test still means "due in a week" next month.

```yaml
apiVersion: semar/v1
kind: Script
scenario: SHOP-482-02
authoredBy: { instance: hand, model: none, harness: none }
steps:
  - step: create-order
    request: { service: orders, method: POST, path: /v1/orders, json: { amount: 100000, dueAt: "{{datetime.plus7d}}" } }
    captures: [{ name: order_id, jsonpath: "$.data.id" }]
  - step: refund-too-much
    request: { service: orders, method: POST, path: "/v1/orders/{{order_id}}/refunds", json: { amount: 150000 } }
```

You can write both files by hand, with no model: `semar script add <file>` stores the script, and `semar run --scenario SHOP-482-02` runs it.

### Data that a test needs

A fixture is a reviewed file that finds or makes a record in a given state, across services, and removes it after the test.

```yaml
apiVersion: semar/v1
kind: Fixture
id: order.paid
description: A paid order of 1000
version: 1.0.0
owner: team-orders
find:
  - request: { service: orders, method: GET, path: /v1/orders, query: { status: paid } }
    captures: [{ name: id, jsonpath: "$.data[0].id" }]
create:
  - request: { service: orders, method: POST, path: /v1/orders, json: { amount: 1000, ref: "semar-{{run.id}}" } }
    captures: [{ name: id, jsonpath: "$.data.id" }]
  - request: { service: payments, method: POST, path: /v1/payments, json: { orderId: "{{id}}" } }
  - request: { service: orders, method: GET, path: "/v1/orders/{{id}}" }
    until: { jsonpath: "$.data.status", equals: paid, timeout: 30s, every: 1s }
outputs: [id]
cleanup:
  - request: { service: orders, method: POST, path: "/v1/orders/{{id}}/cancel" }
```

A scenario names the state and holds no preparation steps: `Given the fixture order.paid as order`. Its script then reads `{{order.id}}`.

### A web test with no locator to write

Do the flow once in Chrome. Hold Option and click what must be true. Semar writes the screens, the scenario, and the script.

```bash
semar login shop --user ana@example.com --password-env SHOP_WEB_PASSWORD   # once
semar record SHOP-482-07 uat --given order.paid as o --open '/orders/{{o.id}}'
```

```text
Recorded 4 actions and 2 checks.
Wrote .semar/screens/shop.yaml (1 screen, 5 elements)
      .semar/scenarios/SHOP-482/SHOP-482-07.feature
      the script of SHOP-482-07
Replayed once: passed.

Next: semar review SHOP-482-07
```

### The ticket changed

Plan again. A criterion that has a test costs no model call and gets no second test.

```bash
semar plan
```

```text
criteria: 6, 4 kept, 2 planned (1 changed), 0 removed
SHOP-482-02 was written for an earlier text of criterion 1. Read both tests. To remove the old one: semar rm SHOP-482-02 --reason "criterion 1 changed"
```

### What only a person can check

```bash
semar status --checklist                           # the steps and the expected result of each manual check
semar check SHOP-482/manual-1 pass --note "the amount shows after a pull to refresh"
semar defer SHOP-482/uncovered-1 --until 2026-12-17 --reason "the change only sends an internal email"
```

### A flow across services

A journey runs leg by leg against one set of builds, and checks what only the whole flow can show.

```bash
semar journey run checkout --env uat
semar journey run --critical --env uat             # a release gate in your own pipeline
```

### In a pipeline

`semar run` exits with 0 when everything passed, 1 when a test failed, and 2 when something could not be judged. An export writes a stored run for your own tools.

```bash
semar run uat --no-plan
semar export junit r-20261118T010232Z-666f --out junit.xml       # also ctrf and sarif
```

### Production

A production environment is read only: Semar sends `GET` and `HEAD`, and refuses every request that changes data. A run there must name the environment.

```bash
semar init --kind production --base-url https://orders.example.com
semar run --all --env production --build <commit>
```

## Commands

A person who knows git needs five commands every day.

| Command | What it does |
| --- | --- |
| `semar run` | Runs the tests of this branch, then says where the change stands |
| `semar review` | Shows everything that needs a person, one item at a time |
| `semar status` | Shows each use case in one group, and ends with `Ship: yes` or `Ship: no` |
| `semar check <id> pass` | Records what a person checked by hand |
| `semar defer <id> --until <day> --reason <text>` | Leaves a use case untested until a day, under your name |

| Command | What it does |
| --- | --- |
| `semar init` | Sets up a repository, or checks this machine against a repository that is set up |
| `semar plan` | Drafts tests for the change of this branch |
| `semar record` | Records a web test |
| `semar login` | Signs in to a web application |
| `semar rm` | Retires a test, and keeps a record of why |
| `semar log`, `semar show`, `semar export` | List the runs, print a report or what a run did, and write a run as CTRF, JUnit XML, or SARIF |
| `semar fixture`, `semar journey`, `semar screens` | Work with fixtures, journeys, and the screens of web tests |
| `semar providers`, `semar dashboard` | Choose the model provider, and see what the plans cost |

The [command reference](docs/GUIDE.md#command-reference) lists every command with every flag.

## What leaves your machine

`semar plan` sends the text of the acceptance criteria, parts of your API document and of the diff, and the names and descriptions of your fixtures and accounts to the model provider that you chose, through your own account or key. In the single pipeline the model can also read a copy of your source. A plan sends no token and no address of your environment. When a plan explores a web page, the model gets a list of what the page shows.

Every other command sends nothing to a model. A run sends requests from your machine to your own environment, and stores the evidence in `.semar/runs/` with secrets removed.

## Documents

| File | What it holds |
| --- | --- |
| [docs/GUIDE.md](docs/GUIDE.md) | Every feature with its file formats and its limits, the configuration, and every command with every flag |
| [docs/ROADMAP.md](docs/ROADMAP.md) | What is built, what comes next and in which order, and what Semar does not do |

## License

Apache-2.0 is the intended license. The repository has no `LICENSE` file yet.
