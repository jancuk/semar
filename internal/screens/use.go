package screens

import (
	"fmt"
	"strings"

	"github.com/jancuk/semar/internal/domain"
)

// Use is one web step as the screens file sees it: where it is, its action, and the elements that its
// checks and captures name. Those elements are read from the screen of the action.
type Use struct {
	// Where names the step in a message, such as "step read" or "leg web_read, step read".
	Where    string
	Action   domain.WebAction
	Elements []string
}

// Check says, in plain words, which screens and elements of the uses the screens file does not have.
func Check(s domain.Screens, uses []Use) []string {
	var out []string
	for _, u := range uses {
		named := append(u.Action.Elements(), u.Elements...)
		if u.Action.Screen == "" {
			if len(u.Elements) > 0 {
				out = append(out, say(u.Where, " ", "names element %s but has no screen", u.Elements[0]))
			}
			continue
		}
		elements, ok := s.Screens[u.Action.Screen]
		if !ok {
			out = append(out, say(u.Where, ": ", "screen %s is not in the screens of %s; its screens are %s",
				u.Action.Screen, s.App, list(s.ScreenNames())))
			continue
		}
		seen := map[string]bool{}
		for _, name := range named {
			if seen[name] {
				continue
			}
			seen[name] = true
			if _, ok := elements[name]; !ok {
				out = append(out, say(u.Where, ": ", "element %s is not on screen %s of %s; its elements are %s",
					name, u.Action.Screen, s.App, list(s.ElementNames(u.Action.Screen))))
			}
		}
	}
	return out
}

// say puts where in front of the message, with the joint between them, and leaves both out when the
// caller has no place to name.
func say(where, joint, format string, args ...any) string {
	msg := fmt.Sprintf(format, args...)
	if where == "" {
		return msg
	}
	return where + joint + msg
}

func list(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// Needed lists the elements of a screen that the engine must resolve to act on the named elements,
// with the elements that their within chains name, each once and in order.
func Needed(s domain.Screens, screen string, names []string) []string {
	var out []string
	var add func(name string, depth int)
	add = func(name string, depth int) {
		l, ok := s.Locator(screen, name)
		if !ok || depth > 64 {
			return
		}
		for _, have := range out {
			if have == name {
				return
			}
		}
		out = append(out, name)
		if l.Within != "" {
			add(l.Within, depth+1)
		}
	}
	for _, n := range names {
		add(n, 0)
	}
	return out
}
