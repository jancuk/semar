# Plan 15: Progress while a plan runs

## Goal

`semar plan` can take several minutes, and it printed nothing during that time. A person now sees that the planner is still working, for how long, and what it has done.

1. On a terminal, one status line is rewritten in place while the planner works.
2. Where standard error is not a terminal (a pipe, a file, a CI log), one plain line is printed every 30 seconds.
3. When the planner ends, the status line is removed and one line says how long the planner took.

## Decisions

| # | Decision | Reason |
|---|---|---|
| D1 | The line always shows the elapsed time and the draft number. It shows the responses and the tool calls when the provider reports them | The elapsed time needs nothing from a provider, so every provider gets a moving line. A count is shown only when it is a fact |
| D2 | A provider reports progress through a function of the job. A job with no function runs as before | A stage decides what to do with progress. A provider only says what happened |
| D3 | Claude Code always writes the stream format. The single-object format and its parser are removed | The stream is the only format that reports while the job runs. One parser is less code than two |
| D4 | The line never shows text of the model, a file name, or the arguments of a tool. It shows the name of the latest tool, and an MCP tool is shown as `an MCP tool` | The names of a person's MCP servers stay out of logs, as in `plan.md`. A file name or an argument is untrusted text |
| D5 | No new dependency. A terminal is a character device, other than the null device, whose `TERM` is set and is not `dumb`. The line is at most 78 characters, because the width of the terminal is not read | G18 |
| D6 | No flag turns the progress off. A pipe gets one line per 30 seconds, and nothing else changes | A flag is added when someone needs it |

## Trust

The output of a harness and the output of the model are untrusted.

| Risk | Answer |
|---|---|
| A tool name with control characters or escape sequences reaches the terminal | The name is passed through `terminalSafe`, every character outside printable ASCII becomes `?`, and the name is cut to 24 characters. A wide character takes two columns, so it would make the line wrap and scroll on every redraw |
| A very long tool name makes every report large | A name in a report is cut to 200 bytes (`provider.MaxToolName`) |
| A very long line of a stream is held in memory a second time | A line above 1 MiB is not parsed for progress. The whole output is still kept under the existing cap for the final parse |
| A progress report after the job has returned writes to a terminal that shows other text | A provider never calls the function after `Run` returns. The reporter ignores a report after it has stopped |

## Contract

### `internal/provider`

```go
// Progress is what a running job has done so far. A provider reports it when it can.
type Progress struct {
	Turns     int    // responses of the model so far
	ToolCalls int    // tool calls so far
	Tool      string // the name of the latest tool call, "" when there is none
}

// Job gains:
//   Progress func(Progress) // nil for no reports
```

* A provider calls `Progress` with the totals so far each time they change. It may call it from another goroutine, one call at a time. It never calls it after `Run` returns.
* `ProcessSpec` gains `OnStdoutLine func(line []byte)`. `RunProcess` calls it for each complete line of standard output as the line arrives, in order, from one goroutine, and never after `RunProcess` returns. A line above 1 MiB is skipped. A last line with no line ending is not passed, and the calls stop when the cap is reached. The slice is valid only during the call, and the function must return quickly, because the child waits while it runs. Standard output is still kept whole under `StdoutCap`.
* Claude Code: always `--output-format stream-json --verbose`. One message of the model arrives in several `assistant` events with the same message ID, so each new message ID is one response, and an event without an ID counts alone. Each new `tool_use` call ID is one tool call. The harness's own call that returns the structured output is not counted. The final result takes the number of turns that the harness reports, which can differ from the number of messages.
* Codex: each item that the final parse counts as a tool call is one tool call when it first appears, with the same name. The latest tool is the call that started last. Each `turn.completed` event is one response.
* An API kind: after each response of the model, with the calls of that response.
* The fake kind reports nothing.

### `internal/stage/plan`

```go
// Progress is where a plan is. Attempt starts at 1.
type Progress struct {
	Attempt  int
	Problems int // the problems of the previous draft, 0 on the first attempt
	provider.Progress
}

// Input gains:
//   Progress func(Progress) // nil for no reports
```

`Run` reports once when each attempt starts, with zero counts, and then each report of the provider with the attempt added.

### `internal/cli`

A reporter owns the status line from the start line of the plan until `plan.Run` returns.

On a terminal, every 120 ms:

```
⠹ planning · 1m24s · draft 1 of 3 · 12 responses · 31 tool calls · last: Read
```

* The parts after the elapsed time appear only when they are known: the draft part from the second draft on (`draft 2 of 3`), the counts when they are above zero, the latest tool when there is one.
* The line is written as a carriage return, the text, and the sequence that erases the rest of the line. When the reporter stops, the line is erased, so that the next output starts at the first column.
* When a draft is rejected, a line stays on the screen: `semar: draft 1 had 4 problems; the planner is fixing them`.

Not on a terminal, every 30 seconds:

```
semar: still planning: 1m30s, draft 1 of 3, 12 responses, 31 tool calls
```

After the planner ends, in both cases, when the plan succeeded:

```
semar: the planner finished in 1m24s
```

* The text of the line is cut to 78 characters by dropping parts from the end, never the spinner or the elapsed time. The name of the tool is cut to 24 characters, a newline or a tab in it becomes a space, and any other character outside printable ASCII (0x20 to 0x7e) becomes `?`, after the escaping and before the cut.
* Not on a terminal, there is no status line. Every 30 seconds one line is printed, as above. It always names the draft, shows a count only when it is above zero, and never names a tool. The rejected-draft line is printed in this mode too.
* On an error or an interrupt, no line is added, and the failure lines follow the erased line.
* The elapsed time is `45s`, `1m24s`, or `1h02m03s`.
* The clock and the ticker are injected (G8), so a test does not sleep. `options` has `StderrTerminal func() bool`, which reads the real standard error and `TERM`. `planChange` calls it once per plan, when the reporter starts. `options` also has `Tick`, a function from an interval to a channel of ticks and a stop function.
* `Report` never waits for a write, because for Claude Code and Codex it runs inside the line callback of the child, and a write to a stalled terminal (flow control with Ctrl-S, a stuck SSH session) would otherwise block the child on its full pipe, and then the cancel and the timeout could not end the job. `Report` only stores the latest state, and the rejected drafts that are due, under a small mutex that is never held during a write, and wakes the reporter's goroutine with a non-blocking send on a channel of size 1.
* Every write to standard error is made by the goroutine, or by `Stop`, under a separate write mutex. The rejected-draft line is written by the goroutine at once, before the next status line. Each line is written once and in order, and `Stop` writes the ones that are still due. `Stop` then erases the line, ends the goroutine, and waits for it. A report after `Stop` writes nothing.
* `planChange` calls `Stop` with `defer` as soon as the reporter starts, and also calls it before the success line, so a panic or an early return cannot leave the status line on the screen.
* The reporter is safe for calls from several goroutines, and `Stop` can be called more than once.

## Tasks

| # | Task | Packages | Tests that prove it |
|---|---|---|---|
| A | Providers report progress | `internal/provider` and every driver | Lines arrive while the child still runs; a long line is skipped; no call after the return; the counts of each driver equal the counts of its final result |
| B | The reporter and the plan | `internal/stage/plan`, `internal/cli` | The terminal line and the plain line with a fake clock; a rejected draft; a stopped reporter writes nothing; an end-to-end plan |

## Open points

1. `semar run` and the fetch of `semar plan` show no progress. Both are short today.
2. The width of the terminal is not read, so a narrow terminal wraps the line.
3. A Claude Code job without the person's setup now writes the stream, which repeats every tool result, so a very large plan can reach the 64 MiB output cap where the single-object format did not.
4. The separator `·` and the spinner are not ASCII, so a terminal that shows such characters two columns wide can wrap the line.
