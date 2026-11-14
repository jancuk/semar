# Plan 10: Android

> **For agentic workers:** This is a specification. Before you execute it, expand each task into steps in the format of [Plan 01](01-core-replay.md), against the code that exists by then.

* **Guide:** [implementation guide](README.md). **Spec:** TRD, [Android engine](../TRD.md#android-engine), [Signing in](../TRD.md#signing-in)
* **Needs first:** Plan 09. Spike S7.
* **Requirements:** FR-21, 45, 136, 203

**Goal.** The agent uses a native Android application on an emulator or a device, and code replays what it did.

**What works when it is done.** An Android scenario runs through `semar verify` with screenshots and the device log as evidence. A journey can cross the API, the web, and Android on one order.

## Architecture

```text
android_run (pilot)  --+
android_* primitives --+-->  engine.Surface (android)  -->  device driver  -->  adb  -->  emulator or device
android_batch (replay) +
```

* The Android engine is the third implementation of `engine.Surface` from Plan 09. Recording, replay, checks, locators, the pilot, and secret typing are reused. This plan adds the device side only.
* The device driver sits behind an interface, as the browser driver does. Spike S7 picks the first one. A company whose application needs another driver replaces one subpackage.
* A later platform, such as iOS, is a fourth surface with its own driver. It reuses everything above the driver, and its results never stand in for Android's.

**Ports added:** device driver.

## File structure

```text
internal/engine/android/engine.go      implements Surface for a device
internal/engine/android/driver.go      the Driver interface
internal/engine/android/adb.go         install, launch, clear, logcat, screenshot, through the adb command
internal/engine/android/<driver>/      the on-device server that spike S7 picks
internal/engine/android/snapshot.go    a screen as a list of elements with refs
internal/engine/android/locator.go     accessibility ID, then resource ID, then text
internal/engine/android/build.go       the application's build: package, version, commit when the app exposes one
internal/engine/android/invariants.go  crashes and "application not responding"
internal/auth/android.go               sign-in steps on screens, emulator snapshots
internal/mcpserver/tools_android.go    android_run, the primitives, android_batch
internal/workflow/journey.go           one journey across surfaces
```

## Interfaces

```go
// internal/engine/android
type Driver interface {
	Connect(ctx context.Context, serial string) error
	Install(ctx context.Context, apk string) error
	Launch(ctx context.Context, pkg string) error
	Clear(ctx context.Context, pkg string) error
	Snapshot(ctx context.Context) (RawSnapshot, error)
	Act(ctx context.Context, ref string, op string, value string) error // tap, type, swipe, key
	Screenshot(ctx context.Context) ([]byte, error)
	Log(ctx context.Context, since time.Time) ([]LogLine, error)
	Close() error
}

type Locator struct {
	AccessibilityID string
	ResourceID      string
	Text            string
	Screen          string // the activity or the screen name
}
func Find(snap Snapshot, l Locator) (ref string, found bool, ambiguous bool)

// internal/workflow
type Journey struct {
	ID        string
	Scenarios []domain.ScenarioID // in order, across surfaces
	Shared    []string            // capture names that later scenarios use, such as order_id
}
```

## Formats

**Configuration.**

```yaml
android:
  device: emulator-5554
  app: { package: com.example.shop, apk: build/shop-uat.apk }
  authProfile: shop-android
  keepAppData: true
  snapshot: signed-in            # an emulator snapshot to start from, or empty

authProfiles:
  shop-android:
    surface: android
    signIn:
      mode: manual               # manual | steps
      signedInWhen: { screen: HomeActivity }
```

**A journey,** in a scenario file's sibling `journey.yaml`:

```yaml
apiVersion: semar/v1
kind: Journey
id: SHOP-482-J1
scenarios: [SHOP-482-05, SHOP-482-06, SHOP-482-07]   # android, api, web
shared: [order_id]
```

## Rules

| # | Rule | Source |
| --- | --- | --- |
| R1 | Every `adb` call is an argument list with the device serial. No shell string is built from a value that comes from a file or a tool call | G13 |
| R2 | Evidence from Android comes from the installed application on a declared emulator or device. A browser sized like a phone is never reported as Android | FR-136 |
| R3 | The run records the application's package, its version, and the commit when the application exposes one. A scenario for a build that is not the one under test is `blocked` | Product rule 5 |
| R4 | A locator tries the accessibility ID, then the resource ID, then the exact text. A script stores all three when the element has them | TRD, Testing practices built into Semar |
| R5 | An element with no accessibility ID and no resource ID is recorded with a warning. The report lists such elements, because their steps break more easily | TRD, Android engine |
| R6 | A crash or an "application not responding" entry for the application's package during a step breaks an invariant | TRD, Verdict rules |
| R7 | The device log is stored for the step's time window only, after redaction | Product rule 7 |
| R8 | Semar never types a password of a third-party account on a device. Sign-in with Google uses an account that a person added to the emulator once | FR-203 |
| R9 | `semar login <profile>` for Android opens the system's account settings on the emulator and waits. After a successful sign-in in the application, Semar can save an emulator snapshot | TRD, Android sign-in |
| R10 | With `keepAppData: false`, the application's data is cleared before each scenario. With `true`, a scenario must not depend on data that an earlier scenario left | Q2 |
| R11 | The application makes its own requests, so Semar adds no header to them. The activity log ties an Android step to the service's logs by the test account and the time window, and observer findings for it are advisory unless the service continues a trace ID from the application | TRD, Activity log |
| R12 | A journey passes only when every scenario in it passed on one set of builds, and each shared value was captured by an earlier scenario of the same run | FR-134 |
| R13 | A missing emulator, a missing APK, or a device that is offline gives `blocked` with the piece that is missing. A project that has no Android application marks the surface "not applicable" in configuration, and no scenario is planned for it | FR-135 |

## Tasks

| # | Task | Files | Tests that prove it |
| --- | --- | --- | --- |
| 1 | The `adb` wrapper | `engine/android/adb.go` | With a fake `adb` on `PATH`: install, launch, clear, a screenshot, and a log window. A serial with a space or a leading dash is passed as one argument (R1) |
| 2 | Driver interface and a fake driver | `engine/android/driver.go` | The fake serves screens from files, so the engine is tested with no device |
| 3 | Snapshots and locators | `engine/android/snapshot.go`, `locator.go` | The three-step lookup order (R4). An element with no stable ID is flagged (R5). Missing and ambiguous are reported apart |
| 4 | The engine | `engine/android/engine.go` | The surface tests of Plan 09 run against the Android engine with the fake driver |
| 5 | The application's build | `engine/android/build.go` | A package whose version differs from the configured build is `blocked` (R3) |
| 6 | Invariants and the device log | `engine/android/invariants.go` | A crash line for the package breaks the invariant. A crash of another package does not. The stored log covers the step's window only (R6, R7) |
| 7 | The real driver | `engine/android/<driver>` | The engine's tests run on an emulator with a test application, with a build tag, in CI |
| 8 | Sign-in and snapshots | `auth/android.go`, `cli/login.go` | With the fake driver: sign-in steps fill fields and the code. A sign-in host stores no screenshot. A saved snapshot is started when configured |
| 9 | Tools, recording, and replay | `mcpserver/tools_android.go`, `replay` | With the fake provider: a session is recorded, submitted, and replayed to the same results |
| 10 | Journeys | `workflow/journey.go` | An order created in the Android scenario is read in the API scenario and the web scenario. With one scenario on another build, the journey is `stale` (R12) |

## Done when

On an emulator in CI, a test application's scenario runs through `semar verify` with the fake provider: recorded, replayed to `passed`, with screenshots and a device log window in the run directory.

A journey test, with the fake drivers, creates an order on Android and checks it through the API and on a web page. When the web page shows another order, the journey fails on the readback.

## Review focus

1. **The emulator is detected and the application refuses to run.** The scenario is `blocked` with what the application showed. Task 7.
2. **The application pins its certificates.** Semar does not intercept its traffic, so this plan is not affected. The trace ID limit of R11 is the cost. Task 9.
3. **A screen with no accessibility labels.** Steps are recorded with text locators and a warning, and the report lists them. Task 3.
4. **The build of the application does not match the backend's build.** The run records both, and a mismatch that configuration declares as invalid is `blocked`. Task 5.
5. **A pipeline runner with a fresh emulator.** No third-party account exists on it. Sign-in must be scripted, or come from a prepared snapshot, and `semar doctor` says which is missing. Task 8.

## Open points

* Spike S7 picks the driver and checks emulator detection, certificate pinning, and how an OTP reaches the device.
* Device video is left out of this plan. Screenshots per step are the evidence.
* A device farm is not managed by Semar. A remote device is used when `adb` can reach it.
