# Plan 31: Dates that follow the run, and no value that expires

* **Status:** Planned. Not built.
* **Needs first:** Plans 17, 25, and 30.
* **Implements:** PRD decision D75 and FR-375 to FR-380.

## Goal

A test that a plan writes today gives the same result next month. A script holds no date that is near the day of the plan, and no ID of a record that nobody created for it.

Before this plan a script could fill in four kinds of value at run time: `{{run.id}}`, a capture, a fixture output, and a `{{data.<key>}}` in a fixture parameter. A date such as "due tomorrow" was typed in as text, so the test changed its meaning the day after. A body field such as `customerId` could hold an ID that the model made up or copied.

## Decisions

| # | Decision | Reason |
| --- | --- | --- |
| D1 | A date variable is a name of three forms: `date.today`, `date.plus<N>d`, `date.minus<N>d`; `datetime.now`, `datetime.plus<N><u>`, `datetime.minus<N><u>`; `local.now`, `local.plus<N><u>`, `local.minus<N><u>`. `<u>` is `d`, `h`, or `m` (minutes). `date` takes `d` only. `<N>` is 1 to 36500 with no leading zero. No other spelling exists | Every name fits the grammar of a variable name as it is, so the six places that hold that grammar stay as they are. An offset in the name lets a criterion test a boundary, such as 2, 3, and 4 days |
| D2 | `date.*` gives `2006-01-02` and `local.*` gives `2006-01-02T15:04`, both in the time zone of the machine. `datetime.*` gives RFC 3339 in UTC with seconds and `Z` | `date` and `local` are what a person at this machine types into a date field and a `datetime-local` field. An instant in UTC is the form that most APIs take |
| D3 | The instant is the time inside the run ID (`r-20060102T150405Z-xxxx`), read by a new `domain.RunID` method. Every date variable of one run is computed from that one instant | Every place that fills in a variable has `run.id` already, so no new value is passed anywhere. Two steps of a run agree, and a person who reads the evidence can compute the dates again from the run ID. A run ID that holds no time gives an error that says so |
| D4 | One function resolves a name: the map of variables first, then a date variable. `script.Render`, `script.RenderWeb`, `script.RenderLocator`, the lookup of expected values in a run, the parameters and `with` values of a fixture, the fill text and the `expect` text of a web flow all use it | A date variable works wherever `{{run.id}}` works, and no place can be forgotten, because there is no list of places to scan |
| D5 | Validation accepts a date variable wherever it accepts `{{run.id}}`: scripts, expected values, fixtures, journeys, and web turns. A `given` entry may not have the `as` name `date`, `datetime`, or `local` | A name that validation refuses cannot reach a run. A fixture output named `date.today` would hide the variable. At run time the map still wins, so a stored scenario with such an `as` behaves as before |
| D6 | `openapi.Param` and `openapi.Field` gain `Format`, read from the document. The schema of a criterion answer names the variable to write for `date` and `date-time`. The web prompt names `{{date.…}}` for a `date` field and `{{local.…}}` for a `datetime-local` field | A small model writes the right form the first time when the description of the value says it. Without the hint it learns the rule from a refusal, which costs a second call |
| D7 | The plan refuses a typed-in date that is near the day of the plan. A text is a date when it starts with `YYYY-MM-DD`, followed by the end, `T`, or a space, and names a real day. It is near when it is less than 3650 days from the day of the plan. The rule reads query values, JSON body strings at any depth, expected values, `with` values of a `given` entry, web fill texts, and web `expect` texts. The message names the variable with the offset that gives the same day, for example `{{date.plus7d}}` | A date near today changes its meaning as days pass. A date far away does not: `2000-01-01` stays in the past. A fixed far date is also the only way to check a date that the page shows in its own words |
| D8 | A typed-in date is kept when the ticket file of the scenario holds the same `YYYY-MM-DD` text | A criterion that names a day, such as a leap day, means that day |
| D9 | In the criteria pipeline, a step that expects no 4xx status may not send a typed-in ID. A query parameter or a top-level body field is an ID when its name is `id` or ends in `Id`, `ID`, `_id`, `Ids`, `IDs`, or `_ids`, or when its format is `uuid`. Its value must hold a variable. It is kept when the API document lists it in an enum of the field, or when the ticket file holds it as a whole word | An ID that no step and no fixture gave is a guess, or a record that may be gone next week. The rule is the one that `fillPath` has for a path parameter, with the same message. A step that expects a refusal sends a wrong ID on purpose |
| D10 | `plan.Input` gains `Now time.Time`, which the command sets from its clock. A zero value turns the rule of D7 off | The rule needs the day of the plan. Tests of other behaviour leave it zero |
| D11 | The versions of the criterion prompt, the plan prompt, and the web prompt go up by one | A stored answer of an older prompt may hold a typed-in date |
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

A refused draft, as the model reads it:

```text
scenario 1, step create-order: json.dueAt holds the date 2026-11-25T10:00:00Z, which is near today and will be wrong on a later day: write {{datetime.plus7d}}, or a date at least 10 years away when the value must not move
scenario 1, step create-order: json.customerId holds the ID "c-1042", which no step or fixture gave: capture it in an earlier step, take it from a fixture, or name it in the ticket
```

A date variable that cannot be computed:

```text
unknown variable date.today: the run ID "r1" holds no time
```

## Trust

Untrusted: the repository, its ticket files, its API document, every model answer, and every response. A date variable is computed by code from the run ID and the clock zone of the machine, and holds only digits, `-`, `:`, `T`, and `Z`. A name that is not exactly one of D1 is not a date variable, so a name cannot carry text into a request. The rules of D7 and D9 only refuse a draft. They never change one.

## Tasks

Each task is one commit, with its test written first.

1. `internal/datevar`: parse a name and give its text at an instant in a zone (D1, D2). `domain.RunID` gives its time (D3).
2. The resolver of D4 in `internal/script`, used by `Render`, `RenderWeb`, `RenderLocator`, and the run's lookup of expected values. Validation of scripts, fixtures, and journeys accepts the names, and refuses the three `as` names (D5).
3. Fixture parameters and `with` values accept a date variable (D4).
4. `Format` on `openapi.Param` and `openapi.Field`, the value descriptions of the answer schema, and the three prompts with their versions (D6, D11).
5. The date rule in `plan.Validate`, with `Input.Now` and the ticket exception (D7, D8, D10).
6. The ID rule in the criteria pipeline, in `fillQuery` and `fillBody` (D9).
7. Web flows: the turn check accepts a date variable in fill text and `expect` and applies D7, the explorer types the computed text, and the written script holds the variable, not the text.
8. End-to-end test in `internal/cli`: a plan whose scripted model first types a near date is corrected, the stored script holds `{{date.plus7d}}`, and two runs with run IDs a week apart send dates a week apart.

## Not built

* A check of the scripts that exist for typed-in dates and IDs.
* Dates in other forms: `18/11/2026`, a number of seconds, a month, a week, a time of day.
* A time zone in the configuration. The zone is the one of the machine.
* A date typed by a person in `semar record` stays as typed.
* The ID rule in the single pipeline, and for a field inside an object or an array that the model gives as JSON text.
* A test that breaks on a value that expired still ends as it does today.
