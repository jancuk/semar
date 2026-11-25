package mobilerec

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/jancuk/semar/internal/domain"
)

// REPL is the terminal front end of a Session: it reads a command a line, acts, and shows the screen as
// a numbered list. It has no screen of its own. The numbers are those of the frame that it showed last,
// and an action on a frame that the device has left is refused and the new frame shown.
type REPL struct {
	Session *Session
	In      io.Reader
	Out     io.Writer
	// Replace lets finish write over a flow or a scenario of the same ID.
	Replace bool
	// Verify runs what finish saved on a clean device session, and says how it ended. It is optional.
	Verify func(ctx context.Context, saved *Saved) (VerifyOutcome, error)

	frame *Frame
	// diag is what the last verify found wrong with the start, for fix.
	diag  *Diagnosis
	lines *bufio.Scanner
}

// VerifyOutcome is how a verify ended: what to tell the person, and the step that failed, if one did.
type VerifyOutcome struct {
	Message    string
	FailedStep string
}

const helpText = `Commands (N is the number of an element; N.K picks the Kth locator of loc N, in tap, long, type, secret, scroll, wait, swipe, assert):
  tap N, long N            touch an element
  type N text              type text in a field
  secret N NAME            type a secret in a field: its value is asked, and kept in memory only; the files hold env:NAME
  swipe up|down|left|right [N]   swipe the screen, or an element
  scroll N [up|down|left|right]  scroll until an element is on the screen
  press KEY                back, home, enter, search, tab, delete, hide-keyboard
  open LINK                open a link with the app
  wait [N]                 wait for the screen, or for an element
  assert N [equals|contains|exists|not_exists|visible|hidden|enabled|disabled|checked|unchecked|count] [text]   check an element; a value may be {{NAME}} of a capture
  capture N[.K] NAME [REGEX]  keep the text of an element (or the first group of REGEX) as {{NAME}} for the steps after it
  shot [FILE]              save a screenshot (capture FILE does the same)
  param S NAME [required]  turn the text that step S types into the param NAME (a flow only)
  use FLOW [name=value ...]  run a flow now, and record that the scenario uses it
  screen [Name]            name the screen that is shown, or list the names that exist
  rename-screen OLD NEW    rename a screen in the recorded steps
  common S [off]           send the element of step S to the common section, which every screen can find
  loc N                    list the locators that find element N, and how many elements each finds
  locator S [K]            list the locators of step S, or use the Kth one
  repick S N               point step S at element N of this screen (go to the step before it first)
  refresh                  read the screen again
  steps                    list the recorded steps, and where a new step goes
  delete [S]               delete step S, or the last step (the device stays where it is)
  goto S|end               take the device through the first S steps, so that a new step goes after them
  finish                   save, and verify when the command can
  fix                      after a verify that failed at the first step on another screen: reset with clear on the first launch, save, and verify again
  undo                     take back the last save, when no file changed since
  quit                     leave`

// Run reads commands until quit or the end of the input. The session must have started.
func (r *REPL) Run(ctx context.Context) error {
	r.lines = bufio.NewScanner(r.In)
	r.lines.Buffer(make([]byte, 0, 64<<10), 1<<20)
	r.frame = r.Session.Frame()
	if r.frame == nil {
		return errors.New("the session has not started")
	}
	r.say("Recording %s %s. Type help for the commands.", r.Session.Kind(), r.Session.ID())
	r.show(r.frame)
	for {
		fmt.Fprint(r.Out, "> ")
		line, ok := r.readLine()
		if !ok {
			return nil
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if quit := r.command(ctx, line); quit {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

func (r *REPL) readLine() (string, bool) {
	if !r.lines.Scan() {
		return "", false
	}
	return r.lines.Text(), true
}

func (r *REPL) say(format string, args ...any) {
	fmt.Fprintf(r.Out, format+"\n", args...)
}

// show prints the frame: its screen and its elements, numbered from 1.
func (r *REPL) show(f *Frame) {
	r.frame = f
	if f.Notice != "" {
		r.say("Note: %s", f.Notice)
	}
	r.say("Screen %s (frame %d)%s", f.Screen, f.ID, map[bool]string{true: ", a secret is shown: its text is hidden"}[f.Secret])
	for _, e := range f.Elements {
		r.say("%3d  %s", e.Index+1, describe(e))
	}
}

func describe(e Element) string {
	kind := e.Role
	if kind == "" {
		kind = e.Class[strings.LastIndex(e.Class, ".")+1:]
	}
	label := firstNonEmpty(e.Text, e.Desc, e.Hint, shortID(e.ID))
	extra := ""
	switch {
	case e.Password:
		extra = " (password)"
	case e.Editable:
		extra = " (field)"
	}
	loc := ""
	if e.Stability != "" {
		loc = " [" + e.Stability + "]"
	}
	return fmt.Sprintf("%-9s %s%s%s", kind, label, extra, loc)
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// elementK reads N or N.K: the number of an element, and the locator to use for it. K is 0 when it is left
// out, which is the best locator.
func (r *REPL) elementK(arg string) (*Target, int, error) {
	num, k := arg, 0
	if a, b, ok := strings.Cut(arg, "."); ok {
		n, err := strconv.Atoi(b)
		if err != nil || n < 1 {
			return nil, 0, fmt.Errorf("%q is not a locator number: write N.K with K from 1, as in tap 3.2", b)
		}
		num, k = a, n
	}
	t, err := r.element(num)
	return t, k, err
}

// element reads the number of an element of the shown frame.
func (r *REPL) element(arg string) (*Target, error) {
	n, err := strconv.Atoi(arg)
	if err != nil || n < 1 || n > len(r.frame.Elements) {
		return nil, fmt.Errorf("%q is not the number of an element: pick one from 1 to %d", arg, len(r.frame.Elements))
	}
	return &Target{Index: n - 1}, nil
}

// command runs one line, and says whether the REPL ends.
func (r *REPL) command(ctx context.Context, line string) (quit bool) {
	fields := strings.Fields(line)
	cmd, args := strings.ToLower(fields[0]), fields[1:]
	rest := func(n int) string { // the line after its first n words, with its spaces
		parts := strings.SplitN(strings.TrimSpace(line), " ", n+1)
		if len(parts) <= n {
			return ""
		}
		return strings.TrimSpace(parts[n])
	}
	done := func(f *Frame, err error) {
		if err != nil {
			r.fail(err)
			return
		}
		r.show(f)
	}
	switch cmd {
	case "help", "?":
		r.say("%s", helpText)
	case "list", "ls":
		r.show(r.frame)
	case "quit", "exit", "q":
		return true
	case "refresh":
		done(r.Session.Refresh(ctx))
	case "tap", "long", "wait", "scroll", "swipe":
		r.element0(ctx, cmd, args)
	case "type":
		if len(args) < 2 {
			r.say("Usage: type N text")
			return false
		}
		t, k, err := r.elementK(args[0])
		if err != nil {
			r.fail(err)
			return false
		}
		done(r.Session.Act(ctx, Act{Do: DoType, Frame: r.frame.ID, Target: t, Value: rest(2), Candidate: k}))
	case "secret":
		if len(args) != 2 {
			r.say("Usage: secret N NAME")
			return false
		}
		t, k, err := r.elementK(args[0])
		if err != nil {
			r.fail(err)
			return false
		}
		fmt.Fprintf(r.Out, "Value of %s (typed in the open, kept in memory for this session only): ", args[1])
		value, ok := r.readLine()
		if !ok {
			return true
		}
		done(r.Session.Act(ctx, Act{Do: DoType, Frame: r.frame.ID, Target: t, Value: strings.TrimRight(value, "\r\n"), Secret: args[1], Candidate: k}))
	case "press":
		if len(args) != 1 {
			r.say("Usage: press KEY")
			return false
		}
		done(r.Session.Act(ctx, Act{Do: DoPress, Frame: r.frame.ID, Value: args[0]}))
	case "open":
		if len(args) != 1 {
			r.say("Usage: open LINK")
			return false
		}
		done(r.Session.Act(ctx, Act{Do: DoOpen, Frame: r.frame.ID, Value: args[0]}))
	case "assert":
		r.assert(ctx, args)
	case "capture":
		if len(args) > 0 && isElementArg(args[0]) {
			r.captureValue(ctx, args, rest(3))
		} else {
			r.shot(ctx, args)
		}
	case "shot":
		r.shot(ctx, args)
	case "param":
		r.param(args)
	case "use":
		r.useFlow(ctx, args)
	case "screen":
		switch len(args) {
		case 0:
			r.say("Screen names (the shown screen is %s):", r.frame.Screen)
			for _, n := range r.Session.ScreenNames() {
				r.say("  %s", n)
			}
		case 1:
			done(r.Session.SetScreen(args[0]))
		default:
			r.say("Usage: screen [Name]")
		}
	case "rename-screen":
		if len(args) != 2 {
			r.say("Usage: rename-screen OLD NEW")
			return false
		}
		f, err := r.Session.RenameScreen(args[0], args[1])
		if err != nil {
			r.fail(err)
			return false
		}
		r.say("Renamed %s to %s in the recorded steps.", args[0], args[1])
		if f != nil {
			r.show(f)
		}
	case "common":
		r.common(args)
	case "loc":
		r.loc(ctx, args)
	case "locator":
		r.locator(args)
	case "repick":
		r.repick(ctx, args)
	case "goto":
		r.gotoStep(ctx, args)
	case "steps":
		r.steps()
	case "delete":
		r.delete(args)
	case "finish":
		r.finish(ctx, r.Replace)
	case "fix":
		r.fix(ctx)
	case "undo":
		if err := r.Session.Undo(); err != nil {
			r.fail(err)
			return false
		}
		r.say("Undone: the files are as they were before the save.")
	default:
		r.say("Unknown command %q. Type help.", cmd)
	}
	return false
}

// element0 runs the commands that act on an element or, for some, on none.
func (r *REPL) element0(ctx context.Context, cmd string, args []string) {
	var do string
	switch cmd {
	case "tap":
		do = DoTap
	case "long":
		do = DoLongPress
	case "wait":
		do = DoWait
	case "scroll":
		do = DoScroll
	default:
		do = DoSwipe
	}
	a := Act{Do: do, Frame: r.frame.ID}
	switch cmd {
	case "swipe":
		if len(args) < 1 || len(args) > 2 || !isDirection(args[0]) {
			r.say("Usage: swipe up|down|left|right [N]")
			return
		}
		a.Value = args[0]
		if len(args) == 2 {
			t, k, err := r.elementK(args[1])
			if err != nil {
				r.fail(err)
				return
			}
			a.Target, a.Candidate = t, k
		}
	case "wait":
		if len(args) > 1 {
			r.say("Usage: wait [N]")
			return
		}
		if len(args) == 1 {
			t, k, err := r.elementK(args[0])
			if err != nil {
				r.fail(err)
				return
			}
			a.Target, a.Candidate = t, k
		}
	default:
		if len(args) < 1 || len(args) > 2 || (len(args) == 2 && cmd != "scroll") {
			r.say("Usage: %s N", cmd)
			return
		}
		t, k, err := r.elementK(args[0])
		if err != nil {
			r.fail(err)
			return
		}
		a.Target, a.Candidate = t, k
		if len(args) == 2 {
			if !isDirection(args[1]) {
				r.say("Usage: scroll N [up|down|left|right]")
				return
			}
			a.Value = args[1]
		}
	}
	f, err := r.Session.Act(ctx, a)
	if err != nil {
		r.fail(err)
		return
	}
	r.show(f)
}

func isDirection(s string) bool {
	for _, d := range domain.MobileDirections {
		if s == d {
			return true
		}
	}
	return false
}

func (r *REPL) assert(ctx context.Context, args []string) {
	if len(args) < 1 {
		r.say("Usage: assert N [equals|contains|exists] [text]")
		return
	}
	t, k, err := r.elementK(args[0])
	if err != nil {
		r.fail(err)
		return
	}
	a := Assert{Frame: r.frame.ID, Target: *t, Candidate: k}
	if len(args) > 1 {
		switch k := AssertKind(args[1]); {
		case k.Known() && k != AssertAttribute:
			a.Kind = k
			a.Value = strings.Join(args[2:], " ")
		default:
			a.Value = strings.Join(args[1:], " ")
		}
	}
	f, err := r.Session.Assert(ctx, a)
	if err != nil {
		r.fail(err)
		return
	}
	steps := r.Session.Steps()
	r.say("Check recorded: %s", steps[max(r.Session.Cursor(), 1)-1].Text)
	if f.Notice != "" {
		r.say("Note: %s", f.Notice)
	}
	r.frame = f
}

// isElementArg says whether an argument is the number of an element, N or N.K, and not a file name.
func isElementArg(arg string) bool {
	a, b, dotted := strings.Cut(arg, ".")
	if _, err := strconv.Atoi(a); err != nil {
		return false
	}
	if dotted {
		_, err := strconv.Atoi(b)
		return err == nil
	}
	return true
}

func (r *REPL) captureValue(ctx context.Context, args []string, pattern string) {
	if len(args) < 2 {
		r.say("Usage: capture N[.K] NAME [REGEX]")
		return
	}
	t, k, err := r.elementK(args[0])
	if err != nil {
		r.fail(err)
		return
	}
	f, value, err := r.Session.CaptureValue(ctx, CaptureReq{Frame: r.frame.ID, Target: *t, Candidate: k, Name: args[1], Pattern: pattern})
	if err != nil {
		r.fail(err)
		return
	}
	r.say("Captured %s = %s. Write {{%s}} in a later text or check.", args[1], quote(value), args[1])
	r.frame = f
}

func (r *REPL) param(args []string) {
	if len(args) < 2 || len(args) > 3 || len(args) == 3 && args[2] != "required" {
		r.say("Usage: param S NAME [required]")
		return
	}
	n, err := strconv.Atoi(args[0])
	if err != nil {
		r.say("Usage: param S NAME [required]: %q is not a step number", args[0])
		return
	}
	note, err := r.Session.MakeParam(n, args[1], len(args) == 3)
	if err != nil {
		r.fail(err)
		return
	}
	r.say("Step %d now types {{%s}}.", n, args[1])
	if note != "" {
		r.say("Note: %s.", note)
	}
}

func (r *REPL) shot(ctx context.Context, args []string) {
	path := "capture.png"
	if len(args) > 0 {
		path = args[0]
	}
	png, err := r.Session.Capture(ctx)
	if err != nil {
		r.fail(err)
		return
	}
	if err := os.WriteFile(path, png, 0o600); err != nil {
		r.fail(err)
		return
	}
	r.say("Saved a screenshot of %d bytes to %s.", len(png), path)
}

func (r *REPL) useFlow(ctx context.Context, args []string) {
	if len(args) < 1 {
		r.say("Usage: use FLOW [name=value ...]")
		return
	}
	with := map[string]string{}
	for _, kv := range args[1:] {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			r.say("Usage: use FLOW [name=value ...]: %q is not name=value", kv)
			return
		}
		with[k] = v
	}
	f, res, err := r.Session.UseFlow(ctx, args[0], with)
	if err != nil {
		r.fail(err)
		return
	}
	r.say("The flow %s %s, and the scenario now uses it.", args[0], res.Status)
	r.show(f)
}

// fix sets the reset of the first launch to clear, and saves and verifies again over the files of the last
// save, which is what the diagnosis of the verify asked for.
func (r *REPL) fix(ctx context.Context) {
	if r.diag == nil {
		r.say("There is nothing to fix: a verify has not found the first step after the launch failing on another screen.")
		return
	}
	err := r.Session.ClearFirstLaunch()
	if errors.Is(err, ErrNeedsConfirm) {
		r.say("The first launch must reset with clear, which deletes the data of the app on %s.", r.Session.Device())
		if answer, _ := r.ask("Type yes to allow it: "); strings.EqualFold(strings.TrimSpace(answer), "yes") {
			r.Session.ConfirmClear()
			err = r.Session.ClearFirstLaunch()
		}
	}
	if err != nil {
		r.fail(err)
		return
	}
	r.finish(ctx, true)
}

func (r *REPL) finish(ctx context.Context, replace bool) {
	r.diag = nil
	sv, err := r.Session.Save(SaveOptions{Replace: replace})
	if errors.Is(err, ErrNeedsConfirm) {
		r.say("A replay of this sign-in must start with reset: clear, which deletes the data of the app on %s.", r.Session.Device())
		fmt.Fprint(r.Out, "Type yes to allow it: ")
		answer, _ := r.readLine()
		if strings.EqualFold(strings.TrimSpace(answer), "yes") {
			r.Session.ConfirmClear()
			sv, err = r.Session.Save(SaveOptions{Replace: replace})
		}
	}
	if err != nil {
		r.fail(err)
		return
	}
	r.say("Saved:")
	for _, f := range sv.Files {
		r.say("  %s", f)
	}
	for _, n := range sv.Draft.Notes {
		r.say("Note: %s.", n)
	}
	if r.Verify == nil {
		r.say("Next: undo takes the save back, quit leaves.")
		return
	}
	r.say("Verifying on a clean device session...")
	out, err := r.Verify(ctx, sv)
	if err != nil {
		r.fail(err)
	} else {
		r.say("%s", out.Message)
		r.diag = r.Session.DiagnoseReplay(out.FailedStep)
	}
	if r.diag != nil {
		r.say("%s", r.diag.Message)
		r.say("Next: fix does that and verifies again, undo takes the save back, quit leaves.")
	} else {
		r.say("Next: undo takes the save back, quit leaves.")
	}
	if f := r.Session.Frame(); f != nil {
		r.frame = f
	}
}

func (r *REPL) fail(err error) {
	var stale *StaleError
	if errors.As(err, &stale) {
		r.say("The screen changed, so nothing was done. This is the screen now:")
		r.show(stale.Frame)
		return
	}
	r.say("Error: %s", r.Session.ov.Scrub(err.Error()))
}

// steps lists the steps, and marks where a new step goes.
func (r *REPL) steps() {
	steps := r.Session.Steps()
	if len(steps) == 0 {
		r.say("No steps yet.")
		return
	}
	cur := r.Session.Cursor()
	for i, st := range steps {
		if i == cur && cur < len(steps) {
			r.say("     --- recording goes here: the steps below are after the device (goto N moves it) ---")
		}
		extra := ""
		if st.Locator != "" {
			extra = "  " + st.Locator
			if st.Shared {
				extra += " (common)"
			}
		}
		r.say("%3d  %-8s %s%s", st.N, st.Kind, st.Text, extra)
	}
	if cur == len(steps) {
		r.say("     --- recording goes here ---")
	}
}

func (r *REPL) delete(args []string) {
	if len(args) > 1 {
		r.say("Usage: delete [S]")
		return
	}
	n := len(r.Session.Steps())
	if len(args) == 1 {
		var err error
		if n, err = strconv.Atoi(args[0]); err != nil {
			r.say("Usage: delete [S]: %q is not a step number", args[0])
			return
		}
	}
	if err := r.Session.DeleteStep(n); err != nil {
		r.fail(err)
		return
	}
	if r.Session.Dirty() {
		r.say("Deleted step %d. The device still shows what it did: goto N takes the device through the steps that are left.", n)
		return
	}
	r.say("Deleted step %d.", n)
}

// ask reads one line for a value, or says that the input ended.
func (r *REPL) ask(prompt string) (string, bool) {
	fmt.Fprint(r.Out, prompt)
	v, ok := r.readLine()
	return strings.TrimRight(v, "\r\n"), ok
}

func (r *REPL) gotoStep(ctx context.Context, args []string) {
	steps := r.Session.Steps()
	if len(args) != 1 {
		r.say("Usage: goto S|end")
		return
	}
	n := len(steps)
	if args[0] != "end" {
		var err error
		if n, err = strconv.Atoi(args[0]); err != nil {
			r.say("Usage: goto S|end: %q is not a step number", args[0])
			return
		}
	}
	if n < 1 || n > len(steps) {
		r.say("There is no step %d: the recording has %d.", n, len(steps))
		return
	}
	// The values that the steps need and that no file holds are asked first, and kept in memory only.
	for _, name := range r.Session.MissingSecrets(n) {
		v, ok := r.ask(fmt.Sprintf("Value of the secret %s (typed in the open, kept in memory for this session only): ", name))
		if !ok {
			return
		}
		if err := r.Session.SetSecret(name, v); err != nil {
			r.fail(err)
			return
		}
	}
	for _, name := range r.Session.MissingParams(n) {
		v, ok := r.ask(fmt.Sprintf("Value of the param %s: ", name))
		if !ok {
			return
		}
		if err := r.Session.SetParam(name, v); err != nil {
			r.fail(err)
			return
		}
	}
	f, err := r.Session.Goto(ctx, n)
	if errors.Is(err, ErrNeedsConfirm) {
		r.say("The steps reset with clear, which deletes the data of the app on %s.", r.Session.Device())
		if answer, _ := r.ask("Type yes to allow it: "); strings.EqualFold(strings.TrimSpace(answer), "yes") {
			r.Session.ConfirmClear()
			f, err = r.Session.Goto(ctx, n)
		}
	}
	if err != nil {
		r.fail(err)
		if f != nil {
			r.say("The device stopped after step %d.", r.Session.Cursor())
			r.show(f)
		}
		return
	}
	r.say("The device is after step %d. A new step goes there.", n)
	r.show(f)
}

// loc lists the locators of an element of the frame. A field is read for typing, anything else for a tap.
func (r *REPL) loc(ctx context.Context, args []string) {
	if len(args) < 1 || len(args) > 2 {
		r.say("Usage: loc N [tap|type|assert]")
		return
	}
	t, err := r.element(args[0])
	if err != nil {
		r.fail(err)
		return
	}
	do := DoTap
	if r.frame.Elements[t.Index].Editable {
		do = DoType
	}
	if len(args) == 2 {
		do = args[1]
	}
	list, err := r.Session.Candidates(ctx, r.frame.ID, *t, do)
	if err != nil {
		r.fail(err)
		return
	}
	if len(list.Usable) == 0 {
		r.say("No locator finds element %s alone.", args[0])
	}
	for _, c := range list.Usable {
		r.say("%3d  %s", c.K, describeLocator(c))
	}
	for _, c := range list.Refused {
		r.say("  -  %s  %s", c.Locator, c.Note)
	}
	if len(list.Usable) > 0 {
		r.say("Pick one by its number: tap %s.K", args[0])
	}
}

func describeLocator(c LocatorInfo) string {
	out := c.Locator
	if c.Matches > 0 && !c.Explicit {
		out += fmt.Sprintf("  %d matches", c.Matches)
	} else if c.Matches == 0 {
		out += "  1 match"
	}
	if c.Stability != "" {
		out += " [" + c.Stability + "]"
	}
	if c.Note != "" {
		out += " " + c.Note
	}
	if c.Explicit {
		out += " (used only when you pick it)"
	}
	if c.Current {
		out += " (in use)"
	}
	return out
}

func (r *REPL) locator(args []string) {
	if len(args) < 1 || len(args) > 2 {
		r.say("Usage: locator S [K]")
		return
	}
	n, err := strconv.Atoi(args[0])
	if err != nil {
		r.say("Usage: locator S [K]: %q is not a step number", args[0])
		return
	}
	if len(args) == 2 {
		k, err := strconv.Atoi(args[1])
		if err != nil {
			r.say("Usage: locator S [K]: %q is not a locator number", args[1])
			return
		}
		if err := r.Session.SetLocator(n, k); err != nil {
			r.fail(err)
			return
		}
		r.say("Step %d now uses locator %d. The screens file gets a new element for it when you finish.", n, k)
		return
	}
	list, err := r.Session.Locators(n)
	if err != nil {
		r.fail(err)
		return
	}
	for _, c := range list {
		r.say("%3d  %s", c.K, describeLocator(c))
	}
}

func (r *REPL) repick(ctx context.Context, args []string) {
	if len(args) != 2 {
		r.say("Usage: repick S N")
		return
	}
	n, err := strconv.Atoi(args[0])
	if err != nil {
		r.say("Usage: repick S N: %q is not a step number", args[0])
		return
	}
	t, k, err := r.elementK(args[1])
	if err != nil {
		r.fail(err)
		return
	}
	f, err := r.Session.Repick(ctx, n, r.frame.ID, *t, k)
	if err != nil {
		r.fail(err)
		return
	}
	r.say("Step %d now points at element %s.", n, args[1])
	r.show(f)
}

func (r *REPL) common(args []string) {
	if len(args) < 1 || len(args) > 2 || len(args) == 2 && args[1] != "off" {
		r.say("Usage: common S [off]")
		return
	}
	n, err := strconv.Atoi(args[0])
	if err != nil {
		r.say("Usage: common S [off]: %q is not a step number", args[0])
		return
	}
	on := len(args) == 1
	if err := r.Session.EditStep(n, Edit{Common: &on}); err != nil {
		r.fail(err)
		return
	}
	if on {
		r.say("The element of step %d goes to the common section when you finish.", n)
	} else {
		r.say("The element of step %d goes back to its screen.", n)
	}
}
