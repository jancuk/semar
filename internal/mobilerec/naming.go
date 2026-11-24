package mobilerec

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/engine/mobile"
	"github.com/jancuk/semar/internal/record"
	"github.com/jancuk/semar/internal/screens"
)

const (
	// guessMinHits and guessMinPercent say how much of a screen of the screens file must be on the tree
	// for the tree to be that screen.
	guessMinHits    = 2
	guessMinPercent = 60
)

// screenName is the name of the screen of a tree: the one that the person gave, else the name that the
// person gave to a screen with the same heading, else the screen of the screens file that the tree
// looks like, else the heading of the screen, else Screen1.
func (s *Session) screenName(t *mobile.Tree) string {
	if s.screen != "" {
		return s.screen
	}
	derived := derivedName(t)
	if a, ok := s.aliases[derived]; ok && derived != "Screen1" {
		return a
	}
	if g := s.guessScreen(t); g != "" {
		return g
	}
	return derived
}

// derivedName is the name that the heading of a screen gives it.
func derivedName(t *mobile.Tree) string {
	if h := record.Slug(ScreenHeading(t)); h != "" {
		return titleName(h)
	}
	return "Screen1"
}

// knownScreens is the screens file of the app as it is on disk, read once until the next save.
func (s *Session) knownScreens() domain.Screens {
	if s.known == nil {
		scr, err := screens.Load(s.o.Repo, s.o.App)
		if err != nil {
			scr = domain.Screens{}
		}
		s.known = &scr
	}
	return *s.known
}

func (s *Session) guessScreen(t *mobile.Tree) string {
	if t == nil || t.Digest == "" {
		return ""
	}
	if s.guessTree == t.Digest {
		return s.guessName
	}
	s.guessTree, s.guessName = t.Digest, fingerprint(s.knownScreens(), t)
	return s.guessName
}

// fingerprint is the screen of the file whose elements are found, each alone, on the tree: at least two of
// them, and at least 60 percent of its elements that hold no variable. The one with the greatest share
// wins, then the one with the most elements found, then the first by name, so the guess is always the same.
func fingerprint(scr domain.Screens, t *mobile.Tree) string {
	best, bestHits, bestTotal := "", 0, 1
	for _, name := range slices.Sorted(maps.Keys(scr.Screens)) {
		hits, total := 0, 0
		for el := range scr.Screens[name] {
			locs, ok := locsOf(scr, name, el)
			if !ok {
				continue
			}
			total++
			if st, node, err := mobile.Resolve(t, locs, el); err == nil && st.Found && node != nil {
				hits++
			}
		}
		if hits < guessMinHits || hits*100 < guessMinPercent*total {
			continue
		}
		if best == "" || hits*bestTotal > bestHits*total || hits*bestTotal == bestHits*total && hits > bestHits {
			best, bestHits, bestTotal = name, hits, total
		}
	}
	return best
}

// locsOf gives the locators that an element needs, with the elements that its within chain names. It
// gives false when a locator is missing or holds a variable, which the recorder cannot fill.
func locsOf(scr domain.Screens, screen, name string) (map[string]domain.Locator, bool) {
	locs := map[string]domain.Locator{}
	for n, depth := name, 0; n != ""; depth++ {
		l, ok := scr.Locator(screen, n)
		if !ok || depth > 8 {
			return nil, false
		}
		for _, v := range l.Fields() {
			if strings.Contains(v, "{{") {
				return nil, false
			}
		}
		locs[n] = l
		n = l.Within
	}
	return locs, true
}

// SetScreen names the screen that the frame shows. The name holds until an action leaves the screen, and
// a later screen with the same heading gets it too.
func (s *Session) SetScreen(name string) (*Frame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkName(name); err != nil {
		return nil, err
	}
	s.screen = name
	if s.tree == nil {
		return nil, errors.New("there is no screen yet")
	}
	if d := derivedName(s.tree); d != "Screen1" && d != name {
		s.aliases[d] = name
	}
	return s.setFrame(s.tree), nil
}

// RenameScreen renames the screen of every recorded step that is on old, and makes a later screen that
// would have been called old, or what old was an alias for, get the new name. Elements that the screens
// file holds under the old name stay there: the steps get entries under the new name when they are saved.
func (s *Session) RenameScreen(old, name string) (*Frame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkName(old); err != nil {
		return nil, err
	}
	if err := checkName(name); err != nil {
		return nil, err
	}
	if old == name {
		return nil, errors.New("the screen has that name already")
	}
	// Nothing changes before the name is known to be one of a screen of the recording.
	known := slices.ContainsFunc(s.events, func(ev *event) bool { return ev.screen == old })
	for _, v := range s.aliases {
		known = known || v == old
	}
	if !known && s.frame != nil && s.frame.Screen != old {
		return nil, fmt.Errorf("no recorded step and no frame is on a screen called %s", old)
	}
	for _, ev := range s.events {
		if ev.screen == old {
			ev.screen, ev.name, ev.containerName = name, "", ""
		}
	}
	for k, v := range s.aliases {
		if v == old {
			s.aliases[k] = name
		}
	}
	if old != "Screen1" {
		s.aliases[old] = name
	}
	if s.screen == old {
		s.screen = name
	}
	if s.tree == nil {
		return s.frame, nil
	}
	return s.setFrame(s.tree), nil
}

// ScreenNames lists the names that a screen can have: the screens of the screens file, the names that the
// person gave, and the screens of the recorded steps.
func (s *Session) ScreenNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := map[string]bool{}
	for n := range s.knownScreens().Screens {
		set[n] = true
	}
	for _, v := range s.aliases {
		set[v] = true
	}
	for _, ev := range s.events {
		if ev.screen != "" {
			set[ev.screen] = true
		}
	}
	if s.frame != nil && s.frame.Screen != "" {
		set[s.frame.Screen] = true
	}
	return slices.Sorted(maps.Keys(set))
}

// CommonCount is how many elements the common section of the screens file holds, for the studio.
func (s *Session) CommonCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.knownScreens().Common)
}
