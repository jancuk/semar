# Plan 30: The plan writes web tests

* **Status:** Implemented. This text describes the system as built.
* **Needs first:** Plans 25, 27, 28, and 29.
* **Implements:** PRD decision D74 and FR-369 to FR-374. TRD section "Plan writes web tests".

## Goal

`semar plan` writes a web test for an acceptance criterion that a web page shows. A model reads the page and does the flow in a browser, and Semar writes what it did as a test that replays with no model. A person reads the result in `semar review`, as with an API test.

Before this plan the plan answered such a criterion with a manual check, because a model that cannot see the page invents locators (Plan 28). `semar record` gave the page to a person. This plan gives it to the model, and keeps everything that `record` does after the last click.

## How it works

The explorer is the recorder of Plan 28 with a model in the seat of the person.

1. The first job of a criterion (Plan 25) says that the check is on a web page.
2. Semar opens a browser with no window on its own profile, signed in by Plan 29, with the recorder in the page.
3. Each turn, Semar sends the model the criterion, what was done so far, and a list of what the page shows. The model answers with a few actions on elements of that list, or says that it is done, or that a person must do the check.
4. Semar performs each action as real mouse and key input. A check is the Option-click of Plan 28. The recorder hears them as it hears a person.
5. When the model is done, the code of `record` writes the screens, the scenario, and the script, and replays the scenario once.

## Decisions

Each row says what was built. Where it differs from the plan as first written, the last sentence of the reason says how and why.

| # | Decision | Reason |
| --- | --- | --- |
| D1 | The answer `manualCheck` of a criterion job gains `surface`, one of `web`, `mobile`, and `other`. The prompt version rises (criterion 3 to 4). The obligation keeps it as `manual.surface`, and `plan.md` shows it | Code must know which manual checks a browser can do. The job has read the criterion already, so the question costs no job. Changed: the plan did not say where the surface is kept. A person who reads the checklist sees which checks are for a phone |
| D2 | A web criterion is explored when the environment is a test environment that allows test data, Chrome is found, and the service has a web page. A service has a web page when `GET <base URL><start>` answers `text/html`, where `start` is `services[].web.start`, else `/`. The probe is one GET for each service of a plan, up to the first that answers. `--no-web` turns the exploring off. A criterion that is not explored stays a manual check, and the plan says why once | No setting is needed for the common case, and a service that is an API only costs one request. A recording clicks and types, so the rules of where `record` runs hold here too. Changed: `services[].web.start` did not exist, so it is added to the configuration as `web: {start: /path}`, checked to be a path that starts with one `/`. Only the first service with a web page is explored. The reason is on standard error once, in `plan.md`, and not in the exit code |
| D3 | The page list has the address, the title, and at most 150 lines, one for each visible element that a person can act on or read: `e<n> <role> "<name>"`, with the text, the value of a field, the `form` of the value of a date or time field, and `disabled` when they apply. Texts are cut at 80 characters. An element out of the view is listed, and Semar scrolls to it before it acts | A small model reads a short list well. The reference is the only way to name an element, so the model cannot invent a locator. Changed: one page function reads the page, and not the accessibility tree, because it must also know what is visible and what a person can drag or click, and it keeps the elements in `window.__semarRefs` so that a reference maps to an element. A list over 150 lines keeps what a person can act on first and says how many it left out. The address has no query and no fragment. A role that a page wrote can add no line. A text inside a button or a link is not listed again, and a text inside a card that can be dragged is, so that a badge can be checked on its own |
| D4 | The answer of a turn follows a closed schema and is one of three kinds. `act` has 1 to 5 actions, each `click`, `fill` (with `text`), `press` (`Enter` or `Escape`), `drag` (with `to`), or `check` (with `expect`), on a reference of the list. `done` has the title and the intent of the test, and `unproved`. `manual` has the reason. Semar stops the actions of a turn at the first that cannot be done, and the next turn says which and why | Several actions in a turn make a form one turn and not five. A closed schema is what makes a small model usable (Plan 25). Changed: the references of the list are an enum of the schema, so a reference that is not on the page cannot be named. `press` takes an empty reference for a key that goes to the page. `done` names in `unproved` the part of the criterion that its checks did not prove, or nothing: the test is written for the rest, and the plan keeps a manual check of the same criterion for that part. A turn is a closed sum in code, `act`, `done`, `manual`, or `given`. An answer with a problem goes back once to the same model with the problems in plain words, as a criterion does, and then the flow ends as a manual check |
| D5 | A `fill` text may hold `{{run.id}}`. Semar types the ID of the run, and the written step keeps the variable, through the values of Plan 28 (D6). The prompt asks for it in every name that the flow creates | A test that creates "Board" at every run meets its own leftovers. Changed: another `{{...}}` in a text is refused with a sentence, because the engine would read it as a variable of a step. `{{run.id}}` in the `expect` of a check is the ID of the run too. The run ID is a value of every flow, with or without fixtures, so that the text that a flow typed, the locators that follow it, and the text of a check all keep the variable. The prompt tells the model to make its own data, because a first live run used a board that an earlier run left, and its test did not run again |
| D6 | The first answer may be `given`, with fixtures of the catalogue and an alias for each, in place of the three kinds. Semar makes them as `record --given` does, gives the model their outputs, and loads the page again. The catalogue in the prompt has the ID, the description, and the outputs of each fixture, drafts of this plan included | A flow needs data, and a fixture gives it with a cleanup. The model chooses before it acts, because a fixture that is made after a click would not be in the recording's start. Changed: "the page" that is loaded again is the first page of the service, `services[].web.start`, because the model has no action that opens an address. The recording starts over at that page. `given` is offered only when the catalogue has a fixture, and a `given` after the first answer is refused with a note and does nothing |
| D7 | A `check` records what the element shows, as Plan 28 does. When `expect` equals it, the check is written with the source `ticket` and the cite of the criterion. When they differ, the check is written with the text of `expect`, and the plan lists it under "fails now" with what the page shows | The expectation comes from the ticket and not from the page, so a page with a defect gives a test that fails. This is the rule of the API scenarios. Changed: every check of a flow is by `model` as well, so it needs a person's confirmation as an API expectation of a model does. The replay inside the plan therefore treats the expectations of its one scenario as confirmed for that one run and stores nothing, because an unconfirmed result is `inconclusive` and never `failed`, and the plan must tell whether the page meets the criterion. Spaces do not make a claim differ from what the page showed. A check on an element that shows no text is refused with a sentence, because the recorder would read the element around it Changed after the measurement: a check whose `expect` differs from what the element shows is not recorded the first time. The model is told what the element shows, and that such a check is written as a test that fails now. It sends the same check again when the page is what is wrong, or the check with the right text. A model had written a name that it typed itself with a word too many, and the test failed on a correct page |
| D8 | A flow ends as a manual check, with the reason, when the model says `manual`, when it records no check, after 12 turns, after 40 actions, after 3 minutes, or when the replay of the written test does not end as the recording predicts. A flow that ends so on one model is tried once on the next model of the ladder, in a new browser with new fixtures. Nothing of a flow that ends so is written | A wrong test costs more than a manual check. The ladder is the one that the criteria jobs use. Changed: the prediction is `passed` when every check met its claim and `failed` when one did not. A test that ends otherwise is removed: its scenario, its script, and what it added to the screens file, which `record.Written.Undo` puts back. A flow that never asked the model, because the browser could not be opened or the fixtures could not be made, is not tried on another model, since it would meet the same failure. A cost limit ends the flows that follow. A page gets 300 milliseconds after each action before Semar asks whether it is still, because a page starts its request a moment after a click. A plan that ends with an error removes the tests that its flows wrote. The replay declares the head of the change for a service with no build probe, because the plan does not judge a build |
| D9 | The written scenario takes the next number of its ticket and the tag `@surface:web`. It covers the use case of its criterion, so the use case shows a test and not a manual check. The screens go into the screens file of the app, merged, as in Plan 28 | The status of the change must count the test. Changed: the web tests are explored before the plan numbers its own scenarios, so a web test of a ticket has the next number, and the API scenarios of the plan follow it. The criterion has no manual obligation, and its scenario has the obligation `<ticket>/scenario-<id>`. The Feature line is the sentence of the ticket, as for an API scenario, and the title and the intent come from the model |
| D10 | Nothing is approved. The screens file and each scenario go through `semar review`, with the marks `(by model)` | A person reads what a model wrote before it counts |
| D11 | One criterion is explored at a time | An account has one browser profile, and two flows in one application disturb each other |
| D12 | Each turn is a job of the plan: it counts in the model use line, in `--max-cost`, and in the usage file with the pipeline `web`. The plan prints, for the web part, the criteria explored, the tests written, the ones left for a person with the reason, the turns, and the cost | The person sees what the web part cost, apart from the API part. Changed: the turns are in the total and in the cost limit, and the use line adds `N web turns`. `plan.md` lists the web tests, the models of the web part, and the reason for each criterion left for a person |
| D13 | The text of the page is data. The prompt marks it as such, the model has no tool, its answer can only name an action on a reference, and the browser stays inside the hosts and the methods of the environment (Plan 22) | A page can hold text that reads as an instruction. Changed: the recorder of a flow has the effects policy of the environment, and judges every request of the page as the engine of a replay does. That policy lists hosts and a deny list for a browser, and not methods, because a page sends requests that no step wrote. The environment of a flow is a test environment that allows test data, so a write is allowed |
| D14 | Not built: a mobile screen, a flow across two tabs, a file upload, a hover, and a select list, which the recorder does not hear (Plan 28), and a color, slider, or file field, which a flow cannot type into. The estimate of `semar run` before a plan does not count the web part | Each is its own work. The count of web criteria is not known before the first jobs. Changed: the list names such a field by its kind, such as `color`, and a fill of it is refused with a sentence, so that a model says so early and leaves that part to a person. The first live run showed the same for a `datetime-local` field, and a later change builds it, as D15 says |
| D15 | A `fill` of an `input` of type `date`, `datetime-local`, `time`, `month`, or `week` sets its value, in the form that the value of the field takes: `2026-03-09`, `2026-03-09T14:30`, `14:30`, `2026-03`, and `2026-W11`. The page list gives such a field its kind as the role and `form=YYYY-MM-DDTHH:MM` and the like on its line. The prompt (web 4) gives the forms and says that a typed date is the same at every run, so a criterion that depends on the present time needs a date far in the past or far in the future. A `color`, a slider, and a file are still refused | Key events do not reach the segments of these fields in a way that holds from one browser to the next, so the engine sets the value through the native setter of the element, focuses the field, and sends `input` and `change` events that bubble. The setter is the one of the prototype, so a framework that wraps the setter of the instance still sees the change. A field that does not keep the text, such as an empty value or another one afterwards, ends the step with an error that names the form. The events of a script are not trusted, so the recorder does not hear them: the explorer asks the recorder to send the fill, and the same step replays by the same code. A person who sets such a field is recorded by Plan 28 as one `fill` |

## Contract

```text
$ semar plan --provider claude --base base-main
...
web: 2 criteria explored, 1 test written, 1 left for a person, 22 turns, $0.12
web: left for a person: docs/tickets/SHOP-482.md:13: the card colour is a color input that cannot be typed into, so the colour of the card cannot be shown on this page.
```

A check that the page does not meet is listed with what the page shows, for example:

```text
web: SHOP-482-03 fails now: due-date should show "2026-12-01" and the page shows "2026-12-02"
```

The scenario of a flow is the one in the Measured section. Every expected result is `by model` and cites its criterion.

## Trust

| Risk | Answer |
| --- | --- |
| A page tells the model what to do | The prompt calls every text of the page data. The model has no tool. Its answer can only name an action on a reference of the list, and the schema lists the references |
| A flow changes data that it should not | A flow runs only on a test environment that allows test data. The browser is judged by the effects policy, host by host. Fixtures are cleaned up |
| A password reaches the model or a file | The list never reads the value of a password field, a password field cannot be checked, and the sign-in is Plan 29's, in code |
| A test is true whatever the product does | A check expects the text of the criterion, and not what the page shows. A check that the page does not meet is listed as "fails now" and replays as `failed` |
| A wrong test is kept | The replay must end as the recording predicts, or the test is removed |
| A model writes a test that counts | Every expectation is by `model` and needs a person's confirmation. Nothing is approved |

## As built

* **The list.** `internal/pagelist` is the shape (`Item`, `Element`, `List`) with the 150 lines and the 80 characters, and no browser. `internal/engine/web/pagelist.js` reads the page. `web.Explorer` is `NewExplorer(recorder)` with `List`, `Click`, `Fill`, `Press`, `Drag`, and `Check`, and `RunID` for `{{run.id}}`.
* **The input.** `internal/engine/web/webinput` has the mouse and key calls that the replay engine, the explorer, and the tests of the recorder share. `webtest.Driver` finds an element by a selector and sends through it.
* **The recorder.** `RecorderOptions.Policy` guards the tab. `Recorder.Settle` waits for the page, with no request open when guarded. `Recorder.Reopen` starts the recording over at an address. `watchJS` is in every window.
* **The turn.** `plan.TurnSchema`, `DecodeTurn`, `TurnInput.Prompt`, and `runState.turnModel` in `webturn.go`. The prompt is `internal/prompt/web.md`, version 1. `plan.RunFlow` in `webflow.go` is the loop of a model function and a browser. `plan.WebExplorer` in `webexplore.go` is the seam that `internal/cli` gives, with `exploreWeb` and `exploreOne` for the order, the ladder, and the numbers.
* **The files.** `record.Options.Claims` and `Draft.FailsNow`, and `record.Written.Undo`. `internal/cli/webexplore.go` is the explorer, with `planWeb` for the decision of D2. `startRecording`, `persistDraft`, and `replayScenario` in `record.go` are shared by `semar record` and the explorer.
* **The plan.** `Output.WebTests`, `Draft.WebTests`, and `CriterionOutcome.Web`. The criteria table says `web test`, and the accounting cites the criterion by the test. `PipelineStats.Web` and `WebUses` hold the part, and `usage.PipelineWeb` is the pipeline of its records.

### Tests

* `internal/pagelist`: the format, the cuts, the 150 lines that keep what a person can act on, a secret value, and a role that adds a line.
* `internal/engine/web` with a real Chrome: the page list of a page with every kind of element, hidden ones, a password, and 150 lines; each action on a reference as the same event that a person makes; a drag; a check that the page does not see; an element out of the view; every refusal in plain words; `{{run.id}}`; a password that is never read back; the guard against another host; settle after a slow request; reopen.
* `internal/stage/plan` with a scripted model and a scripted browser: a flow of two turns, an action that fails and is reported, `given` first and `given` later, the limits, no check, a model that says manual, no usable answer, a cost limit, a cancel; the schema, the decoding, and the prompt; a turn as a job that counts apart; the exploring of a plan: the order and the numbers, a mobile check left alone, the next model, one at a time, "fails now", the cost limit, and a failed plan that removes the tests of its flows.
* `internal/record`: the claims, "fails now", spaces, variables in a claim, and `Undo`.
* `internal/cli`: the explorer with a real Chrome: a test that replays and runs as a recorded one does, a claim that the page does not meet, a flow that ends as manual and writes nothing, a replay that differs from the recording and removes the test, and fixtures that are made and cleaned up. A plan of one API criterion, one web criterion, and one Android criterion that writes the API scenario and the web test, lists both in `plan.md` and the obligations, and passes the web test after `semar review`. The reasons for not exploring, and the usage file.

## Measured

**The first run (history).** The first measurement, before the date fields were built, wrote 1 web test (a drag, by `haiku` in 11 turns) and left the due date criterion for a person after 11 turns of two models, because its `datetime-local` field could not be typed into. The plan cost $0.19.

**The run with date fields.** The same sample board application and the same tickets: an API, a web page with a sign-in form that keeps its session in `sessionStorage`, and 16 acceptance criteria in all. The provider is Claude Code with the default ladder, `haiku`, then `sonnet`, a new backend, and an empty `.semar` before the run. The numbers are from the output of `semar plan`, `plan.md`, and the usage file, and they are one run, not a mean. Two plans before this one, on the same application, found the first two defects below.

| What | Measured |
| --- | --- |
| Acceptance criteria | 16: 11 answered as API scenarios, 5 as manual checks |
| Web criteria explored | 2, both with `haiku` |
| Web tests written | 2: the due date criterion, and the drag criterion |
| Left for a person | 0 criteria. The due date test names in `unproved` what its checks do not prove, and a manual check stays for it |
| Turns of the web part | 22, no correction, no turn by `sonnet` |
| Time | 3 minutes 25 seconds for the plan |
| Cost | $0.10 for the plan, of which $0.03 for the web turns. The three plans of this measurement cost $0.54 in all, as the usage file counts them |
| After `semar review` | `semar run` passed 12 scenarios of 13, the due date test among them: `SHOP-482-02  passed`. The one that failed is the drag test: its model wrote `Card A SHOP-482 {{run.id}}` as the expected text of a card, and the page shows `Card A {{run.id}}`. The plan had listed it under "fails now" |
| The page made wrong | In a copy of the sample, the label of the badge was changed from `Overdue` to `Late`. `semar run --scenario SHOP-482-02` ended `failed` with exit code 1: `step click-save-2: expected element span-3 equals Overdue, observed Late (source ticket)` |

What the live runs of this work found, and the structure that now prevents each:

* **A check of something that is gone.** In the first plan the model checked, after a clear of the due date, that the title of the card was still there, twice. That is true whatever the product does. The prompt now says that a check cannot show that something is gone, and to name it in `unproved`. The next plan did so: its `unproved` says that the test did not prove the absence of the badge for a future date or the clearing of the date.
* **Half of a text.** A model wrote `Jan 1, 2099, 9:00 AM` for an element that shows `Due Jan 1, 2099, 9:00 AM`, and the plan listed both checks under "fails now". The prompt now says that the check compares the whole text of the element, so `expect` holds the words around the value, read from the list after the cause.
* **A browser that keeps the page.** The browser profile of an account keeps the scripts of the application. A copy of the sample that was changed for a moment was served and still ran the old script for a few minutes, so a run on the wrong page passed. The browser cache of the profile was cleared in the measurement. A run that must see a changed page needs a profile with no cache, or a server that sends `Cache-Control`.

The due date test that the model wrote, as it stands in the repository, with the names renamed:

```gherkin
Feature: Due dates and overdue cards

  @id:SHOP-482-02 @ticket:SHOP-482 @surface:web
  Scenario: Card shows its due date and a red Overdue badge for a past due date
    Before this run, a card had no verified due date or Overdue badge on the board. Now a card created in this run shows its due date as set in the editor (Jan 1, 2000, 9:00 AM) and shows the Overdue badge, as the criterion requires for a past due date.

    Given Open /
    # step: open
    When Type "Board {{run.id}}" in new-board
    # step: fill-new-board
    And Click new-board-2
    # step: click-new-board-2
    And Type "Todo {{run.id}}" in add-list
    # step: fill-add-list
    And Click add-list-2
    # step: click-add-list-2
    And Type "Due check {{run.id}}" in add-card
    # step: fill-add-card
    And Click add-card-2
    # step: click-add-card-2
    And Click due-check-run-id
    # step: click-due-check-run-id
    And Type "2099-01-01T09:00" in due-date
    # step: fill-due-date
    And Click save
    # step: click-save
    Then the element span equals "Due Jan 1, 2099, 9:00 AM"
    # ticket docs/tickets/SHOP-482.md:13 Web: the card shows its due date, and a red "Overdue" badge when `overdue` is true, by model
    When Click due-check-run-id
    # step: click-due-check-run-id-2
    And Type "2000-01-01T09:00" in due-date
    # step: fill-due-date-2
    And Click save
    # step: click-save-2
    Then the element span-2 equals "Due Jan 1, 2000, 9:00 AM"
    # ticket docs/tickets/SHOP-482.md:13 Web: the card shows its due date, and a red "Overdue" badge when `overdue` is true, by model
    And the element span-3 equals "Overdue"
    # ticket docs/tickets/SHOP-482.md:13 Web: the card shows its due date, and a red "Overdue" badge when `overdue` is true, by model
```

Read as a QA would. The test makes its own board, list, and card with the ID of the run, so it runs again. It uses a date far in the future and then a date far in the past, as the prompt says, so that the result does not depend on the day of the run. The first check proves that the date that was typed is shown, and the third that a past date has the badge, which the product could get wrong. Its weak parts are the locators `span`, `span-2`, and `span-3`, which are short CSS paths in the page, and a check that the first date has no badge, which a check cannot show. A person fixes the first in the file and keeps the manual check for the second. The ID of the scenario here is the number of its ticket in the sample, renamed.

The first run's drag test, which a person also reads:

```gherkin
Feature: Move a card to another list

  @id:SHOP-482-01 @ticket:SHOP-482 @surface:web
  Scenario: Dragging cards moves them across lists and reorders them in place, without a reload
    Before these drags, the board had Card 1 and Card 3 in List B and Card 2 in List A, with no drag between them. Now Card 1 was dragged into List B, and Card 3 was dragged onto Card 1's place, so the page shows Card 3 first and Card 1 second in List B, with no reload.

    Given Open /
    # step: open
    When Type "Board {{run.id}}" in new-board
    # step: fill-new-board
    And Click new-board-2
    # step: click-new-board-2
    And Type "List A {{run.id}}" in add-list
    # step: fill-add-list
    And Click add-list-2
    # step: click-add-list-2
    And Type "Card 1 {{run.id}}" in add-card
    # step: fill-add-card
    And Click add-card-2
    # step: click-add-card-2
    And Type "Card 2 {{run.id}}" in add-card
    # step: fill-add-card-2
    And Click add-card-2
    # step: click-add-card-2-2
    And Type "List B {{run.id}}" in add-list
    # step: fill-add-list-2
    And Click add-list-2
    # step: click-add-list-2-2
    And Drag card-1-run-id to list-b-run-id
    # step: drag-card-1-run-id-list-b-run-id
    Then the element button equals "Card 1 {{run.id}}"
    # ticket docs/tickets/SHOP-482.md:16 Web: dragging a card onto another list or another place in a list moves it, and the board shows the new order without a reload, by model
    When Type "Card 3 {{run.id}}" in add-card-3
    # step: fill-add-card-3
    And Click add-card-4
    # step: click-add-card-4
    And Drag card-3-run-id to card-1-run-id-2
    # step: drag-card-3-run-id-card-1-run-id-2
    Then the element button-2 equals "Card 3 {{run.id}}"
    # ticket docs/tickets/SHOP-482.md:16 Web: dragging a card onto another list or another place in a list moves it, and the board shows the new order without a reload, by model
```

Read as a QA would. The test makes its own board, lists, and cards with the ID of the run in each name, so it runs again. Its two checks prove something that the product can get wrong: after the first drag the first card of the second list is the card that was moved, and after the second drag the card that was dropped on it comes first. A page that drops to the wrong place fails the second check, as the row above shows. Its weak parts are the story in the intent, which does not match what the test does, and the locators of three elements, which are short CSS paths such as `section:nth-of-type(2) > ul > li > button`, the last choice of the recorder. A person fixes both in the file.

What the five live runs before this one found, and the structure that now prevents each:

* **Stale references.** A page that has just been clicked had not started its request when Semar read it, so the model chose an element that the page then replaced. A page now gets 300 milliseconds after an action before it is asked whether it is still.
* **A field that cannot be typed into.** A `datetime-local` field kept none of the text of two models, and each said so after four turns. The list then gave such a field its kind and a fill of it was refused at once with a sentence. A later change builds the fill (D15), and the refusal stays for a `color`, a slider, and a file.
* **A whole criterion left to a person.** A criterion with a part that the page cannot show, such as the refusal of a server, ended as `manual` on both models. `done` now carries `unproved`, and the plan keeps a manual check for that part.
* **A test that did not run again.** A model used a board that an earlier run had left, and its check named a run ID that the replay did not have. The prompt now tells the model to make its own data, and the run ID is a value of every flow, so the typed text, the locators, and the text of a check keep the variable. Before this, a flow with no fixture wrote the literal ID.
* **A check that proves nothing.** A model checked the label "Due date" and a "Clear due date" button, and the test listed a mismatch that only a wrong element caused. A check on an element with no text is refused, and the prompt says that a label or a control is not a result.
* **Numbers.** A flow that proved part of a criterion left the number of its test out of the numbers of the plan, so an API scenario took the same number and no plan was written. The plan then removed the web test, as it does for every plan that fails, and the next run numbered correctly. A test now covers it.
* **Records of another run.** Found on 2026-11-18 with the work of Plans 31 to 33. The flow of a higher model started on the page that the flow of the lower model left behind, used its board, and wrote a test whose locator held the ID of that flow. The test passed at the plan and was inconclusive at the next run. `record.Build` now refuses a recording that still names a run after the values of its own run became variables, and the page list of a flow leaves out each element that names another run. `plan.md` also says why the flow of a lower model wrote no test, because the person pays for the higher one.
* **Dates.** Plan 31 gives a flow the date variables. A flow types `{{date.plus1d}}` or `{{local.plus1d}}` for a date near today, and a fixed date at least 10 years away when the page must show the date in its own words.

## Not built

* A mobile screen, a flow across two tabs, a file upload, a hover, and a choice in a select list, which the recorder does not hear.
* A `color` field and a slider, which a flow cannot type into. The clearing of a date or time field is not written as a step, as for a text field, and a flow clears it with the button of the page.
* An action that opens an address. A flow starts at the first page of the service and moves by clicks.
* More than one service with a web page in one plan: the first is explored.
* The count of web criteria in the estimate before a plan.
* A status line while a flow runs. The plan prints the web part when it ends.
