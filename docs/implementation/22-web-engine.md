# Plan 22: The web engine and web legs

* **Status:** Implemented. This text describes the system as built. It supersedes [Plan 09](09-web.md).
* **Needs first:** Plans 17, 18, 19, and 21.
* **Implements:** Milestone M2 in part: the web engine, web legs of journeys, web scenarios, screen objects, a Chrome profile of Semar's own, and deterministic repair. PRD decisions D8 and D61 are narrowed as D1 below says. TRD sections "Web engine", "Recording and replay", "Screens and UI legs", "Journeys", and "Signing in".

## Goal

Semar drove APIs only, so every web check of a release was done by a person. A QA engineer who tests a kanban board by hand opens the board in a browser, signs in, drags a card from one column to the next, and reads the screen. This plan lets a journey leg do that, with no model on replay.

A leg with `surface: web` opens pages, clicks, types, presses keys, drags, and reads the screen. A step names an element through a screen object, and a person approves the screen objects. When a locator no longer finds its element, the leg ends `inconclusive`, and Semar proposes a repair that a person approves. The checks that a leg or a scenario makes about a screen read the recorded text of the page, never a description of it.

## Decisions

| # | Decision | Reason |
| --- | --- | --- |
| D1 | The web engine is an in-process Chrome DevTools Protocol client in `internal/engine/web`, built on `github.com/chromedp/chromedp` (MIT), pinned to one exact version in `go.mod`. The earlier plan to adopt another tool's engine, by import, by its daemon, or by a copy, is dropped, and so are spikes S6 and S8 | Semar needs a few things of a browser: real input events, the accessibility tree, a request interceptor, and a screenshot. One maintained Go library gives all of them, with no second process and no private protocol between two programs of different versions |
| D2 | One step is one action, and the scenario and the script stay apart. `domain.ScriptStep` gains `Web *WebAction` beside `Request`, and exactly one is set. `domain.LegStep` gains `Web *WebAction` as the alternative to `Request`. The expected results stay in the scenario, so a script can never change an expectation | The split of Plans 01 and 18 is the reason a replay can be trusted. A web step must not be the one place where it is weakened |
| D3 | A `WebAction` has `do` (`open`, `click`, `fill`, `press`, `drag`, or `wait`), `path` (for `open`, relative to the base URL of the service, may hold `{{variables}}`), `screen`, `element`, `to` (the element that `drag` ends on, on the same screen), `value` (for `fill` and `press`, may hold variables), and `secret` (for `fill` only: a secret reference such as `env:SHOP_PASSWORD`, resolved by `internal/secret`, typed by the engine, and redacted everywhere). A `WebAction` also carries `service` and `app`, which a leg sets from its own `service` and `app`, as a request carries its `service` | The script has to say which base URL and which screens file a step belongs to, as the scenario of a leg is built from the leg alone |
| D4 | Elements are named through screen objects. `.semar/screens/<app>.yaml` has `apiVersion: semar/v1`, `kind: Screens`, `app`, and `screens: {<Screen>: {<element>: <locator>}}`. A locator has `testId`, `role`, `name`, `text`, `css`, and `within`. Every field may hold `{{variables}}`, such as a card whose name is `{{title}}`. `within` names another element of the same screen and limits the search to it. The engine tries `testId` (the `data-testid` attribute), then `role` with the accessible name from the accessibility tree, then the exact visible text, then `css`. A locator must match exactly one visible element. Zero or several is "not found", with the number | This is rule 4 of "Screens and UI legs" in its web form. A step in business words stays readable, and a change of the page is one edit in one file |
| D5 | A leg has `app`, the name of its screens file. A leg with `surface: web` uses the leg's `service` for the base URL, and the leg's `as` or a step's `as` for the browser profile. A leg scenario takes its `surface` from the leg. A scenario in `.semar/scenarios` with `surface: web` runs through the same replay runner: `Runner.steps` has one dispatch on the kind of step, and no second path | A web leg that ran beside the API replay would need its own approval, evidence, and verdict code, and the two would drift |
| D6 | A check gains `element` as a fourth subject beside `status`, `jsonpath`, and `header`, with `equals`, `notEquals`, and `contains` on the text of the element, `exists` (visible or not), and `count` (the number of matches). A capture gains `element` as the alternative to `jsonpath` and captures the text. A check or a capture has exactly one subject. The hash of the expected results covers `element` only when it is set | A leg must be able to say that the card shows in Doing, and to hand the card's title to an invariant |
| D7 | After every web action the engine waits for the page to settle: the document is loaded, no request is open, and the page has not changed for a short time. It then reads every element that the checks and the captures of the step name, and records the state in the action. A check reads that record and nothing else | Code compares the text of the snapshot, never a model's description of it (rule 2). A check that reads before the page settled fails at random |
| D8 | `click`, `fill`, `press`, and `drag` are writes. `open` and `wait` are reads. A write is refused when the effects mode of the environment is `read_only`, and the action is recorded with `refused` set, as the API engine does. The browser fails every request to a host that the environment does not list, through the interception of the DevTools Protocol, and adds a W3C `traceparent` header to the requests that it allows. In a read-only environment it also fails every request that is a write. The engine has the request count limit of the API engine, counted in actions. A secret never appears in an action, a report, or a log | The browser makes requests that no step names: page resources, background calls, and redirects. They are bound by the same policy as a step |
| D9 | Semar has its own Chrome profile for each environment and account, at `browser/<environment>/<account>` below the Semar home (`$SEMAR_HOME`, else `$XDG_CONFIG_HOME/semar`, else `~/.config/semar`). It never uses the profile that a person browses with and never attaches to a Chrome that is running. `semar login <service> --env <environment> [--as <account>]` opens a window on that profile at the base URL of the service, so that a person signs in once, and returns when the window closes. A script can also sign in with `fill` and `secret`. A run is headless, and `--headed` on `semar run` and on `semar journey run` shows the window. When no Chrome is found, a web step ends `blocked` with "Chrome was not found: install Chrome or set web.chrome in semar.yaml". An environment may have `web: {chrome: <path>}` | The cookies of a person's own browser are the keys to every service that the person uses. A profile of Semar's own holds only the accounts of the test environment |
| D10 | A screens file must be approved by a person before a step uses it. `semar confirm --screens <app>` shows every element with its locator and stores an approval under `.semar/store/approvals/screens`, keyed by the hash of the file's content. A step that uses a screens file with no approval for its current hash ends `inconclusive` with "the screens of <app> are not approved: semar confirm --screens <app>". The hash covers the content and not the layout, so a comment or a blank line changes nothing | A locator decides what a check looks at. A repository can be changed by anyone, so a person must see a change of a locator, as a person sees a fixture (rule 8) |
| D11 | A locator that does not find its element ends the step, and so the leg, `inconclusive` with "an element was not found: either the product changed or the script is out of date". It is never `failed`. A check of `exists` or `count` is the way to say that an element is absent, and it can fail. Semar cannot tell a moved button from a removed one, so a person decides | Rule 5 of "Screens and UI legs" |
| D12 | Repair is deterministic and uses no model. When an element is not found, the engine reads the accessibility tree and records up to three candidates for that element in `web.repair`: the same role with the closest name, the same name with another role, or the same text. Each is a full locator with the reason. `semar screens repair <run-id>` prints, for each element that was not found, the old locator beside each candidate. `--apply <screen>.<element>=<n>` rewrites that element in the screens file and keeps the rest of the file. The hash changes, so `semar confirm --screens` is needed again, and its output says whether the role and the name stayed the same ("followed a moved element") or changed ("the element changed: check it is the same thing"). A repair can never touch a scenario, an expectation, or a step | The cheapest repair is a lookup in a tree that the engine already has. A model that proposes a locator would also have to be trusted to keep the expectation |
| D13 | Each web action leaves one PNG screenshot in the run directory, under `screenshots/`, named by the action. The action refers to it with a path relative to the run directory, and `report.md` links it and shows the action text of the step beside the operation and the target | A person who reads a failed web leg wants to see the page |

## Trust

| Risk | Answer |
| --- | --- |
| A page that the test opens links to another host and loads a script from it | The browser fails the request. The action lists the hosts that it blocked, with no path |
| A locator in a screens file is changed so that a check reads a harmless element | The approval is keyed by the hash of the content. A changed file is not approved until a person has seen the new locators |
| A repair swaps a button for another | `--apply` writes the file but does not approve it. `confirm --screens` says that the role or the name changed |
| A secret typed into a field reaches the report | The engine never writes the typed value. The text that it reads from the page goes through the redactor with the other texts of the action. A password input is never read |
| A write is sent to a read-only environment by a page | The action is refused in the engine, and the browser fails a write request of the page |
| The browser holds a session of a person | The profile is Semar's own, per environment and account. No setting points Semar at another profile |

## Contract

### Journey and script files

```yaml
legs:
  - id: web_read
    surface: web
    service: board
    app: lanes
    as: alice
    needs: [move]
    steps:
      - id: open
        action: Alice opens the board in the browser
        web: { do: open, path: "/" }
      - id: read
        action: Read the title of the card that Bob moved
        web: { do: wait, screen: Board, element: doing-card }
        expect:
          - check: { element: doing-card, equals: "{{move.title}}" }
            source: person
        captures:
          - { name: shown, element: doing-card }
    outputs: [shown]
```

* A web leg has no `hint` and no `wait`. Its steps have no `request`.
* `screen` and `element` of a step name one entry of the screens file of the leg's `app`. `to` names a second entry of the same screen.
* A script step has `web:` in place of `request:`. A script step of a scenario also has `service` and `app` in its `web:`.

### Screens file

```yaml
apiVersion: semar/v1
kind: Screens
app: shop
screens:
  Cart:
    place-order: { role: button, name: Place order }
    line:        { css: "li.line", within: cart-list }
    cart-list:   { testId: cart }
```

### Configuration

* `environments.<name>.web.chrome` (optional): the path of the Chrome program.

### Commands

* `semar login <service> --env <environment> [--as <account>]`
* `semar run --headed`, `semar journey run --headed`
* `semar confirm --screens <app>` (repeatable)
* `semar screens repair <run-id> [--apply <screen>.<element>=<n>]`

### The record of an action

`domain.Action` has `web`, set for a web action, with `url`, `op`, `target` (the locator after the variables), `elements` (for each element that a check or a capture names: `found`, `count`, `text`, `visible`, and the locator that was looked up), `screenshot`, `repair`, and `blocked`. `Found` is true when exactly one visible element matched. `Count` is the number of matches, visible or not.

## Rules

| # | Rule |
| --- | --- |
| W1 | A step has exactly one of `request` and `web`. A leg step with `web` is valid only in a leg with `surface: web`, and a leg step with `request` only in one with `surface: api` |
| W2 | Every message that a person reads names a file, a leg or a step, and a field in plain words: no Go type name |
| W3 | A step that names a screen or an element that the screens file lacks is a load error with the names that the file has |
| W4 | The expected results of a scenario cannot be changed by a script, by a screens file, or by a repair |
| W5 | A web action is recorded whatever its outcome, so that a refused or a failed action is evidence |
| W6 | A test never writes to the real Semar home or to the keychain of the machine. Chrome runs with a mock keychain and a password store of its own kind |

## Not built

* The engine gateway and the engine registry, packs, and the conformance suite.
* The MCP tools `web_*` and an agent that authors web steps.
* A pilot, and a decision model that picks the next click.
* The planner drafting web steps. A person writes them, or a person writes the screens file.
* Known interruptions: permission dialogs, update prompts, and banners.
* Object storage for evidence.
* Android legs and the Android engine.
* A session cookie with no expiry date does not survive the end of a browser. A service that needs it keeps the person's session in storage with a date, or signs in again with `fill` and `secret`.
