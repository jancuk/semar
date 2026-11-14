# Semar user scenarios

* **Status:** Draft 2. Semar is not built. This document describes the planned experience, so that the design can be judged from the user's side.
* **Date:** 2026-11-14
* **Related:** [product requirements](PRD.md), [technical requirements](TRD.md), [test workflow](WORKFLOW.md), [the main scenario as a page](scenario.html)

Each scenario says who you are, where you start, what you do, and where the result appears. Terminal output is illustrative. The examples use an invented order service and tickets `SHOP-482`, `SHOP-490`, and `SHOP-495`.

| Scenario | Read it when |
| --- | --- |
| [What must be ready](#what-must-be-ready) | You want to know what Semar depends on |
| [1. Your tickets are already on UAT](#scenario-1-your-tickets-are-already-on-uat) | You deployed and want to know what comes next. Start here |
| [How you gain confidence in a result](#how-you-gain-confidence-in-a-result) | You want to know why a result can be trusted, and how to check |
| [2. A scenario failed](#scenario-2-a-scenario-failed) | The report shows a failure or a security finding |
| [3. Pipeline mode](#scenario-3-pipeline-mode-from-merge-request-to-release) | Nobody should have to type a command |
| [4. You review and decide](#scenario-4-you-review-and-decide) | You answer questions, triage findings, or sign off |
| [5. Set up Semar for a repository](#scenario-5-set-up-semar-for-a-repository) | You are the first person on the team to use Semar |
| [6. Check the whole product before a release](#scenario-6-check-the-whole-product-before-a-release) | You want to know that existing features still work |
| [7. A change with no code](#scenario-7-a-change-with-no-code) | The ticket turns on a flag or changes a permission |
| [8. Change a test, a rule, or a model](#scenario-8-change-a-test-a-rule-or-a-model) | You want Semar to test something else, or with another model. You edit YAML only |

Some steps need a later milestone. Each scenario says which.

| Milestone | What works |
| --- | --- |
| M1 | You run Semar on your machine against the API. The result is a local report |
| M1.1 | Pipeline jobs run Semar. The result also appears on the merge request and the Jira ticket |
| M2 | The web, with a Chrome profile of Semar's own |
| M3 | Android, on an emulator or a device |

## What must be ready

Semar needs the pieces below. `semar doctor` checks each one and says what the setup is ready for.

| Piece | What it is for | Needed |
| --- | --- | --- |
| The Semar binary | Everything. It starts its own background daemon | Always |
| An agent harness, signed in: Claude Code or Codex CLI | The planner and the tester. DeepSeek works through Claude Code | Always. One is enough |
| `.semar/semar.yaml` and `.semar/map.yaml` in the repository | Hosts, accounts, limits, and the feature map | Always |
| Read access to the git repositories a ticket can touch | Linking a ticket to its code changes | Always |
| Jira: a token, or an Atlassian MCP server | Reading tickets, and posting the result comment | Always for reading. M1.1 for the comment |
| UAT: hosts, the build probe of each service, and whether tests may change data | The entry check and every test | Always |
| Test accounts, and where the OTP comes from | Signing in. A second account and a lower role make the access checks possible | One account always. Two for access checks |
| Grafana: a read-only token, or a Grafana MCP server | Logs and traces for each step | Recommended. Without it the report says "logs and traces not checked" |
| A TypeSafe key | The pilot, which is the fast way through pages and screens, and the screener | Optional. Semar is slower without it |
| A second agent from another vendor, such as DeepSeek | The second reader of the plan | Optional |
| Read-only accounts for PostgreSQL, MySQL, or Elasticsearch | Checks that no API read can make | Optional |
| TestRail, team documents, other MCP servers | Earlier testing and team knowledge for the plan | Optional |
| Chrome, and one `semar login` per web profile | Web scenarios | For the web, M2 |
| `adb`, an emulator or a device, and the UAT build of the app | Android scenarios | For Android, M3 |
| A code host token and the pipeline templates | Plans and reports on merge requests, and runs with no command | For pipeline mode, M1.1 |

```text
$ semar doctor

Semar          daemon running, configuration and feature map found (12 features, 47 entry points)

Models
  planner        claude, Claude Code, signed in                      ready
  tester         codex, Codex CLI, signed in                         ready
  second reader  deepseek, through Claude Code                       ready      optional
  pilot          typesafe, jev-latest                                ready      optional
  screener       typesafe, jev-latest                                ready      optional

Connections
  Git            3 repositories readable                             ready
  Jira           through MCP server "atlassian": read, own comment   ready
  Grafana        read-only token, Loki and Tempo                     ready
  TestRail       not configured                                      skipped    optional
  Data stores    orders-db, PostgreSQL, account can only read        ready      optional
                 search, Elasticsearch, the key can write            REFUSED

UAT
  Environment    uat, tests may change data, 2 operations denied     ready
  Build probes   orders, catalog, web                                ready
  Accounts       support-agent, API                                  signed in
                 shop-web, web                                       expired: run semar login shop-web
                 second account for access checks                    missing: 2 access checks will not run
  Android        emulator-5554, shop-uat.apk                         ready

Ready for the API and Android. Not ready for the web: sign in. Access checks are limited.
```

## Scenario 1: your tickets are already on UAT

**You are** a developer. Three tickets are merged and deployed to UAT. You want them tested before the release.

**Before you start.** Semar is set up for the repository. If it is not, do [Scenario 5](#scenario-5-set-up-semar-for-a-repository) first.

### 1. Start the run

Run one command with the ticket keys:

```sh
semar verify --ticket SHOP-482 SHOP-490 SHOP-495
```

There are three other ways to start the same run:

* **Let Semar find the tickets.** `semar verify --new` takes every ticket that has new commits on UAT since the last build Semar tested.
* **Ask your own agent.** With the Semar plugin in Claude Code or Codex, say "Verify SHOP-482, SHOP-490, and SHOP-495 on UAT with Semar."
* **Run nothing.** In pipeline mode (M1.1) the job starts when the UAT deploy finishes.

### 2. Semar plans, tests, and checks

You do not act during this step. Semar follows the [test workflow](WORKFLOW.md):

| # | What happens | Who does it |
| --- | --- | --- |
| 1 | **Intake.** Find the tickets in Jira. Link each one to its merge requests and commits. Compare with everything else that changed on UAT | Code |
| 2 | **Risk.** Set a risk level for each ticket from what changed. The level sets how deep the testing goes | Code |
| 3 | **Plan.** Read the ticket, the deployed code, and what was tested before. Write the scenarios, the access checks, and the security plan | The planner, an agent |
| 4 | **Entry check.** Each merge is in the running build and was not reverted. The accounts are signed in. A few stored scenarios replay to show that UAT is healthy | Code |
| 5 | **Test session.** Work through each scenario on UAT, explore around the change, and test security. Every action is recorded | The tester, an agent, with the fast pilot on pages and screens |
| 6 | **Independent check.** Replay each script with no AI. Read Grafana, and the data stores where a check needs them. Compute each result | Code |
| 7 | **Triage.** Suggest a likely cause for each failure, and write a defect draft | Code, with the fast screener |
| 8 | **Report.** Write the report, the activity log, and one readiness line per ticket | Code |

```text
$ semar verify --ticket SHOP-482 SHOP-490 SHOP-495

Tickets    3 found in Jira
Changes    SHOP-482  2 merge requests in 2 repositories, 14 changed pieces
           SHOP-490  1 merge request, 4 changed pieces
           Also on UAT, not asked for: SHOP-501. With no ticket: 1 commit (a41c9d0)
Risk       SHOP-482  high: internal/payment changed, overlap with SHOP-490
           SHOP-490  standard
Plan       SHOP-482  3 targets, 6 scenarios (1 reused from regression), 4 access checks
           SHOP-490  1 target, 2 scenarios, 2 access checks
           3 items need a second look. 3 expected results came only from the code
Entry      SHOP-482  deployed, orders build 9f3c2ab
           SHOP-490  deployed, orders build 9f3c2ab
           SHOP-495  NOT deployed: catalog runs 51d0e7a and the merge is not in it
           UAT health check: 5 of 5 passed
Session    scenarios 8 of 8, explore 10 minutes, security 9 probe kinds
           58 actions, 9 writes. 3 orders created on UAT, 2 cleaned up
Replay     8 scenarios on build 9f3c2ab, no AI

Result     SHOP-482  3 passed, 1 failed, 2 inconclusive (waiting for an answer)
                     1 confirmed security finding, 2 suspected
           SHOP-490  1 passed, 1 inconclusive (waiting for an answer)
           SHOP-495  blocked: not deployed

Ready      SHOP-482  not ready for sign-off: 1 failed, 1 finding, 2 questions
           SHOP-490  not ready for sign-off: 1 question
Report     .semar/runs/r-20261114-0042/report.md
```

Semar does not wait for people. A scenario whose expected result came only from the code is tested anyway and stays `inconclusive` until someone confirms the expected result.

#### How Semar knows what changed in a ticket

A ticket holds no code, so Semar's code links the two before the agent starts. The plan shows the result as a table that you can check:

```text
SHOP-482 change set: 2 merge requests, 14 changed pieces, all deployed

Changed code                     Kind       Affects                          Scenarios   Note
orders  refund.go:30-58          behaviour  POST /v1/orders/{id}/refunds     1, 2, 3     OVERLAP with SHOP-490
orders  order.go:210             behaviour  GET /v1/orders/{id}              4           NOT IN TICKET: rounding
orders  refund_worker.go:88-104  behaviour  no entry point                   none        UNCOVERED
web     RefundForm.tsx:12-40     behaviour  /orders/:id/refund               5
orders  refund_test.go           test       -                                -
```

Read the notes first. They are the changes that need a second look:

* **Uncovered.** A behaviour change that no scenario exercises. Test it by hand, or tell Semar how to reach it.
* **Overlap.** Another ticket in the same deploy changed the same code. Each ticket can pass alone and still break the other.
* **Not in ticket.** The code changes something the ticket does not mention. Either the ticket is incomplete or the change does not belong.
* **High risk.** The change touches money, permissions, deletion, or a migration.

When the ticket has acceptance criteria, the plan has a second table: each criterion and the scenarios that test it. A criterion with no scenario is on the same second-look list.

To see the change table without running any test or any AI:

```sh
semar changes --ticket SHOP-482
semar changes --build
```

#### What Semar changes on UAT

Semar tests the way a QA engineer does, so it creates test data: an order, a refund attempt, a cancelled booking. Three limits apply:

* It works only through the application's API and screens, signed in as one of your test accounts.
* It never runs an operation on the environment's deny list, such as a real payment or a real SMS.
* Each scenario makes its own data and does not touch data that someone else made.

Semar only reads from the UAT databases and from Elasticsearch. It changes data through the application and nowhere else.

#### See what the agent did

Every action of the agent is in the activity log, written by Semar and not by the agent:

```text
$ semar activity r-20261114-0042 --ticket SHOP-482

 #  Time      Pass      Scenario     Action   Target                               Result                    Why (written by AI)
 1  10.06.02  setup     SHOP-482-01  sign in  support-agent                        signed in
 2  10.06.03  scenario  SHOP-482-01  create   POST /v1/orders                      201, order 81234          A paid order to refund
 3  10.06.04  scenario  SHOP-482-01  create   POST /v1/orders/81234/refunds        422 REFUND_EXCEEDS_TOTAL  Refund above the total
 4  10.06.04  scenario  SHOP-482-01  read     GET /v1/orders/81234                 200, refunded_total 0     Check the order is unchanged
 5  10.06.05  scenario  SHOP-482-01  query    orders-db refund_events              0 rows                    No settlement row is expected
...
41  10.09.40  security  -            read     GET /v1/orders/81190 as other user   200, order 81190          Try another user's order
```

Add `--writes` to see only what was created, changed, or deleted. Add `--follow` to watch a run in progress. Each request also carries the run ID in an `X-Semar-Run` header, so you can find the same run in your service's own logs.

### 3. Someone answers the yes or no questions

An expected result that came only from the code needs a person who did not write the change. A reviewer, a tech lead, a product owner, or a QA engineer answers. You, the author, cannot.

| Mode | Where the questions appear | How the person answers |
| --- | --- | --- |
| M1, local | In the terminal and in the report | `semar confirm`, which shows each question in turn |
| M1.1 | As a comment on a small merge request that holds the new scenario files | A reply such as `/semar yes 1 2` and `/semar no 3`, or an approval of that merge request |

A question looks like this:

```text
SHOP-482, scenario 2
A refund equal to the order total is still accepted. Yes or no?
Source: refund.go:42. The ticket does not say.
```

After the answers, Semar recomputes the results from the evidence it already has. It does not test again.

* A "yes" turns the waiting scenario into `passed` or `failed`, depending on what the evidence shows.
* A "no" means the agent read the code wrongly. The person writes the right expected result, and Semar runs that scenario again.

### 4. Read the result

The same result appears in up to four places.

| Where | What you see | From |
| --- | --- | --- |
| The terminal | One line per ticket with the count of each outcome, and the readiness line | M1 |
| The local report, `.semar/runs/<run-id>/report.md` | Failed, blocked, and inconclusive scenarios first, each with its evidence and a defect draft. Then security testing, the data left on UAT, and the QA notes that the agent wrote. Passes are collapsed | M1 |
| The merge request | The same report as one comment | M1.1 |
| The Jira ticket | One comment: the build, the outcomes, the targets tested, what was done in each scenario, what was not tested, and a link to the evidence | M1.1 |

The report ends with security testing, the data that the run left on UAT, and the readiness line:

```text
Security  9 probe kinds on 4 entry points, 212 requests
          1 confirmed: GET /v1/orders/{id} returns another user's order   FAILED
          2 suspected, for a person to check
          1 kind not run: request limits (turned off)
Data      3 orders created, 2 cleaned up, 1 still on UAT (order 81240)

SHOP-482  not ready for sign-off
  1 failed             Refund above the order total is rejected
  1 confirmed finding  GET /v1/orders/{id} returns another user's order
  2 questions open     expected results that came only from the code
```

To see the state of every ticket at any time (M1.1):

```text
$ semar status

SHOP-482  build 9f3c2ab  not ready   3 passed, 1 failed, 1 finding   waiting on: you (2 defects to fix)
SHOP-490  build 9f3c2ab  not ready   1 passed, 1 question            waiting on: the reviewer (1 answer)
SHOP-495  not deployed                                               waiting on: the catalog deploy
```

### 5. Act on each outcome

| Outcome | What it means | What you do |
| --- | --- | --- |
| `passed` | Every check held, the expected results are confirmed, and the build did not change | Nothing |
| `failed` | A check on a confirmed expected result was false, the service logged an error for this run, or a security finding was confirmed | Open the defect draft and fix the defect. See [Scenario 2](#scenario-2-a-scenario-failed) |
| `inconclusive` | Semar cannot decide. The reason is always given: an answer is missing, code cannot judge a screenshot, an element was not found, or a write timed out | Send the named decision to the person who can make it |
| `blocked` | The test could not run: no ticket, not deployed, UAT unhealthy, sign-in expired, or the environment does not allow the step | Fix the cause that the report names, then run again |
| `stale` | The build or its configuration changed during or after the run | Run again |
| `not_run` | Semar does not support that surface yet, or the scenario was left out | Test it by hand |

### 6. Sign off

When every ticket reads "ready for sign-off", the release owner signs off. The release owner can be a developer, a lead, or a product owner. A QA role is not required.

```text
/semar signoff release 2026.11.3
```

A sign-off names its builds. When a newer build is deployed to UAT, the sign-off stops applying, whether or not anyone tested again.

A ticket can be ready with an accepted gap, for example a criterion that cannot be tested on UAT. The person who accepts it gives a reason, and the gap stays in the report. A failed result cannot be accepted away.

### What is left behind

* The scenarios and scripts of each signed-off ticket join the regression suite. You do nothing for this.
* The script of each fixed security finding joins it too, so the defect cannot return unnoticed.
* The evidence stays in the evidence store, where a manager or a PM reads it through the roll-up.

## How you gain confidence in a result

A green line from an AI tool deserves doubt. Semar gives you three levels of checks, from one result to the tool as a whole.

### One result

For any `passed` or `failed`, you can check five things without trusting the agent:

| Question | Where the answer is |
| --- | --- |
| Where did the expected result come from? | Each one names its source: the ticket with the quoted sentence, a test case, a named person, or the code. Only a confirmed one can give `passed` |
| What ran? | The recorded request and response, or the screenshot, stored by Semar's recorder. The agent's own words are never evidence |
| Who decided the result? | Code computed it from the evidence. The agent cannot mark its work as passed |
| Does it happen again? | `semar run --scenario <id>` replays the script with no AI and must give the same result |
| On which build? | Every result names its build. A result for another build is `stale` |

### One ticket

The readiness line tells you what "tested" covered and what it did not:

* The two coverage tables show each acceptance criterion and each changed piece of code with the scenarios that tested it, and what has none.
* The second-look list names uncovered changes, overlaps with other tickets, and changes that the ticket does not mention.
* Security testing lists the probe kinds that ran, the kinds that did not, and why.
* The activity log shows every action and every record that the run created, changed, or deleted.

A report never says "everything works" or "secure". It says what was tested, what was found, and what was not tested.

### The tool itself

Trust in Semar is earned in stages, on your own tickets. The numbers below are proposals to revise after the first weeks.

| Stage | Semar's result is | Your current testing | Move to the next stage when |
| --- | --- | --- | --- |
| 1. Shadow | Advice. Nobody relies on it | Continues unchanged for every ticket | 30 tickets are compared, with no false pass on a confirmed expected result, and at most 1 false failure in 10 |
| 2. Assist | The first check. A person samples it | Only for high-risk tickets, and for anything that is not ready | Two releases pass with no false pass found by a sample or in production |
| 3. Rely | The check of record for low and standard risk | A person still reviews the evidence of high-risk tickets | A false pass sends the affected feature back to stage 2 |

Three mechanisms support the stages:

* **Mark a wrong result.** When your own testing or production finds a defect that Semar passed, mark the scenario as a false pass, with a reason. Mark a wrong failure the same way. The roll-up counts both.
* **A random sample.** For each release, Semar picks a few passed scenarios at random and asks a person to open their evidence. A sample that nobody chose finds problems that a favourable example hides.
* **Seeded defects.** Semar ships a sample application with known defects. Before each Semar release, the full workflow runs against it, and the release notes say how many defects it found.

### What a pass does not mean

* It does not mean the requirement was understood. A wrong expected result that a person confirmed will pass.
* It does not mean untested behaviour works. Read the coverage tables.
* It does not mean the system is secure. Ten probe kinds on the changed entry points are not a security review.

## Scenario 2: a scenario failed

**You are** the developer of `SHOP-482`. The report shows one failed scenario and one confirmed security finding.

1. **Open the defect draft.** It is in the report, and `semar defect SHOP-482-01` prints it:

	```text
	Title      Refund above the order total is accepted
	Build      orders 9f3c2ab on UAT. Ticket SHOP-482
	Steps      1. Sign in as support-agent
	           2. POST /v1/orders, a paid order for 100000
	           3. POST /v1/orders/{id}/refunds with amount 150000
	Expected   422 REFUND_EXCEEDS_TOTAL. Source: the ticket, "a refund above the order total must be rejected"
	Actual     201, refunded_total 150000
	Evidence   exchanges/SHOP-482-01/0003-refund-too-much.json, trace 4bf92f35
	Severity   High, suggested: the change is under internal/payment
	Cause      Likely the product. A hint
	```

	The steps come from the activity log, so they are what ran. The severity and the cause are suggestions.

2. **Reproduce it.** The script replays with no AI:

	```sh
	semar run --scenario SHOP-482-01
	```

3. **Fix the defect**, merge, and wait for the deploy.
4. **Test again.**

	```sh
	semar verify --ticket SHOP-482
	```

	Semar reuses the stored scripts, so this run calls no model for them and is fast. In pipeline mode it starts by itself after the deploy.

5. **Read the new result.** The Jira comment is edited in place and links the earlier run. An earlier sign-off no longer applies, because the build changed.

A confirmed security finding is handled the same way. Its script replays the request that showed the defect, and after the fix it joins the regression suite.

If you believe the test is wrong and the product is right, say so in a reply. A person who is not the author corrects the expected result. The agent cannot change it.

## Scenario 3: pipeline mode, from merge request to release

**You are** a developer on a team that installed the pipeline templates (M1.1). You run no Semar command.

| When | What happens | What you do |
| --- | --- | --- |
| You open a merge request that names a ticket | Semar posts the test plan as a comment: the risk level, the targets, the scenarios, earlier testing, and yes or no questions | Answer a question if one is addressed to you |
| The reviewer approves the merge request | The approval confirms the expected results in the plan | Nothing |
| The UAT deploy finishes | Semar runs the entry check, tests the tickets in that deploy, and posts the report on the merge request and the Jira ticket | Read the Jira comment. Fix a failure if there is one |
| Every ticket of the release is ready | The release owner replies `/semar signoff release 2026.11.3` | Nothing, unless you are the release owner |
| Every night | The regression suite replays with no model, and its report is published | Nothing, unless a scenario in your feature is newly failing |

Opening a merge request without a ticket gets one comment that asks for a ticket, and nothing else runs.

A pipeline runner has no person. It needs an API key for the agent, and a sign-in that needs no person. See [Sign in once for the web and Android](#sign-in-once-for-the-web-and-android-m2-m3).

## Scenario 4: you review and decide

**You are** a peer developer, a tech lead, a product owner, or a QA engineer. A release holds twelve tickets. Semar runs the tests.

1. **Answer the questions.** Each plan lists the expected results that came only from the code, and any proposed "must never" rule, as yes or no items. Results that quote the ticket or a test case are not asked again.
2. **Check the second-look list.** Uncovered changes, overlaps, and criteria with no scenario. Add a scenario by reply, or accept the gap with a reason.
3. **Triage the suspected findings.** The agent found something that code cannot check. Decide for each one: a defect, a new scenario, or noise.
4. **Review by exception.** Open `semar status` or the roll-up. Look at what failed, what is blocked, and what is inconclusive. Open the random sample of passes.
5. **Judge what code cannot.** An `inconclusive` with a screenshot is waiting for your eyes.

The time that used to go to running tests by hand goes to the questions, the gaps, and the findings.

### Teams without a QA engineer

Nothing in Semar needs a QA job title, a QA account, or TestRail. The decisions above go to people the team already has.

| Decision | Who can make it |
| --- | --- |
| Confirm a new expected result | Anyone who did not write the change: a reviewer, a lead, a product owner |
| Triage a suspected finding, or accept a gap | A lead, or the owner of the feature |
| Sign off a release | The release owner |

A developer who works alone can confirm their own expected results. Semar then labels them "confirmed by the author", and the report does not count that as an independent check.

## Scenario 5: set up Semar for a repository

**You are** the first person on the team to use Semar. You do this once per repository.

1. **Install** the Semar binary, and an agent harness if you have none: Claude Code or Codex CLI.
2. **Create the configuration.**

	```sh
	semar init
	```

	Semar detects the code host, OpenAPI files, and services, writes `.semar/semar.yaml`, and lists what it could not detect as questions.

3. **Describe UAT:** its hosts, the build probe of each service, whether tests may change data, and the operations that must never run, such as a real payment or a real SMS.
4. **Add the test accounts** and where the OTP comes from. A second account and an account with a lower role make the access checks possible.
5. **Choose a provider for each model role.** The planner and the tester need an agent: Claude Code, Codex CLI, or DeepSeek through Claude Code. The pilot and the screener are optional and use TypeSafe's fast model.
6. **Connect what the team already has.** Add the Jira and Grafana addresses, or import your MCP servers and list the tools that Semar may use:

	```sh
	semar mcp import .mcp.json
	semar mcp tools
	```

	Add read-only accounts for the data stores if you want database checks.

7. **Store the secrets** in the operating system keychain or in environment variables. They never go in the file. The one exception is a fixed OTP code for test accounts, which you can write in the file as `otp: { source: fixed, code: "123456" }`.
8. **Draft the feature map.**

	```sh
	semar map
	```

	The agent lists each feature with its URLs, paths, and screens. Correct the draft in a merge request, and add what must never happen in each feature. Plans, security testing, and regression coverage depend on it.

9. **Check the setup.**

	```sh
	semar doctor
	```

	Doctor names each missing piece with the exact setting to add, and says which surfaces the setup is ready for. It also reports what each credential is allowed to do and warns when one allows more than Semar needs.

10. **Commit** `.semar/semar.yaml` and `.semar/map.yaml`.

From here, Scenario 1 works for everyone who clones the repository and has test accounts.

### Sign in once for the web and Android (M2, M3)

Semar creates a new Chrome profile of its own and opens the browser on it. You do not choose or prepare a profile. Your own Chrome stays open and unchanged: Semar never uses your profile, never attaches to your browser, and never closes it.

```sh
semar login shop-web
```

1. A separate Chrome window opens on a new, empty profile named "Semar: shop-web".
2. You sign in with the test account, the way the application asks: its own form, or "Continue with Google" with Google's own pages and second step.
3. Semar sees that you are signed in and closes the window.

Later runs start Chrome on the same profile and are already signed in. When the session ends, the report says "the sign-in for shop-web has expired: run `semar login shop-web`", and you sign in once more.

Use a test account, not your own Google account. The agent acts as the account you sign in with.

| Your sign-in | What to configure |
| --- | --- |
| The application's own form with a static OTP | Scripted steps and `otp: { source: fixed, code: "123456" }`. Nobody signs in by hand |
| Google on the web | `semar login`, once. Google may block a browser that software controls, so Semar leaves Google's pages to you |
| Google on Android | `semar login shop-android`, once. You add the test Google account to the emulator, and the app then offers it |
| A pipeline runner | A test sign-in in the UAT build, because a runner has no person and no kept profile |

The agent never sees a password or a code. It asks Semar to fill a field by the secret's name, and Semar types the value.

## Scenario 6: check the whole product before a release

**You are** a release owner. You want to know that existing features still work, not only the changed ones.

```sh
semar regression run
```

In pipeline mode this runs on a schedule and you only read the report.

The report compares the run with the previous one and shows newly failing scenarios first. Every scenario has stored evidence, pass or fail. `semar regression coverage` shows which entry points have a scenario and which have none, so "the whole product" is a number you can read and not an assumption.

A regression run replays stored scripts. It calls no model and needs no AI provider.

A scenario that passes on a baseline alone is reported as "unchanged since the baseline". That means the behaviour did not change. It does not mean a person confirmed that the behaviour is correct.

## Scenario 7: a change with no code

**You are** a developer. Ticket `SHOP-500` turns on the feature flag `refund_v2` on UAT. No commit belongs to it.

Semar finds no code change and ends the ticket `blocked`, with the reason. You then declare the change:

```sh
semar verify --ticket SHOP-500 --no-code "flag refund_v2 turned on for UAT"
```

Semar plans from the ticket text and the feature map. Every expected result must come from the ticket, a test case, or a person, because there is no code change to read. The rest of the flow is the same.

## Scenario 8: change a test, a rule, or a model

**You are** a developer or a QA engineer. You want Semar to test something differently. You edit a YAML file. You never read or change Semar's code, and you do not rebuild or restart anything.

| You want to | Edit |
| --- | --- |
| Add or change a test | A file in `.semar/scenarios/` |
| Run one test with many inputs | `cases:` in the scenario |
| Share steps between tests | A file in `.semar/fragments/` |
| Use other test data on another environment | `.semar/data/<env>.yaml` |
| Add a rule that every response must meet | `policy.invariants` in `.semar/semar.yaml` |
| Say what must never happen in a feature | `rules:` in `.semar/map.yaml` |
| Add a security probe | A file in `.semar/probes/` |
| Change which model does which job | `providers`, `agents`, and `assign` in `.semar/semar.yaml` |
| Change who must confirm, or when a ticket is ready | `policy:` in `.semar/semar.yaml` |

**Run one test with three inputs.** Add a table of cases to the scenario:

```yaml
cases:
  - { name: one-below, amount: 99999,  status: 201 }
  - { name: equal,     amount: 100000, status: 201 }
  - { name: one-above, amount: 100001, status: 422 }
```

**Give low-risk tickets to a cheaper model, and run three testers at once.**

```yaml
agents:
  tester:       { provider: openai, parallel: 3 }
  tester-light: { provider: deepseek }
  fast:         { provider: typesafe }
assign:
  test:
    use: tester
    when:
      - { risk: low, use: tester-light }
  decide: fast        # every small yes or no question goes to the fast model first
```

**Check your edit before anything runs.**

```text
$ semar validate

.semar/scenarios/SHOP-482/SHOP-482-02.yaml:14  unknown key "expects". Did you mean "expect"?
.semar/semar.yaml:61                           agent "tester-lite" is not defined. Defined: tester, tester-light, fast
2 problems. Nothing was run.

$ semar validate --changed

.semar/fragments/paid-order.yaml changed. It affects 6 scenarios:
  SHOP-482-01, SHOP-482-02[one-below], SHOP-482-02[equal], SHOP-482-02[one-above], SHOP-490-01, SHOP-495-03
Their confirmations and scripts no longer apply.
```

`semar agents` shows which model does each job. `semar explain <setting>` says what a setting does and what it is set to. `semar new scenario` writes a starting file with a comment on every field.

Three things cannot be changed in YAML, on purpose: what `passed` means, Semar's fixed rules such as "no server error", and who may confirm an expected result. Those are the same in every company, so that a result can be trusted.

## What Semar never asks of you

* To write a test scenario from scratch.
* To collect evidence, take screenshots, or paste results into a ticket.
* To keep a list of what was tested, or of the test data left on UAT.
* To write the steps to reproduce a defect.
* To judge whether a passing test passed.
