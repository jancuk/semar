package mobilemcp

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/engine/mobile/android"
)

const (
	healthProbeCooldown = 2 * time.Second
	healthProbeTimeout  = 1500 * time.Millisecond
	healthCrashLimit    = 10
	healthLogLimit      = 100
	maxHealthNotes      = 3
)

// screenHealth tracks app lifecycle and diagnostic probe baselines for proactive crash and
// JS/Flutter error notes on screen reads.
type screenHealth struct {
	baselineDone     bool
	baselineCrashMax time.Time // max device-clock Crash.Time seen during baseline (same clock as Crash.Time)
	lastProbe        time.Time
	seenCrashes      map[string]bool
	seenLogErrors    map[string]bool
	trackedPkg       string
	intentionalExit  bool
	reportedExit     bool
}

func (m *Manager) trackLaunch(pkg string) {
	m.health.trackedPkg = pkg
	m.health.intentionalExit = false
	m.health.reportedExit = false
	m.health.lastProbe = time.Time{}
}

func (m *Manager) trackExit(pkg string, clearPkg bool) {
	if pkg == "" || pkg == m.health.trackedPkg {
		m.health.intentionalExit = true
		if clearPkg {
			m.health.trackedPkg = ""
		}
	}
}

func (m *Manager) trackIntentionalNav() {
	if m.health.trackedPkg != "" {
		m.health.intentionalExit = true
	}
}

// baselineHealth snapshots existing crashes and runtime log errors once per device session so
// pre-existing noise is suppressed while launch-time and later errors are reported.
func (m *Manager) baselineHealth(ctx context.Context, dev Device) {
	if m.health.baselineDone || m.mode == modeRecord || dev == nil || dev.Remote() {
		return
	}
	m.health.baselineDone = true
	m.health.baselineCrashMax = time.Time{}
	m.health.seenCrashes = map[string]bool{}
	m.health.seenLogErrors = map[string]bool{}
	m.probeHealth(ctx, dev, true)
}

// healthNotes returns up to maxHealthNotes concise, single-line, scrubbed notes describing new
// native crashes, unexpected app exits, or React Native / Flutter errors since the previous read.
// Diagnostic failures are swallowed silently so listing the screen never fails.
func (m *Manager) healthNotes(ctx context.Context, dev Device, t *mobile.Tree, ws []mobile.Window) []string {
	if m.mode == modeRecord {
		return nil
	}
	exitReason := ""
	unexpectedExit := false
	if exited, reason := m.detectUnexpectedExit(t, ws); exited && !m.health.reportedExit {
		unexpectedExit = true
		exitReason = reason
		m.health.reportedExit = true
	}

	var crashNotes []string
	var logNotes []string

	if dev != nil && !dev.Remote() {
		now := m.clock()
		if !m.health.baselineDone {
			m.baselineHealth(ctx, dev)
			m.health.lastProbe = now
		} else if unexpectedExit || m.health.lastProbe.IsZero() || now.Sub(m.health.lastProbe) >= healthProbeCooldown {
			m.health.lastProbe = now
			crashNotes, logNotes = m.probeHealth(ctx, dev, false)
		}
	}

	var notes []string
	for _, n := range crashNotes {
		if len(notes) >= maxHealthNotes {
			break
		}
		notes = append(notes, n)
	}
	if unexpectedExit && len(crashNotes) == 0 && len(notes) < maxHealthNotes {
		note := fmt.Sprintf("app %s is no longer on screen (%s); check mobile_list_crashes", m.health.trackedPkg, exitReason)
		notes = append(notes, collapse(m.scrub.Text(note)))
	}
	for _, n := range logNotes {
		if len(notes) >= maxHealthNotes {
			break
		}
		notes = append(notes, n)
	}
	return notes
}

func (m *Manager) probeHealth(ctx context.Context, dev Device, baseline bool) (crashNotes, logNotes []string) {
	pkg := ""
	if !baseline {
		pkg = m.health.trackedPkg
	}
	cctx, ccancel := context.WithTimeout(ctx, healthProbeTimeout)
	crashes, cerr := dev.ListCrashes(cctx, pkg, healthCrashLimit)
	ccancel()
	if cerr == nil {
		for _, cr := range crashes {
			if cr.ID == "" || m.health.seenCrashes[cr.ID] {
				continue
			}
			m.health.seenCrashes[cr.ID] = true
			if baseline {
				if cr.Time.After(m.health.baselineCrashMax) {
					m.health.baselineCrashMax = cr.Time
				}
				continue
			}
			// Both Crash.Time and baselineCrashMax come from the device's wall clock, avoiding host/device
			// timezone skew while still filtering older rotated dropbox entries predating the baseline.
			if !cr.Time.IsZero() && !m.health.baselineCrashMax.IsZero() && !cr.Time.After(m.health.baselineCrashMax) {
				continue
			}
			who := nonEmpty(cr.Package, cr.Process)
			if who == "" {
				who = "app"
			}
			summary := truncateText(collapse(cr.Summary), 120)
			var line string
			if summary != "" {
				line = fmt.Sprintf("crash detected: %s (%s) — call mobile_get_crash with id %q", who, summary, cr.ID)
			} else {
				line = fmt.Sprintf("crash detected: %s — call mobile_get_crash with id %q", who, cr.ID)
			}
			crashNotes = append(crashNotes, collapse(m.scrub.Text(line)))
		}
	}

	queries := []android.LogQuery{
		{
			Filters: []android.LogFilter{{Key: "tag", Value: "ReactNativeJS"}, {Key: "level", Value: "E"}},
			Limit:   healthLogLimit,
			Follow:  false,
		},
		{
			Filters: []android.LogFilter{{Key: "tag", Value: "flutter"}},
			Limit:   healthLogLimit,
			Follow:  false,
		},
	}
	for _, q := range queries {
		lctx, lcancel := context.WithTimeout(ctx, healthProbeTimeout)
		entries, err := dev.Logs(lctx, q)
		lcancel()
		if err != nil {
			continue
		}
		for _, e := range entries {
			kind, ok := classifyRuntimeLogError(e)
			if !ok {
				continue
			}
			msg := collapse(e.Message)
			if msg == "" {
				continue
			}
			key := e.Time + "\x00" + e.Tag + "\x00" + msg
			if m.health.seenLogErrors[key] {
				continue
			}
			m.health.seenLogErrors[key] = true
			if baseline {
				continue
			}
			line := fmt.Sprintf("%s error: %s", kind, truncateText(msg, 160))
			logNotes = append(logNotes, collapse(m.scrub.Text(line)))
		}
	}
	return crashNotes, logNotes
}

func classifyRuntimeLogError(e android.LogEntry) (string, bool) {
	switch e.Tag {
	case "ReactNativeJS":
		if e.Level == "E" || e.Level == "F" {
			return "ReactNativeJS", true
		}
	case "flutter":
		if e.Level == "E" || e.Level == "F" ||
			strings.Contains(e.Message, "EXCEPTION CAUGHT BY") ||
			strings.Contains(e.Message, "Unhandled Exception:") ||
			strings.Contains(e.Message, "Fatal error:") {
			return "Flutter", true
		}
	}
	return "", false
}

var launcherPackages = []string{
	"com.android.launcher",
	"com.android.launcher3",
	"com.google.android.apps.nexuslauncher",
	"com.sec.android.app.launcher",
	"com.miui.home",
}

func isLauncherPackage(pkg string) bool {
	if slices.Contains(launcherPackages, pkg) {
		return true
	}
	lower := strings.ToLower(pkg)
	return strings.Contains(lower, "launcher") && !strings.Contains(lower, "permission")
}

func isSystemOrLauncher(pkg string) bool {
	return pkg == "android" || pkg == "com.android.systemui" || isLauncherPackage(pkg)
}

func isCrashDialogTree(t *mobile.Tree, ws []mobile.Window) bool {
	found := false
	t.Walk(func(n *mobile.Node) bool {
		if !n.Visible {
			return true
		}
		w := mobile.WindowOf(n, ws)
		if w != nil && w.Kind != mobile.WindowSystem && w.Package != "android" {
			return true
		}
		for _, s := range []string{n.Text, n.Desc} {
			lower := strings.ToLower(s)
			if strings.Contains(lower, "keeps stopping") || strings.Contains(lower, "has stopped") || strings.Contains(lower, "isn't responding") {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// detectUnexpectedExit reports whether the last launched app vanished from the screen without an
// intentional exit (terminate/clear/uninstall/HOME/BACK/APP_SWITCH/openURL). Permission dialogs,
// notification shade / system overlays, and unrelated apps do not trigger it. When the tracked app
// reappears in WindowApp, intentionalExit and reportedExit reset so later crashes are caught.
func (m *Manager) detectUnexpectedExit(t *mobile.Tree, ws []mobile.Window) (bool, string) {
	if m.health.trackedPkg == "" || t == nil || t.Root == nil {
		return false, ""
	}
	if isCrashDialogTree(t, ws) {
		return true, "crash dialog shown"
	}
	anyPkg := false
	for _, w := range ws {
		if w.Package != "" {
			anyPkg = true
		}
		if w.Package == m.health.trackedPkg {
			if w.Kind == mobile.WindowApp {
				m.health.intentionalExit = false
				m.health.reportedExit = false
			}
			return false, ""
		}
	}
	if !anyPkg || m.health.intentionalExit {
		return false, ""
	}
	nonKeyboard := 0
	allSystemOrLauncher := true
	hasLauncher := false
	for _, w := range ws {
		if w.Kind == mobile.WindowKeyboard {
			continue
		}
		if w.Package == "" {
			continue
		}
		nonKeyboard++
		if isLauncherPackage(w.Package) {
			hasLauncher = true
		}
		if !isSystemOrLauncher(w.Package) {
			allSystemOrLauncher = false
		}
	}
	if nonKeyboard > 0 && allSystemOrLauncher && hasLauncher {
		return true, "screen returned to launcher"
	}
	return false, ""
}
