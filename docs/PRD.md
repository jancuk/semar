# Semar product requirements

* **Status:** Draft 1 with updated product scope and no-QA workflow. The model-free core (Plan 01) is implemented.
* **Author:** azhar
* **Date:** 2026-11-14
* **Related:** [user scenarios](USER_SCENARIOS.md), [technical requirements](TRD.md), [test workflow](WORKFLOW.md), [workflow diagram](workflow.html)

**Scope (2026-11-14):** The scope, the workflow for teams without QA, and decisions D26 to D31 below set what Semar is for. No claim here says that Semar is the first or the only tool of its kind.

## Current scope

Initial product scope: **backend APIs, web, and native Android**. Next platform: **native iOS**, extending mobile support to Android and iOS. Android is not deferred to the later iOS phase. We interpret the repeated Android in the owner's priority statement as continued Android support when iOS is added.

| Target | First-scope capability | Initial support boundary |
| --- | --- | --- |
| Backend API | Authenticated REST/JSON behavior, schema and business checks, positive/negative/boundary cases, role/object access, asynchronous readback | Document supported protocols; GraphQL, gRPC, load testing, and arbitrary event systems are later extensions |
| Web | Real-browser user journeys, functional UI assertions, API-connected outcomes, action/network/screenshot evidence | Start with a declared Chromium matrix; other browsers and unsupported browser capabilities remain visible |
| Android | Native app install/launch on supported test devices, input/navigation, permissions, UI assertions, backend readback, screenshots and app logs | Start with a declared APK/emulator/device matrix; mobile-browser emulation does not count as native Android |
| iOS | Later: native iOS journeys on a separately validated runner/device matrix | Android results do not imply iOS results; unsupported iOS checks are explicitly not run |

These are support boundaries for delivery, not claims that every company uses all three surfaces. Select applicable surfaces per project and journey. A missing required Android artifact blocks that journey; an API-only service can declare Android not applicable with a reason.

Grafana and read-only database access are supporting evidence integrations. They are required only where a check depends on them. Jira is the initial work source; existing test management and a prebuilt knowledge base are optional. Start from approved requirements, developer explanations, API specifications, and previous evidence when there is no TestRail or QA-owned suite.

## Teams without dedicated QA

The primary workflow must work without a QA job title, QA account, or test-management installation. Verification responsibilities map to existing people: developer, peer developer, tech lead, product owner, or founder. QA remains an optional participant. Automation does not supply missing business intent or silently accept release risk.

| Situation | Decision path |
| --- | --- |
| Unchanged approved expectations, complete clean evidence, permitted low-risk scope | Reuse the approved checks; mark verified under the preconfigured policy without a new QA acceptance step. Normal release authorization still applies. |
| New or changed business rule | Ask a focused question in the existing issue/MR. An eligible peer developer, lead, or product owner confirms the expectation and source revision. |
| Unknown intent, incomplete evidence, flaky behavior, or high-risk change | Route to the named owner with evidence and the exact unresolved decision. Do not wait on a nonexistent QA role. |
| Solo developer with no independent reviewer | Allow diagnostic testing and author confirmation, labeled `author_confirmed`. Preserve factual check results, but do not call this independent intent validation. A permitted low-risk release requires explicit owner risk acceptance; high-risk policies may remain blocked. |
| Team has QA | QA can fulfill the same verification-owner role; it is not a separate mandatory gate on every run. |

Risk policy must define high-risk areas such as authorization, tenant boundaries, money movement, irreversible deletion, and migrations. A second AI agent is not an independent business approver. Existing approved assertions can be reused without repetitive questions; new generated translations are reviewed under the source-trust contract.

A new team can bootstrap critical journeys from its product descriptions and sandbox. Observed behavior is labeled a baseline, not automatically accepted as correct. Baselines and missing requirements remain visible until a responsible person decides.

## Positioning

Semar offers evidence-backed change verification across API, web, and Android for developer-led teams without dedicated QA. A pilot must show lower total human effort, useful defect detection, and acceptable maintenance and cost before broad delivery. "First," "only," and "no QA means no human judgment" are not supported claims.

## Summary

Semar has two aims. The first is to make a team depend less on QA: a change does not wait for a QA engineer to test it by hand, because the developer who made it gets it tested by an AI agent, with recorded evidence. The second is to give testing by an AI agent one standard: the same scenario format, the same labels for where an expected result comes from, the same recorded evidence, the same verdict rules, and the same report, whoever starts the run and whichever model does the work. A person still decides what the product must do and which risk to accept.

Semar is a planned open-source behavior-verification tool for developer-led teams, including teams without dedicated QA. It verifies changes in an authorized test environment and produces evidence tied to requirements and the exact deployed target. On UAT it creates and changes test data only through the application, signed in as a test account, as a QA engineer does. It never writes to a database directly.

Semar gives an AI agent the hands of a QA engineer. The agent can call the API as a person would in Postman, use the web app in a browser, use the Android app on an emulator or device, and read logs, traces, and dashboards in Grafana. Every action goes through Semar, which records it.

The agent is the tester, and it does four jobs. It drafts test scenarios from a code change. It works through each scenario and leaves a script behind. It explores around the change for defects that no scenario covers. It tests the security of what changed. Every action it takes is written to an activity log that a developer or a QA engineer can read. Semar replays the scripts with no model. Plain code decides each verdict. The agent never grades its own work.

Semar also keeps a regression suite. Each ticket that goes through Semar leaves its scenarios behind, and a regression run replays all of them, with evidence for every one and no model call.

Developers and reviewers are already busy, so Semar automates execution and evidence collection and asks people only for unresolved intent and risk decisions. Pipeline jobs can start runs; decisions stay in the existing issue/MR workflow. A dedicated QA engineer is not required.

Every change verification has a traceable work identity, initially a Jira ticket. Before drafting, the agent reads available requirements and prior testing. TestRail, team documents, and prior runs enrich this when present; their absence does not prevent onboarding. Missing business intent remains an explicit question. Publication writes are separately configured and authorized.

Semar reaches AI providers through agent harnesses that are installed on the machine. The agent acts on a surface through one tool layer, with brojev as the web engine.

## Problem

A developer-led team without QA must build features, choose checks, prepare data, run API/web/Android tests, investigate failures, and maintain the resulting scripts. Checks can be skipped or limited to the happy path. Teams with QA face a similar verification queue, but a QA department cannot be an adoption prerequisite.

Two things make the queue worse:

* Expected behavior is often incomplete. Someone with product knowledge must clarify it; the model cannot establish intent from code alone.
* There is no shared record of which scenarios were tested for a ticket. Managers and PMs cannot see it, and a later release cannot reuse it.

## Users

| User | What they need from Semar |
| --- | --- |
| Developer, including solo developer | Useful checks without writing a suite first, actionable evidence, reproducible failures, and visible uncertainty |
| Verification owner: peer developer, tech lead, product owner, or founder | Focused intent questions and exception review within existing tools, without needing a QA account |
| QA engineer, when present | The same scenario/evidence workflow, with optional deeper exploration and suite ownership |
| Merge request reviewer | A short list of expected results to confirm while reviewing the code |
| Manager or PM | A roll-up of what was tested per ticket, team, and service, and how each result was reached |
| Open-source adopter | A tool that works with their code host, ticket tracker, observability stack, and AI provider without code changes to Semar |

## Decisions this draft makes for you

You did not answer these directly, so this draft picks the option it judges best. Each one changes the requirements below if you reverse it.

| # | Decision | Reason | What reversing it costs |
| --- | --- | --- | --- |
| D1 | The agent writes a script once, a runner replays the script, and code decides the verdict | A pass from an agent that tests live on every run cannot be trusted or repeated | A pass depends on the agent's own report, and every re-test costs a model call |
| D2 | Backend APIs, web, and native Android form the first product scope; native iOS follows. Grafana is an evidence integration | Owner's current priority. Intermediate delivery may sequence API, web, then Android; completing API alone does not complete the scope | Changing scope requires an explicit product decision |
| D3 | Start in advisory/shadow mode against the team's actual verification process, even if developers perform it or checks are currently skipped | Reliability and saved effort must be measured without requiring a QA department | Unmeasured tool evidence becomes a release gate |
| D4 | A standalone OSS runner works locally/CI; managed cloud and private-runner operation are staged product goals | Supports both self-managed adoption and companies wanting managed infrastructure | Cloud requires tenant isolation, operations, and governance beyond the local runner |
| D5 | The first adapters are Jira for tickets, TestRail for test cases, Grafana with Loki and Tempo for logs and traces, and GitLab for merge requests | You named Jira, TestRail, and Grafana. GitLab is inferred from your wording ("merge request") and is not confirmed | None if GitLab is wrong. The adapters sit behind interfaces. Tell me your code host |
| D6 | Scenario drafting at merge request time is the recommended path. Drafting after the merge is also supported | Confirmation then happens inside code review and not on the release queue | Confirmation always waits until after deployment |
| D7 | Semar drives agent harnesses that are installed on the machine (Claude Code, Codex CLI, and any harness that speaks the Agent Client Protocol). Semar never calls a model vendor's API for the main agent and never handles the harness login | Your instruction. It also keeps credentials out of Semar | Semar must implement and maintain a client per model vendor |
| D8 | The agent's tools follow the brojev model: a long-lived daemon owns the connections, a thin MCP server exposes them, and each UI surface has three tiers of tools (a fast `run` for a whole sub-goal, step-by-step primitives, and a `batch` with no model) | Your instruction. The `batch` tier is also the replay, so "acts like a human" and "repeatable" fit in one design | The agent drives each surface one slow step at a time, and replay needs a separate mechanism |
| D9 | Semar ships two ways: as a plugin inside the user's own Claude Code or Codex session, and as an unattended command that spawns a harness | The plugin runs inside a session that the user started, and the command spawns a harness. One tool layer serves both | One of the two audiences is not served |
| D10 | The fast `run` tier needs a small, separate model (brojev uses Jev through OpenRouter). It is optional and off by default | It is the one place Semar holds a model key and sends page content to a second vendor | With it required, Semar cannot run on a harness login alone |
| D11 | Testing by hand ("exploring") produces findings for a person to triage. It never produces `passed` | A pass must come from a confirmed expected result and recorded evidence | A pass rests on the agent's opinion again |
| D12 | A feature map in the repository lists each feature, its URLs, paths, and screens, and its source files. The agent drafts it once and people correct it | It lets a plan name the exact targets a change affects, and it makes "all features" a number for regression | Each plan guesses the affected URLs from the diff alone, and regression coverage cannot be measured |
| D13 | The regression suite fills by itself: a signed-off ticket's scenarios join it. For features no ticket has touched, Semar records current behaviour as a baseline | No one has time to write a suite by hand | A suite that someone must write and maintain |
| D14 | A baseline pass is reported as "unchanged since the baseline", not as "correct" | Recorded behaviour includes the defects the system has today | A regression report that claims more than it knows |
| D15 | Decisions use existing issue/MR roles; unchanged approved assertions are reused. New/changed business assertions need source-appropriate confirmation; no dedicated QA acceptance is mandatory | Owner's no-QA-team use case and the source-trust review | Excessive repeated questions remove the time saving; absent intent cannot be automated away |
| D16 | The UAT database is an evidence source of last resort. Semar only reads from it, through an account that the database itself limits to reading | Your instruction. A defect in Semar then cannot change UAT data | An ordinary account, with safety resting on Semar's own checks |
| D17 | The ticket is the unit of work. A change with no ticket is not planned and not run | Your instruction. It also settles where a result is posted and how the roll-up groups | Results with no home, and a roll-up that cannot group |
| D18 | Semar's code posts the result comment on the Jira ticket, built from the recorded actions. The agent does not write it and holds no Jira credential | You asked for the agent to post it. Built by code, the comment says what ran, and text hidden in a page or a ticket cannot make the agent post to Jira | A comment that reports what the agent says it did |
| D19 | The team's knowledge sources are read-only and are presented to the agent as one search. Each source stays the system of record | Your instruction to consolidate. Reading in place avoids a second copy that goes stale | A synced copy that Semar must keep fresh and secure |
| D20 | A tool from an MCP server reaches the agent only when configuration lists it by name | An MCP server can expose tools that write | A misconfigured server lets the agent change another system |
| D21 | The agent can propose a memory note. A person approves it before it joins the knowledge base | A note persists into every later run, so untrusted text must not write itself in | Memory that the agent fills unchecked |
| D22 | An expected result taken from a TestRail case needs no further confirmation by default | QA wrote the case. Semar checks that the quoted text exists in it | QA confirms again what QA already wrote |
| D23 | Code works out which code changes belong to a ticket, and shows how it found each one. The agent does not decide this | A wrong or missing link is then visible and the same every time | The agent could leave a change out and nobody would see it |
| D24 | Semar also looks at everything that changed on UAT since the last verified build, and compares that with the tickets | Code reaches UAT with no ticket, and two tickets change the same function. A per-ticket view shows neither | Untracked changes and clashes between tickets go untested |
| D25 | Every changed piece of behaviour has a scenario, or appears on a double-check list with the reason | The number of scenarios says nothing about whether the riskiest change was tested | A plan that looks thorough and skips the change that matters |
| D26 | On UAT, Semar creates and changes test data through the application's own API and screens, signed in as a test account. It never writes to a database directly. Each environment states whether tests may change data | Your decision of 2026-11-14. A read-only UAT cannot test a login or a refund | Semar can only read, and most scenarios cannot run |
| D27 | Each environment lists operations that never run, such as a real payment or a real SMS | A test account can still reach a third party | A test sends money or messages outside the test environment |
| D28 | The agent cannot label an expected result as written by a person or as an invariant. Code assigns both | Those two labels need no confirmation, so they must be out of the agent's reach | An agent's guess counts as a confirmed result |
| D29 | Every plan includes access checks: no sign-in, another user's object, and a lower role. The report says what was checked and never says that a change is secure | You asked that a change be checked for security problems. A change that works for the wrong user is a defect | Access defects are found only when someone thinks to test for them |
| D30 | A ticket with no code change, such as a flag or a permission change, can be verified when a person declares the change | Not every change is a commit | Such tickets stay blocked |
| D31 | Each scenario still ends in one outcome, and every reason that applies is listed with it | One word is what people read. The review proposed six separate result dimensions | A longer report that fewer people read |
| D32 | The agent is the tester. In one session per ticket it works through the scenarios, explores, and tests security, as a QA engineer does. Code still computes each outcome from what was recorded | Your instruction that the agent acts as QA. An agent that grades its own work is not trusted | With the agent grading itself, a pass rests on the agent's word |
| D33 | Semar writes an activity log: one entry for every visit, read, create, update, delete, click, and query, with the agent's reason. The recorder writes it, not the agent | Your instruction. A developer or a QA engineer must be able to check what was done on UAT | Nobody can tell what the agent did, or which test data it left behind |
| D34 | The agent tests security with a fixed list of probe kinds. A finding counts as a failure only when code can confirm it from recorded actions | Your instruction that the agent tests security. A fixed list lets a person read what the agent may do | Free probing with no list, or security findings that are opinions |
| D35 | Every direct connection to a data store is read-only: PostgreSQL, MySQL, and Elasticsearch | Your instruction. Semar changes UAT data through the application only | A defect in Semar, or a bad query, could change UAT data directly |
| D36 | Semar is written from scratch and copies no third-party code | This keeps the license choice (Apache-2.0) free | Semar would have to carry the license of the code it copied |
| D37 | Semar opens the browser on a Chrome profile of its own, never the one a developer browses with. A person signs in there once, and later runs reuse the session | Your instruction. The developer's own accounts stay out of the agent's reach | The agent acts inside the developer's personal browser, or someone signs in before every run |
| D38 | Sign-in with Google is done by a person, once, in an ordinary window. A pipeline uses a test sign-in in the UAT build. Semar does not work around Google's check on automated browsers | Google may block a browser that software controls | A sign-in that breaks whenever Google tightens its checks, on an account that may then be locked |
| D39 | The agent refers to a secret by name and Semar types it. A fixed OTP code for test accounts may be written in the configuration file | Your instruction for a static OTP. Product rule 6 still holds | Passwords in the agent's context, or a static code that needs a keychain entry on every machine |
| D40 | Models work in roles: a planner, a tester, an optional second reader from another vendor, a pilot, and a screener. Each role names its provider in configuration | Different jobs need different models, and no part of Semar is tied to one vendor | One model does every job, at one speed and one price |
| D41 | The agent roles run on harnesses: Claude Code for Anthropic, Codex CLI for OpenAI, and DeepSeek through Claude Code's compatible endpoint or OpenCode | Your instruction to add DeepSeek. Decision D7 stays as it is | A client to build and maintain per vendor |
| D42 | TypeSafe's Jev is a decision model, not an agent. Semar uses it for the pilot and for screening: fast choices and scores, each with a probability | TypeSafe's documentation says that Jev "does not generate text, write code, or hold a conversation" | Listing it as a third agent provider would not work |
| D43 | A decision model never decides an outcome. A choice below the confidence limit goes back to the tester or to a person. Semar still works with no decision model, more slowly | The same rule as for the agent. You said that some parts may depend on TypeSafe "if needed", so a team can mark a role as required | A fast model's guess becomes a pass or a fail |
| D44 | Semar reads existing MCP configuration files. Every tool stays denied until a person lists it, with its use and whether it writes | Your instruction to integrate MCP configurations. Teams already run MCP servers for Jira and Grafana | Each connection is set up twice, or every tool of a server reaches the agent |
| D45 | Testing follows one workflow with fixed gates: risk sets the depth, an entry check comes before the session, code checks the result independently, and exit criteria give a readiness line per ticket | Your request for a workflow that fits proper QA and security testing | Depth, order, and "done" depend on what the agent chooses in each run |
| D46 | Each feature keeps a short list of things that must never happen, confirmed by a person once. Security probes check them on every later ticket | A scanner does not know business rules, and a person should not answer the same question on every ticket | Abuse of a business rule is tested only when the agent thinks of it |
| D47 | Trust in Semar is earned in three stages on the team's own tickets: shadow, assist, and rely. A person can mark any result as wrong, and each release gets a random sample of passes to check | A tool that says "passed" must show how often it is wrong | Nobody can say when Semar's result may replace a person's test |
| D48 | Semar has a fixed core, company policy in configuration, and an adapter for every outside system. Outcomes and verdict rules are the same for every company | Your instruction that other companies can adopt the framework. A result must mean the same thing in two companies | Each adopter forks Semar to fit its tracker, its code host, and its habits |
| D49 | Every use case is a file under `.semar/`: scenarios, shared steps, test data, checks, company rules, security probes, testing passes, agents, and policy. Code holds mechanisms only | Your instruction that developers and QA change YAML and never need the Go code | Each new test idea waits for someone who can change Semar |
| D50 | Models are organised as named agents. Jobs are assigned to agents in configuration, with backups, parallel sessions, and review by several agents at once | Your instruction | One fixed set of roles, and one model for everything |
| D51 | Where a job is a choice among fixed options, the fast decision provider is asked first, and a company can add its own questions | Your instruction to use the cheap, fast model wherever it fits | Agent turns are spent on small choices |
| D52 | Configuration can add rules. It cannot turn off a fixed rule, change what an outcome means, or let an agent confirm its own expected result | The freedom of D49 and D50 must not weaken a result | A "passed" that depends on each company's settings |

## Goals

The first two goals are the reason Semar exists. The numbered goals after them say how.

* **Depend less on QA.** Manual verification by a QA engineer is no longer the step that every change waits for. Developers verify their own changes with an agent, and QA, where a team has it, spends its time on intent, risk, and exploration.
* **One standard for testing with an AI agent.** Every run follows the same workflow and produces the same kind of scenario, evidence, verdict, and report, so that results can be trusted, repeated, and compared across people, teams, and models.

1. Shorten the time between "the build is on UAT" and "the evidence is accepted" for a batch of tickets, by testing tickets in parallel.
2. Produce test scenarios when the ticket is blank.
3. Make a "passed" result trustworthy: recorded by the tool, computed by code, tied to a named build, and labelled with where each expected result came from.
4. Give every failure a recorded request and response, or a screenshot, plus a script that reproduces it.
5. Leave behind reusable regression scripts owned by the development team, with QA participation when available.
6. Give managers and PMs a roll-up that shows outcomes, not only counts.
7. Work with more than one AI provider through one adapter contract.
8. Let the agent test the way a QA engineer does, across the API, the web app, the Android app, and Grafana, with every action recorded.
9. Find defects that no scenario covers, by exploring around the change.
10. Show, before anything runs, which URLs, paths, and screens a change affects and will be tested, with the reason for each.
11. Test the existing features, not only the change, with evidence for every scenario.
12. Ask each person for decisions only. Nobody starts a run, collects evidence, or keeps a list of what was tested.
13. Verify state that no API exposes by reading the UAT database, without any risk of changing it.
14. Use what the team already knows: earlier tickets, existing test cases, past results, and team documents, through one search.
15. Leave a record on the ticket of what was tested and what was done, with no one writing it by hand.
16. Show, for each ticket, exactly which code changes belong to it, which of them a scenario covers, and which need a second look.
17. Check that a change does not work for the wrong user, and say plainly which access checks ran.
18. Let a developer or a QA engineer see every action the agent took on UAT, in order, and every record it created, changed, or deleted.
19. Have the agent test the security of what changed, the way a QA engineer would, and report what it tried.
20. Fit a company with other tools: another tracker, another code host, another observability stack, with configuration and no change to Semar's code.
21. Let developers and QA change tests, rules, and models by editing YAML, with no knowledge of Semar's code.
22. Let a team choose which model does which job, run agents in parallel, and use a cheap, fast model wherever a job is a simple choice.

## Non-goals

* Semar does not detect a misunderstood requirement when the expected behaviour is written nowhere outside the code. A person must supply or confirm it.
* Semar does not replace unit tests, integration tests in CI, or load tests.
* Semar does not run against production. It refuses any environment that is not declared as a test environment.
* The standalone runner needs no hosted account. Managed cloud is a separate staged delivery, with explicit tenant/security/operations gates.
* Native iOS is outside the initial API/web/Android scope and follows it; native Android is included from the first product scope.
* Semar does not claim exhaustive interaction coverage across a release. Declared critical cross-service and cross-surface journeys are in scope; untested combinations remain explicit.
* Semar does not manage test devices or a device farm.
* Semar does not let the agent's opinion produce a "passed".
* Semar is not a security scanner and not a penetration test. The agent tests the security of the changed entry points with a fixed list of probe kinds. It runs no automated scan and no load test.
* Semar does not change a ticket's status or fields, and does not create, edit, or delete TestRail cases. It adds its own comment and, when turned on, test results.
* Semar is not a knowledge base of record. Jira, TestRail, and the team's documents stay where they are.

## Product rules

These rules hold in every milestone. A feature that breaks one is out of scope.

1. **The tool records the evidence.** Only an action that goes through Semar's recorder counts as evidence. In an unattended run the agent has no other path to the test environment.
2. **Code computes the verdict.** The same evidence always gives the same verdict.
3. **Every expected result names its source:** the ticket, an existing test case, a person, the diff, a baseline, or an invariant such as "no server error".
4. **Expected behavior has authenticated provenance and policy-appropriate review.** Reuse unchanged approved checks. New/changed business assertions need an eligible independent reviewer, who may be a developer, lead, or product owner. A quote alone does not validate its executable translation. Solo author confirmation is explicitly labeled and does not count as independent validation; release handling follows the no-QA policy above. Registered invariants are assigned by trusted code, not by the model.
5. **Every result names the build it ran against.** A result for a different build is stale.
6. **The agent never sees a password, a token, or an OTP.**
7. **Secrets and personal data are removed before evidence is stored or shown to the model.**
8. **Semar never writes to Grafana or to a data store.** It reads from Grafana, PostgreSQL, MySQL, and Elasticsearch, and each account itself must be unable to write.
9. **Recommendations from the AI are advice.** They are labelled as written by AI and never change a verdict.
10. **Knowledge is data, not instructions.** Text from a ticket, a test case, a document, or a memory note can inform a plan. It cannot change a verdict or a permission.
11. **Code does every write to another system.** The agent holds no credential for Jira, TestRail, the code host, Grafana, or a database.
12. **A test changes the test environment only through the application.** Semar acts as a test account, only where the environment allows changes, and never runs an operation on the environment's deny list.
13. **Every action on the test environment is in the activity log.** The recorder writes each entry. The agent supplies only its reason, which is labelled as written by AI.

## Workflow

[The test workflow](WORKFLOW.md) describes each phase, who acts in it, and why it is there. [The diagram](workflow.html) shows the same flow. In short:

1. **Intake.** Code links each ticket to its code changes and to the build on UAT. A change with no ticket stops here.
2. **Risk and depth.** Code sets a risk level from the change. The level sets how deep the testing goes.
3. **Plan.** The planner writes the test plan: scenarios, access checks, the security plan, and two coverage tables. Code validates it.
4. **Confirm intent.** People answer yes or no questions about new expected results. Nothing waits for them.
5. **Entry check.** Code checks the deploy, the sign-in, and the health of UAT before any model is called for the session.
6. **Test session.** The tester works through the scenarios, explores, and tests security. Every action is recorded.
7. **Independent check.** Code replays the scripts with no model, reads logs, traces, and data stores, and computes each outcome.
8. **Triage.** Likely causes, merged findings, and a defect draft for each failure. A person decides suspected findings.
9. **Report and readiness.** The report, the activity log, the ticket comment, and one readiness line per ticket.
10. **Fix and re-test.** The stored scripts replay on the new build.
11. **Release.** A regression run, the sign-off, and the roll-up.
12. **Learn.** Scenarios join the regression suite, and confirmed rules stay with their feature.

## Functional requirements

Delivery sequence: **M1** is an API pilot, **M1.1** adds shared team operation, **M2** adds web, and **M3** adds native Android and completes the initial surface scope. **M4** adds native iOS. These are implementation increments, not a reduction of the first product scope. All applicable surfaces and at least one connected journey must meet the common evidence contract before claiming initial scope complete.

### Team adoption without QA

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-130 | Setup and verification work without a QA account, TestRail, or an existing automation suite | M1 |
| FR-131 | Configure verification and release responsibilities by capability; peer developers, leads, and product owners may fulfill them | M1 local; authenticated shared roles in M1.1 |
| FR-132 | Reuse approved unchanged expectations without repeated confirmation; show only unresolved decisions | M1 |
| FR-133 | Solo-team mode records author confirmation separately from independent review; it cannot silently satisfy high-risk separation-of-duty policy | M1 |
| FR-134 | A journey may cross API, web, and Android with shared resource IDs, fixture ownership, and target manifest | M3, with API/web journeys in M2 |
| FR-135 | Each project declares applicable surfaces; distinguish not applicable, unsupported, blocked, and verified | M1 onward |
| FR-136 | Native Android evidence comes from the installed app and declared emulator/device, not a mobile-sized browser | M3 |
| FR-137 | Add native iOS using the same requirement/evidence contract and separately validated platform capabilities | M4 |
| FR-138 | Measure total developer/reviewer setup, execution, triage, confirmation, and maintenance time, including skipped verification in the baseline | M0 onward |
| FR-139 | A low-risk automatic assessment requires complete evidence and previously approved policy/expectations; no model can approve its own new business rule | M1.1 |

### Act like a QA engineer

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-42 | The agent can send API requests and read the responses, as a person does in Postman | M1 |
| FR-43 | The agent can query Grafana logs, traces, and metrics, and view a dashboard panel. Semar only reads from Grafana | M1 |
| FR-44 | The agent can use the web app in a real browser: read the page, click, type, select, upload, handle dialogs, and take screenshots | M2 |
| FR-45 | The agent can use the Android app on an emulator or device: install the build under test, launch it, read the screen, tap, type, swipe, and take screenshots. Semar captures the app's device log | M3 |
| FR-46 | On the web and Android, the agent can hand a whole sub-goal to a fast `run` tool, act one step at a time, or send a batch of known steps | M2, M3 |
| FR-47 | A signed-in session, an open browser, and a running app persist between the agent's actions and between agent sessions | M1 |
| FR-48 | Every action on any surface is recorded with its result. A script is built only from actions that were executed | M1 |
| FR-49 | The agent can explore around a change within a time limit and an action limit, and record findings. A finding carries the recorded actions that show it | M1 for API, then per surface |
| FR-50 | Semar works as a plugin inside the user's own Claude Code or Codex session, and as an unattended command | M1 |
| FR-51 | The fast `run` tier is optional. Semar works without it and without any model key of its own | M2 |

### Plan

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-1 | Given a merge request, Semar collects the diff, the merge request description, and the linked ticket text | M1 |
| FR-2 | Semar drafts a behaviour statement in the form "before X, now Y" for a person to confirm | M1 |
| FR-3 | Semar drafts scenarios. Each scenario has steps, and each step has expected results | M1 |
| FR-4 | Each expected result carries a source: `ticket`, `testcase`, `person`, `diff`, or `baseline`. The agent can submit every source except `person`, which code assigns when a signed-in person writes the result. Invariants are a fixed list in Semar's code | M1 |
| FR-5 | Semar lists changed areas that no scenario covers, and scenarios that cannot run with the reason | M1 |
| FR-6 | Scenarios are stored as files in the service repository so that a merge request can carry them | M1 |
| FR-7 | Semar can plan from a local branch before a merge request exists | M1 |
| FR-53 | Semar keeps a feature map in the repository: each feature, its API routes, web pages, Android screens, and background jobs, and its source files. The agent drafts it and people correct it | M1 |
| FR-54 | For a change, Semar lists each affected entry point with the code path from the changed line to it, and marks the reach as direct, indirect, or unknown | M1 |
| FR-55 | The test plan lists every URL, path, and screen that will be tested, why each is affected, and which scenarios cover it | M1 |
| FR-56 | Semar checks the plan with code: every cited line exists in the diff, and every listed path exists in the feature map or an OpenAPI file. A path it cannot find is flagged, not hidden | M1 |
| FR-57 | A change that cannot be traced to an entry point is listed as "could not be traced", with the features that use the changed file | M1 |
| FR-58 | For each entry point the agent drafts scenarios for the changed behaviour, the boundary, a refused input, and neighbouring behaviour that should not have changed | M1 |
| FR-59 | The plan includes recommendations from the AI: the risk of the change with the reason, questions only a person can answer, checks to do by hand, regression scenarios to rerun, test data to prepare, and areas Semar cannot test | M1 |
| FR-60 | The plan keeps at most 8 scenarios per change by default, ranked by risk, and lists the rest as "not selected". A scenario for a high-risk change and an access check are never cut | M1 |
| FR-61 | An expected result that came from the ticket quotes the ticket sentence. Semar checks that the sentence exists in the ticket | M1 |

### Confirm

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-8 | A person who is not the author of the change confirms the expected results. Approval of the merge request that contains the scenario files counts as confirmation | M1.1 |
| FR-9 | A person can also confirm a scenario with a command, for scenarios drafted after the merge | M1.1 |
| FR-10 | A confirmation covers the behaviour statement and the expected results. When either changes, the confirmation lapses | M1.1 |
| FR-11 | In M1, before the shared record exists, Semar records a local confirmation and marks it "local, unverified identity" in the report | M1 |

### Gate, sign in, and set up

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-12 | Semar reads the running build of each service from a configured probe and refuses to run when the merged commit is not in that build. It also checks that the change was not reverted and that every replica runs the same build | M1 |
| FR-13 | Login and OTP steps are defined in configuration. Supported OTP sources are a fixed code, a TOTP secret, an HTTP lookup, a local command, and a prompt to a person | M1 |
| FR-14 | Secrets come from environment variables or the operating system keychain, never from the configuration file. The one exception is a fixed OTP code for test accounts | M1 |
| FR-15 | Semar refuses to send a request to a host that the environment does not list | M1 |

### Author and run

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-16 | For each scenario, the agent writes a script by making recorded requests through Semar. The agent cannot change the expected results | M1 |
| FR-17 | Semar replays a script without a model call. The replay is the run of record | M1 |
| FR-18 | Semar reuses a stored script when the scenario's expected results are unchanged | M1 |
| FR-19 | API scenarios replay over HTTP | M1 |
| FR-20 | Web scenarios replay in a browser and capture screenshots | M2 |
| FR-21 | Android scenarios replay on an emulator or device and capture screenshots | M3 |
| FR-52 | When a web or Android script no longer finds an element, the scenario ends `inconclusive`, because either the product changed or the script is out of date. The agent can propose a repair. A repair cannot change an expected result or a step, and a person approves it when the element's role or name changed | M2 |
| FR-22 | Several tickets can run at the same time on one machine | M1 |

### Observe

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-23 | Semar sends a trace ID with every request and reads logs and traces for that ID from Grafana | M1 |
| FR-24 | When a service does not carry the trace ID, Semar falls back to a time window, labels the finding "not attributed to this run", and does not fail the scenario on it | M1 |
| FR-25 | Semar only reads from the observability stack | M1 |

### Verdict and report

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-26 | Each scenario ends in one outcome: `passed`, `failed`, `blocked`, `inconclusive`, `not_run`, or `stale` | M1 |
| FR-27 | Each outcome lists its reasons in plain English | M1 |
| FR-28 | A failure includes the recorded request and response, or a screenshot, and the script that reproduces it | M1 |
| FR-29 | The report shows failed, blocked, and inconclusive scenarios first and collapses the passes | M1 |
| FR-30 | The report states which model, provider, and harness version wrote each script | M1 |
| FR-31 | Semar publishes the report to the merge request and stores the evidence in the shared record | M1.1 |

### Accept, sign off, and roll up

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-32 | The configured verification owner records acceptance when policy requires human review; QA is optional | M1.1 |
| FR-33 | The authorized release owner signs off under team policy. The decision names the target manifest and scenario versions; no QA job title is required | M1.1 |
| FR-34 | An acceptance or sign-off lapses as soon as the build or the recorded configuration on UAT differs from the one it names, whether or not a newer run exists | M1.1 |
| FR-35 | The roll-up lists, per ticket, team, and service: the build, the count of each outcome, how many expected results came only from the diff, and who confirmed, accepted, and signed | M1.1 |

### Providers

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-36 | Semar drives Claude Code and Codex CLI through one adapter contract | M1 |
| FR-37 | Semar drives any harness that speaks the Agent Client Protocol, which covers Gemini CLI and OpenCode | M1.1 |
| FR-38 | A user can configure several provider instances, each with its own binary path, environment variables, and models. An instance that points Claude Code at an Anthropic-compatible endpoint reaches vendors such as DeepSeek | M1 |
| FR-39 | A user can choose a different provider instance and model for planning and for script writing | M1 |
| FR-40 | `semar doctor` reports whether each provider is installed, its version, and whether it is signed in, without starting a model turn | M1 |
| FR-41 | Semar works with a harness that is signed in with a subscription and with a harness that uses an API key | M1 |

### From a ticket to its code changes

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-115 | For a ticket, Semar finds every merge request and commit that belongs to it, in every repository the configuration lists | M1 from git history. M1.1 from the code host and Jira links |
| FR-116 | The plan shows the change set: repositories, merge requests, commits, the number of changed pieces by kind, and how each link was found | M1 |
| FR-117 | Semar reports the deploy state of each part of a ticket. A ticket that is partly deployed says which part is missing | M1 |
| FR-118 | The agent reads the code as it runs on UAT, not as it was when the merge request was open | M1 |
| FR-119 | Semar lists everything that changed on UAT since the last verified build, grouped by ticket | M1 |
| FR-120 | Semar reports tickets that are on UAT and that nobody asked to verify, and commits that name no ticket | M1 |
| FR-121 | Semar flags two tickets in the same deploy that changed the same lines or the same function | M1 |
| FR-122 | Each scenario cites the code changes that motivate it. For each changed piece of behaviour, Semar reports the scenarios that cover it | M1 |
| FR-123 | A changed piece of behaviour with no scenario is flagged as uncovered, with the reason | M1 |
| FR-124 | The plan opens with a double-check list: uncovered changes, high-risk changes, overlaps with other tickets, changes the ticket does not mention, and changes with no ticket | M1 |
| FR-125 | Semar reports behaviour that the ticket describes and for which it finds no code change | M1 |
| FR-126 | A change marked as a refactor still gets a scenario that checks behaviour did not change | M1 |
| FR-127 | `semar verify --new` chooses the tickets itself: every ticket with new commits on UAT since the last verified build | M1 |
| FR-128 | When Semar finds no code change for a ticket, the outcome is `blocked`. A person can then declare a change that has no code, such as a flag, and Semar plans from the ticket text and the feature map | M1 |
| FR-129 | When one merge request serves several tickets, the agent proposes which change belongs to which ticket and a person confirms | M1.1 |

### Regression

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-62 | Semar keeps a regression suite in the repository, grouped by feature | M1 |
| FR-63 | When a run is signed off, its scenarios and scripts join the suite with no action from a person | M1.1 |
| FR-64 | For entry points with no scenario, Semar drafts baseline scenarios that record what the system does now | M1.1 |
| FR-65 | A baseline pass is reported as "unchanged since the baseline" and counted apart from other passes. QA can confirm baselines in bulk, one feature at a time | M1.1 |
| FR-66 | A regression run replays stored scripts. It calls no model and needs no AI provider | M1 |
| FR-67 | Every scenario in a regression run stores evidence, whether it passes or fails | M1 |
| FR-68 | Semar reports coverage per feature: entry points with a scenario, entry points with none, and entry points it cannot reach | M1 |
| FR-69 | A regression run can cover the whole suite, one feature, or only the scenarios that a change affects | M1 |
| FR-70 | The regression report compares a run with the previous one: newly failing first, then still failing, needs repair, newly passing, and passing | M1.1 |
| FR-71 | Semar can import the requests and status checks of an existing Postman collection as scenarios | M1.1 |
| FR-72 | Each team chooses how long to keep regression evidence | M1.1 |

### Low effort

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-73 | Semar provides pipeline templates: plan when a merge request opens, verify when a UAT deploy finishes, and regression on a schedule. No person starts these | M1.1 |
| FR-74 | A person confirms, accepts, and signs off by approving the merge request or replying to a Semar comment. Nobody needs Semar installed to decide | M1.1 |
| FR-75 | Only expected results that came from the code alone or from a baseline need confirmation. Each is asked as a yes or no question | M1 |
| FR-76 | A person can accept or sign off every ticket in a release with one reply | M1.1 |
| FR-77 | `semar init` detects the code host, OpenAPI files, and services, and lists what it could not detect as questions | M1 |
| FR-78 | `semar doctor` names each missing piece of setup with the exact setting to add | M1 |
| FR-79 | `semar status` shows, per ticket, what is waiting and on whom | M1.1 |
| FR-80 | Semar mentions a person only when that person's decision is needed | M1.1 |

### Database evidence

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-81 | A scenario can check state in the UAT database when no API read can verify it | M1 |
| FR-82 | A database check must state why no API can verify the result. Semar warns when an OpenAPI file shows a read endpoint that returns the same field | M1 |
| FR-83 | Semar only reads from a database. It requires an account limited to reading, runs every query in a read-only transaction, accepts a single `SELECT` statement, and reads only listed tables | M1 |
| FR-84 | `semar doctor` reports whether the database account can write. When it can, Semar refuses every database check | M1 |
| FR-85 | The query, its parameters, and the returned rows are stored as evidence, limited in rows and redacted | M1 |
| FR-86 | A database check can wait a set time for a row to appear | M1 |
| FR-87 | Each report counts how many checks used the database | M1 |
| FR-88 | The agent never receives a database connection string | M1 |

### Tickets and result comments

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-89 | Every change must link to a ticket. Semar finds the ticket key in the merge request title, the description, the branch name, or the commit messages, and checks that the ticket exists | M1 |
| FR-90 | When a change has no ticket, Semar does not plan and does not run. It says so on the merge request, and the outcome is `blocked` | M1 |
| FR-91 | After a run, Semar posts one comment on the Jira ticket: the build, the outcomes, the targets tested, what was done in each scenario in plain steps, what was not tested, which earlier test cases were used, who confirmed and accepted, and a link to the evidence | M1.1 |
| FR-92 | A later run edits the same comment and links the earlier runs. A team can choose a new comment per run | M1.1 |
| FR-93 | Code builds the comment from recorded actions. A summary written by AI is optional and labelled | M1.1 |
| FR-94 | The comment carries no request or response body | M1.1 |
| FR-95 | Semar's access to the tracker is limited to reading tickets and writing its own comment on a linked ticket | M1.1 |
| FR-96 | A ticket with several merge requests gets one combined result | M1.1 |
| FR-97 | A regression run needs no ticket. A team can name a release ticket to receive its report | M1.1 |

### Knowledge and earlier testing

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-98 | A team lists its knowledge sources in configuration: Jira, TestRail, a folder of documents, Semar's own past runs, and any MCP server | M1 for Jira, files, and Semar's own history. M1.1 for the rest |
| FR-99 | Semar presents every source to the agent as one search. Each result shows its source, a link, and its date | M1 |
| FR-100 | During planning, the agent looks up earlier testing for each affected feature: test cases, earlier tickets and their test notes, past scenarios, and past results | M1 |
| FR-101 | The plan lists the earlier testing it found and, for each item, whether the plan reuses it, adapts it, or sets it aside, with the reason | M1 |
| FR-102 | A scenario that already exists in the regression suite is selected for rerun and is not drafted again | M1 |
| FR-103 | The plan lists gaps: existing test cases for the feature that no scenario covers | M1.1 |
| FR-104 | An expected result taken from an existing test case names the case. Semar checks that the case exists and contains the quoted text | M1.1 |
| FR-105 | Semar only reads from knowledge sources | M1 |
| FR-106 | A tool from an MCP server reaches the agent only when configuration lists it by name. `semar doctor` lists each server's tools so that a person can choose | M1.1 |
| FR-107 | The MCP servers a team configures are available to the agent in an interactive session and in an unattended job, with the same limits | M1.1 |
| FR-108 | The agent can propose a memory note for later runs. A person approves it before it joins the knowledge base | M1.1 |
| FR-109 | A team can mark a source "do not send to the model" | M1 |

### TestRail

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-110 | Semar reads TestRail cases, runs, and results for the sections that the feature map links to a feature | M1.1 |
| FR-111 | A scenario can link to TestRail case IDs | M1.1 |
| FR-112 | When turned on, Semar records each run in TestRail: one test run, and a result with an evidence link for each linked case. It is off by default | M1.1 |
| FR-113 | `inconclusive`, `blocked`, and `stale` never map to a passing TestRail status | M1.1 |
| FR-114 | Semar never creates, edits, or deletes a TestRail case. It lists scenarios with no linked case as "cases QA may want to add" | M1.1 |

### What a test may change

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-140 | Each environment states whether tests may change data or only read. There is no default | M1 |
| FR-141 | Semar changes data on the test environment only through the application's API and screens, signed in as a configured test account | M1 |
| FR-142 | Each environment lists operations that never run. Semar refuses them in authoring, replay, and exploring | M1 |
| FR-143 | On a read-only environment, a scenario that needs a write ends `blocked` and sends no request | M1 |
| FR-144 | A write that times out is not sent again. The scenario ends `inconclusive` with "effect unknown" | M1 |
| FR-145 | Semar follows no redirect to an unlisted host, and blocks requests to unlisted hosts inside the browser | M1 for the API, M2 for the web |

### Checks tied to actions

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-146 | Every step with an expected result is bound to one recorded action. On the API, that request matches the step's method and path | M1 |
| FR-147 | A scenario with no check besides the invariants cannot end `passed`. A step with no recorded result ends the scenario `inconclusive` | M1 |
| FR-148 | A false check on an unconfirmed expected result ends `inconclusive`, not `failed` | M1 |
| FR-149 | When logs and traces could not be read, the report says "not checked". A team can make that case `inconclusive` | M1 |
| FR-150 | A step that expects a refusal does not fail on an error log line for that request. The line is reported | M1 |
| FR-151 | A result lists every reason that applies, not only the first | M1 |
| FR-152 | The build identity can include a hash of a service's flags or configuration. When a service exposes none, the report says "flags not recorded" | M1 |

### Access checks

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-153 | For each affected entry point that needs sign-in, the plan adds access checks: no sign-in, another user's object, and a lower role | M1 for the API, then per surface |
| FR-154 | An access check on a write also reads the object again and checks that it did not change | M1 |
| FR-155 | An access check that cannot run, for example with no second test account, is listed as not run in the plan and in the report | M1 |
| FR-156 | The report states the number of access checks run, the findings, and the checks not run. It never states that a change is secure | M1 |
| FR-157 | Semar reports a recorded response that contains a value matching its secret patterns | M1 |

### Pipeline reliability and suite upkeep

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-158 | A run is keyed by ticket, builds, and plan revision. A repeated deploy event starts no second run | M1.1 |
| FR-159 | Scenarios that run in parallel use different test accounts of the same profile | M1.1 |
| FR-160 | An older run never replaces the result comment of a newer one | M1.1 |
| FR-161 | A reply such as `/semar yes 1 2` applies to one plan revision. Semar rejects it when the plan changed | M1.1 |
| FR-162 | A regression scenario that is `inconclusive` three runs in a row is quarantined. It stays in the report and does not count as a pass | M1.1 |
| FR-163 | A promoted scenario replaces an existing one for the same entry point and steps | M1.1 |

### The agent as tester

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-164 | In one session per ticket the agent works through the scenarios, explores around the change, and tests security. `semar verify` runs all three passes, and each can be skipped | M1 for the API, then per surface |
| FR-165 | The agent writes QA notes at the end: what it tested, what it could not test, and what worries it. The report labels them as written by AI, and they never change an outcome | M1 |
| FR-166 | A finding can become a scenario when a person accepts it | M1.1 |

### Activity log

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-167 | Semar writes one activity entry per action: the time, the actor, the ticket, the scenario and step, the action (visit, read, create, update, delete, click, type, query, and so on), the target, the record touched, the result, and the agent's reason | M1 |
| FR-168 | The recorder writes the entry. The agent supplies only its reason, which is labelled as written by AI | M1 |
| FR-169 | Semar lists every record that a run created, changed, or deleted on UAT, with the scenario that did it and whether it was cleaned up | M1 |
| FR-170 | `semar activity` prints the log, can show writes only, and can follow a run in progress | M1 |
| FR-171 | The report shows the activity per scenario. The Jira comment gives the counts and a link | M1. M1.1 for the comment |
| FR-172 | Every API request carries a Semar user agent and the run ID in a header, so that a developer can find the run in the service's own logs | M1 |
| FR-173 | Every write belongs to a scenario step, an exploration, or a security probe, and the entry says which | M1 |

### Security testing by the agent

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-174 | The agent tests the changed entry points with a fixed list of probe kinds: another user's object, a function above the role, sign-in handling, fields the client should not set, input handling, too much data, business rules, error text, browser protections, and request limits | M1 for the API. M2 for browser protections |
| FR-175 | A security finding is confirmed only when code can check it from recorded actions. A confirmed finding counts as a failed scenario. Any other finding is suspected and goes to a person | M1 |
| FR-176 | A probe changes only records that the run created, respects the deny list, and stays inside a request budget per entry point | M1 |
| FR-177 | The request-limit probe is off by default | M1 |
| FR-178 | The report states the probe kinds run, the entry points, the request count, the confirmed and suspected findings, and the kinds not run with the reason | M1 |
| FR-179 | A confirmed security finding leaves a script that joins the regression suite | M1.1 |

### Data stores

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-180 | Semar reads PostgreSQL, MySQL, and Elasticsearch or OpenSearch. Every direct connection to a data store is read-only | M1 for one SQL engine. M1.1 for the rest |
| FR-181 | On Elasticsearch, Semar sends only a search, a count, or a read by ID, on listed indices, and refuses a request body that contains a script | M1.1 |
| FR-182 | `semar doctor` reads the privileges of each store account. Semar refuses every check on a store whose account can write | M1 |

### Testing practices built in

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-183 | When a ticket has acceptance criteria, the plan numbers them and shows which scenarios test each one. A criterion with no scenario is on the double-check list | M1 |
| FR-184 | `semar bundle` writes everything about one scenario into one folder for a developer | M1 |
| FR-185 | Each failed or inconclusive result carries a likely cause, labelled as a hint | M1 |
| FR-186 | Semar exits with 0 when every scenario passed, 1 when one failed, and 2 when nothing failed but a scenario could not be judged or no scenario ran | M1 |
| FR-187 | A team can list known failures, each with a ticket and an expiry date. A listed scenario that passes is reported | M1.1 |
| FR-188 | Semar exports results as JUnit XML | M1.1 |
| FR-189 | Semar imports Gherkin feature files as scenarios | M1.1 |
| FR-190 | `semar ui` shows runs, evidence, and the live activity log on a local page | M1.1 |
| FR-191 | Semar posts newly failing regression scenarios to a webhook | M1.1 |
| FR-192 | On the web, an uncaught script error and a 5xx from a background request each break an invariant. Console and network logs are stored per scenario | M2 |
| FR-193 | The agent can run an accessibility check on a page. The results are findings | M2 |

### Signing in on the web and Android

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-194 | Semar creates a new Chrome profile of its own for each web auth profile and runs a separate Chrome process on it. It never uses, attaches to, or closes the developer's own Chrome, and no setting points it at an existing profile | M2 |
| FR-195 | `semar login <profile>` opens that profile for a person to sign in once. Later runs reuse the session and ask nobody to sign in | M2 |
| FR-196 | Before a run, Semar checks that each session is still signed in. An ended session that no scripted step can restore ends the scenarios `blocked` and names the command to run | M2 |
| FR-197 | Sign-in with Google works by a person signing in once in the dedicated profile. A test sign-in in the UAT build and scripted Google pages are the other two routes | M2 |
| FR-198 | When Google asks for a step that the configuration does not cover, Semar stops and reports that the sign-in needs a person. It does not work around Google's checks | M2 |
| FR-199 | A sign-in on a page or a screen can be scripted in configuration: fields, buttons, secrets by name, and the OTP | M2 for the web, M3 for Android |
| FR-200 | The agent refers to a secret by name. Semar types the value, and only into an input field on a host that the auth profile lists | M2 |
| FR-201 | A fixed OTP code can be written in the configuration file. Semar fills an OTP field from the profile's OTP source, including a field that is split into one box per digit | M1 for the API, M2 for the web, M3 for Android |
| FR-202 | Semar stores no screenshot and no snapshot of a Google sign-in page | M2 |
| FR-203 | On Android, sign-in with Google uses a test Google account that a person adds to the emulator once. Semar never types a Google password on a device | M3 |
| FR-204 | `semar logout <profile>` deletes a profile's stored session. The profile directory is never copied to the evidence store | M2 |
| FR-205 | `semar doctor` shows, for each auth profile, whether it is signed in and how old the session is | M1 for the API, M2 for the web |

### Models and providers

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-206 | Each model role names its provider in configuration: planner, tester, second reader, pilot, and screener | M1. M2 for the pilot |
| FR-207 | DeepSeek works as an agent provider through the compatible endpoint of an installed harness | M1 |
| FR-208 | Semar calls a decision model for the pilot and for screening. The first is TypeSafe's Jev, reached directly or through OpenRouter | M2 for the pilot. M1.1 for screening |
| FR-209 | A decision carries a probability. Below the configured limit it goes back to the tester or to a person. A decision model never sets an outcome | M1.1 |
| FR-210 | Semar works with no decision model configured | M1 |
| FR-211 | Each provider lists the kinds of data it may receive. Semar refuses a job that would send another kind | M1 |
| FR-212 | An optional second reader from another vendor reads the plan and proposes missing scenarios as advice | M1.1 |

### MCP configurations

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-213 | `semar mcp import` reads server definitions from existing MCP configuration files. Every imported tool stays denied until a person lists it | M1.1 |
| FR-214 | Each listed MCP tool states its use (knowledge, observe, tracker, OTP, or surface) and whether it writes | M1.1 |
| FR-215 | The tracker and the observability connection can each run through an MCP server in place of Semar's own adapter. Code makes those calls, not the agent | M1.1 |
| FR-216 | An MCP tool that writes is recorded in the activity log as a write and obeys the environment's limits | M1.1 |

### Test workflow

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-217 | Code sets a risk level per ticket from the change. The level sets the depth: the number of scenarios, the explore time, the security level, and who must confirm. The agent can raise the level and cannot lower it | M1 |
| FR-218 | Before a session, Semar runs a health check of a few stored scenarios with no model. When it fails, the tickets end `blocked` and no model is called | M1 |
| FR-219 | Each feature keeps "must never" rules that a person confirms once. Security probes check them on every later ticket for that feature | M1 |
| FR-220 | Semar builds most security probes as variations of a recorded request, sends them with no model, and checks them with code | M1 |
| FR-221 | Each failed scenario and each confirmed finding gets a defect draft: steps to reproduce from the activity log, the expected result with its source, the actual result, evidence, the build, and a suggested severity | M1 |
| FR-222 | Each ticket gets a readiness line computed from exit criteria, with every open item listed | M1 |
| FR-223 | A person can accept a gap with a reason. An accepted gap is shown as accepted and never turns a failed result into a passed one | M1.1 |

### Confidence in the results

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-224 | `semar doctor` ends with what the setup is ready for, per surface, and names what is missing for the rest | M1 |
| FR-225 | A person can mark a result as a false pass or a false failure, with a reason. The roll-up counts both per feature and per release | M1 locally. M1.1 by reply |
| FR-226 | For each release, Semar picks a few passed scenarios at random for a person to check, and records who checked them | M1.1 |
| FR-227 | The roll-up shows the stage of trust of each feature and the numbers behind it: tickets compared, false passes, and false failures | M1.1 |

### Adoption by other companies

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-228 | Every outside system is reached through an adapter that configuration selects: the tracker, the code host, observability, test management, data stores, secrets, OTP sources, model providers, the evidence store, and notifications | M1 onward, with each system's plan |
| FR-229 | A company can add an adapter as a command in any language that reads one JSON request and writes one JSON response. No change to Semar is needed | M1 |
| FR-230 | A company can serve an adapter through an MCP server that it already runs, by mapping tools in configuration | M1.1 |
| FR-231 | Company policy is configuration: ticket key patterns, where a key may appear, risk rules, test depth, which sources need confirmation, exit criteria, and who may confirm, accept, and sign off | M1 |
| FR-232 | `semar init --preset` writes a starting policy: light, standard, or strict | M1 |
| FR-233 | A missing optional system never stops a run. The feature that needs it is skipped, and the report says what was not checked | M1 |
| FR-234 | Tickets can be read from files in the repository, so that a company can try Semar before it grants access to its tracker | M1 |
| FR-235 | Each kind of adapter has a conformance test kit that an adapter author can run | M1 |

### Agents and assignments

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-236 | A team defines agents in configuration: a name, a description, a provider and a model, options, backups, an instruction file, limits, and parallel sessions | M1 |
| FR-237 | A team assigns each job to an agent in configuration. A job with no agent of its own uses the agent of the more general job | M1 |
| FR-238 | An assignment can depend on the risk level, the surface, or the feature | M1.1 |
| FR-239 | A provider is added in configuration: a harness, a compatible endpoint, an ACP command, or a decision interface. Presets fill in known vendors | M1 |
| FR-240 | An agent lists backups. Semar moves to the next on a provider failure, in order, and never to a provider that is not listed | M1.1 |
| FR-241 | An agent can run several sessions at once, each with its own test account. The number is limited by the free accounts and by policy | M1.1 |
| FR-242 | Several agents can review one plan at once, and none sees another's answer | M1.1 |
| FR-243 | An agent from another vendor can check each script against its scenario. An objection becomes a question for a person | M1.1 |
| FR-244 | Where a job is a choice among fixed options, Semar asks the decision provider first. A team can add its own questions at fixed points. No answer sets an outcome | M1.1 |
| FR-245 | `semar agents` prints which agent, provider, and model does each job, with its backups and its fallback | M1 |

### Configuration over code

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-246 | A change to a scenario, a rule, a probe, an agent, or policy needs an edit to files under `.semar/` only. No rebuild and no restart follows | M1 |
| FR-247 | A scenario can run with a table of cases | M1 |
| FR-248 | Scenarios can share steps through fragments | M1 |
| FR-249 | Test data can differ per environment through named data sets | M1 |
| FR-250 | `semar validate --changed` lists the scenarios that a changed file affects. Their confirmations and scripts lapse | M1 |
| FR-251 | A company can add rules that every response must meet. They are reported apart from Semar's fixed rules, and a broken one fails the scenario | M1 |
| FR-252 | A company can add security probes in YAML, with conditions that code checks | M1.1 |
| FR-253 | Semar's own probes ship in the same format and can be printed and copied | M1.1 |
| FR-254 | A company can add a testing pass in configuration: its aim, its agent, its surfaces, and its limits. A pass gives findings only | M1.1 |
| FR-255 | Every file kind has a published schema, so that an editor completes and checks it | M1 |
| FR-256 | `semar validate` checks every file and reports all problems, each with the file, the line, and a suggestion. Every command validates before it runs | M1 |
| FR-257 | `semar explain` prints what a setting does, its allowed values, its default, and its current value | M1 |
| FR-258 | `semar new` writes a commented starting file for a scenario, a fragment, a probe, or a pass | M1 |
| FR-259 | An agent can get extra guidance from a file, and a prompt can be replaced. The report says when a prompt was replaced | M1.1 |
| FR-260 | No setting can turn off a fixed rule, change a verdict rule, or let an agent label an expected result as confirmed | M1 |

## Non-functional requirements

| ID | Requirement |
| --- | --- |
| NFR-1 | **Repeatable.** Replaying one script against one build gives the same verdict, apart from real changes in the environment |
| NFR-2 | **Safe by construction.** Limits on hosts, methods, and agent tools are enforced by code, not by instructions to the model |
| NFR-3 | **One binary.** Semar installs as a single Go binary for macOS and Linux. Windows is not a target in version 1. The only other programs it needs are an agent harness, `git`, Chrome for the web, and `adb` for Android |
| NFR-4 | **No server.** Version 1 needs no service that the adopter must operate |
| NFR-5 | **Visible data flow.** Semar can print what it will send to the model before it sends it |
| NFR-6 | **Bounded cost.** Each agent job has a turn limit and a time limit. The report shows token use per job where the harness reports it |
| NFR-7 | **Pinned harness versions.** Semar states which harness versions it is tested with and warns on others |
| NFR-8 | **Plain evidence.** Evidence is stored as JSON, YAML, Markdown, and image files that a person can read without Semar |
| NFR-9 | **Small touch time.** Proposed targets, to revise after the baseline: a reviewer or QA engineer spends at most 2 minutes per ticket on yes or no items, and a developer runs no Semar command in pipeline mode |
| NFR-10 | **Read-only by enforcement.** For Grafana and for databases, the limit on writing is enforced by the credential itself, not only by Semar's code |

## Success measures

There is no baseline today. Milestone 0 measures one. The thresholds below are proposals to revise once the baseline exists.

| Measure | How it is measured | Proposed threshold |
| --- | --- | --- |
| Time from "build on UAT" to "evidence accepted", per ticket | Timestamps in the run record | Lower than the manual baseline |
| Human minutes per ticket for confirmation, evidence review, and triage | Self-reported during shadow mode | Lower than the manual QA minutes per ticket |
| False passes | A defect that manual QA or production finds in a scenario that Semar marked `passed` | Zero during shadow mode before any ticket skips manual QA |
| False failures | `failed` results that triage marks "not a bug" | At most 1 in 10 |
| Scenarios that end `inconclusive` or `blocked` | Count per run | Falling release over release |
| Expected results that came only from the diff | Count per run | Falling release over release |
| Commands a developer or QA engineer runs per ticket | Counted in pipeline mode | Zero |
| Yes or no items per ticket | Count per plan | Falling release over release |
| Regression coverage | Entry points with a scenario, out of all entry points in the feature map | Rising release over release |
| Regression scenarios that rest on an unconfirmed baseline | Count per run | Falling release over release |
| Checks that used the database | Count per run | Low and stable. A rise means services lack read endpoints |
| Changed pieces of behaviour with no scenario | Count per ticket | Falling release over release, each with a stated reason |
| Commits on UAT that name no ticket | Count per deploy | Zero |

**Stop criterion (proposal).** After 30 tickets in shadow mode, stop or rethink if either of these holds:

* Human minutes per ticket with Semar are equal to or higher than the manual baseline.
* A false pass occurs on a scenario whose expected results were confirmed, and the cause cannot be fixed in the verdict rules.

### Stages of trust

The thresholds are proposals to revise after the first weeks.

| Stage | Semar's result is | The team's current testing | Move to the next stage when |
| --- | --- | --- | --- |
| 1. Shadow | Advice. Nobody relies on it | Continues unchanged for every ticket | 30 tickets are compared, with no false pass on a confirmed expected result, and at most 1 false failure in 10 |
| 2. Assist | The first check. A person samples it | Only for high-risk tickets, and for anything that is not ready | Two releases pass with no false pass found by a sample or in production |
| 3. Rely | The check of record for low and standard risk | A person still reviews the evidence of high-risk tickets | A false pass sends the affected feature back to stage 2 |

## Risks

| Risk | Severity | Mitigation in this plan |
| --- | --- | --- |
| The expected result is wrong because it came from the code under test | High | Source labels, confirmation by a non-author, `inconclusive` for unconfirmed results. The limit in [Non-goals](#non-goals) remains |
| Vendor terms restrict subscription logins in third-party tools | High | Semar runs the user's own unmodified harness and never touches its credentials. API keys work from the first release. The reading of the terms is not confirmed by the vendors. See the technical requirements |
| Harness command-line flags and output formats change | Medium | Version pinning, a contract test per provider driver, and a small adapter per harness |
| Shared UAT: other testers' data and errors | Medium | Trace-ID filtering, reserved accounts per user, weak findings never fail a scenario |
| Services do not carry a trace ID from the caller | Medium | Fallback to a labelled time window. The observability check is then advisory |
| Code, ticket text, and UAT data leave the company for a model vendor | High | Redaction before the model sees data, a preview of outgoing data, and a per-project switch for ticket text. Company policy must allow it |
| Human time moves and does not shrink | High | Shadow mode, measured minutes, and the stop criterion |
| Evidence stored in git cannot be removed once a secret is pushed | Medium | Redaction before storage, synthetic test accounts, and a protected evidence branch |
| Android blocks emulators or pins certificates | Medium | Deferred to M3 with its own investigation |
| Four surfaces are four engines to build and maintain | High | Fixed build order, one recorder and one verdict engine shared by all, and reuse of brojev's engine for the web |
| A web or Android script breaks when the screen changes | Medium | Scripts find elements by role and name, not by position. A broken script is `inconclusive`, and the agent can repair it |
| The web engine depends on brojev, a young project with one author | Medium | Three integration routes, decided by a spike. The MIT license allows a copy as the fallback |
| No Android engine exists yet, and the fast `run` tier is proven on web pages only | High | A spike before M3 is planned. Android can ship with step-by-step tools first |
| The fast `run` tier sends page content to a second model vendor | Medium | Off by default, listed in the outgoing-data preview, password fields masked |
| An interactive session lets the user's own agent act outside Semar | Low | Only recorded actions count as evidence, and the run of record is the model-free replay |
| The AI names a URL or a code line that does not exist | Medium | Code checks every cited line and every listed path, and flags what it cannot find |
| The AI misses an affected endpoint, most likely through a shared function | High | The feature map, "could not be traced" listed openly, and regression scenarios for features that use the changed file |
| A baseline records today's defects as expected behaviour | High | The "unchanged since the baseline" label, separate counts, and bulk confirmation by QA |
| "All features" is read as full coverage | Medium | The coverage report shows entry points with no scenario and entry points that cannot be reached |
| Fewer confirmations let a wrong ticket sentence pass unchallenged | Medium | Semar checks that the quote exists. A team can require confirmation of ticket results too |
| A database check writes to UAT | High | An account that can only read and a read-only transaction, both enforced by the database, then Semar's own statement check |
| A database check returns personal data | High | A column deny list, a row limit, and redaction. Someone must keep the deny list current |
| Database checks become the easy path and tie tests to table layout | Medium | A required reason, a warning when an API read exists, and a count in every report |
| Pipeline jobs that plan or write scripts need a paid API key | Medium | Regression and replay need no model. Planning can stay on a person's machine with a subscription login |
| An old test case or ticket describes behaviour that has since changed, and the agent follows it | High | Every item shows its date. The agent must give a decision per item, and a person sees the decision in the plan |
| Semar places an earlier ticket or test case in the wrong feature | Medium | Three placement rules, the plan shows what was found, and a spike measures placement on your data |
| A document or ticket contains instructions aimed at the agent | High | Knowledge is data. In an unattended job the agent has no tool that could act on such text |
| An MCP server exposes a tool that writes | High | Deny by default. Only tools listed by name reach the agent |
| A wrong memory note misleads every later run | Medium | A person approves each note in a merge request |
| Result comments flood a ticket | Low | One comment per ticket, edited in place |
| A Jira or TestRail credential allows more than Semar needs | Medium | `semar doctor` reports what each credential allows and warns on excess |
| Company knowledge is sent to a model vendor | High | A per-source "do not send to the model" switch, redaction, and the outgoing-data preview |
| A commit or merge request does not carry the ticket key, so Semar cannot link it | High | The build view lists it under "changes with no ticket". A spike counts how often this happens in your history |
| The agent calls a behaviour change a refactor | Medium | A refactor still gets a scenario that checks behaviour did not change, and stays in the change table |
| Two tickets each pass alone and break each other | High | Overlap detection on lines and functions, and a double-check flag |
| A ticket spans a repository that Semar was not given | Medium | "Described in the ticket, not found in the code" shows the gap |
| A test on UAT triggers a real payment, SMS, or email | High | The environment's deny list, test accounts only, and a required setting for what tests may change. The team must keep the deny list current |
| Test data piles up on UAT | Low | Each scenario cleans up where the API allows, and marks its data with the run ID where it can |
| A check passes on the wrong request | High | Each step is bound to one recorded action that matches its method and path. The report shows the step text beside the recorded request |
| UAT lacks the accounts that access checks need | Medium | The check is listed as not run. Open question 26 asks for the accounts |
| A security probe locks a test account or leaves odd data on UAT | Medium | A probe changes only records that the run created. Each entry point has a request budget. The request-limit probe is off by default |
| A team reads "no confirmed findings" as "secure" | High | The report lists what was tried and what was not, and never says secure |
| Three passes per ticket cost more model use and more time | Medium | Limits on actions and minutes per pass, and a flag to skip a pass. Re-tests and regression replay with no model |
| Milestone 1 holds more than a small team can build quickly | High | The technical requirements mark what can move to M1.1. The first slice should be one API ticket from plan to report |
| Google refuses the browser, or ends the session | High | A person signs in once in an ordinary window. Pipelines use a test sign-in in the UAT build. Semar reports `blocked` and never works around the check |
| A developer signs in with a personal Google account | High | `semar login` says to use a test account. Semar cannot check it |
| The stored Chrome profile holds a live session | Medium | It stays on the machine with access for the user only, is never stored as evidence, and `semar logout` deletes it |
| TypeSafe changes its interface or its price, or stops serving Jev | Medium | The pilot and the screener are optional, and each job has a fallback. The driver is small |
| A team's policy does not allow a vendor to receive code or UAT data | High | Each provider lists the kinds of data it may receive, and Semar refuses a job that would send another kind |
| The risk rules give ordinary depth to a dangerous change | Medium | The planner can raise the level, a person can set it, and the plan states the level and the reason at the top |
| "Ready for sign-off" is read as "correct" | High | The line lists what was tested and every accepted gap. A wrong expected result that a person confirmed still passes |
| Adapters multiply and go stale | Medium | Few built-in adapters, a conformance kit per kind, and the command and MCP routes for everything else |
| A company's policy weakens the result until "ready" means little | High | Outcomes and verdict rules are not policy. "Nothing failed" and "nothing is undecided" cannot be turned off |
| A typo in a YAML file silently changes a test | High | Strict files, a schema per file kind, and `semar validate` before every command, with the file and the line |
| A shared fragment is edited and many tests change at once | Medium | `semar validate --changed` names every affected scenario, and their confirmations lapse |
| More agents and panels cost more model use | Medium | Every optional job is off until assigned. The report shows usage per agent. Re-tests and regression use no model |

## Open questions

Answer these before Milestone 1 starts. The first three change the design.

1. Which code host do you use? Decision D5 assumes GitLab.
2. May diffs, ticket text, and UAT responses be sent to a model vendor, and does UAT hold real customer data?
3. Do your services accept a `traceparent` header from the caller and write its trace ID to logs and traces?
4. How does each UAT service expose its running commit: a version endpoint, a deploy record in the code host, or something else?
5. Where does the OTP come from on UAT: a fixed code, a bypass, a readable inbox, or a person?
6. Which test accounts may Semar use on UAT, and which operations must it never run there, such as a real payment or a real SMS? You decided on 2026-11-14 that Semar may change UAT data with test accounts.
7. Which existing team roles may confirm intent, review exceptions, and authorize a release? Which high-risk decisions require independent review, and what is permitted for solo teams?
8. Where do QA's minutes per ticket go today, by stage and by surface?
9. Which license do you want? Apache-2.0 and MIT are the usual choices for a Go tool.
10. Can the brojev author export its browser engine as importable Go packages? If not, Semar drives the brojev daemon or carries a copy.
11. Is there an Android build that points at UAT, and does it run on an emulator?
12. May page content go to a second model vendor for the fast `run` tier, or must Semar run without it?
13. Which data stores does UAT use (PostgreSQL, MySQL, Elasticsearch), and can each get an account limited to reading, ideally on a replica?
14. Which tables may Semar read, and which columns hold personal data?
15. Does your pipeline already know when a UAT deploy finishes, so that it can start Semar?
16. May a pipeline job use a paid API key for planning and script writing, or must those stay on a person's machine?
17. Do you have Postman collections that QA uses today? They are the fastest start for the regression suite.
18. Is your Jira the cloud edition or self-hosted, and the same for TestRail? The interfaces differ.
19. How are TestRail sections organised: by feature, by squad, or by release? Semar needs a link from a section to a feature.
20. Which documents and memory sources should the agent read, and may their text be sent to a model vendor?
21. Should Semar record its runs in TestRail, or is the Jira comment enough?
22. May Semar hold a Jira credential that can write comments, and who issues it?
23. How do you link code to tickets today: the branch name, the merge request title, commit messages, or Jira's development panel?
24. Do you squash commits on merge?
25. Which repositories can one ticket touch, and may Semar read all of them?
26. Does UAT have a second test account, and an account with a lower role, for access checks?
27. Can each service expose its feature flags or a configuration version to a probe?
28. May the agent run the security pass on UAT by default, and may it use the request-limit probe on sign-in and OTP?
29. Does your application keep its own activity or audit log, and where? Semar's run ID can then be matched to it.
30. Which of your applications use sign-in with Google, on the web and on Android? Is there a Google test account for UAT, and does it use a second step?
31. Can the UAT build offer a test sign-in, so that a pipeline runner needs no person?
32. Which model vendors may receive your source code, your ticket text, and UAT responses? The answer sets the provider for each role.
33. Which MCP servers does your team already run, and which of their tools may Semar use?
34. Who may accept a gap in a ticket's readiness, and which gaps may nobody accept?
