# Plan 26: The daily loop

* **Status:** Implemented. This text describes the system as built.
* **Needs first:** Plans 12, 17, 18, 19, 21, 22, and 24.
* **With:** Plans 27 and 28. The three plans share one acceptance test, in "Done when" of Plan 27.

## Goal

A person who knows git can use Semar without a manual. A developer runs `semar run` before a merge request. A reviewer runs `semar review`. A product owner reads `semar status`. Each command ends with the command to run next.

Semar has 18 commands today, and one release decision on a fresh repository took about 35 command lines. `semar confirm` has four jobs. A run happens twice, with a confirmation between. The state of a release is in three places. This plan changes the commands, and not what Semar proves: the stored evidence, the approvals, and the use case states of Plan 19 stay.

## Decisions

| # | Decision | Reason |
| --- | --- | --- |
| D1 | The everyday commands are `init`, `run`, `review`, `status`, `check`, `defer`, `log`, `show`, `rm`, `plan`, and `record` (Plan 28). `semar --help` lists them first under "Every day", and the others under "More": `changes`, `export`, `fixture`, `journey`, `login`, `providers`, `dashboard`, `scenarios`, `screens`, `script`, `version`, `completion`. `record` joins "Every day" when Plan 28 adds it | A new person reads the first group and can start. The others stay for a person who wants control |
| D2 | `confirm`, `ledger`, and `activity` are removed, with no alias. `review` does what `confirm` did. `status`, `check`, and `defer` do what `ledger`, `ledger record`, and `ledger defer` did. `show` does what `activity` did. A removed name answers with one line that names the new command, and exit code 3. The answer comes from a hidden command that takes any arguments, so the old arguments are never read | Semar has no users outside its owner yet. Two names for one thing is what made the help long |
| D3 | The change is the range from the base branch to `HEAD`. The base is `change.base` in `semar.yaml`, else the default branch of `origin`, else `main`, else `master`. `--base <branch>` on `run`, `status`, and `plan` names another. `run` and `status` read the range from the clone as it is, with no fetch. The tickets of the change are the tickets that `semar changes` finds in the range. A directory that is not in a git repository has no change: `status` then shows every use case and says `Not in a git repository`, and `run` stops and names `--all` and `--scenario`. `--ticket` on `status` ignores the change and filters every use case, as it did on `ledger` | `run` and `status` must be fast and work with no network. A repository that releases from another branch sets the base once |
| D4 | `semar run [environment]` with no `--all` and no `--scenario` runs the change: every scenario whose `@ticket:` is a ticket of the change, and then every journey with `critical: true`. `--all` runs every scenario and every critical journey. `--scenario <id>` runs the named scenarios and no journey. `--no-journeys` leaves the journeys out. The environment is the first argument or `--env`, and never both. `--all` and `--scenario` do not go together | A developer wants the tests of the branch, and the flows that must never break |
| D5 | Before a run sends its first request, it prints what it selected. The header comes after the secrets of the run are found, because reading the build is a request and a run with a missing secret sends nothing: the change, the environment with its kind and build, the number of scenarios and journeys, and the gaps. A gap is a ticket of the change with no use case file, a commit with no ticket, or a surface that cannot run here. Only an Android scenario is known not to run here, because Semar has no engine for it. When the change has no ticket, `run` says so, names `--all`, and ends with exit code 2 | A person must be able to tell what a green result covers. A commit with no ticket is a change that no test was chosen for |
| D6 | A ticket of the change that has a ticket file and no file in `.semar/obligations/` is not planned. On a terminal, `run` offers to plan the tickets that are not planned, with the provider, the model, and an estimate from the usage log, and plans only on `y`. With no terminal, or with `--no-plan`, `run` never plans: it lists the tickets as "not planned" and its exit code is at least 2. The estimate comes from `Options.PlanEstimate`, a function with the default "no estimate yet", and Plan 27 supplies the real one. On `y`, `run` runs `semar plan` for the change, as if the person had typed it | `run` is the one command of every day, so it must lead to a plan. A command that spends money on a model asks first, and a pipeline never spends it |
| D7 | The words for a result are `passed`, `failed`, `blocked`, `needs review`, and `stale`. `needs review` is the stored outcome `inconclusive` or `blocked` with a reason that an approval is missing. A fixture or a screens file that is not approved is stored as `blocked`, and not `inconclusive` as this plan first said, so both outcomes count. A result that was not run keeps the word `not_run`. The reasons are: a scenario that is not confirmed, a fixture or a screens file that is not approved. Every other `inconclusive` result keeps its word. `stale` is a pass on another build, or of a scenario that changed since. The stored outcomes do not change | The person needs to know what to do, and `inconclusive: not confirmed` does not say it |
| D8 | Every line of a result, and every group of `status`, ends with the next command: `needs review → semar review`, `stale → semar run`, `for a person → semar check <id> pass`, `not planned → semar plan`. `run` ends with the same block that `status` prints | The reader should never need the documentation to continue |
| D9 | `semar review [id...]` shows everything that needs a person, one item at a time, and takes `y` (approve), `n` (skip), `d` (details), and `q` (stop). The items are, in order: the fixtures that are not approved, the screens files that are not approved, the scenarios that need a confirmation, grouped by use case, and the journeys. An ID narrows the list: a scenario, a ticket, a fixture, a journey, or the app of a screens file. `--yes` approves every item with no question. With no terminal and no `--yes`, `review` lists the items, one line each, and ends with exit code 2. It also ends with exit code 2 when an item was skipped or the review stopped. An ID that is none of the kinds is an error that says so. A scenario item is a use case: it holds every test of the use case that needs a confirmation, and `y` approves them all. `--env` names the environment of a journey's legs, and it is needed only when there are several | One command for one question: what needs my eyes |
| D10 | An item opens with the requirement, and not with the file. For a scenario: the cite and the text of the criterion, the title of the test, its surface and accounts, and its Given, When, and Then lines. Under it: the fixtures it uses. For a fixture: its description, the requests it sends with their methods and paths, and what it removes afterwards. For a screens file: each screen with the number of its elements. `d` prints the whole file as `confirm` did. Every request of a fixture is on the screen before its approval is asked. The first view of a fixture does not show its hash or its parameters, and `d` does. The first view of a screens file shows what changed since the last approval, because a person must see a moved element before approving it | A product owner or a developer can judge "a refund larger than the payment is refused". Nobody can judge a hash. A fixture writes data, so its requests are shown in full |
| D11 | An approval also stores the `user.email` of git, when it is set. `status` marks a use case whose every approval came from an author of a commit of the change: `approved by an author of the change`. The approvals of a use case are the confirmations of its scenarios and the approvals of the fixtures that they use. The approvals of a screens file are stored with the email and are not counted. A use case with no approval, or with one that has no email, is not marked. It is a mark, and it does not change a state or an exit code | A team with no tester lets a developer approve. The reader of a release report must be able to tell |
| D12 | `semar status [environment]` shows the use cases of the change, each in exactly one group, and the groups add up to the total. The groups are `verified`, `failing`, `blocked`, `needs review`, `stale`, `for a person`, `no test`, and `deferred`. `--all` shows every use case, and `--ticket` narrows as before. With an environment, results count only on the build that runs now, as `semar ledger --env` did. The first line names the branch, the base, the environment, and the build. The last line is `Ship: yes` or `Ship: no` with the reasons. The exit codes are those of `semar ledger`. A group lists its use cases, except `verified`, which lists only the use cases that have something to say: a manual check, a linked test, or the mark of D11. A ticket of the change with no use case is shown as `not planned` outside the groups, so the groups still add up, and it makes the exit code 2. With no environment named, `status` reads the only environment of `semar.yaml`, and when it cannot be read it goes on with the builds not read and says so | A count that does not add up cannot be trusted. One requirement can have several tests, so the count is of requirements and the tests are listed under them |
| D13 | A manual check that a person recorded as passed on the build that runs now is in `verified`, with `checked by <user>`. It was shown under "needs a person" before, although the exit code counted it. The state is `verified` in `obligation.Derive`, so every reader sees it. A manual `failed` result is under `failing` | The table and the exit code must say the same |
| D14 | `semar status --html <file>` also writes the same content as one HTML file with no outside resource: the first line, the verdict, the groups, each use case with its reasons and the run it was read from, and the manual checklist. `--checklist` prints the manual checklist as before | A product owner has no command line. The file can be attached to a ticket or sent |
| D15 | `semar check <id> pass\|fail [--note <text>]` records what a person checked, as `semar ledger record` did. `semar check <id> --by <scenario id>` says that a scenario does the manual check: the scenario is added to `covers` of the use case, and the store keeps who said so. From then on the state of the use case comes from the scenario, and `status` shows `by test <id>, linked by <user>`. `semar defer <id> --until <date> --reason <text>` is `semar ledger defer`. A pass is `pass`, a failure is `fail`, and the note is `--note`. `check` reads the builds of the environment you name with `--env`, or of the only one in `semar.yaml`, so a pass counts on the current build. A link counts only while its scenario is in `covers`, and `check <id> pass` on a linked use case is refused. Writing `covers` back loses the comments of the obligations file | An automated test proves a manual check only when it checks the same requirement under the same conditions. A person decides that. Semar never links the two on its own |
| D16 | `semar log` lists the stored runs, newest first: the run, the time, the environment, the builds, and the counts of each outcome. `-n <count>` sets how many, and the default is 20. `semar show [run]` prints the report of a run, and the newest when none is named. `--activity` prints what the run did in order, as `semar activity` did, and `--writes`, `--scenario`, and `--pass` go with it. `show` takes `latest` as well | The history of a release is what a person asks for after a failure |
| D17 | `semar rm <scenario id> --reason <text>` retires a scenario. It removes the scenario file and its scripts, removes the scenario from `covers` of every use case, and adds `retired` to each of them: the scenario, the reason, the user, and the day. Stored runs stay. A use case that loses its last test goes to `no test`, and `status` shows the reason and who retired it. `rm` refuses an ID that is not a scenario and says what the ID is. A use case with a manual check and no test left goes back to `for a person`, with the retirement in its reasons | Removing a failing test must not make a release look ready. The use case stays and shows that nothing tests it |
| D18 | `semar plan` with no `--diff` plans the change of D3. It reads the clone and never fetches, and `--base` names another base | The range is known, so the person does not type it |

## Exit code of `run`

`run` ends with the worse of what the tests did (0 passed, 1 failed, 2 anything else) and what the selection fixed (2 for a ticket that is not planned, or a change with no ticket). It does not end with the code of `status`, so a change with a manual check for a person still ends a run with 0 when everything that ran passed. `semar status` gives the verdict of the change, and its last line is `Ship`.

## Contract

### `semar run`

```text
$ semar run
Change    feature/refund-limit against main: SHOP-482, SHOP-490
Target    uat (test), orders at 4f2a91c
Selected  8 scenarios, 2 critical journeys
Gaps      SHOP-490 is not planned → semar plan
          1 commit has no ticket: 7c1d2aa "tidy the logger"

SHOP-482-01  passed
SHOP-482-02  failed        step refund-too-large: expected status 422, observed status 200
SHOP-482-03  needs review  not confirmed → semar review SHOP-482-03
journey order-to-refund  passed

<the block of semar status>
```

### `semar status`

```text
$ semar status
On feature/refund-limit against main · uat · orders at 4f2a91c
9 use cases of SHOP-482, SHOP-490

  verified      5
  failing       1   SHOP-482/2  SHOP-482-02 failed in r_20261117_101502: expected status 422, observed status 200
  needs review  1   SHOP-482/3  → semar review SHOP-482-03
  for a person  1   SHOP-482/manual-1  Check the refund receipt on a phone → semar check SHOP-482/manual-1 pass
  no test       1   SHOP-490/1  not planned → semar plan

Ship: no. 1 failing, 3 open.
```

The numbers of the groups add up to the number in the second line. A group with no use case is not printed. With no environment and several in `semar.yaml`, the rule of `semar ledger` holds.

### `semar review`

```text
$ semar review
1 of 3 · SHOP-482, criterion 3 (docs/tickets/SHOP-482.md:14)
  "A refund larger than the paid amount is refused."

  Test SHOP-482-03 · API · as default
    Given the fixture order.paid as o
    When  Refund more than was paid
    Then  the status is 422
          $.error.code equals "refund_too_large"

  Uses the fixture order.paid (approved)

  [y] approve  [n] skip  [d] details  [q] stop
```

## Tasks

Each task is test first: the test fails, the code makes it pass, and the task is one commit.

1. **The change.** `change.base` in `semar.yaml`, the base rule of D3, and one function that gives the tickets and the commits with no ticket of `base..HEAD` from the clone with no fetch. `--base`.
2. **Words and next steps.** One place that turns a stored result into the word of D7 and the next command of D8. `run`, the report, and `status` use it.
3. **`status`.** The groups of D12 and D13 over the entries of Plan 19, the scope of the change, the first and the last line, `--all`, `--ticket`, `--checklist`, and the exit codes. Remove `ledger`.
4. **`status --html`.** D14.
5. **`check` and `defer`.** D15, with `--by` and the change to how a use case with a manual check and a linked scenario gets its state.
6. **`review`.** D9, D10, and D11. Remove `confirm`. Every test of `confirm` moves to `review`.
7. **`run`.** The selection of D4, the header and the gaps of D5, the journeys, the offer of D6 behind a function that Plan 27 can call, and the status block at the end.
8. **`log` and `show`.** D16. Remove `activity`.
9. **`rm`.** D17.
10. **Help and documents.** The groups of D1, the lines for removed names, `plan` with no `--diff`, the README, the PRD and TRD sections that name a removed command, and this plan as built.

## Done when

On the sample application, with a branch that changes one ticket:

* `semar run` prints the header, runs only the scenarios of that ticket and the critical journeys, and ends with the status block.
* A scenario that is not confirmed shows `needs review`, and after `semar review --yes` the next `semar run` passes it.
* `semar status` counts add up, and `semar status --html out.html` writes a file that opens with no network.
* `semar rm <id> --reason <text>` moves its use case to `no test`, and `Ship` says `no`.
* `semar confirm`, `semar ledger`, and `semar activity` each answer with the new name.
* `go test ./...` and `go vet ./...` pass.

## Not built

* An environment that the clone remembers, like an upstream branch. The rule for several environments stays.
* Retiring a journey, a fixture, or a use case with `rm`.
* Evidence from logs, metrics, or traces beside a failure. It is a plan of its own.
* A rule that refuses an approval by an author of the change. D11 only marks it.
