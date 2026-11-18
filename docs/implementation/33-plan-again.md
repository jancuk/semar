# Plan 33: A second plan writes only what is missing

* **Status:** Implemented.
* **Needs first:** Plans 19, 25, 30, and 31.
* **Implements:** PRD decision D77 and FR-385 to FR-390.

## Goal

`semar plan` on a ticket that was planned before writes a test only for a criterion that has none. A criterion that has a test costs no model call and gets no second test.

Before this plan a second plan of the same ticket did not fail. It planned every criterion again and wrote each test a second time under a new ID. A stored answer of the first plan was refused for its title, which a scenario now had, and was sent back to the model for a paid correction.

## Decisions

| # | Decision | Reason |
| --- | --- | --- |
| D1 | A criterion has a key: the SHA-256 of its whole text, in hexadecimal, with its list number removed and each run of white space made one space | The line of a criterion moves when the file is edited, and its number is its place under the heading. Only the text says what the criterion asks. Changed: the key is taken from the lines as the file holds them, not from the lines as the prompt holds them, because the prompt cuts a line at 1000 characters and an edit after that point must still change the key |
| D2 | Each obligation that the criteria pipeline writes for a criterion gains `criterion: {number, key}`: the entry of a scenario, of a web test, of a manual check, of a case not drafted, and of a criterion that nothing accounts for | The obligations file is the durable record of what a plan decided, and a plan only adds to it. Changed: the criterion of an entry is the one that its cite names by file and line, which is how a plan already decides what accounts for a criterion, so no packet is carried to the writer. The key is written in double quotes when it starts with a digit, so that no YAML reader takes it for a number. The single pipeline writes no `criterion` |
| D3 | `plan.Input` gains `Earlier`: what was planned before, per ticket, read by the command from the obligations files and the scenario files of the tickets of the change | The stage has no access to the repository's files beyond its input. Changed: an obligations file that has a problem is not believed at all, and its ticket is planned in full, as before. The plan then refuses to write that ticket's obligations, as it did, until the file is fixed |
| D4 | Before any model call, code gives each criterion of a ticket one state. **Kept:** an entry with its key covers a scenario whose file exists and names the same ticket, or is a manual check. **New:** every other criterion. A criterion whose earlier entry is a case not drafted, or whose test was removed, is new | A criterion that could not be drafted deserves another try when a plan runs anyway. Removing a test with `semar rm` is the way to ask for a new one |
| D5 | A new criterion is **changed** when an entry of the ticket has its number, another key, and that key is the key of no criterion of the ticket today. It is planned like a new one, and the plan names the tests of the earlier text | The person edited the criterion, so the plan should write the test for the new text. The old test may be right or wrong now, and only a person can say |
| D6 | An entry whose key is the key of no criterion today, and whose number is not the number of a changed one, is reported as **removed** when it holds a test or a manual check | A test with no requirement behind it is a cost with no owner. Changed: an entry that holds nothing, such as a case not drafted, is not reported, because there is nothing for a person to remove. An entry of the earlier text of a criterion that a later plan wrote again, with a new key, is reported as removed on the plans after that one |
| D7 | The plan never removes, rewrites, or retires a test. For a changed or removed criterion it prints the command `semar rm <id> --reason "<text>"` for each test | A person approved the old test. Taking it away is that person's decision. Changed: a manual check has no `semar rm`, so it is named in `plan.md` and gets no command |
| D8 | An entry with no `criterion`, from a plan before this one, is matched by its citation. For a scenario, the citation of an expected result with the source `ticket`. For a manual check, its `cite`. The entry belongs to the criterion whose citation text, after the file and the line, is the same | A repository that was planned once must not get every test a second time on its first plan with this version. The citation text is the first sentence of the criterion, which is the only link that the older files hold. Changed: when two criteria have the same first sentence, an entry belongs to the first one that no other entry has claimed. An entry of this kind whose text matches no criterion is reported as removed and never as changed, because it holds no number. An entry that names only the place of a criterion, as a person writes it by hand, is an entry of that criterion, and keeps it from the offer of D12, and it keeps nothing else |
| D9 | A kept criterion makes no packet, reaches no model and no stored answer, and is not explored in a browser. It counts as accounted for, by the tests of its entries | The saving is the point, and a stored answer of a kept criterion is what was refused before. `plan.md` shows such a criterion as `<id> (kept)` or `manual check (kept)` |
| D10 | When every criterion of every ticket of the change is kept, the plan calls no model, writes no plan directory, prints one line, and ends with exit 0 | A plan with nothing to say should say so and cost nothing. Changed: when the files also hold entries that D6 reports as removed, the command lines of D7 follow the one line, because they would be lost otherwise. The check comes before any provider is built, and so before the checks of `--ladder` |
| D11 | `plan.md` gains a section that lists each kept criterion with its tests, each changed one with the tests of the earlier text, and each removed one. The lines that end a plan count them | The person sees why a criterion got no new test |
| D12 | The cost estimate counts only the criteria that will be planned. The daily command offers a plan for a ticket that has a criterion with no entry at all, or a changed one. A criterion that was not drafted before does not cause an offer | The estimate was the cost of the whole ticket. An offer on every run for a criterion that can never be drafted would train people to ignore it. Changed: the offer for such a ticket says `<ticket> has criteria with no test or manual check yet`, and joins the older offer for a ticket with no use case when both apply |
| D13 | The single pipeline is unchanged | It plans a change and not a criterion, so it has no unit to keep |

## As built

* `internal/stage/plan/criteria.go`: `Criterion.Key`, `Criterion.Cited`. `tickets.go` splits `ticketLine` into a clean step and a cut step, so that the key can use the first.
* `internal/stage/plan/again.go`: `Earlier`, `State`, `Standing`, `Removed`, `Again`, and `Classify`, which is D4 to D6 and D8 as one pure function of the ticket files and what was read. `Again` knows `Count`, `AllKept`, and `Tickets`.
* `internal/stage/plan/validate.go`: `Input.Earlier`. `plan.go`: `Output.Again`.
* `internal/stage/plan/packet.go`: `Packets` skips a kept criterion. `pipeline.go`: `RunCriteria` classifies first, returns `ErrAllKept` when nothing is left, takes the kept criteria out of the gaps, and says of a ticket whose criteria are all kept that they have a test or a manual check from an earlier plan, instead of sending the person to the single pipeline. The web exploration works on the outcomes, so a kept criterion is never in it.
* `internal/stage/plan/accounting.go`: `Accounting` takes the `Again` and cites a kept criterion by its tests. `AllCriteria` lists the criteria of the ticket files.
* `internal/stage/plan/render.go`: the section `Criteria that earlier plans covered`, and a `Kept` line under `How this plan was made`.
* `internal/obligation/file.go`, `marshal.go`: `Obligation.Criterion` with its keys, its checks (a number of 1 or more and a 64-digit key), and its form in a file. `fromplan.go`: `FromPlanWith` takes the criteria of the ticket files and gives each entry its criterion.
* `internal/cli/planearlier.go`: `readEarlier` reads the obligations files and the scenario files, and `againLines` makes the lines that end a plan. `plan.go` reads `Earlier` after the pipeline is chosen, ends the plan of D10, and prints the lines of D11. `planwrite.go` passes the criteria to the obligations.
* `internal/cli/planoffer.go`, `daily.go`: the estimate counts the criteria that a plan would take, and the offer covers a ticket with a missing criterion.

## Tests

* `internal/stage/plan/criterionkey_test.go`: the key ignores the number, the line, and the spacing, and sees a change past the cut of the text.
* `internal/stage/plan/again_test.go`: kept by a test and by a manual check, an entry with no test, changed, removed, a swapped or renumbered criterion, a match by cite, a cite that matches nothing, an entry that names only a place, and a ticket that the plan does not have.
* `internal/stage/plan/pipeline_again_test.go`: a kept criterion makes no job and its stored answer is not read, a kept web criterion is not explored, a changed criterion is planned, `ErrAllKept`, and the accounting of a kept criterion.
* `internal/stage/plan/render_again_test.go`: the section of `plan.md`, no section for a first plan, and the reason that `plan.md` gives for a ticket whose criteria are all kept.
* `internal/obligation/fromplan_test.go`: the criterion of each kind of entry, a file round trip, and the files that are refused.
* `internal/cli/plan_earlier_test.go`: what is read from the files, a ticket outside the change, a file with a problem, and a scenario of another ticket.
* `internal/cli/plan_againlines_test.go`: the lines that end a plan.
* `internal/cli/plan_again_test.go`: the end-to-end test. A plan, a second plan with no change that calls no model and writes nothing, a plan after one criterion was edited and one added, which writes two tests and names the old one, a plan after a criterion was deleted, and a second plan of files that hold no keys. `plan_web_test.go` also checks that the entries of a web test, an API scenario, and a manual check name their criteria.
* `internal/cli/run_daily_again_test.go`: the offer and its estimate for an added criterion, an edited one, a case not drafted before, and a ticket with nothing missing.

The existing tests that plan a ticket twice are `TestASecondPlanOfTheSameDiff` and `TestPlanRepeatedWithTheSameFlags`. Both use the single pipeline, which D13 leaves as it is, so they are unchanged. `TestRunCriteriaCorrectsAStoredAnswerThatNoLongerPasses` plans with a scenario that no obligation entry names, which is still a new criterion, so it is unchanged too.

## Contract

The end of a second plan, when one criterion was added to the ticket and one was edited:

```text
criteria: 9, 7 kept, 2 planned (1 changed), 0 removed
SHOP-7-04 was written for an earlier text of criterion 5. Read both tests. To remove the old one: semar rm SHOP-7-04 --reason "criterion 5 changed"
```

For a test of a criterion that the ticket no longer has:

```text
SHOP-7-09 was written for a criterion that SHOP-7 no longer has (it was criterion 9). To remove it: semar rm SHOP-7-09 --reason "criterion 9 was removed"
```

When nothing is missing:

```text
Every criterion of SHOP-7 has a test or a manual check already. Nothing was planned. To plan one again, remove its test with semar rm.
```

A first plan prints none of these lines.

An obligation entry:

```yaml
- id: SHOP-7/scenario-SHOP-7-04
  title: An order that is paid cannot be cancelled
  source: diff
  covers: [SHOP-7-04]
  criterion: {number: 5, key: "3f6c…"}
```

## Trust

Untrusted: the ticket files, the obligations files, the scenario files, and every model answer. A key is computed by code from the ticket file. An obligations file can only make the plan skip a criterion, and only when a scenario file with that ID exists and names the same ticket, or when the entry is a manual check. The skipped criterion is listed in `plan.md` with the test that covers it. No path from an obligations file is opened. A key that is not 64 lowercase hexadecimal digits makes the file invalid, and an invalid file is not believed.

## Tasks

Each task is one commit, with its test written first.

1. The key of a criterion (D1), and `criterion` on an obligation entry, written by the code that makes obligations from a plan (D2).
2. The command reads what was planned before into `plan.Input` (D3, D8).
3. The states of D4 to D6, computed by one pure function from the criteria of today and what was planned before.
4. The criteria pipeline and the web exploration skip a kept criterion, and accounting counts it (D9). The case of D10.
5. `plan.md` and the last lines of the plan (D11), with the commands of D7.
6. The cost estimate and the offer of the daily command (D12).
7. End-to-end test in `internal/cli`.

## Not built

* The plan does not update a test in place.
* The single pipeline still writes every test again on a second plan.
* A criterion that was split in two, or two that were merged, show as removed and new.
* A change after the first sentence of a criterion is not seen for an entry that D8 matches.
* A manual check keeps its criterion for good. A web criterion that was left a manual check because the browser was off is not explored on a later plan, and `semar rm` does not remove a manual check. To plan it again, a person deletes its entry from the obligations file.
* An entry that a person wrote by hand, with no `criterion` and no text in its cite, keeps no criterion. The criterion is planned, and only the offer of the daily command is held back.
* The daily command does not say which criterion is missing, only the ticket.
