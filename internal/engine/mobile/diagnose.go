package mobile

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jancuk/semar/internal/domain"
)

const (
	// diagnoseLimit bounds the questions to the device after a failure, so a dead device does not hold up
	// the run.
	diagnoseLimit = 15 * time.Second
	logcatLines   = 200
	maxSummary    = 300
)

// diagnose adds the evidence of a crash to an action that failed: the report of the crash that the
// device recorded since the run began, and, when the configuration asks, the end of the log of the app.
// It runs when the action failed, or when the step looks for an element that is not there and the app is
// no longer in front. It only adds files and a reference to them: it never changes the error, so the
// verdict stays what the checks say. Any trouble is a debug line.
func (e *Engine) diagnose(ctx context.Context, d Driver, c Call, a *domain.Action, targetMissing bool) {
	dg, ok := e.o.Platform.(Diagnoser)
	if !ok || e.o.Files == nil || e.o.Recorder == nil || ctx.Err() != nil {
		return
	}
	failed := a.Err != "" && !isNotFoundText(a.Err)
	if !failed && !targetMissing {
		return
	}
	pkg := a.Mobile.Package
	if pkg == "" {
		pkg = d.About().Package
	}
	if pkg == "" {
		return
	}
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), diagnoseLimit)
	defer cancel()
	if !failed && !e.appGone(dctx, pkg) {
		// The target is missing, and the app is in front: it is a screen that differs, not a crash.
		return
	}
	crashes, err := dg.Crashes(dctx, pkg, e.started)
	if err != nil {
		e.o.Logger.DebugContext(ctx, "no crashes", "error", err)
		crashes = nil
	}
	name := ""
	evidence := func() string {
		if name == "" {
			name = e.evidenceName(a.Use)
		}
		return name
	}
	for _, cr := range crashes {
		e.mu.Lock()
		seen := e.crashes[cr.ID]
		e.crashes[cr.ID] = true
		e.mu.Unlock()
		if seen {
			continue
		}
		rel := "crash-" + evidence() + ".txt"
		body := fmt.Sprintf("%s\nprocess: %s\ntime: %s\n\n%s\n", firstLine(cr.Summary), cr.Process, cr.Time.UTC().Format(time.RFC3339), cr.Body)
		if err := e.o.Files.WriteFile(rel, []byte(e.o.Recorder.Redact(body))); err != nil {
			e.o.Logger.DebugContext(ctx, "the crash report was not written", "error", err)
			break
		}
		a.Mobile.Crash = &domain.MobileCrash{Summary: truncateSummary(firstLine(cr.Summary)), File: rel}
		break
	}
	if !e.o.LogcatOnFailure {
		return
	}
	tail, err := dg.LogTail(dctx, pkg, logcatLines)
	if err != nil || strings.TrimSpace(tail) == "" {
		if err != nil {
			e.o.Logger.DebugContext(ctx, "no log", "error", err)
		}
		return
	}
	rel := "logcat-" + evidence() + ".txt"
	if err := e.o.Files.WriteFile(rel, []byte(e.o.Recorder.Redact(tail+"\n"))); err != nil {
		e.o.Logger.DebugContext(ctx, "the log was not written", "error", err)
		return
	}
	a.Mobile.LogcatFile = rel
}

// appGone is true when the app of the run is no longer the app in front. When the platform cannot say,
// or the answer is an error, it is false: a missing element is then no reason to look for a crash.
func (e *Engine) appGone(ctx context.Context, pkg string) bool {
	fg, ok := e.o.Platform.(Foregrounder)
	if !ok {
		return false
	}
	front, err := fg.Foreground(ctx)
	if err != nil {
		e.o.Logger.DebugContext(ctx, "no foreground", "error", err)
		return false
	}
	return front != pkg
}

// evidenceName is the name of the evidence files of a step: the step, or run for an action with no step.
// A name that was used already, because the step was retried or its flow acted again, gets a number.
func (e *Engine) evidenceName(u domain.UseCase) string {
	name := string(u.Step)
	if name == "" {
		name = "run"
	}
	name = safeName(name)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.evidence[name]++
	if n := e.evidence[name]; n > 1 {
		return fmt.Sprintf("%s-%d", name, n)
	}
	return name
}

// safeName keeps letters, digits, and - _ . so that a step name is a file name.
func safeName(s string) string {
	b := []byte(s)
	for i, ch := range b {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9', ch == '-', ch == '_':
		default:
			b[i] = '-'
		}
	}
	return string(b)
}

func firstLine(s string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(first)
}

func truncateSummary(s string) string {
	if len(s) <= maxSummary {
		return s
	}
	return strings.ToValidUTF8(s[:maxSummary], "") + "…"
}

// untilMissing is true when a check of the step wants an element that the last reading did not find.
func untilMissing(until []domain.Check, states map[string]domain.ElementState) bool {
	for _, ck := range until {
		if ck.Element == "" || (ck.Exists != nil && !*ck.Exists) || ck.Count != nil {
			continue
		}
		if st, read := states[ck.Element]; read && !st.Found {
			return true
		}
	}
	return false
}
