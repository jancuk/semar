# Plan 30: The plan writes web tests

* **Status:** Planned.
* **Needs first:** Plans 25, 27, 28, and 29.
* **Implements:** PRD decision D74 and FR-369 to FR-374.

## Goal

`semar plan` writes a web test for an acceptance criterion that a web page shows. A model reads the page and does the flow in a browser, and Semar writes what it did as a test that replays with no model. A person reads the result in `semar review`, as with an API test.

Today the plan answers such a criterion with a manual check, because a model that cannot see the page invents locators (Plan 28). `semar record` gave the page to a person. This plan gives it to the model, and keeps everything that `record` does after the last click.

## How it works

The explorer is the recorder of Plan 28 with a model in the seat of the person.

1. The first job of a criterion (Plan 25) says that the check is on a web page.
2. Semar opens a browser with no window on its own profile, signed in by Plan 29, with the recorder in the page.
3. Each turn, Semar sends the model the criterion, what was done so far, and a list of what the page shows. The model answers with a few actions on elements of that list, or says that it is done, or that a person must do the check.
4. Semar performs each action as real mouse and key input. A check is the Option-click of Plan 28. The recorder hears them as it hears a person.
5. When the model is done, the code of `record` writes the screens, the scenario, and the script, and replays the scenario once.

## Decisions

| # | Decision | Reason |
| --- | --- | --- |
| D1 | The answer `manualCheck` of a criterion job gains `surface`, one of `web`, `mobile`, and `other`. The prompt version rises | Code must know which manual checks a browser can do. The job has read the criterion already, so the question costs no job |
| D2 | A web criterion is explored when the environment is a test environment that allows test data, Chrome is found, and the service has a web page. A service has a web page when `GET <base URL><start>` answers `text/html`, where `start` is `services[].web.start`, else `/`. The probe is one GET for each service of a plan. `--no-web` turns the exploring off. A criterion that is not explored stays a manual check, and the plan says why once | No setting is needed for the common case, and a service that is an API only costs one request. A recording clicks and types, so the rules of where `record` runs hold here too |
| D3 | The page list has the address, the title, and at most 150 lines, one for each visible element that a person can act on or read: `e<n> <role> "<name>"`, with the text, the value of a field, and `disabled` when they apply. Texts are cut at 80 characters. An element out of the view is listed, and Semar scrolls to it before it acts | A small model reads a short list well. The reference is the only way to name an element, so the model cannot invent a locator |
| D4 | The answer of a turn follows a closed schema and is one of three kinds. `act` has 1 to 5 actions, each `click`, `fill` (with `text`), `press` (`Enter` or `Escape`), `drag` (with `to`), or `check` (with `expect`), on a reference of the list. `done` has the title and the intent of the test. `manual` has the reason. Semar stops the actions of a turn at the first that cannot be done, and the next turn says which and why | Several actions in a turn make a form one turn and not five. A closed schema is what makes a small model usable (Plan 25) |
| D5 | A `fill` text may hold `{{run.id}}`. Semar types the ID of the run, and the written step keeps the variable, through the values of Plan 28 (D6). The prompt asks for it in every name that the flow creates | A test that creates "Board" at every run meets its own leftovers |
| D6 | The first answer may be `given`, with fixtures of the catalogue and an alias for each, in place of the three kinds. Semar makes them as `record --given` does, gives the model their outputs, and loads the page again. The catalogue in the prompt has the ID, the description, and the outputs of each fixture, drafts of this plan included | A flow needs data, and a fixture gives it with a cleanup. The model chooses before it acts, because a fixture that is made after a click would not be in the recording's start |
| D7 | A `check` records what the element shows, as Plan 28 does. When `expect` equals it, the check is written with the source `ticket` and the cite of the criterion. When they differ, the check is written with the text of `expect`, and the plan lists it under "fails now" with what the page shows | The expectation comes from the ticket and not from the page, so a page with a defect gives a test that fails. This is the rule of the API scenarios |
| D8 | A flow ends as a manual check, with the reason, when the model says `manual`, when it records no check, after 12 turns, after 40 actions, after 3 minutes, or when the replay of the written test does not end as the recording predicts. A flow that ends so on one model is tried once on the next model of the ladder, in a new browser with new fixtures. Nothing of a flow that ends so is written | A wrong test costs more than a manual check. The ladder is the one that the criteria jobs use |
| D9 | The written scenario takes the next number of its ticket and the tag `@surface:web`. It covers the use case of its criterion, so the use case shows a test and not a manual check. The screens go into the screens file of the app, merged, as in Plan 28 | The status of the change must count the test |
| D10 | Nothing is approved. The screens file and each scenario go through `semar review`, with the marks `(by model)` | A person reads what a model wrote before it counts |
| D11 | One criterion is explored at a time | An account has one browser profile, and two flows in one application disturb each other |
| D12 | Each turn is a job of the plan: it counts in the model use line, in `--max-cost`, and in the usage file with the pipeline `web`. The plan prints, for the web part, the criteria explored, the tests written, the ones left for a person with the reason, the turns, and the cost | The person sees what the web part cost, apart from the API part |
| D13 | The text of the page is data. The prompt marks it as such, the model has no tool, its answer can only name an action on a reference, and the browser stays inside the hosts and the methods of the environment (Plan 22) | A page can hold text that reads as an instruction |
| D14 | Not built: a mobile screen, a flow across two tabs, a file upload, a hover, and a select list, which the recorder does not hear (Plan 28). The estimate of `semar run` before a plan does not count the web part | Each is its own work. The count of web criteria is not known before the first jobs |

## Tasks

Each task starts with a test that fails, and ends with one commit.

1. **Surface of a manual check.** D1: the schema, the prompt, the decoding, the obligations file, and the plan text. Tests in `internal/stage/plan`.
2. **Page list.** D3: a function of the web engine that returns the list and a map from reference to element. Tests against pages of a test server: roles and names, a field with a value, a disabled button, hidden elements left out, the cut at 150 lines and 80 characters.
3. **Act on a reference.** D4 and D5: click, fill, press, drag, and check as real input on the recorder's tab, with the scroll, and the error of an action that cannot be done. The input code of `internal/engine/web/webtest` moves to where both can use it. Tests: each action is heard by the recorder as the same event that a person makes.
4. **The turn.** The prompt, the schema, the decoding, and the rules of D4, D6, and D8 as a loop that takes a function for the model and a function for the browser. Tests with a scripted model: a flow of two turns, an action that fails and is reported, `given` first, `given` later refused, the limits, no check.
5. **Write and replay.** D7 and D9: the events go through the code of `record`; the check source and cite; "fails now"; the use case link; a replay that fails ends the flow as a manual check and removes what was written.
6. **In the plan.** D2, D8 (the ladder), D10, D11, D12: the probe, the order after the fixture drafts, the output, the stats, `--no-web`, and `--max-cost`. Tests in `internal/cli` with a scripted provider and a test page: a plan of one API criterion and one web criterion writes both tests, and a run of them passes.
7. **Docs.** This file as built with a Measured section, the README, the PRD (D74, FR-369 to FR-374), and the TRD.

## Done when

* On a sample application with a web page, `semar plan` with the Claude Code provider writes at least one web test for a web criterion, with no file written by hand, and `semar run` passes it after `semar review`.
* The same test fails when the page shows a wrong value for its check.
* A criterion on a mobile screen stays a manual check.
* The Measured section gives, for that plan: the web criteria, the tests written, the turns, the models, the time, and the cost.
* `go test ./...` passes.
