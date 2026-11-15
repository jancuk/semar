# Semar

Semar is an open-source tool in which an AI agent tests a service the way a QA engineer does: it calls the API, uses the web app, uses the Android app, and reads logs and traces. Everything the agent does is recorded as evidence. Code, not the model, computes each outcome. A recorded script replays with no model.

It is built for developer-led teams, including teams with no dedicated QA.

## What Semar is for

Semar has two aims.

1. **Depend less on QA.** A change should not wait in a queue for a QA engineer to test it by hand. With Semar, the developer who made the change gets it tested: an AI agent plans the tests from the change, runs them against the test environment, and records the evidence. A team with no QA can verify its own work, and a team with QA frees those people for the decisions that need them: what the product must do, which risks are acceptable, and what to explore next.
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

Developers divide their time between building, checking changes by hand, and maintaining tests. Verification may be incomplete or skipped, and expected behavior may exist only in conversations. Teams with QA face a verification queue. Both need useful tests and trustworthy evidence without a new full-time maintenance job.

## How a result can be trusted

1. **The agent tests.** It drafts scenarios from a change, works through them, explores around the change, and leaves a script behind.
2. **The recorder stores the evidence.** Every request, response, and action is written down, with secrets redacted. The agent does not record its own work.
3. **Code computes the outcome.** Each scenario ends as `passed`, `failed`, `blocked`, `inconclusive`, `not_run`, or `stale`. A pass needs a confirmed expected result and recorded evidence.
4. **Replay needs no model.** A re-test or a regression run replays the stored scripts and costs no model call.

If the expected behavior is written nowhere outside the code, no tool can detect a misunderstood requirement. Semar marks such results as needing a person to confirm them.

## Status

Two parts are implemented in Go.

* **The model-free core:** replay of accepted scripts against an HTTP API, an effects policy that limits what a test may change, redaction, an evidence store, the verdict, and the report.
* **A test plan from a diff:** `semar plan --diff` reads the difference between two branches, finds the tickets in the commits, and has a model draft scenarios and requests from the source. Code validates the draft. The model has read-only tools and a copy of the source, and it never reaches the test environment.

Live recording of scripts by an agent, the tracker and risk levels, security probes, providers and agents, data stores, team mode, web, and Android are specified in `docs/implementation/` and are not built yet. [docs/implementation/README.md](docs/implementation/README.md) gives the sequence of plans and what each one adds. Plans 01 to 04 are the first usable version, from a plan to a report for one API ticket.

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
| [From a change](#from-a-change) | A change is deployed to a test environment, and you want tests for what changed | Yes, to draft the scenarios |
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

`--base-url` and `--token-env` are needed only the first time. They write `.semar/semar.yaml`, and later runs read that file. See [Configuration](#configuration) for the other flags.

What `plan` does:

1. It copies the source at the head commit to a temporary directory.
2. It starts the planner in that copy. The planner can read a file, search, and list files. It cannot read a file outside the copy, and it cannot reach your test environment. With a harness provider it can also use your own MCP servers and skills, unless you turn them off. See [Choose the model](#choose-the-model).
3. The model returns one draft. Code validates it. Problems go back to the model, for at most three attempts.
4. It writes the files below and removes the copy.

| File | Content |
| --- | --- |
| `.semar/scenarios/<ticket>/<ticket>-NN.yaml` | One scenario: its steps and expected results |
| `.semar/store/scripts/...` | The exact request of each step |
| `.semar/plans/<head commit>/plan.md` | What changed, the scenarios per ticket, what was not drafted and what it needs, and the files that no scenario covers |

The output ends with the commands of the next steps:

```text
SHOP-482 (behaviour): 2 scenarios
SHOP-490 (config): 1 scenario
not drafted: 1
plan: .semar/plans/9f3c2ab41d07/plan.md
next:
  semar run --all --build 9f3c2ab41d07
  semar confirm --all
  semar run --all --build 9f3c2ab41d07
```

Read `plan.md` and the scenario files before you run them. The model read them from the code, and nothing was sent yet.

**3. Run against the test environment.**

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

* It does not read the tracker. A ticket is its key and the commits that name it.
* It does not invent data of the test environment. A scenario that needs an existing record, such as the ID of an order that the test account may read, is listed in `plan.md` under "Not drafted" with what it needs.
* It drafts requests from the source. It does not try them against the service first.

**What leaves your machine.** `plan` sends parts of your source and the diff to the model provider that you chose, through your own account or key. The line that `plan` prints at its start names the provider. It sends no token, no address of your test environment, and no file outside the copy. `changes`, `run`, `confirm`, and `activity` send nothing to a model.

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
* set the default provider and the model of each provider;
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
    model: ""                   # "" means the harness's own default
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

### Configuration

`.semar/semar.yaml` describes the test environment. `semar init` and the first `semar plan` write it from these flags:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--base-url` | none, required | The address of the service in the test environment |
| `--service` | The name of the repository directory | The name that scripts use for the service |
| `--env` | `uat` | The name of the environment |
| `--auth-header` | `Authorization` | The header that carries the session |
| `--auth-value` | `Bearer {{secret.TOKEN}}` | The value of that header. `{{secret.TOKEN}}` is replaced at run time |
| `--token-env` | `SEMAR_TOKEN` | The environment variable that holds the token |
| `--build-url` | none | An address of the service that answers with the commit it runs, such as `https://orders.uat.example.com/version` |
| `--build-commit` | `$.commit` | Where that answer holds the commit, as a JSONPath |

The file that they write:

```yaml
version: 1
environments:
  uat:
    kind: test
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

Three rules to know:

* **The token is never in a file.** The configuration names an environment variable. Semar reads it at run time and removes its value from everything it stores. Use a test account, never a personal or a production credential.
* **A result belongs to a build.** A run with no recorded build ends as `inconclusive`. With a build probe, Semar asks the service which commit it runs, before and after the run, and a change between the two makes the results `stale`. Without one, declare the build with `semar run --build <commit>`. The report then says that the build was declared and not read from the service.
* **The harness is yours.** `semar plan` starts the `claude` or the `codex` on your `PATH`, or the absolute path in the environment variable `SEMAR_CLAUDE_BIN` or `SEMAR_CODEX_BIN`. The configuration cannot name a program to start, because it is a file of the repository under test.

The file also takes `redact`, `policy`, `replay`, `providers`, `tickets`, and `plan` blocks. The [technical requirements](docs/TRD.md) and [Plan 12](docs/implementation/12-plan-from-a-diff.md) describe them.

### Commands

Every command takes `--dir <path>`, the root of the repository that holds `.semar/`. It defaults to the current directory. `semar <command> --help` lists every flag.

| Command | What it does |
| --- | --- |
| `semar changes --diff <link or range>` | Prints the tickets, commits, and files of a change. No model |
| `semar plan --diff <link or range>` | Drafts scenarios and scripts for a change, and writes `plan.md`. Also takes `--provider <id>`, `--model <name>`, `--no-setup`, `--max-scenarios <n>`, and `--no-fetch` |
| `semar dashboard` | Opens a page on your machine to manage the model providers and see usage. `--no-open` prints the address only, `--port <n>` sets the port |
| `semar providers` | Prints your providers and marks the one that would plan here. No model |
| `semar init --base-url <url>` | Writes `.semar/semar.yaml` |
| `semar script add <file>` | Checks a script against its scenario and stores it |
| `semar run --all` | Replays every scenario in `.semar/scenarios` |
| `semar run --scenario <id>` | Replays one scenario. Repeat the flag for more |
| `semar confirm <scenario id>` | Lists the expected results that need a person, and stores the confirmation. `--all` for every scenario, `--yes` to skip the question |
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
| `blocked` | The scenario could not start or continue: no script, a setup step failed, or the environment refused a request |
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
  plans/<head commit>/  plan.md and the accepted draft of one planned change
  runs/<run id>/        run.json, report.md, activity.jsonl, and the recorded exchanges of one run
  store/                accepted scripts and confirmations
```

Commit `semar.yaml` and `scenarios/` so that the team shares them. `runs/` holds recorded requests and responses of your test environment, with secrets redacted. Decide with your team whether it belongs in the repository.

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

## Documents

| File | What it holds |
| --- | --- |
| [docs/PRD.md](docs/PRD.md) | Product requirements: scope, users, decisions, requirements by milestone, risks, and open questions |
| [docs/TRD.md](docs/TRD.md) | Technical requirements: the Go design, the tool layer, the provider layer, verdict rules, configuration, commands, milestones, and spikes |
| [docs/WORKFLOW.md](docs/WORKFLOW.md) | The test workflow from intake to release, who acts in each phase, and the exit criteria |
| [docs/USER_SCENARIOS.md](docs/USER_SCENARIOS.md) | Walkthroughs from the user's side: setup, a failed scenario, pipeline mode, sign-in, and regression |
| [docs/implementation/README.md](docs/implementation/README.md) | The implementation guide: engineering rules, architecture, dependencies, and the sequence of plans |

## Open questions

Whether the vendors of Claude Code and Codex CLI allow a subscription login to drive their harness from another tool is not confirmed. Semar runs the user's own unmodified harness and never touches its credentials, and API keys work from the first release. The PRD lists this under its risks.

## License

Apache-2.0 is the intended license. The repository has no `LICENSE` file yet.
