package record

import (
	"fmt"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
)

const maxName = 40

// Slug turns what an element is called by into the name that a screens file takes: lower case, with
// hyphens, and short. It gives "" when the text has no letter or digit.
func Slug(hint string) string {
	return words(strings.ToLower(hint), maxName)
}

// words joins the runs of ASCII letters and digits with hyphens and cuts the result at a hyphen when
// it is longer than limit.
func words(s string, limit int) string {
	var parts []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			parts = append(parts, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	out := ""
	for _, p := range parts {
		next := p
		if out != "" {
			next = out + "-" + p
		}
		if len(next) > limit {
			if out == "" {
				out = p[:limit]
			}
			break
		}
		out = next
	}
	return out
}

// Namer gives the elements of one screen their names. An element that the screen has already, under
// any name, keeps that name. Two elements that would be called the same get a number.
type Namer struct {
	taken map[string]domain.Locator
}

// NewNamer starts with the elements that the screen has.
func NewNamer(existing map[string]domain.Locator) *Namer {
	n := &Namer{taken: map[string]domain.Locator{}}
	for name, loc := range existing {
		n.taken[name] = loc
	}
	return n
}

// Name gives the name of the element that the locator finds.
func (n *Namer) Name(hint string, loc domain.Locator) string {
	names := make([]string, 0, len(n.taken))
	for name := range n.taken {
		names = append(names, name)
	}
	slices.Sort(names)
	base := Slug(hint)
	if base == "" {
		base = "element"
	}
	if have, ok := n.taken[base]; ok && have == loc {
		return base
	}
	for _, name := range names {
		if n.taken[name] == loc {
			return name
		}
	}
	name := base
	for i := 2; ; i++ {
		if _, used := n.taken[name]; !used {
			break
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
	n.taken[name] = loc
	return name
}

// ScreenName names a screen after the title of the page, or after its path when there is no title.
// drop lists the values that must not be in the name, such as the ID of a fixture: they are cut out
// of the title and of the path, so that two recordings of the same page give the same name.
func ScreenName(title, path string, drop []string) string {
	cut := func(s string) string {
		for _, d := range drop {
			if d != "" {
				s = strings.ReplaceAll(s, d, " ")
			}
		}
		return s
	}
	if name := words(cut(title), maxName); name != "" {
		return name
	}
	if name := words(cut(path), maxName); name != "" {
		return name
	}
	return "Home"
}
