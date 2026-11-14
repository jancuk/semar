# Core and Replay Implementation Plan

> **Status:** Implemented. This text describes the system as built.

> **For agentic workers:** Implement this plan task by task. The steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `semar run` replays a hand-written scenario and script against an HTTP API with no model, and produces an outcome, stored evidence, an activity log, and a report.

**Architecture:** A pure core (`check`, `verdict`, `script` validation, `activity`) takes values and returns values. One I/O path, the API engine, sends every request through an effects policy and a recorder that redacts before it stores. A replay runner connects them, and a sample application with seeded defects proves the whole chain in tests.

**Tech Stack:** Go 1.26 or newer, `spf13/cobra`, `go.yaml.in/yaml/v4`, `theory/jsonpath`, `bmatcuk/doublestar/v4`, `google/go-cmp`, `golang.org/x/sync`. Versions are in the [implementation guide](README.md#dependencies).

**Spec:** [PRD](../PRD.md), [TRD](../TRD.md). The TRD sections this plan implements: [Domain model](../TRD.md#domain-model), [Recording and replay](../TRD.md#recording-and-replay), [Recorder and redaction](../TRD.md#recorder-and-redaction), [Activity log](../TRD.md#activity-log), [API engine](../TRD.md#api-engine), [Verdict rules](../TRD.md#verdict-rules), [What a test may change](../TRD.md#what-a-test-may-change), and [Evidence store, report, and sign-off](../TRD.md#evidence-store-report-and-sign-off).

## Global Constraints

* Every rule in the [implementation guide](README.md#engineering-rules) applies: G1 to G19, and Q1 to Q14 where a task names one.
* Module path: `github.com/jancuk/semar`. The owner chose it on 2026-11-14.
* `go 1.26.0` and `toolchain go1.27.4` in `go.mod`. `CGO_ENABLED=0`.
* No model, no harness, and no network outside `127.0.0.1` in any test of this plan.
* Outcomes are exactly `passed`, `failed`, `blocked`, `inconclusive`, `not_run`, and `stale`.
* Sources are exactly `ticket`, `testcase`, `person`, `diff`, and `baseline`. `invariant` never appears in a file.
* Stored files carry `apiVersion: semar/v1`, or `"schema": 1` in JSON.
* Exit codes: 0 when every scenario passed, 1 when one failed, 2 when none failed and one is blocked, inconclusive, stale, or not run, or no scenario ran. 3 for a usage or internal error.
* Checks are evaluated on the redacted, stored action, never on the raw response. The same stored evidence must give the same outcome on any machine.
* Request headers on every call: `traceparent`, `baggage: semar.run=<run id>`, `User-Agent: Semar/<version>`, and `X-Semar-Run: <run id>`.
* Default limits: response body 1 MiB, request budget 200 per run, HTTP timeout 30 seconds, 4 workers.

## Review Focus

These five conditions are the ones most likely to hurt a person who uses this plan's output. Each has a test in the task that owns the code.

1. **A response that is not JSON, or that is cut at the size limit, under a JSONPath check.** The check is `not_evaluated` and the scenario is `inconclusive`. It never panics and never passes. Task 10.
2. **A secret that comes back URL-encoded or base64-encoded.** It is still removed from the stored action. Task 5.
3. **A misspelled key in a scenario or the configuration, such as `expects:` for `expect:`.** Loading fails with the file and the line. The scenario does not load with zero checks. Tasks 3 and 4.
4. **A run that is interrupted or stopped by an error.** The run directory has no `run.json` and loads as interrupted. A write that was in flight is not sent again. Tasks 8, 11, and 16.
5. **A URL that tries to leave the allowlist:** user info before the host, another port, upper case, a trailing dot, or a redirect. It is refused and no request reaches the other host. Tasks 6 and 8.

## File Structure

```text
go.mod, Makefile, .gitignore
cmd/semar/main.go                 calls cli.Run and exits with its code
internal/cli/                     cobra commands: root.go, run.go, script.go, activity.go, confirm.go, scenarios.go, exit.go
internal/domain/                  scenario.go, script.go, action.go, run.go, hash.go
internal/scenario/                parse.go, validate.go
internal/config/                  config.go, validate.go
internal/secret/                  secret.go
internal/yamlfile/                yamlfile.go: strict YAML decoding with the file and line, and JSONable
internal/hostrule/                hostrule.go: the one rule for a listed host, used by effects and config
internal/idcheck/                 idcheck.go: an ID that is safe as one path segment
internal/atomicfile/              atomicfile.go: write a whole file or none, and encode JSON for storage
internal/redact/                  redact.go, jsonbody.go, patterns.go, views.go
internal/effects/                 policy.go
internal/recorder/                recorder.go
internal/activity/                activity.go, ledger.go, timeline.go
internal/engine/api/              engine.go
internal/script/                  parse.go, validate.go, render.go, match.go
internal/check/                   eval.go, invariants.go
internal/verdict/                 verdict.go
internal/evidence/                store.go, run.go, verify.go
internal/confirm/                 confirm.go
internal/build/                   http.go
internal/sampleapp/               app.go
internal/replay/                  runner.go
internal/report/                  markdown.go, summary.go, escape.go, report.md.tmpl
testdata/e2e/                     .semar/semar.yaml, scenarios, scripts
```

`internal/cli` is added to the package map of the guide. It holds the commands so that they can be tested by calling `cli.Run`.

---

### Task 1: Module skeleton and the `version` command

**Files:**
- Create: `go.mod`, `Makefile`, `.gitignore`, `cmd/semar/main.go`, `internal/cli/root.go`, `internal/cli/exit.go`
- Test: `internal/cli/root_test.go`

**Interfaces:**
- Produces: `cli.Run(ctx context.Context, args []string, stdout, stderr io.Writer) int`, and the constants `cli.ExitOK = 0`, `cli.ExitFailed = 1`, `cli.ExitUndecided = 2`, `cli.ExitError = 3`. `cli.Version` is a package variable set with `-ldflags "-X github.com/jancuk/semar/internal/cli.Version=..."`. It is the one allowed package variable, and it is never changed at run time.

- [ ] **Step 1: Create the module**

```bash
go mod init github.com/jancuk/semar
go get github.com/spf13/cobra@v1.10.2 github.com/google/go-cmp@v0.7.0
```

Set `go 1.26.0` and `toolchain go1.27.4` in `go.mod`. The toolchain line is the newest stable release (G1).

- [ ] **Step 2: Write the failing test**

```go
func TestVersion(t *testing.T) {
	var out, errw bytes.Buffer
	code := cli.Run(context.Background(), []string{"version"}, &out, &errw)
	if code != cli.ExitOK { t.Fatalf("exit %d, stderr %q", code, errw.String()) }
	if !strings.HasPrefix(out.String(), "semar ") { t.Fatalf("got %q", out.String()) }
}

func TestUnknownCommandIsAUsageError(t *testing.T) {
	var out, errw bytes.Buffer
	if code := cli.Run(context.Background(), []string{"nope"}, &out, &errw); code != cli.ExitError {
		t.Fatalf("exit %d", code)
	}
}
```

- [ ] **Step 3: Run it and see it fail**

Run: `go test ./internal/cli/`
Expected: FAIL, `undefined: cli.Run`

- [ ] **Step 4: Implement `cli.Run` and `main`**

`cli.Run` builds a cobra root with `SilenceUsage` and `SilenceErrors`, sets its output writers and its context, and maps a cobra error to `ExitError`. `main` is `os.Exit(cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr))`, where `ctx` comes from `signal.NotifyContext` for `os.Interrupt` and `syscall.SIGTERM`.

- [ ] **Step 5: Add the Makefile and `.gitignore`**

`Makefile` targets: `build` (`CGO_ENABLED=0 go build -trimpath -ldflags "-X github.com/jancuk/semar/internal/cli.Version=$(VERSION)" -o bin/semar ./cmd/semar`), `test` (`go test -race ./...`), and `check` (`test -z "$$(gofmt -l .)"`, `go vet ./...`, `go run honnef.co/go/tools/cmd/staticcheck@latest ./...`, `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`, then `test`). The Makefile exports `GOTOOLCHAIN` from the toolchain line of `go.mod`, because a tool that `go run pkg@version` starts picks its own toolchain, which can be older than the standard library it must read. `check` runs `staticcheck` and `govulncheck` at pinned versions, not `@latest`, so that it gives the same result on every machine.

`.gitignore`: `bin/`, `.semar/runs/`, `.semar/store/`, `.semar/semar.local.yaml`.

- [ ] **Step 6: Run the checks**

Run: `make check && make build && ./bin/semar version`
Expected: PASS, and a line that starts with `semar `

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum Makefile .gitignore cmd internal/cli
git commit -m "feat: module skeleton and version command"
```

---

### Task 2: Domain types and the expectation hash

**Files:**
- Create: `internal/domain/scenario.go`, `internal/domain/script.go`, `internal/domain/action.go`, `internal/domain/run.go`, `internal/domain/hash.go`
- Test: `internal/domain/hash_test.go`

**Interfaces:**
- Produces, in `scenario.go`:

```go
type (
	ScenarioID string
	StepID     string
	RunID      string
	ActionID   string
	Hash       string
	Surface    string // "api" | "web" | "android"
	Source     string // "ticket" | "testcase" | "person" | "diff" | "baseline"
	Outcome    string // "passed" | "failed" | "blocked" | "inconclusive" | "not_run" | "stale"
)

type Scenario struct {
	APIVersion  string     `yaml:"apiVersion"`
	Kind        string     `yaml:"kind"`
	ID          ScenarioID `yaml:"id"`
	Title       string     `yaml:"title"`
	Ticket      string     `yaml:"ticket"`
	Intent      string     `yaml:"intent"`
	Surface     Surface    `yaml:"surface"`
	AuthProfile string     `yaml:"authProfile"`
	Steps       []Step     `yaml:"steps"`
}

type Step struct {
	ID     StepID        `yaml:"id"`
	Setup  bool          `yaml:"setup,omitempty"`
	Action string        `yaml:"action"`
	Hint   *RequestHint  `yaml:"hint,omitempty"`
	Expect []Expectation `yaml:"expect,omitempty"`
}

type RequestHint struct {
	Method string `yaml:"method"`
	Path   string `yaml:"path"` // a template: /v1/orders/{id}/refunds
}

type Expectation struct {
	Check  Check  `yaml:"check"`
	Source Source `yaml:"source"`
	Cite   string `yaml:"cite,omitempty"`
}

// Exactly one of Status, JSONPath, Header is set. JSONPath and Header take exactly one operator.
type Check struct {
	Status     *int     `yaml:"status,omitempty"`
	JSONPath   string   `yaml:"jsonpath,omitempty"`
	Header     string   `yaml:"header,omitempty"`
	Equals     any      `yaml:"equals,omitempty"`
	NotEquals  any      `yaml:"notEquals,omitempty"`
	Contains   any      `yaml:"contains,omitempty"`
	Exists     *bool    `yaml:"exists,omitempty"`
	Greater    *float64 `yaml:"greater,omitempty"`
	Less       *float64 `yaml:"less,omitempty"`
	Count      *int     `yaml:"count,omitempty"`
	EqualsPath string   `yaml:"equalsPath,omitempty"`
}
```

- Produces, in `script.go`:

```go
type Script struct {
	APIVersion string       `yaml:"apiVersion"`
	Kind       string       `yaml:"kind"`
	Scenario   ScenarioID   `yaml:"scenario"`
	ExpectHash Hash         `yaml:"expectHash"`
	AuthoredBy ProviderRef  `yaml:"authoredBy"`
	Steps      []ScriptStep `yaml:"steps"`
}

type ProviderRef struct{ Instance, Model, Harness string }

type ScriptStep struct {
	Step     StepID      `yaml:"step"`
	Request  *APIRequest `yaml:"request"`
	Captures []Capture   `yaml:"captures,omitempty"`
}

// Path, Query, Headers, and JSON may hold {{name}} variables.
type APIRequest struct {
	Service string            `yaml:"service"`
	Method  string            `yaml:"method"`
	Path    string            `yaml:"path"`
	Query   map[string]string `yaml:"query,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty"`
	JSON    any               `yaml:"json,omitempty"`
}

type Capture struct {
	Name     string `yaml:"name"`
	JSONPath string `yaml:"jsonpath"`
}
```

- Produces, in `action.go`:

```go
type (
	Pass  string // "setup" | "scenario" | "explore" | "security" | "cleanup"
	Actor string // "agent" | "pilot" | "replay"
	Verb  string // "visit" | "read" | "create" | "update" | "delete" | "sign_in" | "query"
)

type UseCase struct {
	Pass     Pass
	Scenario ScenarioID
	Step     StepID
}

type Action struct {
	Schema        int           `json:"schema"` // 1
	ID            ActionID      `json:"id"`
	Seq           int           `json:"seq"`
	Run           RunID         `json:"run"`
	Surface       Surface       `json:"surface"`
	Op            string        `json:"op"` // "request"
	Use           UseCase       `json:"use"`
	By            Actor         `json:"by"`
	Request       *HTTPRequest  `json:"request,omitempty"`
	Response      *HTTPResponse `json:"response,omitempty"`
	Captures      map[string]string `json:"captures,omitempty"`
	Refused       string        `json:"refused,omitempty"`       // why policy refused it. No request was sent
	Err           string        `json:"error,omitempty"`         // transport error
	EffectUnknown bool          `json:"effectUnknown,omitempty"` // a write that failed after it may have reached the server
	TraceID       string        `json:"traceId"`
	At            time.Time     `json:"at"`
	DurationMS    int64         `json:"durationMs"`
	Why           string        `json:"why,omitempty"`
}

type HTTPRequest struct {
	Method       string              `json:"method"`
	URL          string              `json:"url"`
	PathTemplate string              `json:"pathTemplate"`
	Headers      map[string][]string `json:"headers"`
	Body         Body                `json:"body,omitempty"`
}

type HTTPResponse struct {
	URL       string              `json:"url,omitempty"` // set only when redirects were followed to another URL
	Status    int                 `json:"status"`
	Headers   map[string][]string `json:"headers"`
	Body      Body                `json:"body,omitempty"`
	Truncated bool                `json:"truncated,omitempty"`
}

type Activity struct {
	Seq      int       `json:"seq"`
	At       time.Time `json:"at"`
	Actor    Actor     `json:"actor"`
	Use      UseCase   `json:"use"`
	Surface  Surface   `json:"surface"`
	Verb     Verb      `json:"verb"`
	Target   string    `json:"target"`
	Object   *ObjectRef `json:"object,omitempty"`
	Result   string    `json:"result"`
	Why      string    `json:"why,omitempty"`
	TraceID  string    `json:"traceId"`
	Evidence string    `json:"evidence"` // path of the action file, relative to the run directory
}

type ObjectRef struct{ Type, ID string }

// Body holds the bytes of a request or response body. It is stored in the most readable exact form:
// {"json": ...} when the bytes are valid JSON, {"text": "..."} when they are valid UTF-8,
// and {"base64": "..."} otherwise. Evidence must always be storable and readable by a person (NFR-8).
type Body []byte
```

- Produces, in `run.go`:

```go
type BuildRef struct{ Service, Commit, ConfigHash string }

type Reason struct {
	Rule   Outcome `json:"rule"`   // which rule matched
	Text   string  `json:"text"`   // plain English
	Step   StepID  `json:"step,omitempty"`
}

type CheckState string // "true" | "false" | "not_evaluated"

type CheckResult struct {
	Step     StepID     `json:"step"`
	Index    int        `json:"index"`     // position in Step.Expect. -1 for an invariant
	Source   string     `json:"source"`    // a Source, or "invariant"
	State    CheckState `json:"state"`
	Expected string     `json:"expected"`
	Observed string     `json:"observed"`
	Detail   string     `json:"detail,omitempty"`
	Confirmed bool      `json:"confirmed"`
}

type ScenarioResult struct {
	Scenario ScenarioID    `json:"scenario"`
	Outcome  Outcome       `json:"outcome"`
	Reasons  []Reason      `json:"reasons"`
	Checks   []CheckResult `json:"checks"`
	Actions  []ActionID    `json:"actions"`
}

type Run struct {
	Schema      int               `json:"schema"`
	ID          RunID             `json:"id"`
	Environment string            `json:"environment"`
	StartedAt   time.Time         `json:"startedAt"`
	EndedAt     time.Time         `json:"endedAt"`
	BuildsStart []BuildRef        `json:"buildsStart"`
	BuildsEnd   []BuildRef        `json:"buildsEnd"`
	Results     []ScenarioResult  `json:"results"`
	Files       map[string]string `json:"files"` // relative path to SHA-256
}
```

- Produces, in `hash.go`: `func ExpectHash(s Scenario) Hash`

- [ ] **Step 1: Write the failing test**

```go
func base() domain.Scenario { /* SHOP-482-01 from the TRD, three steps */ }

func TestExpectHash(t *testing.T) {
	h := domain.ExpectHash(base())
	if len(h) != 64 { t.Fatalf("want a SHA-256 hex string, got %q", h) }

	same := base(); same.Title = "another title"; same.Steps[0].Action = "reworded"
	if domain.ExpectHash(same) != h { t.Error("title and action text must not change the hash") }

	for name, change := range map[string]func(*domain.Scenario){
		"intent":      func(s *domain.Scenario) { s.Intent += " and more" },
		"authProfile": func(s *domain.Scenario) { s.AuthProfile = "customer" },
		"hint":        func(s *domain.Scenario) { s.Steps[1].Hint.Method = "PUT" },
		"value":       func(s *domain.Scenario) { v := 409; s.Steps[1].Expect[0].Check.Status = &v },
		"source":      func(s *domain.Scenario) { s.Steps[1].Expect[0].Source = "diff" },
		"setup":       func(s *domain.Scenario) { s.Steps[0].Setup = false },
		"step id":     func(s *domain.Scenario) { s.Steps[2].ID = "renamed" },
	} {
		s := base(); change(&s)
		if domain.ExpectHash(s) == h { t.Errorf("%s must change the hash", name) }
	}
}
```

- [ ] **Step 2: Run it and see it fail**

Run: `go test ./internal/domain/`
Expected: FAIL, `undefined: domain.ExpectHash`

- [ ] **Step 3: Implement the types and `ExpectHash`**

`ExpectHash` marshals a private struct with `encoding/json` and returns the hex SHA-256. The struct holds `Intent`, `AuthProfile`, and for each step its `ID`, `Setup`, `Hint`, and `Expect`. It leaves out `Title` and the step's `Action` text. `encoding/json` sorts map keys, so the result is stable.

- [ ] **Step 4: Run the test and see it pass**

Run: `go test ./internal/domain/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/domain
git commit -m "feat(domain): scenario, script, action, and run types with the expectation hash"
```

---

### Task 3: Scenario files

**Files:**
- Create: `internal/scenario/parse.go`, `internal/scenario/validate.go`
- Test: `internal/scenario/scenario_test.go`, `internal/scenario/testdata/*.yaml`

**Interfaces:**
- Consumes: `domain.Scenario` and its parts.
- Produces:

```go
type Origin int
const (
	FromFile  Origin = iota // written or edited by a person
	FromAgent               // submitted by an agent
)

type Problem struct{ Path, Message string } // Path such as "steps[1].expect[0].source"

func Parse(data []byte, name string) (domain.Scenario, error) // strict. An error names the file and the line
func Load(path string) (domain.Scenario, error)
func Validate(s domain.Scenario, origin Origin) []Problem
```

- [ ] **Step 1: Write the failing tests**

```go
func TestParseRejectsUnknownKey(t *testing.T) { // Review Focus 3
	_, err := scenario.Parse([]byte("apiVersion: semar/v1\nkind: Scenario\nid: A-1\nsteps:\n  - id: s\n    expects: []\n"), "a.yaml")
	if err == nil || !strings.Contains(err.Error(), "a.yaml:6") || !strings.Contains(err.Error(), "expects") {
		t.Fatalf("want an error that names a.yaml:6 and the key, got %v", err)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name   string
		change func(*domain.Scenario)
		origin scenario.Origin
		want   string // substring of one Problem.Message. Empty means no problem
	}{
		{"valid", func(*domain.Scenario) {}, scenario.FromFile, ""},
		{"wrong apiVersion", func(s *domain.Scenario) { s.APIVersion = "semar/v2" }, scenario.FromFile, "apiVersion must be semar/v1"},
		{"no ticket", func(s *domain.Scenario) { s.Ticket = "" }, scenario.FromFile, "ticket is required"},
		{"unknown surface", func(s *domain.Scenario) { s.Surface = "ios" }, scenario.FromFile, "surface must be api, web, or android"},
		{"duplicate step id", func(s *domain.Scenario) { s.Steps[1].ID = s.Steps[0].ID }, scenario.FromFile, "duplicate step id"},
		{"setup with an expected result", func(s *domain.Scenario) { s.Steps[0].Expect = s.Steps[1].Expect }, scenario.FromFile, "a setup step has no expected result"},
		{"two check kinds", func(s *domain.Scenario) { s.Steps[1].Expect[0].Check.JSONPath = "$.a" }, scenario.FromFile, "exactly one of status, jsonpath, header"},
		{"jsonpath with no operator", func(s *domain.Scenario) { s.Steps[2].Expect[0].Check.Equals = nil }, scenario.FromFile, "exactly one operator"},
		{"jsonpath that does not parse", func(s *domain.Scenario) { s.Steps[2].Expect[0].Check.JSONPath = "$..[" }, scenario.FromFile, "jsonpath"},
		{"invariant in a file", func(s *domain.Scenario) { s.Steps[1].Expect[0].Source = "invariant" }, scenario.FromFile, "invariants are fixed in Semar"},
		{"person from an agent", func(s *domain.Scenario) { s.Steps[1].Expect[0].Source = "person" }, scenario.FromAgent, "an agent cannot use the source person"},
		{"person from a file", func(s *domain.Scenario) { s.Steps[1].Expect[0].Source = "person" }, scenario.FromFile, ""},
		{"ticket with no cite", func(s *domain.Scenario) { s.Steps[1].Expect[0].Cite = "" }, scenario.FromFile, "cite is required for the source ticket"},
	}
	// each case: load testdata/valid.yaml, apply change, Validate, compare with want
}
```

`testdata/valid.yaml` is scenario `SHOP-482-01` from the TRD, [Scenario file](../TRD.md#scenario-file).

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/scenario/`
Expected: FAIL, `undefined: scenario.Parse`

- [ ] **Step 3: Implement `Parse`, `Load`, and `Validate`**

`Parse` uses a `yaml.Decoder` with `KnownFields(true)`. An ID matches `^[A-Za-z0-9][A-Za-z0-9._-]*$`. `Validate` returns every problem, not only the first. It compiles each JSONPath with `jsonpath.Parse`. `Parse` refuses a check operand (`equals`, `notEquals`, `contains`, `greater`, or `less`) whose YAML number text is not exactly equal to the decoded value, such as an integer that a 64-bit float rounds. The error names the file and the line. The user writes the number as a string or with fewer digits.

- [ ] **Step 4: Run the tests and see them pass**

Run: `go test ./internal/scenario/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/scenario
git commit -m "feat(scenario): strict parsing and validation of scenario files"
```

---

### Task 4: Configuration and secret references

**Files:**
- Create: `internal/config/config.go`, `internal/config/validate.go`, `internal/secret/secret.go`, `internal/yamlfile/yamlfile.go`
- Test: `internal/config/config_test.go`, `internal/secret/secret_test.go`, `internal/config/testdata/*.yaml`

**Interfaces:**
- Produces:

```go
// internal/config
type Config struct {
	Version      int                    `yaml:"version"`
	Environments map[string]Environment `yaml:"environments"`
	AuthProfiles map[string]AuthProfile `yaml:"authProfiles"`
	Redact       Redact                 `yaml:"redact"`
	Policy       Policy                 `yaml:"policy"`
	Replay       Replay                 `yaml:"replay"`
}
type Environment struct {
	Kind     string    `yaml:"kind"`
	Hosts    []string  `yaml:"hosts"`
	Methods  []string  `yaml:"methods"`
	Effects  Effects   `yaml:"effects"`
	Services []Service `yaml:"services"`
}
type Effects struct {
	Mode  string      `yaml:"mode"` // "test_data" | "read_only". No default
	Deny  []OpPattern `yaml:"deny"`
	Once  []OpPattern `yaml:"once"`
	Reads []OpPattern `yaml:"reads"`
}
type OpPattern struct{ Method, Path string }
type Service struct {
	Name    string      `yaml:"name"`
	BaseURL string      `yaml:"baseUrl"`
	Build   *BuildProbe `yaml:"build"`
}
type BuildProbe struct{ Probe, URL, Commit string } // Probe is "http". Commit is a JSONPath
type AuthProfile struct {
	Session *SessionSpec      `yaml:"session"`
	Secrets map[string]string `yaml:"secrets"` // name to reference, such as "env:SEMAR_UAT_TOKEN"
}
type SessionSpec struct{ Header, Value string } // Value may hold {{secret.NAME}}
type Redact struct {
	JSONPaths []string `yaml:"jsonPaths"`
	Headers   []string `yaml:"headers"`
	Patterns  []string `yaml:"patterns"`
}
type Policy struct{ Trust Trust `yaml:"trust"` }            // company policy. Later plans add to it
type Trust struct{ Confirm []domain.Source `yaml:"confirm"` } // default: diff, baseline
type Replay struct{ Workers int `yaml:"workers"` }             // default: 4

func Load(repoRoot string) (Config, error) // reads <repoRoot>/.semar/semar.yaml
func (c Config) Environment(name string) (string, Environment, error) // empty name: the only environment, or an error that lists them

// internal/secret
type Resolver interface {
	Resolve(ctx context.Context, ref string) (string, error)
	Loaded() []string // every value resolved so far, for the redactor
}
func NewEnv(lookup func(string) (string, bool)) *Env // handles "env:NAME". Any other scheme: ErrUnsupported
var ErrUnsupported = errors.New("secret scheme is not supported yet")
// Expand replaces each {{secret.NAME}} in text with values[NAME]. A placeholder that is not closed, that names
// no secret, or that is still in the text after expansion is an error. An error names the placeholder, never a value.
func Expand(text string, values map[string]string) (string, error)
```

- [ ] **Step 1: Write the failing tests**

```go
func TestLoad(t *testing.T) {
	cases := []struct{ file, want string }{
		{"valid.yaml", ""},
		{"unknown-key.yaml", "semar.yaml:4"},                        // "enviroments:". Review Focus 3
		{"kind-prod.yaml", "kind must be test"},
		{"no-effects-mode.yaml", "effects.mode is required: test_data or read_only"},
		{"service-off-list.yaml", "baseUrl host api.other.example.com is not in hosts"},
		{"later-plan-keys.yaml", ""},                                // providers:, observe:, web: are accepted and ignored
	}
	// each case: copy the file to <tmp>/.semar/semar.yaml, call config.Load(tmp)
}

func TestEnvResolver(t *testing.T) {
	r := secret.NewEnv(func(k string) (string, bool) { v, ok := map[string]string{"TOKEN": "s3cret-value"}[k]; return v, ok })
	if v, err := r.Resolve(ctx, "env:TOKEN"); err != nil || v != "s3cret-value" { t.Fatal(v, err) }
	if _, err := r.Resolve(ctx, "env:MISSING"); err == nil { t.Fatal("want an error for a missing variable") }
	if _, err := r.Resolve(ctx, "keychain:semar/x"); !errors.Is(err, secret.ErrUnsupported) { t.Fatal(err) }
	if got := r.Loaded(); !slices.Equal(got, []string{"s3cret-value"}) { t.Fatal(got) }
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/config/ ./internal/secret/`
Expected: FAIL, `undefined: config.Load`

- [ ] **Step 3: Implement `config.Load` and `secret.Env`**

`Load` first decodes the file to `map[string]yaml.Node`. A top-level key must be one of the `Config` fields or one of the keys that later plans own: `observe`, `web`, `android`, `access`, `databases`, `repos`, `risk`, `tickets`, `knowledge`, `mcpServers`, `mcpTools`, `publish`, `plan`, `regression`, `security`, `explore`, `notify`, `decisions`, `providers`, `agents`, `assign`, `passes`, `limits`, `evidence`, `change`. Any other key is an error with the line. `internal/yamlfile` holds the strict decoding and its file-and-line errors, and `scenario` and `script` use it too. `Load` then decodes each known node with `KnownFields(true)`, applies the two defaults, and validates. `kind` must be `test`, and every other value is refused (PRD non-goal: no production). A host entry is checked by `internal/hostrule`, the one rule that `effects` also uses to match a host.

- [ ] **Step 4: Run the tests and see them pass**

Run: `go test ./internal/config/ ./internal/secret/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config internal/secret
git commit -m "feat(config): strict configuration loading and env secret references"
```

---

### Task 5: Redaction

**Files:**
- Create: `internal/redact/redact.go`
- Test: `internal/redact/redact_test.go`, `internal/redact/fuzz_test.go`

**Interfaces:**
- Consumes: `config.Redact`
- Produces:

```go
type Redactor struct{ /* unexported */ }

// key is at least 16 bytes. An invalid JSONPath or pattern is an error.
func New(cfg config.Redact, secrets func() []string, key []byte) (*Redactor, error)
func (r *Redactor) Action(a domain.Action) domain.Action // returns a redacted copy
func (r *Redactor) Marker(value string) string           // "[redacted:" + first 8 hex of HMAC-SHA256(key, value) + "]"
func IsMarker(s string) bool                             // the whole string is exactly one marker
func HasMarker(s string) bool                            // the string holds a marker anywhere
```

**Decisions made during implementation.** The marker is keyed, so that a low-entropy value such as an OTP cannot be recovered from its marker by trying candidates. Only the span of text that encodes a secret is replaced, and the rest of a string is never rewritten. A secret is also found in percent-decoded views of a string. The ledger of this plan records the rulings.

**Decision this task records.** The TRD said that a redacted value "keeps its type". This plan replaces every redacted value with the marker string, whatever its type, because a typed zero could satisfy a check by accident. The TRD sentence is updated to match.

- [ ] **Step 1: Write the failing tests**

```go
func TestAction(t *testing.T) {
	secrets := func() []string { return []string{"s3cret-value", "abc"} } // "abc" is under 6 characters and is not searched
	key := bytes.Repeat([]byte{7}, 32)
	r, _ := redact.New(config.Redact{JSONPaths: []string{"$.data.national_id"}, Headers: []string{"X-Api-Key"}}, secrets, key)
	in := domain.Action{
		Request: &domain.HTTPRequest{
			URL:     "https://api.uat.example.com/v1/x?token=s3cret-value",
			Headers: map[string][]string{"Authorization": {"Bearer s3cret-value"}, "X-Api-Key": {"k"}, "Accept": {"application/json"}},
		},
		Response: &domain.HTTPResponse{
			Headers: map[string][]string{"Set-Cookie": {"sid=1"}, "Location": {"/next?t=s3cret%2Dvalue"}},
			Body:    []byte(`{"data":{"national_id":"3201","note":"abc","jwt":"eyJhbGciOi.eyJzdWIiOi.c2ln","b64":"czNjcmV0LXZhbHVl"}}`),
		},
	}
	out := r.Action(in)
	// The four fixed headers and the configured header hold only markers
	// Accept is unchanged
	// $.data.national_id is redact.Marker("3201")
	// $.data.note is still "abc"
	// $.data.jwt is a marker: the JWT pattern
	// $.data.b64 is a marker: the base64 form of a loaded secret. Review Focus 2
	// Neither "s3cret-value" nor "s3cret%2Dvalue" appears anywhere in json.Marshal(out)
	// in is unchanged: Action returns a copy
}

func FuzzNoSecretSurvives(f *testing.F) { // G16
	f.Add("prefix", "suffix", "hunter2-token")
	f.Fuzz(func(t *testing.T, a, b, secretValue string) {
		if len(secretValue) < 6 || strings.ContainsAny(secretValue, "\x00") { t.Skip() }
		r, _ := redact.New(config.Redact{}, func() []string { return []string{secretValue} }, bytes.Repeat([]byte{7}, 32))
		out := r.Action(domain.Action{Response: &domain.HTTPResponse{Body: []byte(a + secretValue + b)}})
		if bytes.Contains(out.Response.Body, []byte(secretValue)) { t.Fatalf("secret survived in %q", out.Response.Body) }
	})
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/redact/`
Expected: FAIL, `undefined: redact.New`

- [ ] **Step 3: Implement the redactor**

Order of work on a copy of the action:

1. Replace the values of `Authorization`, `Cookie`, `Set-Cookie`, `Proxy-Authorization`, and each configured header.
2. When a body parses as JSON, replace the value at each configured JSONPath with `Marker(original)`, and walk every string value and every object key through step 3. Run each configured pattern on the text of every JSON number, and replace a number that matches with a marker string.
3. In every string (a URL, a header value, a JSON string, a body that is not JSON, and also `Action.Why` and `HTTPRequest.PathTemplate`), look for each loaded secret of 6 characters or more as it is, in standard base64, in URL-safe base64, and in decoded views of the text. Decoding is lenient: each valid `%XX` decodes and a bad `%` stays. A second view reads `+` as a space. A secret found in a view is mapped back to its span in the source text, and only that span is replaced. Then replace matches of the built-in patterns and of each configured pattern. The built-in patterns are a JSON Web Token, a bearer token of at least 16 characters, and a Basic authorization value.

- [ ] **Step 4: Run the tests and a short fuzz run**

Run: `go test ./internal/redact/ && go test ./internal/redact/ -run xxx -fuzz FuzzNoSecretSurvives -fuzztime 20s`
Expected: PASS, and no crasher

- [ ] **Step 5: Update the TRD sentence and commit**

In `docs/TRD.md`, section "Recorder and redaction", replace "A redacted value keeps its type and a short stable hash, so two exchanges can still be compared." with "A redacted value is replaced by a marker that carries a short keyed hash, so two exchanges from one machine can still be compared, and a low-entropy value such as an OTP cannot be recovered from its marker. A check on a redacted value is not evaluated."

```bash
git add internal/redact docs/TRD.md
git commit -m "feat(redact): remove secrets from actions before storage"
```

---

### Task 6: The effects policy

**Files:**
- Create: `internal/effects/policy.go`
- Test: `internal/effects/policy_test.go`

**Interfaces:**
- Consumes: `config.Environment`
- Produces:

```go
type Policy struct{ /* unexported */ }
type Class int
const ( Read Class = iota; Write )

type Request struct {
	Method string
	URL    *url.URL
}

type Refusal struct{ Kind RefusalKind; Detail string }
type RefusalKind string // "host_not_allowed" | "method_not_allowed" | "denied" | "read_only"
func (r *Refusal) Error() string

func New(env config.Environment) (*Policy, error) // an invalid pattern is an error
func (p *Policy) Classify(r Request) Class
func (p *Policy) Admit(r Request) error // nil, or a *Refusal
func (p *Policy) Once(r Request) bool
```

- [ ] **Step 1: Write the failing test**

The environment for every row: hosts `api.uat.example.com`, methods `GET, POST, DELETE`, deny `POST /v1/payouts/**`, reads `POST /v1/search`, once `POST /v1/vouchers/*/redeem`.

```go
func TestAdmit(t *testing.T) {
	cases := []struct{ mode, method, url, want string }{
		{"test_data", "GET", "https://api.uat.example.com/v1/orders/1", ""},
		{"test_data", "POST", "https://api.uat.example.com/v1/orders", ""},
		{"test_data", "POST", "https://api.uat.example.com/v1/payouts/7/send", "denied"},
		{"test_data", "PUT", "https://api.uat.example.com/v1/orders/1", "method_not_allowed"},
		{"read_only", "POST", "https://api.uat.example.com/v1/orders", "read_only"},
		{"read_only", "POST", "https://api.uat.example.com/v1/search", ""},
		{"read_only", "GET", "https://api.uat.example.com/v1/orders/1", ""},
		// Review Focus 5
		{"test_data", "GET", "https://api.uat.example.com@evil.example/v1/orders", "host_not_allowed"},
		{"test_data", "GET", "https://API.UAT.EXAMPLE.COM/v1/orders/1", ""},
		{"test_data", "GET", "https://api.uat.example.com./v1/orders/1", ""},
		{"test_data", "GET", "https://api.uat.example.com:8443/v1/orders/1", "host_not_allowed"},
		{"test_data", "GET", "https://api.uat.example.com.evil.example/v1", "host_not_allowed"},
		{"test_data", "GET", "http://127.0.0.1/v1", "host_not_allowed"},
	}
	// each case: build the policy with the mode, call Admit, compare Refusal.Kind with want
}
```

Also assert `Classify` gives `Read` for `GET`, `HEAD`, and `POST /v1/search`, and `Write` for every other method, and that `Once` is true only for `POST /v1/vouchers/9/redeem`.

- [ ] **Step 2: Run it and see it fail**

Run: `go test ./internal/effects/`
Expected: FAIL, `undefined: effects.New`

- [ ] **Step 3: Implement the policy**

Match hosts with `hostrule.Allowed`: it compares `URL.Hostname()`, lower-cased in ASCII only, with one trailing dot removed, and it refuses a host that holds a byte above 0x7F. A host entry with no port allows only the scheme's default port. An entry with a port allows only that port. Match a pattern on the method and, with `doublestar.Match`, on `URL.Path`. A deny or once pattern matches a normalised path: lower-cased, with one trailing slash removed (tried with and without it), and with a `;...` parameter removed from each segment. A reads pattern, which relaxes a write into a read, matches the path exactly as written. Each kind errs toward refusing. Check in this order: host, method, deny, then the mode.

- [ ] **Step 4: Run the test and see it pass**

Run: `go test ./internal/effects/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/effects
git commit -m "feat(effects): admit or refuse a request by host, method, deny list, and mode"
```

---

### Task 7: The recorder and the activity log

**Files:**
- Create: `internal/recorder/recorder.go`, `internal/activity/activity.go`, `internal/activity/ledger.go`, `internal/activity/timeline.go`
- Test: `internal/recorder/recorder_test.go`, `internal/activity/activity_test.go`, `internal/activity/testdata/timeline.golden`

**Interfaces:**
- Consumes: `redact.Redactor`, `effects.Policy` (for `Classify`), the domain types.
- Produces:

```go
// internal/recorder
type Sink interface {
	WriteAction(a domain.Action) (path string, err error) // path is relative to the run directory
	AppendActivity(e domain.Activity) error
}
type Recorder struct{ /* unexported, holds a mutex */ }
func New(sink Sink, red *redact.Redactor, classify func(method string, u *url.URL) effects.Class, run domain.RunID) *Recorder
// Record sets Seq and ID, redacts, stores the action, appends its activity entry, and returns the redacted action.
func (r *Recorder) Record(a domain.Action) (domain.Action, error)

// internal/activity
func FromAction(a domain.Action, class effects.Class, evidencePath string) domain.Activity // pure
type LedgerRow struct {
	Verb      domain.Verb
	Object    domain.ObjectRef
	Use       domain.UseCase
	CleanedUp bool
}
func Ledger(entries []domain.Activity) []LedgerRow // pure
type Filter struct {
	WritesOnly bool
	Scenario   domain.ScenarioID
	Pass       domain.Pass
}
func Timeline(w io.Writer, entries []domain.Activity, f Filter) error
```

- [ ] **Step 1: Write the failing tests**

```go
func TestFromAction(t *testing.T) {
	cases := []struct {
		method, template, url string
		status                int
		captures              map[string]string
		class                 effects.Class
		wantVerb              domain.Verb
		wantTarget            string
		wantObject            *domain.ObjectRef
		wantResult            string
	}{
		{"POST", "/v1/orders", ".../v1/orders", 201, map[string]string{"order_id": "81234"}, effects.Write, "create", "POST /v1/orders", &domain.ObjectRef{Type: "order", ID: "81234"}, "201"},
		{"GET", "/v1/orders/{id}", ".../v1/orders/81234", 200, nil, effects.Read, "read", "GET /v1/orders/81234", &domain.ObjectRef{Type: "order", ID: "81234"}, "200"},
		{"POST", "/v1/orders/{id}/refunds", ".../v1/orders/81234/refunds", 422, nil, effects.Write, "create", "POST /v1/orders/81234/refunds", &domain.ObjectRef{Type: "refund"}, "422"},
		{"PATCH", "/v1/orders/{id}", ".../v1/orders/81234", 200, nil, effects.Write, "update", "PATCH /v1/orders/81234", &domain.ObjectRef{Type: "order", ID: "81234"}, "200"},
		{"DELETE", "/v1/addresses/{id}", ".../v1/addresses/5512", 204, nil, effects.Write, "delete", "DELETE /v1/addresses/5512", &domain.ObjectRef{Type: "address", ID: "5512"}, "204"},
		{"POST", "/v1/search", ".../v1/search", 200, nil, effects.Read, "read", "POST /v1/search", nil, "200"},
	}
	// A refused action gives Result "refused: <reason>". A transport error gives Result "error: <text>".
}

func TestLedger(t *testing.T) {
	// create order 1, create order 2, update order 2, delete order 1, read order 2
	// want three rows in order: created order 1 (CleanedUp true), created order 2 (false), updated order 2.
	// Reads never appear. A create whose status is 400 or above never appears.
}

func TestRecorderIsSafeInParallel(t *testing.T) {
	// 50 goroutines call Record. Run with -race.
	// Seq values are exactly 1..50 with no gap. Every stored action is the redacted one.
}
```

`TestTimeline` compares the output for six entries with `testdata/timeline.golden`, and asserts that `WritesOnly` keeps only `create`, `update`, and `delete`.

- [ ] **Step 2: Run them and see them fail**

Run: `go test -race ./internal/recorder/ ./internal/activity/`
Expected: FAIL, `undefined: activity.FromAction`

- [ ] **Step 3: Implement the recorder and the activity functions**

The verb comes from the class and the method: a `Read` is `read`, and for a `Write`, `POST` is `create`, `PUT` and `PATCH` are `update`, and `DELETE` is `delete`. The object type is the last literal segment of the path template, made singular: `ies` becomes `y`, `sses` becomes `ss`, a word that ends in `ss` stays, and any other final `s` is removed. A read has an object only when a path parameter follows that segment, because a list or a search touches no single object. For a write, the object ID is the value of the path parameter that follows that segment when there is one, otherwise the first capture in name order, otherwise empty. A create is cleaned up when a later `delete` with a status under 400 has the same object type and ID. The target is the method and the path of the real URL.

- [ ] **Step 4: Run the tests and see them pass**

Run: `go test -race ./internal/recorder/ ./internal/activity/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/recorder internal/activity
git commit -m "feat(recorder): record redacted actions with an activity log and a data ledger"
```

---

### Task 8: The API engine

**Files:**
- Create: `internal/engine/api/engine.go`
- Test: `internal/engine/api/engine_test.go`

**Interfaces:**
- Consumes: `effects.Policy`, `recorder.Recorder`, `domain.Action`, `domain.UseCase`
- Produces:

```go
type Options struct {
	Policy   *effects.Policy
	Recorder *recorder.Recorder
	Services map[string]string // service name to base URL
	Session  func(ctx context.Context, authProfile string) (header, value string, err error)
	Run      domain.RunID
	Version  string
	Timeout  time.Duration // default 30s
	MaxBody  int64         // default 1 MiB
	Budget   int           // default 200 requests per engine
	Clock    func() time.Time
	TraceID  func() string // 32 lower-case hex characters
	SpanID   func() string // 16 lower-case hex characters
	HTTP     http.RoundTripper // default: a transport that never reuses a connection
	Logger   *slog.Logger      // default: discards
}

type Call struct {
	Use          domain.UseCase
	By           domain.Actor
	Service      string
	Method       string
	Path         string // already rendered
	PathTemplate string
	Query        map[string]string
	Headers      map[string]string
	JSON         []byte
	AuthProfile  string
	Why          string
	Captures     []domain.Capture // JSONPaths to read from the raw response
}

type Result struct {
	Action   domain.Action     // redacted, as stored
	Captured map[string]string // the raw value of each capture, for later requests only. Never store them
	Missing  []string          // the names of the captures that found no value
}

func New(o Options) *Engine
// Do never returns an error for a refusal, a timeout, or a failed request. Those are in Result.Action.
// It returns an error only when the action could not be recorded or ctx was cancelled.
func (e *Engine) Do(ctx context.Context, c Call) (Result, error)
```

- [ ] **Step 1: Write the failing tests**

Each test starts an `httptest.Server` and builds an engine whose policy allows that server's host.

```go
func TestHeaders(t *testing.T) {
	// The server sees: traceparent "00-<32 hex>-<16 hex>-01", baggage "semar.run=r-1",
	// User-Agent "Semar/test", X-Semar-Run "r-1", and Authorization "Bearer tok".
	// The stored action has Authorization as a marker, and TraceID equal to the 32 hex characters.
}

func TestRefusalSendsNothing(t *testing.T) {
	// A POST under a read_only policy: Result.Action.Refused contains "read_only",
	// Response is nil, and the server's hit counter is 0. The action is recorded.
}

func TestWriteNeedsAUseCase(t *testing.T) {
	// A POST with a zero UseCase: Refused contains "a write must belong to a scenario step, an exploration, or a probe". Hits: 0.
	// A GET with a zero UseCase is sent.
}

func TestRedirectOffTheListIsNotFollowed(t *testing.T) { // Review Focus 5
	// Server A answers 302 to server B, which is not on the list.
	// B's hit counter is 0. Refused contains "redirect to a host that is not allowed". Response.Status is 302.
}

func TestRedirectedWriteIsNotFollowed(t *testing.T) {
	// A POST that gets a 302 to a listed host: the 3xx response is kept, Refused is "redirect refused: a write is not followed",
	// and the target's hit counter is 0. A GET redirected to a listed host is followed, and Response.URL is the final URL.
}

func TestSessionValueIsNotStored(t *testing.T) {
	// The server sees the session header. The stored action holds "[session of <auth profile>]" in its place.
	// A redirect to another host and port carries no session header.
}

func TestWriteTimeoutIsNotSentAgain(t *testing.T) { // Review Focus 4
	// The server sleeps past Timeout on POST. Result.Action.EffectUnknown is true, Err is set,
	// and the server received exactly 1 request.
}

func TestLargeBodyIsCut(t *testing.T) {
	// MaxBody 16, body of 100 bytes: Response.Truncated is true and len(Response.Body) is 16.
}

func TestBudget(t *testing.T) {
	// Budget 2: the third call has Refused "request budget reached" and the server saw 2 requests.
}

func TestCancelledContext(t *testing.T) {
	// Do with a cancelled ctx returns an error that wraps context.Canceled and sends nothing.
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `go test -race ./internal/engine/api/`
Expected: FAIL, `undefined: api.New`

- [ ] **Step 3: Implement the engine**

Build the URL from the service's base URL and the path. Ask the policy before sending, and again in `CheckRedirect` for each redirect target. On a refused redirect, return `http.ErrUseLastResponse` and set `Refused`. Use one `http.Client` with the timeout and no retry of any kind. Read at most `MaxBody + 1` bytes to detect a cut. Every path, including a refusal, ends in exactly one `Recorder.Record` call.

* **Redirects.** A redirect is never followed when the redirected request is a write. The 3xx response is kept, and the action is refused with "redirect refused: a write is not followed". A redirected read is followed after the policy admits the target. A redirect to another host or port loses the session header, and the stored response carries the final URL.
* **Uncertain writes.** A write whose transport failed after a connection was obtained has `EffectUnknown` set, whatever the error: a timeout, a reset, or an end of file. A failure before any connection, such as a refusal or a DNS error, does not set it.
* **Session.** The engine sends the session value and never stores it. The stored action holds `[session of <auth profile>]` in its place, because a value from a sign-in response is not a loaded secret and the redactor would not know it.
* **Captures.** The engine evaluates each `Call.Captures` JSONPath on the raw response and puts the names and values into `Action.Captures` before it records, so that redaction applies and the activity entry can name the created object. It returns the raw values in `Result.Captured` and the names that found no value in `Result.Missing`. The runner takes variables from these.
* **Error text.** No error of the engine holds a raw request URL, a rendered value, or a captured value. An error names the method and the path hint of the step.
* **Headers.** A `Content-Type` that the caller sets wins over the engine's default.

- [ ] **Step 4: Run the tests and see them pass**

Run: `go test -race ./internal/engine/api/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/engine
git commit -m "feat(api): HTTP engine behind the effects policy and the recorder"
```

---

### Task 9: Script files and their binding to a scenario

**Files:**
- Create: `internal/script/parse.go`, `internal/script/validate.go`, `internal/script/render.go`, `internal/script/match.go`
- Test: `internal/script/script_test.go`, `internal/script/fuzz_test.go`, `internal/script/testdata/*.yaml`

**Interfaces:**
- Consumes: `domain.Script`, `domain.Scenario`, `domain.ExpectHash`
- Produces:

```go
type Problem struct{ Step domain.StepID; Message string }

func Parse(data []byte, name string) (domain.Script, error) // strict
func Load(path string) (domain.Script, error)
func Validate(sc domain.Scenario, s domain.Script) []Problem
func MatchPath(hint, actual string) bool
// Render replaces {{name}} in the path, the query, the headers, and string values of the JSON body.
func Render(req domain.APIRequest, vars map[string]string) (domain.APIRequest, error) // an unknown variable is an error
```

`testdata/valid.yaml`:

```yaml
apiVersion: semar/v1
kind: Script
scenario: SHOP-482-01
expectHash: "<the hash of testdata scenario SHOP-482-01>"
authoredBy: { instance: hand, model: none, harness: none }
steps:
  - step: create-order
    request: { service: orders, method: POST, path: /v1/orders, json: { amount: 100000 } }
    captures: [{ name: order_id, jsonpath: "$.data.id" }]
  - step: refund-too-much
    request: { service: orders, method: POST, path: "/v1/orders/{{order_id}}/refunds", json: { amount: 150000 } }
  - step: order-unchanged
    request: { service: orders, method: GET, path: "/v1/orders/{{order_id}}" }
```

- [ ] **Step 1: Write the failing tests**

```go
func TestValidate(t *testing.T) {
	cases := []struct {
		name   string
		change func(*domain.Script)
		want   string // substring of one Problem.Message. Empty means no problem
	}{
		{"valid", func(*domain.Script) {}, ""},
		{"other hash", func(s *domain.Script) { s.ExpectHash = "0000" }, "the script was written for other expected results"},
		{"step with no request", func(s *domain.Script) { s.Steps = s.Steps[:2] }, "step order-unchanged has an expected result and no request"},
		{"two requests for one step", func(s *domain.Script) { s.Steps = append(s.Steps, s.Steps[1]) }, "step refund-too-much is bound more than once"},
		{"unknown step", func(s *domain.Script) { s.Steps[2].Step = "nope" }, "step nope is not in the scenario"},
		{"other method", func(s *domain.Script) { s.Steps[1].Request.Method = "PUT" }, "the request is PUT and the step expects POST"},
		{"other path", func(s *domain.Script) { s.Steps[1].Request.Path = "/v1/orders/{{order_id}}/cancel" }, "does not match /v1/orders/{id}/refunds"},
		{"out of order", func(s *domain.Script) { s.Steps[1], s.Steps[2] = s.Steps[2], s.Steps[1] }, "steps are not in the scenario's order"},
		{"variable never captured", func(s *domain.Script) { s.Steps[0].Captures = nil }, "order_id is used before any step captures it"},
		{"readback of another object", func(s *domain.Script) { s.Steps[2].Request.Path = "/v1/orders/999" }, "does not use a value from an earlier step"},
	}
}

func TestMatchPath(t *testing.T) {
	yes := [][2]string{{"/v1/orders", "/v1/orders"}, {"/v1/orders/{id}", "/v1/orders/{{order_id}}"}, {"/v1/orders/{id}/refunds", "/v1/orders/81234/refunds"}, {"/v1/orders/{id}", "/v1/orders/81234/"}}
	no := [][2]string{{"/v1/orders/{id}", "/v1/orders"}, {"/v1/orders/{id}", "/v1/orders/1/refunds"}, {"/v1/orders", "/v1/Orders"}, {"/v1/orders/{id}", "/v1/orders/../admin"}}
}

func FuzzMatchPath(f *testing.F) { // G16: never panics, and a hint always matches itself
}
```

The last `Validate` case applies only to a hint with a path parameter: when the hint has `{id}` and an earlier step of the scenario has a capture, the request's path must hold a `{{variable}}` in that position.

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/script/`
Expected: FAIL, `undefined: script.Validate`

- [ ] **Step 3: Implement the four functions**

`MatchPath` splits both paths on `/`, ignores one trailing slash, and refuses a `.` or `..` segment. A hint segment in `{}` matches any one segment. Other segments must be equal, case included. `Validate` returns every problem. It rejects an empty `expectHash` on either side, so that no empty hash matches another. It checks the path after variables are rendered: a raw backslash, an encoded slash or backslash, and `%25` in a rendered path are refused, and so are `.` or `..` segments also after decoding. A capture needs a request. A malformed variable, and a control character in a header, are reported. `Parse` shares its YAML decoding with `scenario` through `yamlfile`, and `yamlfile.JSONable` turns a decoded YAML value into one that `encoding/json` can store.

- [ ] **Step 4: Run the tests and a short fuzz run**

Run: `go test ./internal/script/ && go test ./internal/script/ -run xxx -fuzz FuzzMatchPath -fuzztime 20s`
Expected: PASS, and no crasher

- [ ] **Step 5: Commit**

```bash
git add internal/script
git commit -m "feat(script): script files and validation of their binding to a scenario"
```

---

### Task 10: Checks and invariants

**Files:**
- Create: `internal/check/eval.go`, `internal/check/invariants.go`
- Test: `internal/check/eval_test.go`

**Interfaces:**
- Consumes: `domain.Check`, `domain.Action`, `redact.IsMarker`
- Produces:

```go
type Result struct {
	State    domain.CheckState // "true" | "false" | "not_evaluated"
	Expected string
	Observed string
	Detail   string
}

func Eval(c domain.Check, a domain.Action) Result // pure
// Invariants returns the fixed checks for one step. In this plan: no 5xx response,
// unless an expected result of the step asks for that status.
func Invariants(step domain.Step, a domain.Action) []Result // pure
```

- [ ] **Step 1: Write the failing test**

```go
func TestEval(t *testing.T) {
	body := `{"data":{"id":"o-1","refunded_total":0,"total":100000,"items":[{"q":1},{"q":2}],"note":"paid in full"},"error":null}`
	cases := []struct {
		name  string
		check string // YAML of a domain.Check
		act   domain.Action
		want  domain.CheckState
		detail string
	}{
		{"status equal", "status: 200", ok(200, body), "true", ""},
		{"status differs", "status: 422", ok(200, body), "false", ""},
		{"equals a number, 0 and 0.0", `{jsonpath: "$.data.refunded_total", equals: 0.0}`, ok(200, body), "true", ""},
		{"equals a string", `{jsonpath: "$.data.id", equals: o-1}`, ok(200, body), "true", ""},
		{"notEquals", `{jsonpath: "$.data.id", notEquals: o-2}`, ok(200, body), "true", ""},
		{"contains in a string", `{jsonpath: "$.data.note", contains: paid}`, ok(200, body), "true", ""},
		{"exists true", `{jsonpath: "$.data.id", exists: true}`, ok(200, body), "true", ""},
		{"exists false on a missing path", `{jsonpath: "$.data.nope", exists: false}`, ok(200, body), "true", ""},
		{"equals on a missing path", `{jsonpath: "$.data.nope", equals: 1}`, ok(200, body), "false", "no value at $.data.nope"},
		{"greater", `{jsonpath: "$.data.total", greater: 99999}`, ok(200, body), "true", ""},
		{"less", `{jsonpath: "$.data.total", less: 100000}`, ok(200, body), "false", ""},
		{"count", `{jsonpath: "$.data.items[*]", count: 2}`, ok(200, body), "true", ""},
		{"equalsPath", `{jsonpath: "$.data.total", equalsPath: "$.data.refunded_total"}`, ok(200, body), "false", ""},
		{"header", `{header: Content-Type, equals: application/json}`, ok(200, body), "true", ""},
		// Review Focus 1
		{"body is not JSON", `{jsonpath: "$.a", equals: 1}`, ok(200, "<html>"), "not_evaluated", "the response body is not JSON"},
		{"body was cut", `{jsonpath: "$.a", equals: 1}`, cut(200, `{"a":`), "not_evaluated", "the response body was cut at the size limit"},
		{"value is redacted", `{jsonpath: "$.t", equals: x}`, ok(200, `{"t":"[redacted:ab12cd34]"}`), "not_evaluated", "the value is redacted"},
		{"redacted inside a string", `{jsonpath: "$.t", contains: x}`, ok(200, `{"t":"id [redacted:ab12cd34] ok"}`), "not_evaluated", "the value is redacted"},
		{"integers above 2^53 differ", `{jsonpath: "$.n", equals: 9007199254740993}`, ok(200, `{"n":9007199254740992}`), "false", ""},
		{"request was refused", "status: 200", refused("read_only"), "not_evaluated", "no response was recorded"},
		{"request failed", "status: 200", failed("connection reset"), "not_evaluated", "no response was recorded"},
	}
}

func TestInvariants(t *testing.T) {
	// A 500 with no expected 5xx: one Result, State "false", Expected "no server error".
	// A 500 on a step that expects status 500: State "true".
	// A 422: State "true". A refused action: State "not_evaluated".
}
```

- [ ] **Step 2: Run it and see it fail**

Run: `go test ./internal/check/`
Expected: FAIL, `undefined: check.Eval`

- [ ] **Step 3: Implement `Eval` and `Invariants`**

Decode the body with `UseNumber`, and compare numbers as exact decimals with `math/big`, so that `0` equals `0.0` and two different integers above 2^53 are never equal. No `float64` rounding decides a check. Compare other values by decoded JSON value. A string value that holds a redaction marker anywhere, not only as the whole string, is `not_evaluated` for `equals`, `notEquals`, `contains`, `equalsPath`, `greater`, and `less`, because a comparison on redacted text can be wrong in both directions. `exists` and `count` are not affected. For a header with several values, `notEquals` is `not_evaluated` when one value holds a marker. `equals: null` cannot be written, because a null operand reads as no operator. Use `exists`. `Observed` is a short text form of the selected value, at most 200 characters. A JSONPath that selects more than one value is `false` for every operator except `count`. `Describe` gives the expected value of a check in the same words as `Eval`, for example "status 422", and `semar confirm` uses it.

- [ ] **Step 4: Run the test and see it pass**

Run: `go test ./internal/check/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/check
git commit -m "feat(check): evaluate status, JSONPath, and header checks with three states"
```

---

### Task 11: Evidence store, confirmations, and build probes

**Files:**
- Create: `internal/evidence/store.go`, `internal/evidence/run.go`, `internal/evidence/verify.go`, `internal/confirm/confirm.go`, `internal/build/http.go`
- Test: `internal/evidence/evidence_test.go`, `internal/confirm/confirm_test.go`, `internal/build/http_test.go`

**Interfaces:**
- Consumes: the domain types, `config.Service`
- Produces:

```go
// internal/evidence. root is <repository>/.semar
type Store struct{ /* unexported */ }
type Run struct{ /* unexported */ } // implements recorder.Sink

var ErrInterrupted = errors.New("the run was interrupted: it has no run.json")

func Open(root string) (*Store, error)
func (s *Store) NewRun(id domain.RunID) (*Run, error) // an existing directory is an error
func (r *Run) WriteAction(a domain.Action) (string, error)  // exchanges/<scenario>/<seq, 4 digits>-<step>.json. No step: <seq>.json. No scenario: exchanges/_run/<seq>.json
func (r *Run) AppendActivity(e domain.Activity) error       // activity.jsonl
func (r *Run) WriteFile(rel string, data []byte) error
func (r *Run) Close(run domain.Run) error                   // writes run.json with the SHA-256 of every file
func (s *Store) LoadRun(id domain.RunID) (domain.Run, []domain.Activity, error)
func (s *Store) Activity(id domain.RunID) ([]domain.Activity, error) // activity.jsonl of a run, finished or not
func (s *Store) RedactKey() ([]byte, error) // the 32 bytes that key the redaction marker. Created on first use
func (s *Store) Runs() ([]domain.RunID, error) // newest first
func (s *Store) Verify(id domain.RunID) (changed []string, err error)
func (s *Store) PutScript(sc domain.Script) error
func (s *Store) Script(id domain.ScenarioID, h domain.Hash) (domain.Script, bool, error)

// internal/confirm
type Record struct {
	Schema     int               `json:"schema"`
	Scenario   domain.ScenarioID `json:"scenario"`
	ExpectHash domain.Hash       `json:"expectHash"`
	User       string            `json:"user"`
	Identity   string            `json:"identity"` // "local, unverified identity" in this plan. FR-11
	At         time.Time         `json:"at"`
	Sources    []domain.Source   `json:"sources"` // the sources of the expectations the person saw. Sorted, no duplicates
}
type Store interface {
	Put(r Record) error
	Get(id domain.ScenarioID, h domain.Hash) (Record, bool, error)
}
func NewLocal(root string) *Local                              // <root>/store/confirmations/<scenario>/<hash>/<user>.json
func Needs(src domain.Source, trust []domain.Source) bool      // person never needs one

// internal/build
type Prober interface {
	Probe(ctx context.Context, service string) (domain.BuildRef, error)
}
func NewHTTP(services []config.Service, client *http.Client) *HTTP
```

Layout under `.semar`:

```text
runs/<run-id>/run.json, report.md, activity.jsonl
runs/<run-id>/exchanges/<scenario-id>/0003-refund-too-much.json
runs/<run-id>/exchanges/_run/0004.json            an action with no scenario
runs/<run-id>/scripts/<scenario-id>.yaml
store/scripts/<scenario-id>/<expect-hash>.yaml
store/confirmations/<scenario-id>/<expect-hash>/<user>.json
store/redact.key
```

- [ ] **Step 1: Write the failing tests**

```go
func TestRunRoundTrip(t *testing.T) {
	// NewRun, three WriteAction, three AppendActivity, WriteFile("report.md"), Close.
	// LoadRun returns the same Run and three activity entries in Seq order.
	// run.json lists every file except itself, each with a 64-character hash.
	// Every directory has mode 0700 and every file 0600. G19
}

func TestInterruptedRun(t *testing.T) { // Review Focus 4
	// NewRun and one WriteAction, then no Close.
	// LoadRun returns an error for which errors.Is(err, evidence.ErrInterrupted) is true.
}

func TestVerifyFindsAChangedFile(t *testing.T) {
	// After Close, append one byte to an exchange file. Verify returns that one path.
}

func TestNoHalfWrittenFiles(t *testing.T) {
	// WriteFile to a read-only directory fails, and the directory holds no temporary file afterwards. G11
}

func TestScriptStoreIsKeyedByHash(t *testing.T) {
	// PutScript with hash A. Script(id, A) finds it. Script(id, B) reports not found.
}

func TestOpenRefusesALinkedOrNonDirectoryStore(t *testing.T) {
	// .semar, .semar/runs, and .semar/store each as a symbolic link, and as a file: Open returns an error.
	// Nothing is created or changed behind a link.
}

func TestRedactKeyRefusesAnUnsafeKeyFile(t *testing.T) {
	// A key that is a link, is not a regular file, has a mode other than 0600, or does not hold 32 bytes is an error.
	// A missing key is created with mode 0600, and a second call returns the same key.
}

func TestAnActionWithNoScenarioIsStored(t *testing.T) {
	// An action with an empty use case is stored under exchanges/_run/. An action with a scenario and no step is stored as <seq>.json.
}

func TestNeeds(t *testing.T) {
	trust := []domain.Source{"diff", "baseline"}
	// diff: true. baseline: true. ticket: false. testcase: false. person: false.
	// With trust {"diff","baseline","ticket"}: ticket true. person is false for every trust list.
}

func TestConfirmationStoresSources(t *testing.T) {
	// A record put with sources {"baseline","diff"} is read back with the same sources.
	// A record with no sources confirms nothing.
}

func TestHTTPProbe(t *testing.T) {
	// A server that answers {"commit":"9f3c2ab"} with Commit "$.commit" gives BuildRef{Service: "orders", Commit: "9f3c2ab"}.
	// A 500, a body that is not JSON, and a missing path each give an error that names the service.
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/evidence/ ./internal/confirm/ ./internal/build/`
Expected: FAIL, `undefined: evidence.Open`

- [ ] **Step 3: Implement the three packages**

Write each file with `atomicfile.Write`: a temporary name in its own directory, `Sync`, then rename. `atomicfile.EncodeJSON` does not escape `<`, `>`, and `&`, so that a stored body is readable (NFR-8). Check every relative path with `filepath.IsLocal`, and every scenario ID, step ID, and run ID with `idcheck.Check`: letters, digits, dot, underscore, and hyphen, starting with a letter or digit. So an ID is never a path, hidden, or reserved by a leading underscore, which keeps `_run` apart from every scenario. `activity.jsonl` is opened once with `O_APPEND` and guarded by a mutex. A user name in a confirmation path is cleaned to `[A-Za-z0-9._-]`.

* **Every action is storable.** An action with no scenario (a read with an empty use case, a refused write, or a later explore action) goes to `exchanges/_run/<seq>.json`. An action with a scenario and no step goes to `exchanges/<scenario>/<seq>.json`. The store never refuses an action because its use case is empty: the engine records every path, and a store that refuses would turn a recorded refusal into a program error.
* **`Verify` and `Close`.** `Verify` hashes the files and parses none of them. `Close` can be called again after it failed.
* **Links.** `Open` refuses a `.semar`, `.semar/runs`, or `.semar/store` that is a symbolic link or is not a directory, before it creates a directory or changes a mode. Evidence must not be written outside the repository.
* **The redaction key.** `RedactKey` makes 32 random bytes at `store/redact.key` with mode 0600 on first use. It refuses a key file that is a link, is not a regular file, or has another mode, because a key that others can read is not secret, and a key from git is known to everyone. The run then stops with exit code 3. Deleting the file makes a new key.
* **Confirmation sources.** A confirmation record stores the sources of the expectations that the person saw. A record with no sources confirms nothing. A later change of `policy.trust` must not count results that nobody saw.

- [ ] **Step 4: Run the tests and see them pass**

Run: `go test -race ./internal/evidence/ ./internal/confirm/ ./internal/build/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/evidence internal/confirm internal/build
git commit -m "feat(evidence): local run store with hashes, confirmations, and build probes"
```

---

### Task 12: The verdict engine

**Files:**
- Create: `internal/verdict/verdict.go`
- Test: `internal/verdict/verdict_test.go`

**Interfaces:**
- Consumes: `domain.Scenario`, `domain.Action`, `check.Result`
- Produces:

```go
type StepEvidence struct {
	Action     *domain.Action // nil when the step did not run
	Checks     []check.Result // one per Step.Expect, in order
	Invariants []check.Result
}

type Input struct {
	Scenario    domain.Scenario
	Steps       map[domain.StepID]StepEvidence
	Confirmed   func(e domain.Expectation) bool // true when a person confirmed it, or its source needs no confirmation
	BuildsStart []domain.BuildRef
	BuildsEnd   []domain.BuildRef
	NotRun      string   // not empty: why the scenario was not run
	Blocked     []string // reasons found before the scenario ran
	Retried     bool
}

func Compute(in Input) domain.ScenarioResult // pure. G9
```

- [ ] **Step 1: Write the failing test**

One row per rule of the TRD, [Verdict rules](../TRD.md#verdict-rules), and rows for the order of the rules.

```go
func TestCompute(t *testing.T) {
	cases := []struct {
		name        string
		in          func() verdict.Input
		want        domain.Outcome
		wantReasons []string // substrings, in order. Every matching rule is listed, not only the first
	}{
		{"all true and confirmed", pass, "passed", nil},
		{"not run", with(pass, notRun("the surface is not supported")), "not_run", []string{"the surface is not supported"}},
		{"blocked before the run", with(pass, blocked("sign-in failed")), "blocked", []string{"sign-in failed"}},
		{"setup step answered 500", with(pass, setupStatus(500)), "blocked", []string{"setup failed: create-order answered 500"}},
		{"setup step was refused", with(pass, setupRefused("read_only")), "blocked", []string{"the environment does not allow a step"}},
		{"step refused by policy", with(pass, stepRefused("refund-too-much", "denied")), "blocked", []string{"the environment does not allow a step"}},
		{"build changed", with(pass, buildsEnd("orders", "aaaaaaa")), "stale", []string{"the build changed during the run"}},
		{"confirmed check is false", with(pass, checkState("refund-too-much", 0, "false")), "failed", []string{"refund-too-much"}},
		{"invariant is false", with(pass, invariant("refund-too-much", "false")), "failed", []string{"no server error"}},
		{"unconfirmed and true", with(pass, unconfirmed("refund-too-much", 1)), "inconclusive", []string{"an expected result is not confirmed"}},
		{"unconfirmed and false", with(pass, unconfirmed("refund-too-much", 1), checkState("refund-too-much", 1, "false")), "inconclusive", []string{"not confirmed and not met"}},
		{"check not evaluated", with(pass, checkState("order-unchanged", 0, "not_evaluated")), "inconclusive", []string{"a check could not be evaluated"}},
		{"step did not run", with(pass, noAction("order-unchanged")), "inconclusive", []string{"step order-unchanged has no recorded result"}},
		{"write timed out", with(pass, effectUnknown("refund-too-much")), "inconclusive", []string{"a write timed out and its effect is unknown"}},
		{"needed a retry", with(pass, retried), "inconclusive", []string{"the script needed a retry"}},
		{"no check at all", noChecks, "inconclusive", []string{"the scenario has no check besides the invariants"}},
		{"step failed in transport", with(pass, transportError("refund-too-much", "connection reset")), "inconclusive", []string{"step refund-too-much failed: connection reset"}},
		{"step with no expectation never ran", with(pass, noAction("tidy-up")), "inconclusive", []string{"step tidy-up has no recorded result"}},
		{"action from another pass", with(pass, inPass("refund-too-much", "explore")), "inconclusive", []string{"the recorded evidence does not match the scenario"}},
		{"more results than expectations", with(pass, extraCheck("refund-too-much")), "inconclusive", []string{"the recorded evidence does not match the scenario"}},
		{"invariant false on a setup step", with(pass, invariant("create-order", "false")), "blocked", []string{"setup failed: create-order"}},
		{"no build at the end", with(pass, noBuildsEnd), "inconclusive", []string{"no build was recorded"}},
		// order of the rules
		{"stale with a failed check lists both", with(pass, buildsEnd("orders", "aaaaaaa"), checkState("refund-too-much", 0, "false")), "stale", []string{"the build changed during the run", "refund-too-much"}},
		{"blocked wins over failed", with(pass, blocked("gate failed"), checkState("refund-too-much", 0, "false")), "blocked", []string{"gate failed", "refund-too-much"}},
		{"failed wins over inconclusive", with(pass, checkState("refund-too-much", 0, "false"), unconfirmed("order-unchanged", 0)), "failed", []string{"refund-too-much", "an expected result is not confirmed"}},
	}
}

func TestComputeIsPure(t *testing.T) {
	// Call Compute twice with the same Input. The results are equal with cmp.Diff.
	// The Input is unchanged afterwards.
}
```

- [ ] **Step 2: Run it and see it fail**

Run: `go test ./internal/verdict/`
Expected: FAIL, `undefined: verdict.Compute`

- [ ] **Step 3: Implement `Compute`**

Evaluate all six rules, collect a `domain.Reason` for each one that matches with its `Rule` set to that rule's outcome, and return the outcome of the first rule that matched in the order `not_run`, `blocked`, `stale`, `failed`, `inconclusive`, `passed`. `Checks` in the result holds every check and every invariant with its state and `Confirmed`, whatever the outcome. A setup step fails when its action is missing, was refused, has an error, has no response, or answered 400 or above, and when an invariant is false on it. These rules also hold:

* Every non-setup step needs a recorded response for the scenario to pass, whether or not it has expectations. A missing action gives the inconclusive reason "step <id> has no recorded result". An action with a transport error or no response gives "step <id> failed: <error>".
* Only expectations on non-setup steps count as "a check besides the invariants".
* An action counts as evidence for a step only when its scenario and step IDs are the step's, and its `Use.Pass` is `setup` for a setup step and `scenario` for any other step. An explore action with the right IDs must not prove a scenario. Evidence that does not match gives the inconclusive reason "the recorded evidence does not match the scenario". Mismatched evidence is an action of another step or pass, more check results than expectations, a step ID that the scenario lacks, a duplicated step ID, and a scenario with an empty ID.
* A step that ran has invariant results. Without them the result is inconclusive.
* When no build was recorded at the start or at the end, the result is inconclusive: "no build was recorded". Builds that were recorded and differ give `stale`.

- [ ] **Step 4: Run the test and see it pass**

Run: `go test ./internal/verdict/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/verdict
git commit -m "feat(verdict): compute one outcome with every reason from recorded evidence"
```

---

### Task 13: The sample application

**Files:**
- Create: `internal/sampleapp/app.go`
- Test: `internal/sampleapp/app_test.go`

**Interfaces:**
- Produces:

```go
type Defect string
const (
	RefundOverTotal   Defect = "refund-over-total"   // a refund above the total is accepted with 201
	RefundServerError Defect = "refund-server-error" // every refund answers 500
	SlowRefund        Defect = "slow-refund"         // an accepted refund is applied, then the answer comes after Delay. A refused refund is not delayed
	BuildChanges      Defect = "build-changes"       // /version gives another commit from its second call on
	RedirectAway      Defect = "redirect-away"       // GET of an order answers 302 to RedirectTo
	OtherUserReads    Defect = "other-user-reads"    // an order can be read by a user who does not own it. Used in Plan 05
)

type Options struct {
	Defects    map[Defect]bool
	Commit     string            // default "9f3c2ab"
	Tokens     map[string]string // token to user. Default {"tok-alice": "alice", "tok-bob": "bob"}
	Delay      time.Duration
	RedirectTo string
}

type App struct{ /* unexported: orders, a mutex, counters */ }
func New(o Options) *App
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request)
func (a *App) Requests(method, pathPrefix string) int // how many requests it received
```

Routes. Every route except `/version` needs `Authorization: Bearer <token>` and answers 401 without it.

| Route | Answer |
| --- | --- |
| `GET /version` | 200 `{"commit":"9f3c2ab"}` |
| `POST /v1/orders` with `{"amount":100000}` | 201 `{"data":{"id":"o-1","amount":100000,"refunded_total":0,"owner":"alice"}}`. IDs count up from `o-1` |
| `GET /v1/orders/{id}` | 200 with the order. 404 for an unknown ID. 403 for another user's order |
| `POST /v1/orders/{id}/refunds` with `{"amount":150000}` | 201 `{"data":{"id":"r-1","amount":...}}`, and `refunded_total` grows. 422 `{"error":{"code":"REFUND_EXCEEDS_TOTAL"}}` when the amount is above `amount - refunded_total` |

- [ ] **Step 1: Write the failing test**

```go
func TestRoutes(t *testing.T) {
	// No token: 401. Create: 201 and o-1. Refund of 150000 on 100000: 422 and the code.
	// Read: refunded_total is 0. Bob reads Alice's order: 403.
}

func TestDefects(t *testing.T) {
	// RefundOverTotal: the same refund answers 201 and refunded_total is 150000.
	// RefundServerError: 500.
	// BuildChanges: the first /version gives 9f3c2ab and the second another commit.
	// SlowRefund with Delay 200ms: the refund is applied before the answer arrives.
	// RedirectAway: GET of an order answers 302 with Location RedirectTo.
	// OtherUserReads: Bob reads Alice's order with 200.
}
```

- [ ] **Step 2: Run it and see it fail**

Run: `go test ./internal/sampleapp/`
Expected: FAIL, `undefined: sampleapp.New`

- [ ] **Step 3: Implement the application**

Use `http.ServeMux` with method and wildcard patterns. Guard the state with one mutex.

- [ ] **Step 4: Run the test and see it pass**

Run: `go test -race ./internal/sampleapp/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/sampleapp
git commit -m "test(sampleapp): HTTP service with a switch for each seeded defect"
```

---

### Task 14: The replay runner

**Files:**
- Create: `internal/replay/runner.go`
- Test: `internal/replay/runner_test.go`

**Interfaces:**
- Consumes: `api.Engine` (`Do`), `script.Validate`, `script.Render`, `check.Eval`, `check.Invariants`, `verdict.Compute`, `build.Prober`, `confirm.Store`, `confirm.Needs`, `sampleapp.App`
- Produces:

```go
type Doer interface {
	Do(ctx context.Context, c api.Call) (api.Result, error)
}
type Scripts interface {
	Script(id domain.ScenarioID, h domain.Hash) (domain.Script, bool, error)
}

type Deps struct {
	Engine   Doer
	Scripts  Scripts
	Builds   build.Prober
	Services []string // names to probe
	Confirm  confirm.Store
	Trust    []domain.Source
	Workers  int // default 4
}

type Runner struct{ /* unexported */ }
func New(d Deps) *Runner
// Run replays each scenario and returns one result per scenario, in input order.
// The builds are probed once before the first scenario and once after the last.
func (r *Runner) Run(ctx context.Context, scenarios []domain.Scenario) (results []domain.ScenarioResult, start, end []domain.BuildRef, err error)
```

- [ ] **Step 1: Write the failing tests**

Each test starts `sampleapp.New` behind `httptest.NewServer`, with a real engine, policy, recorder, and a temporary evidence store. `confirmAll` puts a confirmation record for the scenario.

```go
func TestReplay(t *testing.T) {
	cases := []struct {
		name    string
		defects []sampleapp.Defect
		mode    string // effects.mode
		confirm bool
		want    domain.Outcome
		reason  string
		check   func(t *testing.T, app *sampleapp.App)
	}{
		{"correct service", nil, "test_data", true, "passed", "", nil},
		{"refund above the total is accepted", []sampleapp.Defect{sampleapp.RefundOverTotal}, "test_data", true, "failed", "refund-too-much", nil},
		{"refund answers 500", []sampleapp.Defect{sampleapp.RefundServerError}, "test_data", true, "failed", "no server error", nil},
		{"nobody confirmed the diff result", nil, "test_data", false, "inconclusive", "an expected result is not confirmed", nil},
		{"build changed during the run", []sampleapp.Defect{sampleapp.BuildChanges}, "test_data", true, "stale", "the build changed during the run", nil},
		{"read-only environment", nil, "read_only", true, "blocked", "the environment does not allow a step",
			func(t *testing.T, app *sampleapp.App) { if n := app.Requests("POST", "/v1/"); n != 0 { t.Fatalf("%d writes reached the service", n) } }},
		{"write times out", []sampleapp.Defect{sampleapp.SlowRefund, sampleapp.RefundOverTotal}, "test_data", true, "inconclusive", "a write timed out and its effect is unknown",
			func(t *testing.T, app *sampleapp.App) { if n := app.Requests("POST", "/v1/orders/o-1/refunds"); n != 1 { t.Fatalf("the refund was sent %d times", n) } }}, // Review Focus 4
	}
}

func TestMissingScriptIsBlocked(t *testing.T) {
	// No script in the store: "blocked", reason "no script was accepted". The service received no request.
}

func TestScriptThatDoesNotFitIsBlocked(t *testing.T) {
	// A script whose second step uses PUT: "blocked", and the reason holds the Problem text. No request was sent.
}

func TestProbeFailureIsBlocked(t *testing.T) {
	// The build probe's address refuses the connection: every scenario is "blocked",
	// reason "the build of orders could not be read". Run returns a nil error, and no test request was sent.
}

func TestStepWithNoRequestStopsTheScenario(t *testing.T) {
	// A script that leaves out a step, or binds it with no request: the scenario is inconclusive,
	// and no later step is sent, in particular no later write.
}

func TestNoReasonHoldsARawValue(t *testing.T) {
	// A request URL, a rendered value, and a captured value with a marker string never appear in a reason or in the error of Run.
}

func TestScenariosDoNotShareData(t *testing.T) { // Q2
	// Two copies of the scenario with different IDs, Workers 2, run with -race.
	// Both pass, and the two readback steps read different order IDs.
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `go test -race ./internal/replay/`
Expected: FAIL, `undefined: replay.New`

- [ ] **Step 3: Implement the runner**

For one scenario:

1. Find the script by `domain.ExpectHash`. Without one, the result is blocked.
2. Run `script.Validate`. With a problem, the result is blocked, and nothing is sent.
3. For each scenario step in order: render the request with the variables so far, and call `Engine.Do` with `UseCase{Pass: "setup" or "scenario", Scenario, Step}`, `By: "replay"`, and the step's captures in `Call.Captures`. Take variables from `Result.Captured`. Stop at the first step that the script leaves out or binds with no request, that was refused, that failed or has no response, or whose capture is in `Result.Missing`. Later steps then have no action, so no write is sent for a step that never ran. A response with any status lets the scenario go on.
4. Evaluate `check.Eval` and `check.Invariants` on the redacted `Result.Action` of each step.
5. Call `verdict.Compute`. `Confirmed` is true for an expectation when `confirm.Needs` is false, or when the store has a record for this scenario and hash whose sources include the expectation's source.

No reason of the runner holds a raw request URL, a rendered value, or a captured value. A reason for a failed send is fixed text with the step ID, for example "step refund-too-much could not be sent or recorded". The cause of a failed build probe is also kept out of the reason, because it can quote the response body.

Probe the builds before the first scenario. When a probe fails, return every scenario as blocked with "the build of <service> could not be read" and send nothing. Probe again after the last scenario. When a build cannot be read after the run, the end builds are dropped, and each scenario gets the inconclusive reason "the build of <service> could not be read after the run".

Run scenarios in an `errgroup` with `SetLimit(Workers)`. Variables belong to one scenario and are never shared. A cancelled context stops the group. `Run` returns the results that are complete, and its error is the context's error. A scenario that sent nothing is `not_run` with "the run was interrupted", and one that was cut short is inconclusive with the same text. An error of `Engine.Do` that is not a cancellation (the action could not be recorded) stops that scenario, and `Run` returns it, joined with any context error.

- [ ] **Step 4: Run the tests and see them pass**

Run: `go test -race ./internal/replay/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/replay
git commit -m "feat(replay): replay scripts with no model and compute outcomes"
```

---

### Task 15: The report

**Files:**
- Create: `internal/report/markdown.go`, `internal/report/summary.go`
- Test: `internal/report/report_test.go`, `internal/report/testdata/report.golden.md`, `internal/report/testdata/summary.golden.txt`

**Interfaces:**
- Consumes: `domain.Run`, `domain.Scenario`, `domain.Activity`, `activity.Ledger`, `activity.Timeline`
- Produces:

```go
type Data struct {
	Run       domain.Run
	Scenarios map[domain.ScenarioID]domain.Scenario
	Activity  []domain.Activity
}
func Markdown(w io.Writer, d Data) error
func Summary(w io.Writer, run domain.Run) error
func ExitCode(results []domain.ScenarioResult) int // 0, 1, or 2. No scenario: 2
```

- [ ] **Step 1: Write the failing tests**

```go
func TestMarkdownGolden(t *testing.T) {
	// A run with one failed, one inconclusive, one blocked, and two passed scenarios.
	// Compare with testdata/report.golden.md. Rewrite the file only with -update. G15
}

func TestMarkdownOrder(t *testing.T) {
	// The failed scenario comes first, then blocked, then inconclusive, then stale, then not_run.
	// The two passes are inside one <details> block after them. FR-29
	// Each failed check shows: the expected value, its source, its cite when it has one, and the observed value.
	// Each scenario that is not passed shows the line "Reproduce: semar run --scenario <id>". FR-28
	// The report holds a "Data touched on UAT" section built from activity.Ledger.
	// The report holds no request body and no response body. Bodies stay in the exchange files.
	// A hostile value (a link, markup, math, a bidirectional control) is neutralised, and a long value is cut.
}

func TestExitCode(t *testing.T) {
	cases := []struct{ outcomes []domain.Outcome; want int }{
		{[]domain.Outcome{"passed", "passed"}, 0},
		{[]domain.Outcome{"passed", "failed", "inconclusive"}, 1},
		{[]domain.Outcome{"passed", "inconclusive"}, 2},
		{[]domain.Outcome{"blocked"}, 2},
		{[]domain.Outcome{"stale"}, 2},
		{[]domain.Outcome{"not_run"}, 2},
		{nil, 2},
	}
}
```

`Summary` prints one line per scenario with its outcome and its first reason, `SHOP-482-01  failed  step refund-too-much: expected status 422, observed status 201 (source ticket)`, and a last line with the count of each outcome. `TestSummaryGolden` compares it with `testdata/summary.golden.txt`.

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/report/`
Expected: FAIL, `undefined: report.Markdown`

- [ ] **Step 3: Implement the report**

Use `text/template` with the template in an embedded file. Write times in UTC as RFC 3339. The cite of a failed check comes from the scenario's expectation. A run whose `EndedAt` is zero starts with "This run did not finish".

A reason, a cite, a title, and an observed value come from a scenario file or a service, so the report treats them as untrusted. `escape.go` cuts every long value, and it neutralises links, images, HTML, math, and bidirectional and other control characters, so that a service cannot change how the report reads or inject a sequence into a terminal.

- [ ] **Step 4: Run the tests and see them pass**

Run: `go test ./internal/report/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/report
git commit -m "feat(report): markdown report, terminal summary, and exit codes"
```

---

### Task 16: The `run`, `script add`, `confirm`, and `activity` commands

**Files:**
- Create: `internal/cli/run.go`, `internal/cli/script.go`, `internal/cli/activity.go`, `internal/cli/confirm.go`, `testdata/e2e/.semar/semar.yaml`, `testdata/e2e/.semar/scenarios/SHOP-482/SHOP-482-01.yaml`, `testdata/e2e/scripts/SHOP-482-01.yaml`
- Modify: `internal/cli/root.go` (register the four commands)
- Test: `internal/cli/e2e_test.go`

**Interfaces:**
- Consumes: every package of this plan.
- Produces these commands:

| Command | Behaviour |
| --- | --- |
| `semar run --scenario <id>... [--env <name>] [--dir <repository root>]` | Loads each scenario from `.semar/scenarios/**/<id>.yaml`, replays it, writes the run directory with `report.md`, prints the summary and the report path, and exits with `report.ExitCode` |
| `semar run --all` | The same for every scenario file |
| `semar script add <file>` | Validates a script against its scenario and stores it. A script with no `expectHash` is bound to the current hash of its scenario. This is how a hand-written script enters the store before Plan 02 exists |
| `semar confirm <scenario id> --yes` | Shows each expected result that needs confirmation, with its source, its cite, and the full expected value (`check.Describe`, escaped), and stores one local record with the sources it confirmed. It refuses an empty user name. Without `--yes` it asks on the terminal |
| `semar activity <run id> [--writes] [--scenario <id>] [--pass <pass>]` | Prints the timeline. `latest` is accepted as the run ID. For a run with no `run.json` it says "the run did not finish (it was interrupted or stopped by an error)" and prints the activity it has, which no hash covers |

The session of an auth profile comes from its `session` block. `{{secret.NAME}}` is resolved with `secret.Env` and expanded with `secret.Expand`. The loaded values go to the redactor, and so does the expanded session value, whether or not it held a placeholder. Every secret is resolved before the run directory is made, so a missing secret sends nothing and leaves no run.

- [ ] **Step 1: Write the failing end-to-end tests**

Each test copies `testdata/e2e` to `t.TempDir()`, starts the sample application, writes its address into the copied `semar.yaml`, sets the token with `t.Setenv("SEMAR_SAMPLE_TOKEN", "tok-alice")`, and calls `cli.Run`.

```go
func TestRunEndToEnd(t *testing.T) {
	// 1. "script add scripts/SHOP-482-01.yaml": exit 0.
	// 2. "run --scenario SHOP-482-01": exit 2. The summary says "inconclusive" and "not confirmed".
	// 3. "confirm SHOP-482-01 --yes": exit 0.
	// 4. "run --scenario SHOP-482-01": exit 0. The summary says "passed".
	// 5. The newest run directory holds run.json, report.md, activity.jsonl, and three exchange files.
	// 6. grep of the whole run directory for "tok-alice" finds nothing.
	// 7. "activity latest --writes" prints two lines: the create of the order and the refund attempt.
}

func TestRunFindsTheSeededDefect(t *testing.T) {
	// With sampleapp.RefundOverTotal and a confirmation: exit 1.
	// report.md names step refund-too-much, "expected status 422", "observed status 201", the source "ticket", and its cite.
}

func TestMissingSecretSendsNothing(t *testing.T) {
	// Without SEMAR_SAMPLE_TOKEN: exit 3, stderr names the reference env:SEMAR_SAMPLE_TOKEN and no value,
	// the service received no request, and no run directory exists.
}

func TestRunWithNoScenario(t *testing.T) {
	// "run --scenario NOPE-1": exit 3, and stderr names the ID and the directory that was searched.
	// "run --all" in a repository with no scenario file: exit 2, "no scenario ran".
}

func TestInterruptedRunIsVisible(t *testing.T) { // Review Focus 4
	// Run with sampleapp.RefundOverTotal and sampleapp.SlowRefund and Delay 2s, and cancel the context after 300ms.
	// SlowRefund delays only a refund that the service accepts, so RefundOverTotal is needed too.
	// cli.Run returns 2. "activity latest" reports "the run did not finish".
	// The service received the refund request exactly once.
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `go test -race ./internal/cli/`
Expected: FAIL, unknown command "run"

- [ ] **Step 3: Implement the commands**

Each command builds its dependencies in one function, in this order: configuration, secrets, redactor, policy, evidence store, recorder, engine, runner. The redactor's key comes from `evidence.Store.RedactKey()`, which is why the redactor is built after the store. A run ID is `r-<UTC date and time>-<4 random hex characters>`, made by an injected function (G8). After the runner returns, write `report.md`, close the run, print the summary, and return the exit code.

* **Cancellation.** On a cancelled context, skip `Close`, so that the run loads as interrupted, and exit with 2.
* **Any other error from `Run`.** The run reports no outcome, because its evidence may be incomplete. Skip `Close` and exit with 3.
* **A failed `Close`.** Rewrite `report.md` as a run that did not finish.
* **Scenario files.** A `*.yaml` that is not a regular file is refused, and `.yml` is accepted.

- [ ] **Step 4: Run every check**

Run: `make check`
Expected: PASS

- [ ] **Step 5: Try it by hand**

```bash
make build
cd testdata/e2e && SEMAR_SAMPLE_TOKEN=tok-alice ../../bin/semar run --all; echo "exit $?"
```

The profile of the scenario names the secret `env:SEMAR_SAMPLE_TOKEN`. Without it the command exits with 3 and sends nothing. The committed `testdata/e2e/.semar/semar.yaml` points at `http://127.0.0.1:9`, where nothing listens. Expected: the summary says `blocked` with "the build of orders could not be read", and the exit code is 2. A missing service is reported and does not crash.

- [ ] **Step 6: Commit**

```bash
git add internal/cli testdata
git commit -m "feat(cli): run, script add, confirm, and activity commands with end-to-end tests"
```

---

## What this plan leaves out

These belong to later plans and are not gaps in this one: any model or harness, the daemon and the MCP server, sign-in steps and OTP, the keychain, logs and traces, databases, planning from a change, the gate beyond a build probe, security probes, the web and Android, and publishing to a code host or a tracker.

## Self-review

* **Spec coverage.** Verdict rules 1 to 6: Task 12. The effects policy with `read_only`, deny, once, and reads: Task 6. No redirect off the list, no followed redirect of a write, and no resend of a write that may have reached the service: Task 8. Binding of a step to one request: Task 9. Redaction before storage: Tasks 5 and 7. The activity log, the data ledger, and "every write names its use case": Tasks 7 and 8. Run headers: Task 8. The evidence layout with hashes: Task 11. Local confirmation with an unverified identity, and the sources it confirmed: Tasks 11 and 16. Exit codes: Task 15. `effects.once` is parsed and exposed by `Policy.Once` in Task 6. Its use, skipping the immediate replay after authoring, belongs to Plan 02.
* **Types.** `api.Result.Action` is the redacted action in Tasks 8, 10, and 14, and `api.Result.Captured` holds the raw capture values that the runner in Task 14 uses as variables. `api.Call.Captures` carries the captures from the script. `check.Result` is used by `verdict.StepEvidence` in Task 12 and produced in Task 10. `recorder.Sink` in Task 7 is implemented by `evidence.Run` in Task 11. `replay.Scripts` in Task 14 is implemented by `evidence.Store` in Task 11.
* **Order.** Task 7 tests the recorder with a hand-written fake `Sink`. The real one arrives in Task 11. Task 13 comes before Task 14 because the runner's tests need the sample application.
