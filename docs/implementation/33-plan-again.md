# Plan 33: A second plan writes only what is missing

* **Status:** Planned. Not built.
* **Needs first:** Plans 19, 25, 30, and 31.
* **Implements:** PRD decision D77 and FR-385 to FR-390.

## Goal

`semar plan` on a ticket that was planned before writes a test only for a criterion that has none. A criterion that has a test costs no model call and gets no second test.

Before this plan a second plan of the same ticket did not fail. It planned every criterion again and wrote each test a second time under a new ID. A stored answer of the first plan was refused for its title, which a scenario now had, and was sent back to the model for a paid correction.

## Decisions

| # | Decision | Reason |
| --- | --- | --- |
| D1 | A criterion has a key: the SHA-256 of its whole text, with its list number removed and each run of white space made one space | The line of a criterion moves when the file is edited, and its number is its place under the heading. Only the text says what the criterion asks |
| D2 | Each obligation that the criteria pipeline writes for a criterion gains `criterion: {number, key}`: the entry of a scenario, of a manual check, of a case not drafted, and of a criterion that nothing accounts for | The obligations file is the durable record of what a plan decided, and a plan only adds to it |
| D3 | `plan.Input` gains what was planned before, per ticket, read by the command from the obligations files and the scenario files of the tickets of the change | The stage has no access to the repository's files beyond its input |
| D4 | Before any model call, code gives each criterion of a ticket one state. **Kept:** an entry with its key covers a scenario whose file exists, or is a manual check. **New:** every other criterion. A criterion whose earlier entry is a case not drafted, or whose test was removed, is new | A criterion that could not be drafted deserves another try when a plan runs anyway. Removing a test with `semar rm` is the way to ask for a new one |
| D5 | A new criterion is **changed** when an entry of the ticket has its number, another key, and that key is the key of no criterion of the ticket today. It is planned like a new one, and the plan names the tests of the earlier text | The person edited the criterion, so the plan should write the test for the new text. The old test may be right or wrong now, and only a person can say |
| D6 | An entry whose key is the key of no criterion today, and whose number is not the number of a changed one, is reported as **removed**: its tests were written for a criterion that the ticket no longer has | A test with no requirement behind it is a cost with no owner |
| D7 | The plan never removes, rewrites, or retires a test. For a changed or removed criterion it prints the command `semar rm <id> --reason "<text>"` | A person approved the old test. Taking it away is that person's decision |
| D8 | An entry with no `criterion`, from a plan before this one, is matched by its citation. For a scenario, the citation of an expected result with the source `ticket`. For a manual check, its `cite`. The entry belongs to the criterion whose citation text, after the file and the line, is the same | A repository that was planned once must not get every test a second time on its first plan with this version. The citation text is the first sentence of the criterion, which is the only link that the older files hold |
| D9 | A kept criterion makes no packet, reaches no model and no stored answer, and is not explored in a browser. It counts as accounted for, by the tests of its entries | The saving is the point, and a stored answer of a kept criterion is what was refused before |
| D10 | When every criterion of every ticket of the change is kept, the plan calls no model, writes no plan directory, prints one line, and ends with exit 0 | A plan with nothing to say should say so and cost nothing |
| D11 | `plan.md` gains a section that lists each kept criterion with its tests, each changed one with the tests of the earlier text, and each removed one. The line that ends a plan counts them | The person sees why a criterion got no new test |
| D12 | The cost estimate counts only the criteria that will be planned. The daily command offers a plan for a ticket that has a criterion with no entry at all, or a changed one. A criterion that was not drafted before does not cause an offer | The estimate was the cost of the whole ticket. An offer on every run for a criterion that can never be drafted would train people to ignore it |
| D13 | The single pipeline is unchanged | It plans a change and not a criterion, so it has no unit to keep |

## Contract

The end of a second plan, when one criterion was added to the ticket and one was edited:

```text
criteria: 9, 7 kept, 2 planned (1 changed), 0 removed
SHOP-7-04 was written for an earlier text of criterion 5. Read both tests. To remove the old one: semar rm SHOP-7-04 --reason "criterion 5 changed"
```

When nothing is missing:

```text
Every criterion of SHOP-7 has a test or a manual check already. Nothing was planned. To plan one again, remove its test with semar rm.
```

An obligation entry:

```yaml
- id: SHOP-7/scenario-SHOP-7-04
  title: An order that is paid cannot be cancelled
  source: diff
  covers: [SHOP-7-04]
  criterion: {number: 5, key: 3f6c…}
```

## Trust

Untrusted: the ticket files, the obligations files, the scenario files, and every model answer. A key is computed by code from the ticket file. An obligations file can only make the plan skip a criterion, and the skipped criterion is listed in `plan.md` with the test that covers it, so a file that claims a test which does not exist is caught by D4: the scenario file must exist. No path from an obligations file is opened.

## Tasks

Each task is one commit, with its test written first.

1. The key of a criterion (D1), and `criterion` on an obligation entry, written by the code that makes obligations from a plan (D2).
2. The command reads what was planned before into `plan.Input` (D3, D8).
3. The states of D4 to D6, computed by one pure function from the criteria of today and what was planned before.
4. The criteria pipeline and the web exploration skip a kept criterion, and accounting counts it (D9). The case of D10.
5. `plan.md` and the last line of the plan (D11), with the commands of D7.
6. The cost estimate and the offer of the daily command (D12).
7. End-to-end test in `internal/cli`: a plan, then a second plan with no change that calls no model, then a plan after one criterion was added and one edited, which writes two tests and names the old one.

## Not built

* The plan does not update a test in place.
* The single pipeline still writes every test again on a second plan.
* A criterion that was split in two, or two that were merged, show as removed and new.
* A change after the first sentence of a criterion is not seen for an entry that D8 matches.
