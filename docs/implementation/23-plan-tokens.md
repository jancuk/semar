# Plan 23: The tokens of a plan

* **Status:** Implemented. This text describes the system as built.
* **Needs first:** Plans 12, 13, and 21.
* **Implements:** No new requirement. It changes how Plans 12 and 13 spend tokens, and it narrows PRD decision D61 and FR-310 and the TRD section "Engines and the gateway", which said that the planner may use the person's own MCP servers and skills.

## Goal

One `semar plan` on a real release cost $5 to $6 and 2.7 to 11 million input tokens, most of them read from the cache, over 25 to 52 turns. A rejected draft started a second job from nothing. This plan makes one plan cost far fewer tokens and keeps the plan as good as before.

Two things made the cost. The first is a fixed charge on every turn: with the person's MCP servers and skills loaded, the first turn of a Claude Code job held 57,069 input tokens, and without them 6,050, measured with the flags of `internal/provider/claude/args.go` and a one-word prompt. The planner only reads, so it needs none of them. The second is the context that grows while the agent fetches the change turn by turn: it reads the change file, searches the diff, finds the routes, and reads the API document, and every turn sends all of it again.

## Decisions

| # | Decision | Reason |
| --- | --- | --- |
| D1 | A plan job of a harness kind runs without the person's MCP servers and skills. Claude Code gets `--strict-mcp-config`, `--setting-sources ""`, and no `Skill` tool. Codex gets `--ignore-user-config` and its skill and plugin features off. The opt-in is `semar plan --with-setup` for one run, or `plan.withSetup: true` in the person's own `providers.yaml` for every plan. `--with-setup=false` turns the file's opt-in off for one run. The opt-in is never read from `semar.yaml`, and a provider that `semar.yaml` declares gets no setup either. `--no-setup` is removed, because off is now the default. When the opt-in is on, the start line says that the setup adds tokens to every turn | The setup was about 51,000 input tokens on every turn, about half of the latest plan, and the planner uses the three file tools only. The repository under test is untrusted, so it must not be able to widen what the planner loads, as with every other setup switch (Plan 13, D4). The person's `setup` switches of a provider stay as they are, and say what an opted-in plan may load |
| D2 | The prompt holds the material that a plan always needs, so the planner does not fetch it turn by turn. The prompt version becomes 8. It carries the change table, the files left out of the copy (which `changes.md` listed), and the difference. A difference of at most `MaxDiffBytes` (48 KiB) is shown whole. A larger one is shown as whole files while the budget lasts: a file that a ticket names before one that no ticket names, then source, migration, configuration, test, documents, and generated files in that order, then the smaller file first so that more files fit. The files of `.semar` are never shown, whatever the budget, because they hold Semar's own configuration and name hosts (Plan 12, R19), and the copy of the source has none of them. The per-file stat is the files table of the change table. The files whose hunks are left out are listed, and `.semar-plan/change.diff` still holds the whole difference for the planner to search. The prompt also lists the operations of the API documents of `knowledge` whose route or operation ID appears in the difference, one line each: method, path, operation ID, summary, the declared statuses, and the line of the document to read for the rest. The ticket criteria were already in the prompt. The prompt keeps the instruction that the planner may read any file for what it does not give | A plan spent its first turns reading `.semar-plan/changes.md`, searching the diff, finding routes, and reading the API document, and every later turn sent all of that again. One block in the first prompt costs the same tokens once, and saves the turns that fetch it. The diff is the repository's text, so it is fenced with a fence that its text cannot end, and the prompt says that it is data. A route matches only where it ends, and a parameter matches by position and not by name, so `/orders/{id}` in a router matches `/orders/{orderId}` in the document |
| D3 | A rejected draft is repaired, not planned again. The next attempt has the previous draft as JSON and the list of problems in its prompt, and is told to change only what the problems name and to copy the rest as it is. It runs with at most `RepairMaxTurns` (12) turns, or `plan.maxTurns` when that is lower. This holds for every problem that sends a draft back: a problem of validation, a criterion that nothing cites, and a problem that the trial found. The repair no longer reads `.semar-plan/previous.json`, so the file is no longer written. An output that is not a draft has nothing to repair, so the next attempt plans again with the whole turn limit and a prompt that says the output was no draft. The attempt limit stays `MaxAttempts` (3): a repair that is rejected is repaired in turn. When a repair fails in the harness, for example at its turn limit, after an earlier draft passed validation, that draft is kept and the plan is written with its gaps. With no passing draft the failure ends the run at once, as before | A repair job starts with the draft and its problems, so it need not explore the change again, and the old attempt spent a whole new exploration on a few problems. The latest plan needed two attempts. A turn limit that cannot be met would otherwise throw away two paid jobs, and keeping a draft that passed is what the last attempt already does (Plan 21, D9) |
| D4 | `semar plan` ends with one line, `model use: <n> attempts, <n> turns, <n> input (<n> cached), <n> output, $<cost>`, summed over every attempt, on standard output when the plan is written and on standard error with the prefix `semar: ` when it fails after a job ran. The cached part and the cost appear only when the provider reports them. With the opt-in on, a second line says that the setup adds tokens to every turn. The usage log gets the same totals as before | A person cannot lower what they cannot see. The numbers are the ones of the usage record, so the line and the dashboard agree |
| D5 | The default model, the reasoning level, and `plan.maxTurns` do not change. Only the repair attempt has a lower limit | The change is meant to cost less at the same quality. A weaker model or a lower turn limit would change the quality, so it is the person's decision and not part of this plan |

## Trust

| Risk | Answer |
| --- | --- |
| A repository turns the setup back on | The opt-in is read from the person's own `providers.yaml` and from the command line only. A `plan.withSetup` key in `semar.yaml` is refused as an unknown key, and a provider that `semar.yaml` declares gets no setup unless the person asks |
| The diff in the prompt carries an instruction | It sits in a fenced block that its own text cannot end, under a sentence that calls it data, and it reaches the template only as a value, like a ticket file or a fixture description |
| The diff shows Semar's own configuration | The files of `.semar` are never shown, whatever the budget (R19) |
| A repair hides a failure | A repair that the harness cannot finish ends the run as before, unless an earlier draft passed validation. That draft is written, and its criteria that nothing cites stay obligations with a gap |

## Measured

The same release of a small mock service, planned once with the build of the commit before this plan (`66699b1`) and once with the build of this plan (`235f464`, the last commit that changed code), with the same provider, model, and reasoning level (Claude Code, `sonnet`, `medium`), and the person's `setup: {mcp: true, skills: true}` in both, so that "before" is what a plan cost. Each build had its own `SEMAR_HOME` with a copy of `providers.yaml` only, so the usage log of the person was not touched. The trial was off in both, `--no-trial`, so that neither needed the environment. The base was the commit that the release branch last merged from `main`, and the head was the release head, because `main` already held the whole release. The working tree had no scenario, obligation, or plan of an earlier run, so the prompt had no list of existing scenarios.

```sh
export SEMAR_HOME=/tmp/semar-home-before   # providers.yaml only
/tmp/semar-before plan --diff base-main..release --provider claude --no-trial
export SEMAR_HOME=/tmp/semar-home-after
/tmp/semar-after  plan --diff base-main..release --provider claude --no-trial
```

| Run | Attempts | Turns | Input tokens | Cache read | Cache write | Output tokens | Cost |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| before, run 1 | 2 | 20 | 1,895,845 | 1,313,865 | 581,956 | 37,283 | $2.83 |
| before, run 2 | 2 | 12 | 723,842 | 484,942 | 238,888 | 19,881 | $1.20 |
| after, run 1 | 2 | 6 | 134,388 | 48,275 | 86,107 | 17,148 | $0.52 |
| after, run 2 | 2 | 7 | 183,107 | 98,292 | 84,807 | 18,936 | $0.54 |

Input tokens fell by 93% in the first pair and by 75% in the second. The mean of the two runs fell from 1,309,844 to 158,748, 88%, and the cost from $2.02 to $0.53. All four runs needed a second attempt. In all four, the plan accounted for all 16 acceptance criteria of the two tickets, and the criterion that a move to a list of another board is refused with 422 and a named error code was an expectation with `"source": "ticket"` in every plan.

What is left of the cost is the prompt. A job without the setup starts at 6,050 input tokens, and the prompt of this release adds the change table, up to 48 KiB of the 216 KiB diff, the tickets, and the API operations, so a turn costs about 22 to 26 thousand input tokens, and a plan runs for 6 to 7 turns. The before runs averaged 95 thousand input tokens a turn in the first run and 60 thousand in the second, and ran 12 to 20 turns.
