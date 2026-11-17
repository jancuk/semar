# Plan 28: Record a web test

* **Status:** Planned.
* **Needs first:** Plans 17, 22, and 24.
* **With:** Plans 26 and 27.

## Goal

A person makes a web test by doing the flow once in Chrome. Semar writes the screens file, the script, and the scenario. Nobody writes a locator by hand.

Plan 22 runs web steps, and a person writes them: one screens file with a locator for each element, one script, and one scenario. On the measured first run these were three of the six hand-written files, and the slowest ones. The planner does not draft web steps, because a model that cannot see the page invents locators. A recording has the page.

## Decisions

| # | Decision | Reason |
| --- | --- | --- |
| D1 | `semar record <scenario id> [environment]` opens a Chrome window on Semar's own profile of the environment and the account (Plan 22, D9), at the address of the service, and records what the person does until the window closes. `--as <account>` names the account, `--service` the service when there are several, and `--open <path>` the first page. It refuses a production environment and an environment that is read only | A recording clicks and types, so it writes. The profile rule of Plan 22 holds: never the browser that the person uses |
| D2 | The recorder is a script that Semar adds to every page of the window before the page's own scripts run, with one binding that sends events to Semar. It records a click, the text of a field when the field loses focus or Enter is pressed, the Enter and Escape keys, a drag from one element to another, and a move to another address that the person typed or that `--open` gave. A move that a click caused is not a step | These are the actions that the engine of Plan 22 replays: `open`, `click`, `fill`, `press`, and `drag` |
| D3 | A click with the Option key (Alt) records a check and does nothing on the page: the event is stopped before the page sees it, and the element shows an outline for a moment. The check is `the element <name> equals "<text>"` with the text that the element shows then. A recording with no check writes nothing, and says that a test needs at least one check and how to record one | A test with no expected result proves nothing. The person shows what must be true by pointing at it |
| D4 | The recorder chooses a locator for each element in this order: a test ID (`data-testid`, `data-test`, `data-qa`), the role with the name, the text, and last a short CSS path. A role with a name is used only when exactly one element of the page matches it then, and the same holds for a text. The element gets a name from the test ID, the name, or the text, in lower case with hyphens, and a number when two would be the same | The order is from what a redesign changes least to what it changes most, as Plan 22 chose for a person |
| D5 | A password field is never read. The recorder sends that a secret was typed and nothing of its value, and the step is written with `secret: env:<NAME>`. `--secret-env <NAME>` gives the name, and on a terminal Semar asks for it after the window closes | A password must not reach a file, a log, or the binding |
| D6 | `--given <fixture>[ as <alias>]` makes the fixture before the window opens, and the path of `--open` may use its outputs. When the recording ends, every value that the fixture gave, and the run ID, is replaced by its variable wherever it occurs in a path, a typed text, a name, a CSS path, or the text of a check: `/boards/b_81` becomes `/boards/{{b.board}}`. The scenario gets the `Given the fixture` line. The fixture is cleaned up after the recording | Without this a recording replays once and then fails on data that no longer exists. This is what makes a recorded test repeatable |
| D7 | Semar writes three things when the window closes: the screens of the recording into `.semar/screens/<app>.yaml`, the scenario file with one `When` or `And` line for each action and a `Then` line for each check, and the script through the same code as `semar script add`. A screen is named after the title of the page, or the path when there is no title. A screen or an element that exists is kept: a new element is added to its screen, and an element with the same name and another locator gets a number. `--ticket` sets the ticket, and else it is the group of the ID | A second recording must not break the tests of the first |
| D8 | After it writes, `record` replays the scenario once, with no window, and prints the outcome. A scenario that does not replay is still written, with the step that failed, so that the person can fix it or record again. `--no-replay` skips it | The person learns that the recording works before leaving it, and not in the next run of a pipeline |
| D9 | Nothing that `record` writes is approved. The screens file and the scenario go through `semar review` (Plan 26). `record` ends with that command. `--covers <use case id>` only prints the command `semar check <use case id> --by <scenario id>` | An approval is given by a person who read the result, and a recording made in a minute can hold a mistake |
| D10 | An existing scenario ID is refused unless `--replace` is set | A recording must not overwrite a confirmed test without a word |

## Contract

```text
$ semar record SHOP-482-07 --given order.paid as o --open '/orders/{{o.id}}'
Made the fixture order.paid.
A Chrome window is open at https://shop.uat.example.com/orders/o_4471
  Do the flow. Hold Option and click what must be true. Close the window to finish.

Recorded 4 actions and 2 checks.
Wrote .semar/screens/shop.yaml (2 screens, 5 elements)
      .semar/scenarios/SHOP-482/SHOP-482-07.feature
      the script of SHOP-482-07
Replayed once: passed.

Next: semar review SHOP-482-07
```

The scenario:

```gherkin
  @id:SHOP-482-07 @ticket:SHOP-482 @surface:web
  Scenario: Recorded on 2026-11-17: Order o
    Given the fixture order.paid as o
    And Open /orders/{{o.id}}
    When Click refund
    And Type "5" in amount
    And Click confirm-refund
    Then the element refund-total equals "5.00"
    And the element status equals "partly refunded"
```

The person changes the title and the wording of the steps in the file. The step IDs in the comments tie them to the script.

## Tasks

Each task is test first, and one commit. The tests of the recorder run a real Chrome against a local test page and send real input through the browser protocol, as the tests of Plan 22 do.

1. **The recorder script and the binding.** D2: click, fill, press, and open, with the events that reach Semar.
2. **Drag.** D2.
3. **Locators and names.** D4.
4. **Checks.** D3.
5. **Secrets.** D5, with a test that the value is in no event, file, or log.
6. **Fixture values.** D6.
7. **Writing.** D7: the merge into a screens file, the scenario, and the script.
8. **The command.** D1, D8, D9, and D10.
9. **Documents.** The README, the PRD and TRD, and this plan as built.

## Done when

On the sample application's web page, a test drives a recording through the browser protocol (two fills, a click, a drag, and two checks), and the scenario that `record` wrote replays as `passed`. With one element changed in the page so that a check is wrong, it replays as `failed`. `go test ./...` and `go vet ./...` pass.

## Not built

* Recording on Android.
* A recorded wait. The engine waits for an element as Plan 22 says.
* A check other than the text of an element: a count, a colour, or that an element is absent. A person adds them in the file.
* Hover, a file upload, and a second tab.
