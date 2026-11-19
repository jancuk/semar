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

This walk-through tests one ticket of a service named `orders`. Run the commands inside a clone of the repository of that service. Four commands do it: `init`, `plan`, and `run`, and `login` for a web application.

**1. Write the ticket as a file.** Semar reads `docs/tickets/<ticket key>.md`, and finds the tickets of a change in its commit messages.

```markdown
# SHOP-482 Limit a refund

## Acceptance criteria

1. A refund larger than the paid amount is refused.
```

**2. Set up the repository, once.** `init` asks for the address of the service and for the environment variable that holds the token of a test account. The token itself is never written to a file.

```bash
semar init
export ORDERS_UAT_TOKEN='<the token of a test account>'
```

**3. Plan.** One small model job answers each criterion, and code checks every answer. A plan approves nothing.

```bash
semar plan
```

```text
SHOP-482 (behaviour): 1 scenario
not drafted: 0
plan: .semar/plans/9f3c2ab41d07/plan.md
model use: 1 job (1 haiku), 0 corrections, 14k input (9k cached), 0.8k output, $0.01
next:
  semar run   run the tests against the service
  the run asks you to review what the plan drafted, and runs the approved tests again
```

**4. Run.** A test that is not approved still runs, so that the review can show what the service answered. When a test needs a person, `run` asks whether to review it now. Answer `y`, or press Enter. Each item shows the line of the ticket, the test, what the test sends, and what the run observed. Answer `y` to approve. The approved tests then run again, and the run ends with where the change stands.

```bash
semar run
```

```text
Change    feature/refund-limit against main: SHOP-482
Target    local (test), orders at 9f3c2ab
Selected  1 scenario

SHOP-482-02  needs review  an expected result is not confirmed → semar review SHOP-482-02
report: .semar/runs/r-20261118T171445Z-a1c1/report.md

1 test needs your review. Review it now? [Y/n]

1 of 1 · SHOP-482 · docs/tickets/SHOP-482.md:5
  "A refund larger than the paid amount is refused."

  Test SHOP-482-02 · API · as support-agent
    Refund above the order total is rejected
    Given Create a paid order for 100000
          POST /v1/orders
          json {"amount":100000}
    When  Request a refund of 150000 for that order
          POST /v1/orders/{{order_id}}/refunds
          json {"amount":150000}
    Then  the status is 422
          last run r-20261118T171445Z-a1c1: observed status 422, met
    And   $.error.code equals "REFUND_EXCEEDS_TOTAL" (source diff)
          last run r-20261118T171445Z-a1c1: observed REFUND_EXCEEDS_TOTAL, met
    When  Read the order again
          GET /v1/orders/{{order_id}}
    Then  $.data.refunded_total equals 0
          last run r-20261118T171445Z-a1c1: observed 0, met

  [y] approve  [n] skip  [d] details  [q] stop
> y
approved SHOP-482-02 as ana (local, unverified identity)

approved 1 of 1 item
SHOP-482-02  passed
report: .semar/runs/r-20261118T171450Z-2175/report.md

On feature/refund-limit against main · local · orders at 9f3c2ab
1 use case of SHOP-482

  verified      1
    SHOP-482/1  A refund above the paid amount is refused
      approved by an author of the change

Ship: yes.
```

The run is two runs: the first one that found the test unapproved, and the second one of the approved test. `semar log` lists both. The exit code is that of the final state, after the second run. When you answer `n`, or run `semar run --no-review`, or run with no terminal, there is no second run, and each result that needs a person ends with the command to type, such as `semar review SHOP-482-02`.

Each day after that, `semar run` replays the stored tests with no model call.

## More samples

Each sample has a full section in the [guide](docs/GUIDE.md).

### A web application

A web application, such as an internal dashboard, needs no token. `init` asks for the address, and Enter answers every other question. It writes an account with no token when the address answers with a web page.

```bash
semar init
```

```text
Address of the service in the test environment: http://localhost:3000
Environment variable that holds the token, or Enter for none:
Who is this account, in one line:
User that this account signs in with on the web, or Enter for none:
Another account? Name, or Enter for none:
No build probe was found at http://localhost:3000/version. The service is on this machine, so the head commit of the repository is its build. To name one, add build: {probe: http, url: <address>, commit: <JSONPath of the commit>} to the service in .semar/semar.yaml.
Wrote .semar/semar.yaml.
The address answers with a web page, so the account default has no token. To add one later, add session and secrets to authProfiles.default in .semar/semar.yaml.
```

`login` opens a window. Sign in once, in any way that the application allows, including through another site, and close the window. With one service, `login` takes no argument. It notes the hosts that the page asked for and the environment does not list, and asks about them.

```bash
semar login
```

```text
the window asked for 2 hosts that environments.uat.hosts does not list, so a run blocks them and a page that needs one can miss data:
  1. login.example.com
  2. cdn.example.com
Add them to environments.uat.hosts in .semar/semar.yaml? Type all, none, or numbers such as 1,3 [none]: all
added 2 hosts to environments.uat.hosts in .semar/semar.yaml
```

Then plan and run as in the first run. A service on this machine runs the head commit of the repository, so `semar run` needs no `--build`. A service that is somewhere else needs `--build <commit>` or a build probe.

```bash
semar plan
semar run
```

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

`semar run` exits with 0 when everything passed, 1 when a test failed, and 2 when something could not be judged. With no terminal it never asks to plan or to review, and `--no-plan` and `--no-review` say so for a runner that has one. An export writes a stored run for your own tools.

```bash
semar run uat --no-plan --no-review
semar export junit r-20261118T010232Z-666f --out junit.xml       # also ctrf and sarif
```

### Production

A production environment is read only: Semar sends `GET` and `HEAD`, and refuses every request that changes data. A run there must name the environment.

```bash
semar init --kind production --base-url https://orders.example.com
semar run --all --env production --build <commit>
```

## Commands

A person needs four commands every day: `init`, `login` for a web application, `plan`, and `run`. `run` asks for the review and ends with where the change stands.

| Command | What it does |
| --- | --- |
| `semar init` | Sets up a repository, or checks this machine against a repository that is set up |
| `semar login` | Signs in to a web application, in a Chrome window or by itself |
| `semar plan` | Drafts tests for the change of this branch |
| `semar run` | Runs the tests of this branch, offers the review of what needs a person, runs the approved tests again, and says where the change stands |

These commands are typed by hand when a person has something to say or to find.

| Command | What it does |
| --- | --- |
| `semar check <id> pass` | Records what a person checked by hand |
| `semar defer <id> --until <day> --reason <text>` | Leaves a use case untested until a day, under your name |
| `semar record` | Records a web test |
| `semar rm` | Retires a test, and keeps a record of why |
| `semar log`, `semar show` | List the runs, and print a report or what a run did |

The other commands are for a script, for a second look, and for what a person has not met yet.

| Command | What it does |
| --- | --- |
| `semar review` | Shows everything that needs a person, one item at a time. `semar run` asks for it, so you type it for a script, for `--yes`, or to look again |
| `semar status` | Shows each use case in one group, and ends with `Ship: yes` or `Ship: no`. `semar run` ends with it. `--html` writes a page, and `--checklist` prints the manual checks |
| `semar export` | Writes a stored run as CTRF, JUnit XML, or SARIF |
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
