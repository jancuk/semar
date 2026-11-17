# Plan 00: Spikes

* **Status:** Specification. No spike has been run.
* **Guide:** [implementation guide](README.md). **Spec:** TRD, [Spikes before Milestone 1](../TRD.md#spikes-before-milestone-1)

**Goal.** Each unproven claim that the design depends on gets a written answer before the plan that depends on it starts.

**What works when it is done.** One file per spike in `docs/implementation/spikes/`, and each plan's open points are closed or the plan is changed.

## Rules

| # | Rule |
| --- | --- |
| R1 | A spike is a throwaway script and a written result. It adds no product code. The script is kept beside the result so that the spike can be repeated |
| R2 | A spike that touches a test environment or a production system only reads. This is the owner's rule, and it applies to whoever runs the spike |
| R3 | A result states what was run, on which versions, what was observed, and the decision that follows. "It works" is not a result |
| R4 | A spike that needs the owner's accounts, data, or past merge requests is run by the owner, or by an agent in a session where the owner granted that access |
| R5 | No ticket text, code, or response from the owner's company is copied into a result. A result holds counts and conclusions |

## Result file

`docs/implementation/spikes/S<n>.md`:

```markdown
# S<n>: <the question>

* **Run on:** <date>, by <who>
* **Versions:** <tool and version, one per line>
* **Answer:** <yes, no, or partly, in one sentence>

## What was run
<commands or the script name, and the inputs in general terms>

## What was observed
<facts and numbers>

## Decision
<which plan changes, and how. Or: no change>
```

## The spikes

| Spike | Question | Who | Procedure | Blocks |
| --- | --- | --- | --- | --- |
| S1 | Does the Claude Code flag set give a session with no shell, no network, and no write tool, in which a stdio MCP call succeeds without a prompt, on a subscription login? | Anyone with Claude Code signed in | Start `claude -p --output-format stream-json --verbose` with the flag set of the TRD and a tiny MCP server that has one tool. Ask the model to call the tool, to run a shell command, to fetch a URL, and to write a file. Record which of the four happen | Plan 02 |
| S2 | Do `-c mcp_servers.*` overrides work in `codex exec`, and does an MCP call run with no approval prompt under the read-only sandbox? | Anyone with Codex CLI signed in | The same four requests through `codex exec --json --sandbox read-only` | Plan 06 |
| S3 | Does `github.com/coder/acp-go-sdk` complete `initialize`, `session/new` with an MCP server, and `session/prompt` against `gemini --acp`? | Anyone with Gemini CLI | A 50-line Go program | Plan 06, the ACP driver only |
| S4 | Do the services continue a `traceparent` header, and does the trace ID appear in logs and traces? After how many seconds? | The owner, read-only | One `GET` with a known trace ID to one service on the test environment. Search logs and traces for the ID | Plan 04, the observer |
| S5 | Can a model draft useful scenarios from three past merge requests with blank tickets? | The owner | For each merge request, give a model the diff and the ticket. Compare the drafted scenarios with what was tested by hand. Count matched, missed, and wrong | Plan 03. Run this first |
| S6 | Which engine does the web engine use? Settled on 2026-11-17: an in-process Chrome DevTools Protocol client built on chromedp ([Plan 22](22-web-engine.md), D1) | Anyone | None. No other tool's engine is imported | Plan 22 |
| S7 | Which on-device server gives a screen snapshot fast enough, and does it run on the owner's Android build? | The owner, with an emulator | Time a snapshot with `uiautomator dump`, Appium UiAutomator2, and Maestro's driver. Check emulator detection and certificate pinning | Plan 10 |
| S8 | Can the web engine add `traceparent` to a page's requests, including cross-origin ones, without breaking them? Settled on 2026-11-17: the engine adds it through request interception, and a test calls a second origin that is listed | Anyone | None | Plan 22 |
| S9 | Which stores does the test environment use, can each get a read-only account, and which Go parsers handle PostgreSQL and MySQL statements without cgo? | The owner for the first two, anyone for the third | Parse 20 statements of each dialect, including the ones the gate must refuse | Plan 07 |
| S10 | Can a model build a correct feature map for one service, and trace a change in a shared function to the right endpoints? | The owner | Draft a map for one service. Mark each wrong or missing entry point. Trace three past changes | Plan 03 |
| S11 | Can Semar read cases from the owner's test management tool, edit its own comment on the tracker, and place earlier tickets in the right feature? | The owner. The comment test runs on a ticket the owner names | One read, one comment created and edited, 20 tickets placed | Plan 08 |
| S12 | For one past deploy, how many commits can code link to a ticket, and how many tickets span more than one repository? | The owner | A script over `git log` between two deploy commits | Plan 03 |
| S13 | Does a Google session made by a person in the dedicated Chrome profile stay valid when a tool drives that profile, and for how long? | The owner, with a Google test account | Sign in once in an ordinary window. Attach through the DevTools protocol each day for a week | Plan 09 |
| S14 | On real failed runs and probe responses, how often is the decision model's likely cause right, and does its ranking put the odd responses first? | Anyone, after Plan 05 has output | Label 50 results by hand and compare | Plan 06, the screener |

## Order

1. **S5.** It decides whether planning from a change is worth building. If fewer than half of the hand-tested scenarios are matched, stop and rethink before Plan 02.
2. **S1.** It blocks Plan 02.
3. **S12 and S10.** They block Plan 03.
4. **S4.** It decides whether the observer can fail a scenario or only advise.
5. The rest, each before its plan.

## Done when

Every spike that blocks a plan has a result file, and each "Open points" section of a plan either cites the result or records a changed design.
