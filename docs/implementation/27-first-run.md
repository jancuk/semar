# Plan 27: A first run with no hand-written files

* **Status:** Planned.
* **Needs first:** Plans 13, 14, 16, 17, 23, and 25.
* **With:** Plans 26 and 28.

## Goal

A developer with a repository that has never used Semar reaches a useful result without writing a YAML file and without copying one from another branch. A second developer who clones the repository learns in one command what the machine still needs.

On the measured first run, a person wrote or copied six files by hand: two fixtures, the screens file, one web scenario with its script, and a journey. A plan that needed data said "a person writes the fixture" and stopped there. This plan removes the hand-written files of an API change. Plan 28 removes those of a web test.

## Decisions

| # | Decision | Reason |
| --- | --- | --- |
| D1 | `semar init` on a terminal asks for what it cannot find and no flag gave: the address of the service, the environment variable that holds the token, and further accounts with one line each on who the account is. It prints what it found before it asks: the API document, the documents, the ticket files, and the build probe. Every question has a flag, and `init` with no terminal asks nothing and names the flags that are missing | A person who starts should not need to read eleven flags. A pipeline must not hang on a question |
| D2 | `init` looks for the build probe when `--build-url` is not given: it sends `GET` to `/version` on the address of the service and takes it when the answer is JSON with the commit at `$.commit`. It tries no other path and sends nothing else. When it finds none, it says how to name one and writes the service with no probe | The probe is what ties a result to a build, and most first runs left it out. One read of one conventional path is safe on a test environment |
| D3 | `--describe <account>=<text>` gives an account its description, and the question of D1 asks for it. The default account can have one too | The planner chooses the account of a step from the descriptions. An account with no description is one that the planner cannot use well |
| D4 | `semar init` in a repository that has `.semar/semar.yaml` writes nothing. It checks the machine and prints one line for each item with the way to fix it: the environment variable of each auth profile, the address and the build probe of each service of each test environment, the model provider that would plan, Chrome when a scenario or a journey leg has a web step, and the fixtures, screens files, and scenarios that are not approved. The exit code is 0 when nothing is missing and 2 otherwise. `--force` writes a new file as before | The second developer of a team clones a repository that is set up. What that person needs is the list of what the machine lacks |
| D5 | The criteria pipeline is the default of `semar plan` when at least one ticket of the change has acceptance criteria. Otherwise the plan uses the single pipeline and says why. `--pipeline single` and `--pipeline criteria` choose as before | Plan 25 measured the criteria pipeline at a twentieth of the cost with the same score. A first plan should be the cheap one. This replaces D12 of Plan 25, and the owner decided it |
| D6 | With no `--ladder` and no `plan.ladder`, a Claude Code provider plans with the ladder `haiku`, `sonnet`, and then the model of the provider. Every other provider plans with its own model, as before | A person who never set a ladder still gets the small model first, and the strong one only for what the small one cannot answer |
| D7 | A criterion that needs data which no fixture gives no longer ends as "a person writes the fixture". Its answer names the fixture it needs: an ID, one sentence on the state, and the outputs. After the criteria jobs, code runs one job with no tools for each fixture that is needed, at most 5 in a plan. The answer is a closed schema: the steps of `create`, each with an operation from the API documents of the fixture's services, its path parameters, its body fields, and its captures, and then the outputs and the steps of `cleanup`. Code fills in the methods and the paths, as in Plan 25, and checks the answer with the checks of Plan 25 and the fixture loader. A failing answer goes back once, and then up the ladder | The fixture was the largest file that a person wrote by hand, and its parts are the ones a model with a closed list of operations gets right |
| D8 | A drafted fixture is written to `.semar/fixtures/<id>.yaml` with `owner: semar` and is not approved. On a test environment that allows writes, and unless `--no-trial` is set, the plan runs the fixture once and cleans up, and a fixture that fails goes back to the model with the problem, inside the same bounds. A fixture that still fails is not written, and its criteria stay "not drafted" with the reason | A fixture that was never run is a guess. The trial of Plan 16 has the same reason |
| D9 | After the fixtures are written, the criteria that needed them are planned again, once, with the new fixtures in their packets. Their scenarios name the fixture under `Given`. A scenario whose fixture is not approved runs as `needs review` (Plan 26, D7), and `semar review` shows the fixture before the scenario | The person approves the fixture and its tests in one sitting, and nothing runs a fixture that nobody read |
| D10 | `plan.maxCost` in the person's own `providers.yaml`, and `--max-cost <dollars>` on `semar plan`, bound what one plan may spend. When the sum that the providers reported reaches it, no new job starts, the jobs that run finish, and the plan is written with what was answered. The criteria that were not reached are "not planned: the cost limit of $X was reached", and the exit code is 2. A provider that reports no cost is not bound, and the plan says so at the start. In the single pipeline the limit is checked between jobs, because one job cannot be cut | A team that pays for the model needs a ceiling. Semar had bounds on turns and none on money |
| D11 | A repository's `semar.yaml` cannot set `plan.maxCost` or the ladder | They decide what the person spends and which provider receives the code, as `plan.withSetup` and `plan.ladder` do |
| D12 | The offer of `semar run` to plan (Plan 26, D6) shows an estimate: the mean cost of a criterion in the usage log of the provider and model that would plan, times the number of criteria. With no record it says that there is no estimate yet | A person who is asked to spend money should see how much |

## Contract

### `semar init` on a new repository

```text
$ semar init
Found   docs/openapi.yaml (API document), docs/ (7 documents), docs/tickets/ (12 tickets)
Address of the service in the test environment: https://orders.uat.example.com
Found   the build probe at https://orders.uat.example.com/version
Environment variable that holds the token [SEMAR_TOKEN]: ORDERS_TOKEN
Who is this account, in one line: a customer with paid orders
Another account? Name, or Enter for none: support
  Environment variable of its token: ORDERS_TOKEN_SUPPORT
  Who is this account, in one line: a support agent who can refund any order
Another account? Name, or Enter for none:

Wrote .semar/semar.yaml. Next: semar run
```

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

### A drafted fixture in plan.md

```text
## Fixtures drafted
- order.paid: an order of the default account that is paid. 2 requests, cleans up. Ran once: ok.
  Used by SHOP-482-02, SHOP-482-03. Not approved → semar review order.paid
```

## Tasks

Each task is test first, and one commit.

1. **Questions of `init`.** D1 and D3, behind an interface for the terminal so that a test answers. No terminal: no question.
2. **The build probe.** D2.
3. **The check of a machine.** D4.
4. **The default pipeline and ladder.** D5 and D6, with the documents of Plan 25 changed where they say otherwise.
5. **The fixture a criterion needs.** The answer of a criterion names it (D7), and the plan collects the distinct ones.
6. **The fixture job.** The packet, the schema, the checks, the assembly to a fixture file, and the correction and the ladder (D7).
7. **The trial and the second pass.** D8 and D9, with plan.md.
8. **The cost limit.** D10 and D11.
9. **The estimate.** D12, as a function that `run` of Plan 26 calls.
10. **Documents.** The README, the PRD and TRD, and this plan as built.

## Done when

The acceptance test of Plans 26, 27, and 28 together, on a clone of a sample service with no `.semar` folder and nothing copied from another branch:

1. `semar init` with answers.
2. `semar run`, with `y` to the offer to plan. The plan drafts the fixtures it needs.
3. `semar review`.
4. `semar run` passes on the fixed build and fails on the build with the defect, with the criterion that the defect breaks.
5. `semar status` says `Ship: no` with the manual checks that are open.

The run is recorded with the number of commands, the files written by hand (the target is none for the API), the cost of the plan, and the gaps that stay.

## Not built

* A fixture with `find` or `until` steps from a model. A drafted fixture has `create` and `cleanup`.
* A drafted journey. A journey is written by a person.
* A limit on tokens for a provider that reports no cost.
