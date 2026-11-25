package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/android"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/replay"
)

// androidCloseLimit bounds the end of a run on a device. The run's own context may be cancelled by then,
// so the cleanup runs on one that is not, with this limit.
const androidCloseLimit = 15 * time.Second

// androidServices lists the services of the environment that have an app for android, in order. The
// slice is never nil, so that a scenario that names another service ends blocked.
func androidServices(env config.Environment) []string {
	out := []string{}
	for _, s := range env.Services {
		if s.Android != nil {
			out = append(out, s.Name)
		}
	}
	return out
}

// androidApps gives the app of each service that has one, in the form of the Android platform. The APK is
// an absolute path, and its digest is set only when the build of the service is that digest, so the
// file that is installed is the build that the result belongs to.
func androidApps(repo string, env config.Environment, declared declaredBuilds) map[string]android.App {
	apps := map[string]android.App{}
	for _, s := range env.Services {
		a := s.Android
		if a == nil {
			continue
		}
		app := android.App{
			Package:     a.AppPackage,
			Activity:    a.AppActivity,
			Ref:         a.App,
			Reset:       a.ResetMode(),
			Permissions: config.Permissions{All: a.Permissions.All, Names: slices.Clone(a.Permissions.Names)},
		}
		if a.APK != "" {
			app.APK = a.APK
			if declared.isAPK(s.Name) {
				app.APKDigest = declared.commits[s.Name]
			}
		}
		apps[s.Name] = app
	}
	return apps
}

// mobileEngine makes the engine of the app on a device. It starts nothing until the first step on a
// device needs it, so a run with no such step never looks for adb. It is nil when no service of the
// environment has an android app.
func (rg *envRig) mobileEngine(o options, repo string, env config.Environment, declared declaredBuilds, budget int) *mobile.Engine {
	eng, _ := rg.mobileEngineOn(o, repo, env, declared, budget, 0)
	return eng
}

// mobileEngineOn is mobileEngine that also gives the platform that the engine acts on, for a caller that
// reads the screen itself, as the recorder does. A zero settle uses the engine's default (3s).
func (rg *envRig) mobileEngineOn(o options, repo string, env config.Environment, declared declaredBuilds, budget int, settle time.Duration) (*mobile.Engine, mobile.Platform) {
	apps := androidApps(repo, env, declared)
	if len(apps) == 0 {
		return nil, nil
	}
	// A settle that the caller bounds is the one of a recording, where a person waits on every action.
	var poll time.Duration
	if settle > 0 {
		poll = recordingPoll
	}
	home, _ := o.home()
	services := map[string]mobile.Service{}
	for _, s := range env.Services {
		if s.Android != nil {
			services[s.Name] = mobile.Service{Schemes: slices.Clone(s.Android.Schemes)}
		}
	}
	platform := android.New(android.Options{
		Home:     home,
		Env:      env.Android,
		Apps:     apps,
		RepoRoot: repo,
		// The two references of a farm go through the secrets of the run, so that the redactor knows them.
		LookupEnv: func(name string) (string, bool) {
			if slices.Contains(env.Android.SecretEnvNames(), name) {
				v, err := rg.secrets.Resolve(context.Background(), "env:"+name)
				return v, err == nil
			}
			return o.LookupEnv(name)
		},
		Now:   o.Clock,
		Sleep: o.Sleep,
	})
	return mobile.New(mobile.Options{
		Policy:             rg.policy,
		Recorder:           rg.recorder,
		Files:              rg.run,
		Secrets:            rg.secrets,
		Platform:           platform,
		Services:           services,
		Kind:               env.Kind,
		Run:                rg.id,
		Budget:             budget,
		Settle:             settle,
		SettlePoll:         poll,
		CheckTimeout:       env.Android.CheckTimeout(),
		Clock:              o.Clock,
		LogcatOnFailure:    env.Android.LogcatOnFailure,
		AccessibilityCheck: env.Checks.AccessibilityMode() != "off",
		Sleep:              o.Sleep,
	}), platform
}

type androidDisplayFlags struct {
	theme        string
	fontScale    float64
	fontScaleSet bool
	locale       string
}

func addAndroidDisplayFlags(cmd *cobra.Command, f *androidDisplayFlags) {
	cmd.Flags().StringVar(&f.theme, "android-theme", "", "set Android theme for the run (light or dark); restored when the run ends")
	cmd.Flags().Float64Var(&f.fontScale, "android-font-scale", 0, "set Android system font scale for the run (0.5 to 3.0); restored when the run ends")
	cmd.Flags().StringVar(&f.locale, "android-locale", "", "set Android per-app BCP-47 locale for the run (Android 13+, e.g. en-US); restored when the run ends")
}

func validateAndroidDisplayFlags(cmd *cobra.Command, f *androidDisplayFlags) error {
	if f.theme != "" {
		f.theme = strings.ToLower(strings.TrimSpace(f.theme))
		if f.theme != "light" && f.theme != "dark" {
			return fmt.Errorf("--android-theme must be light or dark, got %q", f.theme)
		}
	}
	if cmd.Flags().Changed("android-font-scale") {
		f.fontScaleSet = true
		if math.IsNaN(f.fontScale) || math.IsInf(f.fontScale, 0) || f.fontScale < config.AndroidMinFontScale || f.fontScale > config.AndroidMaxFontScale {
			return fmt.Errorf("--android-font-scale must be from %.1f to %.1f, got %g", config.AndroidMinFontScale, config.AndroidMaxFontScale, f.fontScale)
		}
	}
	if f.locale != "" {
		f.locale = strings.TrimSpace(f.locale)
		if !config.ValidAndroidLocale(f.locale) {
			return fmt.Errorf("--android-locale %q must be a BCP-47 locale such as en-US", f.locale)
		}
	}
	return nil
}

func applyAndroidDisplayFlags(stderr io.Writer, env config.Environment, f androidDisplayFlags) (config.Environment, error) {
	if f.theme == "" && !f.fontScaleSet && f.locale == "" {
		return env, nil
	}
	if env.Android.Remote() {
		return env, errors.New("--android-theme, --android-font-scale, and --android-locale need adb and cannot be used with android.serverUrl")
	}
	if len(androidServices(env)) == 0 {
		if stderr != nil {
			var passed []string
			if f.theme != "" {
				passed = append(passed, "--android-theme")
			}
			if f.fontScaleSet {
				passed = append(passed, "--android-font-scale")
			}
			if f.locale != "" {
				passed = append(passed, "--android-locale")
			}
			fmt.Fprintf(stderr, "semar: ignoring %s: this environment has no Android service\n", strings.Join(passed, ", "))
		}
		return env, nil
	}
	if f.theme != "" {
		env.Android.Theme = f.theme
	}
	if f.fontScaleSet {
		env.Android.FontScale = f.fontScale
	}
	if f.locale != "" {
		env.Android.Locale = f.locale
	}
	return env, nil
}

const (
	// actionsPerStep is how many device actions the budget allows for each step of a scenario. A step
	// that uses a flow is many actions, and a retry replays them all, so a run that signs in with a flow
	// in every scenario needs more than the default of the engine.
	actionsPerStep = 8
	// maxMobileBudget bounds the budget, so that a very large run still has a limit.
	maxMobileBudget = 20000
)

// mobileBudget is the number of device actions that a run of steps scenario steps may take, retries
// included. It never goes under the engine's default of 200.
//
// perStep is how many actions one step may take: the default, or the length of the longest flow of the
// repository when that is more, because a step that uses a flow runs all its steps.
func mobileBudget(steps, retries, perStep int) int {
	return min(max(200, steps*max(perStep, actionsPerStep)*(max(retries, 0)+1)), maxMobileBudget)
}

// longestFlow is the number of steps of the longest flow of the repository, or 0 when it has none.
func longestFlow(repo string) int {
	flows, _ := flow.LoadAll(repo)
	n := 0
	for _, e := range flows {
		n = max(n, len(e.Flow.Steps))
	}
	return n
}

// scenarioSteps counts the steps of the scenarios.
func scenarioSteps(scenarios []domain.Scenario) int {
	n := 0
	for _, sc := range scenarios {
		n += len(sc.Steps)
	}
	return n
}

// closeMobile ends what the device engine started. It runs on a context that the run's cancellation does
// not reach, so that Ctrl-C leaves no instrumentation and no forward behind. It is safe on nil.
func closeMobile(ctx context.Context, eng *mobile.Engine) {
	if eng == nil {
		return
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), androidCloseLimit)
	defer cancel()
	_ = eng.Close(cctx)
}

// useMobile gives the replay the device engine, and the services that have an app. With no android
// service the engine stays nil, and a step on a device ends blocked with that reason.
func useMobile(deps *replay.Deps, eng *mobile.Engine, env config.Environment) {
	deps.MobileServices = androidServices(env)
	if eng != nil {
		deps.Mobile = eng
	}
}

// androidGap says why the android scenarios of a run cannot run here, or "" when they can. It reads
// nothing from the device: it asks whether the environment has an app, whether it allows a device at
// all, and whether adb is there for a run that uses it.
func androidGap(o options, repo string, env config.Environment) string {
	switch {
	case len(androidServices(env)) == 0:
		return "no service of the environment has an android app: add android.appPackage to one in semar.yaml"
	case env.Kind == config.KindProduction:
		return "the environment is production, and the app on a device makes requests that Semar cannot see"
	case env.Effects.Mode == "read_only":
		return "the environment is read only, and the app on a device makes requests that Semar cannot see"
	case env.Android.Remote():
		return ""
	}
	if _, err := android.FindADB(o.LookupEnv, repo); err != nil {
		return err.Error()
	}
	return ""
}

// surfaceGaps names the scenarios that cannot run on this machine, with the reason.
func surfaceGaps(o options, repo string, cfg config.Config, envName string, scs []domain.Scenario) []string {
	var out []string
	_, env, err := cfg.Environment(envName)
	if err != nil {
		return nil
	}
	reason, checked := "", false
	for _, sc := range scs {
		if sc.Surface != domain.SurfaceAndroid {
			continue
		}
		if !checked {
			reason, checked = androidGap(o, repo, env), true
		}
		if reason != "" {
			out = append(out, fmt.Sprintf("%s is an Android scenario, which cannot run here: %s", sc.ID, reason))
		}
	}
	return out
}
