package screens

import (
	"fmt"
	"strings"

	"github.com/jancuk/semar/internal/domain"
)

// Use is one step that reads a screen, as the screens file sees it: where it is, which surface runs
// it, the screen it acts on, and every element that the action, the checks, and the captures name.
type Use struct {
	// Where names the step in a message, such as "step read" or "leg web_read, step read".
	Where string
	// Surface is the surface of the step. Empty means web.
	Surface domain.Surface
	// Screen is the screen of the action. Empty when the action names none.
	Screen string
	// Elements are the elements to find on Screen, with those of the action first.
	Elements []string
}

// WebUse is the use of a web step: the elements of its action, then those of its checks and captures.
func WebUse(where string, w domain.WebAction, others []string) Use {
	return Use{Where: where, Surface: domain.SurfaceWeb, Screen: w.Screen, Elements: append(w.Elements(), others...)}
}

// MobileUse is the use of a mobile step, built as WebUse is.
func MobileUse(where string, m domain.MobileAction, others []string) Use {
	return Use{Where: where, Surface: domain.SurfaceAndroid, Screen: m.Screen, Elements: append(m.Elements(), others...)}
}

// Check says, in plain words, which screens and elements of the uses the screens file does not have.
func Check(s domain.Screens, uses []Use) []string {
	var out []string
	for _, u := range uses {
		if u.Screen == "" {
			if len(u.Elements) > 0 {
				out = append(out, say(u.Where, " ", "names element %s but has no screen", u.Elements[0]))
			}
			continue
		}
		elements, ok := s.Screens[u.Screen]
		if !ok {
			out = append(out, say(u.Where, ": ", "screen %s is not in the screens of %s; its screens are %s",
				u.Screen, s.App, list(s.ScreenNames())))
			continue
		}
		seen := map[string]bool{}
		var present []string
		for _, name := range u.Elements {
			if seen[name] {
				continue
			}
			seen[name] = true
			if _, ok := elements[name]; !ok {
				out = append(out, say(u.Where, ": ", "element %s is not on screen %s of %s; its elements are %s",
					name, u.Screen, s.App, list(s.ElementNames(u.Screen))))
				continue
			}
			present = append(present, name)
		}
		for _, name := range Needed(s, u.Screen, present) {
			l := elements[name]
			switch {
			case u.Surface == domain.SurfaceAndroid && !l.ForMobile():
				out = append(out, say(u.Where, ": ", "element %s on screen %s of %s cannot be found on a device: give it id, desc, role, name, text, xpath, or class",
					name, u.Screen, s.App))
			case u.Surface != domain.SurfaceAndroid && !l.ForWeb():
				out = append(out, say(u.Where, ": ", "element %s on screen %s of %s cannot be found in a browser: give it testId, role, name, text, or css",
					name, u.Screen, s.App))
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
