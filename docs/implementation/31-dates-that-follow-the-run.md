# Plan 31: Dates that follow the run, and no value that expires

* **Status:** Implemented. This text describes the system as built.
* **Needs first:** Plans 17, 25, and 30.
* **Implements:** PRD decision D75 and FR-375 to FR-380.

## Goal

A test that a plan writes today gives the same result next month. A script holds no date that is near the day of the plan, and no ID of a record that nobody created for it.

Before this plan a script could fill in four kinds of value at run time: `{{run.id}}`, a capture, a fixture output, and a `{{data.<key>}}` in a fixture parameter. A date such as "due tomorrow" was typed in as text, so the test changed its meaning the day after. A body field such as `customerId` could hold an ID that the model made up or copied.

## Decisions

Each row says what was built. Where it differs from the plan as first written, the last sentence of the reason says how and why.

| # | Decision | Reason |
| --- | --- | --- |
| D1 | A date variable is a name of three forms: `date.today`, `date.plus<N>d`, `date.minus<N>d`; `datetime.now`, `datetime.plus<N><u>`, `datetime.minus<N><u>`; `local.now`, `local.plus<N><u>`, `local.minus<N><u>`. `<u>` is `d`, `h`, or `m` (minutes). `date` takes `d` only. `<N>` is 1 to 36500 with no leading zero. No other spelling exists | Every name fits the grammar of a variable name as it is, so the five places that hold that grammar stay as they are. An offset in the name lets a criterion test a boundary, such as 2, 3, and 4 days. Changed: the plan said six places, and the code has five. A `d` is a calendar day of the zone for `date` and `local`, so a local time keeps its clock reading across a change of summer time, and it is 24 hours for `datetime`, which is in UTC |
| D2 | `date.*` gives `2006-01-02` and `local.*` gives `2006-01-02T15:04`, both in the time zone of the machine (`time.Local`). `datetime.*` gives RFC 3339 in UTC with seconds and `Z` | `date` and `local` are what a person at this machine types into a date field and a `datetime-local` field. An instant in UTC is the form that most APIs take |
| D3 | The instant is the time inside the run ID (`r-20060102T150405Z-xxxx`), read by `domain.RunID.Time`. Every date variable of one run is computed from that one instant | Every place that fills in a variable has `run.id` already, so no new value is passed anywhere. Two steps of a run agree, and a person who reads the evidence can compute the dates again from the run ID. A run ID that holds no time gives an error that says so: `unknown variable date.today: the run ID "r1" holds no time`. The run IDs of the tests that predate this plan, such as `r-test-0001`, hold no time, so a test of a date variable sets a run ID that does |
| D4 | One function resolves a name: `script.Resolve` takes the map of variables first, then a date variable. `script.Render`, `script.RenderWeb`, and `script.RenderLocator` use it. The lookup of expected values in a run uses `script.Lookup`, which is the same function without the error. The parameters and `with` values of a fixture, and the fill text and the `expect` text of a web flow, use it too | A date variable works wherever `{{run.id}}` works, and no place can be forgotten, because there is no list of places to scan. Changed: the web flow has its own `Explorer.Expand`, because the text that a flow types and expects has no captures, and `Expand` also remembers which date variables it filled in, so that the recording keeps them as variables. It calls `script.Resolve` for the value |
| D5 | Validation accepts a date variable wherever it accepts `{{run.id}}`: scripts (requests, web actions, and expected values), fixtures (the request steps and the parameter defaults), journeys, and web turns. A `given` entry may not have the `as` name `date`, `datetime`, or `local`. At run time the map still wins, so a stored scenario with such an `as` behaves as before | A name that validation refuses cannot reach a run. A fixture output named `date.today` would hide the variable. Changed: the three names are in one list, `scenario.ReservedHeads`, which the leg IDs of a journey and the preset variables of `RunOne` use too, because a leg output `date.x` would meet the same clash. A `with` key is not restricted, since it is a parameter name and has no dot |
| D6 | `openapi.Param` and `openapi.Field` gain `Format`, read from the document. The schema of a criterion answer names the variable to write for `date` and `date-time`, for a query parameter and for a body field. The web prompt names `{{date.…}}` for a `date` field and `{{local.…}}` for a `datetime-local` field | A small model writes the right form the first time when the description of the value says it. Without the hint it learns the rule from a refusal, which costs a second call. Changed: a query parameter with a date format has its own branch in the schema, so that its description can say it; the other parameters keep the one branch of before. The web part is in the commit of the web flow and not in the commit of the schema, so that the web prompt never promises what the flow still refuses |
| D7 | The plan refuses a typed-in date that is near the day of the plan. A text is a date when it starts with `YYYY-MM-DD`, followed by the end, `T`, or a space, and names a real day. It is near when it is less than 3650 days from the day of the plan, in the zone of the machine. The rule reads query values, JSON body strings at any depth, expected values (`equals`, `notEquals`, `contains`), `with` values of a `given` entry, web fill texts, and web `expect` texts. The message names the variable with the offset that gives the same day, for example `{{date.plus7d}}`. Changed: a text of a web page shows words around a date, such as `Due 2026-12-01`, so for a web fill text and a web `expect` a date at the start of any word counts, and not only at the start of the text. A browser field holds no zone, so the variable that the message names for it is a `local` one. For an API text, the form is `date` for a bare day, `datetime` for a time with `Z` or an offset, and `local` for the rest. A step that expects a refusal is not excused: a date that a refusal needs is `{{date.minus1d}}`, or a date more than 10 years away | A date near today changes its meaning as days pass. A date far away does not: `2000-01-01` stays in the past. A fixed far date is also the only way to check a date that the page shows in its own words. The offset of the message is a number of calendar days, so for a `datetime` it can be a day off near midnight UTC |
| D8 | A typed-in date is kept when the ticket file of the scenario holds the same `YYYY-MM-DD` text | A criterion that names a day, such as a leap day, means that day. Changed: "holds" means as a whole word, with no letter, digit, underscore, or hyphen on either side, and the file is the one that the plan was given, which is cut at `MaxTicketBytes`, so a date after the cut is not seen |
| D9 | In the criteria pipeline, a step that expects no 4xx status may not send a typed-in ID. A query parameter or a top-level body field is an ID when its name is `id` or ends in `Id`, `ID`, `_id`, `Ids`, `IDs`, or `_ids`, or when its format is `uuid`. Its value must hold a variable. It is kept when the API document lists it in an enum of the field, or when the ticket file holds it as a whole word | An ID that no step and no fixture gave is a guess, or a record that may be gone next week. A step that expects a refusal sends a wrong ID on purpose. Changed: the message is the one of the contract below, which names the place and says what to do, and not the sentence of `fillPath`, which speaks of `missing-{{run.id}}` and has no meaning for a body field. A number counts as an ID too, so `warehouseId: 12` is refused. A field of the type `object` or `array` is not read, because its value is JSON text and the rule does not look inside it. The prompt of a criterion says the same in one sentence |
| D10 | `plan.Input` gains `Now time.Time`, which the command sets from its clock. A zero value turns the rule of D7 off | The rule needs the day of the plan. Tests of other behaviour leave it zero. Changed: the web flow reads it too, because the turn of a model goes through the same rule |
| D11 | The versions of the criterion prompt, the plan prompt, and the web prompt go up by one: criterion 4 to 5, plan 9 to 10, web 4 to 5 | A stored answer of an older prompt may hold a typed-in date. The packets hash the operations, and `Format` is now part of an operation, so the hash of a packet changes for a document that has a format. That is accepted |
| D12 | A stored script is not changed and not checked again. A run never refuses a script for a typed-in date | A fixed far date is right, and a run cannot tell why a date was typed in |

## Contract

```text
{{date.today}}          2026-11-18            machine zone
{{date.plus7d}}         2026-11-25
{{date.minus90d}}       2026-08-20
{{datetime.now}}        2026-11-18T07:15:02Z  UTC
{{datetime.plus2h}}     2026-11-18T09:15:02Z
{{local.now}}           2026-11-18T14:15      machine zone
{{local.minus1d}}       2026-11-17T14:15
```

A refused draft, as the model reads it. The place is the path of the problem, and the message follows it:

```text
scenarios[0].steps[1]: json.dueAt holds the date 2026-11-25T10:00:00Z, which is near today and will be wrong on a later day: write {{datetime.plus7d}}, or a date at least 10 years away when the value must not move
steps[0].body[0]: field customerId of createOrder holds the ID "c-1042", which no step or fixture gave: capture it in an earlier step, take it from a fixture, or name it in the ticket
```

A date variable that cannot be computed:

```text
unknown variable date.today: the run ID "r1" holds no time
```

## Trust

Untrusted: the repository, its ticket files, its API document, every model answer, and every response. A date variable is computed by code from the run ID and the clock zone of the machine, and holds only digits, `-`, `:`, `T`, and `Z`. A name that is not exactly one of D1 is not a date variable, so a name cannot carry text into a request. The rules of D7 and D9 only refuse a draft. They never change one.

## As built

* **The names.** `internal/datevar` parses a name and gives its text at an instant in a zone (`Is`, `Text`), and `Suggest` writes the name that gives a day from today. `domain.RunID.Time` in `internal/domain/runid.go` reads the instant.
* **Rendering.** `script.Resolve` and `script.Lookup` in `internal/script/render.go`. `substitute` calls `Resolve`, so `Render`, `RenderWeb`, and `RenderLocator` have it. `evalExpectation` in `internal/replay/runner.go` calls `Lookup`. `variableCheck` and `RequestProblems` in `internal/script/validate.go` accept a date variable.
* **The reserved names.** `scenario.ReservedHeads` is used by `validateGiven` in `internal/scenario/validate.go`, by the leg IDs in `internal/journey/validate.go`, and, as `reservedHeads`, by the preset variables in `internal/replay/single.go`.
* **Fixtures.** `defaultProblem` in `internal/fixture/validate.go` and `Resolver.params` in `internal/fixture/resolve.go`. The error of a parameter that holds a variable with no value now names the parameter, and before it named the variable.
* **The document.** `Format` on `openapi.Param` and `openapi.Field` in `internal/openapi/detail.go`. `queryItems`, `valueSchema`, and `dateHint` in `internal/stage/plan/answer.go` write the hints.
* **The date rule.** `internal/stage/plan/dates.go` has `dateRule`, `validator.dates`, and `walkStrings`. `Input.Now` is set by `semar plan` in `internal/cli/plan.go`.
* **The ID rule.** `internal/stage/plan/idrule.go` has `idRule` and `namesWord`. `fillQuery` and `fillBody` in `convert.go` call it.
* **The web flow.** `Explorer.Expand` and `Explorer.Variables` in `internal/engine/web/explore.go`: `Fill` types the expanded text, and `Variables` gives the date variables that a text had with the text of each. `internal/cli/webexplore.go` expands the checks of a flow and gives `Variables` to the values of the recording, so that `record.Values.Replace` writes the variable back in place of the text. `checkTextVariables`, `turnDates`, and `TurnInput.Ticket` in `internal/stage/plan/webturn.go` and `webflow.go`.
* **The prompts.** `criterion.md` (version 5), `plan.md` (version 10), and `web.md` (version 5), with their golden files.

### Tests

* `internal/datevar` and `internal/domain`: every form of a name and the names that are not one (zero, a leading zero, 36501, the wrong unit for `date`, another case, a full-width digit); the day of the zone near midnight; a leap day and a month end; `Suggest`; `RunID.Time` and the run IDs that hold no time.
* `internal/script`: a date variable in the path, the query, a header, the body, a web action, and a locator; the map that wins; a run ID with no time; a name that is not a date variable; validation of requests, web steps, and expected values. `internal/replay`: an expected value that holds a date variable, which was inconclusive before.
* `internal/scenario` and `internal/journey`: the three reserved names for a given entry and a leg; a date variable in a leg. `internal/fixture`: a default and a `with` value that hold a date variable, the same in a request step, and the refusal of a name that is not one.
* `internal/openapi`: `Format` of a parameter and a field. `internal/stage/plan`: the hints in the answer schema; every place where a near date is refused and each message; a date far away, no real day, digits around the date, a date after other text, a header, and a number, which are kept; the boundary of 3650 days on both sides; the day of the plan in the zone of the machine; a date that the ticket names, and a date that only another ticket names; no clock; an ID in a query and in a body, an ID that something gave, a refusal, and the types that are not read; the web turn that types a date, checks one in words, types a far date, and names a date of the ticket.
* `internal/engine/web` with a real Chrome: a fill that types the computed text of `{{local.plus7d}}`, `Variables` and `Expand`, and the refusal of a variable that has no value. `internal/cli` with a real Chrome: a flow that types `{{date.plus7d}}` and checks `Due {{date.plus7d}}` writes a scenario and a script with the variable and no date, replays once, and renders to two dates a week apart for two run IDs a week apart.
* `internal/cli`: a plan whose first draft types a near date, which is sent back, and whose second draft writes `{{datetime.plus7d}}` in a body and in an expected value. The stored scripts hold the variable and no date. Two runs with run IDs a week apart send the dates `2026-11-21T10:00:00Z` and `2026-11-28T10:00:00Z`, and both pass.

## Not built

* A check of the scripts that exist for typed-in dates and IDs.
* Dates in other forms: `18/11/2026`, a number of seconds, a month, a week, a time of day.
* A time zone in the configuration. The zone is the one of the machine, and a test must set `time.Local` to be independent of it.
* A date typed by a person in `semar record` stays as typed.
* The ID rule in the single pipeline, and for a field inside an object or an array that the model gives as JSON text. The rule is not read for a path parameter either, which `fillPath` had already.
* The date rule does not read a header value, a typed date in a fixture file, or a typed date in a journey file. A person writes those.
* An end-to-end test of the ID rule through `semar plan`: it is tested at the level of the conversion and of `CheckAnswer`.
* A test that breaks on a value that expired still ends as it does today.
