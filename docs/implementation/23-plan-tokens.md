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
