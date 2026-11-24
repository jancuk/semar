# Semar roadmap

This page says what Semar does today, what comes next and in which order, and what it will not do. It names no dates. The [guide](GUIDE.md) describes everything that is built.

## Built

| Area | What works today |
| --- | --- |
| Replay and verdict | Stored scripts replay against an HTTP API with no model. Code computes one of six outcomes from recorded evidence, and a result belongs to the build that was tested |
| Rules of an environment | Hosts, methods, and an effects policy limit what a test may do. A production environment is read only. Secrets are removed from everything that is stored |
| Planning | `semar plan` drafts tests from the change of a branch and its ticket files. One job with no tools answers each acceptance criterion, on a ladder from a small model up, under a cost limit |
| Checks on a draft | Code compares each drafted request with the API document, tries the read-only steps once, and refuses a typed-in date near today and a typed-in ID |
| Edge cases | A plan drafts a missing field, a wrong type, a value out of range or outside an enum, an unknown ID, no session, and a read as another account from the API document, with no model. A person reviews each one |
| Checks against the API document | Every response of a run is compared with the schema of the API document, and with a time limit when one is set. A misfit fails the step by default, and a setting turns it to a warning |
| Import | `semar import` turns a Postman collection or a HAR file into scenarios and scripts, with no session and no secret |
| A second plan | A criterion that has a test is skipped. A new or changed criterion is planned, and the plan names the tests of a criterion that changed or is gone |
| Review | One review shows each item that needs a person, with what the test sends and what the service answered in the last run. `semar run` offers it after the tests ran, runs the approved tests again, and ends with the status |
| Data | Fixtures find or make a record across services and clean it up. A plan drafts a fixture that is missing and tries it once |
| Accounts | A step, a fixture step, and a journey leg can act as another account |
| Web | Chrome on a profile of Semar's own, sign-in by itself, `semar record`, web tests from a plan, and a locator repair with no model. A check reads the text, the state, or an attribute of an element, that an element is absent, a count, and the path or the title of the page, and it waits for its condition. A step can select, hover, check, uncheck, and upload. Every web step leaves a screenshot |
| Five commands for a day | `semar --help` lists `init`, `login`, `plan`, `run`, and `show` under "Every day", and five more under "Also". The other commands still work. `semar init` needs only the address, writes an account with no token for a web page, and names `semar login` as the next step. `semar login` needs no argument, ends when its last page is closed, and offers to add the hosts that the page asked for. A run and a plan name the hosts that the browser blocked. A service on this machine runs the head commit of the repository, so `semar run` needs no `--build` |
| Android | `@surface:android` scenarios and journey legs tap, long-press, double-tap, drag, type, swipe, scroll, press keys, open deep links, and read an app on an emulator or a device over adb or a WebDriver server, with multi-window support for soft keyboards (IMEs) and system dialogs, locator repair with no model (`semar screens repair`), the closest elements of a miss in the summary and the report, `semar run --pause-on-fail`, one restart of a dead UiAutomator2 server per run (never a server at `serverUrl`), and the crash report of the app, with an optional end of its log, in a step that failed. The APK is the build. A screenshot of each step is in the report. An app with no backend is a service with no `baseUrl` |
| Reusable flows | A flow in `.semar/flows` is a sequence of device steps, such as a sign-in, that a scenario or another flow runs with `use` and `with` (up to 3 levels, no cycle). A flow may have checks, which are not the check that a scenario needs, and captures that later steps read. A run records which version of a flow ran |
| Android recording | `semar record` records a flow or a scenario on a device, in the terminal or in a local web studio (`--ui`), with guided setup, multi-service picker, secrets kept in memory, a reset before each recording, a clean-session replay to verify it, and an undo of the last save. It deletes any step, picks or changes a locator, reopens a saved recording and records on from a step, names screens and shares elements through `common`, captures a value into `{{name}}` (with a regular expression), makes a typed text a param of a flow, and puts checks and `use` steps in a flow |
| MCP server | `semar mcp` serves Android tools that follow the names of mobile-mcp, over standard input and output or loopback HTTP with a token, to an agent such as Claude Code, Codex, Cursor, Gemini CLI, or Windsurf. Refs, multi-window soft keyboards, and Flutter apps are handled, and `semar_run_flow` runs a saved flow at native speed with no model call. In a repository it adds web tools, API tools that follow the effects policy, and journey tools. An agent records a scenario, a flow, a web test, or a journey, and code replays it once and keeps it only when it passes. A check that the agent recorded says `by model`. Production, a read-only environment, and `--read-only` make the server observe only. `--studio` shows the device to a person, who can pause the agent, take over, and hand back |
| Journeys | A business flow across services, with API, web, and Android legs, runs against one set of builds |
| The ledger | Every use case of a change ends in one state. Manual checks, deferrals, and retired tests are recorded with a name and a reason |
| A regression set in a pipeline | Suites by a tag, retries with a flaky mark, and `semar run --ci`. Examples for GitHub Actions and GitLab CI |
| Reports | Each run writes `report.md`, `report.html` with its screenshots inside, `junit.xml`, and `summary.md`, and `--report-dir` copies them for a pipeline. An activity log, a status page as one HTML file, and exports as CTRF, JUnit XML, and SARIF |
| Model providers | Claude Code, Codex, the Anthropic API, and any server with the OpenAI chat format. A page on your machine manages them and shows usage |

## Next

The order is the order of work. Each item says what a person does today in its place.

| # | Item | Today |
| --- | --- | --- |
| 1 | **Android plan.** `semar plan` writes a test for an Android criterion | An Android criterion becomes a manual check. A person records the scenario with `semar record`, or writes it and the screens file, or records the result with `semar check` |
| 2 | **Record a flow into a fixture.** A person does the preparation once, in a browser or with requests, and Semar writes the fixture | A person or a plan writes the fixture file. `semar import` turns a Postman collection or a HAR file into scenarios, and not into a fixture |
| 3 | **A fixture that uses a fixture.** A paid order needs a customer, and the customer fixture is written once | Each fixture repeats the steps that it needs |
| 4 | **Fixtures and status across repositories.** A team shares fixtures from one place, and one view shows the status of a ticket that touches several services. Tickets come from the tracker and not only from files | Fixtures live in the repository under test. A ticket is a file in `docs/tickets/`, and `semar status` reads one repository |
| 5 | **Repair of a test.** When a criterion changes, Semar proposes the change to its test, and a person approves it | The plan writes a new test and names the old one. A person removes it with `semar rm` |
| 6 | **A sign-in that Semar repeats through another site.** A person signs in once in the window, and Semar records the sign-in and repeats it when the session ends | A person signs in again in the window of `semar login` each time that the session ends |

## Later

These parts are designed and not built. Their order is open.

* **Logs and traces as evidence.** A scenario reads the logs and traces of the environment for the time of its run, to explain a failure and to check what an API cannot show.
* **Data stores.** Read-only checks behind the API, on a database, a search index, or an event.
* **Security checks.** Access checks and variations of a request, with confirmed and suspected findings.
* **An agent that explores.** An agent works through a scenario with Semar's tools, explores around the change, and leaves a script behind. An agent that you run can do this today over `semar mcp` and leave a scenario, a flow, or a journey. What is later is exploration that Semar's planner drives by itself, with no client and no person at the keyboard.
* **Team mode.** Results on the merge request and on the ticket, and decisions by reply.
* **A knowledge base.** Earlier tickets, decisions, and test cases from a tracker or a wiki, for the planner.
* **History.** A view of what was tested for each ticket, team, and scenario over time, with metrics.
* **More configuration and fewer files.** Shared cases, data sets, named checks, and company rules as YAML.
* **More formats.** The Arazzo format, and signed bundles of evidence.
* **The dashboard.** Views of fixtures, journeys, and the ledger.
* **Playwright as a second browser driver.** A team could choose it for its trace and its video of a run. A trial drove the browser through Playwright behind the same Go interface as the current driver. All 28 browser tests passed unchanged, and a profile of `semar login` stayed signed in. It is not built, for two reasons. It makes Node a requirement of every machine and every pipeline. A trace and a video of Playwright hold cookies and headers that the redaction of Semar does not reach.

## Limits of what is built

Each limit is described in the guide, in the section of its feature.

* A check against the API document does not check a `format`, an extra property without `additionalProperties: false`, a schema that points outside the document, a setup step, a fixture request, or a response after a redirect. It keeps at most 5 misfits for one step.
* A check does not look behind the API, at a database or at an event.
* The time zone of a date variable is the zone of the machine. Only the form `YYYY-MM-DD` is read as a typed-in date.
* The rule against a typed-in ID reads a query parameter and a top-level body field, in the criteria pipeline only.
* An approval covers the steps and the expected results of a scenario, and not the payload of its script.
* The single pipeline writes every test again on a second plan.
* A web test does not use a second tab, a color field, or a slider. A recording does not hear a hover or an upload, and a plan cannot use a custom checkbox that the page hides. A web test does not repeat a sign-in through another site or with a second factor. A person signs in in the window of `semar login`, and the session lasts in the profile until it ends. A pipeline has no window, so a web application there needs a user and a password variable.
* A param is made only in a flow, from a typed text that is not a secret: a scenario has none, and takes a captured `{{name}}`. Flows nest up to 3 levels. A recording on a device reopens only a scenario or a flow of steps on a device, and a reopened scenario is written again as a feature file, so a comment that was added by hand is lost. iOS is not recorded.
* An Android test does not see the requests of the app, so a read-only or a production environment refuses every action on a device. It uses one device at a time, no Chrome DevTools inside a WebView (only its Android accessibility tree is read), no pinch, and no video in a run. Typed text may not fire key listeners. iOS is not built.
* A video of the screen is made only through `semar mcp`, for up to 180 seconds.
* `semar mcp` serves Android only: there is no iOS tool and no cloud device. It uses one device at a time. A person still reads what `by model` marks. The location is set on an emulator only, and the clipboard needs the UiAutomator2 server. The web, API, and journey tools need a repository, and an agent cannot record a write against production.
* A screenshot is not redacted.
* A web check of one element refuses a locator that matches several elements.
* `hasText` and `index` are for a device only.
* Edge cases come only from an API document, and a plan never tries a write. A plan drafts at most 6 for one operation and 20 in all.
* An import reads a Postman collection in the format 2.1 and a HAR file. It does not read a Postman environment file, a test script of Postman, or a form body.
* A retry does not run a journey again, or a scenario that sent a request of `effects.once`.
* A review inside `semar run` covers the scenarios of the run. A journey that needs review is not offered, and `semar review` takes it.
* A lease on a fixture covers one machine. Runners on several machines are not covered.
* Semar has no scheduler. Run `semar run`, `semar fixture check`, and `semar journey run` from your own pipeline.
* Semar keeps no history of results over many runs, and it posts no result to a merge request or a ticket.
* An identity is local and not verified: an approval holds the user name of the machine and the email of git.
* The exports were not run through the official schema of each format.
* Linux and macOS are tested. Windows compiles and is not tested.

## What will not change

* A model never computes an outcome. Code does, from recorded evidence.
* A plan approves nothing. A person confirms every expected result that a model wrote.
* A plan never removes or rewrites a test that a person approved.
* A token is never written to a file, and the repository under test cannot choose where your source is sent.
* A run with no model stays possible: every test is a file that a person can read and write.

## Open questions

* Whether the vendors of Claude Code and Codex allow a subscription sign-in to drive their harness from another tool is not confirmed. Semar runs your own unmodified harness and never touches its credentials. An API key works without that question.
* Apache-2.0 is the intended license. The repository has no `LICENSE` file yet.
