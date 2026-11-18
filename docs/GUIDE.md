# Semar guide

This guide describes every feature and every command of Semar as it is built today. The [README](../README.md) says what Semar is and shows a first run. The [roadmap](ROADMAP.md) says what is not built yet.

The examples use a service named `orders`, a web application named `shop`, and a ticket named `SHOP-482`.

## Contents

1. [Features](#features)
2. [Install](#install)
3. [Set up a repository](#set-up-a-repository)
4. [The daily loop](#the-daily-loop)
5. [Plan tests from a change](#plan-tests-from-a-change)
6. [Review what needs a person](#review-what-needs-a-person)
7. [Write a test by hand](#write-a-test-by-hand)
8. [Prepare data with fixtures](#prepare-data-with-fixtures)
9. [Test a web application](#test-a-web-application)
10. [Test a business flow with a journey](#test-a-business-flow-with-a-journey)
11. [See that nothing is skipped: the use case ledger](#see-that-nothing-is-skipped-the-use-case-ledger)
12. [Results](#results)
13. [Command reference](#command-reference)
14. [The files of a repository](#the-files-of-a-repository)
15. [When something goes wrong](#when-something-goes-wrong)

## Features

| Feature | What it does | Where |
| --- | --- | --- |
| A plan from a change | `semar plan` reads the change of a branch, finds its tickets, and has a model draft tests. Code checks every draft | [Plan tests from a change](#plan-tests-from-a-change) |
| One job for each acceptance criterion | A small, cheap model answers one closed question for each criterion. A failing answer goes to the next model. `--max-cost` bounds the spend | [Plan with a small model](#plan-with-a-small-model) |
| Tickets as requirements | A test expects what the ticket says, so it fails where the code does not meet the ticket | [Tickets as requirements](#tickets-as-requirements) |
| Documents | Code compares every drafted request with your OpenAPI or Swagger document | [Tell the planner about your documents](#tell-the-planner-about-your-documents) |
| A trial run | The read-only steps of a draft are sent once, so a wrong path is fixed before you see the plan | [The trial run](#the-trial-run) |
| A second plan | A criterion that has a test costs no model call and gets no second test | [Plan the same ticket again](#plan-the-same-ticket-again) |
| Values that follow the run | `{{date.plus7d}}` and its relatives are computed at each run. A plan refuses a typed-in date near today and a typed-in ID | [Values that follow the run](#values-that-follow-the-run) |
| One review | `semar review` shows each item that needs a person, with what the test sends and what the service answered last | [Review what needs a person](#review-what-needs-a-person) |
| Replay with no model | `semar run` replays stored scripts. Code computes each outcome from recorded evidence | [The daily loop](#the-daily-loop), [Results](#results) |
| Tests by hand | A Gherkin scenario and a YAML script, with no model at all | [Write a test by hand](#write-a-test-by-hand) |
| Several accounts | A step acts as another account with `as`, for access checks | [Act as another account](#act-as-another-account) |
| Fixtures | A reviewed file finds or makes the data that a test needs, across services, and cleans it up | [Prepare data with fixtures](#prepare-data-with-fixtures) |
| Web tests | Semar drives Chrome on a profile of its own, and signs in by itself | [Test a web application](#test-a-web-application) |
| A recorded web test | `semar record` writes a web test while you do the flow once | [Record a web test](#record-a-web-test) |
| Web tests from a plan | A model does the flow in Chrome, and Semar writes it as a test that replays with no model | [Plan writes web tests](#plan-writes-web-tests) |
| Journeys | A business flow across services runs leg by leg against one set of builds | [Test a business flow with a journey](#test-a-business-flow-with-a-journey) |
| The use case ledger | Every use case of a change ends in one stated state, and `semar status` ends with `Ship: yes` or `Ship: no` | [The use case ledger](#see-that-nothing-is-skipped-the-use-case-ledger) |
| Manual checks | What only a person can check has steps, an expected result, and a recorded answer | [The use case ledger](#see-that-nothing-is-skipped-the-use-case-ledger) |
| Evidence and exports | Every request and response is stored with secrets removed. A run exports as CTRF, JUnit XML, or SARIF | [Results](#results), [Send results to your tools](#send-results-to-your-tools) |
| Rules of an environment | Hosts, methods, and an effects policy limit what a test may do. A production environment is read only | [Configuration](#configuration) |
| Model providers | Claude Code, Codex, the Anthropic API, or any server with the OpenAI chat format. A page on your machine manages them | [Choose the model](#choose-the-model) |

Semar tests an HTTP API and a web application. A check on Android stays a manual check for now: the plan writes its steps, and a person records the result.

There are two ways to get tests. Both end with the same `semar run`.

| Way | When to use it | A model is needed |
| --- | --- | --- |
| [A plan](#plan-tests-from-a-change) | A change is deployed to an environment, and you want tests for what changed | Yes, to draft the tests |
| [By hand](#write-a-test-by-hand), or [recorded](#record-a-web-test) | You know what to test, or you want no model at all | No |

## Install

Semar is one static binary with no runtime dependency.

**You need:**

* Go 1.26 or later. The build uses the toolchain named in `go.mod`, and Go downloads it on the first build.
* `git` and `make`.
* For `semar plan` only: a model provider. The default is [Claude Code](https://claude.com/claude-code), installed and signed in, with `claude` on your `PATH`. [Choose the model](#choose-the-model) lists the others. The other commands need no model.

**Build:**

```bash
git clone https://github.com/jancuk/semar.git
cd semar
make build            # writes bin/semar
bin/semar version     # prints "semar dev"
```

**Put it on your `PATH`.** Run one of these from the root of this repository:

```bash
# copy the binary to a directory that is already on PATH
sudo install -m 0755 bin/semar /usr/local/bin/semar

# or keep it here and add an alias to your shell's startup file
echo "alias semar=\"$(pwd)/bin/semar\"" >> ~/.zshrc   # ~/.bashrc for bash
```

The second command writes the full path of the binary into the file, so the alias works from any directory. Open a new terminal afterwards. Do not write `$PWD` in the startup file itself: the shell would replace it with the directory each terminal starts in.

**Other ways to build:**

| Goal | Command |
| --- | --- |
| Set the version that `semar version` prints | `make build VERSION=0.1.0` |
| Build without `make` | `CGO_ENABLED=0 go build -trimpath -o bin/semar ./cmd/semar` |
| Build for another system | `GOOS=linux GOARCH=amd64 make build`. Linux, macOS, and Windows on `amd64` and `arm64` compile. Only Linux and macOS are tested |
| Run every check before a change | `make check`: `gofmt`, `go vet`, `staticcheck`, `govulncheck`, and the tests with the race detector |

Build again after every update of the source: `git pull && make build`.

## Set up a repository

Run `semar init` once, inside a clone of the repository of the service under test.

```bash
cd orders
semar init
```

```text
Found   the build probe at https://orders.uat.example.com/version
wrote .semar/semar.yaml
added knowledge: api (docs/openapi.yaml), docs (docs), tickets (docs/tickets)
export ORDERS_UAT_TOKEN=<the token of the test account> before semar run
```

On a terminal, `init` asks for what it cannot find and no flag gave: the address of the service, the environment variable of the token, and further accounts, each with one line on who the account is. For each account it also asks for the user that the account signs in with on the web, and the variable of its password (Enter for none). It prints the API document, the documents, and the ticket files that it found. It looks for the build probe with one `GET` to `/version` on the address of the service. The probe counts when the answer is JSON with the commit at `$.commit`.

Every question has a flag, and `init` with no terminal asks nothing:

```bash
semar init --base-url https://orders.uat.example.com --token-env ORDERS_UAT_TOKEN \
  --account bob=ORDERS_UAT_TOKEN_BOB --describe "bob=a customer with no orders"
```

**Check a machine.** In a repository that has `.semar/semar.yaml`, `semar init` writes nothing. It checks this machine and prints one line for each item: `ok`, `missing` with the way to fix it, or `review` for what a person has not approved. The exit code is 0 when nothing is missing and 2 otherwise. Run it after you clone a repository that a colleague set up.

### Configuration

`.semar/semar.yaml` describes the environment to test, a UAT or a production one. `semar init` and the first `semar plan` write it from these flags:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--base-url` | none, required unless `semar init` runs on a terminal, which asks for it | The address of the service in the environment |
| `--service` | The name of the repository directory | The name that scripts use for the service |
| `--env` | `uat`, or `production` with `--kind production` | The name of the environment |
| `--kind` | `test` | `test` or `production`. A production environment is written as read only, with the methods `GET` and `HEAD` |
| `--auth-header` | `Authorization` | The header that carries the session |
| `--auth-value` | `Bearer {{secret.TOKEN}}` | The value of that header. `{{secret.TOKEN}}` is replaced at run time |
| `--token-env` | `SEMAR_TOKEN` | The environment variable that holds the token |
| `--build-url` | none | An address of the service that answers with the commit it runs, such as `https://orders.uat.example.com/version` |
| `--build-commit` | `$.commit` | Where that answer holds the commit, as a JSONPath |
| `--describe <account>=<text>` | none | One line on who an account is, such as `default=a customer with paid orders`. The planner chooses the account of a step from it. `default` names the default account. Repeat the flag for more |
| `--account <name>=<ENV>` | none | Another account: an auth profile `<name>` with the same session header and value as `default`, and the token in the environment variable `<ENV>`. Repeat the flag for more accounts. A name that is not a valid profile name, is `default`, or is given twice stops the command with exit 3 |
| `--web-user <account>=<user>` | none | The user that an account signs in with on the web. Needs `--web-password-env` for the same account. `default` names the default account. Repeat the flag for more accounts |
| `--web-password-env <account>=<NAME>` | none | The environment variable that holds the web password of an account. Needs `--web-user` |

The file that they write:

```yaml
version: 1
environments:
  uat:
    kind: test                                 # test or production
    hosts: ["orders.uat.example.com"]         # a request to any other host is refused
    methods: [GET, POST, PUT, PATCH, DELETE]   # remove the methods your tests must not use
    effects:
      mode: test_data                          # read_only refuses every request that changes data
    services:
      - name: orders
        baseUrl: https://orders.uat.example.com
        build:                                 # only with --build-url
          probe: http
          url: https://orders.uat.example.com/version
          commit: $.commit
authProfiles:
  default:
    session: {header: Authorization, value: "Bearer {{secret.TOKEN}}"}
    secrets: {TOKEN: "env:ORDERS_UAT_TOKEN"}
```

With `--account bob=ORDERS_UAT_TOKEN_BOB --account carol=ORDERS_UAT_TOKEN_CAROL`, the `authProfiles` block also holds `bob` and `carol`, each with its own token variable. A scenario step can then act as one of them (see [Act as another account](#act-as-another-account)).

`init` also writes `.semar/.gitignore` when there is none, and never changes one that exists. It keeps `runs/`, `store/redact.key`, `store/fixture-health/`, and `semar.local.yaml` on your machine. The redaction key must stay there: published next to the evidence, it would let a reader guess a short redacted value. Approvals and confirmations are not listed, so the team commits them.

**Production.** The same file can declare a production environment. What Semar may do there is what the file says: the hosts, the methods, and the effects mode.

```bash
semar init --kind production --base-url https://orders.example.com
semar run --all --env production --build <commit>
```

`semar init --kind production` writes a read-only environment: the methods `GET` and `HEAD`, and `effects.mode: read_only`, which refuses every request that changes data. Edit the file to allow more, for example a method and a path that a test account may use. A run against a production environment must name it with `--env`, even when it is the only environment, so that no run reaches production by habit. The line that `run` prints at its start, and the report, say which environment and which kind was tested.

Three rules to know:

* **The token is never in a file.** The configuration names an environment variable. Semar reads it at run time and removes its value from everything it stores. Use an account made for testing, never a personal one.
* **A result belongs to a build.** A run with no recorded build ends as `inconclusive`. With a build probe, Semar asks the service which commit it runs, before and after the run, and a change between the two makes the results `stale`. Without one, declare the build with `semar run --build <commit>`. The report then says that the build was declared and not read from the service.
* **The harness is yours.** `semar plan` starts the `claude` or the `codex` on your `PATH`, or the absolute path in the environment variable `SEMAR_CLAUDE_BIN` or `SEMAR_CODEX_BIN`. The configuration cannot name a program to start, because it is a file of the repository under test.

**Keys for fixtures, journeys, and the trial run.** `semar init` does not write these. Add them by hand when you need them.

```yaml
plan:
  trial: true                                  # false turns the trial run off
environments:
  uat:
    data:
      customers.default: c-1                   # read as {{data.customers.default}} in a fixture
    services:
      - name: payments
        baseUrl: https://payments.uat.example.com
        authProfile: payments-tester           # the session of this service in a fixture or a journey leg
```

| Key | Meaning |
| --- | --- |
| `plan.trial` | `true` by default. `false` turns the [trial run](#the-trial-run) off. It must be a boolean |
| `environments.<env>.data` | The data of the environment: a flat map from a dotted key to a string, at most 100 keys. A fixture reads a value as `{{data.<key>}}`, in a parameter default or in a `with` value |
| `environments.<env>.services[].web.start` | The path below the base URL where the web page of the service starts, such as `/boards`. A plan probes it with one GET, and explores the web criteria only when it answers `text/html`. A service with no `web` starts at `/` |
| `environments.<env>.services[].authProfile` | The auth profile that a fixture step or a journey leg uses for this service, unless the leg or its step has `as`. It must be defined under `authProfiles`. A service with none uses the auth profile of the scenario |
| `authProfiles.<name>.description` | One line, at most 200 characters, with no control character: what the account can do, such as `a member of another board, without the owner's rights`. The planner reads it to draft access checks |

The file takes these keys too:

| Key | Default | Meaning |
| --- | --- | --- |
| `change.base` | The default branch of `origin`, else `main`, else `master` | The branch that the change of a branch is measured against |
| `tickets.pattern` | `[A-Z][A-Z0-9]+-[0-9]+` | The regular expression that finds a ticket key in a commit message |
| `plan.provider` | None | The provider that plans, when your own providers file names no default |
| `plan.maxScenarios` | 12 | The most scenarios that one plan drafts |
| `plan.maxTurns` | 60 | The most turns of one planner job in the single pipeline |
| `plan.timeout` | `15m` | How long one planner job may take |
| `replay.workers` | 4 | How many scenarios run at the same time |
| `policy.trust.confirm` | `[diff, baseline]` | The sources of an expected result that need a person's confirmation. A result that a model wrote always needs one |
| `redact.jsonPaths`, `redact.headers`, `redact.patterns` | None | More values that Semar removes from the evidence before it stores it. The secrets of the auth profiles are always removed |
| `environments.<env>.effects.deny` | None | Requests that are never sent, each as `{method: DELETE, path: /v1/**}` |
| `environments.<env>.effects.once` | None | Requests with an effect that cannot be repeated, in the same form. A fixture and a trial run never send one |
| `environments.<env>.effects.reads` | None | Requests that only read although their method is not `GET`, such as `{method: POST, path: /v1/search}` |
| `environments.<env>.web.chrome` | The Chrome that Semar finds on the machine | The path of the Chrome program |

## The daily loop

A person who knows git needs five commands. `semar --help` lists them first, under "Every day".

```text
$ semar run                 # the tests of this branch, then where the change stands
$ semar review              # everything that needs my eyes, one item at a time
$ semar status              # one group for each use case, and Ship: yes or Ship: no
$ semar check <id> pass     # what I checked by hand
$ semar defer <id> --until 2026-12-17 --reason "..."
```

The change is the range from the base branch to `HEAD`. The base is `change.base` in `semar.yaml`, else the default branch of `origin` as the clone knows it, else `main`, else `master`, and `--base <branch>` names another. `run` and `status` read the range from the clone with no fetch. The tickets of the change are the tickets that `semar changes` finds in the range.

`semar run` prints what it selected before it sends anything: the change, the environment with its kind and build, the number of scenarios and journeys, and the gaps. A gap is a ticket of the change with no use cases, a commit with no ticket, or a scenario that cannot run here. A ticket that has a ticket file and no use cases is not planned: on a terminal, `run` offers to plan it, with the provider, the model, and an estimate, and plans only when you answer `y`. A change with no ticket ends with exit code 2 and names `--all`.

A result has one of these words: `passed`, `failed`, `blocked`, `needs review`, `stale`. `needs review` is a missing approval of a scenario, a fixture, or a screens file. `stale` is a pass on another build, or of a scenario that changed since. Every line of a result that has a next step ends with the command, such as `needs review → semar review SHOP-482-03`. `semar status` names the command once on the line of a group when every use case of the group continues with it, such as `needs review  3  → semar review`.

`semar review` stores the `user.email` of git with each approval, when it is set. `semar status` marks a use case whose every approval came from an author of a commit of the change with `approved by an author of the change`. The mark changes no state and no exit code.

Every command that reads the repository takes `--dir <path>`, the root of the repository that holds `.semar/`. It defaults to the current directory. The [command reference](#command-reference) lists every flag.

## Plan tests from a change

`semar plan` drafts tests for a change. A model drafts, code checks every draft, and a person approves the result in `semar review`. A plan approves nothing.

A plan is made in one of two ways, and `--pipeline` names one:

| Pipeline | When it is the default | How it works |
| --- | --- | --- |
| `criteria` | A ticket of the change has acceptance criteria in a ticket file | One job with no tools for each acceptance criterion. [Plan with a small model](#plan-with-a-small-model) describes it |
| `single` | No ticket of the change has acceptance criteria | One job that reads a copy of the source with read-only tools. [From a change](#from-a-change) describes it |

### From a change

Run these commands inside a clone of the repository of the service under test. `semar plan` with no `--diff` plans the change of the checked-out branch against its base, from the clone, with no fetch. `--diff` names two other branches, and fetches them.

**1. Look at the change.** This step starts no model and writes nothing.

```bash
semar changes --diff main..release
```

`--diff` takes a range, `<base>..<head>`, or the compare link of a code host:

| Code host | Link |
| --- | --- |
| Bitbucket Cloud | `https://bitbucket.org/<workspace>/<repo>/branches/compare/<head>%0D<base>` |
| GitHub | `https://github.com/<owner>/<repo>/compare/<base>...<head>` |
| GitLab | `https://<host>/<group>/<repo>/-/compare/<base>...<head>` |

The base is what runs in production, and the head is what runs in the test environment. Semar fetches both branches from `origin` with your own git, so it needs no token of the code host. It prints the tickets it found in the commit messages, the commits with no ticket, and the changed files.

A link to one pull request is not accepted. Pass the compare link of its two branches, or a range.

**2. Draft the scenarios.**

```bash
semar plan --diff main..release \
  --base-url https://orders.uat.example.com \
  --token-env ORDERS_UAT_TOKEN
```

`--base-url` and `--token-env` are needed only the first time. They write `.semar/semar.yaml`, and later runs read that file. Export the token variable before this step, so that the [trial run](#the-trial-run) can sign in. See [Configuration](#configuration) for the other flags. A repository that `semar init` set up needs neither flag.

What `plan` does in the single pipeline:

1. It copies the source at the head commit to a temporary directory.
2. It starts the planner in that copy. The planner can read a file, search, and list files. It cannot read a file outside the copy, and it cannot reach your environment. With a harness provider it can also use your own MCP servers and skills when you ask with `--with-setup`. See [Choose the model](#choose-the-model).
3. The model returns one draft. Code validates it. Problems go back to the model, for at most three attempts.
4. Code sends the read-only steps of a valid draft once to your environment. A request problem goes back to the model inside the same three attempts. See [The trial run](#the-trial-run).
5. It writes the files below and removes the copy.

While the planner works, a status line on standard error shows the elapsed time, the draft number, and the responses and tool calls of the planner. When standard error is not a terminal, a log gets one line every 30 seconds instead. When the planner ends, `semar` prints how long it took.

| File | Content |
| --- | --- |
| `.semar/scenarios/<ticket>/<ticket>-NN.feature` | One scenario as a Gherkin file: its steps and expected results, with the source of each result |
| `.semar/store/scripts/...` | The exact request of each step |
| `.semar/obligations/<ticket>.plan.yaml` | The use cases of the ticket, for the [ledger](#see-that-nothing-is-skipped-the-use-case-ledger) |
| `.semar/plans/<head commit>/plan.md` | What changed, the scenarios per ticket, the acceptance criteria and what cites each one, what was not drafted and what it needs, the fixtures needed, the journeys to rerun, the manual checks for a person, the files that no scenario covers, the trial run, the documents that the planner was told to read, and the problems of every draft that went back to the planner |

The output ends with the commands of the next steps:

```text
SHOP-482 (behaviour): 2 scenarios
SHOP-490 (config): 1 scenario
not drafted: 1
plan: .semar/plans/9f3c2ab41d07/plan.md
tried: 3 of 5 steps, 0 problems returned, 1 for a second look
model use: 1 attempt, 12 turns, 310k input (270k cached), 9.5k output, $0.84
next:
  semar review SHOP-482-01   read what the plan drafted, a fixture before the scenarios that use it, and approve it
  semar run --all --build 9f3c2ab41d07   run the approved scenarios against the service
  semar status   where things stand
```

`model use` is what the plan cost, summed over its attempts. The cost shows when the provider reports it. The planner is given the change table, the diff, and the API operations that the diff names in its prompt, and a draft that is sent back is repaired in a job of at most 12 turns, so a plan stays short.

Read `plan.md` and the scenario files before you run them. The model read them from the code. The only requests sent so far are the read-only ones of the [trial run](#the-trial-run).

**3. Run against the environment.**

```bash
export ORDERS_UAT_TOKEN='<the token of a test account>'
semar run --all --build 9f3c2ab41d07
```

The first run shows whether the drafted requests fit the service. Every scenario ends as `needs review`, because nobody confirmed the expected results yet. The run still stores what the service answered, and the review of the next step shows it.

**4. Confirm the expected results.** A model's reading of a diff is not a requirement, so a person accepts each expected result before it counts.

```bash
semar review          # shows each item that needs a person, and asks
```

[Review what needs a person](#review-what-needs-a-person) says how to read an item.

**5. Run again for the result.**

```bash
semar run --all --build 9f3c2ab41d07; echo "exit $?"
semar show --activity        # every request that was sent, in order
```

**What `plan` does not do yet:**

* It does not read the tracker. A ticket is its key and the commits that name it, and the file `docs/tickets/<key>.md` when the repository has one: see [tickets as requirements](#tickets-as-requirements).
* It does not invent data of the environment. A scenario that needs an existing record, such as the ID of an order that the test account may read, names a [fixture](#prepare-data-with-fixtures) when the repository has one that fits. When none fits, the criteria pipeline drafts the fixture: see [Plan with a small model](#plan-with-a-small-model). The single pipeline lists the scenario in `plan.md` under "Not drafted" with what it needs, and under "Fixtures needed" with a proposal, and a person writes the fixture.
* It tries only the read-only steps against the service, once. It never tries a step that writes.

**What leaves your machine.** `plan` sends parts of your source and the diff to the model provider that you chose, through your own account or key. The line that `plan` prints at its start names the provider. It sends no token, no address of your environment, and no file outside the copy. `changes`, `run`, `review`, `status`, and `show` send nothing to a model. The trial run sends requests from your machine to your own environment. It sends nothing of the responses to the model except a status number and the name of a capture.

### Plan with a small model

`semar plan` plans one acceptance criterion at a time when at least one ticket of the change has acceptance criteria in a ticket file. Otherwise it plans in one job that reads the repository with tools, and says why on standard error. `--pipeline single` and `--pipeline criteria` choose as before. The criteria pipeline changes the shape of the job so that a small, cheap model can do it. Code does the research, a model answers one closed question for each acceptance criterion, code checks the answer, and only an answer that fails goes to a bigger model.

```bash
semar plan --diff main..release
semar plan --diff main..release --ladder claude:haiku,claude:sonnet,claude:opus
```

It needs the tickets and the API document in `knowledge`, as in [Tickets as requirements](#tickets-as-requirements) and [Tell the planner about your documents](#tell-the-planner-about-your-documents). For each acceptance criterion, Semar finds the operations of the API document that the ticket reaches (by a route or an operationId in the diff or the ticket, or by a router line of a changed file whose handler changed), their parameters, body fields, and response fields, the diff of the ticket's files, the fixtures, and the accounts. A job with no tools gets all of that in its prompt and answers in a fixed shape: a scenario, a manual check, a fixture that it needs, or a "not drafted" entry. It picks operations, accounts, and fixtures from lists and never writes a method, a path, or the cite. Code checks the answer and says what is wrong in plain words, for example `field amountt does not exist in post_v1_orders_orderId_refunds; its fields are amount, reason`. A failing answer goes back once to the same model, and then to the next model of the ladder. The files, the obligations, and the ledger are the same as for the single planner.

* **The ladder.** `--ladder` lists `<provider id>:<model>` steps from the cheapest up. `plan.ladder` in your own `providers.yaml` does the same for every plan. A repository cannot set it, because it decides which provider receives your code. With neither, a Claude Code provider plans with `haiku`, then `sonnet`, then the model of the provider, and every other provider plans with its own model. `--model` names one model and builds no ladder. `--ladder` with `--pipeline single` is an error.
* **No tools, no setup.** The jobs run in an empty directory. `--with-setup` with `--pipeline criteria` is an error, and without `--pipeline` it keeps the single pipeline.
* **The cost.** The last line says how the work went: `model use: 16 jobs (14 haiku, 2 sonnet), 3 corrections, 92k input, 21k output, $0.09`. A job is a criterion, counted under the model that answered it. `plan.md` lists the model that answered each criterion, and the problems that went back to a model.
* **A limit on the cost.** `--max-cost 2.50` or `plan.maxCost` in your own `providers.yaml` bounds what one plan may spend, as the providers report it. When the sum reaches it, no new job starts, the jobs that run finish, and the plan is written with what was answered. The criteria that were not reached are listed as "not planned: the cost limit of $2.50 was reached", and the exit code is 2. A provider that reports no cost, such as Codex, is not bound, and the plan says so at the start. In the single pipeline the limit is checked between attempts. A repository cannot set it.
* **Fixtures that it drafts.** A criterion that needs data which no fixture gives names the fixture it needs: an ID, one sentence on the state, and the values a step reads. A job with no tools then drafts that fixture, at most 5 in a plan, from the operations of your API documents: the requests that make the state, and the requests that remove it. When you have two or more accounts, the job sees them with their descriptions and gives a step `as` when it makes something that belongs to another account. When the one sentence on the state names an account, such as `a board owned by carol`, and no step acts as that account, code sends the answer back with `no step acts as carol: set as on each step that carol sends`. Code checks it with the rules of a fixture file, and the plan runs it once on a test environment that allows writes, and cleans up, unless `--no-trial` is set. A fixture that fails goes back to the model with what the environment answered. The file is written to `.semar/fixtures/<id>.yaml` with `owner: semar` and is not approved. The criteria that needed it are planned again with it, and their scenarios name it under `Given`. `plan.md` lists each fixture under "Fixtures drafted", with the scenarios that use it, and `semar review <id>` shows it before the scenarios. A fixture that no model could write leaves its criteria as "not drafted" with the reason.
* **Reuse.** The answers are kept in `.semar/plans/<head commit>/answers.json`. A later plan reuses the answer to a criterion whose packet is unchanged, and checks it again. A criterion that has a test already is not planned at all: see [Plan the same ticket again](#plan-the-same-ticket-again).
* **Write criteria as Given, When, and Then.** A criterion is a numbered line, or a group of lines that starts with `Given`, with `When`, `Then`, and `And` lines after it, under the heading "Acceptance criteria". A small model plans the second form more reliably, because it already says the setup, the request, and the result. `plan.md` suggests it when a ticket has only prose.

```markdown
## Acceptance criteria

Given a paid order
When a support agent refunds more than the order total
Then the API answers 422 with the error code REFUND_EXCEEDS_TOTAL
And the order stays as it was
```

### Tickets as requirements

A ticket file is what the team agreed before the code was written. When the code does not do what the file says, a test that expects what the code does cannot find the defect. So the planner expects what the ticket says, and the run fails where the code does not meet it.

```yaml
knowledge:
  - id: tickets
    kind: tickets
    path: docs/tickets       # holds SHOP-482.md, SHOP-483.md, ...
```

`semar init` writes this entry when `docs/tickets/` exists. A `tickets` source names a folder, and it has no `service`. Semar reads `<folder>/<ticket key>.md` for each ticket of the change from the copy of the source at the head commit. A ticket with no file there is named in one line on standard error, and its expected results come from the code, as before. A file that is not a regular file is not read, and a file above 64 KiB is cut with a marker that says so.

**What the planner is told.** The prompt shows each file as data, numbered by line, inside a fence that its own text cannot end. It cannot change the schema, the rules, or a result: a person still confirms every expected result. The planner is told to:

* give each criterion that a caller of the API can observe at least one expected result with the source `ticket` and a cite of the form `<ticket file>:<line> <one sentence>`, which is written in the comment under the `Then` line as `# ticket <ticket file>:<line> <one sentence>`. Code checks that the file is the ticket file of the scenario's ticket and that the line exists. A scenario whose ticket has no file cannot use the source `ticket`, and the problem goes back to the planner;
* expect what the criterion says where the code does not meet it, add a `differences` entry, and name the scenario after the requirement. `plan.md` marks that scenario with `This scenario is expected to fail on this build: the code does not meet docs/tickets/SHOP-482.md:14.` The mark is a prediction that a person can check in the run;
* put a criterion that only a person can check, on a screen, on a device, or by a gesture, under `manualChecks`: the ticket, a title, 1 to 20 steps, the expected result, and the cite of the criterion;
* account for every criterion under a heading that contains "Acceptance criteria", a numbered line or a group of Given, When, and Then lines. Code reads those lines and checks that each one is cited by a scenario with the source `ticket`, by a manual check, or by a `notDrafted` entry with a `cite`. A criterion that nothing cites goes back to the planner. If it is still missing after the last draft, the plan is written and the criterion becomes an obligation `<ticket>/criterion-<n>` with the gap "criterion not accounted for by the planner", so the ledger keeps it. `plan.md` shows a table "Acceptance criteria" in each ticket's section, with what cites each one;
* test a record that is not found with an ID that cannot exist, `missing-{{run.id}}`, in the path. It may not use any other made-up ID.

**Manual checks.** The model names the surface of each one: `web`, `mobile`, or `other`, and the obligation keeps it as `manual.surface`. A `web` check that the plan explored and wrote as a test has no manual check: see [Plan writes web tests](#plan-writes-web-tests). Each manual check is listed in `plan.md` under its ticket, and becomes an obligation `<ticket>/manual-<n>` in `.semar/obligations/<ticket>.plan.yaml`, with `source: criterion` and a `manual` reason of the kind `judgement` that is written by `ai`. It derives `needs_a_person`, and `semar status --checklist` prints its steps and its expected result. A person records the result with `semar check`.

**Accounts.** When `semar.yaml` has two auth profiles or more, the prompt lists their names and their one-line `description`, and tells the planner to add a scenario for the most likely access mistake, with `as`. The planner may use only these names. An unknown name is a problem that goes back to the planner. A [trial run](#the-trial-run) does not send a step that acts as another account, because it has the session of the plan's account only.

**Expected results from the planner.** Every expected result that the planner drafts is marked `, by model` in the comment under its `Then` line, with the source that the planner gave, `diff` unless it cites a ticket. It always needs a person's confirmation.

**What `plan.md` adds.** "How this plan was made" lists the problems of every draft that went back to the planner, at most 30 for each draft, escaped. When a trial sent nothing, the summary line of `semar plan` says why, for example `tried: 0 of 27 steps (every step needs a write or a fixture create, which a trial never sends)`.

Code cannot check that a cited line says what the planner claims. `semar review` opens each item with that line, and shows the test in the words of its Gherkin file, so a person compares them. `d` prints the whole file.

### Tell the planner about your documents

The planner reads the code, and the code says what the service does. A document says what the service should do. When the planner reads both, it can find a route that the API document does not list, and a rule that the code does not follow.

**The convention.** Keep the API document at `docs/openapi.yaml` (or `docs/swagger.yaml`), and the other documents of the service under `docs/`. `semar init` finds these paths, writes the entries with the IDs `api` and `docs`, and prints a line such as `added knowledge: api (docs/openapi.yaml), docs (docs)`. It also writes `tickets` for the folder `docs/tickets` when that exists (see [tickets as requirements](#tickets-as-requirements)). The planner uses only what the file lists. These names of an API document are found, and the first one in this order is used:

`docs/openapi.{yaml,yml,json}`, `docs/swagger.{yaml,yml,json}`, `api/openapi.{yaml,yml,json}`, `api/swagger.{yaml,yml,json}`, `openapi.{yaml,yml,json}`, `swagger.{yaml,yml,json}`

The folder `docs` is found when it exists. A link is never followed. When Semar is set up in a folder below the root of the repository, it looks in that folder and writes each path from the root, for example `services/orders/docs/openapi.yaml`.

**The file.** The `knowledge` section of `.semar/semar.yaml` is a list:

```yaml
knowledge:
  - id: api
    kind: openapi
    path: docs/openapi.yaml
    service: orders          # optional
  - id: docs
    kind: files
    path: docs
```

| Kind | What `path` names | What Semar does with it |
| --- | --- | --- |
| `openapi` | One OpenAPI 3 or Swagger 2 file, in YAML or JSON | The planner is told to read it. Code compares every drafted request with it. `service` is optional: when it is set, only the steps of that service are compared |
| `files` | A file or a folder of documents | The planner is told to read it before it drafts |
| `tickets` | A folder of `<ticket key>.md` files | The planner reads the file of each ticket of the change as a requirement. See [tickets as requirements](#tickets-as-requirements) |

A source needs a unique `id`. A `path` is relative to the root of the repository, and it cannot leave the repository. A list holds at most 16 sources.

**What `plan.md` shows.** When the file lists a source, `plan.md` has a section "Documents" after "Files that no scenario covers". It has three parts. The first part names each source and what Semar found:

```text
- api: the API document docs/openapi.yaml (OpenAPI 3.0.3, 42 operations)
- docs: the documents under docs (12 files)
- old: docs/old is not in the copy of the source
```

The second part is "Compared with the API document". Code writes one line for each difference between a drafted request and the API document: a route that the document does not have, a method that it does not have on that route, or an expected status that it does not declare.

```text
- SHOP-482-01, step refund: status 422 is not declared for POST /v1/orders/{id}/refunds; the document declares 200 and 400
```

The third part, "Where the planner says a document and the code differ", holds what the planner reported. Each entry names the ticket, a place in the document, a place in the code, and what differs. Code checks that both places exist. It does not check that the two texts differ. `semar plan` also prints the number of both kinds of difference in its summary, as `differs from the documents: <n>`.

**Limits.**

* Semar reads the files from the copy of the source at the commit that is planned. It does not read the working tree, and it fetches nothing from a network.
* A difference from the API document is reported. It never rejects a draft, because the document can be the stale one. A route that the change adds is often missing from it.
* Semar does not compare the fields of a request or a response with the schemas of the API document yet.
* An expected result still comes from the code, and a person still confirms it.
* Code cannot tell whether a document and the code really differ. A person decides which one is right.
* Semar cannot tell whether the planner opened a document. It tells the planner to read it.
* An API document in YAML can be 2 MiB at most, and one in JSON 5 MiB. A YAML document above its limit is still given to the planner as a document, and no request is compared with it. A file above 5 MiB is left out of the copy.
* The planner cannot report a difference for a document whose file name holds a space or a colon.
* A wiki or a tracker outside the repository is not reached through this section. A harness provider reaches it through your MCP servers, with `--with-setup`.

**A repository that has a configuration already.** Add the `knowledge` section to `.semar/semar.yaml` by hand, and commit the documents: a document that is only in your working tree is reported as not in the copy. When the file lists no source and a conventional path exists, `semar plan` prints a hint:

```text
semar: the repository has docs/openapi.yaml and docs; list them under knowledge in .semar/semar.yaml so that the planner reads them first
```

### The trial run

A draft can have a wrong path, a wrong method, or a capture that finds nothing, and none of that shows until the first run. So after a draft passes validation, code sends the read-only steps of the draft to the environment, once, and returns the problems to the planner inside the three attempts that it already has.

**What is sent.** Only a step that the effects policy of the environment calls a read: a `GET` or `HEAD`, or a request that you list under `effects.reads`, and never one under `effects.deny`. A step that writes is "not tried". So is a step that matches `effects.once`, a step that needs a value that no read gave, and a fixture `create`. A fixture `find` is sent, so the trial shows whether a record exists. A trial sends at most 50 requests, each with a 10 second timeout, and stops after 2 minutes. The trial uses the auth profile of the plan, so the token variable of that profile must be set when you run `semar plan`.

**What goes back to the planner.**

| The environment answered | What happens |
| --- | --- |
| 405 | Back to the planner: the method is likely wrong |
| 404 on a route that the API document had before the change, or that no API document has | Back to the planner: the path or the route is likely wrong |
| 404 on a route that only the new API document has, or when there is no API document | Not sent back. `plan.md` lists it under "Needs a second look", because the route may be new |
| 400 or 422 | Back to the planner: a parameter or a header is likely wrong |
| 401 or 403 | Back to the planner, with the name of the auth profile |
| A capture finds nothing | Back to the planner |
| 5xx | Not sent back. `plan.md` lists it under "Needs a second look", because it may be a defect |

A step whose expected results include a status from 400 to 499 gets no status problem, because a refusal is often the behaviour under test. The planner is told the status number and the capture name. It never sees a body, a header, or a value of a response, so it cannot copy a response into an expected result. After a trial, a later draft must keep the expected results of each scenario that it keeps, and may not add a step with expected results to one. A scenario first drafted after a trial is marked in `plan.md`, so check that its expected results come from the code.

**What it never does.** A trial result is not a test result. It never counts as a pass, and no expected result is compared with it. Problems that remain after the third attempt are listed in `plan.md` under "Problems that remain", and the plan is still written.

`plan.md` has a section "Trial run" with the result next to each step:

```text
### SHOP-482-05 A paid order is read back as paid

- given order (order.paid): find found a record
- read-paid-order: GET /v1/orders/{id} answered 200, capture status found
- refund: not tried: writes
```

The section names the build that the environment ran. The change is usually not deployed yet, so that is often the build before it. Every request of the trial is stored in a run of its own, and `semar show <run> --activity` lists them.

**Turn it off.** Pass `--no-trial` for one run, or set `plan.trial: false` in `.semar/semar.yaml` (it is `true` by default). The trial is skipped, with one line on standard error, when no account can be prepared, for example because the token variable is not set, or when the environment is a production one that `--env` did not name. A trial never makes `semar plan` fail.

### Choose the model

`semar plan` works with these kinds of provider:

| Kind | What it is | Sign-in |
| --- | --- | --- |
| `claude` | Claude Code on your machine. The default | Its own sign-in, a subscription or a key |
| `codex` | Codex on your machine | Its own sign-in |
| `anthropic` | The Anthropic API | An API key |
| `openai` | The OpenAI API, and any server that speaks the same chat format, such as DeepSeek, OpenRouter, or a local server | An API key. None for a server on your own machine |

**The dashboard.** One command opens a page on your own machine:

```bash
semar dashboard
```

The page lists your providers and shows for each one whether it is installed and signed in. There you can:

* add a provider, and paste an API key, which goes to the keychain of your operating system;
* start the sign-in of Claude Code or Codex;
* choose the provider that plans, its model from a list, and its reasoning level;
* turn your MCP servers and skills on or off for a harness provider (a plan loads them only with `--with-setup` or `plan.withSetup`);
* see the usage: runs, turns, tokens, and cost per provider and model, and for Claude Code the subscription windows that it reported after the last run.

The page runs until you press Ctrl+C. It listens on `127.0.0.1` only, and its address holds a token that every request needs.

**The file.** The dashboard writes `~/.config/semar/providers.yaml`, and you can edit the file by hand instead. A reload of the page shows your edit.

```yaml
version: 1
default: claude                 # plans when nothing else names a provider
providers:
  - id: claude
    kind: claude
    model: ""                   # "" means the provider's own default
    reasoning: high             # optional; "" means the model's own default
    setup:
      mcp: true                 # your MCP servers
      skills: true              # your skills
      deny: []                  # tool names that never run

  - id: anthropic
    kind: anthropic
    model: claude-opus-5-5
    apiKey: env:ANTHROPIC_API_KEY

  - id: deepseek
    kind: openai
    baseUrl: https://api.deepseek.com
    model: <model name>
    apiKey: keychain:deepseek   # stored by the dashboard
```

The file never holds a key. `apiKey` is `env:NAME`, an environment variable, or `keychain:NAME`, an entry that the dashboard stored. Set `SEMAR_HOME` to keep the file in another directory.

**Which provider plans.** In this order: the flag `--provider <id>`, then `default` of your file, then `plan.provider` of the repository's `semar.yaml`, then the built-in `claude`. Your file comes before `semar.yaml`, so a repository cannot choose where your source goes. `semar providers` prints the list and marks the one that would plan in the current directory.

```bash
semar providers
semar plan --diff <link or range> --provider deepseek --model <model name>
semar plan --diff <link or range> --reasoning high
```

**Your MCP servers and skills.** A harness provider can load them, with your own permission rules, but a plan runs without them unless you ask: see `--with-setup` below. Your hooks and your instruction file are not loaded for Claude Code. Nothing inside the repository under test is loaded: no settings, no MCP file, no skill.

Know three things before you rely on this:

* The planner reads a repository while it has these tools. Text in a file can ask it to call one. Your permission rules decide which tools run, and `setup.deny` names tools that never run. `plan.md` records how many tool calls were made and how many were refused.
* It costs more. Every tool and skill is described to the model on every turn. On one machine with many plugins, the setup added about 51,000 input tokens to every turn of a Claude Code job, and a plan runs for tens of turns.
* A plan therefore has the three file tools only. `--with-setup` loads the setup for one run. `plan.withSetup: true` in your providers file loads it for every plan. The repository's `semar.yaml` cannot turn it on. `--with-setup=false` turns a `plan.withSetup: true` off for one run.

```bash
semar plan --diff <link or range> --with-setup
```

```yaml
version: 1
default: claude
plan:
  withSetup: true               # plan with your MCP servers and skills
```

An API provider never has MCP servers or skills. It runs with the three file tools.

Codex differs in three ways: it always reads your own instruction file, it does not support `setup.deny`, and it has no limit on turns.

### Plan the same ticket again

A ticket grows: someone adds a criterion, or edits one. Run `semar plan` again. In the criteria pipeline, a plan writes a test only for a criterion that has none.

Semar gives each criterion a key: a hash of its whole text, without its list number, and with each run of white space read as one space. The key is stored on the use case of the criterion in `.semar/obligations/<ticket>.plan.yaml`:

```yaml
- id: SHOP-482/scenario-SHOP-482-04
  title: An order that is paid cannot be cancelled
  source: diff
  covers: [SHOP-482-04]
  criterion: {number: 5, key: "3f6c…"}
```

Before any model call, code gives each criterion of the ticket one state:

| State | When | What the plan does |
| --- | --- | --- |
| Kept | An earlier plan wrote a test for the same text, and the scenario file still exists. A criterion with a manual check is kept too | Nothing: no model call, no browser, and no second test |
| New | Nothing covers the criterion. A criterion that no model could draft before is new, and so is one whose test you removed with `semar rm` | Plans it |
| Changed | The criterion at that number has another text than the one that an earlier test was written for | Plans it, and names the earlier tests |
| Removed | A test or a manual check belongs to a text that the ticket no longer has | Names the test |

The plan never removes or rewrites a test, because a person approved it. It prints the command for you instead:

```text
criteria: 9, 7 kept, 2 planned (1 changed), 0 removed
SHOP-482-04 was written for an earlier text of criterion 5. Read both tests. To remove the old one: semar rm SHOP-482-04 --reason "criterion 5 changed"
```

When nothing is missing, the plan calls no model, writes nothing, and exits 0:

```text
Every criterion of SHOP-482 has a test or a manual check already. Nothing was planned. To plan one again, remove its test with semar rm.
```

`plan.md` lists each kept, changed, and removed criterion with its tests under "Criteria that earlier plans covered". The cost estimate that `semar run` shows before it offers a plan counts only the criteria that the plan would take.

**Limits.**

* The single pipeline has no criterion to keep, so it writes every test again.
* A criterion that was split in two, or two that were merged, show as removed and new.
* A manual check keeps its criterion. To plan that criterion again, delete its entry from the obligations file. There is one exception: the manual check that a web test leaves for the part that it does not prove goes with that test, so `semar rm` of the web test makes the next plan write it again.
* An entry that a plan wrote before keys existed has no key. It is matched by the first sentence of its criterion. When that sentence changes, the old test shows as removed and the criterion as new.

### Values that follow the run

A test that a plan writes today must give the same result next month. So a script holds no date that is near the day of the plan, and no ID of a record that no step made.

**Date variables.** A date variable is computed at each run:

| Variable | Example value | Form |
| --- | --- | --- |
| `{{date.today}}` | `2026-11-18` | A day, in the time zone of the machine |
| `{{date.plus7d}}`, `{{date.minus90d}}` | `2026-11-25`, `2026-08-20` | A day, some days later or earlier |
| `{{datetime.now}}` | `2026-11-18T07:15:02Z` | An instant in UTC, as RFC 3339 |
| `{{datetime.plus2h}}`, `{{datetime.minus1d}}` | `2026-11-18T09:15:02Z`, `2026-11-17T07:15:02Z` | An instant, later or earlier |
| `{{local.now}}` | `2026-11-18T14:15` | The form of a `datetime-local` field of a web page, in the time zone of the machine |
| `{{local.plus1d}}`, `{{local.minus30m}}` | `2026-11-19T14:15`, `2026-11-18T13:45` | The same form, later or earlier |

The offset is `plus` or `minus`, a number from 1 to 36500, and a unit: `d` for days, `h` for hours, or `m` for minutes. `date` takes days only. No other spelling exists.

Every date variable of one run is computed from the time inside the run ID, such as `r-20261118T071502Z-9c1e`. So two steps of one run agree, and a person who reads the evidence can compute the dates again.

A date variable works wherever `{{run.id}}` works: in a request, in an expected value, in a parameter of a fixture, in a journey leg, in the text that a web step types, and in the text that a web check expects.

```yaml
steps:
  - step: create-order
    request: { service: orders, method: POST, path: /v1/orders, json: { customerId: "{{customer.id}}", dueAt: "{{datetime.plus7d}}" } }
```

**What a plan refuses.** Code reads every draft before it is written, and sends these back to the model:

* **A typed-in date near today.** A text that starts with a real `YYYY-MM-DD` less than 3650 days from the day of the plan is refused. The message names the variable that gives the same day. A fixed date at least 10 years away is allowed, for a value that must not move. A date that the ticket file names is kept, because a criterion that names a day means that day.
* **A typed-in ID.** In the criteria pipeline, a step that expects no 4xx status may not send a typed-in value in a query parameter or a top-level body field that is an ID: a name of `id`, a name that ends in `Id`, `ID`, `_id`, `Ids`, `IDs`, or `_ids`, or the format `uuid`. The value must come from a capture or a fixture. It is kept when the API document lists it in an enum, or when the ticket names it.

```text
scenarios[0].steps[1]: json.dueAt holds the date 2026-11-25T10:00:00Z, which is near today and will be wrong on a later day: write {{datetime.plus7d}}, or a date at least 10 years away when the value must not move
steps[0].body[0]: field customerId of createOrder holds the ID "c-1042", which no step or fixture gave: capture it in an earlier step, take it from a fixture, or name it in the ticket
```

**Limits.**

* The zone of `date` and `local` is the zone of the machine that runs the test. The configuration has no key for it.
* Only the form `YYYY-MM-DD` is read. A date such as `18/11/2026`, a number of seconds, or a time of day is not.
* A date that a person types in `semar record`, in a fixture file, or in a journey file stays as typed.
* The ID rule is not in the single pipeline, and it does not read a field inside an object or an array.
* A stored script is never checked again, and a run never refuses a script for a typed-in date.
* The name of a fixture in a scenario (`as`) and the ID of a journey leg may not be `date`, `datetime`, or `local`.

## Review what needs a person

Nothing that a plan, a recording, or a person writes counts until a person approves it. `semar review` is the one place for that. It shows the fixtures that are not approved, the screens files that are not approved, the scenarios that need a confirmation, and the journeys, one item at a time.

```bash
semar review                 # everything that waits
semar review SHOP-482        # the items of one ticket
semar review SHOP-482-02     # one scenario
semar review order.paid      # one fixture
```

Answer `y` to approve an item, `n` to skip it, `d` for its details, and `q` to stop. `--yes` approves every item with no question. With no terminal and no `--yes`, the items are listed and nothing is approved. The exit code is 0 when nothing is left to review, and 2 when an item was skipped or the review stopped.

```text
2 of 16 · SHOP-482 · docs/tickets/SHOP-482.md:9
  "A refund above the order total is rejected with 422 and the error code REFUND_EXCEEDS_TOTAL."

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

How to read an item:

| Part | Meaning |
| --- | --- |
| The first line | The position, the ticket, and the place of the criterion that the test cites. The line under it is that line of the ticket file, read from your working tree. A cite to a line that is not there prints `line not found` |
| `Test … · API · as default` | The scenario, its surface, and the account that it acts as |
| The lines under a step | What the stored script sends: the method and the path, then `query`, `headers`, and `json`. A web step shows its action, its element, its screen, and its text. A variable stays `{{name}}`, and a secret shows as `the value of $NAME` |
| `(by model)` | A model wrote the expected result. `(source diff)` ends a result whose source is not the ticket |
| `last run …` | What the newest run observed for this expected result: `met`, `not met`, or `not evaluated` with the reason |

**Run before you review.** A scenario that is not approved still runs. Its result is `needs review`, and the run stores what the service answered. So the order plan, run, review, run gives the review the real answer beside each expected result, and you can see that a test expects 422 where the service answers 201 before you approve it. With no run, review prints one line:

```text
  not run yet: semar run --scenario SHOP-482-02 sends it and stores what the service answers
```

The last run is the newest of the 30 newest runs that judged the same expected results. A trial run, a fixture check, and a recording do not count.

**The details.** `d` prints the whole scenario file. For each step it adds the stored exchange of the last run: the request with its method and address, the request body, the status, and the response body. Each text is cut at 2000 characters. The evidence was redacted when it was stored, so a token does not show.

**What an approval covers.** An approval covers the steps and the expected results of the scenario. The payload is shown and is not part of the approval, so a change of a script does not ask for a new one. `semar plan` never writes over a stored script.

**Who approved.** An approval is stored with your user name and the words "local, unverified identity", and with the `user.email` of git when it is set. `semar status` marks a use case whose every approval came from an author of the change.

`plan.md` shows the same `Sends` line under each step, and no value of a response, because the file is committed. A journey is shown without its script and without a run.

## Write a test by hand

A test is two files: a scenario that says what must be true, and a script that holds the exact requests. You can write both, and no model is involved.

### Hand-written scenarios

**1. Write the configuration,** or let `init` write it:

```bash
semar init --base-url https://orders.uat.example.com --token-env ORDERS_UAT_TOKEN
```

**2. Write a scenario** in `.semar/scenarios/SHOP-482/SHOP-482-01.feature`. It is a Gherkin file, the format that QA engineers know from Cucumber. It says what must be true, and where each expected result comes from:

```gherkin
Feature: Refunds of an order

  @id:SHOP-482-01 @ticket:SHOP-482 @surface:api
  Scenario: Refund above the order total is rejected
    Before: a refund larger than the order total was accepted.
    Now: the API rejects it with 422 and leaves the order unchanged.

    Given Create a paid order for 100000
    # step: create-order; POST /v1/orders
    When Request a refund of 150000 for that order
    # step: refund-too-much; POST /v1/orders/{id}/refunds
    Then the status is 422
    # ticket SHOP-482: a refund above the order total must be rejected
    And $.error.code equals "REFUND_EXCEEDS_TOTAL"
    # diff
```

How to read it:

| In the file | Means |
| --- | --- |
| `@id:`, `@ticket:`, `@surface:` | The scenario's ID, its ticket, and `api` or `web`. All three are required. `@auth:<profile>` names the auth profile, and nothing means `default`. `@noauth` means no profile |
| The lines under `Scenario:` | The intent: what was true before, and what is now |
| `Given` | A setup step: it prepares data and makes no claim. It has no expected result |
| `When` | A step with a request, and expected results |
| `# step: <id>; <METHOD> <path>` | The comment under a step. It holds the step's ID, which reports and scripts use, and its request path as a hint. A web step has no hint: `# step: <id>` |
| `Then`, `And` | An expected result of the `When` step above. Write one sentence from the table below |
| `# <source> <cite>` | The comment under a `Then` line. The source is `ticket`, `testcase`, `person`, `diff`, or `baseline`. A model's result ends `, by model` |

A `Then` line is one of these. A value is JSON, so a string is in double quotes:

| Sentence | Checks |
| --- | --- |
| `the status is 422` | The HTTP status |
| `$.error.code equals "X"`, `does not equal`, `contains` | The value at a JSON path |
| `$.data.id exists`, `$.data.id does not exist` | That the path selects a value, or does not |
| `$.data.total is greater than 0`, `is less than 100` | A number |
| `$.data.items has 3 items` | How many values the path selects |
| `$.a equals the value at $.b` | Two values of the response |
| `the header Location contains "/v1/"` | A response header, with the same operators as a path |
| `the element todo-first equals "{{b_title}}"` | The text of an element of a web page, with equals, does not equal, contains, exists, and has N items |

A line that is none of these is an error that names the file and the line, and lists the forms. A tag, a comment, or a step in another place is an error too, so that a typo cannot change a test silently. A comment elsewhere is a note and is ignored.

**3. Write its script** in any file, for example `scripts/SHOP-482-01.yaml`. It holds the exact request of each step:

```yaml
apiVersion: semar/v1
kind: Script
scenario: SHOP-482-01
authoredBy: { instance: hand, model: none, harness: none }
steps:
  - step: create-order
    request: { service: orders, method: POST, path: /v1/orders, json: { amount: 100000 } }
    captures: [{ name: order_id, jsonpath: "$.data.id" }]
  - step: refund-too-much
    request: { service: orders, method: POST, path: "/v1/orders/{{order_id}}/refunds", json: { amount: 150000 } }
```

A capture takes a value from a response, and `{{order_id}}` uses it in a later step.

**4. Store the script, run, and confirm:**

```bash
semar script add scripts/SHOP-482-01.yaml     # checks the script against the scenario
export ORDERS_UAT_TOKEN='<the token of a test account>'
semar run --scenario SHOP-482-01 --build 9f3c2ab41d07
semar review SHOP-482-01                      # needed because one result has the source "diff"
semar run --scenario SHOP-482-01 --build 9f3c2ab41d07; echo "exit $?"
```

The folder `testdata/e2e/` of the Semar repository holds these files as a working example. The scenario there was converted from the older YAML format, so its file also has the tag `@intent-newline`: it keeps the intent exactly as it was, so that the confirmation of the YAML file still holds. Its configuration points at an address where nothing listens, so a run there ends as `blocked`. The tests in `internal/cli` run the same files against a sample service.

### Act as another account

A step uses the auth profile of the scenario. Starting a step with `as <account>, ` makes it, a setup step too, act as another auth profile of `semar.yaml`. Every request of that step then carries that profile's session, and no other:

```gherkin
    When as carol, A member of another board moves the card
    # step: move-to-other-board; POST /v1/cards/{id}/move
    Then the status is 403
    # ticket docs/tickets/SHOP-482.md:14 only a member of the board may move a card
```

A name that `semar.yaml` does not define blocks the scenario before any request, with `account carol is not in the configuration`. A step of a fixture has `as` too, as [a fixture](#prepare-data-with-fixtures) explains. A journey leg and a journey step have `as` too, as [a journey](#test-a-business-flow-with-a-journey) explains.

### Expected results from a model, and variables in them

`, by model` at the end of the comment under a `Then` line says that a model wrote the expected result. It always needs a person's confirmation, whatever its source and whatever `policy.trust.confirm` lists. `semar review` opens an item with the ticket and the cited lines of the results that need a person. For a result whose cite starts with `<file of the repository>:<line>` it prints that line, read from your working tree as one escaped line of at most 300 characters. A cite to a file or a line that is not there prints `line not found`, so you can see that it does not hold up. Under it comes the test: its title, and its `Given`, `When`, and `Then` lines as the scenario file has them, with `as` for each step that has it. `(by model)` ends a result that a model wrote, and `(source diff)` a result whose source is not the ticket. `d` prints the whole file as Gherkin.

A string value after `equals`, `does not equal`, or `contains` may hold `{{name}}`: a capture of this or an earlier step, an output of a `given` entry such as `{{card.board}}`, `{{run.id}}`, or a [date variable](#values-that-follow-the-run) such as `{{date.plus7d}}`. Semar fills it in before it checks. A `secret.` or `data.` name, a malformed `{{`, or a name that nothing provides is refused when the files are loaded, and a value that is missing at run time makes the check `not_evaluated`. The report shows the filled-in value next to what was written, and a value that the redaction hides stays hidden.

`has N items` compares a number of values. On a path made only of names and indexes, such as `$.data.lists[0].cards`, that selects one array, it is the length of the array, so `has 0 items` means the array is empty. On any other path, such as `$.data.cards[*]`, it is the number of values the path selects.

```gherkin
    Then $.data.board equals "{{card.board}}"
    # ticket docs/tickets/SHOP-482.md:9 the card stays on its board
```

`as` and `by model` are part of what a confirmation covers, but only when set, so a scenario that does not use them keeps its hash and its stored confirmations and scripts.

### Convert scenarios from the older YAML format

Scenario files were YAML before they were Gherkin. Semar reads `.feature` files only, and a `.yaml` or `.yml` file under `.semar/scenarios` stops every command with "scenario files are Gherkin now: run semar scenarios convert". Run it once:

```bash
semar scenarios convert        # --dir <repository root>
```

It writes a `.feature` file beside each YAML scenario file, deletes the YAML file, and prints each file with its expectation hash. The scenario is the same, so every confirmation and every stored script stays valid, and `semar run` gives the results that it gave before. It never overwrites a `.feature` file, and it writes nothing when any file cannot be converted. A file that cannot be converted says which field Gherkin cannot carry exactly, for example an intent that ends with two line breaks. Change that text in the YAML file, run `convert` again, and expect the scenario to need a new confirmation, because its hash changes with the text.

## Prepare data with fixtures

A scenario often needs data in a given state, such as a paid order, and making it can take calls to several services. A fixture is a reviewed file that does this. A scenario names the state it needs and holds no preparation steps.

**1. Write the fixture** in `.semar/fixtures/<id>.yaml`. The file name is the ID, so this one is `order.paid.yaml`:

```yaml
apiVersion: semar/v1
kind: Fixture
id: order.paid
description: A paid order of 1000 that belongs to the given customer
version: 1.0.0
owner: team-orders
params:
  customer: { default: "{{data.customers.default}}" }
find:
  - request: { service: orders, method: GET, path: /v1/orders, query: { status: paid, customer: "{{customer}}" } }
    captures: [{ name: id, jsonpath: "$.data[0].id" }]
create:
  - request: { service: orders, method: POST, path: /v1/orders, json: { amount: 1000, customer: "{{customer}}", ref: "semar-{{run.id}}" } }
    captures: [{ name: id, jsonpath: "$.data.id" }]
  - request: { service: payments, method: POST, path: /v1/payments, json: { orderId: "{{id}}" } }
  - request: { service: orders, method: GET, path: "/v1/orders/{{id}}" }
    until: { jsonpath: "$.data.status", equals: paid, timeout: 30s, every: 1s }
outputs: [id]
cleanup:
  - request: { service: orders, method: POST, path: "/v1/orders/{{id}}/cancel" }
```

What each part does:

* `find` runs first. It must only read. When it gives a record, Semar uses it and `create` does not run. A `find` that gets a 404, an empty list, or a capture with no value gives nothing.
* `create` runs only when `find` gave nothing. A step with `until` repeats a read until the value matches. It waits `every` first (2 seconds when unset), then doubles the wait up to 15 seconds. `timeout` is required and at most 15 minutes. A fixed sleep does not exist.
* `outputs` names the captures that a scenario may read, as `{{<as>.<output>}}`.
* `cleanup` runs after the last step of the scenario, also after a failure or an interrupt, in reverse order of the `given` entries. It runs only for what `create` made. A record that `find` reused is never cleaned up. A state that cannot be undone declares no `cleanup`, and the report then says that the record is kept.
* `{{run.id}}` is the ID of the run, so that two runs do not share a record. Put it in a note or a reference field of the record, where the service allows. `{{customer}}` is a parameter, and `{{data.customers.default}}` is a value of the environment. See [Configuration](#configuration) for `data`.
* A fixture holds no secret. A header such as `Authorization` is refused, and a variable that is not a parameter, a capture, `run.id`, or `data.*` is refused. Each step is sent with the session of its service: set `authProfile` on the service. A service with none uses the auth profile of the scenario.
* A step of `find`, `create`, `until`, or `cleanup` can act as another account with `as`, as a step of a scenario can: `- as: carol` before its `request`. Use it for data that belongs to another account, such as an order that carol made and the default account may not read. A step with no `as` is sent as before. An `as` that is not an auth profile of `semar.yaml` is refused with `create[0].as: carol is not an auth profile of the configuration`. The `as` is part of what you approve, so adding it to an approved fixture asks for a new approval, and `semar review`, `semar fixture list`, and `plan.md` show it, as in `create 1 as carol: POST orders /v1/orders`.
* Every step goes through the same rules as a scenario step: the host list, the methods, `effects.deny`, `effects.once`, and `effects.mode`. A step on the deny list or the once list is refused. A `find` step that writes is refused. On a `read_only` environment no `create` is sent.
* A fixture cannot use another fixture.

**2. Name it in a scenario** with a `Given the fixture` line, and read its outputs in the script:

```gherkin
Feature: Reading an order

  @id:SHOP-482-05 @ticket:SHOP-482 @surface:api
  Scenario: A paid order is read back as paid
    The owner of a paid order reads it and sees the status paid.

    Given the fixture order.paid as order
    When Read the paid order
    # step: read-paid-order; GET /v1/orders/{id}
    Then the status is 200
    # ticket SHOP-482: a paid order can be read by its owner
    And $.data.status equals "paid"
    # ticket SHOP-482: the order stays paid
```

The script step is `request: { service: orders, method: GET, path: "/v1/orders/{{order.id}}" }`. A fixture line can also give a parameter with a table of two columns under it, such as `| customer | c-2 |`, and end with `with an exclusive lease`. The folder `testdata/e2e-fixtures/` of the Semar repository holds a working example of these files. The fixture lines are part of what a person confirms, so adding or changing one lapses the confirmation of the scenario.

**3. Approve it.** A fixture counts only after a person approves it, as an expected result counts only after it is confirmed.

```bash
semar review order.paid      # prints each step, then asks
```

`--yes` skips the question, and the command takes several IDs. The approval names the version and a hash of the whole fixture. A change to the file lapses it, and you approve again. It is stored under `.semar/store/approvals/fixtures/` with your name and the words "local, unverified identity", as a confirmation is. Until a fixture is approved, a scenario that names it ends `blocked` and sends nothing.

**4. Look at the catalogue and check that it works.**

```bash
semar fixture list --env uat
semar fixture check --all --env uat
```

`list` prints one line for each fixture, then each file that is not valid with its problem. With `--env` it also shows the health on that environment and checks each fixture against the effects policy of that environment:

```text
order.paid  1.0.0  team-orders  approved  not checked on uat  used by SHOP-482-05
```

`check` runs `find`, then `create`, then `cleanup`. It runs `create` also when `find` found a record, so that `create` is checked too. This sends writes to the environment, under the effects policy, and a fixture that is not approved is not run. On a `read_only` environment a fixture with `create` steps is not fully checked. A production environment must be named with `--env`. Each check stores a health record in `.semar/store/fixture-health/<env>/<id>.json`. A record counts only for the fixture as it was when it was checked. A fixture whose last check failed blocks every scenario that uses it, with "fixture unhealthy". Semar has no scheduler: run `semar fixture check` from your own scheduler or pipeline. The exit code is 0 when every fixture is healthy, 1 when one is unhealthy, 2 when one was not fully checked, and 3 for an error.

**Where leases live.** A `lease: exclusive` on a fixture or on a `given` entry reserves one record for one run at a time on this machine. The lease is an operating system file lock under `leases/` in the Semar home: `$SEMAR_HOME`, else `$XDG_CONFIG_HOME/semar`, else `~/.config/semar`. It is never in the repository. The lock is released when the process ends, also after a crash. When `find` returns a record that another run holds, Semar treats `find` as giving nothing and goes to `create`. When the fixture has no `create`, Semar waits up to 2 minutes and then ends the scenario `blocked` with "lease not available". A lease covers one machine. Runners on several machines are not covered, and the lock on Windows has not been tried on a Windows machine.

**What the planner does with fixtures.** The planner sees the catalogue: for each fixture its ID, description, parameters, and outputs, and whether it is approved. It picks a fixture by its description and never writes a multi-service flow. When no fixture gives the state that a scenario needs, it does not draft the scenario. `plan.md` lists it under "Not drafted" and under "Fixtures needed", with a proposed fixture. A person writes the fixture, approves it, and plans again. A fixture file that is not valid stays out of the catalogue, and `semar plan` prints one line about it. Read the `Given` lines of `plan.md`: code checks that a fixture and its outputs exist, and not that the fixture gives the state that the scenario needs.

**What `blocked` means.** When a fixture cannot give its state, the scenario ends `blocked` with the reason, and no scenario step is sent. A fixture never makes a scenario `failed`, because a failed preparation says nothing about the behaviour under test.

| What the report says | What to do |
| --- | --- |
| `fixture order.paid is not approved` | Run `semar review order.paid` |
| `fixture order.paid (as order) is missing` | The scenario names a fixture that has no valid file in `.semar/fixtures` |
| `fixture unhealthy` | The last `semar fixture check` failed. Fix the fixture or the environment, and check again |
| `find gave nothing and create step 2 answered 500` | The fixture could not make the state. Fix the fixture, or look at the service |
| `until` waited and the value never matched | The condition did not hold within `timeout`. The text names the step and the number of attempts |
| `this environment is read-only` | `find` found no record and `create` may not run. Use an environment that allows writes, or make the record |
| `fixture effect unknown` | A `create` may have reached the service. Nothing was sent again, and the report lists the record as possibly left |
| `lease not available` | Another run on this machine holds the record |

Cleanup never changes an outcome. A record that cleanup could not remove is listed in the report under "Still exists". Each scenario of the report shows what it was given, for example `Given order: order.paid 1.0.0 (hash 8b38156352ff), made by create; cleanup done`.

## Test a web application

A scenario with the tag `@surface:web`, or a journey leg with `surface: web`, drives Chrome on a profile of Semar's own. A step names an element of a screen, and the screens file `.semar/screens/<app>.yaml` holds how each element is found. A person approves that file in `semar review`. Nobody has to write it: `semar record` and `semar plan` do.

When a redesign moves an element, the run says which element it did not find. `semar screens repair latest` then prints up to three candidates that the browser found by role, by name, or by text, with no model, and `--apply <screen>.<element>=<n>` rewrites that one locator. The file then needs a new approval.

`semar run --headed` shows the browser window of a web step.

### Sign in to a web application

An application that keeps its session in the tab shows the sign-in form at every start, so a recorded test would start with the typing of the sign-in, and so would each recording. Name the user and the password variable of an account once, and Semar signs in by itself wherever it meets the form. A test holds no sign-in step.

```bash
semar login shop --user ana@example.com --password-env SHOP_WEB_PASSWORD
```

```text
Wrote the web sign-in of default to .semar/semar.yaml.
Signed in as default (ana@example.com) at https://shop.uat.example.com. The profile of default in uat is saved.
```

The command adds one line to the account in `.semar/semar.yaml` and keeps every other line and comment. It then signs in once with no window, so you learn at once that the two values are right. `semar init` writes the same line: it asks, after each account, for the user that the account signs in with on the web and for the variable of its password, and it has the flags `--web-user <account>=<user>` and `--web-password-env <account>=<NAME>` (`default` names the first account). `--as <account>` on `login` names another account.

```yaml
authProfiles:
  default:
    session: {header: Authorization, value: "Bearer {{secret.TOKEN}}"}
    secrets: {TOKEN: "env:SEMAR_TOKEN"}
    web: {user: ana@example.com, password: "env:SHOP_WEB_PASSWORD"}
```

* **When it signs in.** After every `open` of a web step, once the page has settled, and when `semar record` opens its first page. If the page shows a sign-in form, Semar fills it and sends it. If there is no form, it does nothing. If the sign-in sent the tab to another page, Semar opens the address of the step again.
* **What a form is.** One visible password field, and the last visible text, email, or tel field before it, in the same form. A page with two password fields sets a password, and Semar leaves it alone. Semar sends the form with its submit button, else the first button after the password field, else Enter.
* **The password is never written.** It stays in the environment variable. It reaches no file, log, report, or screenshot text, and a stored `open` says only that Semar signed in and as which account. The machine check of `semar init` lists a password variable that is not set.
* **A sign-in that does not finish** ends the step with a line that names the account, says that the form is still there, quotes the alert that the page shows, and names `web.user` and the variable. The scenario is `blocked` or `inconclusive`, never `failed`, because the product was not judged. `semar login --user` exits with 2.
* **A test of the sign-in page itself** uses an account with no `web` block.

It does not sign in through another site, with a second factor, with a captcha, or with a form in two pages. For those, `semar login <service>` with no flag opens a window, and the session lasts in the profile.

### Record a web test

A web test took three hand-written files: the screens, a script, and a scenario. `semar record` writes all three while you do the flow once in Chrome. You write no locator.

```bash
semar login shop --env uat --user ana@example.com --password-env SHOP_WEB_PASSWORD   # once: Semar signs in by itself from then on
semar record SHOP-482-07 uat --given order.paid as o --open '/orders/{{o.id}}'
```

```text
Made the fixture order.paid.
A Chrome window is open at https://shop.uat.example.com/orders/o_4471
  Do the flow. Hold Option and click what must be true. Close the window to finish.

Recorded 4 actions and 2 checks.
Wrote .semar/screens/shop.yaml (1 screen, 5 elements)
      .semar/scenarios/SHOP-482/SHOP-482-07.feature
      the script of SHOP-482-07
Replayed once: passed.

Next: semar review SHOP-482-07
```

The window is Semar's own profile of the environment and the account, never the browser that you use. Do the flow as a user does: click, type, press Enter or Escape, drag, or type another address. Hold Option (Alt on Windows and Linux) and click what must be true. The page does not get that click. The element shows an outline for a moment, and the text that it shows becomes an expected result. Close the window to finish.

A test with no check proves nothing, so a recording with no check writes nothing and says so.

Semar writes the scenario with one line for each action and a `Then` line for each check, and you can reword the lines and the title. The step IDs in the comments tie the lines to the script.

```gherkin
  @id:SHOP-482-07 @ticket:SHOP-482 @surface:web @auth:support-agent
  Scenario: Recorded on 2026-11-17: Order
    Given the fixture order.paid as o
    And Open /orders/{{o.id}}
    # step: open
    When Click refund
    # step: click-refund
    And Type "5" in amount
    # step: fill-amount
    And Click confirm-refund
    # step: click-confirm-refund
    Then the element refund-total equals "5.00"
    # person
    And the element status equals "partly refunded"
    # person
```

* **How elements are found.** Semar picks, for each element, the way that a redesign changes least: a test ID (`data-testid`, `data-test`, or `data-qa`), the role with the name, the text, and last a short CSS path. It uses a role with a name, or a text, only when exactly one element of the page is found that way. An element that a check reads is never found by the text that the check expects. The name of an element comes from its test ID, its name, or its text.
* **A password is never read.** Semar notes that a secret was typed, and the step is written with `secret: env:<NAME>`. Name the variable with `--secret-env <NAME>`, or Semar asks for it after the window closes. The test types the value of that variable when it replays.
* **Data that repeats.** `--given <fixture>[ as <alias>]` makes the fixture before the window opens and cleans it up after. Every value that the fixture gave, and the run ID, is written as its variable wherever it is in a path, a typed text, a name, or the text of a check, so `/orders/o_4471` becomes `/orders/{{o.id}}`. A value of one or two characters is left as it is. Use `--as <account>` when the fixture needs an account.
* **Screens that exist.** The elements go into `.semar/screens/<app>.yaml`, and `--app` names another file than the service. A screen or an element that is there is kept: a new element is added below the last one of its screen, and an element with the same name and another locator gets a number. A second recording does not break the tests of the first.
* **Replay.** After it writes, `record` replays the scenario once with no window and says how it went. A scenario that does not replay is still written, with the step that failed, so that you can fix it or record again. `--no-replay` skips it.
* **Nothing is approved.** A recording made in a minute can hold a mistake, so a person reviews the screens file and the scenario. The command ends with `semar review <id>`, and `--covers <use case id>` also prints `semar check <use case id> --by <id>`. `record` prints the two commands and does not run them.
* **Safety.** `semar record` refuses a production environment and a read-only one, because it clicks and types. It refuses an ID that has a scenario unless `--replace` is set. It uses `--service` when the environment has several, `--ticket` for a ticket other than the group of the ID, and `--open <path>` for the first page.

A date or time field (`date`, `datetime-local`, `time`, `month`, `week`) is recorded as one step with its value, whether the person keys it or picks it. A date that you type stays as you typed it: write a [date variable](#values-that-follow-the-run) in the script by hand when the test must follow the day of the run. `semar record` does not record on Android, a wait of time, a check of anything but the text of an element, the clearing of such a field, a color field, a slider, a choice in a select list, hover, a file upload, or a second tab. Add them to the files by hand.

### Plan writes web tests

`semar plan` used to answer a criterion that a web page shows with a manual check, because a model that cannot see the page invents locators. `semar record` gave the page to a person. The plan now gives it to a model, and keeps everything that `record` does after the last click: the screens, the scenario, the script, and a replay.

```bash
semar plan --base main                 # writes the API tests and the web tests
semar plan --base main --no-web        # the web criteria stay manual checks, for this run
```

The first job of a criterion says whether a manual check is on `web`, on `mobile`, or on `other`. A `web` check is explored when the environment is a test environment that allows test data, Chrome is found, and the service has a web page: `GET <base URL><start>` answers `text/html`, where `start` is `services[].web.start` (a path such as `/boards`) and else `/`. A criterion that is not explored stays a manual check, and the plan says why once.

For each such criterion, Semar opens Chrome with no window on its own profile, signed in as in [Sign in to a web application](#sign-in-to-a-web-application), with the recorder of `semar record` in the page. Each turn is one job with no tools. The model gets the criterion, what was done so far, and a list of what the page shows, at most 150 lines such as `e3 button "Save"`, `e5 textbox "Title" value="Fix login"`, or `e9 text "Overdue"`. It answers in a closed shape with 1 to 5 actions on references of that list: `click`, `fill`, `press` Enter or Escape, `drag`, and `check`. Semar does each action as real mouse and key input, so the recorder hears what it hears from a person. A check is the Option-click of `semar record`. The model never writes a locator, an address, or a credential, and the text of the page is data: the prompt says so, the model has no tool, and the browser stays inside the hosts of the environment.

```text
$ semar plan --base main
...
web: 2 criteria explored, 1 test written, 1 left for a person, 22 turns, $0.12
web: left for a person: docs/tickets/SHOP-482.md:13: the card colour is a color input that cannot be typed into, so the colour of the card cannot be shown on this page.
```


* **A criterion with several parts.** A flow may end as done with the part of the criterion that the page shows, and name in `unproved` the part that its checks did not prove. The test is written for the first part, and the second stays a manual check of the same criterion, so a use case is never marked as tested for what no check proves.
* **Data of its own.** A flow makes what it needs through the page, with names that hold `{{run.id}}`, and does not act on data that was there before, so that the test runs again. The prompt says so.
* **A check comes from the ticket, not from the page.** `expect` is the text that the criterion says. When it equals what the element showed, the check is written with the source `ticket` and the cite of the criterion. When it differs, the check is written with the text of `expect`, and the plan lists it under "fails now" with what the page shows. A page with a defect gives a test that fails.
* **Data.** In its first answer a model may name fixtures of the catalogue, drafts of this plan included. Semar makes them as `record --given` does, tells the model what they gave, loads the first page again, and cleans them up. A name that the flow creates holds `{{run.id}}`, which Semar types as the ID of the run and keeps as the variable in the test.
* **When a flow ends as a manual check.** When the model says `manual`, when it records no check, after 12 turns, after 40 actions, after 3 minutes, or when the replay of the written test does not end as the recording predicts: `passed`, or `failed` when a check fails now. The same flow is then tried once on the next model of the ladder, in a new browser with new fixtures. Nothing of a flow that ends as a manual check is written.
* **What is written.** The test takes the next number of its ticket, with the tag `@surface:web`, and it covers the use case of its criterion, so the use case shows a test and not a manual check. The screens go into `.semar/screens/<app>.yaml`, merged. Nothing is approved: `semar review` shows the screens file and each scenario, with `by model` on every expected result. Until a person confirms it, a run of the test is `inconclusive`.
* **The cost.** Each turn is a job of the plan. It counts in the `model use` line, in `--max-cost`, and in the usage file with the pipeline `web`, apart from the criteria. The estimate that `semar run` shows before a plan does not count the web part, because the number of web criteria is not known before the first jobs.
* **One criterion at a time.** An account has one browser profile, and two flows in one application disturb each other.

It does not explore a mobile screen, a flow across two tabs, a file upload, a hover, a choice in a select list, or a color, slider, or file field, because the recorder does not hear them or a flow cannot type into them. A date or time field is filled with its value in the form that the field takes, and the page list shows the form. A flow types a [date variable](#values-that-follow-the-run) such as `{{local.plus1d}}` for a date near today, and a fixed date at least 10 years away when the page must show the date in its own words. The plan does not sign in through another site or with a second factor.

## Test a business flow with a journey

A journey is a business flow that crosses services, such as "an order is placed, paid, and shown as paid". A scenario tests one service. No service owns a journey: a named person, the owner, does. A journey runs one leg at a time against one set of builds, and its result is valid only for those builds.

API legs and web legs are built. A leg on Android is refused when the file loads.

**1. Write the journey** in `.semar/journeys/<id>.yaml`. The file name is the ID.

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

* `owner` and `criticality` (`critical` or `standard`) are required.
* A leg names its `service`. Its steps are scenario steps with a `request` and `captures` written in the file. `needs` lists the legs that must pass first. A later leg reads an output of a leg that it needs, directly or through other legs, as `{{<leg>.<output>}}`. Any other reference is refused when the file loads. A leg ID has no hyphen.
* `wait` repeats the last step of the leg, which must be a `GET` or `HEAD`, until the condition holds or `timeout` ends (at most 15 minutes). When the time ends, the leg is `failed`.
* An invariant checks what only a journey can see: the values are the same in every leg. Two values are equal when both are decimals with the same value, or both are the same text. A value that holds a redaction marker is not compared.
* `given` on the journey names fixtures that the whole flow shares. A leg has no `given` of its own. A leg uses the session of its service (`authProfile` on the service), else the `authProfile` of the journey.
* `as: <auth profile>` on a leg makes every step of the leg act as that account, such as a member who is not the owner. `as` on a step wins over the leg's. Both name an auth profile of `semar.yaml`. A name that it does not define blocks the whole journey before any request, with `account bob is not in the configuration`. `semar journey run` loads the session of every account that the journey uses. `semar review <id>` shows `as` for each leg and step, the report shows who each leg acted as, and the hash of a confirmation covers `as` only when it is set, so a journey without `as` keeps its confirmations.

```yaml
  - id: member
    surface: api
    service: orders
    as: bob                       # the web leg is Alice's, this leg is Bob's
    needs: [place]
    steps:
      - id: read
        action: Read the order as a member
        hint: { method: GET, path: "/v1/orders/{id}" }
        request: { method: GET, path: "/v1/orders/{{place.id}}" }
        expect: [{ check: { status: 403 }, source: person }]
```

* Every request goes through the effects policy, as a scenario step does. A journey file has at most 12 legs, 20 steps per leg, and 8 invariants. The folder `testdata/e2e-journeys/` of the Semar repository holds this file as a working example.

**2. Confirm the expected results.** A leg's expected results are confirmed as a scenario's are. A journey whose expected results nobody confirmed is `blocked`, and nothing is sent for it.

```bash
semar review checkout        # repeatable. With --env <name>, the environment whose auth profiles the legs use
```

An expected result with the source `person` needs no confirmation.

**3. List and run.**

```bash
semar journey list --env uat
semar journey run checkout --env uat
semar journey run --critical --env uat
```

`journey list` prints one line for each journey, with its criticality, owner, legs, and last stored result:

```text
checkout  critical  team-commerce  3 legs  no result yet
```

With `--env` it reads the builds that the environment runs now and shows a stored result as `stale` when any of them differs from the builds of that result. `journey run` takes journey IDs, `--critical` for every journey with criticality `critical`, or `--all`. It also takes `--env` and `--build`, as `semar run` does, and a production environment must be named. A journey is not run by `semar run`. Use `--critical` as a release gate in your own pipeline: Semar has no scheduler.

A run writes a report at `.semar/runs/<run id>/report.md` with a section for each journey, and prints a summary and the path of the report. The exit code is 0 when every journey passed, 1 when one failed, and 2 otherwise, also when a journey file was skipped because it is not valid.

**The manifest.** Before the first leg, Semar reads the build of every service that the journey calls, through the build probes of `semar.yaml` or `--build <service>=<commit>`, and hashes them. That is the manifest. It reads the builds again after the last leg. A result is valid only for its manifest. When a build differs at the end, or later, the result is `stale`.

**Outcomes.** A journey has five outcomes. It has no `inconclusive`: a leg that ends `inconclusive` makes the journey `blocked`.

| Outcome | Meaning |
| --- | --- |
| `passed` | Every leg passed, every invariant held, and the manifest was the same at the start and at the end |
| `failed` | The first leg that did not pass ended `failed`, or an invariant is false. The report names the first failing leg, and the legs after it are `not_run` |
| `blocked` | The manifest could not be read, a fixture of the journey failed, a leg could not start or ended `blocked` or `inconclusive`, an expected result is not confirmed, or an invariant could not be evaluated |
| `stale` | A build changed during the journey |
| `not_run` | The journey was not selected |

`semar plan` also lists the journeys to rerun. For each journey that has a leg step with the same method and route as a step that the plan drafted, `plan.md` has a line under "Journeys to rerun" with the command to run it.

## See that nothing is skipped: the use case ledger

A plan can leave a use case untested without anyone noticing. The ledger makes each use case end in one stated state. The state is computed by code from the outcomes of the scenarios and journeys that cover it. It does not change any of those outcomes.

**Where the use cases come from.** A use case is an obligation, in a file under `.semar/obligations/`.

* `semar plan` writes `.semar/obligations/<ticket>.plan.yaml` for each ticket. It holds one obligation for each drafted scenario, one for each case that was not drafted, one for each file that no scenario covers, and one for each difference between a document and the code that the planner reported. A later plan of the same ticket adds entries and never removes or changes one. In the criteria pipeline an entry also holds the `criterion` that it belongs to: see [Plan the same ticket again](#plan-the-same-ticket-again). Delete an entry that no longer applies in a reviewed commit.
* You write the others by hand in any other file of the folder, for example manual test cases that you copy from a spreadsheet:

```yaml
apiVersion: semar/v1
kind: Obligations
obligations:
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

An obligation has an ID like `<group>/<name>`, a title, and a `source`: `criterion`, `diff`, `knowledge`, `testcase`, `feature_map`, or `defect`. It then has one of these: `covers`, a list of scenario IDs; `journeys`, a list of journey IDs; a `gap`, a declared reason with no cover; or a `manual` reason with steps and an expected result. A `cite` is required for `criterion`, `testcase`, and `defect`. Semar shows it and does not check it. A key `state` is refused, because only code gives a state. With `ticket` at the top of the file, every ID starts with the ticket and a slash. A file with a problem stops `semar status` with exit 3, and the problems are listed.

**The states.** The first row that matches gives the state.

| State | When |
| --- | --- |
| `failed` | A covering scenario or journey failed. This shows even under a deferral or a manual reason |
| `deferred` | A person deferred it, and the deferral has not expired |
| `needs_a_person` | It has a `manual` reason |
| `verified` | At least one scenario or journey covers it, and every one passed on a current build, with the scenario unchanged since the run |
| `blocked` | Anything else. The reasons name each covering item that is not `passed`, or say that nothing covers it, or give the `gap` |

An `inconclusive`, `stale`, or `not_run` cover gives `blocked`, and the reason names the missing piece, such as a confirmation or a run on the current build. A pass counts only while the scenario file still has the expected results that the run judged. After you edit them, the use case is `blocked` with "rerun". An obligation with no cover, no `gap`, and no `manual` reason is a defect of the plan. It is listed first, as "use case with no test and no reason".

**Show the ledger.**

```bash
semar status uat
```

`semar status` prints the first line, the count of use cases, each group with one line for each use case, and the verdict. A use case is its ID and its title. A group whose use cases continue with one command names it once, on the line of the group. The reasons of a use case are printed under it, except in `needs review`. Its flags are `--ticket <key>` (repeatable), `--env <name>`, `--build <commit>` with `--env`, `--run <id>`, `--all`, `--checklist`, and `--html <file>`.

```text
On feature/refund-limit against main · uat · orders at 4f2a91c
5 use cases of SHOP-482

  verified      1
  blocked       1
    SHOP-482/not-drafted-1  A refund of a paid order by another customer
      not drafted: needs fixture order.paid
  for a person  2  → semar check <id> pass
    SHOP-482/difference-1  The document and the code differ: the status of a refund above the total
    refunds/refund-email   The customer gets an email about the rejection
  deferred      1
    SHOP-482/uncovered-1  Changes in internal/refund/notify.go
      deferred by tester until 2026-12-17: the change only sends an internal email

Ship: no. 3 open.
```

Commits of the change that name no ticket are one line before the verdict: `4 commits have no ticket: 7c1d2aa "tidy the logger", 4405647 "start without the tool", and 2 more`. A commit that changes only files under `.semar/` is Semar's own setup and is not counted.

Results come from the newest complete run that holds them. Only results on the builds that the environment runs now count, and `--env` reads those builds. Without `--env`, Semar says on standard error that the builds were not read. `--run <id>` reads one run only and prints the manual checklist after the table. The exit code is 0 when every use case is `verified` or `deferred`, or needs a person and has a newest manual result that passed on the builds that run now (see below), 1 when a use case failed or a manual result failed, and 2 otherwise. With no obligation it prints `no use case: there is no obligation in .semar/obligations` and exits 2.

**Defer a use case.** Leave it untested until a day, under your name:

```bash
semar defer SHOP-482/uncovered-1 --reason "the change only sends an internal email" --until 2026-12-17
```

The day is after today and at most 180 days ahead, and the reason is one line. The use case shows as `deferred` until then, with your name and the reason. A deferral that expired is ignored, and the ledger says so. A use case that failed still shows as failed.

**Record a manual check.** Every `needs_a_person` use case has a checklist with the steps and the expected result. `semar status --checklist` prints it:

```text
Manual checks for SHOP-482 (1)
1. SHOP-482/difference-1  The document and the code differ: the status of a refund above the total
   Why a person: A person decides whether the document or the code is right
   Steps (written by code):
     1. Read docs/refunds.md:14: the refusal is 409.
     2. Read internal/refund/handler.go:41: the refusal is 422.
     3. Decide which one is right, and change the other, or the scenario.
   Expected: The document and the code agree
   Cite: docs/refunds.md:14 (checked when the plan was made)
   Record:   semar check SHOP-482/difference-1 pass|fail --note "<what you saw>"
```

Do the steps, then record what you saw:

```bash
semar check SHOP-482/difference-1 pass --note "the document is right: the code was changed"
```

Add `--env <name>` (and `--build`, as for `semar status`) to tie the result to the builds that the environment runs now:

```bash
semar check SHOP-482/difference-1 pass --note "the document is right" --env uat
```

The record then stores the service and the commit of each build, and the output line shows them. `semar status uat` counts a pass toward the exit code only when its record names builds and every service it names runs the same commit now. So the code can be 0 for a ticket whose criteria only a person can check, and it goes back to 2 after a new build, until the check is run and recorded again. A pass recorded without `--env`, or on other builds, keeps the code at 2 at best, and its line says why. A failed result gives 1. The summary line keeps `Checked by a person: 13 passed` and adds `(9 on the builds that run now)` when fewer count.

A manual result is counted apart and shown as "checked by a person". It never makes a use case `verified`. It is only for a use case that needs a person. A record is stored in `.semar/store/obligations/` with your name and "local, unverified identity", and a new record never replaces an old one.

**In the report.** The report of `semar run` opens with one line, and `semar run` prints it after the counts. It counts every use case whose ticket or `covers` touch a scenario of the run, on the builds of that run:

```text
4 use cases: 1 verified, 2 blocked, 1 needs a person
```

The next line names `semar status --ticket <key>`. A run that did not finish prints no line.

Semar does not read a feature map or past defects into obligations. Copy such use cases into a hand-written file.

## Results

`semar run` prints one line per scenario, a line with the count of each outcome, and the path of the report:

```text
SHOP-482-01  failed  refund-too-much: expected 422, got 201
failed 1, blocked 0, inconclusive 0, stale 0, not_run 0, passed 0
report: .semar/runs/r-20261115T032840Z-d50b/report.md
```

| Outcome | Meaning |
| --- | --- |
| `passed` | Every expected result held, each one is confirmed where it needs to be, and the evidence is recorded |
| `failed` | An expected result did not hold |
| `blocked` | The scenario could not start or continue: no script, a setup step failed, a fixture could not give its state, or the environment refused a request |
| `inconclusive` | The run gives no verdict: a result is not confirmed, no build was recorded, or a write timed out |
| `stale` | The build of the service changed during the run |
| `not_run` | The run stopped before the scenario started |

The exit code of `semar run`:

| Code | Meaning |
| --- | --- |
| 0 | Every scenario passed |
| 1 | At least one scenario failed |
| 2 | Nothing failed, but a scenario could not be judged, or no scenario ran |
| 3 | An error: bad configuration, a bad file, or a run that stopped |

### Send results to your tools

`semar export` writes one stored run in a common format, from the evidence that Semar stored. It sends nothing, and it computes no outcome.

```bash
semar export ctrf r-20261115T032840Z-d50b > results.json
semar export junit r-20261115T032840Z-d50b --out junit.xml
semar export sarif r-20261115T032840Z-d50b --out semar.sarif
```

The run ID is in the path on the `report:` line of `semar run`. The report goes to standard output, or with `--out <file>` to a new file. An existing file is refused unless you pass `--force`. The exit code is 0 when the report was written and 3 when it was not. It never shows the outcomes, because an export is not a gate: use the exit code of `semar run` for that.

| Format | What it holds |
| --- | --- |
| `ctrf` | A JSON report with one test for each scenario: its name (the scenario ID and title), status, duration, the first reason as a message, every reason as a trace, and the ticket as the suite. It also holds the counts, the start and stop times, the environment, and the builds |
| `junit` | JUnit XML with one test suite for each ticket and one test case for each scenario. A `failed` scenario is a `failure`, and a `blocked`, `inconclusive`, or `stale` one is an `error`, so that no pipeline shows it as green. The run, the environment, and the builds are properties |
| `sarif` | SARIF 2.1.0 with the confirmed defects of the run only: one result for each false check on a confirmed expected result, one for each broken invariant, and one for each failed journey, which names its first failing leg. A scenario result points at the scenario file and, when known, the line of the step. A `blocked` or `inconclusive` scenario is not a finding about the product, so it gives no result |

Every export also carries the Semar outcome of each scenario beside the status of the format, so that none of the six outcomes is lost. `blocked`, `inconclusive`, and `stale` are never written as a pass. In `ctrf`, `not_run` is `skipped`, and `blocked`, `inconclusive`, and `stale` are `other`. In `junit`, `not_run` is `skipped`.

The run of a journey has one test for each leg, and one more test named `journey <id>` in the suite `semar journeys` of `ctrf` and `junit`. A journey can fail on an invariant while every leg passed, so read the journey test as well. Its `blocked` and `stale` outcomes are `other` in `ctrf` and `error` in `junit`.

An export is refused for a run that did not finish, for a run whose stored evidence changed after it was written (the message names the file), and for a run with no scenario result, such as a trial run or a fixture check. The evidence was redacted when it was stored, and an export adds no request and no header.

**The versions of the formats are not verified.** Semar writes the fields that it knows of each format and checks them against a copy of that shape in its own tests. It has not run the output through the official schema of any of them, or through the reader of a CI system. `ctrf` is written without a `reportFormat` or a `specVersion` key, because Semar does not know which values the current schema wants. `junit` is the common dialect, with no schema claimed. `sarif` states version 2.1.0 and has no `$schema` key. Try the output in your own tool before you rely on it.

## Command reference

`semar --help` lists the commands, and `semar <command> --help` prints the text of this section. `semar completion <shell>` writes a completion script for your shell.

Each command that reads the repository also takes `--dir <path>`, the root of the repository that holds `.semar/`. The default is the current directory. A command exits with 3 on an error, such as a bad configuration or a bad file. The other exit codes are given with each command.

| Command | What it does |
| --- | --- |
| [`semar init`](#semar-init) | Writes `.semar/semar.yaml` for one environment, or checks this machine against it |
| [`semar plan`](#semar-plan) | Drafts scenarios and scripts for the change of this branch, or of `--diff` |
| [`semar run`](#semar-run) | Runs the tests of the change against an environment, and says where the change stands |
| [`semar review`](#semar-review) | Shows everything that needs a person, and approves it one item at a time |
| [`semar status`](#semar-status) | Shows what is verified, what fails, and what waits for a person |
| [`semar check`](#semar-check) | Records what a person checked by hand, or says that a scenario does the check |
| [`semar defer`](#semar-defer) | Leaves a use case untested until a day, under your name |
| [`semar record`](#semar-record) | Records a web test while you do the flow once in Chrome |
| [`semar log`](#semar-log) | Lists the stored runs, newest first |
| [`semar show`](#semar-show) | Prints the report of a run, or what it did |
| [`semar rm`](#semar-rm) | Retires a scenario, and keeps a record on the use cases that it covered |
| [`semar changes`](#semar-changes) | Prints the tickets, commits, and files between two branches, with no model |
| [`semar login`](#semar-login) | Signs in to the web application, in a Chrome window or by itself |
| [`semar fixture list`](#semar-fixture-list) | Lists the fixtures with their approval, their health, and the scenarios that use them |
| [`semar fixture check`](#semar-fixture-check) | Runs the fixtures on an environment and records their health |
| [`semar journey list`](#semar-journey-list) | Lists the journeys with their last result |
| [`semar journey run`](#semar-journey-run) | Runs journeys against an environment, leg by leg |
| [`semar screens repair`](#semar-screens-repair) | Proposes a new locator for each element that a run did not find |
| [`semar scenarios convert`](#semar-scenarios-convert) | Converts older YAML scenario files to Gherkin |
| [`semar script add`](#semar-script-add) | Checks a script against its scenario and stores it |
| [`semar export`](#semar-export) | Writes one stored run as CTRF, JUnit XML, or SARIF |
| [`semar providers`](#semar-providers) | Lists the model providers and the one that would plan here |
| [`semar dashboard`](#semar-dashboard) | Opens a page on your machine to manage the model providers and see usage |
| [`semar version`](#semar-version) | Prints the version |

### Every day

#### `semar init`

On a terminal, init asks for what it cannot find and no flag gave: the address of the service, the variable that holds the token, and who each account is.

Every question has a flag. With no terminal, init asks nothing and names the flags that are missing.

```text
semar init
```

| Flag | Meaning |
| --- | --- |
| `--account <text>` | Another account as \<name>=\<ENV>: an auth profile with the same session header and the token in the environment variable ENV; repeatable |
| `--auth-header <text>` | The header that carries the session (default "Authorization") |
| `--auth-value <text>` | The value of that header; it must hold {{secret.TOKEN}} (default "Bearer {{secret.TOKEN}}") |
| `--base-url <text>` | The address of the service in the environment, such as https://orders.uat.example.com |
| `--build-commit <text>` | The JSONPath of the commit in the answer of --build-url (default $.commit) |
| `--build-url <text>` | An address on the host of --base-url that answers with the commit the service runs, such as https://orders.uat.example.com/version |
| `--describe <text>` | Who an account is, as \<account>=\<text>, one line; the planner chooses the account of a step from it; default names the default account; repeatable |
| `--env <text>` | The name of the environment (default uat, or production with --kind production) |
| `--kind <text>` | The kind of the environment, test or production; production is read only and allows GET and HEAD (default "test") |
| `--service <text>` | The name of the service (default: the name of the repository directory) |
| `--token-env <text>` | The environment variable that holds the token (default "SEMAR_TOKEN") |
| `--web-password-env <text>` | The environment variable that holds the web password of an account, as \<account>=\<ENV>; needs --web-user; repeatable |
| `--web-user <text>` | The user that an account signs in with on the web, as \<account>=\<user>; default names the default account; needs --web-password-env; repeatable |

#### `semar plan`

Draft scenarios and scripts for the change of this branch, or of --diff

```text
semar plan [--diff <link or range>]
```

| Flag | Meaning |
| --- | --- |
| `--account <text>` | Another account as \<name>=\<ENV>: an auth profile with the same session header and the token in the environment variable ENV; repeatable |
| `--auth-header <text>` | The header that carries the session (default "Authorization") |
| `--auth-profile <text>` | The auth profile of the scenarios; needed when there are several and none is named default |
| `--auth-value <text>` | The value of that header; it must hold {{secret.TOKEN}} (default "Bearer {{secret.TOKEN}}") |
| `--base <text>` | With no --diff, the branch that the change of this branch is measured against (default: change.base, else the default branch of origin, else main) |
| `--base-url <text>` | The address of the service in the environment, such as https://orders.uat.example.com |
| `--build-commit <text>` | The JSONPath of the commit in the answer of --build-url (default $.commit) |
| `--build-url <text>` | An address on the host of --base-url that answers with the commit the service runs, such as https://orders.uat.example.com/version |
| `--describe <text>` | Who an account is, as \<account>=\<text>, one line; the planner chooses the account of a step from it; default names the default account; repeatable |
| `--diff <text>` | A compare link of the code host, or a range such as main..release |
| `--env <text>` | The environment of semar.yaml to plan for; needed when there are several. With --base-url, the name of the new environment (default uat, or production with --kind production) |
| `--kind <text>` | The kind of the environment, test or production; production is read only and allows GET and HEAD (default "test") |
| `--ladder <text>` | With --pipeline criteria, the models from the cheapest up, each as \<provider id>:\<model>, such as claude:haiku,claude:sonnet,claude:opus (default: plan.ladder of your providers file, else the provider and model of the plan) |
| `--max-cost <n>` | The most this plan may spend, in dollars, as the providers report it: when the sum reaches it, no new job starts, the jobs that run finish, and the plan is written with what was answered, with exit code 2 (default: plan.maxCost of your providers file, else no limit) |
| `--max-scenarios <n>` | The most scenarios to draft (default: plan.maxScenarios) |
| `--model <text>` | The model of the planner (default: the provider's model) |
| `--no-fetch` | Use the branches as they are in this clone, without fetching origin |
| `--no-trial` | Do not send the read-only steps of the draft to the environment before the plan is written, for this run |
| `--no-web` | Do not explore the web criteria in a browser: each stays a manual check, for this run |
| `--pipeline <text>` | How the plan is made: criteria, one job with no tools for each acceptance criterion, which suits small models; or single, one job that reads the repository (default: criteria when a ticket of the change has acceptance criteria, else single) |
| `--provider <text>` | The provider that plans (default: see semar providers) |
| `--reasoning <text>` | The reasoning level of the planner, such as high (default: the provider's reasoning, else its own default) |
| `--service <text>` | The name of the service (default: the name of the repository directory) |
| `--token-env <text>` | The environment variable that holds the token (default "SEMAR_TOKEN") |
| `--web-password-env <text>` | The environment variable that holds the web password of an account, as \<account>=\<ENV>; needs --web-user; repeatable |
| `--web-user <text>` | The user that an account signs in with on the web, as \<account>=\<user>; default names the default account; needs --web-password-env; repeatable |
| `--with-setup` | Plan with your MCP servers and skills, which add tokens to every turn (default: plan.withSetup of your providers file, else off) |

#### `semar run`

Run every scenario whose ticket is a ticket of the change, then every journey with criticality critical, and end with the status of the change.

The change is the commits from the base branch to HEAD. --all runs every scenario and every critical journey, and --scenario \<id> runs the scenarios named and no journey.

A ticket of the change that has a ticket file and no use cases is not planned. On a terminal, run offers to plan it and asks first. With no terminal, or with --no-plan, it never plans, and its exit code is at least 2.

Exit 0 when everything that ran passed, 1 when something failed, and 2 otherwise.

```text
semar run [environment]
```

| Flag | Meaning |
| --- | --- |
| `--all` | Run every scenario and every critical journey, not only those of the change |
| `--base <text>` | The branch that the change is measured against (default: change.base, else the default branch of origin, else main) |
| `--build <text>` | The commit that a service with no build probe runs, as \<commit> or \<service>=\<commit>; repeat for more |
| `--env <text>` | The environment of semar.yaml to run against (the same as the argument); needed when there are several, and always for a production environment |
| `--headed` | Show the browser window of a web step |
| `--no-journeys` | Leave the critical journeys out |
| `--no-plan` | Never offer to plan a ticket that has no use cases |
| `--scenario <text>` | Run the scenario with this ID, and no journey; repeat for more |

#### `semar review`

Show what needs a person, one item at a time: the fixtures that are not approved, the screens files that are not approved, the scenarios that need a confirmation, grouped by use case, and the journeys.

Answer y to approve an item, n to skip it, d for its details, and q to stop. An ID narrows the list: a scenario, a ticket, a fixture, a journey, or the app of a screens file.

--yes approves every item with no question. With no terminal and no --yes, the items are listed and nothing is approved.

Exit 0 when nothing is left to review, and 2 when an item was skipped or the review stopped.

```text
semar review [id...]
```

| Flag | Meaning |
| --- | --- |
| `--env <text>` | The environment whose auth profiles the legs of a journey use; needed when there are several |
| `--yes` | Approve every item without asking |

#### `semar status`

Show each use case of the change in one group: verified, failing, blocked, needs review, stale, for a person, no test, or deferred.

The change is the commits from the base branch to HEAD, and its tickets are the ones that those commits name. --all shows every use case.

Exit 0 when every use case is verified or deferred, 1 when one failed or a person recorded a failure, and 2 otherwise.

With no environment, and several in semar.yaml, the builds are not read, so a pass on any build counts: the exit code is then 2 at best.

```text
semar status [environment]
```

| Flag | Meaning |
| --- | --- |
| `--all` | Show every use case, not only those of the change |
| `--base <text>` | The branch that the change is measured against (default: change.base, else the default branch of origin, else main) |
| `--build <text>` | With an environment, the commit that a service with no build probe runs, as \<commit> or \<service>=\<commit>; repeat for more |
| `--checklist` | Print the manual checklist instead of the table |
| `--env <text>` | The environment of semar.yaml whose builds count (the same as the argument) |
| `--html <text>` | Also write the status as one HTML file that needs no network, for a person with no command line |
| `--run <text>` | Read the results of this run only, and print the manual checklist after the table |
| `--ticket <text>` | Show only the use cases of this ticket, or of this group in the ID; repeat for more |

#### `semar check`

Store the result of a manual check as a new record, under your name. A pass on the build that the environment runs now makes the use case verified, and it is shown as checked by you.

The builds come from the environment of semar.yaml: the one you name with --env, or the only one there is. A pass that names no build stays open for a person.

With --by, say that a scenario does the manual check. The scenario is added to the covers of the use case, and your name is kept as the person who said so. From then on the state of the use case comes from the scenario. Semar never links the two by itself: only a person can say that a test checks the same requirement under the same conditions.

```text
semar check <id> (pass|fail [--note <text>] | --by <scenario id>)
```

| Flag | Meaning |
| --- | --- |
| `--build <text>` | With --env, the commit that a service with no build probe runs, as \<commit> or \<service>=\<commit>; repeat for more |
| `--by <text>` | The ID of a scenario that does the manual check |
| `--env <text>` | The environment whose builds the result names (default: the only environment of semar.yaml) |
| `--note <text>` | What you saw, at most 2000 characters |

#### `semar defer`

Store a deferral. The day is after today and at most 180 days ahead, and the use case shows as deferred until it passes. A use case that failed still shows as failed.

```text
semar defer <id> --until <YYYY-MM-DD> --reason <text>
```

| Flag | Meaning |
| --- | --- |
| `--reason <text>` | Why the use case is left untested: one line of at most 300 characters |
| `--until <text>` | The last day of the deferral, as YYYY-MM-DD |

#### `semar record`

Open a Chrome window on Semar's own profile of the environment and the account, and record what you do until you close it. Semar then writes the screens file, the scenario, and the script, and replays the scenario once.

Click, type, press Enter or Escape, and drag as a user does. Hold Option and click what must be true: that is a check, it does nothing on the page, and the text that the element shows becomes the expected result. A recording with no check writes nothing.

A password field is never read: the test types the value of an environment variable, named with --secret-env.

Nothing that record writes is approved. The screens file and the scenario go through a review.

```text
semar record <scenario id> [environment] [--open <path>] [--given <fixture>[ as <alias>]]
```

| Flag | Meaning |
| --- | --- |
| `--app <text>` | The screens file to add to, .semar/screens/\<app>.yaml; the default is the name of the service |
| `--as <text>` | The account whose browser profile and session the recording uses: an auth profile of semar.yaml |
| `--covers <text>` | The use case that the scenario covers: print the command that records the link |
| `--given <text>` | Make a fixture before the window opens, as \<fixture> or \<fixture> as \<alias>; repeat for more |
| `--no-replay` | Do not replay the scenario once after it is written |
| `--open <text>` | The first page, a path below the service; it may use {{variables}} of the fixtures |
| `--replace` | Write over the scenario of this ID if there is one |
| `--secret-env <text>` | The environment variable that holds the password that you type |
| `--service <text>` | The service to record against, when the environment has several |
| `--ticket <text>` | The ticket of the scenario; the default is the group of the ID, as SHOP-482 for SHOP-482-07 |

#### `semar log`

List the stored runs, newest first: the run, the time, the environment, the builds, and how many results of each outcome it has. Read one with semar show \<run>.

```text
semar log [-n <count>]
```

| Flag | Meaning |
| --- | --- |
| `-n`, `--count <n>` | How many runs to list (default 20) |

#### `semar show`

Print the report of a run, and of the newest run when none is named. With --activity, print what the run did, in order.

```text
semar show [run]
```

| Flag | Meaning |
| --- | --- |
| `--activity` | Print what the run did, in order, and not its report |
| `--pass <text>` | With --activity, show only the actions of this pass: setup, scenario, explore, security, cleanup, or trial |
| `--scenario <text>` | With --activity, show only the actions of this scenario |
| `--writes` | With --activity, show only the actions that create, update, or delete |

#### `semar rm`

Remove the scenario file and its stored scripts, take the scenario out of the covers of every use case, and write on each of those use cases the scenario, the reason, your name, and the day.

A use case that loses its last test is shown as having no test, with the reason and who retired the test, so that removing a failing test never makes a release look ready. Stored runs stay.

```text
semar rm <scenario id> --reason <text>
```

| Flag | Meaning |
| --- | --- |
| `--reason <text>` | Why the scenario is retired: one line of at most 300 characters |

### More

#### `semar changes`

Print the tickets, commits, and files between two branches, with no model

```text
semar changes --diff <link or range>
```

| Flag | Meaning |
| --- | --- |
| `--diff <text>` | A compare link of the code host, or a range such as main..release |
| `--no-fetch` | Use the branches as they are in this clone, without fetching origin |

#### `semar login`

Open Chrome on the browser profile of an account of an environment, at the base URL of the service, and wait until the window is closed.

The profile is Semar's own, at browser/\<environment>/\<account> below the Semar home. It is never the profile that you browse with, and a later run of a web step starts on it, so the sign-in is still there.

Without --as, the account is the auth profile of the service, or default.

With --user and --password-env, login writes the web block of the account into semar.yaml, signs in once with no window, and says whether it worked. Semar then signs in by itself wherever a web step or a recording meets the sign-in form. The password stays in the environment variable. For a second factor, a captcha, or a sign-in through another site, use login with a window.

```text
semar login <service> [--env <environment>] [--as <account>] [--user <user> --password-env <NAME>]
```

| Flag | Meaning |
| --- | --- |
| `--as <text>` | The account whose profile to sign in to: an auth profile of semar.yaml |
| `--env <text>` | The environment of semar.yaml; needed when there are several, and always for a production environment |
| `--password-env <text>` | The environment variable that holds the web password of the account; needs --user |
| `--user <text>` | The user that the account signs in with on the web; needs --password-env |

#### `semar fixture list`

List the fixtures with their approval, their health, and the scenarios that use them

```text
semar fixture list
```

| Flag | Meaning |
| --- | --- |
| `--env <text>` | Show the health of each fixture on this environment, and check each fixture against its effects policy |

#### `semar fixture check`

Run find, then create, then cleanup of each fixture on an environment, and record whether it still works.

A fixture that is not approved is not run. A fixture that failed its last check blocks the scenarios that use it.

```text
semar fixture check (<id>... | --all) --env <name>
```

| Flag | Meaning |
| --- | --- |
| `--all` | Check every fixture in .semar/fixtures |
| `--auth-profile <text>` | The auth profile for a service that sets none (default: the only one, or the one named default) |
| `--build <text>` | The commit that a service with no build probe runs, as \<commit> or \<service>=\<commit>; repeat for more |
| `--env <text>` | The environment of semar.yaml to check on; needed when there are several, and always for a production environment |

#### `semar journey list`

List each journey of .semar/journeys with its criticality, its owner, its legs, and its last stored result.

With --env, read the builds that the environment runs now, and show a result as stale when any of them differs from the manifest of the result.

Exit 0, or 2 when a journey file is not valid.

```text
semar journey list
```

| Flag | Meaning |
| --- | --- |
| `--build <text>` | With --env, the commit that a service with no build probe runs, as \<commit> or \<service>=\<commit>; repeat for more |
| `--env <text>` | Read the builds that this environment of semar.yaml runs now, and mark a result stale when one differs |

#### `semar journey run`

Run each journey one leg at a time in the order of its needs, check its invariants, and read the builds again at the end.

A journey whose expected results nobody confirmed is blocked, and nothing is sent for it. Review them with semar review \<id>.

Exit 0 when every journey passed, 1 when one failed, and 2 otherwise, including a journey file that was skipped because it is not valid.

```text
semar journey run (<id>... | --critical | --all)
```

| Flag | Meaning |
| --- | --- |
| `--all` | Run every valid journey in .semar/journeys |
| `--build <text>` | The commit that a service with no build probe runs, as \<commit> or \<service>=\<commit>; repeat for more |
| `--critical` | Run every journey with criticality critical |
| `--env <text>` | The environment of semar.yaml to run against; needed when there are several, and always for a production environment |
| `--headed` | Show the browser window of a web leg |

#### `semar screens repair`

Read a stored run, and for each element that a web step did not find, print its locator beside up to three candidates that the browser found by role, by name, or by text. No model is used.

With --apply \<screen>.\<element>=\<n>, rewrite that element of the screens file with candidate n. Nothing else is touched: not a scenario, an expected result, or a step.

The file then has another hash, so a person must approve it again with semar review \<app>.

```text
semar screens repair <run id|latest> [--apply <screen>.<element>=<n>]...
```

| Flag | Meaning |
| --- | --- |
| `--apply <text>` | Rewrite \<screen>.\<element> with candidate \<n>, as Board.card=1; repeat for more |

#### `semar scenarios convert`

Scenarios were YAML files and are Gherkin .feature files now. convert writes the .feature file beside each

.yaml or .yml scenario file under .semar/scenarios and deletes the YAML file. The scenario is the same: its

expectation hash does not change, so every confirmation and every stored script stays valid. convert never

overwrites a .feature file, and it writes nothing when any file cannot be converted.

```text
semar scenarios convert
```

#### `semar script add`

Check a script against its scenario and store it

```text
semar script add <file>
```

#### `semar export`

Write one stored run in a common format. ctrf and junit list every scenario, and sarif lists the

confirmed defects of the run. An export reads stored evidence, computes no outcome, and refuses a

run that is interrupted or whose evidence changed. The exit code is 0 when the report was written

and 3 when it was not; it does not reflect the outcomes.

```text
semar export (ctrf | junit | sarif) <run id>
```

| Flag | Meaning |
| --- | --- |
| `--force` | Replace the file named by --out when it exists |
| `--out <text>` | Write the report to this new file instead of standard output |

#### `semar providers`

List the model providers and the one that would plan here

```text
semar providers
```

#### `semar dashboard`

Open a page on this computer to manage the model providers and see usage

```text
semar dashboard [--port n] [--no-open]
```

| Flag | Meaning |
| --- | --- |
| `--no-open` | Print the address without opening a browser |
| `--port <n>` | The port on 127.0.0.1 (default: any free port) |

#### `semar version`

Print the semar version

```text
semar version
```

The commands that were `semar confirm`, `semar ledger`, and `semar activity` are gone, with no alias. `semar review` does what `confirm` did. `semar status`, `semar check`, and `semar defer` do what `ledger`, `ledger record`, and `ledger defer` did. `semar show --activity` does what `activity` did. The old names answer with one line that names the new command, and exit 3.

## The files of a repository

The layout of `.semar/`:

```text
.semar/
  semar.yaml            environments, services, auth profiles, redaction, policy
  scenarios/            one Gherkin .feature file per scenario, for example SHOP-482/SHOP-482-01.feature
  fixtures/             one YAML file per fixture, for example order.paid.yaml
  journeys/             one YAML file per journey, for example checkout.yaml
  obligations/          the use cases: <ticket>.plan.yaml from semar plan, and the files that you write
  plans/<head commit>/  plan.md and the accepted draft of one planned change
  runs/<run id>/        run.json, report.md, activity.jsonl, and the recorded exchanges of one run
  store/                accepted scripts and confirmations, fixture approvals and health, deferrals, and manual results
```

Commit `semar.yaml`, `scenarios/`, `fixtures/`, `journeys/`, and `obligations/` so that the team shares them. `runs/` holds recorded requests and responses of your environment, with secrets redacted. A trial run, a fixture check, and a journey run each make a run directory of their own. Decide with your team whether `runs/` belongs in the repository. `store/` holds records that a person made with a local, unverified identity, so review what a committed `store/` adds.

## When something goes wrong

| What you see | What to do |
| --- | --- |
| `inconclusive` with "no build was recorded" | Pass `--build <commit>` to `semar run`, or add a build probe with `--build-url` |
| `needs review` (stored as `inconclusive` with "an expected result is not confirmed") | Run `semar review`, then run again |
| `blocked` with "no script was accepted" | The scenario has no stored script. Run `semar script add <file>`, or `semar plan` again |
| `blocked` with "the environment does not allow a step" | The request uses a host or a method that `semar.yaml` does not list, or `effects.mode` is `read_only` |
| Exit 3 that names an environment variable | Export the token variable that `semar.yaml` names |
| `semar plan` says that the binary `claude` was not found on `PATH` | Install Claude Code, or set `SEMAR_CLAUDE_BIN` to the absolute path of its binary |
| `semar plan` says that the link names another repository | Run the command inside a clone of the repository in the link |
| A fetch fails | Check that `git fetch origin` works in this clone, or pass `--no-fetch` to use the branches as they are |
| `semar plan` prints `trial run skipped` | The trial could not start. The line says why, for example that no account can be prepared because the token variable is not set. Export it, or pass `--no-trial` to turn the trial off |
| `needs review` with "fixture ... is not approved" (stored as `blocked`) | Run `semar review <id>`. [What `blocked` means](#prepare-data-with-fixtures) lists the other fixture reasons |
| A journey is `blocked` and nothing was sent | Its expected results are not confirmed, or a build could not be read. Run `semar review <id>`, or pass `--build <service>=<commit>` for a service with no probe |
| `semar status` says that the builds were not read | Pass `--env <name>`, so that only results on the builds that the environment runs now count |
| `semar export` is refused | The run did not finish, its stored evidence changed, or it has no scenario result. The message says which |
