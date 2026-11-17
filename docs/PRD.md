# Semar product requirements

* **Status:** Draft 1 with updated product scope and roles. The model-free core (Plan 01), the test plan from a diff (Plan 12), the model providers with the dashboard (Plan 13), the documents of the repository (Plan 14), and progress while a plan runs (Plan 15) are implemented. **Updated 2026-11-17:** adds the coverage ledger, fixtures, journeys, engines and the gateway, packs and the registry, and standards and interchange. These are built, with the limits that each section names: the trial run (Plan 16), fixtures (Plan 17), API-only journeys (Plan 18), the coverage ledger (Plan 19), and the CTRF, JUnit XML, and SARIF exports (Plan 20). These are requirements and are not built: engines and the gateway, packs and the registry, screens and UI legs, the dashboard views, the Arazzo format, the Postman and HAR imports, signed evidence bundles, and enterprise operation. Facts that are not verified are marked as such.
* **Author:** azhar
* **Date:** 2026-11-14
* **Related:** [user scenarios](USER_SCENARIOS.md), [technical requirements](TRD.md), [test workflow](WORKFLOW.md), [workflow diagram](workflow.html)

**Scope (2026-11-14):** The scope, the roles of the people who use it, and decisions D26 to D31 below set what Semar is for. No claim here says that Semar is the first or the only tool of its kind.

## Current scope

Initial product scope: **backend APIs, web, and native Android**. Next platform: **native iOS**, extending mobile support to Android and iOS. Android is not deferred to the later iOS phase. We interpret the repeated Android in the owner's priority statement as continued Android support when iOS is added.

| Target | First-scope capability | Initial support boundary |
| --- | --- | --- |
| Backend API | Authenticated REST/JSON behavior, schema and business checks, positive/negative/boundary cases, role/object access, asynchronous readback | Document supported protocols; GraphQL, gRPC, load testing, and arbitrary event systems are later extensions |
| Web | Real-browser user journeys, functional UI assertions, API-connected outcomes, action/network/screenshot evidence | Start with a declared Chromium matrix; other browsers and unsupported browser capabilities remain visible |
| Android | Native app install/launch on supported test devices, input/navigation, permissions, UI assertions, backend readback, screenshots and app logs | Start with a declared APK/emulator/device matrix; mobile-browser emulation does not count as native Android |
| iOS | Later: native iOS journeys on a separately validated runner/device matrix | Android results do not imply iOS results; unsupported iOS checks are explicitly not run |

These are support boundaries for delivery, not claims that every company uses all three surfaces. Select applicable surfaces per project and journey. A missing required Android artifact blocks that journey; an API-only service can declare Android not applicable with a reason.

**Journeys (added 2026-11-17).** A journey is a business flow that belongs to the product and crosses services and surfaces. It is a fourth layer above scenarios and runs against one target manifest. API-only journeys come first (M1.1), then web legs (M2), then Android legs (M3), with no change to the journey format. API-only journeys are built (Plan 18). Web and Android legs are not built. See [Journeys](#journeys) and decisions D58 and D59.

**Engines (added 2026-11-17).** Semar does not build every surface engine itself. It integrates existing MCP servers as engines behind a gateway, and adds what they do not provide: recording, verdicts computed by code, an effects policy, replay, journeys, and fixtures. Which engine serves the web, Android, and iOS is not decided. Spikes compare candidates (D61). **iOS timing depends on that spike:** iOS stays after Android and is planned for M4, and it may come earlier if a candidate engine covers it. This is open question 37.

Grafana and read-only database access are supporting evidence integrations. They are required only where a check depends on them. Jira is the initial work source; existing test management and a prebuilt knowledge base are optional. Start from approved requirements, developer explanations, API specifications, and previous evidence when there is no TestRail or QA-owned suite.

## Who uses Semar and who decides

Semar serves three roles: the QA engineer, the developer, and the product manager. It does not replace the QA engineer, and it is not built to run a team with no QA. It changes what the QA engineer spends time on: less testing by hand and more automation. The QA engineer owns the scenarios, the rules, and the configuration that let an agent test any change, and builds on them with AI. The developer gets a change tested without waiting for a manual pass. The product manager sees what was tested and decides on intent.

Verification responsibilities are configured by capability and are not tied to a job title: a QA engineer, a peer developer, a tech lead, or a product owner can hold one. Automation does not supply missing business intent or silently accept release risk.

| Situation | Decision path |
| --- | --- |
| Unchanged approved expectations, complete clean evidence, permitted low-risk scope | Reuse the approved checks; mark verified under the preconfigured policy without a new acceptance step. Normal release authorization still applies. |
| New or changed business rule | Ask a focused question in the existing issue/MR. An eligible peer developer, lead, or product owner confirms the expectation and source revision. |
| Unknown intent, incomplete evidence, flaky behavior, or high-risk change | Route to the named owner with evidence and the exact unresolved decision. Never leave it without an owner. |
| Solo developer with no independent reviewer | Allow diagnostic testing and author confirmation, labeled `author_confirmed`. Preserve factual check results, but do not call this independent intent validation. A permitted low-risk release requires explicit owner risk acceptance; high-risk policies may remain blocked. |
| QA engineer | Owns the scenarios, the rules, and the automation. Reviews what the agent drafted and confirms expected results. A manual pass by the QA engineer is not a gate on every run. |

Risk policy must define high-risk areas such as authorization, tenant boundaries, money movement, irreversible deletion, and migrations. A second AI agent is not an independent business approver. Existing approved assertions can be reused without repetitive questions; new generated translations are reviewed under the source-trust contract.

A new team can bootstrap critical journeys from its product descriptions and sandbox. Observed behavior is labeled a baseline, not automatically accepted as correct. Baselines and missing requirements remain visible until a responsible person decides.

## Positioning

Semar helps QA engineers, developers, and product managers run integration tests in a UAT or a production environment, across API, web, and Android, under rules that the team configures, with recorded evidence. It keeps a history of what was tested, per ticket, team, and scenario. A pilot must show lower total human effort, useful defect detection, and acceptable maintenance and cost before broad delivery. "First" and "only" are not supported claims, and Semar does not remove human judgment.

## Summary

Semar has two aims. The first is to make a team depend less on QA for manual testing: a change does not wait for a person to test it by hand, because an AI agent tests it, with recorded evidence. This does not replace the QA engineer. The QA engineer's work moves to automation: the scenarios, the rules, and the tools that let an agent test any change. The second is to give testing by an AI agent one standard: the same scenario format, the same labels for where an expected result comes from, the same recorded evidence, the same verdict rules, and the same report, whoever starts the run and whichever model does the work. A person still decides what the product must do and which risk to accept.

**The promise is accountability, not correctness.** Semar does not guarantee that all behaviour is correct. Testing shows defects and cannot prove that none exist. Semar guarantees three things:

1. Nothing that needs testing is silently skipped. Every use case that needs testing ends in a state with evidence, or on a short list for a person, with the reason.
2. Every pass has evidence that a person can open and check.
3. The false-pass rate is measured. A random sample of passes is checked by a person (`semar sample`), and a seeded-defect detection check runs before each release.

Semar is a planned open-source behavior-verification tool for QA engineers, developers, and product managers. It runs integration tests in a UAT or a production environment and produces evidence tied to requirements and the exact deployed target. What Semar may do in an environment depends on how the configuration declares it: its kind, its hosts, its methods, and what a test may change. On UAT it creates and changes test data only through the application, signed in as a test account, as a QA engineer does. It never writes to a database directly.

Semar keeps the history of testing. For each ticket, team, and scenario it shows what was tested, on which build, with which outcome, and who confirmed it.

An agent that knows nothing about the product cannot choose a good scenario, add a step, or explain a result. Semar therefore gives the agent context from three sources: the team's tools through MCP servers, a knowledge base and memory of earlier tickets and decisions, and the source code of the change. The agent uses them to choose scenarios, to run the tests, and to understand the product.

Semar gives an AI agent the hands of a QA engineer. The agent can call the API as a person would in Postman, use the web app in a browser, use the Android app on an emulator or device, and read logs, traces, and dashboards in Grafana. Every action goes through Semar, which records it.

The agent is the tester, and it does four jobs. It drafts test scenarios from a code change. It works through each scenario and leaves a script behind. It explores around the change for defects that no scenario covers. It tests the security of what changed. Every action it takes is written to an activity log that a developer or a QA engineer can read. Semar replays the scripts with no model. Plain code decides each verdict. The agent never grades its own work.

Semar also keeps a regression suite. Each ticket that goes through Semar leaves its scenarios behind, and a regression run replays all of them, with evidence for every one and no model call.

A scenario often needs data in a given state, such as a customer with a paid order, and preparing it can involve several services. The planner reads one repository and cannot know the calls of the other services. Semar keeps that preparation in a **fixture**: a named, reusable flow that a person has reviewed. A scenario names the state it needs. The planner sees only the catalogue of fixtures, picks one by meaning, and never writes a multi-service flow. When no fixture gives the state, the plan lists a fixture that is needed, and a person approves or rewrites it. A fixture that cannot prepare its state ends the scenario `blocked`, never `failed`.

Some business flows belong to no single service. A mobile app places an order, a warehouse service packs it, the web app shows it, and a billing service records it. Semar tests such a flow as a **journey**: legs on several services and surfaces, run against one manifest of versions, with checks for values that must be the same everywhere. A named product or QA owner owns each journey. When a journey fails, the report names the first failing leg.

Semar does not build every surface engine itself. It integrates existing MCP servers as engines behind a **gateway**. During a verification run the agent sees one MCP server, `semar`, and cannot reach an engine directly. Semar forwards each call after it applies policy and records it, so a call that Semar did not see cannot become evidence.

Developers and reviewers are already busy, so Semar automates execution and evidence collection and asks people only for unresolved intent and risk decisions. Pipeline jobs can start runs; decisions stay in the existing issue/MR workflow.

Every change verification has a traceable work identity, initially a Jira ticket. Before drafting, the agent reads available requirements and prior testing. TestRail, team documents, and prior runs enrich this when present; their absence does not prevent onboarding. Missing business intent remains an explicit question. Publication writes are separately configured and authorized.

Semar reaches AI providers through agent harnesses that are installed on the machine. The agent acts on a surface through one tool layer. The web engine is a Chrome DevTools Protocol client of Semar's own (Plan 22). The Android engine is chosen by a spike (D61).

## Problem

A team must build features, choose checks, prepare data, run API/web/Android tests, investigate failures, and maintain the resulting scripts. Much of that work is done by hand, by a QA engineer or by a developer. Checks can be skipped or limited to the happy path, and each change waits in a queue for a manual pass.

Two things make the queue worse:

* Expected behavior is often incomplete. Someone with product knowledge must clarify it; the model cannot establish intent from code alone.
* There is no shared record of which scenarios were tested for a ticket. Managers and PMs cannot see it, and a later release cannot reuse it.

## Users

| User | What they need from Semar |
| --- | --- |
| Developer, including solo developer | Useful checks without writing a suite first, actionable evidence, reproducible failures, and visible uncertainty |
| Verification owner: peer developer, tech lead, product owner, or founder | Focused intent questions and exception review within existing tools |
| QA engineer | Ownership of the scenarios, the rules, and the regression suite. Automation in place of manual passes, and deeper exploration |
| Merge request reviewer | A short list of expected results to confirm while reviewing the code |
| Manager or PM | A roll-up of what was tested per ticket, team, and service, and how each result was reached |
| Platform or QA lead who owns the registry and the journeys | One reviewed catalogue of fixtures, named checks, screens, adapter profiles, and journeys for the company's services, pinned by version. Journeys with a named owner, run on one manifest, and a failed journey that points to the owner of the failing leg. Fixture health, and what breaks when a fixture changes |
| Auditor or compliance reviewer | Evidence that a person can open and check, and a ledger that shows every use case in a state or on the short list with its reason. The versions of the fixtures and packs that each run used, and the manifest it ran against. Signed evidence bundles, which would let a result be verified without trusting Semar, are a later stage and are not designed |
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
| D8 | The agent's tools follow a daemon model: a long-lived daemon owns the connections, a thin MCP server exposes them, and each UI surface has three tiers of tools (a fast `run` for a whole sub-goal, step-by-step primitives, and a `batch` with no model) | Your instruction. The `batch` tier is also the replay, so "acts like a human" and "repeatable" fit in one design | The agent drives each surface one slow step at a time, and replay needs a separate mechanism |
| D9 | Semar ships two ways: as a plugin inside the user's own Claude Code or Codex session, and as an unattended command that spawns a harness | The plugin runs inside a session that the user started, and the command spawns a harness. One tool layer serves both | One of the two audiences is not served |
| D10 | The fast `run` tier needs a small, separate model (a decision model reached through a model API). It is optional and off by default | It is the one place Semar holds a model key and sends page content to a second vendor | With it required, Semar cannot run on a harness login alone |
| D11 | Testing by hand ("exploring") produces findings for a person to triage. It never produces `passed` | A pass must come from a confirmed expected result and recorded evidence | A pass rests on the agent's opinion again |
| D12 | A feature map in the repository lists each feature, its URLs, paths, and screens, and its source files. The agent drafts it once and people correct it | It lets a plan name the exact targets a change affects, and it makes "all features" a number for regression | Each plan guesses the affected URLs from the diff alone, and regression coverage cannot be measured |
| D13 | The regression suite fills by itself: a signed-off ticket's scenarios join it. For features no ticket has touched, Semar records current behaviour as a baseline | No one has time to write a suite by hand | A suite that someone must write and maintain |
| D14 | A baseline pass is reported as "unchanged since the baseline", not as "correct" | Recorded behaviour includes the defects the system has today | A regression report that claims more than it knows |
| D15 | Decisions use existing issue/MR roles; unchanged approved assertions are reused. New/changed business assertions need source-appropriate confirmation; no manual acceptance by one fixed role is mandatory | The owner's instruction that a change must not wait on one role and the source-trust review | Excessive repeated questions remove the time saving; absent intent cannot be automated away |
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
| D42 | A decision model is not an agent. Semar uses it for the pilot and for screening: fast choices and scores, each with a probability | A decision model returns choices with probabilities. It does not generate text, write code, or hold a conversation | Listing it as a third agent provider would not work |
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
| D53 | Semar promises accountability, not correctness. It guarantees that nothing that needs testing is silently skipped, that every pass has evidence a person can open, and that the false-pass rate is measured by a random sample of passes and by a seeded-defect detection check. It does not guarantee that no defect exists | Testing shows defects and cannot prove that none exist. A promise that Semar cannot keep would lose trust at the first defect that escapes | The report claims more than Semar can show, and one escaped defect weakens the trust in every pass |
| D54 | Each use case that needs testing is an obligation. It ends in exactly one of five states, computed by code from the outcomes of the scenarios that cover it: `verified`, `failed`, `blocked`, `needs_a_person`, or `deferred`. The ledger sits above the six scenario outcomes and does not replace them | This extends D25. D25 covers every changed behaviour. The ledger also covers use cases from tickets, documents, manual test cases, and past defects, and it makes a use case that cannot be automated a stated state instead of a silent gap | A plan can look complete while a use case has no state, and nobody can say what was skipped |
| D55 | Test data and preconditions are fixtures: named, reusable, reviewed preparation flows in `.semar/fixtures/`. A scenario names the state it needs and never holds the flow. The planner sees only the catalogue (id, description, parameters, outputs) and picks by meaning. A fixture counts only after a person approves it | The planner reads one repository and cannot know the calls of other services. "Take the first item of a list" fails when the environment holds no such record. For preparation steps, a fixture replaces the fragment of FR-248 and of [Plan 11](implementation/11-configuration.md) | The planner guesses multi-service setup, results depend on whatever data the environment holds, and each scenario copies its own setup |
| D56 | Fixtures are written in a format compatible with the OpenAPI Initiative's Arazzo workflows, with Semar's find, create, cleanup, lease, and effects under `x-semar` extension keys. To verify: the exact mapping is not verified, and a spike checks it. If Arazzo does not fit, Semar keeps its own YAML and offers an import | A format that other tools can read lowers the cost of adopting and of leaving. Whether Arazzo can express find before create, cleanup, and a lease is not known | Semar uses its own fixture format from the start. Other tools cannot read fixtures, and the same fallback applies if the spike fails |
| D57 | After the planner's draft passes validation, a trial run sends the read-only steps once against the environment and returns problems to the planner for repair, inside the existing three attempts. A trial result is evidence about the script. It never counts as a pass and never changes an expected result | Today the first meeting of a script and the environment is `semar run`, so a wrong path or an empty capture shows up late. A response shows what the service does, and an expected result still comes from the diff, the ticket, a person, or a document (D28) | Wrong paths and empty captures reach the run. If a trial could change an expected result, the service under test would write its own expectation |
| D58 | A journey belongs to the product, not to one service. A named product or QA owner owns it. It lives in `.semar/journeys/` of the organisation repository, or of the repository of a single-repository product, and it runs against one target manifest. Legs bind data by name and reuse fixtures and named checks, not another repository's scenarios | No service can own a flow that crosses services. A result is valid only for the manifest it ran on. Independent legs let a service team change its scenarios without breaking another team's journey. This replaces the narrow journey of [Plan 10](implementation/10-android.md), an ordered list of scenario IDs, which is a specification that is not built | A journey depends on other teams' scenarios and breaks when they change, or it has no owner. A pass on mixed builds is read as a pass of the product |
| D59 | API-only journeys come first (M1.1), then web legs (M2), then Android legs (M3), with no change to the journey format | API-only journeys use the engine that exists. Each later surface adds a kind of leg and no new format. This matches FR-134 | Journeys wait for every engine, or the format changes when a surface arrives |
| D60 | A UI leg has two phases. The agent authors the leg once, like a tester, and the recorder stores each action with a locator. Semar replays the leg with no model. Screen objects (`kind: Screens`) give each screen and element a stable name and ID, and a person approves them | The agent needs stable names to refer to elements, and replay must stay repeatable. The Page Object pattern is common QA practice. This applies D1 and D8 to UI legs | The model runs on every replay, so cost and results vary, or each leg depends on element position or visible text |
| D61 | Semar integrates existing MCP servers as engines behind a gateway. During a verification run the agent sees one MCP server, `semar`, and has no direct path to an engine. Semar forwards each call after it applies policy and records it, and adds recording, verdicts computed by code, an effects policy, replay, journeys, and fixtures. Planning is unchanged: the planner is read-only and may use the person's own MCP servers and skills | A call that Semar did not see cannot become evidence (product rule 1). This extends D44, which keeps every tool denied until a person lists it. It also changes how D8 applies. The three tiers of D8 stay as the tool model. The web engine is built in process on a Chrome DevTools Protocol library (Plan 22), so for the web the gateway is not needed. Candidates for mobile, none of them verified: mobile-mcp and Maestro, and Appium | Semar builds and maintains an engine for each surface, or the agent reaches an engine directly and its calls cannot count as evidence |
| D62 | Engines are registered in the person's own Semar home (`engines.yaml`), never read from the repository. Each engine runs at an exact pinned version and never `latest`. Semar hashes each tool description, and a changed description needs re-approval. Every tool stays denied until a person maps it in an adapter profile | The repository under test is untrusted, which is the rule for provider setup. A tool description can change after approval (a rug pull). This extends D44 and D20 | A repository can name an engine that runs with the person's permissions, or a changed tool gains reach unnoticed |
| D63 | Where an adapter supports a native replay format, such as a Playwright script or a Maestro flow, the stored script may be that native artifact. Semar still binds each expected result to a recorded action and computes the verdict. To verify: whether the binding works with a native artifact is not verified, and a spike checks it | A team can then run the same flow without Semar, which lowers lock-in. The binding of FR-146 must still hold | Semar stores only its own script format and a flow cannot run without Semar. The same fallback applies if the spike fails |
| D64 | Content comes in three layers plus journeys, merged with this precedence: repository, then organisation, then built-in. The organisation registry is a git repository. `semar pack add acme/orders@1.2` pins a version and writes a lock file with a digest. Every run records the pack versions | A company needs one reviewed set of fixtures, named checks, screens, journeys, and adapter profiles for its services, defined once and not copied into every repository. Git gives review, history, and pinning | Each repository copies shared fixtures and the copies drift, or packs float and nobody knows which version a run used |
| D65 | The dashboard is a view and an approval interface. Git is the source of truth. A shared team edition is a later stage | A second record would drift from the files that D49 makes the source. The local dashboard exists and serves model settings | Two sources of truth, or the dashboard cannot be used to approve |
| D66 | The open-source core stays Apache-2.0. Enterprise features belong to a later managed edition: single sign-on, role-based access, an audit log, run history across teams, secret-manager integration, masking of personal data in evidence, and data residency. The format and the engine are never made commercial | This is consistent with D4, which stages a managed cloud, and with D36, which names Apache-2.0 as the license choice. Open question 9 stays open until the owner confirms the license, and open question 40 asks where the boundary lies | Adopters cannot rely on the core staying open, or the managed edition has nothing to offer that funds it |
| D67 | Semar publishes its scenario, fixture, journey, screens, evidence, and verdict formats as versioned JSON Schemas, with a conformance suite that another tool can run. It does not claim to be a standard | Adoption is earned. Published schemas and a conformance suite let others implement the formats. A neutral foundation and an RFC process are a later option. This extends FR-255 | The formats stay private and nobody can check an implementation, or Semar claims a status that nobody has given it |
| D68 | Semar exports reports in CTRF and JUnit XML and findings in SARIF. It imports Postman collections, OpenAPI documents, HAR files, and existing manual test cases as drafts that a person reviews. To verify: the exact version of each format is not verified | Results should reach the tools a team already runs, and an existing collection is the fastest start. An import is a draft because Semar cannot judge its fidelity. This extends FR-71 and FR-188 | Each team writes its own converter, or an import counts without review |

D53 to D68 were added on 2026-11-17. They extend D8, D25, and D44 and reverse no earlier decision.

## Goals

The first two goals are the reason Semar exists. The numbered goals after them say how.

* **Depend less on QA for manual testing.** A manual pass by a QA engineer is no longer the step that every change waits for. Semar does not replace the QA engineer. The QA engineer's time moves to automation: the scenarios, the rules, and the tools that let an agent test any change, and to intent, risk, and exploration.
* **One standard for testing with an AI agent.** Every run follows the same workflow and produces the same kind of scenario, evidence, verdict, and report, so that results can be trusted, repeated, and compared across people, teams, and models.

1. Shorten the time between "the build is on UAT" and "the evidence is accepted" for a batch of tickets, by testing tickets in parallel.
2. Produce test scenarios when the ticket is blank.
3. Make a "passed" result trustworthy: recorded by the tool, computed by code, tied to a named build, and labelled with where each expected result came from.
4. Give every failure a recorded request and response, or a screenshot, plus a script that reproduces it.
5. Leave behind reusable regression scripts owned by the team, QA engineers and developers together.
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
23. Keep a history of testing and show metrics from it: which tickets, teams, and scenarios were tested, on which build, and with which outcome.
24. Give the agent the context it needs to choose scenarios and explain results: the team's tools through MCP servers, a knowledge base and memory, and the source code.
25. Run in a UAT or a production environment under the rules that the configuration of that environment sets.
26. End every use case that needs testing in a state with evidence, or on a short list for a person, with the reason. Nothing is silently skipped.
27. Make a multi-service precondition a named fixture that a person has approved, never a guess by the planner, and try the read-only steps of a script against the environment before the run of record.
28. Test a business flow that crosses services and surfaces as a journey on one manifest, and name the first failing leg.
29. Drive existing engines through MCP under Semar's own policy, so that every call is recorded and the agent has no direct path to an engine.
30. Give a company one shared, versioned catalogue of fixtures, named checks, screens, and journeys in git, pinned by version, and let a person review and approve it in a local dashboard.
31. Export results in common formats (CTRF, JUnit XML, and SARIF), and publish Semar's own formats as schemas with a conformance suite.
32. Measure the false-pass rate for every release, by a random sample of passes that a person checks and by a seeded-defect detection check.
33. Leave a manual checklist, with exact steps and expected results, for every use case that cannot be automated, and count what a person records apart from automated results.

## Non-goals

* Semar does not detect a misunderstood requirement when the expected behaviour is written nowhere outside the code. A person must supply or confirm it.
* Semar does not replace unit tests, integration tests in CI, or load tests.
* Semar does not act outside the rules of an environment. Every environment declares its kind, test or production, its hosts, its methods, and what a test may change. Semar refuses a request outside them, and a run against production must name the environment.
* The standalone runner needs no hosted account. Managed cloud is a separate staged delivery, with explicit tenant/security/operations gates.
* Native iOS is outside the initial API/web/Android scope and follows it; native Android is included from the first product scope.
* Semar does not claim exhaustive interaction coverage across a release. Declared critical cross-service and cross-surface journeys are in scope; untested combinations remain explicit.
* Semar does not manage test devices or a device farm.
* Semar does not let the agent's opinion produce a "passed".
* Semar is not a security scanner and not a penetration test. The agent tests the security of the changed entry points with a fixed list of probe kinds. It runs no automated scan and no load test.
* Semar does not change a ticket's status or fields, and does not create, edit, or delete TestRail cases. It adds its own comment and, when turned on, test results.
* Semar is not a knowledge base of record. Jira, TestRail, and the team's documents stay where they are.
* Semar does not guarantee the absence of defects. Testing shows defects and cannot prove that none exist. Semar guarantees accountability: every use case that needs testing ends in a recorded state or on a short list for a person, every pass has evidence, and the false-pass rate is measured.
* Semar does not build every surface engine itself. It integrates existing MCP servers as engines and adds recording, verdicts computed by code, an effects policy, replay, journeys, and fixtures.
* The dashboard is not a database of record. Git is the source of truth, and the dashboard is a view and an approval interface.
* Semar does not claim to be a standard. It publishes schemas and a conformance suite so that another tool can implement the formats, and adoption is earned.

## Product rules

These rules hold in every milestone. A feature that breaks one is out of scope.

1. **The tool records the evidence.** Only an action that goes through Semar's recorder counts as evidence. In an unattended run the agent has no other path to the test environment.
2. **Code computes the verdict.** The same evidence always gives the same verdict.
3. **Every expected result names its source:** the ticket, an existing test case, a person, the diff, a baseline, or an invariant such as "no server error".
4. **Expected behavior has authenticated provenance and policy-appropriate review.** Reuse unchanged approved checks. New/changed business assertions need an eligible independent reviewer, who may be a developer, lead, or product owner. A quote alone does not validate its executable translation. Solo author confirmation is explicitly labeled and does not count as independent validation; release handling follows the role policy above. Registered invariants are assigned by trusted code, not by the model.
5. **Every result names the build it ran against.** A result for a different build is stale.
6. **The agent never sees a password, a token, or an OTP.**
7. **Secrets and personal data are removed before evidence is stored or shown to the model.**
8. **Semar never writes to Grafana or to a data store.** It reads from Grafana, PostgreSQL, MySQL, and Elasticsearch, and each account itself must be unable to write.
9. **Recommendations from the AI are advice.** They are labelled as written by AI and never change a verdict.
10. **Knowledge is data, not instructions.** Text from a ticket, a test case, a document, or a memory note can inform a plan. It cannot change a verdict or a permission.
11. **Code does every write to another system.** The agent holds no credential for Jira, TestRail, the code host, Grafana, or a database.
12. **A test changes the test environment only through the application.** Semar acts as a test account, only where the environment allows changes, and never runs an operation on the environment's deny list.
13. **Every action on the test environment is in the activity log.** The recorder writes each entry. The agent supplies only its reason, which is labelled as written by AI.
14. **Nothing that needs testing is silently skipped.** Every use case that needs testing ends in a recorded state, or on a short list for a person, with the reason. A gap is a state and never an absence.
15. **An engine and its output are untrusted.** An engine's tools stay denied until a person maps them, and its version is pinned. What an engine returns is data. It cannot change a verdict or a permission.
16. **A fixture or a pack counts only after a person approves it.** The registry address comes from the person's configuration and never from the repository under test.

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

### Team adoption

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-130 | Setup and verification need no test-management installation and no existing automation suite | M1 |
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
| FR-32 | The configured verification owner records acceptance when policy requires human review; any configured role can hold it | M1.1 |
| FR-33 | The authorized release owner signs off under team policy. The decision names the target manifest and scenario versions; the role is configured and is not tied to a job title | M1.1 |
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
| FR-208 | Semar calls a decision model for the pilot and for screening. The first is a hosted decision model | M2 for the pilot. M1.1 for screening |
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

The sections below were added on 2026-11-17. Their design is in the matching sections of the [technical requirements](TRD.md). Where a row extends an earlier one, it names that row. Each section says below which of its rows are built.

### Obligations and the ledger

Design: [TRD, Test obligations and the coverage ledger](TRD.md#test-obligations-and-the-coverage-ledger). Decisions D53 and D54.

Built in [Plan 19](implementation/19-coverage-ledger.md): FR-262 to FR-270, and FR-261 in part. Obligations come from the scenarios of a plan, the cases it did not draft, the files that no scenario covers, the document differences that the planner reports, and files that a person writes in `.semar/obligations/`. Not built: obligations from acceptance criteria, the feature map, and past defects, obligations that the planner proposes, and the import of manual test cases. FR-267 is built as a ledger line, "use case with no test and no reason", and not as a plan validation. A deferral and a manual result carry a local identity that is not verified.

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-261 | Semar lists each use case that a QA engineer would test by hand as an obligation, from these sources: the acceptance criteria of the ticket, the behaviour found in the diff, the documents under `knowledge`, manual test cases imported from a tracker or a spreadsheet, the feature map, and past defects and incidents | M1.1 |
| FR-262 | Each obligation ends in exactly one of five states: `verified`, `failed`, `blocked`, `needs_a_person`, or `deferred`. Code computes the state from the outcomes of the scenarios that cover it | M1.1 |
| FR-263 | An obligation is `verified` only when every scenario that covers it passed, with confirmed expected results and recorded evidence | M1.1 |
| FR-264 | An obligation is `failed` when a covering scenario failed, with evidence, and `blocked` when it could not run, and the report names the missing piece | M1.1 |
| FR-265 | An obligation that cannot be automated, such as one that needs a physical device, an OTP from a third party, or human judgment, is `needs_a_person` and states the reason | M1.1 |
| FR-266 | An obligation is `deferred` only when a person chose to skip it, and the entry carries that person's name, a reason, and an expiry date | M1.1 |
| FR-267 | An obligation with no covering scenario and no declared reason is a defect of the plan, and the entry check or the plan validation reports it. This extends FR-123 and decision D25 | M1.1 |
| FR-268 | For every `needs_a_person` obligation, Semar writes a manual checklist with exact steps and expected results | M1.1 |
| FR-269 | A result that a person records on a manual checklist is counted apart from automated results and labelled as checked by a person | M1.1 |
| FR-270 | The report opens with one summary line of obligations by state, such as `42 use cases: 35 verified, 2 failed, 3 blocked, 2 need a person`. The ledger sits above the six outcomes of FR-26 and changes none of them | M1.1 |

### Test data: fixtures

Design: [TRD, Fixtures](TRD.md#fixtures). Decisions D55 and D56. A fixture takes the place of a fragment (FR-248) for preparation steps.

Built in [Plan 17](implementation/17-fixtures.md): FR-271 to FR-282, in Semar's own YAML, for one repository and one machine. FR-281 is built as a file lease on one machine. FR-282 is built as the command `semar fixture check`: Semar has no scheduler, so a team runs the command from its own scheduler or pipeline. Not built: the Arazzo format (FR-334), the Postman and HAR imports (FR-338), fixtures in packs, and a fixture that uses another fixture.

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-271 | A fixture is a file `.semar/fixtures/<id>.yaml` with `apiVersion: semar/v1`, `kind: Fixture`, an ID, a description, a version, an owner, parameters, `find`, `create`, `outputs`, and `cleanup` | M1.1 |
| FR-272 | A scenario names the state it needs in a `given` entry with an alias, holds no preparation flow, and reads the fixture's outputs by name | M1.1 |
| FR-273 | The planner receives only the catalogue of fixtures (id, description, parameters, outputs), picks a fixture by meaning, and writes no multi-service flow | M1.1 |
| FR-274 | When no fixture gives the state that a scenario needs, `plan.md` lists a "fixtures needed" entry with a proposed fixture, and the scenario is not drafted and appears under `notDrafted` | M1.1 |
| FR-275 | A fixture counts only after a person approves it, and every run records the version of each fixture it used | M1.1 |
| FR-276 | A fixture runs `find` first, then `create` only when `find` yields nothing, and waits with `until` on a condition and a timeout. Validation rejects a fixed sleep | M1.1 |
| FR-277 | When `find` and `create` both fail, the scenario ends `blocked`, names the fixture and the step, and never ends `failed` | M1.1 |
| FR-278 | Cleanup runs in reverse order for what the fixture created, also after a failure, and the report lists what is left. A state that cannot be undone declares no cleanup | M1.1 |
| FR-279 | A fixture builds unique values from `{{run.id}}`, so that two runs do not share records, and writes the run ID into a note or reference field where the application allows | M1.1 |
| FR-280 | A fixture step obeys the environment's `effects` policy (deny, once, reads) as a scenario step does. A fixture holds no secret, and each service uses its own auth profile | M1.1 |
| FR-281 | `lease: exclusive` on a `given` entry reserves the account or record for one run at a time. On one machine a file lock is enough. The mechanism for runners on several machines is not decided (open question 35) | M1.1 on one machine |
| FR-282 | A scheduled run of each fixture's `find` and `create` on a test environment records fixture health, and the scenarios that use a failing fixture end `blocked` with the reason "fixture unhealthy", never `failed` | M1.1 |

### Trial run after a plan

Design: [TRD, Trial run](TRD.md#trial-run). Decision D57. Built in [Plan 16](implementation/16-trial-run.md): FR-283 to FR-287. The trial uses the one auth profile of the plan. That a trial leads the planner to a correct repair is not verified.

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-283 | After the planner's draft passes validation, Semar sends each read-only step once against the environment and records the evidence | M1.1 |
| FR-284 | Semar sends only operations that the environment's effects policy classes as reads, and lists steps that write, and fixture creates, as "not tried" | M1.1 |
| FR-285 | Semar returns each trial problem, such as a 404 or a capture that finds nothing, to the planner for repair, inside the existing three attempts | M1.1 |
| FR-286 | A trial result is evidence about the script. It never counts as a pass, and the planner cannot change an expected result because of a trial response | M1.1 |
| FR-287 | The trial is on by default when the environment, its address, and an account are configured, and it is skipped with one line when they are not. A flag turns it off, and `plan.md` shows each trial result beside its step | M1.1 |

### Journeys

Design: [TRD, Journeys](TRD.md#journeys). Decisions D58 and D59. FR-134 stays as it is: it places journeys that cross surfaces at M2 for the API and the web and at M3 for Android. The rows below add API-only journeys at M1.1.

Built in [Plan 18](implementation/18-journeys.md): FR-288 to FR-298, and FR-300 for API legs. The manifest holds the services only. FR-292 is built as the commands `semar journey run` and `semar journey run --critical` and the "Journeys to rerun" section of `plan.md`: Semar has no scheduler, so the release gate and the nightly run belong to the team's own pipeline. Not built: FR-299 (it needs the registry), web legs, and Android legs.

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-288 | A journey is a file in `.semar/journeys/` of the organisation repository, or of the repository of a single-repository product, with an ID, a title, a criticality, `given` fixtures, `legs`, and `invariants`. A named product or QA owner owns it, and no service does | M1.1 |
| FR-289 | A journey runs against a target manifest: the version of every service, the mobile build, and the web build. Its result is valid only for that manifest. This extends the journey ID and target manifest of the technical requirements | M1.1 |
| FR-290 | A leg names its surface and, for an API leg, its service. It may depend on earlier legs with `needs` and wait on a condition with a timeout, and validation rejects a fixed sleep | M1.1 |
| FR-291 | A leg binds data explicitly, so that a later leg uses a capture of an earlier leg by name, and it reuses fixtures and named checks and does not depend on another repository's scenarios | M1.1 |
| FR-292 | A journey runs when a change touches one of its legs, as a release gate for every critical journey, and nightly | M1.1 |
| FR-293 | A journey ends `passed` only when every leg passed and every invariant held, on one manifest | M1.1 |
| FR-294 | A journey ends `failed` when a leg failed or an invariant broke. The report names the first failing leg, and later legs are `not_run` with the reason | M1.1 |
| FR-295 | A journey ends `blocked` when a leg could not start, for example because of an unhealthy fixture or a missing device | M1.1 |
| FR-296 | A journey ends `stale` when a component of its manifest changed after its last run | M1.1 |
| FR-297 | An invariant checks what only a journey can see, such as the same value on every surface | M1.1 |
| FR-298 | When `semar plan` reads a change, it lists the journeys whose legs call the touched routes, so that the plan says which business flows to rerun | M1.1 |
| FR-299 | The report of a failed journey points to the owner of the failing leg's service, read from the registry | M2 |
| FR-300 | API legs run in M1.1, web legs in M2, and Android legs in M3, and the journey format does not change between them | M1.1, M2, M3 |

### UI legs and screen objects

Design: [TRD, Screens and UI legs](TRD.md#screens-and-ui-legs). Decision D60. The web and Android engines are specified and not built. Only API replay is built. Until the engines exist, a mobile leg may be tested through the API calls that the app itself makes.

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-301 | A UI leg is authored by the agent, like a tester, and the recorder stores each action with a locator. Replay finds each element again from its locator, repeats the actions, and calls no model | M2 for the web, M3 for Android |
| FR-302 | While it authors a leg, the agent works from the step in business words, the live screen read as a snapshot of elements with roles and accessible names, the app source, the documents under `knowledge`, and the journey bindings. It acts through Semar's tools and has no shell | M2 for the web, M3 for Android |
| FR-303 | Secrets in a UI leg are placeholders that Semar fills in, and the agent never sees a password. A person signs in once for an OTP or a third-party account | M2 for the web, M3 for Android |
| FR-304 | A `kind: Screens` file gives each screen a stable name and each element a stable ID. The agent proposes it from the source and the live screen, and a person approves it | M2 for the web, M3 for Android |
| FR-305 | A UI step may carry `screen:`, a capture may read `from: { screen, element }`, and an expectation may use `check: { element, text }`. Each expected result is bound to exactly one recorded action | M2 for the web, M3 for Android |
| FR-306 | Code compares numbers and text from the snapshot and never the model's description of a screenshot. A UI capture is kept minimal, and API legs are the source of truth that Semar checks against it | M2 for the web, M3 for Android |
| FR-307 | A locator that is not found ends the leg `inconclusive`. A repair proposal needs a person's approval when the element's role or name changed, and it never changes an expectation or a step. This extends FR-52 | M2 for the web, M3 for Android |
| FR-308 | Each app declares known interruptions, such as permission dialogs, update prompts, and feature banners, that replay dismisses. An unknown interruption ends the leg `inconclusive`, and an element without a stable ID is recorded with a warning that the report lists | M2 for the web, M3 for Android |

### Engines and the gateway

Design: [TRD, Engines and the gateway](TRD.md#engines-and-the-gateway). Decisions D61, D62, and D63. Candidate engines for mobile, none of them verified: mobile-mcp and Maestro, and Appium. Spike S7 compares them. The web engine is built in process (Plan 22). iOS may come earlier than M4 if a candidate covers it (open question 37). Further surfaces would use the same gateway, but none is designed and no candidate is evaluated: email and SMS inboxes of test accounts, webhooks and queues, and scheduled jobs.

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-309 | During a verification run the agent sees one MCP server, `semar`, which forwards each call to the real engine after it applies policy and records it. The agent has no direct path to an engine, and a call that Semar did not see cannot become evidence | M2 |
| FR-310 | Planning is unchanged: the planner is read-only and may use the person's own MCP servers and skills. See FR-107 | Unchanged |
| FR-311 | Engines are registered in `engines.yaml` in the person's own Semar home and are never read from the repository | M2 |
| FR-312 | An adapter profile maps one MCP server's tool names to Semar's actions, such as `snapshot`, `click`, `type`, and `tap`, and a tool that no mapping allows stays denied. This extends decision D44 | M2 |
| FR-313 | Semar runs an engine only at an exact pinned version and refuses `latest` | M2 |
| FR-314 | Semar hashes each tool description, and a changed description needs re-approval before the tool can be used | M2 |
| FR-315 | Each mapped tool has an effect class (read, write, or destructive), and Semar applies the environment's effects policy and host allowlist to it | M2 |
| FR-316 | Semar enforces an action limit per job and a size cap on each engine result | M2 |
| FR-317 | Semar redacts engine output before storing or showing it, and a secret reaches an engine only as a placeholder that Semar fills in | M2 |
| FR-318 | Semar treats every engine output as untrusted data, so that text in a page or a response cannot change a verdict or a permission | M2 |
| FR-319 | Each engine runs as a separate process with only the network access it needs | M2 |
| FR-320 | Where an adapter supports a native replay format, such as a Playwright script or a Maestro flow, the stored script may be that native artifact, and Semar binds each expected result to a recorded action and computes the verdict. To verify: whether the binding works with a native artifact is not verified, and a spike checks it | M2 |
| FR-321 | Observability and data-check engines go through the same gateway with a read-only subset of their tools allowed and their writing tools denied. This extends FR-215 | M2 |
| FR-322 | Semar exposes its own MCP tools for plan, run, verdict, and ledger, so that another agent harness can call Semar and Semar judges | M2 |

### Packs and the registry

Design: [TRD, Packs and the registry](TRD.md#packs-and-the-registry). Decisions D64 and product rule 16. Semar core holds the format, the engine, and generic packs, and is open source. The organisation registry holds the fixtures, named checks, screens, journeys, and adapter profiles of the company's services, and a platform or QA team owns it. The repository holds the scenarios and fixtures of one service, and the service team owns it.

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-323 | Semar merges the repository layer, the organisation layer, and the built-in layer, with this precedence: repository, then organisation, then built-in | M2 |
| FR-324 | The organisation registry is a git repository and is the source of truth for fixtures, named checks, screens, journeys, and adapter profiles | M2 |
| FR-325 | `semar pack add acme/orders@1.2` pins a version of a pack and writes a lock file with a digest | M2 |
| FR-326 | Every run records the version of each pack that it used | M2 |
| FR-327 | The registry address comes from the person's home configuration and never from the repository under test. A repository can only name a pack | M2 |
| FR-328 | A pack counts only after a person reviews and approves it | M2 |
| FR-329 | A fixture step that comes from a pack obeys the environment's effects policy | M2 |
| FR-330 | A fixture that several services share is defined once in the registry and is not generated in each repository | M2 |
| FR-331 | Semar core ships generic packs for access checks, pagination, idempotent create, and the API security probes | M2 |
| FR-332 | An adapter profile is shared through the registry and is pinned like any other pack | M2 |

### Standards and interchange

Design: [TRD, Standards and interchange](TRD.md#standards-and-interchange). Decisions D56, D67, and D68. The exact version of each export format is not verified. Built in [Plan 20](implementation/20-exports.md): FR-188 (JUnit XML), FR-335 (CTRF), and FR-336 (SARIF, for the confirmed defects of a run). Not built: FR-333, FR-334, FR-338, FR-339, and FR-340. JUnit XML export is FR-188. Importing a Postman collection as scenarios is FR-71. In a scenario, `given` follows the Given of Gherkin, and fixture states follow the idea of provider states in consumer-driven contract testing, such as Pact.

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-333 | Semar publishes the scenario, fixture, journey, screens, evidence, and verdict formats as versioned JSON Schemas (`semar/v1`) with a conformance test suite that another tool can run. This extends FR-255 | M2 |
| FR-334 | Fixtures are written in a format compatible with Arazzo workflows, with find, create, cleanup, lease, and effects under `x-semar` extension keys. To verify: the mapping is not verified, and a spike checks it. When Arazzo does not fit, Semar keeps its own YAML and offers an import | M1.1 |
| FR-335 | Semar exports a report in the CTRF format | M1.1 |
| FR-336 | Semar exports findings in the SARIF format | M1.1 |
| FR-337 | Semar sends the W3C Trace Context `traceparent` header on every API and web request, so that the requests of a run can be found in the tracing backend. This extends FR-23 | M1 for the API, M2 for the web |
| FR-338 | Semar can import a Postman collection or a HAR file as a draft fixture, and a person reviews the draft before it counts. Fidelity is not verified | M1.1 for Postman. HAR not yet placed |
| FR-339 | Semar can import an OpenAPI document and existing manual test cases as drafts that a person reviews before they count | Not yet placed |
| FR-340 | An evidence bundle may be signed in the style of in-toto and Sigstore attestations, so that an auditor can verify a result without trusting Semar. This is not designed | M4 |

### Dashboard

Design: [TRD, Dashboard views](TRD.md#dashboard-views). Decision D65. The local dashboard exists and serves model settings. None of the views below is built: `semar fixture list`, `semar journey list`, and `semar ledger` show the same facts on the command line. Each view below arrives with the feature that it shows. The dashboard holds no data of record. Git is the source of truth, and the dashboard is a view and an approval interface.

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-341 | The dashboard shows a catalogue of fixtures, screens, and journeys with the scenarios and journeys that use each, so that a person sees what breaks if a fixture changes | M1.1 for fixtures and journeys, M2 for screens |
| FR-342 | The dashboard shows fixture health: the last time `find` and `create` worked, on which environment and build | M1.1 |
| FR-343 | A person can review and approve a proposed fixture, screen object, or pack in the dashboard | M1.1 for fixtures, M2 for screens and packs |
| FR-344 | The dashboard shows plan gaps, which are the "fixtures needed" entries of `plan.md` | M1.1 |
| FR-345 | The dashboard shows the status of each journey per manifest | M1.1 |
| FR-346 | The dashboard shows the ledger: obligations by state, and the manual checklist | M1.1 |

### Enterprise operation

Decision D66. Enterprise operation is a staged goal for a later managed edition, after M4. It is a team edition on a shared server. Version 1 still needs no server (NFR-4). The open-source core stays Apache-2.0, and the format and the engine are never made commercial.

| ID | Requirement | Milestone |
| --- | --- | --- |
| FR-347 | The team edition offers single sign-on and role-based access to runs, evidence, fixtures, and approvals | After M4 |
| FR-348 | The team edition keeps an audit log of who ran, approved, or changed what | After M4 |
| FR-349 | The team edition keeps run history across teams | After M4 |
| FR-350 | The team edition integrates with a secret manager | After M4 |
| FR-351 | The team edition masks personal data in evidence | After M4 |
| FR-352 | The team edition supports data residency settings | After M4 |

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
| NFR-11 | **Repeatable journeys.** Running one journey on one manifest gives the same outcome, apart from real changes in the environment. Legs wait on conditions with a timeout and never on fixed sleeps, and unique values come from the run ID |
| NFR-12 | **Pinned engines and packs.** Every engine runs at an exact version and every pack at a pinned version with a digest. `latest` is not allowed, and each run records the versions it used |
| NFR-13 | **Conformance of published schemas.** Every published schema is versioned and has a conformance suite that another implementation can run. Semar's own files pass it |
| NFR-14 | **Bounded cost of authoring and replay.** The model runs only when a leg is authored or repaired, and each of those jobs has a turn limit and a time limit, as NFR-6 requires. Replay of a leg or a journey calls no model |
| NFR-15 | **Private evidence in a shared registry.** A shared registry or pack holds definitions only. Evidence, a browser profile, a secret, and personal data are never published to it, and redaction under product rule 7 applies before any evidence is stored or shown |

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
| Obligations closed in a state with evidence, or on the short list for a person with the reason | The ledger per run: obligations in each state, and obligations with no state | No obligation without a state. The share in `verified` or `failed` rises release over release |
| Seeded-defect detection rate | The detection check before each release, on a sample service with seeded defects | Not set until the first measurement. It must not fall from one release to the next |
| False-pass rate from the sample | `semar sample` picks passes at random for a person to check. Count the passes that the person marks wrong, per release | Zero during shadow mode, as for false passes above |
| Scenarios that end `blocked` for a fixture reason | Count per run, by fixture, including "fixture unhealthy" | Falling release over release |
| Time to prepare data | Minutes from a scenario that needs a state to a run with that state ready, self-reported in shadow mode and compared with fixtures in use | Lower than the manual baseline |

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
| Four surfaces are four engines to build and maintain | High | Fixed build order, one recorder and one verdict engine shared by all, and one Chrome DevTools Protocol library for the web engine |
| A web or Android script breaks when the screen changes | Medium | Scripts find elements by role and name, not by position. A broken script is `inconclusive`, and the agent can repair it |
| The web engine depends on one Chrome DevTools Protocol library | Low | The library is MIT-licensed, pinned to one version, and used through a small layer of Semar's own, so another library could replace it |
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
| The provider of the decision model changes its interface or its price, or stops serving it | Medium | The pilot and the screener are optional, and each job has a fallback. The driver is small |
| A team's policy does not allow a vendor to receive code or UAT data | High | Each provider lists the kinds of data it may receive, and Semar refuses a job that would send another kind |
| The risk rules give ordinary depth to a dangerous change | Medium | The planner can raise the level, a person can set it, and the plan states the level and the reason at the top |
| "Ready for sign-off" is read as "correct" | High | The line lists what was tested and every accepted gap. A wrong expected result that a person confirmed still passes |
| Adapters multiply and go stale | Medium | Few built-in adapters, a conformance kit per kind, and the command and MCP routes for everything else |
| A company's policy weakens the result until "ready" means little | High | Outcomes and verdict rules are not policy. "Nothing failed" and "nothing is undecided" cannot be turned off |
| A typo in a YAML file silently changes a test | High | Strict files, a schema per file kind, and `semar validate` before every command, with the file and the line |
| A shared fragment is edited and many tests change at once | Medium | `semar validate --changed` names every affected scenario, and their confirmations lapse |
| More agents and panels cost more model use | Medium | Every optional job is off until assigned. The report shows usage per agent. Re-tests and regression use no model |
| An engine drifts or changes after approval, including a rug pull: a tool or its description changes | High | An exact pinned version, a hash of each tool description with re-approval on change, tools denied until mapped, and a contract test per adapter profile |
| Prompt injection through a page or an engine response | High | Every engine output is untrusted data. The agent has no shell, the effects policy and host allowlist apply, secrets are placeholders, and code computes the verdict |
| Fixtures rot when another service changes | Medium | A scheduled fixture health run. A failing fixture ends its scenarios `blocked: fixture unhealthy` and never `failed`. Each fixture has an owner and a version, and the dashboard shows what a change affects |
| Two runs collide on one account or record | Medium | `lease: exclusive`, unique values from the run ID, and `find` before `create`. For runners on several machines the mechanism is not decided (open question 35) |
| The registry becomes a single point of failure or a supply-chain risk | High | A git repository, versions pinned with a lock file and a digest, approval before a pack counts, and a registry address that comes from the person's configuration and never from the repository under test |
| Scope creeps from "any scenario" | High | A ledger with fixed states, including `needs_a_person` for what cannot be automated, a fixed build order (API journeys, then web, then Android), and journeys kept few and for critical flows. What is not supported is listed as not run |
| The guarantee is over-promised | High | The promise is accountability, not correctness. The report states the states, the evidence, and the measured false-pass rate, and never says that a change is correct or secure. The non-goal stays |
| A native replay artifact does not carry the binding of expected results | Medium | A spike checks it before M2 relies on it. If it fails, the stored script stays Semar's own format (D63) |

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
10. Settled by Plan 22: the web engine is built in process on a Chrome DevTools Protocol library, so no other tool's engine is imported.
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

Questions 35 to 42 belong to the additions of 2026-11-17. They concern work from M1.1 on and do not hold up Milestone 1.

35. How is a lease kept when runners work on several machines? On one machine a file lock is enough. A git repository cannot lock. The options are a lock service in the team edition, or a marker with a time limit on the environment's own record.
36. Does the workflow model of Arazzo fit fixtures without losing find, create, and cleanup? A spike checks it. If it does not fit, Semar keeps its own YAML and offers an import.
37. Which engine serves each surface (spikes S6 and S7), and can iOS come before M4 if a candidate engine covers it?
38. Can a native replay artifact, such as a Playwright script or a Maestro flow, carry Semar's binding of expected results to recorded actions?
39. How is data cleaned up when the application cannot delete it?
40. Where does the commercial boundary lie, and what license do shared packs carry?
41. How are obligations imported from existing manual test cases, and how good are those cases?
42. How faithfully do Postman and HAR imports convert into fixtures?
