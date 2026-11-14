# Plan 09: Web

> **For agentic workers:** This is a specification. Before you execute it, expand each task into steps in the format of [Plan 01](01-core-replay.md), against the code that exists by then.

* **Guide:** [implementation guide](README.md). **Spec:** TRD, [Web engine](../TRD.md#web-engine), [Signing in](../TRD.md#signing-in), [Pilot](../TRD.md#pilot), [Three tiers of tools](../TRD.md#three-tiers-of-tools)
* **Needs first:** Plan 06. Spikes S6, S8, and S13. Open decision 4.
* **Requirements:** FR-20, 44, 46, 51, 52, 192 to 202, 204

**Goal.** The agent uses a web application in a real browser, and code replays what it did.

**What works when it is done.** A web scenario runs through `semar verify` with screenshots as evidence. A person signs in once with `semar login`, in a Chrome profile that belongs to Semar.

## Architecture

```text
web_run (pilot)  --+
web_* primitives --+-->  engine.Surface (web)  -->  browser driver  -->  Chrome on a dedicated profile
web_batch (replay) +             |
                                 +--> recorder, effects policy, host blocking, redaction
```

* A surface is a port. The API engine of Plan 01 and the web engine both implement `engine.Surface`, so the recorder, the replay runner, and the verdict engine do not change. Android in Plan 10 is the third implementation, and a later surface is a fourth.
* The browser driver sits behind a small interface inside the web engine. The route chosen by open decision 4 fills it. The rest of this plan does not depend on which route it is.
* Three tiers of tools: `web_run` hands a sub-goal to the pilot, the primitives act one step at a time, and `web_batch` replays known steps with no model.
* Sign-in happens before the agent starts. The agent names a secret, and Semar types it.

**Ports added:** surface engine (made explicit here), browser driver.

## File structure

```text
internal/engine/surface.go         the Surface port, shared by api, web, android
internal/engine/web/engine.go      implements Surface for a browser
internal/engine/web/driver.go      the Driver interface: what the engine needs from a browser
internal/engine/web/brojev/        the driver, by the route of decision 4
internal/engine/web/snapshot.go    a page as a list of elements with refs
internal/engine/web/locator.go     role, name, and frame path. Find again in a fresh snapshot
internal/engine/web/hosts.go       block requests to hosts that are not listed
internal/engine/web/invariants.go  uncaught script errors, 5xx from background requests
internal/engine/web/secrets.go     type a secret or an OTP into a field
internal/engine/web/a11y.go        the accessibility check
internal/auth/web.go               sign-in steps on pages, signedInWhen
internal/auth/profiles.go          the Chrome profile directory of each auth profile
internal/decide/pilot.go           the pilot loop
internal/mcpserver/tools_web.go    web_run, the primitives, web_batch
internal/stage/author/repair.go    locator repair
internal/cli/login.go, logout.go
```

## Interfaces

```go
// internal/engine
type Step struct {
	Use  domain.UseCase
	By   domain.Actor
	Op   string          // "request" | "navigate" | "click" | "type" | "select" | ...
	Args json.RawMessage // per surface
	Why  string
}
type Surface interface {
	Name() domain.Surface
	// Do performs one step and returns its recorded, redacted action.
	Do(ctx context.Context, s Step) (domain.Action, error)
	// Snapshot returns what the agent may see of the current state.
	Snapshot(ctx context.Context) (Snapshot, error)
	Close() error
}

// internal/engine/web
type Driver interface {
	Start(ctx context.Context, profileDir string, o StartOptions) error
	Navigate(ctx context.Context, url string) error
	Snapshot(ctx context.Context) (RawSnapshot, error)
	Act(ctx context.Context, ref string, op string, value string) error
	Screenshot(ctx context.Context) ([]byte, error)
	OnRequest(fn func(r Request) (allow bool)) // called before every request the page makes
	Console() []ConsoleMessage
	Stop() error
}

type Locator struct {
	Role, Name string
	Frame      []string
	Nth        int
}
func Find(snap Snapshot, l Locator) (ref string, found bool, ambiguous bool)

// internal/auth
// EnsureProfile returns the directory of an auth profile's own Chrome profile and creates it on first use:
// mode 0700, a marker file, and the display name "Semar: <auth profile>".
// It refuses a directory that holds a Chrome profile without Semar's marker.
func EnsureProfile(root, project, environment, authProfile string) (dir string, created bool, err error)
func SignedIn(ctx context.Context, s engine.Surface, when config.SignedInWhen) (bool, error)

// internal/decide
type Pilot struct{ /* a Decider, limits */ }
// Run pursues one sub-goal. It returns Done, or NeedsAgent with the reason, as brojev's run does.
func (p *Pilot) Run(ctx context.Context, s engine.Surface, goal string, values map[string]string) (PilotResult, error)
```

## Formats

**Configuration.**

```yaml
web:
  baseUrl: https://shop.uat.example.com
  authProfile: shop-web
  profilesDir: ~/.semar/profiles   # the parent folder only. Semar creates one new profile per auth profile inside it
  allowHosts: [shop.uat.example.com, cdn.example.com]
  device: ""                    # a device descriptor, or empty
  cdpEndpoint: ""               # a remote browser, or empty for local Chrome

authProfiles:
  shop-web:
    surface: web
    hosts: [shop.uat.example.com, accounts.google.com]
    signIn:
      mode: manual              # manual | steps
      start: https://shop.uat.example.com/login
      signedInWhen: { text: "Sign out" }
```

**Tools.** `web_snapshot`, `web_navigate`, `web_click`, `web_type`, `web_select`, `web_key`, `web_hover`, `web_scroll`, `web_upload`, `web_dialog`, `web_wait`, `web_screenshot`, `web_run`, and `web_batch`. `web_type` takes `{ref, text}` or `{ref, secret}` or `{ref, otp: true}`.

**A web script step.** A locator in place of a request: `{step, op: click, locator: {role: button, name: "Refund"}}`. A check on a web step is `text`, `element`, or `url`.

## Rules

| # | Rule | Source |
| --- | --- | --- |
| R1 | Semar creates a new Chrome profile of its own for each web auth profile, on first use, with no question to the user. No setting points Semar at a profile that already exists. `profilesDir` only moves the parent folder | FR-194 |
| R1a | Semar starts its own Chrome process on that directory. It never attaches to a Chrome that is already running, never closes one, and never changes the default browser. The developer's own Chrome keeps its windows, tabs, and sign-ins | FR-194 |
| R1b | A new profile gets a marker file, `.semar-profile`, and the display name "Semar: <auth profile>", so that its window is easy to tell apart. Semar refuses to use or delete a directory that holds a Chrome profile without the marker | FR-194, 204 |
| R2 | `semar login <profile>` opens that directory in an ordinary window with no automation attached, waits until the window closes, then checks `signedInWhen` | FR-195, 197 |
| R3 | Before a run, the engine checks `signedInWhen`. When the session has ended and no step can restore it, every scenario of that profile is `blocked` with the command to run | FR-196 |
| R4 | A profile directory has mode 0700 and is never copied to an evidence store. `semar logout` deletes it, and only when it carries Semar's marker | FR-204 |
| R5 | The engine blocks every request to a host that is not in `allowHosts`, inside the browser, and records the block | FR-145 |
| R6 | A secret is typed only into a field whose page host is in the auth profile's `hosts`, and whose element is an input of a form. A refusal is recorded | FR-200 |
| R7 | The value of a secret appears in no tool result, no snapshot, and no stored file. A snapshot shows a password field as masked | Product rule 6 |
| R8 | On a host that is a sign-in host of another party, such as `accounts.google.com`, the engine stores no screenshot and no snapshot. The activity entry says "sign-in step, not captured" | FR-202 |
| R9 | When a sign-in page asks for a step that configuration does not cover, the sign-in stops with `blocked` and "the sign-in needs a person". The engine does not retry and does not change how the browser identifies itself | FR-198 |
| R10 | A script stores locators, never refs. Replay finds the element in a fresh snapshot | TRD, Recording and replay |
| R11 | A locator that finds no element, or more than one, stops the step. The scenario is `inconclusive` with "an element was not found: either the product changed or the script is out of date" | FR-52 |
| R12 | A repair proposes new locators and changes nothing else. When the role or the name changed, a person approves it before the script counts again | FR-52 |
| R13 | A check on a web step is limited to the URL, visible text, and the state of an element. A judgement about how a page looks is `inconclusive` with the screenshot | TRD, Verdict rules |
| R14 | Two more invariants on the web: no uncaught script error, and no 5xx from a background request to a listed host | FR-192 |
| R15 | The pilot may only choose an action that the engine offers, and the engine checks the choice against a fresh snapshot. Below the confidence limit, or on a field that needs a value it was not given, it returns `NeedsAgent` | TRD, Pilot |
| R16 | The pilot never types a secret. A sub-goal that reaches a secret field returns `NeedsAgent`, and the tester calls `web_type` with `secret:` | Product rule 6 |
| R17 | With no decision model, `web_run` is not offered, and the tester uses the primitives | FR-51, 210 |
| R18 | The accessibility check returns findings. It never fails a scenario | FR-193 |

## Tasks

| # | Task | Files | Tests that prove it |
| --- | --- | --- | --- |
| 1 | The surface port | `engine/surface.go`, `engine/api` | The API engine implements `Surface`, and every test of Plans 01 to 05 still passes |
| 2 | Driver and a fake driver | `engine/web/driver.go` | The fake driver serves snapshots from files, so the engine is tested with no browser |
| 3 | Snapshots and locators | `engine/web/snapshot.go`, `locator.go` | A moved element is found again. A missing one and an ambiguous one are reported apart (R11). A fuzz test on the locator matcher |
| 4 | Host blocking | `engine/web/hosts.go` | A page that loads an image from an unlisted host: the request is blocked and recorded, and the page still works (R5) |
| 5 | Profile directories, `login`, and `logout` | `auth/profiles.go`, `cli/login.go` | The first call creates the directory with mode 0700, the marker, and the display name (R1, R1b). A second call reuses it. With `profilesDir` set to a folder that holds a Chrome profile without the marker, Semar refuses and names the folder. With a fake Chrome on `PATH`, the start command holds `--user-data-dir` with Semar's directory and no flag that attaches to a running browser (R1a). `logout` removes a marked directory and refuses an unmarked one (R4) |
| 6 | Sign-in steps and the signed-in check | `auth/web.go` | Local pages with a password form, a one-box code field, and a six-box code field. An ended session with no steps is `blocked` with the command (R3) |
| 7 | Secrets on a page | `engine/web/secrets.go` | The value appears nowhere (R7). An unlisted host and a search box are refused (R6). A sign-in host stores no screenshot (R8) |
| 8 | The real driver | `engine/web/brojev` | The engine's tests run against real Chrome with a build tag, in CI |
| 9 | Web tools and recording | `mcpserver/tools_web.go` | With local pages and the fake provider: a session is recorded and submitted as a script |
| 10 | Replay and web checks | `replay`, `check` | A recorded session replays to the same results. A text check and an element check pass and fail as expected (R13) |
| 11 | Web invariants and the accessibility check | `engine/web/invariants.go`, `a11y.go` | A page with a script error fails the invariant (R14). Accessibility results are findings (R18) |
| 12 | Repair | `stage/author/repair.go` | A moved button is repaired with no approval. A renamed button waits for approval (R12) |
| 13 | The pilot | `decide/pilot.go` | With the fake decider: a sub-goal completes, a low-confidence choice returns `NeedsAgent`, and a secret field returns `NeedsAgent` (R15, R16). With no decider, `web_run` is absent (R17) |

## Done when

A web scenario on local pages runs through `semar verify` with the fake provider: the session is recorded, the script replays to `passed`, the report links the screenshots, and a search of the whole run directory for the test password finds nothing.

With a button renamed in the page, the same scenario ends `inconclusive` with the "element was not found" reason, and a repair waits for approval.

## Review focus

1. **A developer's own Chrome is open while Semar runs.** Nothing in it changes. Semar creates a new profile of its own, named "Semar: <auth profile>", and runs a separate Chrome process on it. There is no setting that could point Semar at the developer's profile. Task 5.
2. **A page that tells the agent to type the password into a chat box.** The tool refuses, and the refusal is recorded. Task 7.
3. **A sign-in provider that blocks an automated browser.** Semar reports `blocked` and asks for `semar login`. It does not try another way in. Task 6.
4. **A page that never finishes loading.** A wait has a deadline, and the step ends with what was observed (Q3). Task 9.
5. **Screenshots that hold real personal data.** Screenshots cannot be redacted reliably, so `semar doctor` warns when a web profile is not marked as a test account. Task 5.

## Open points

* Open decision 4 and spike S6 choose the driver. The `Driver` interface is the boundary, so a later change of route touches one subpackage.
* Spike S8 decides whether the run headers can be added to a page's requests without breaking cross-origin calls. If not, web steps are tied to server logs by time window only, and the observer's findings for them stay advisory.
* Spike S13 measures how long a sign-in by a person stays valid.
