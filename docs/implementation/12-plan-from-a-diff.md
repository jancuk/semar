# Plan 12: A test plan from a diff

> **For agentic workers:** This is a specification. Each task names its files and the tests that prove it.

* **Guide:** [implementation guide](README.md). **Spec:** TRD, [Planning from a change](../TRD.md#planning-from-a-change)
* **Needs first:** Plan 01.
* **Relation to other plans:** This is the shortest path from a deployed change to scenarios that `semar run` replays. It builds the parts of Plans 02 and 03 that this path needs: the git reader, the change set, and one agent provider. Plans 02 and 03 keep the rest: the daemon, the tool server, live recording of scripts, the tracker port, the feature map, and risk levels.

**Goal.** One command reads the difference between two branches, finds the tickets in it, and writes scenarios and scripts for the changed behaviour. A person writes no YAML.

**What works when it is done.**

```bash
cd <the repository under test>
semar plan --diff 'https://bitbucket.org/<workspace>/<repo>/branches/compare/<head>%0D<base>' \
  --base-url https://<host of the test environment> --token-env MY_TOKEN
semar run --all --build <head commit>     # shows whether the drafted requests fit
semar confirm --all                       # a person accepts the expected results
semar run --all --build <head commit>     # the result
```

* `semar changes --diff <link or range>` prints the change table with no model: the tickets, their commits, the commits with no ticket, and the changed files.
* `semar plan --diff <link or range>` writes `.semar/semar.yaml` when it is missing, scenario files, stored scripts, and `plan.md`.
* `semar init --base-url <url>` writes only `.semar/semar.yaml`.
* `semar run --build <commit>` declares the build of a service that has no endpoint for its version.

## Architecture

```text
link or range --> codehost.ParseCompare --> vcs.Git (fetch, log, diff, archive) --> changeset.Build
                                                                                         |
     .semar/scenarios/<ticket>/*.feature  <-+                                             v
     stored scripts                     <--+-- stage/plan.Validate <-- provider.Instance.Run (read-only, one JSON object)
     .semar/plans/<head>/plan.md        <--+
```

* Everything left of the provider is code with no model, and so is `Validate` on its right. The planner is the only model call.
* The planner reads a copy of the source at the head commit. It has three tools, all read-only: read a file, search file contents, and list files. It has no shell, no network, and no way to write a file or to reach the test environment (A6).
* The planner returns one JSON object that the harness checks against a JSON Schema. Code then checks it again, builds the scenario and script values, and validates them with `scenario.Validate` and `script.Validate`. Problems go back to the planner, for at most 3 attempts (A4).
* A drafted script is a proposal, like a hand-written one. It was never sent to the service. `semar run` shows whether it fits.
* The planner never labels an expected result. Code gives every drafted expectation the source `diff`, so each scenario needs `semar confirm` before it can pass (product rule: an agent's reading of a diff is not a requirement).

**Ports added:** the agent provider, in a first form that Plan 02 extends with sessions and tool events.

## File structure

```text
internal/domain/change.go           ChangeSet, ChangeCommit, TicketChange, FileChange
internal/vcs/git.go                 reads a git repository through the git command (G13)
internal/vcs/archive.go             the files of one commit, copied to a directory, read from the object store
internal/codehost/compare.go        Compare, LinkParser, ParseCompare, SameRepo
internal/codehost/bitbucket/        the compare link of Bitbucket Cloud
internal/codehost/github/           the compare link of GitHub
internal/codehost/gitlab/           the compare link of GitLab
internal/changeset/build.go         commits, tickets, and files of a range
internal/changeset/kinds.go         file kinds from path rules
internal/changeset/table.go         the change table as Markdown
internal/provider/provider.go       Instance, Job, Result, FailureClass, Error, Health, the registry
internal/provider/process.go        start a child in its own process group, stop the group (G13, H9)
internal/provider/env.go            the child's environment (H2)
internal/provider/claude/           the Claude Code driver: args.go, parse.go, driver.go, testdata/
internal/provider/fake/             replays outputs from files
internal/config/                    gains providers, tickets, and plan
internal/build/declared.go          a build that the user declares, and a prober that combines both kinds
internal/scenario/marshal.go        a Scenario to YAML that Parse reads back unchanged
internal/script/marshal.go          a Script to YAML that Parse reads back unchanged
internal/prompt/prompt.go, plan.md  the embedded planner prompt with a version line (A9)
internal/stage/plan/draft.go        Draft, the JSON Schema
internal/stage/plan/validate.go     a Draft to scenarios and scripts, with every problem
internal/stage/plan/plan.go         the attempts loop
internal/stage/plan/render.go       plan.md
internal/cli/init.go, changes.go, plan.go, wire.go
```

## Interfaces

```go
// internal/domain
type ChangeCommit struct {
	ID      string   // full hash
	Subject string
	Body    string
	Author  string   // name only
	At      time.Time
	Merge   bool
	Tickets []string // keys found in the subject and the body, sorted
	Files   []string // paths the commit touched. Empty for a merge
}
type TicketChange struct {
	Key     string
	Commits []string // commit IDs, oldest first
	Files   []string // changed files that a commit of this ticket touched, sorted
}
type FileChange struct {
	Path    string
	OldPath string // set for a rename
	Status  string // "added" | "modified" | "deleted" | "renamed"
	Kind    string // "source" | "test" | "docs" | "generated" | "migration" | "config"
	Added   int
	Deleted int
	Binary  bool
	Tickets []string
}
type ChangeSet struct {
	Base, Head             string // the names as given
	BaseCommit, HeadCommit string
	MergeBase              string
	Commits                []ChangeCommit // reachable from Head and not from Base, oldest first
	Tickets                []TicketChange // sorted by key
	NoTicket               []string       // IDs of commits that are not merges and carry no key
	Files                  []FileChange   // the difference between MergeBase and HeadCommit, sorted by path
}

// internal/vcs
type Commit struct {
	ID, Subject, Body, Author string
	At      time.Time
	Parents []string
	Files   []string
}
type FileStat struct {
	Path, OldPath, Status string
	Added, Deleted        int
	Binary                bool
}
type Git struct{ /* unexported */ }
func Open(dir string) (*Git, error)                                         // dir must be inside a work tree
func (g *Git) RemoteURL(ctx context.Context, remote string) (string, error)
func (g *Git) Fetch(ctx context.Context, remote string, branches ...string) error
func (g *Git) Resolve(ctx context.Context, remote, name string) (string, error) // refs/remotes/<remote>/<name>, then the name itself
func (g *Git) MergeBase(ctx context.Context, a, b string) (string, error)
func (g *Git) Log(ctx context.Context, from, to string) ([]Commit, error)    // commits of to that from lacks, oldest first
func (g *Git) DiffStat(ctx context.Context, base, head string) ([]FileStat, error)
func (g *Git) Diff(ctx context.Context, base, head string) ([]byte, error)
type Skipped struct{ Path, Reason string }
func (g *Git) Archive(ctx context.Context, commit, dest string) ([]Skipped, error) // dest must not exist

// internal/codehost
type Compare struct {
	Host string // "" for a plain range
	Repo string // the path of the repository on the host, for example "acme/orders". "" for a plain range
	Base string // what is deployed to production
	Head string // what is deployed to the test environment
}
type LinkParser func(u *url.URL) (Compare, bool, error) // false: not a link of this host
func ParseCompare(arg string, parsers ...LinkParser) (Compare, error)
func SameRepo(c Compare, remoteURL string) bool // true for a plain range

// internal/changeset
type Repo interface {
	MergeBase(ctx context.Context, a, b string) (string, error)
	Log(ctx context.Context, from, to string) ([]vcs.Commit, error)
	DiffStat(ctx context.Context, base, head string) ([]vcs.FileStat, error)
}
type Options struct {
	Base, Head             string // names, for the report
	BaseCommit, HeadCommit string
	Ticket                 *regexp.Regexp
}
func Build(ctx context.Context, repo Repo, o Options) (domain.ChangeSet, error)
func Kind(path string) string
func Table(w io.Writer, cs domain.ChangeSet) error

// internal/provider
type FailureClass string // "usage_limit" | "provider_error" | "transport_error" | "permission_error" | "validation_error" | "not_installed" | "unknown"
type Error struct {
	Class  FailureClass
	Detail string // never holds the prompt or the output
}
func (e *Error) Error() string
type Health struct {
	Installed bool
	Version   string
	Detail    string
}
type Job struct {
	Prompt   string
	Workdir  string          // the only directory the harness may read
	Schema   json.RawMessage // the JSON Schema of the output
	Model    string          // "" means: pass no model flag
	MaxTurns int
	Timeout  time.Duration
}
type Result struct {
	Output       json.RawMessage // one JSON object
	Model        string
	Turns        int
	InputTokens  int
	OutputTokens int
}
type Instance interface {
	ID() string
	Kind() string
	Health(ctx context.Context) Health // no model turn (H6)
	Run(ctx context.Context, job Job) (Result, error) // a failure is a *Error
}
type Factory func(cfg config.Provider) (Instance, error)
func NewRegistry() *Registry
func (r *Registry) Register(kind string, f Factory)
func (r *Registry) New(cfg config.Provider) (Instance, error) // an unknown kind is an error that lists the kinds

// internal/scenario and internal/script
func Marshal(s domain.Scenario) ([]byte, error)
func Marshal(s domain.Script) ([]byte, error)

// internal/stage/plan
type Planner interface {
	ID() string
	Kind() string
	Run(ctx context.Context, job provider.Job) (provider.Result, error)
}
type Input struct {
	Change      domain.ChangeSet
	SourceDir   string            // the copy of the source at the head commit
	Services    []string          // names from the configuration
	Methods     []string          // methods the environment allows
	AuthProfile string
	Existing    map[string][]domain.Scenario // by ticket: scenarios that already exist
	Limits      config.Plan
	Model       string
}
type Problem struct{ Path, Message string }
type Output struct {
	Draft     Draft
	Scenarios []domain.Scenario
	Scripts   []domain.Script
	Attempts  int
	Result    provider.Result
}
func Schema() json.RawMessage
func Validate(d Draft, in Input) ([]domain.Scenario, []domain.Script, []Problem) // pure, apart from reading SourceDir for cites
func Run(ctx context.Context, p Planner, in Input) (Output, error)
func Render(w io.Writer, in Input, out Output) error
```

## Formats

**Configuration.** Every block is optional. The values shown are the defaults.

```yaml
providers:
  - id: claude
    kind: claude        # claude | fake
    model: ""           # empty: the harness's own default
tickets:
  pattern: "[A-Z][A-Z0-9]+-[0-9]+"
plan:
  provider: ""          # empty: the first provider
  maxScenarios: 12
  maxTurns: 60
  timeout: 15m         # at most 24h
```

A `fake` provider has `outputs: [file, ...]`, one JSON file per call, relative to the repository root. It makes every stage testable with no model (A11).

The configuration is a file of the repository under test, so it never names a program to start. The Claude Code binary is the one on `PATH`, or the absolute path in the environment variable `SEMAR_CLAUDE_BIN`. A `binaryPath` key is an error.

**Links.** `ParseCompare` accepts:

| Form | Example | Base and head |
| --- | --- | --- |
| A plain range | `master..release/week-02`, or with `...` | `base..head` |
| Bitbucket Cloud | `https://bitbucket.org/acme/orders/branches/compare/release%2Fweek-02%0Dmaster` | The part before `%0D` is the head, and the part after it is the base. `..` is accepted in place of `%0D` |
| GitHub | `https://github.com/acme/orders/compare/master...release/week-02` | `base...head`, or with `..` |
| GitLab | `https://gitlab.example.com/group/sub/orders/-/compare/master...release%2Fweek-02` | `base...head` |

A link to one pull request or merge request is refused with a message that says to pass the compare link or `base..head`. Reading a request needs the host's API, which belongs to Plan 08.

**The draft.** What the planner returns. Code assigns scenario IDs, sources, and the service of a request when the configuration has one service.

```json
{
  "summary": "one paragraph: what this change does to the service's behaviour",
  "tickets": [
    {"key": "SHOP-482", "kind": "behaviour", "behaviour": "what changed for a caller", "risk": "standard", "reason": "why this risk"}
  ],
  "scenarios": [
    {
      "ticket": "SHOP-482",
      "title": "Refund above the order total is rejected",
      "intent": "Before: ... Now: ...",
      "steps": [
        {
          "id": "create-order",
          "setup": true,
          "action": "Create a paid order for 100000",
          "service": "orders",
          "method": "POST",
          "hint": "/v1/orders",
          "path": "/v1/orders",
          "query": {},
          "headers": {},
          "json": {"amount": 100000},
          "captures": [{"name": "order_id", "jsonpath": "$.data.id"}],
          "expect": []
        },
        {
          "id": "refund-too-much",
          "action": "Request a refund of 150000 for that order",
          "method": "POST",
          "hint": "/v1/orders/{id}/refunds",
          "path": "/v1/orders/{{order_id}}/refunds",
          "json": {"amount": 150000},
          "expect": [
            {"check": {"status": 422}, "why": "internal/refund/handler.go:41 returns 422 when the amount is above the rest"}
          ]
        }
      ]
    }
  ],
  "notDrafted": [
    {"ticket": "SHOP-482", "what": "a refund on an order of another branch", "needs": "the ID of an order that the test account may not read"}
  ],
  "uncovered": [
    {"file": "internal/refund/audit.go", "reason": "writes an audit row that no endpoint returns"}
  ]
}
```

* `tickets[].kind` is `behaviour`, `refactor`, `config`, or `none`. `risk` is `low`, `standard`, or `high`.
* `check` has the shape of a check in a scenario file. The schema allows exactly the operators that `domain.Check` has.
* `why` becomes the `cite` of the expectation. It starts with `<file>:<line>`, and the file is one of the source copy.

**Files written.**

| Path | Content |
| --- | --- |
| `.semar/semar.yaml` | Only when it is missing, from the flags of `init` |
| `.semar/scenarios/<ticket>/<ticket>-NN.feature` | One scenario, as a Gherkin file (Plan 24). `NN` continues after the highest number that exists for the ticket |
| The script store | One script per scenario, `authoredBy: {instance: <provider id>, model: <model>, harness: <provider kind>}` |
| `.semar/plans/<first 12 characters of the head commit>/plan.md` | The summary, the change table, one section per ticket with its scenarios, what was not drafted and what it needs, the files that no scenario covers, the provider, the model, the attempts, and the tokens |
| `.semar/plans/<the same>/draft.json` | The accepted draft |

**`semar init`** writes this, and refuses when the file exists:

```yaml
version: 1
environments:
  <--env, default "uat">:
    kind: test
    hosts: ["<host of --base-url, with its port when the URL names one>"]
    methods: [GET, POST, PUT, PATCH, DELETE]
    effects:
      mode: test_data
    services:
      - name: <--service, default: the name of the repository directory>
        baseUrl: <--base-url>
authProfiles:
  default:
    session: {header: <--auth-header, default Authorization>, value: "<--auth-value, default: Bearer {{secret.TOKEN}}>"}
    secrets: {TOKEN: "env:<--token-env, default SEMAR_TOKEN>"}
```

With `--build-url <url>` it adds an `http` build probe to the service, and `--build-commit <JSONPath>` names where the answer holds the commit. The default is `$.commit`.

**A declared build.** A result belongs to a build, so a run with no recorded build is `inconclusive`. A service with no endpoint for its version gets its build from the command line: `semar run --build <commit>`, or `--build <service>=<commit>` when several services lack a probe. The run stores the build with `declared: true`, and the report says that it was declared and not read from the service, and that a declared build cannot show a redeploy during the run.

## Rules

| # | Rule | Source |
| --- | --- | --- |
| R1 | Every git call is `exec.CommandContext` with an argument list. A branch name, a commit, or a path that starts with `-` or holds a control character is refused before git runs, and names follow `--end-of-options` or `--` | G13 |
| R2 | Git runs with `GIT_TERMINAL_PROMPT=0`, no pager, `core.hooksPath` set to the null device, `core.fsmonitor` off, and no signature check in a log. Semar never writes to the work tree, the index, or a branch of the repository under test. `Fetch` updates only remote-tracking references, and only for names that are branches of the remote. A branch that the remote no longer has is an error. A fetch uses the transport and the credentials of the user's own git configuration. That file is the user's and is not content of the repository | The content of the repository under test is not trusted |
| R3 | `Archive` reads the tree and the file contents from the object store, so no attribute, filter, or substitution of the repository applies. It copies regular files only, leaves out a path that is not local (`filepath.IsLocal`), every entry inside a `.git` directory in any spelling that a file system could read as `.git`, links, and submodules, and caps one file at 5 MiB and the copy at 500 MiB. Every file it leaves out is returned with the reason and listed in `plan.md` | G19 |
| R4 | The copy of the source lives in a new directory with mode 0700 under `semar/work` in the user cache directory, not in the system temporary directory, because a harness profile can deny that one. It is removed when the command ends, also on an error or a cancel. A copy that a crash left is removed by the next plan once it is 7 days old. `plan.timeout` is refused above 24 hours, so the three drafts of a running plan take at most 72 hours and its copy is never that old. Without a cache directory, the system temporary directory is used | G19 |
| R5 | Before the planner starts, these are removed from the copy: `CLAUDE.md`, `CLAUDE.local.md`, `AGENTS.md`, `.mcp.json`, and the directories `.claude`, `.codex`, and `.cursor`, at every depth | H3 |
| R6 | A ticket key matches only at a word boundary. `SHOP-4820` does not hold the key `SHOP-482` | Plan 03, review focus 1 |
| R7 | A merge commit adds its keys to the change set and adds no files. A file belongs to the tickets of the commits, not merges, that touched it | TRD, Limits of linking |
| R8 | When a link names a repository, the `origin` remote of the directory must name the same one. Otherwise the command stops with both names | A plan for the wrong repository is worse than none |
| R9 | The Claude driver starts the binary found on `PATH`, or named by `SEMAR_CLAUDE_BIN`, as an absolute path that is inside neither the copy nor the work tree of the repository under test: `claude -p --output-format json --json-schema <schema> --tools Read,Grep,Glob --strict-mcp-config --setting-sources "" --no-session-persistence --max-turns <n>`, with `--model=<name>` as one argument only when one is set, the prompt on standard input, and the copy as the working directory. It never passes `--bare` | H3. Checked on Claude Code 2.1.325: with these flags and the environment of R10, reading a file outside the working directory is refused for an absolute path, a relative path, a link, a search, and a file listing |
| R10 | The child's environment is `PATH`, `HOME`, `USER`, `LOGNAME`, `LANG`, `TERM`, and `TMPDIR` from the parent. No other variable is passed | H2 |
| R11 | On a cancel or a timeout the driver stops the child's whole process group and waits for it | H9 |
| R12 | The driver reads `is_error`, `subtype`, `structured_output`, `num_turns`, `usage`, and `modelUsage` from the output, and ignores every other field. An output with no `structured_output` object is `validation_error` | H4 |
| R13 | `Health` runs only `<binary> --version` | H6 |
| R14 | `Validate` reports a problem when: a ticket key is not in the change set, a service or a method is not one the configuration allows, a step ID repeats, a `{{name}}` is used before a step captures it, a header names the session header of the auth profile, a `why` names no file of the source copy or a line past its end, a scenario has no expectation, a title repeats one that exists for the ticket, a `behaviour` ticket has neither a scenario nor a `notDrafted` entry, or the scenarios exceed `maxScenarios` | A6 |
| R15 | Every problem of `scenario.Validate` and `script.Validate` on the built values is a problem of the draft, with the path of the step in the draft | One set of rules for a file, however it was written |
| R16 | Problems return to the planner in the next prompt, with its previous draft in a file of the copy. After 3 rejected drafts nothing is written, and the command prints the problems and exits with 3 | A4 |
| R17 | Every drafted expectation has the source `diff`. The draft has no field for a source | Product rule: the agent does not label expected results |
| R18 | The prompt, the draft, and text from the repository are data. No field of the draft sets a host, a secret, an auth profile, or a file path to write. File names come from the ticket key and a number that code assigns | A7 |
| R19 | The planner is told the names of the services and that an auth profile exists. It is never given a base URL, a header value, or a secret | A5 |
| R20 | `semar changes` starts no provider. `semar plan` starts none when the change set has no commit | A11 |
| R21 | `semar plan` writes every file or none: it validates the whole draft first, writes scenario files before scripts, and removes what it wrote when a later write fails. No directory on the way to a file may be a link | A half-written plan cannot be told from a small one, and a link must not move a file out of the repository |
| R23 | A request header has a valid name, no control character in its value, and is not one that the HTTP client owns, such as `Host` or `Content-Length`. The rule is in `script.Validate`, so it holds for a hand-written script too | One set of rules for a request |
| R25 | The names of services, environments, auth profiles, and models come from a file of the repository, so each is checked: a name is one safe path segment, and a model name holds only letters, digits, and `. _ : / [ ] -` | A name must not become a flag, a path, or part of a command a person copies |
| R26 | `semar init`, `semar plan`, and `semar confirm` write through no link: every directory from `.semar` down is checked, and a refused or undone plan leaves the `.semar` tree as it was | A committed link must not move a file out of the repository |
| R24 | A ticket key must be usable as one path segment. A range in which no commit names a ticket stops the command before a provider starts | Scenario IDs and directories come from the key |
| R22 | A request in a draft may use a literal ID only when the same scenario captured it or the source copy holds it as a fixed test value. Otherwise the planner lists the case under `notDrafted` with what it needs. This rule is in the prompt and in `plan.md`. Code cannot check it | A drafted test must not guess data of the test environment |

## Tasks

| # | Task | Files | Tests that prove it |
| --- | --- | --- | --- |
| 1 | The change types and the git reader | `domain/change.go`, `vcs/*` | A repository built in `t.TempDir()`: log of a range with the files of each commit, a merge, a diff with a rename, a deletion, and a binary file, the merge base, and an archive. A branch named `--upload-pack=x` is refused (R1). An archive with a link and with a file above the cap (R3). A hook in the fixture repository does not run (R2) |
| 2 | Compare links | `codehost/*` | One row per form of the table, with encoded slashes. A pull request link of each host is refused with the message. `SameRepo` for an SSH and an HTTPS remote, with and without `.git` |
| 3 | The change set and its table | `changeset/*` | The fixture has two tickets, a merge that names a ticket, a commit with two keys, a commit with no key, and a key inside a longer key (R6, R7). A golden file for the table |
| 4 | The provider contract, the child process, and the fake | `provider/provider.go`, `process.go`, `env.go`, `provider/fake` | An unknown kind lists the kinds. A child that ignores the stop signal is killed with its own child (R11). The environment holds no variable outside the list (R10). The fake returns its outputs in order and then fails |
| 5 | The Claude driver | `provider/claude/*` | With a fake binary: the exact arguments (R9), the prompt on standard input, a recorded success output, an error output, an output with no object (R12), a missing binary as `not_installed`, a timeout, and `Health` (R13) |
| 6 | Configuration | `config/*` | The three blocks with their defaults, an unknown key with its line, and a `plan.provider` that names no provider |
| 7 | Writers for scenarios and scripts | `scenario/marshal.go`, `script/marshal.go` | `Parse(Marshal(x))` equals `x` for the fixtures and under a fuzz test, and the expectation hash is unchanged |
| 8 | The draft, its validation, and the attempts loop | `stage/plan/*`, `prompt/*` | One row per problem of R14 and R15. With the fake: a draft with a problem is corrected on the second attempt and accepted, and three bad drafts write nothing (R16). The schema allows every operator of `domain.Check` and no source field (R17). A golden file for `plan.md` |
| 9 | The commands | `cli/init.go`, `changes.go`, `plan.go`, `wire.go`, and `confirm --all` | In a fixture repository with the sample application and the fake provider: `plan --diff main..release --base-url <url>` writes the configuration, two scenarios, and their scripts. `run --all` is `inconclusive`, `confirm --all --yes` then `run --all` is `passed`, and with the seeded defect it is `failed`. `changes --diff` starts no provider (R20). A link for another repository stops the command (R8). The temporary copy is gone after a success, an error, and a cancel (R4) |

## Done when

In a fixture repository, the two commands of "What works when it is done" run with the fake provider and end with `passed` after a confirmation. With a real provider on a real repository, `semar plan --diff` writes scenarios that load, and `semar run` reaches the service with them.

## Review focus

1. **A repository whose text tells the planner what to do.** The planner has no tool that acts, and code validates all of its output (R14, R15, R18).
2. **A very large diff.** The diff is a file in the copy, not part of the prompt. The prompt holds the change table.
3. **A change with no behaviour:** a refactor, a configuration move, or a pipeline file. The plan says so, drafts checks of the endpoints that the changed code serves, and lists the rest as uncovered.
4. **A second run on the same diff.** Existing scenarios of a ticket are given to the planner, and a repeated title is a problem (R14).
5. **A cancel while the harness runs.** The process group stops, the copy is removed, and no file is written (R4, R11, R21).

## Open points

* Scripts are drafted from the source and were never sent. Plan 02 replaces the draft with a recording through Semar's tools.
* Ticket titles and acceptance criteria need the tracker port of Plan 03. Until then a ticket is its key and its commits, and no expectation can have the source `ticket`.
* A scenario that needs data of the test environment, such as the ID of an existing loan, is listed as not drafted. Data sets belong to Plan 11.
* An auth profile sends one session header. A service behind a gateway that reads several identity headers, one set per role, needs the auth profiles of Plan 04.
* A link to one pull request needs the code host's API, which belongs to Plan 08.
