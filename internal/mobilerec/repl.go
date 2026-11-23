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
	Verify func(ctx context.Context, saved *Saved) (string, error)

	frame *Frame
	lines *bufio.Scanner
}

const helpText = `Commands (N is the number of an element):
  tap N, long N            touch an element
  type N text              type text in a field
  secret N NAME            type a secret in a field: its value is asked, and kept in memory only; the files hold env:NAME
  swipe up|down|left|right [N]   swipe the screen, or an element
  scroll N [up|down|left|right]  scroll until an element is on the screen
  press KEY                back, home, enter, search, tab, delete, hide-keyboard
  open LINK                open a link with the app
  wait [N]                 wait for the screen, or for an element
  assert N [equals|contains|exists|not_exists|visible|hidden|enabled|disabled|checked|unchecked|count] [text]   check an element (a scenario only)
  capture [FILE]           save a screenshot
  use FLOW [name=value ...]  run a flow now, and record that the scenario uses it
  screen Name              name the screen that is shown
  refresh                  read the screen again
  steps                    list the recorded steps
  delete                   delete the last step (the device stays where it is)
  finish                   save, and verify when the command can
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
		t, err := r.element(args[0])
		if err != nil {
			r.fail(err)
			return false
		}
		done(r.Session.Act(ctx, Act{Do: DoType, Frame: r.frame.ID, Target: t, Value: rest(2)}))
	case "secret":
		if len(args) != 2 {
			r.say("Usage: secret N NAME")
			return false
		}
		t, err := r.element(args[0])
		if err != nil {
			r.fail(err)
			return false
		}
		fmt.Fprintf(r.Out, "Value of %s (typed in the open, kept in memory for this session only): ", args[1])
		value, ok := r.readLine()
		if !ok {
			return true
		}
		done(r.Session.Act(ctx, Act{Do: DoType, Frame: r.frame.ID, Target: t, Value: strings.TrimRight(value, "\r\n"), Secret: args[1]}))
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
		r.capture(ctx, args)
	case "use":
		r.useFlow(ctx, args)
	case "screen":
		if len(args) != 1 {
			r.say("Usage: screen Name")
			return false
		}
		done(r.Session.SetScreen(args[0]))
	case "steps":
		steps := r.Session.Steps()
		if len(steps) == 0 {
			r.say("No steps yet.")
		}
		for _, st := range steps {
			r.say("%3d  %-8s %s", st.N, st.Kind, st.Text)
		}
	case "delete":
		if err := r.Session.DeleteLastStep(); err != nil {
			r.fail(err)
			return false
		}
		r.say("Deleted the last step. The device stays where it is.")
	case "finish":
		r.finish(ctx)
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
			t, err := r.element(args[1])
			if err != nil {
				r.fail(err)
				return
			}
			a.Target = t
		}
	case "wait":
		if len(args) > 1 {
			r.say("Usage: wait [N]")
			return
		}
		if len(args) == 1 {
			t, err := r.element(args[0])
			if err != nil {
				r.fail(err)
				return
			}
			a.Target = t
		}
	default:
		if len(args) < 1 || len(args) > 2 || (len(args) == 2 && cmd != "scroll") {
			r.say("Usage: %s N", cmd)
			return
		}
		t, err := r.element(args[0])
		if err != nil {
			r.fail(err)
			return
		}
		a.Target = t
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
	t, err := r.element(args[0])
	if err != nil {
		r.fail(err)
		return
	}
	a := Assert{Frame: r.frame.ID, Target: *t}
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
	r.say("Check recorded: %s", steps[len(steps)-1].Text)
	if f.Notice != "" {
		r.say("Note: %s", f.Notice)
	}
	r.frame = f
}

func (r *REPL) capture(ctx context.Context, args []string) {
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

func (r *REPL) finish(ctx context.Context) {
	sv, err := r.Session.Save(SaveOptions{Replace: r.Replace})
	if errors.Is(err, ErrNeedsConfirm) {
		r.say("A replay of this sign-in must start with reset: clear, which deletes the data of the app on %s.", r.Session.Device())
		fmt.Fprint(r.Out, "Type yes to allow it: ")
		answer, _ := r.readLine()
		if strings.EqualFold(strings.TrimSpace(answer), "yes") {
			r.Session.ConfirmClear()
			sv, err = r.Session.Save(SaveOptions{Replace: r.Replace})
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
	msg, err := r.Verify(ctx, sv)
	if err != nil {
		r.fail(err)
	} else {
		r.say("%s", msg)
	}
	r.say("Next: undo takes the save back, quit leaves.")
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
