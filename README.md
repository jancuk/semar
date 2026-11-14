# Semar

Semar is an open-source tool in which an AI agent tests a service the way a QA engineer does: it calls the API, uses the web app, uses the Android app, and reads logs and traces. Everything the agent does is recorded as evidence. Code, not the model, computes each outcome. A recorded script replays with no model.

It is built for developer-led teams, including teams with no dedicated QA.

## The problem

Without dedicated QA, developers divide their time between building, checking changes by hand, and maintaining tests. Verification may be incomplete or skipped, and expected behavior may exist only in conversations. Teams with QA face a verification queue. Both need useful tests and trustworthy evidence without a new full-time maintenance job.

## How a result can be trusted

1. **The agent tests.** It drafts scenarios from a change, works through them, explores around the change, and leaves a script behind.
2. **The recorder stores the evidence.** Every request, response, and action is written down, with secrets redacted. The agent does not record its own work.
3. **Code computes the outcome.** Each scenario ends as `passed`, `failed`, `blocked`, `inconclusive`, `not_run`, or `stale`. A pass needs a confirmed expected result and recorded evidence.
4. **Replay needs no model.** A re-test or a regression run replays the stored scripts and costs no model call.

If the expected behavior is written nowhere outside the code, no tool can detect a misunderstood requirement. Semar marks such results as needing a person to confirm them.

## Status

The model-free core is implemented in Go: replay of accepted scripts against an HTTP API, an effects policy that limits what a test may change, redaction, an evidence store, the verdict, and the report.

The agent layer, planning from a change, security probes, providers and agents, data stores, team mode, web, and Android are specified in `docs/implementation/` and are not built yet. [docs/implementation/README.md](docs/implementation/README.md) gives the sequence of plans and what each one adds. Plans 01 to 04 are the first usable version, from a plan to a report for one API ticket.

## Quick start

Semar needs the Go toolchain named in `go.mod`.

```
make build     # writes bin/semar
make check     # gofmt, go vet, staticcheck, govulncheck, and the tests
```

All four commands take `--dir <path>`, the root of the repository that holds `.semar/`. It defaults to the current directory.

```
semar run --scenario <id>      # replay one scenario; repeat the flag for more
semar run --all                # replay every scenario in .semar/scenarios
semar script add <file>        # check a script against its scenario and store it
semar confirm <scenario id> --yes   # confirm the expected results that need a person
semar activity <run id|latest> # print what a run did, in order
```

`semar run` also takes `--env <name>`, which is needed when `semar.yaml` defines several environments. `semar activity` takes `--writes`, `--scenario <id>`, and `--pass <name>` to narrow the output.

The layout of `.semar/`:

```
.semar/
  semar.yaml          environments, services, auth profiles, redaction, policy
  scenarios/          one YAML file per scenario, for example SHOP-482/SHOP-482-01.yaml
  runs/<run id>/      run.json, report.md, activity.jsonl, and the recorded exchanges of one run
  store/              accepted scripts and confirmations
```

A working example is in `testdata/e2e/`: a `semar.yaml`, a scenario, and a script.

Exit codes of `semar run`:

| Code | Meaning |
| --- | --- |
| 0 | Every scenario passed |
| 1 | At least one scenario failed |
| 2 | Nothing failed, but a scenario could not be judged, or no scenario ran |
| 3 | An error: bad configuration, a bad file, or a run that stopped |

## Documents

| File | What it holds |
| --- | --- |
| [docs/PRD.md](docs/PRD.md) | Product requirements: scope, users, decisions, requirements by milestone, risks, and open questions |
| [docs/TRD.md](docs/TRD.md) | Technical requirements: the Go design, the tool layer, the provider layer, verdict rules, configuration, commands, milestones, and spikes |
| [docs/WORKFLOW.md](docs/WORKFLOW.md) | The test workflow from intake to release, who acts in each phase, and the exit criteria |
| [docs/USER_SCENARIOS.md](docs/USER_SCENARIOS.md) | Walkthroughs from the user's side: setup, a failed scenario, pipeline mode, sign-in, and regression |
| [docs/implementation/README.md](docs/implementation/README.md) | The implementation guide: engineering rules, architecture, dependencies, and the sequence of plans |

## Open questions

Whether the vendors of Claude Code and Codex CLI allow a subscription login to drive their harness from another tool is not confirmed. Semar runs the user's own unmodified harness and never touches its credentials, and API keys work from the first release. The PRD lists this under its risks.

## License

Apache-2.0 is the intended license. The repository has no `LICENSE` file yet.
