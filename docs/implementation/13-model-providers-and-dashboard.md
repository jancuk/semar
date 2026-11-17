# Plan 13: Model providers and the dashboard

## Goal

A person chooses which model plans their tests, and manages that choice in one place.

1. `semar plan` can use Claude Code, Codex, the Anthropic API, the OpenAI API, or any server that speaks the OpenAI chat format (DeepSeek, OpenRouter, a local server).
2. Provider definitions live in the person's own file, `~/.config/semar/providers.yaml`.
3. `semar dashboard` opens a page on the person's own machine that shows the providers, signs a harness in, sets the default provider and model, and shows usage. Every change is written to `providers.yaml`, and a hand edit of the file shows in the page.
4. A harness provider can use the person's own MCP servers and skills. A plan loads them only when the person asks ([Plan 23](23-plan-tokens.md), D1).

## Decisions

| # | Decision | Reason |
|---|---|---|
| D1 | The dashboard is a command of the same binary. It serves a page on `127.0.0.1` and opens the browser | Nothing else to install, one binary, every operating system. A native window can wrap the same page later |
| D2 | Usage is what Semar recorded: runs, turns, tokens, and cost where the provider reports it, per provider and model. For Claude Code the page also shows the subscription windows that the harness reported after the last run | Exact, offline, the same for every provider |
| D3 | A harness provider loads the person's MCP servers, skills, and permission rules by default. It never loads the person's hooks or instruction file. Each part has a switch | The person's choice. Hooks add a long start-up text to every job and run programs |
| D4 | An address, a key reference, and the setup switches come only from the person's file. The repository's `semar.yaml` names a provider and cannot define these | `semar.yaml` is committed in the repository under test. An address there would receive the source and the key |
| D5 | Semar never reads, stores, or forwards a harness sign-in. It starts the harness's own sign-in command and reads the harness's own status command | The harness owns its sign-in |
| D6 | A pasted API key goes to the keychain of the operating system. The file holds a reference, never a key | A key in a plain file is read by every program |
| D7 | An API kind runs Semar's own loop with three tools that only read the copy of the source. It has no MCP servers and no skills | The tools are Semar's code, so the limits are the same for every model |

## Trust

Untrusted, as in Plan 12: the content of the repository under test, the committed `.semar/` with `semar.yaml`, the pasted link, and the output of the model. Trusted: the person's environment, the person's `providers.yaml`, the person's harness setup, and the keychain.

New risks and the answer to each:

| Risk | Answer |
|---|---|
| Text in the repository tells the planner to call an MCP tool that writes | The person's own permission rules decide which tools run. `setup.deny` names tools that never run. `plan.md` lists every tool that was called and how many calls were refused. A plan runs without the setup unless `--with-setup` or `plan.withSetup` asks for it (Plan 23) |
| A settings file, a skill, or an MCP file inside the copy loads with the person's setup | Only the user-level setup is loaded. Plan 12 already removes agent instruction files from the copy. Task B proves by a live test that nothing in the copy loads |
| Another page in the browser calls the dashboard | The server listens on `127.0.0.1` only, needs a random token on every API call, checks `Host` and `Origin`, and sends no CORS header |
| A key appears in a log, an error, or the usage file | A key is never written to any of them. Error text passes the redaction that the Claude driver already uses |
| The repository's file names a keychain entry or a provider address | It cannot. See D4. The secret references of `semar.yaml` keep the `env:` scheme only |

## The file

`<home>/providers.yaml`, mode `0600`, in a directory of mode `0700`. `<home>` is `$SEMAR_HOME`, else `$XDG_CONFIG_HOME/semar`, else `~/.config/semar`.

```yaml
version: 1
default: claude                 # the provider that plans when nothing else names one
providers:
  - id: claude
    kind: claude                # Claude Code with its own sign-in
    model: ""                   # "" means the harness's default model
    setup:
      mcp: true                 # the person's MCP servers
      skills: true              # the person's skills
      deny: []                  # tool names or patterns that never run

  - id: codex
    kind: codex                 # Codex with its own sign-in
    setup: {mcp: true, skills: true}

  - id: anthropic
    kind: anthropic
    model: claude-opus-5-5
    apiKey: env:ANTHROPIC_API_KEY

  - id: deepseek
    kind: openai                # the OpenAI chat format
    baseUrl: https://api.deepseek.com
    model: <model name>
    apiKey: keychain:deepseek   # stored by the dashboard
```

Rules:

* F1. A missing file means the built-in list: one provider `claude` of kind `claude`, which is the default. Nothing is written until the person changes something.
* F2. `id` passes `idcheck`. IDs are unique. `default` names a provider of the list.
* F3. `kind` is `claude`, `codex`, `anthropic`, or `openai`. The `fake` kind is refused here.
* F4. `setup` is allowed on a harness kind only. A missing `mcp` or `skills` means `true`. `baseUrl` and `apiKey` are allowed on an API kind only.
* F5. `baseUrl` is `https`, or `http` when the host is a loopback address. It has no user info, query, or fragment. A missing `baseUrl` means the vendor's address for the kind.
* F6. `apiKey` is `env:NAME` or `keychain:NAME`. It is required unless the host is a loopback address. The file never holds a key, and `Save` refuses a value that is not a reference.
* F7. `model` passes `config.CheckModel`.
* F8. Unknown keys are an error. `Save` writes through no link, to a temporary file first, and then renames it.
* F9. `Save` writes a header comment that says the dashboard rewrites the file and that comments are not kept.

## Which provider plans

In this order:

1. The flag `--provider`.
2. `default` of the person's file, when that file exists.
3. `plan.provider` of `semar.yaml`.
4. The built-in `claude`.

The person's file comes before `semar.yaml` because `semar.yaml` is committed in the repository under test. Once a person has a file of their own, a repository cannot choose where that person's source goes.

An ID is looked up in the person's file first and in `providers` of `semar.yaml` second. The person's definition wins. A provider from `semar.yaml` of a harness kind gets the default setup (both switches on). A provider from `semar.yaml` of an API kind is refused, because `semar.yaml` can set neither an address nor a key. A plan clears the setup, the deny list included, unless the person asks for it with `--with-setup` or `plan.withSetup` of the person's file (Plan 23, D1).

The model: the flag `--model`, then the provider's `model`, then the harness's default. An API kind needs a model, and a missing one is an error before any job starts.

## Contract

`internal/config`: `Provider` has `BaseURL`, `APIKey`, `Setup`, and `User`, none of which YAML can set. `Setup{MCP, Skills bool; Deny []string}`.

`internal/provider`: `Result` has `CacheReadTokens`, `CacheWriteTokens`, `CostUSD`, `Tools`, `Denied`, `Limits`, and `Loaded`. `Account` is an optional interface of an `Instance` with `Status` and `LoginCommand`. Kind constants and `IsHarness`, `IsAPI`.

These are in the tree. Every task builds on them and does not change them without saying so in its report.

## Tasks

Each task owns its directories and commits only its own paths.

### A. The person's file, the usage log, and the keychain

`internal/userconfig`:

```go
const FileName = "providers.yaml"
func Home(lookupEnv func(string) (string, bool)) (string, error)
type File struct { Version int; Default string; Providers []Provider }
type Provider struct { ID, Kind, Model, BaseURL, APIKey string; Setup *Setup }
type Setup struct { MCP, Skills *bool; Deny []string }
func Builtin() File
func Load(home string) (f File, exists bool, err error)   // a missing file gives Builtin()
func Save(home string, f File) error                      // validates first
func (f File) Validate() error
func (f File) Find(id string) (config.Provider, bool)     // User is true, the setup defaults are applied
func (f File) All() []config.Provider
```

`internal/usage`: an append-only log `<home>/usage.jsonl`, mode `0600`, one JSON object per line.

```go
type Record struct {
	At         time.Time        `json:"at"`
	Provider   string           `json:"provider"`
	Kind       string           `json:"kind"`
	Model      string           `json:"model"`
	Command    string           `json:"command"`           // "plan"
	Ref        string           `json:"ref,omitempty"`     // the plan ID
	Outcome    string           `json:"outcome"`           // "ok", "rejected", or "failed:<class>"
	Attempts   int              `json:"attempts"`
	Turns      int              `json:"turns"`
	InputTokens, OutputTokens, CacheReadTokens, CacheWriteTokens int
	CostUSD    float64          `json:"costUsd,omitempty"`
	DurationMs int64            `json:"durationMs"`
	Limits     []provider.Limit `json:"limits,omitempty"`
}
func Append(home string, r Record) error
func Read(home string, since time.Time) ([]Record, error)   // skips a line it cannot read; a missing file is no error
type Row struct { Provider, Kind, Model string; Runs, Failed, Turns, InputTokens, OutputTokens, CacheReadTokens, CacheWriteTokens int; CostUSD float64 }
func Summarize(recs []Record) []Row                         // sorted by provider, then model
func LatestLimits(recs []Record) map[string]Limits          // per provider: the windows and the time of the record
```

A record never holds a path, a repository name, a prompt, or model output.

`internal/keychain`: `Store` with `Get`, `Set`, and `Delete` on names under the service `semar`. `OS()` is the real store, `Memory()` is for tests. `Resolver(lookupEnv, store)` returns `func(ref string) (string, error)` for `env:NAME` and `keychain:NAME`. An error never holds the value.

### B. Claude Code: setup, tool record, sign-in

`internal/provider/claude`. The flags per setup:

| Setup | Flags |
|---|---|
| Both off | As today: `--tools Read,Grep,Glob --strict-mcp-config --setting-sources ""`, the `json` output, and the short environment |
| MCP on | `--setting-sources user`, the settings below, no `--strict-mcp-config` |
| Skills on | `--setting-sources user`, the settings below, and `Skill` joins `--tools` |
| MCP off, skills on | The line above with `--strict-mcp-config` |
| `deny` | `--disallowedTools` with each entry. An entry that is empty, starts with `-`, or holds a comma or a control character is refused |

The settings with any switch on: `--settings '{"disableAllHooks":true,"permissions":{"blockReadsOutsideWorkingDirectories":true}}'`. The second key is needed: a live test showed that under a person's permission mode `auto` the file tools read a file outside the working directory without it.

With any switch on, the child gets the person's environment without the model vendors' variables, without `SEMAR_*`, without the variables that `semar.yaml` names as secrets, and without the markers of an enclosing harness session. Two variables are added: `CLAUDE_CODE_DISABLE_CLAUDE_MDS=1`, because the person's own instruction file loads with `--setting-sources user`, and `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1`. The driver passes no permission mode: the person's rules apply.

With any switch on, the output format is `stream-json`. The driver reads the `init` event (MCP server names, number of skills, permission mode), each tool call of an assistant message, the `rate_limit_event`, and the `result` event (as today, plus `total_cost_usd`, the cache token counts, and the permission denials).

The driver implements `provider.Account`: `Status` runs `claude auth status --json` and keeps `loggedIn`, `authMethod`, and `subscriptionType` only. `LoginCommand` is `claude auth login`.

Prove by live tests, with at most six short runs, and write the results in the report:

1. With the setup on, nothing inside the working directory loads: a settings directory, an MCP file, a skill directory, an instruction file.
2. Whether the person's own instruction file loads with `--setting-sources user`, and the supported way to keep it out.
3. The file tools still refuse a path outside the working directory.

### C. Codex

`internal/provider/codex`: a driver for `codex exec` with a read-only sandbox, the output schema, JSON events, the working directory as the only root, no session kept. Setup on means the person's Codex configuration loads. Setup off means it does not. The driver implements `provider.Account` with `codex login status` and `codex login`.

Prove by live tests, with at most six short runs: the exact flags of the installed version, the events that carry the usage, whether a file outside the working directory can be read under the read-only sandbox, and what the sign-in needs when the person's configuration is not loaded. A property that is weaker than the Claude driver's is written in the report and in the package comment.

### D. API kinds

`internal/provider/agentloop`: the loop and the three tools over `os.Root` of the working directory.

* `read_file(path, offset, limit)`: lines of one file, at most 256 KiB per call.
* `search(pattern, glob)`: a regular expression of RE2 over the files that match, at most 200 hits.
* `list_files(glob)`: at most 2000 paths.
* `submit(...)`: its parameters are the output schema. Its call ends the job.

The loop stops at `MaxTurns`, at the timeout, or at `submit`. It counts tool calls by name and sums the usage of every response.

`internal/provider/anthropic` (the Messages API) and `internal/provider/openai` (chat completions with tools) are the two adapters. Each factory takes the key resolver and an HTTP client. Errors map to the failure classes: 401 and 403 to `permission_error`, 429 to `usage_limit`, 5xx to `provider_error`, a network error to `transport_error`. One retry for 429 and 5xx. A response body is read up to a limit. `Health` checks that the key resolves and makes no model turn. Both implement `provider.Account` with the method `api key` and a nil login command.

Tests use `httptest` servers. No test calls a vendor.

### E. The dashboard

`internal/dashboard` and the command `semar dashboard [--port n] [--no-open]`.

The server listens on `127.0.0.1`. It prints the address with the token, opens the browser unless `--no-open`, and stops on an interrupt.

| Method and path | Does |
|---|---|
| `GET /` | The page, embedded in the binary. No file from another host |
| `GET /api/state` | The home, the file path, whether the file exists, the default, and each provider with its definition, health, account status, and whether its key resolves |
| `PUT /api/providers/{id}` | Creates or changes one provider |
| `DELETE /api/providers/{id}` | Removes one provider. The default cannot be removed |
| `PUT /api/default` | Sets the default provider |
| `PUT /api/providers/{id}/key` | Stores a pasted key in the keychain and sets `apiKey` to `keychain:<id>` |
| `POST /api/providers/{id}/login` | Starts the harness's sign-in command and returns a job |
| `GET /api/logins/{job}`, `DELETE /api/logins/{job}` | The output lines and state of the job; cancels it |
| `GET /api/providers/{id}/models` | The models a person may choose: from Codex's own catalog, from the models route of an API vendor, or the fixed aliases of Claude Code, which has no command that lists models. Also the models that the usage log holds for the provider. No model turn |
| `GET /api/usage?days=n` | The rows, the latest limits, and the last 50 records |

Every `/api/` call needs the token in the header `X-Semar-Token`, a `Host` of `127.0.0.1:<port>` or `localhost:<port>`, and, when `Origin` is present, the same origin. A change needs the content type `application/json`. The file is read on every `GET /api/state`, so a hand edit shows on reload.

The page uses `internal/dashboard/ui/style.css` as given and does not change its tokens.

### F. The command line

`internal/cli`, `internal/config`, `internal/stage/plan`, `internal/prompt`.

* The order of [Which provider plans](#which-provider-plans), with `--provider` and `--with-setup` on `semar plan`.
* The registry gets the `codex`, `anthropic`, and `openai` kinds.
* After a job, one usage record is appended. A failure to append is one notice on standard error and never fails the command.
* `plan.md` names the provider, its kind and model, the number of MCP servers and skills that were loaded, the file tools with their number of calls, the number of calls of MCP tools, and the number of refused calls.
* The start line says what the planner may use, for example "with your MCP servers and skills, which add tokens to every turn" when a plan runs with the setup.
* `semar providers` prints the providers, the default, and the path of the file.

## Checks

* `make check` passes.
* A plan with the `fake` kind writes a usage record, and the dashboard's usage call returns it.
* A plan with an API kind against an `httptest` server reads files through the tools and writes scenarios.
* `semar dashboard --no-open` answers `GET /api/state` with the token and refuses it without the token, with another `Host`, and with another `Origin`.
* One live plan with Claude Code and the setup on, in a scratch clone.

## What the live tests found

| Kind | Finding |
|---|---|
| Claude Code | Nothing inside the working directory loads with the setup on: a settings directory with a hook and a permission rule, an MCP file, a skill directory, and an instruction file were all planted, and none took effect |
| Claude Code | The person's permission mode decides which MCP tools run. Under the mode `auto`, a classifier approves them. Only `setup.deny` stops a tool for certain |
| Claude Code | The person's plugins load with the setup on. Hooks stay off |
| Claude Code | A job with the setup on costs more. On a machine with 11 MCP servers and 139 skills, one plan of 20 turns used 1,665,000 input tokens, of which 1,418,000 were read from the cache. The same plan without the setup used about 161,000. The planner called no MCP tool in that run |
| Codex | The read-only sandbox of the tool lets a command read the whole disk. The driver defines its own permission profile, which reads the system's files and the working directory only. Proven on macOS only |
| Codex | The person's own instruction file loads in every mode. No switch keeps it out and keeps the sign-in |
| Codex | The tool sends the schema in a strict mode that refuses the plan schema. The driver then puts the schema in the prompt and checks that the answer is one JSON object. The plan stage's own checks still run |
| Codex | `setup.deny` cannot be honoured, so the driver refuses a provider that has one. There is no turn limit; the timeout works |
| Codex | A plan came back with no scenario and no tool call. The summary said that the source directory is inside a path that the active filesystem policy denies. The copy of the source was in the system temporary directory, which the profile denies, and the model read that and stopped. Reads of the working directory do work under the profile. The copy is now made under `semar/work` in the user cache directory, and the deny entries stay |
| API kinds | No vendor was called. The adapters are tested against local servers only |

## Reasoning level

A provider can carry `reasoning: <level>`, and `semar plan --reasoning <level>` sets it for one run. Empty means the model's own default. Only the person's file can set it.

| Kind | What is sent |
|---|---|
| `claude` | `--effort=<level>`: `low`, `medium`, `high`, `xhigh`, or `max` |
| `codex` | `--config model_reasoning_effort="<level>"`. The levels of each model come from Codex's catalog |
| `anthropic` | `output_config.effort` in the request |
| `openai` | `reasoning_effort` in the request. For an address that is not OpenAI's own, only when the person set a level |

The level is written to the usage record and to `plan.md`. The models route gives, per model, a description, the levels it accepts, and its default level, and the page offers only those.

## Open points


* The subscription windows are known only after a run. There is no command that returns them without a model turn.
* The `openai` kind uses chat completions with tools. The vendor's documentation says that some of its newest models take tools only through another API, so those models may not plan through this kind. No vendor was called to check.
* An API kind cannot use MCP servers. The person's MCP servers reach the planner through a harness kind only.
* Codex and each API vendor report usage in their own way. The usage record holds what each reports and leaves the rest at zero.
* `plan.md` counts the calls of MCP tools and withholds their names, because a name holds the name of a server on the person's machine and the file may be committed.
