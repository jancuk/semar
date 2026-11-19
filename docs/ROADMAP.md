# Semar roadmap

This page says what Semar does today, what comes next and in which order, and what it will not do. It names no dates. The [guide](GUIDE.md) describes everything that is built.

## Built

| Area | What works today |
| --- | --- |
| Replay and verdict | Stored scripts replay against an HTTP API with no model. Code computes one of six outcomes from recorded evidence, and a result belongs to the build that was tested |
| Rules of an environment | Hosts, methods, and an effects policy limit what a test may do. A production environment is read only. Secrets are removed from everything that is stored |
| Planning | `semar plan` drafts tests from the change of a branch and its ticket files. One job with no tools answers each acceptance criterion, on a ladder from a small model up, under a cost limit |
| Checks on a draft | Code compares each drafted request with the API document, tries the read-only steps once, and refuses a typed-in date near today and a typed-in ID |
| A second plan | A criterion that has a test is skipped. A new or changed criterion is planned, and the plan names the tests of a criterion that changed or is gone |
| Review | One review shows each item that needs a person, with what the test sends and what the service answered in the last run. `semar run` offers it after the tests ran, runs the approved tests again, and ends with the status |
| Data | Fixtures find or make a record across services and clean it up. A plan drafts a fixture that is missing and tries it once |
| Accounts | A step, a fixture step, and a journey leg can act as another account |
| Web | Chrome on a profile of Semar's own, sign-in by itself, `semar record`, web tests from a plan, and a locator repair with no model |
| A web application with four commands | `semar init` needs only the address, and writes an account with no token for a web page. `semar login` needs no argument, ends when its last page is closed, and offers to add the hosts that the page asked for. A run and a plan name the hosts that the browser blocked. A service on this machine runs the head commit of the repository, so `semar run` needs no `--build` |
| Journeys | A business flow across services, with API legs and web legs, runs against one set of builds |
| The ledger | Every use case of a change ends in one state. Manual checks, deferrals, and retired tests are recorded with a name and a reason |
| Reports | A report for each run, an activity log, a status page as one HTML file, and exports as CTRF, JUnit XML, and SARIF |
| Model providers | Claude Code, Codex, the Anthropic API, and any server with the OpenAI chat format. A page on your machine manages them and shows usage |

## Next

The order is the order of work. Each item says what a person does today in its place.

| # | Item | Today |
| --- | --- | --- |
| 1 | **Android tests.** A step taps and reads a screen on an emulator or a device, and a plan writes a test for an Android criterion. The first task is a trial of the ways to drive a device, to choose one | An Android criterion becomes a manual check. The plan writes its steps, and a person records the result with `semar check` |
| 2 | **Record a flow into a fixture.** A person does the preparation once, in a browser or with requests, and Semar writes the fixture. An import of a Postman collection or a HAR file gives the same result | A person or a plan writes the fixture file |
| 3 | **A fixture that uses a fixture.** A paid order needs a customer, and the customer fixture is written once | Each fixture repeats the steps that it needs |
| 4 | **Fixtures and status across repositories.** A team shares fixtures from one place, and one view shows the status of a ticket that touches several services. Tickets come from the tracker and not only from files | Fixtures live in the repository under test. A ticket is a file in `docs/tickets/`, and `semar status` reads one repository |
| 5 | **Repair of a test.** When a criterion changes, Semar proposes the change to its test, and a person approves it | The plan writes a new test and names the old one. A person removes it with `semar rm` |
| 6 | **A sign-in that Semar repeats through another site.** A person signs in once in the window, and Semar records the sign-in and repeats it when the session ends | A person signs in again in the window of `semar login` each time that the session ends |
| 7 | **Web checks for what is absent, and for an attribute.** A check says that an element is not on the page, and a check reads an attribute such as a placeholder | A web check reads the text of an element that is on the page |

## Later

These parts are designed and not built. Their order is open.

* **Logs and traces as evidence.** A scenario reads the logs and traces of the environment for the time of its run, to explain a failure and to check what an API cannot show.
* **Data stores.** Read-only checks on a database or a search index.
* **Security checks.** Access checks and variations of a request, with confirmed and suspected findings.
* **An agent that explores.** An agent works through a scenario with Semar's tools, explores around the change, and leaves a script behind.
* **Team mode.** Results on the merge request and on the ticket, decisions by reply, and jobs for a pipeline.
* **A knowledge base.** Earlier tickets, decisions, and test cases from a tracker or a wiki, for the planner.
* **History.** A view of what was tested for each ticket, team, and scenario over time, with metrics.
* **More configuration and fewer files.** Shared cases, data sets, named checks, and company rules as YAML.
* **More formats.** The Arazzo format, and signed bundles of evidence.
* **The dashboard.** Views of fixtures, journeys, and the ledger.

## Limits of what is built

Each limit is described in the guide, in the section of its feature.

* A check tests the status, a JSON value, a header, or the text of a web element. It does not compare a response with the schema of the API document.
* The time zone of a date variable is the zone of the machine. Only the form `YYYY-MM-DD` is read as a typed-in date.
* The rule against a typed-in ID reads a query parameter and a top-level body field, in the criteria pipeline only.
* An approval covers the steps and the expected results of a scenario, and not the payload of its script.
* The single pipeline writes every test again on a second plan.
* A web flow does not use a select list, a hover, a file upload, a second tab, a color field, or a slider. It does not repeat a sign-in through another site or with a second factor. A person signs in in the window of `semar login`, and the session lasts in the profile until it ends.
* A web check reads the text of an element that is on the page. It cannot say that an element is absent, and it cannot read an attribute such as a placeholder.
* A review inside `semar run` covers the scenarios of the run. A journey that needs review is not offered, and `semar review` takes it.
* A lease on a fixture covers one machine. Runners on several machines are not covered.
* Semar has no scheduler. Run `semar run`, `semar fixture check`, and `semar journey run` from your own pipeline.
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
