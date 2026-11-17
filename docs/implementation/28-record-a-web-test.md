# Plan 28: Record a web test

* **Status:** Implemented. This text describes the system as built.
* **Needs first:** Plans 17, 22, and 24.
* **With:** Plans 26 and 27.
* **Implements:** PRD decision D72 and FR-361 to FR-364. TRD section "Recording a web test".

## Goal

A person makes a web test by doing the flow once in Chrome. Semar writes the screens file, the script, and the scenario. Nobody writes a locator by hand.

Plan 22 runs web steps, and a person writes them: one screens file with a locator for each element, one script, and one scenario. On the measured first run these were three of the six hand-written files, and the slowest ones. The planner does not draft web steps, because a model that cannot see the page invents locators. A recording has the page.

## Decisions

Each row says what was built. Where it differs from the plan as first written, the last sentence of the reason says how and why.

| # | Decision | Reason |
| --- | --- | --- |
| D1 | `semar record <scenario id> [environment]` opens a Chrome window on Semar's own profile of the environment and the account (Plan 22, D9), at the address of the service, and records what the person does until the window closes. `--as <account>` names the account, `--service` the service when there are several, and `--open <path>` the first page. It refuses a production environment and an environment that is read only. The account is `--as`, else the auth profile of the service, else `default`, as `semar login` chooses it. The scenario names that account as its auth profile when the configuration has it, and sends no session of an API when it does not | A recording clicks and types, so it writes. The profile rule of Plan 22 holds: never the browser that the person uses. Changed: `--app <name>` names the screens file, because the plan did not say where its name came from. The default is the name of the service |
| D2 | The recorder is a script that Semar adds to every page of the window before the page's own scripts run, with one binding that sends events to Semar. It records a click, the text of a field when the field loses focus or Enter is pressed, the Enter and Escape keys, a drag from one element to another, and a move to another address that the person typed or that `--open` gave. A move that a click or a form caused is not a step. It records the top page only | These are the actions that the engine of Plan 22 replays: `open`, `click`, `fill`, `press`, and `drag`. Changed: a click that puts the cursor in a text field is not a step, because a `fill` focuses its field itself. Enter is a step only in a text field, and the click that a form makes on its button when Enter is pressed is part of the Enter. A text is always sent before the key, the click, the drag, or the check that follows it, so that a replay types before it acts |
| D3 | A click with the Option key (Alt) records a check and does nothing on the page: the events of the click are stopped before the page sees them, and the element shows an outline for a moment. The check is `the element <name> equals "<text>"` with the text that the element shows then, read as the engine of a replay reads it. A recording with no check writes nothing, and says that a test needs at least one check and how to record one | A test with no expected result proves nothing. The person shows what must be true by pointing at it |
| D4 | The recorder chooses a locator for each element in this order: a test ID (`data-testid`, `data-test`, `data-qa`), the role with the name, the text, and last a short CSS path. A test ID counts when one visible element has it. A role with a name is used only when exactly one visible element of the page has that role and that name, and the same holds for a text. The element gets a name from the test ID, the name, or the text, in lower case with hyphens, and a number when two would be the same | The order is from what a redesign changes least to what it changes most, as Plan 22 chose for a person. Changed three ways. First, the engine reads only `data-testid` as `testId`, so `data-test` and `data-qa` are written as a CSS attribute selector, such as `[data-qa="total"]`. Second, a check never finds its element by the text that it reads: a role whose name is the text of the element, and a text, are skipped for a check, because a locator that follows the text would end the check `inconclusive` when the text is wrong, and not `failed`. Third, a role with a name is used only for the roles whose name the recorder can compute as the browser does (button, link, text field, checkbox, radio, heading, tab, menu item, switch, and spin button), and the CSS path has two tags at least, because one tag breaks when the page gets another of it |
| D5 | A password field is never read. The recorder sends that a secret was typed and nothing of its value, and the step is written with `secret: env:<NAME>`. `--secret-env <NAME>` gives the name, and on a terminal Semar asks for it after the window closes | A password must not reach a file, a log, or the binding. Changed: a field with `autocomplete` of `new-password` or `current-password` counts as a password, and an event names the path of its page and no query or fragment, so that a form that sends a password in the address does not put it in every event after it |
| D6 | `--given <fixture>[ as <alias>]` makes the fixture before the window opens, and the path of `--open` may use its outputs. When the recording ends, every value that the fixture gave, and the run ID, is replaced by its variable wherever it occurs in a path, a typed text, a name, a CSS path, or the text of a check: `/boards/b_81` becomes `/boards/{{b.board}}`. The scenario gets the `Given the fixture` line. The fixture is cleaned up after the recording | Without this a recording replays once and then fails on data that no longer exists. This is what makes a recorded test repeatable. Changed: a value is replaced only where it stands as a whole word, longest value first, and a value of one or two characters is left alone, because an amount of 5 is as likely to be typed text as an ID. A value is also cut out of the title of a page before the title names a screen. The alias is the first word of the fixture ID when there is none, and `--as` is needed when the fixtures need an account that the service does not name |
| D7 | Semar writes three things when the window closes: the screens of the recording into `.semar/screens/<app>.yaml`, the scenario file with one `When` or `And` line for each action and a `Then` line for each check, and the script through the same code as `semar script add`. A screen is named after the title of the page, or the path when there is no title. A screen or an element that exists is kept: a new element is added to its screen, and an element with the same name and another locator gets a number. `--ticket` sets the ticket, and else it is the group of the ID | A second recording must not break the tests of the first. Changed: a screen name is made of letters, digits, and hyphens, and it is `Home` when the title and the path give none. An element that the file has under another name keeps that name. The file is extended in place: an element goes below the last element of its screen and a new screen below the last screen, and every other byte stays. The leading `open` steps are the `Given` lines. A check goes under the step before it when that step acted on the same screen. Otherwise, as after a click that moved to another page, a step that waits for the first element of the check stands between them. Two fills of one field in a row give one step, and a field that was emptied gives none. A move to another host is left out, and the output says so |
| D8 | After it writes, `record` replays the scenario once, with no window, and prints the outcome. A scenario that does not replay is still written, with the step that failed, so that the person can fix it or record again. `--no-replay` skips it | The person learns that the recording works before leaving it, and not in the next run of a pipeline. Changed: the replay is `semar run` for that one scenario, and the screens of the recorded app count as approved for that one run, because the engine of Plan 22 refuses screens that no person approved. No approval is stored |
| D9 | Nothing that `record` writes is approved. The screens file and the scenario go through `semar review` (Plan 26). `record` ends with that command. `--covers <use case id>` only prints the command `semar check <use case id> --by <scenario id>` | An approval is given by a person who read the result, and a recording made in a minute can hold a mistake |
| D10 | An existing scenario ID is refused unless `--replace` is set. The check comes before the fixture is made and before the window opens. `--replace` also removes the older file of that ID when it is in another folder | A recording must not overwrite a confirmed test without a word |

## Contract

```text
$ semar record SHOP-482-07 --given order.paid as o --open '/orders/{{o.id}}'
Made the fixture order.paid.
A Chrome window is open at https://shop.uat.example.com/orders/o_4471
  Do the flow. Hold Option and click what must be true. Close the window to finish.

Recorded 4 actions and 2 checks.
Wrote .semar/screens/shop.yaml (1 screen, 5 elements)
      .semar/scenarios/SHOP-482/SHOP-482-07.feature
      the script of SHOP-482-07
Replayed once: passed.

Next: semar review SHOP-482-07
```

The actions are what the person did, and the typed address counts as one. The numbers in brackets are of the whole screens file after the write. A scenario that does not replay prints its outcome, the reasons with their step, and the report. A recording with no check prints `Recorded 3 actions and no check.` on standard output and `semar: nothing was written: a test needs at least one check: hold Option and click what must be true while the window is open` on standard error, and exits with 3.

The scenario:

```gherkin
  @id:SHOP-482-07 @ticket:SHOP-482 @surface:web @auth:support-agent
  Scenario: Recorded on 2026-11-17: Order
    Given the fixture order.paid as o
    And Open /orders/{{o.id}}
    # step: open
    When Click refund
    # step: click-refund
    And Type "5" in amount
    # step: fill-amount
    And Click confirm-refund
    # step: click-confirm-refund
    Then the element refund-total equals "5.00"
    # person
    And the element status equals "partly refunded"
    # person
```

The person changes the title and the wording of the steps in the file. The step IDs in the comments tie them to the script. The source of every result is `person`.

## Trust

| Risk | Answer |
| --- | --- |
| A password reaches a file, a log, or an event | The recorder script never reads a password field. The test of D5 types a password and searches every event, every file under `.semar` and the Semar home, and every output stream for it in the forms that a secret can take |
| A recording changes what a test claims, and nobody sees | `record` approves nothing. The screens file and the scenario go through a review |
| The page sends an event to the binding as the recorder does | The page under test is the page that the person records. A person reviews every file before a test relies on it |
| A recording clicks and types in an environment that must not change | `record` refuses a production environment and a read-only one |
| A fixture of the recording is left behind | The fixture is cleaned up when the window closes, also when the recording is interrupted or writes nothing. A cleanup that fails is reported with what may still exist |
| The files are written through a link | The folders are checked and the files are written below a root at the repository, as the plan writer does |

## As built

* **The recorder** is `web.Recorder` in `internal/engine/web`, with its script in `recorder.js`. It adds the script to every page with `Page.addScriptToEvaluateOnNewDocument` and the binding with `Runtime.addBinding`. A typed address is a navigation that nobody asked for from the page: `Page.frameRequestedNavigation` marks a move that a click or a form caused, and the others are `open` events. `Open` makes a navigation of that kind, so `--open` and an address that the person types take the same path. The window loop is the one of `semar login`, now `waitWindowClosed`.
* **One path for the window and for a test.** `semar record` and the tests call `StartRecorder`, `Open`, `Wait`, and `Finish`. A test sends the input through `internal/engine/web/webtest`: mouse press, move, and release, key events, the Option modifier, and a drag. It reaches the command through a value of the context, and the command closes the window when the test is done, as a person does. Nothing else differs.
* **The events and the files** are in `internal/record`: `Event`, `Build`, `Values`, `Namer`, `ScreenName`, and `Write`. `Build` does no I/O. `screens.New` and `screens.Add` write the screens file. `storeScript` in `internal/cli` is the check and the store that `semar script add` does, now shared with `record`.
* **The run of the fixtures** that `record` makes is stored as a run with the purpose `recording`, so that no listing counts it as a run of scenarios.
* **The element that a recording calls by its text.** The name of an element comes from the test ID, then the name that the browser gives it, then its short text, then its `id`, `name`, or tag. For an element that a check reads it comes from the test ID, then a name that is not its own text, then its `id`.
* **An outline** of two pixels shows for 700 milliseconds.

### Tests

The tests of the recorder run a real Chrome with no window against a local page, and skip when Chrome is absent. Every test uses a temporary Semar home and never the profile that a person browses with.

* `internal/engine/web`: the events for a click, a typed text, Enter, Escape, a typed address, and a click that moves the page; a drag of the browser's own drag and drop and a drag that the page moves with the mouse, with no click after it; the locator and the name of ten kinds of element, each found again by the engine of a replay as exactly one element; the Option click for five kinds of element, with proof that the page saw nothing; a password in two fields, with a search of the events; a text typed before a check.
* `internal/record`: the scenario, the script, and the screens of the contract above, and of a check on another page, a leading open, a drag, a key with no element, two fills in a row, a move to another host, a screens file that exists, and a password; the replacement of values, with whole words and the longest first; the names; the writing, with a second recording, `--replace`, a refused link, and a scenario that Gherkin cannot carry. `internal/screens` has the tests of `New` and `Add`, which keep every other byte of a file.
* `internal/cli`: the proof of the plan. A recording of two fills, a click, a drag, and two checks is driven through the protocol, and the scenario that `record` wrote replays as `passed`. With one element of the page changed so that a check is wrong, it replays as `failed`, and before that a run of it is `inconclusive` because nothing was approved. Also: no check, an ID that exists, `--replace`, a production and a read-only environment, a password in `--secret-env` and on a terminal, a fixture with its values as variables and its cleanup, and no Chrome.

## Not built

* Recording on Android.
* A recorded wait of time. The engine waits for an element as Plan 22 says, and `record` writes a wait for an element where a check is on another page.
* A check other than the text of an element: a count, a colour, or that an element is absent. A person adds them in the file.
* Hover, a file upload, a choice in a select list, a second tab, and a frame inside the page.
* A move of the page that does not load a new document, such as a change of the part after `#`, is not recorded as a step. A typed address with a fragment is.
* A text that holds `{{` is refused before anything is written, with the step, because the engine would read it as a variable.
* The recording of a person who closes the window with a field that they typed in and did not leave can lose that text. The recorder sends it when the page is hidden, which is best effort.
