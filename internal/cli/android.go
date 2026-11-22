package cli

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jancuk/semar/internal/config"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/android"
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
			Permissions: slices.Clone(a.Permissions),
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
func (rg *envRig) mobileEngine(o options, repo string, env config.Environment, declared declaredBuilds) *mobile.Engine {
	apps := androidApps(repo, env, declared)
	if len(apps) == 0 {
		return nil
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
		Policy:       rg.policy,
		Recorder:     rg.recorder,
		Files:        rg.run,
		Secrets:      rg.secrets,
		Platform:     platform,
		Services:     services,
		Kind:         env.Kind,
		Run:          rg.id,
		CheckTimeout: env.Android.CheckTimeout(),
		Clock:        o.Clock,
		Sleep:        o.Sleep,
	})
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
