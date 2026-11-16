# Semar

Semar is an open-source tool in which an AI agent tests a service the way a QA engineer does: it calls the API, uses the web app, uses the Android app, and reads logs and traces. Everything the agent does is recorded as evidence. Code, not the model, computes each outcome. A recorded script replays with no model.

It helps QA engineers, developers, and product managers run integration tests in a UAT or a production environment, under rules that the team configures.

## What Semar is for

Semar has two aims.

1. **Depend less on QA for manual testing.** A change should not wait in a queue for a person to test it by hand. With Semar, an AI agent plans the tests from the change, runs them against the environment, and records the evidence. Semar does not replace the QA engineer. It moves the QA engineer's work to automation: the scenarios, the rules, and the tools that let an agent test any change.
2. **Give AI-agent testing one standard.** An agent that tests on its own is hard to trust and hard to compare: each run tests something different and reports it differently. Semar makes every test follow the same path, whoever starts it and whichever model runs it.

| What is standard | How |
| --- | --- |
| The test | A scenario file with steps and expected results, in one format |
| The source of each expected result | A label on every expected result: the ticket, a test case, a person, the diff, or a baseline |
| The evidence | Every request and response, recorded by Semar and not by the agent |
| The verdict | Computed by code from the evidence, with the same rules and the same six outcomes everywhere |
| The report | One layout, tied to the build that was tested |
| The rerun | A stored script that replays with no model, so a result can be repeated |

Semar does not remove people from the decision. A person still confirms an expected result that came only from the code, and still accepts the risk of a release.

## The problem

Teams divide their time between building, checking changes by hand, and maintaining tests. Verification may be incomplete or skipped, expected behavior may exist only in conversations, and a change waits in a queue for a manual pass. A team needs useful tests and trustworthy evidence without a new full-time maintenance job.

## What the agent needs to know

An agent that knows nothing about the product cannot choose a good scenario, add a step, or explain a result. Semar gives the agent context from four sources. It uses them to choose scenarios, to run the tests, and to understand the product.

| Source | What it gives the agent | Today |
| --- | --- | --- |
| The source code | The routes, the request and response types, and what a change touched | Built. The planner reads a copy of the source |
| MCP servers | The team's own tools, such as the tracker, the logs, and the documents | Built for a harness provider, which loads your MCP servers. The planner is not yet told to read the ticket through them |
| The documents of the repository | An API document (OpenAPI or Swagger) and the team's documents under `docs/` | Built. See [Tell the planner about your documents](#tell-the-planner-about-your-documents) |
| A knowledge base and memory | Earlier tickets, decisions, test cases, and documents outside the repository, such as a tracker or a wiki | Specified, not built |

## Who it is for

| Role | What Semar gives them |
| --- | --- |
| QA engineer | Automation in place of manual passes. The QA engineer owns the scenarios, the rules, and the configuration, and reviews what the agent drafted |
| Developer | A change tested from its diff, with evidence, without waiting for a manual pass |
| Product manager | A record of what was tested for each ticket, and the questions about intent that only a person can answer |

Semar keeps every run with its evidence. A view of the testing history per ticket, team, and scenario, with metrics, is specified and not built yet.

## How a result can be trusted

1. **The agent tests.** It drafts scenarios from a change, works through them, explores around the change, and leaves a script behind.
2. **The recorder stores the evidence.** Every request, response, and action is written down, with secrets redacted. The agent does not record its own work.
3. **Code computes the outcome.** Each scenario ends as `passed`, `failed`, `blocked`, `inconclusive`, `not_run`, or `stale`. A pass needs a confirmed expected result and recorded evidence.
4. **Replay needs no model.** A re-test or a regression run replays the stored scripts and costs no model call.

If the expected behavior is written nowhere outside the code, no tool can detect a misunderstood requirement. Semar marks such results as needing a person to confirm them.

## Status

These parts are implemented in Go.

* **The model-free core:** replay of accepted scripts against an HTTP API, an effects policy that limits what a test may change, redaction, an evidence store, the verdict, and the report.
* **A test plan from a diff:** `semar plan --diff` reads the difference between two branches, finds the tickets in the commits, and has a model draft scenarios and requests from the source. Code validates the draft. The model has read-only tools and a copy of the source, and it never reaches the environment.
* **Documents of the repository:** `.semar/semar.yaml` can name an API document and a folder of documents. The planner is told to read them before it drafts, and code compares every drafted request with the API document.
* **Model providers and the dashboard:** `semar plan` works with Claude Code, Codex, the Anthropic API, or any server with the OpenAI chat format. `semar dashboard` manages the providers and shows usage. A harness provider can load your MCP servers and skills.
* **A trial run after a plan:** after `semar plan` has a valid draft, code sends the read-only steps once to your environment, and a wrong path or an empty capture goes back to the planner before you see the plan. A trial result never counts as a pass.
* **Fixtures:** a scenario names the data it needs, such as a paid order, and a reviewed file under `.semar/fixtures/` finds or makes it before the first step and cleans it up after the last. See [Prepare data with fixtures](#prepare-data-with-fixtures).
* **API journeys:** a business flow across services, such as an order that is placed, paid, and shown as paid, runs leg by leg against one set of builds. See [Test a business flow with a journey](#test-a-business-flow-with-a-journey).
* **The use case ledger:** each use case that needs testing ends in one stated state, so that nothing is skipped without a word. See [See that nothing is skipped: the use case ledger](#see-that-nothing-is-skipped-the-use-case-ledger).
* **Exports:** `semar export` writes a stored run as CTRF, JUnit XML, or SARIF. See [Send results to your tools](#send-results-to-your-tools).

An environment is declared as `test` or `production`, and Semar acts in it only as its rules allow.

These are specified in `docs/implementation/` and are not built yet: live recording of scripts by an agent, the tracker and risk levels, security probes, providers and agents, data stores, team mode, web and Android, engines and the MCP gateway, screens and UI legs, packs and the registry, the dashboard views of fixtures, journeys, and the ledger, the Arazzo format, the import of Postman collections and HAR files, signed evidence bundles, and the enterprise features. [docs/implementation/README.md](docs/implementation/README.md) gives the sequence of plans and what each one adds. Plans 01 to 04 are the first usable version, from a plan to a report for one API ticket.

## Build the binary

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

## Use it

There are two ways to get scenarios. Both end with the same `semar run`.

| Way | When to use it | A model is needed |
| --- | --- | --- |
| [From a change](#from-a-change) | A change is deployed to an environment, and you want tests for what changed | Yes, to draft the scenarios |
| [Hand-written](#hand-written-scenarios) | You know what to test, or you want no model at all | No |

The examples use a service named `orders` and a ticket named `SHOP-482`.

### From a change

Run these commands inside a clone of the repository of the service under test.

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

`--base-url` and `--token-env` are needed only the first time. They write `.semar/semar.yaml`, and later runs read that file. Export the token variable before this step, so that the [trial run](#the-trial-run) can sign in. See [Configuration](#configuration) for the other flags.

What `plan` does:

1. It copies the source at the head commit to a temporary directory.
2. It starts the planner in that copy. The planner can read a file, search, and list files. It cannot read a file outside the copy, and it cannot reach your environment. With a harness provider it can also use your own MCP servers and skills, unless you turn them off. See [Choose the model](#choose-the-model).
3. The model returns one draft. Code validates it. Problems go back to the model, for at most three attempts.
4. Code sends the read-only steps of a valid draft once to your environment. A request problem goes back to the model inside the same three attempts. See [The trial run](#the-trial-run).
5. It writes the files below and removes the copy.

While the planner works, a status line on standard error shows the elapsed time, the draft number, and the responses and tool calls of the planner. When standard error is not a terminal, a log gets one line every 30 seconds instead. When the planner ends, `semar` prints how long it took.

| File | Content |
| --- | --- |
| `.semar/scenarios/<ticket>/<ticket>-NN.yaml` | One scenario: its steps and expected results |
| `.semar/store/scripts/...` | The exact request of each step |
| `.semar/obligations/<ticket>.plan.yaml` | The use cases of the ticket, for the [ledger](#see-that-nothing-is-skipped-the-use-case-ledger) |
| `.semar/plans/<head commit>/plan.md` | What changed, the scenarios per ticket, what was not drafted and what it needs, the fixtures needed, the journeys to rerun, the manual checks for a person, the files that no scenario covers, the trial run, the documents that the planner was told to read, and the problems of every draft that went back to the planner |

The output ends with the commands of the next steps:

```text
SHOP-482 (behaviour): 2 scenarios
SHOP-490 (config): 1 scenario
not drafted: 1
plan: .semar/plans/9f3c2ab41d07/plan.md
tried: 3 of 5 steps, 0 problems returned, 1 for a second look
next:
  semar run --all --build 9f3c2ab41d07
  semar confirm --all
  semar run --all --build 9f3c2ab41d07
```

Read `plan.md` and the scenario files before you run them. The model read them from the code. The only requests sent so far are the read-only ones of the [trial run](#the-trial-run).

**3. Run against the environment.**

```bash
export ORDERS_UAT_TOKEN='<the token of a test account>'
semar run --all --build 9f3c2ab41d07
```

The first run shows whether the drafted requests fit the service. Every scenario ends as `inconclusive`, because nobody confirmed the expected results yet.

**4. Confirm the expected results.** A model's reading of a diff is not a requirement, so a person accepts each expected result before it counts.

```bash
semar confirm --all          # lists every expected result, then asks
```

**5. Run again for the result.**

```bash
semar run --all --build 9f3c2ab41d07; echo "exit $?"
semar activity latest        # every request that was sent, in order
```

**What `plan` does not do yet:**

* It does not read the tracker. A ticket is its key and the commits that name it, and the file `docs/tickets/<key>.md` when the repository has one: see [tickets as requirements](#tickets-as-requirements).
* It does not invent data of the environment. A scenario that needs an existing record, such as the ID of an order that the test account may read, names a [fixture](#prepare-data-with-fixtures) when the repository has one that fits. When none fits, the scenario is listed in `plan.md` under "Not drafted" with what it needs, and under "Fixtures needed" with a proposal. A person writes the fixture.
* It tries only the read-only steps against the service, once. It never tries a step that writes.

**What leaves your machine.** `plan` sends parts of your source and the diff to the model provider that you chose, through your own account or key. The line that `plan` prints at its start names the provider. It sends no token, no address of your environment, and no file outside the copy. `changes`, `run`, `confirm`, and `activity` send nothing to a model. The trial run sends requests from your machine to your own environment. It sends nothing of the responses to the model except a status number and the name of a capture.

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

The section names the build that the environment ran. The change is usually not deployed yet, so that is often the build before it. Every request of the trial is stored in a run of its own, and `semar activity <run id>` lists them.

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
* turn your MCP servers and skills on or off for a harness provider;
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

**Your MCP servers and skills.** A harness provider loads them by default, with your own permission rules. Your hooks and your instruction file are not loaded for Claude Code. Nothing inside the repository under test is loaded: no settings, no MCP file, no skill.

Know three things before you rely on this:

* The planner reads a repository while it has these tools. Text in a file can ask it to call one. Your permission rules decide which tools run, and `setup.deny` names tools that never run. `plan.md` records how many tool calls were made and how many were refused.
* It costs more. Every tool and skill is described to the model on every turn. On one machine with many plugins, a plan with the setup on used ten times the input tokens of the same plan without it.
* `--no-setup` turns the setup off for one run. The planner then has the three file tools only.

```bash
semar plan --diff <link or range> --no-setup
```

An API provider never has MCP servers or skills. It runs with the three file tools.

Codex differs in three ways: it always reads your own instruction file, it does not support `setup.deny`, and it has no limit on turns. [Plan 13](docs/implementation/13-model-providers-and-dashboard.md) lists what was tested for each kind.

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
* A wiki or a tracker outside the repository is not reached through this section. A harness provider reaches it through your MCP servers.

**A repository that has a configuration already.** Add the `knowledge` section to `.semar/semar.yaml` by hand, and commit the documents: a document that is only in your working tree is reported as not in the copy. When the file lists no source and a conventional path exists, `semar plan` prints a hint:

```text
semar: the repository has docs/openapi.yaml and docs; list them under knowledge in .semar/semar.yaml so that the planner reads them first
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

**What the planner is told.** The prompt (version 6) shows each file as data, numbered by line, inside a fence that its own text cannot end. It cannot change the schema, the rules, or a result: a person still confirms every expected result. The planner is told to:

* give each criterion that a caller of the API can observe at least one expected result with `source: ticket` and a `cite` of the form `<ticket file>:<line> <one sentence>`. Code checks that the file is the ticket file of the scenario's ticket and that the line exists. A scenario whose ticket has no file cannot use `source: ticket`, and the problem goes back to the planner;
* expect what the criterion says where the code does not meet it, add a `differences` entry, and name the scenario after the requirement. `plan.md` marks that scenario with `This scenario is expected to fail on this build: the code does not meet docs/tickets/SHOP-482.md:14.` The mark is a prediction that a person can check in the run;
* put a criterion that only a person can check, on a screen, on a device, or by a gesture, under `manualChecks`: the ticket, a title, 1 to 20 steps, the expected result, and the cite of the criterion.

**Manual checks.** Each manual check is listed in `plan.md` under its ticket, and becomes an obligation `<ticket>/manual-<n>` in `.semar/obligations/<ticket>.plan.yaml`, with `source: criterion` and a `manual` reason of the kind `judgement` that is written by `ai`. It derives `needs_a_person`, and `semar ledger --checklist` prints its steps and its expected result. A person records the result with `semar ledger record`.

**Accounts.** When `semar.yaml` has two auth profiles or more, the prompt lists their names and their one-line `description`, and tells the planner to add a scenario for the most likely access mistake, with `as`. The planner may use only these names. An unknown name is a problem that goes back to the planner. A [trial run](#the-trial-run) does not send a step that acts as another account, because it has the session of the plan's account only.

**Expected results from the planner.** Every expected result that the planner drafts is written with `by: model` and the `source` that the planner gave, `diff` unless it cites a ticket. It always needs a person's confirmation.

**What `plan.md` adds.** "How this plan was made" lists the problems of every draft that went back to the planner, at most 30 for each draft, escaped. When a trial sent nothing, the summary line of `semar plan` says why, for example `tried: 0 of 27 steps (every step needs a write or a fixture create, which a trial never sends)`.

Code cannot check that a cited line says what the planner claims. `semar confirm` prints that line next to the result, so a person compares them.

### Hand-written scenarios

**1. Write the configuration,** or let `init` write it:

```bash
semar init --base-url https://orders.uat.example.com --token-env ORDERS_UAT_TOKEN
```

**2. Write a scenario** in `.semar/scenarios/SHOP-482/SHOP-482-01.yaml`. It says what must be true, and where each expected result comes from:

```yaml
apiVersion: semar/v1
kind: Scenario
id: SHOP-482-01
title: Refund above the order total is rejected
ticket: SHOP-482
intent: >
  Before: a refund larger than the order total was accepted.
  Now: the API rejects it with 422 and leaves the order unchanged.
surface: api
authProfile: default
steps:
  - id: create-order
    setup: true
    action: Create a paid order for 100000
    hint: { method: POST, path: /v1/orders }
  - id: refund-too-much
    action: Request a refund of 150000 for that order
    hint: { method: POST, path: "/v1/orders/{id}/refunds" }
    expect:
      - check: { status: 422 }
        source: ticket
        cite: "SHOP-482: a refund above the order total must be rejected"
      - check: { jsonpath: "$.error.code", equals: REFUND_EXCEEDS_TOTAL }
        source: diff
```

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
semar confirm SHOP-482-01                      # needed because one result has the source "diff"
semar run --scenario SHOP-482-01 --build 9f3c2ab41d07; echo "exit $?"
```

`testdata/e2e/` holds these files as a working example. Its configuration points at an address where nothing listens, so a run there ends as `blocked`. The tests in `internal/cli` run the same files against a sample service.

### Act as another account

A step uses the auth profile of the scenario. `as` makes one step, a setup step too, act as another auth profile of `semar.yaml`. Every request of that step then carries that profile's session, and no other:

```yaml
  - id: move-to-other-board
    action: A member of another board moves the card
    as: carol
    hint: { method: POST, path: "/v1/cards/{id}/move" }
    expect:
      - check: { status: 403 }
        source: ticket
        cite: "docs/tickets/SHOP-482.md:14 only a member of the board may move a card"
```

A name that `semar.yaml` does not define blocks the scenario before any request, with `account carol is not in the configuration`. A fixture step and a journey leg have no `as`: they use the profile of their service, and a file that sets `as` there is refused.

### Expected results from a model, and variables in them

`by: model` on an expected result says that a model wrote it. It always needs a person's confirmation, whatever its `source` and whatever `policy.trust.confirm` lists. `semar confirm` prints the cited line for a `ticket` result whose `cite` starts with `<file of the repository>:<line>`, read from your working tree as one escaped line of at most 300 characters, and `as` for each step that has it. A cite to a file or a line that is not there prints `line not found`, so you can see that it does not hold up.

A string operand of `equals`, `notEquals`, or `contains` may hold `{{name}}`: a capture of this or an earlier step, an output of a `given` entry such as `{{card.board}}`, or `{{run.id}}`. Semar fills it in before it checks. A `secret.` or `data.` name, a malformed `{{`, or a name that nothing provides is refused when the files are loaded, and a value that is missing at run time makes the check `not_evaluated`. The report shows the filled-in value next to what was written, and a value that the redaction hides stays hidden.

```yaml
      - check: { jsonpath: "$.data.board", equals: "{{card.board}}" }
        source: ticket
        cite: "docs/tickets/SHOP-482.md:9 the card stays on its board"
```

`as` and `by` are part of what a confirmation covers, but only when set, so a scenario that does not use them keeps its hash and its stored confirmations and scripts.

### Prepare data with fixtures

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
* Every step goes through the same rules as a scenario step: the host list, the methods, `effects.deny`, `effects.once`, and `effects.mode`. A step on the deny list or the once list is refused. A `find` step that writes is refused. On a `read_only` environment no `create` is sent.
* A fixture cannot use another fixture.

**2. Name it in a scenario** with `given`, and read its outputs in the script:

```yaml
apiVersion: semar/v1
kind: Scenario
id: SHOP-482-05
title: A paid order is read back as paid
ticket: SHOP-482
intent: >
  Given a paid order, its owner reads it and sees the status paid.
surface: api
authProfile: default
given:
  - fixture: order.paid
    as: order
steps:
  - id: read-paid-order
    action: Read the paid order
    hint: { method: GET, path: "/v1/orders/{id}" }
    expect:
      - check: { status: 200 }
        source: ticket
        cite: "SHOP-482: a paid order can be read by its owner"
      - check: { jsonpath: "$.data.status", equals: paid }
        source: ticket
        cite: "SHOP-482: the order stays paid"
```

The script step is `request: { service: orders, method: GET, path: "/v1/orders/{{order.id}}" }`. A `given` entry can also set `with: { customer: c-2 }` to give a parameter, and `lease: exclusive`. `testdata/e2e-fixtures/` holds these files as a working example. `given` is part of what a person confirms, so adding or changing it lapses the confirmation of the scenario.

**3. Approve it.** A fixture counts only after a person approves it, as an expected result counts only after it is confirmed.

```bash
semar confirm --fixture order.paid      # prints each step, then asks "Approve it? [y/N]"
```

`--yes` skips the question, and `--fixture` can repeat. The approval names the version and a hash of the whole fixture. A change to the file lapses it, and you approve again. It is stored under `.semar/store/approvals/fixtures/` with your name and the words "local, unverified identity", as a confirmation is. Until a fixture is approved, a scenario that names it ends `blocked` and sends nothing.

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
| `fixture order.paid is not approved` | Run `semar confirm --fixture order.paid` |
| `fixture order.paid (as order) is missing` | The scenario names a fixture that has no valid file in `.semar/fixtures` |
| `fixture unhealthy` | The last `semar fixture check` failed. Fix the fixture or the environment, and check again |
| `find gave nothing and create step 2 answered 500` | The fixture could not make the state. Fix the fixture, or look at the service |
| `until` waited and the value never matched | The condition did not hold within `timeout`. The text names the step and the number of attempts |
| `this environment is read-only` | `find` found no record and `create` may not run. Use an environment that allows writes, or make the record |
| `fixture effect unknown` | A `create` may have reached the service. Nothing was sent again, and the report lists the record as possibly left |
| `lease not available` | Another run on this machine holds the record |

Cleanup never changes an outcome. A record that cleanup could not remove is listed in the report under "Still exists". Each scenario of the report shows what it was given, for example `Given order: order.paid 1.0.0 (hash 8b38156352ff), made by create; cleanup done`.

### Test a business flow with a journey

A journey is a business flow that crosses services, such as "an order is placed, paid, and shown as paid". A scenario tests one service. No service owns a journey: a named person, the owner, does. A journey runs one leg at a time against one set of builds, and its result is valid only for those builds.

Only API legs are built. A leg on the web or on Android is refused when the file loads.

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
* Every request goes through the effects policy, as a scenario step does. A journey file has at most 12 legs, 20 steps per leg, and 8 invariants. `testdata/e2e-journeys/` holds this file as a working example.

**2. Confirm the expected results.** A leg's expected results are confirmed as a scenario's are. A journey whose expected results nobody confirmed is `blocked`, and nothing is sent for it.

```bash
semar confirm --journey checkout        # repeatable. With --env <name>, the environment whose auth profiles the legs use
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

### See that nothing is skipped: the use case ledger

A plan can leave a use case untested without anyone noticing. The ledger makes each use case end in one stated state. The state is computed by code from the outcomes of the scenarios and journeys that cover it. It does not change any of those outcomes.

**Where the use cases come from.** A use case is an obligation, in a file under `.semar/obligations/`.

* `semar plan` writes `.semar/obligations/<ticket>.plan.yaml` for each ticket. It holds one obligation for each drafted scenario, one for each case that was not drafted, one for each file that no scenario covers, and one for each difference between a document and the code that the planner reported. A later plan of the same ticket adds entries and never removes or changes one. Delete an entry that no longer applies in a reviewed commit.
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

An obligation has an ID like `<group>/<name>`, a title, and a `source`: `criterion`, `diff`, `knowledge`, `testcase`, `feature_map`, or `defect`. It then has one of these: `covers`, a list of scenario IDs; `journeys`, a list of journey IDs; a `gap`, a declared reason with no cover; or a `manual` reason with steps and an expected result. A `cite` is required for `criterion`, `testcase`, and `defect`. Semar shows it and does not check it. A key `state` is refused, because only code gives a state. With `ticket` at the top of the file, every ID starts with the ticket and a slash. A file with a problem stops `semar ledger` with exit 3, and the problems are listed.

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
semar ledger --env uat
```

`semar ledger` prints one line with the counts, then the use cases in groups, with the first reason of each. Its flags are `--ticket <key>` (repeatable), `--env <name>`, `--build <commit>` with `--env`, `--run <id>`, and `--checklist`.

```text
5 use cases: 0 verified, 2 blocked, 2 need a person, 1 deferred. Checked by a person: 1 passed.

Blocked (2)
  SHOP-482/scenario-SHOP-482-01  Refund above the order total is rejected      SHOP-482-01 was deleted: no scenario or journey covers this
  SHOP-482/not-drafted-1         A refund of a paid order by another customer  not drafted: needs fixture order.paid

Needs a person (2)
  SHOP-482/difference-1  The document and the code differ: the status of a refund above the total  A person decides whether the document or the code is right
                                                                                                   passed, checked by a person
  refunds/refund-email   The customer gets an email about the rejection                            The email leaves the test environment

Deferred (1)
  SHOP-482/uncovered-1  Changes in internal/refund/notify.go  deferred by tester until 2026-12-17: the change only sends an internal email
```

Results come from the newest complete run that holds them. Only results on the builds that the environment runs now count, and `--env` reads those builds. Without `--env`, Semar says on standard error that the builds were not read. `--run <id>` reads one run only and prints the manual checklist after the table. The exit code is 0 when every use case is `verified` or `deferred` and every manual result passed, 1 when a use case failed or a manual result failed, and 2 otherwise. With no obligation it prints `no use case: there is no obligation in .semar/obligations` and exits 2.

**Defer a use case.** Leave it untested until a day, under your name:

```bash
semar ledger defer SHOP-482/uncovered-1 --reason "the change only sends an internal email" --until 2026-12-17
```

The day is after today and at most 180 days ahead, and the reason is one line. The use case shows as `deferred` until then, with your name and the reason. A deferral that expired is ignored, and the ledger says so. A use case that failed still shows as failed.

**Record a manual check.** Every `needs_a_person` use case has a checklist with the steps and the expected result. `semar ledger --checklist` prints it:

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
   Record:   semar ledger record SHOP-482/difference-1 passed|failed --notes "<what you saw>"
```

Do the steps, then record what you saw:

```bash
semar ledger record SHOP-482/difference-1 passed --notes "the document is right: the code was changed"
```

A manual result is counted apart and shown as "checked by a person". It never makes a use case `verified`. It is only for a use case that needs a person. A record is stored in `.semar/store/obligations/` with your name and "local, unverified identity", and a new record never replaces an old one.

**In the report.** The report of `semar run` opens with one line, and `semar run` prints it after the counts. It counts every use case whose ticket or `covers` touch a scenario of the run, on the builds of that run:

```text
4 use cases: 1 verified, 2 blocked, 1 needs a person
```

The next line names `semar ledger --ticket <key>`. A run that did not finish prints no line.

Semar does not read the acceptance criteria of a ticket, the feature map, or past defects into obligations, and the planner proposes no obligation. Copy such use cases into a hand-written file.

### Configuration

`.semar/semar.yaml` describes the environment to test, a UAT or a production one. `semar init` and the first `semar plan` write it from these flags:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--base-url` | none, required | The address of the service in the environment |
| `--service` | The name of the repository directory | The name that scripts use for the service |
| `--env` | `uat`, or `production` with `--kind production` | The name of the environment |
| `--kind` | `test` | `test` or `production`. A production environment is written as read only, with the methods `GET` and `HEAD` |
| `--auth-header` | `Authorization` | The header that carries the session |
| `--auth-value` | `Bearer {{secret.TOKEN}}` | The value of that header. `{{secret.TOKEN}}` is replaced at run time |
| `--token-env` | `SEMAR_TOKEN` | The environment variable that holds the token |
| `--build-url` | none | An address of the service that answers with the commit it runs, such as `https://orders.uat.example.com/version` |
| `--build-commit` | `$.commit` | Where that answer holds the commit, as a JSONPath |
| `--account <name>=<ENV>` | none | Another account: an auth profile `<name>` with the same session header and value as `default`, and the token in the environment variable `<ENV>`. Repeat the flag for more accounts. A name that is not a valid profile name, is `default`, or is given twice stops the command with exit 3 |

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
| `environments.<env>.services[].authProfile` | The auth profile that a fixture step or a journey leg uses for this service. It must be defined under `authProfiles`. A service with none uses the auth profile of the scenario |
| `authProfiles.<name>.description` | One line, at most 200 characters, with no control character: what the account can do, such as `a member of another board, without the owner's rights`. The planner reads it to draft access checks |

The file also takes `redact`, `policy`, `replay`, `providers`, `tickets`, and `plan` blocks. The [technical requirements](docs/TRD.md) and [Plan 12](docs/implementation/12-plan-from-a-diff.md) describe them.

### Commands

Every command takes `--dir <path>`, the root of the repository that holds `.semar/`. It defaults to the current directory. `semar <command> --help` lists every flag.

| Command | What it does |
| --- | --- |
| `semar changes --diff <link or range>` | Prints the tickets, commits, and files of a change. No model |
| `semar plan --diff <link or range>` | Drafts scenarios and scripts for a change, and writes `plan.md` and the obligations. Tells the planner to read the documents that `knowledge` lists, and sends the read-only steps of the draft once to the environment. Also takes `--provider <id>`, `--model <name>`, `--reasoning <level>`, `--no-setup`, `--no-trial`, `--max-scenarios <n>`, and `--no-fetch` |
| `semar dashboard` | Opens a page on your machine to manage the model providers and see usage. `--no-open` prints the address only, `--port <n>` sets the port |
| `semar providers` | Prints your providers and marks the one that would plan here. No model |
| `semar init --base-url <url>` | Writes `.semar/semar.yaml`, with a `knowledge` entry for each conventional path that it finds, and `.semar/.gitignore` when there is none. `--account <name>=<ENV>` adds an auth profile, and can repeat |
| `semar script add <file>` | Checks a script against its scenario and stores it |
| `semar run --all` | Replays every scenario in `.semar/scenarios` |
| `semar run --scenario <id>` | Replays one scenario. Repeat the flag for more |
| `semar confirm <scenario id>` | Lists the expected results that need a person, with the cited ticket line and the account of a step, and stores the confirmation. `--all` for every scenario, `--yes` to skip the question |
| `semar confirm --fixture <id>` | Shows a fixture and approves it as it is now. Repeat the flag for more. It cannot be combined with a scenario ID or `--all` |
| `semar confirm --journey <id>` | Confirms the expected results of every leg of a journey. Repeat the flag for more. `--env <name>` names the environment whose auth profiles the legs use |
| `semar fixture list` | Lists the fixtures with their approval, their health, and the scenarios that use them. `--env <name>` shows the health on that environment and checks each fixture against its effects policy |
| `semar fixture check (<id>... \| --all) --env <name>` | With neither IDs nor `--all` it says `name the fixtures to check, or use --all` and exits 3. Runs `find`, `create`, and `cleanup` of fixtures on an environment and records their health. It sends writes. Also takes `--auth-profile <name>` and `--build <commit>` or `--build <service>=<commit>` |
| `semar journey list` | Lists the journeys with their criticality, owner, legs, and last stored result. `--env <name>` marks a result `stale` when a build differs. Also takes `--build` |
| `semar journey run (<id>... \| --critical \| --all)` | Runs journeys one leg at a time and writes a report. Also takes `--env <name>` and `--build` |
| `semar ledger` | Shows the state of every use case in `.semar/obligations`. Also takes `--ticket <key>`, `--env <name>`, `--build`, `--run <id>`, and `--checklist` |
| `semar ledger defer <id> --reason <text> --until <YYYY-MM-DD>` | Leaves a use case untested until a day, under your name |
| `semar ledger record <id> passed\|failed --notes <text>` | Records what a person saw when they ran the manual checklist of a use case |
| `semar export (ctrf \| junit \| sarif) <run id>` | Writes one stored run in a common format. `--out <file>` writes a new file, and `--force` replaces an existing one |
| `semar activity <run id\|latest>` | Prints what a run did, in order. `--writes` shows only the requests that change data |
| `semar version` | Prints the version |

`semar run` also takes `--env <name>`, which is needed when `semar.yaml` defines several environments, and `--build <commit>` or `--build <service>=<commit>`.

### Results

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

The layout of `.semar/`:

```text
.semar/
  semar.yaml            environments, services, auth profiles, redaction, policy
  scenarios/            one YAML file per scenario, for example SHOP-482/SHOP-482-01.yaml
  fixtures/             one YAML file per fixture, for example order.paid.yaml
  journeys/             one YAML file per journey, for example checkout.yaml
  obligations/          the use cases: <ticket>.plan.yaml from semar plan, and the files that you write
  plans/<head commit>/  plan.md and the accepted draft of one planned change
  runs/<run id>/        run.json, report.md, activity.jsonl, and the recorded exchanges of one run
  store/                accepted scripts and confirmations, fixture approvals and health, deferrals, and manual results
```

Commit `semar.yaml`, `scenarios/`, `fixtures/`, `journeys/`, and `obligations/` so that the team shares them. `runs/` holds recorded requests and responses of your environment, with secrets redacted. A trial run, a fixture check, and a journey run each make a run directory of their own. Decide with your team whether `runs/` belongs in the repository. `store/` holds records that a person made with a local, unverified identity, so review what a committed `store/` adds.

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

### When something goes wrong

| What you see | What to do |
| --- | --- |
| `inconclusive` with "no build was recorded" | Pass `--build <commit>` to `semar run`, or add a build probe with `--build-url` |
| `inconclusive` with "an expected result is not confirmed" | Run `semar confirm --all`, then run again |
| `blocked` with "no script was accepted" | The scenario has no stored script. Run `semar script add <file>`, or `semar plan` again |
| `blocked` with "the environment does not allow a step" | The request uses a host or a method that `semar.yaml` does not list, or `effects.mode` is `read_only` |
| Exit 3 that names an environment variable | Export the token variable that `semar.yaml` names |
| `semar plan` says that the binary `claude` was not found on `PATH` | Install Claude Code, or set `SEMAR_CLAUDE_BIN` to the absolute path of its binary |
| `semar plan` says that the link names another repository | Run the command inside a clone of the repository in the link |
| A fetch fails | Check that `git fetch origin` works in this clone, or pass `--no-fetch` to use the branches as they are |
| `semar plan` prints `trial run skipped` | The trial could not start. The line says why, for example that no account can be prepared because the token variable is not set. Export it, or pass `--no-trial` to turn the trial off |
| `blocked` with "fixture ... is not approved" | Run `semar confirm --fixture <id>`. [What `blocked` means](#prepare-data-with-fixtures) lists the other fixture reasons |
| A journey is `blocked` and nothing was sent | Its expected results are not confirmed, or a build could not be read. Run `semar confirm --journey <id>`, or pass `--build <service>=<commit>` for a service with no probe |
| `semar ledger` says that the builds were not read | Pass `--env <name>`, so that only results on the builds that the environment runs now count |
| `semar export` is refused | The run did not finish, its stored evidence changed, or it has no scenario result. The message says which |

## Documents

| File | What it holds |
| --- | --- |
| [docs/PRD.md](docs/PRD.md) | Product requirements: scope, users, decisions, requirements by milestone, risks, and open questions |
| [docs/TRD.md](docs/TRD.md) | Technical requirements: the Go design, the tool layer, the provider layer, verdict rules, configuration, commands, milestones, and spikes |
| [docs/WORKFLOW.md](docs/WORKFLOW.md) | The test workflow from intake to release, who acts in each phase, and the exit criteria |
| [docs/USER_SCENARIOS.md](docs/USER_SCENARIOS.md) | Walkthroughs from the user's side: setup, a failed scenario, pipeline mode, sign-in, and regression |
| [docs/implementation/README.md](docs/implementation/README.md) | The implementation guide: engineering rules, architecture, dependencies, and the sequence of plans |
| [Plan 16](docs/implementation/16-trial-run.md), [17](docs/implementation/17-fixtures.md), [18](docs/implementation/18-journeys.md), [19](docs/implementation/19-coverage-ledger.md), and [20](docs/implementation/20-exports.md) | The trial run, fixtures, API journeys, the use case ledger, and the exports, as built: the rules, the file formats, and the tests |

## Open questions

Whether the vendors of Claude Code and Codex CLI allow a subscription login to drive their harness from another tool is not confirmed. Semar runs the user's own unmodified harness and never touches its credentials, and API keys work from the first release. The PRD lists this under its risks.

## License

Apache-2.0 is the intended license. The repository has no `LICENSE` file yet.
