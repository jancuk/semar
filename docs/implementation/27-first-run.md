# Plan 27: A first run with no hand-written files

* **Status:** Implemented. This text describes the system as built.
* **Needs first:** Plans 13, 14, 16, 17, 23, and 25.
* **With:** Plans 26 and 28.
* **Implements:** FR-356 to FR-360 and PRD decision D71. It changes the default that Plan 25 set (its D12) and the rule of Plan 25 that a criterion with no fixture is `notDrafted`.

## Goal

A developer with a repository that has never used Semar reaches a useful result without writing a YAML file and without copying one from another branch. A second developer who clones the repository learns in one command what the machine still needs.

On the measured first run, a person wrote or copied six files by hand: two fixtures, the screens file, one web scenario with its script, and a journey. A plan that needed data said "a person writes the fixture" and stopped there. This plan removes the hand-written files of an API change. Plan 28 removes those of a web test.

## Decisions

The first column is the decision as the plan stated it. The third column is what was built, and it says where the build differs and why.

| # | Decision | As built |
| --- | --- | --- |
| D1 | `semar init` on a terminal asks for what it cannot find and no flag gave: the address of the service, the environment variable that holds the token, and further accounts with one line each on who the account is. It prints what it found before it asks. Every question has a flag, and `init` with no terminal asks nothing and names the flags that are missing | Built, with one change. The questions run when the terminal is there and `--base-url` is not given. With `--base-url`, `init` is driven by its flags even on a terminal, and asks nothing. Without that rule every existing call of `init --base-url` would stop at a question on a terminal, and a script that runs on a terminal would hang. A flag that was given also skips its own question: `--token-env`, `--account`, `--describe`. The `Found` line names the API document, the number of documents under `docs/` (the tickets folder not counted), and the number of ticket files |
| D2 | `init` looks for the build probe when `--build-url` is not given: it sends `GET` to `/version` on the address of the service and takes it when the answer is JSON with the commit at `$.commit`. It tries no other path and sends nothing else. When it finds none, it says how to name one and writes the service with no probe | Built. The request has no credential, a 5 second limit, a 64 KiB bound on the answer, and follows no redirect, so it cannot leave the host of the service. It runs only when `init` is going to write the file. `semar plan --base-url`, which also writes the file, does not look for the probe: D2 names `init`. The test client of `init` is a field of the options, so that no test reaches a network |
| D3 | `--describe <account>=<text>` gives an account its description, and the question of D1 asks for it. The default account can have one too | Built. `default` names the default account. The text is checked by the same rule as `description` in `semar.yaml`, and `init` with a file compares `--describe` with it as it compares the other flags |
| D4 | `semar init` in a repository that has `.semar/semar.yaml` writes nothing. It checks the machine and prints one line for each item with the way to fix it. The exit code is 0 when nothing is missing and 2 otherwise. `--force` writes a new file as before | Built, without `--force`. `init` never had a flag that overwrites the file: with flags it compares them with the file and refuses a difference. A new flag that can destroy a configuration was not worth adding for the word "before", so a person edits the file. With no flag at all, `init` checks the machine. A line is `ok`, `missing`, or `review`, and only `missing` counts for the exit code. A service of a test environment is read through its build probe, and a service with no probe gets one `GET` to its address, to see that it answers. The check names the default provider, and not each step of a ladder. A scenario counts as not approved when an expected result needs a person under `policy.trust.confirm` and no confirmation holds it |
| D5 | The criteria pipeline is the default of `semar plan` when at least one ticket of the change has acceptance criteria. Otherwise the plan uses the single pipeline and says why. `--pipeline single` and `--pipeline criteria` choose as before | Built. `--with-setup` also keeps the single pipeline, and says so, because the jobs of the criteria pipeline have no tools. `--ladder` with a plan that takes the single pipeline is an error that names the reason. The choice is made after the tickets are read, so the provider is opened after the copy of the source and not before. Plan 25 said the default stays `single`: its D12 and its recommendation now point here |
| D6 | With no `--ladder` and no `plan.ladder`, a Claude Code provider plans with the ladder `haiku`, `sonnet`, and then the model of the provider. Every other provider plans with its own model, as before | Built. A step that repeats an earlier one is left out, so a provider whose model is `sonnet` has the ladder `haiku`, `sonnet`. `--model` names the model and builds no ladder, because the person chose |
| D7 | A criterion that needs data which no fixture gives no longer ends as "a person writes the fixture". Its answer names the fixture it needs: an ID, one sentence on the state, and the outputs. After the criteria jobs, code runs one job with no tools for each fixture that is needed, at most 5 in a plan. The answer is a closed schema: the steps of `create`, each with an operation from the API documents of the fixture's services, its path parameters, its body fields, and its captures, and then the outputs and the steps of `cleanup`. Code fills in the methods and the paths, and checks the answer with the checks of Plan 25 and the fixture loader. A failing answer goes back once, and then up the ladder | Built, with three changes. The answer of a criterion gets a fourth kind, `needsFixture`, with `fixtureId`, `fixtureState`, and `fixtureOutputs`, and not extra fields on `notDrafted`: a model that sees four kinds chooses one, and a field that is empty for most answers is one that a small model fills wrongly. The operations of the job are first those of the criteria that need the fixture and then the rest of the API documents, at most 40, because the state a criterion needs often takes an operation that its own packet left out. And a capture of a whole object is a problem for a fixture and for a scenario step, with the field to read instead: the first live probe of the fixture job (see Measured) captured `$.data` as a list ID, which no later request can use. The distinct fixtures are collected by ID, the first state stands, and the outputs are joined |
| D8 | A drafted fixture is written to `.semar/fixtures/<id>.yaml` with `owner: semar` and is not approved. On a test environment that allows writes, and unless `--no-trial` is set, the plan runs the fixture once and cleans up, and a fixture that fails goes back to the model with the problem, inside the same bounds. A fixture that still fails is not written, and its criteria stay "not drafted" with the reason | Built. The run goes through the engine, the effects policy, and the recorder of a run of its own, with the purpose `fixture-check`, and the fixture counts as approved for that run only. A policy refusal of a step, such as a method that the environment does not allow, goes back to the model like any other problem. A read-only environment, a production environment, `--no-trial`, `plan.trial: false`, and a missing account give a fixture that is drafted and not run, and plan.md says why. The run does not store a health record. The file starts with one comment line that says who drafted it |
| D9 | After the fixtures are written, the criteria that needed them are planned again, once, with the new fixtures in their packets. Their scenarios name the fixture under `Given`. A scenario whose fixture is not approved runs as `needs review` (Plan 26, D7), and `semar review` shows the fixture before the scenario | Built, with one change of order. The second pass happens inside the plan, after the fixtures are drafted and before the files are written, so that the plan is checked as a whole and written once, and a plan that fails writes nothing. The second pass starts at the first rung and has its own correction and ladder. A criterion that names a fixture again in the second pass stays a gap. The plan prints `semar review <fixture>` before the scenarios, and the run of a scenario whose fixture is not approved is Plan 26's |
| D10 | `plan.maxCost` in the person's own `providers.yaml`, and `--max-cost <dollars>` on `semar plan`, bound what one plan may spend. When the sum that the providers reported reaches it, no new job starts, the jobs that run finish, and the plan is written with what was answered. The criteria that were not reached are "not planned: the cost limit of $X was reached", and the exit code is 2. A provider that reports no cost is not bound, and the plan says so at the start. In the single pipeline the limit is checked between jobs, because one job cannot be cut | Built. The sum is compared before each job, a correction and a fixture job included. A job that is already running finishes, so the plan can pass the limit by the jobs of one batch: four run at once. A criterion that was not reached is a `notDrafted` entry with the cite, so the ledger keeps it as a gap, and a later plan reuses the answers that exist. A criterion that had an answer with problems and no room for the correction is not planned too. Only Claude Code reports a cost: Codex and the API kinds report tokens, and the start line names them. In the single pipeline a plan that has a passing draft keeps it and exits with 2, and a plan with none ends with the error "the cost limit of $X was reached after N attempts" and writes nothing |
| D11 | A repository's `semar.yaml` cannot set `plan.maxCost` or the ladder | Built by the strict reading of the file, as for `plan.withSetup`: the key is an unknown key, and the plan stops with a message that names it |
| D12 | The offer of `semar run` to plan (Plan 26, D6) shows an estimate: the mean cost of a criterion in the usage log of the provider and model that would plan, times the number of criteria. With no record it says that there is no estimate yet | Built as `usage.EstimateCost` (see the Contract). The unit is a job of the criteria pipeline. The usage log gets two fields on the records of that pipeline, `pipeline` and `asked`, so that a record of a single plan, which is one huge job, never counts, and a model matches by the name the ladder gives it (`haiku`) or by the name that the provider reported. A provider that has records and reports no cost gets "no estimate: <provider> reports no cost" |
| D13 | A step of a fixture can act as an account, and the fixture job chooses it. A fixture step has `as`, the name of an auth profile, on `find`, `create`, `until`, and `cleanup` steps. The step is sent with that profile, as a scenario step is, and with no `as` it is sent as before. The fixture hash covers it. The packet of the job lists the accounts with their descriptions, and its closed schema has `as` on each step as an enum of them. Code writes `as` into the file only when the account is not the default one. When the description of the needed fixture names an account as a word and no step of the answer acts as it, the answer has a problem that goes back to the model | Built, after a live run in which a draft said "a board owned by the carol account", every request ran as the default account, the trial said `ok`, and the scenario that used the fixture failed on a correct build with `expected status 404, observed status 422`. The `as` of a step wins over the `authProfile` of its service, as the `as` of a scenario step wins over the profile of the scenario. An `as` that the configuration does not define is a problem of the file (Plan 17, F17), and the plan, `semar run`, and `semar fixture check` load the sessions of the accounts that a fixture acts as. The trial of a plan loads every account of the configuration, so that a fixture that acts as one of them can run. With fewer than two accounts the schema and the prompt leave `as` out, because there is no choice. The check matches the name of an account other than the default one as a whole word in any case, and a hyphen, a digit, and an underscore are part of the word. Its text is `the description names the account carol, and no step acts as carol: set as on each step that carol sends`. The check cannot tell the account that owns the data from an account that is only named, so a description such as "an order of carol that bob has read" is refused until bob has a step. The prompt of the fixture job is version 3 |

## Contract

### `semar init` on a new repository

```text
$ semar init
Found   docs/openapi.yaml (API document), docs/ (7 documents), docs/tickets/ (12 tickets)
Address of the service in the test environment: https://orders.uat.example.com
Environment variable that holds the token [SEMAR_TOKEN]: ORDERS_TOKEN
Who is this account, in one line: a customer with paid orders
Another account? Name, or Enter for none: support
  Environment variable of its token: ORDERS_TOKEN_SUPPORT
  Who is this account, in one line: a support agent who can refund any order
Another account? Name, or Enter for none:
Found   the build probe at https://orders.uat.example.com/version
Wrote .semar/semar.yaml.
export ORDERS_TOKEN_SUPPORT=<the token of the account support>
Next: semar run
```

The probe is looked for after the questions, because it needs the address. An `export` line appears for each token variable that the environment does not hold yet. When it holds all of them, `Wrote .semar/semar.yaml. Next: semar run` is one line.

### `semar init` in a repository that is set up

```text
$ semar init
.semar/semar.yaml exists: checking this machine.

  ok       ORDERS_TOKEN is set
  missing  ORDERS_TOKEN_SUPPORT is not set: export it with the token of the account "support"
  ok       uat: orders answers, build 4f2a91c
  ok       the provider claude would plan
  missing  Chrome was not found: 3 scenarios have web steps
  review   2 scenarios and 1 fixture are not approved → semar review

2 missing. Next: fix them, then semar run
```

### A drafted fixture in plan.md and in the plan's output

```text
## Fixtures drafted
- order.open: An open order of the default account. 2 requests, cleans up. Ran once: ok.
  Used by SHOP-482-01. Not approved → semar review order.open
```

```text
fixture drafted: order.open. Ran once: ok. Not approved: semar review order.open
model use: 3 jobs (3 haiku), 1 correction, 1 fixture drafted, 310k input, 18k output, $0.06
next:
  semar review order.open   read what the plan drafted, a fixture before the scenarios that use it, and approve it
  semar run --build 1bb903e53d2c   run the approved scenarios against the service
  semar status   where things stand
```

A fixture whose steps act as other accounts says which: `3 requests as bob, carol, cleans up.` A step of `semar review` shows it as `create 1 as carol: POST orders /v1/orders`.

A fixture that no model could write is `fixture not drafted: <id>: <the first problem>`, and plan.md says `- <id>: not drafted. <problem>. Needed by <cites>.`

### The cost limit

```text
$ semar plan --diff main..release --max-cost 0.50
semar: no new job starts once the providers have reported $0.50
...
semar: the cost limit of $0.50 was reached: 9 criteria were not planned, and plan.md lists each as not planned. Plan again to do the rest: the answers that exist are reused
$ echo $?
2
```

### The estimate that Plan 26 calls

```go
package usage

// EstimateCost is the mean cost of a job of the criteria pipeline on this provider and model, from the
// usage log in home, times the number of criteria.
func EstimateCost(home, providerID, model string, criteria int) (Estimate, error)

type Estimate struct {
	Provider, Model, Kind string
	Criteria              int
	Known                 bool    // false when no earlier job gives a mean
	USD                   float64 // the mean cost of a job times Criteria
	Jobs                  int     // the earlier jobs that the mean is from
	NoCost                bool    // the provider has records and reports no cost
}

func (e Estimate) String() string
// "about $0.08 for 16 criteria, from 48 earlier jobs of claude haiku"
// "no estimate yet: the usage log has no earlier plan of claude haiku"
```

`home` is the Semar home (`options.home()` in `internal/cli`). The provider and the model that would plan come from `firstRung(o, dir, cfg)` in `internal/cli/planladder.go`: the first step of `plan.ladder`, else the first step of the default ladder, with `model` empty for the provider's own model.

## Tasks

Each task was test first, and one commit.

1. **Questions of `init`.** D1 and D3, behind a reader and a flag for the terminal, so that a test answers. No terminal: no question.
2. **The build probe.** D2.
3. **The check of a machine.** D4.
4. **The default pipeline and ladder.** D5 and D6, with the documents of Plan 25 changed where they say otherwise.
5. **The fixture a criterion needs.** The answer of a criterion names it (D7), and the plan collects the distinct ones.
6. **The fixture job.** The packet, the schema, the checks, the assembly to a fixture file, and the correction and the ladder (D7).
7. **The trial and the second pass.** D8 and D9, with plan.md.
8. **The cost limit.** D10 and D11.
9. **The estimate.** D12, as a function that `run` of Plan 26 calls.
10. **Documents.** The README, the PRD and TRD, and this plan as built.

A change after the live check: the fixture job sees the operations of the documents, and a capture reads one value.

11. **A fixture step that acts as an account.** D13, in four commits, each test first: `as` on a fixture step with its validation, hash, and display; the choice of the account in the fixture job; the check of the description; and the documents.

## Measured

**What was run.** A small mock service of boards, lists, and cards, outside the Semar repository, with two tickets of 16 acceptance criteria (about 11 that an API call can show, and the rest on a web or an Android screen), an API document of 15 operations, and three accounts: the owner of the boards, a member of the shared board, and a person outside. The clone had no `.semar` folder and nothing copied from another branch. `semar init` ran with flags (a terminal was not there), and found the build probe at `/version`. `semar init` again said that nothing was missing. Then `semar plan` ran three times with the Claude Code provider and the default ladder, which was `haiku`, `sonnet`, with a temporary Semar home. Each run started with the files of the earlier plan removed, so no answer was reused.

| Run | What changed | Criteria answered | Fixtures asked for | Corrections | Input / output tokens | Cost | Wall time |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | Nothing | 16 of 16 by haiku | 0 | 0 | 553k / 36k | $0.08 | 54 s |
| 2 | The criterion prompt says to name a fixture for data that a chain of setup steps would make | 16 of 16 by haiku | 0 | 0 | 554k / 39k | $0.07 | 55 s |
| 3 | The API document holds 31 operations, so each packet drops the ones that its criterion does not reach | 16 of 16 by haiku | 0 | 0 | 554k / 41k | $0.07 | 61 s |

Every run gave 11 or 12 scenarios, the other criteria were manual checks, and the `model use:` line said `16 jobs (16 haiku)`. The 12 scenarios of run 3 were confirmed and replayed against the service: 12 passed, and 4 use cases are left for a person. No file was written by hand for the API part of this change.

**A fixture job with a real model.** The three plans did not need a fixture: haiku built the boards, the lists, the members, and the cards in setup steps, in all 16 criteria of the three runs, even when the prompt asked it to name a fixture, and in run 3, where each packet was cut to the operations that its criterion reaches. So the path from a criterion that names a fixture to the plan that runs it was proved with a scripted provider and a sample service (see the tests), and not with a real model. The fixture job itself was tried alone with a real model, twice, before and after the change below: a direct call of the job with haiku, for the state "a board of the owner that a second account is a member of, with two empty lists", on the 31 operations of the document, outside the three plans.

* The first answer was valid by the checks and wrong: it captured the whole object `$.data` as the ID of each list. The fixture went to `semar fixture check` against the service, which reported `create step 1 captured no value for board`. The cleanup could not run, and one board stayed on the service. A check for this (a capture of a whole object is a problem, with the field to read instead) and a line in the prompt were added, and the board was removed by hand.
* The second answer, after that change, was right at the first try on haiku: 48k tokens in, 1.7k out, $0.003. The fixture ran on the service as healthy: create made, cleanup done, no board left.
* The fixture does not add the second account to the board, although the state says so. The operation that adds a member needs the email of that account, and neither the state nor the descriptions of the accounts gave it. A fixture can use only a value that the packet holds.

**What the measurement does not show.** Plan 27 asks whether a criterion that needs data no fixture gives ends with a fixture that a person only approves. On this service and this model it never came to that, because the model could build the data itself. Whether a small model names a fixture when the API has no operation that makes the state (a state that another service makes) is not measured. The fixture of the live probe was drafted for a state that the model was told, and not for one that it had to choose. The run of the whole of Plans 26, 27, and 28 (init with answers, run, review, run on the fixed build and on the build with the defect, and status) is for after the three are merged. The cost of the plans is what the provider reported, $0.22 for the three.

**Gaps that stay.**

* A description that names an account that only reads or joins the data, such as "an order of carol that bob has read", is refused until some step acts as bob (D13). The check cannot tell the owner of the data from an account that is only named. The model has to leave the name out or give bob a step.
* A fixture whose run fails at a late `create` step leaves what the earlier steps made, when the failed step captured nothing. The service gets the data of the failed run, and a person removes it.
* A scenario makes data in setup steps and never removes it. Only a fixture cleans up. The prompt now says so, and the models still chose the setup steps.
* The plan prints `semar review` and `semar status` before Plan 26 is merged.

## Not built

* A fixture with `find` or `until` steps from a model. A drafted fixture has `create` and `cleanup`.
* A drafted journey. A journey is written by a person.
* A limit on tokens for a provider that reports no cost.
* `--force` on `semar init`.
