// Package uirepair proposes new locators for an element that a screen no longer shows. It reads the
// accessibility tree that an engine already has, in the one shape that a browser and a device share.
package uirepair

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/jancuk/semar/internal/domain"
)

// maxCandidates is how many candidates a repair proposes for one element.
const maxCandidates = 3

// minCloseName is how alike two names must be for the second to be a candidate for the first, from 0 to 1.
const minCloseName = 0.5

// AXNode is one element of the accessibility tree, as the repair reads it. A browser gives roles such
// as button and StaticText, and a device gives button and text.
//
// A device also gives ID (the resource id) and Desc (the content description) of a node when it has
// them, so that an element that a screens file finds by id or by desc gets candidates of the same kind.
type AXNode struct{ Role, Name, ID, Desc string }

// Candidates proposes up to three locators for an element that was not found. It uses no model. It
// reads the accessibility tree that the engine already has, and it looks for, in this order: the same
// role with the closest name, the same name with another role, and the same text with other spelling.
// A candidate that would match more than one element is left out. An element that the old locator
// names only by testId or css gives none, because there is nothing to compare.
func Candidates(element string, old domain.Locator, tree []AXNode) []domain.Candidate {
	var nodes []AXNode
	for _, n := range tree {
		if n.Name = strings.TrimSpace(n.Name); n.Name != "" || n.ID != "" {
			nodes = append(nodes, n)
		}
	}
	var out []domain.Candidate
	seen := map[domain.Locator]bool{}
	add := func(loc domain.Locator, why string) {
		if len(out) >= maxCandidates || seen[loc] || loc == old {
			return
		}
		if !unique(loc, nodes) {
			return
		}
		seen[loc] = true
		out = append(out, domain.Candidate{Element: element, Locator: loc, Why: why})
	}

	role := strings.ToLower(old.Role)
	if role != "" && old.Name != "" {
		for _, n := range rank(old.Name, nodes, func(n AXNode) bool { return strings.EqualFold(n.Role, role) && n.Name != old.Name }) {
			add(domain.Locator{Role: n.Role, Name: n.Name},
				fmt.Sprintf("the same role (%s) with the name %q, which is close to %q", n.Role, n.Name, old.Name))
		}
	}
	if old.Name != "" {
		for _, n := range nodes {
			if n.Name == old.Name && !strings.EqualFold(n.Role, role) && Meaningful(n.Role) {
				add(domain.Locator{Role: n.Role, Name: n.Name},
					fmt.Sprintf("the same name %q with the role %s, and not %s", n.Name, n.Role, orNone(old.Role)))
			}
		}
	}
	if old.Text != "" {
		for _, n := range rank(old.Text, nodes, func(n AXNode) bool { return n.Name != old.Text }) {
			loc := domain.Locator{Role: n.Role, Name: n.Name}
			if isText(n.Role) {
				loc = domain.Locator{Text: n.Name}
			}
			add(loc, fmt.Sprintf("the text %q, which is close to %q", n.Name, old.Text))
		}
	}
	out = append(out, byIDOrDesc(element, old, nodes, add)...)
	return out
}

// byIDOrDesc proposes the id and the desc of a node that is close to the id or the desc that the old
// locator names, on a screen that no longer has them. add decides whether a candidate is kept.
func byIDOrDesc(element string, old domain.Locator, nodes []AXNode, add func(domain.Locator, string)) []domain.Candidate {
	if oldID := firstOf(old.ID, old.TestID); oldID != "" {
		for _, n := range rankBy(shortID(oldID), nodes, func(n AXNode) string { return shortID(n.ID) }) {
			if n.ID != oldID && shortID(n.ID) != oldID {
				add(domain.Locator{ID: n.ID}, fmt.Sprintf("the id %q, which is close to %q", n.ID, oldID))
			}
		}
	}
	if old.Desc != "" {
		want := strings.Join(strings.Fields(old.Desc), " ")
		for _, n := range rankBy(want, nodes, func(n AXNode) string { return strings.Join(strings.Fields(n.Desc), " ") }) {
			if d := strings.Join(strings.Fields(n.Desc), " "); d != want {
				add(domain.Locator{Desc: d}, fmt.Sprintf("the desc %q, which is close to %q", d, want))
			}
		}
	}
	return nil
}

func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// shortID is the part of a resource id after :id/, which is how a person names it.
func shortID(id string) string {
	if _, short, ok := strings.Cut(id, ":id/"); ok {
		return short
	}
	return id
}

// rankBy is rank on a field that key reads of each node. A node whose field is empty is left out.
func rankBy(want string, nodes []AXNode, key func(AXNode) string) []AXNode {
	type scored struct {
		n AXNode
		s float64
	}
	var list []scored
	for _, n := range nodes {
		k := key(n)
		if k == "" {
			continue
		}
		if s := Similarity(want, k); s >= minCloseName {
			list = append(list, scored{n, s})
		}
	}
	slices.SortStableFunc(list, func(a, b scored) int { return cmp.Compare(b.s, a.s) })
	out := make([]AXNode, len(list))
	for i, l := range list {
		out[i] = l.n
	}
	return out
}

// isText is the role of a node that holds plain text, which a candidate finds by its text.
func isText(role string) bool { return role == "StaticText" || role == "text" }

func orNone(role string) string {
	if role == "" {
		return "any role"
	}
	return role
}

// Meaningful is false for the roles that the tree gives to a box with no meaning of its own.
func Meaningful(role string) bool {
	switch role {
	case "", "generic", "none", "presentation", "StaticText", "text", "InlineTextBox", "LineBreak", "RootWebArea", "paragraph", "group":
		return false
	}
	return true
}

// rank gives the nodes that keep() accepts and whose name is alike want, the most alike first. Equal
// names keep the order of the tree.
func rank(want string, nodes []AXNode, keep func(AXNode) bool) []AXNode {
	type scored struct {
		n AXNode
		s float64
	}
	var list []scored
	for _, n := range nodes {
		if !keep(n) {
			continue
		}
		if s := Similarity(want, n.Name); s >= minCloseName {
			list = append(list, scored{n, s})
		}
	}
	slices.SortStableFunc(list, func(a, b scored) int { return cmp.Compare(b.s, a.s) })
	out := make([]AXNode, len(list))
	for i, l := range list {
		out[i] = l.n
	}
	return out
}

// unique reports whether the locator of a candidate matches exactly one element of the tree.
func unique(loc domain.Locator, nodes []AXNode) bool {
	n := 0
	for _, node := range nodes {
		switch {
		case loc.ID != "":
			if node.ID == loc.ID {
				n++
			}
		case loc.Desc != "":
			if strings.Join(strings.Fields(node.Desc), " ") == loc.Desc {
				n++
			}
		case loc.Text != "":
			if node.Name == loc.Text {
				n++
			}
		case node.Name == loc.Name && strings.EqualFold(node.Role, loc.Role):
			n++
		}
	}
	return n == 1
}

// Similarity is 1 for names that are equal once case and spacing are ignored, and falls toward 0 as
// they differ. A name that holds the other counts as at least 0.7.
func Similarity(a, b string) float64 {
	fa, fb := fold(a), fold(b)
	if fa == fb {
		return 1
	}
	if fa == "" || fb == "" {
		return 0
	}
	score := 1 - float64(distance([]rune(fa), []rune(fb)))/float64(max(len([]rune(fa)), len([]rune(fb))))
	if strings.Contains(fa, fb) || strings.Contains(fb, fa) {
		score = max(score, 0.7)
	}
	return score
}

func fold(s string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(s), unicode.IsSpace), " ")
}

func distance(a, b []rune) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// Closest gives up to n of the nodes that look most like the old locator, the most alike first. It is
// for a person who reads why an element was not found, so it has no threshold: it gives n nodes even
// when none is alike, and the order of the tree breaks a tie. Each locator is the one that names the
// node best, as in a name for a locator that matched several: id, then desc, then role and name, then
// text. A node that has none of them is left out, and a locator that two nodes share is given once and
// says how many matched. The caller gives only the nodes that are visible, and sets Element.
func Closest(old domain.Locator, nodes []AXNode, n int) []domain.Candidate {
	if n <= 0 {
		return nil
	}
	type scored struct {
		loc   domain.Locator
		why   string
		score float64
	}
	var list []scored
	matches := map[domain.Locator]int{}
	for _, node := range nodes {
		loc, label, ok := nodeLocator(node)
		if !ok {
			continue
		}
		matches[loc]++
		if matches[loc] > 1 {
			continue
		}
		role := node.Role
		if role == "" || role == "generic" {
			role = "element"
		}
		list = append(list, scored{loc: loc, why: fmt.Sprintf("%s %q", role, clipLabel(label)), score: likeness(old, node)})
	}
	slices.SortStableFunc(list, func(a, b scored) int { return cmp.Compare(b.score, a.score) })
	var out []domain.Candidate
	for _, s := range list {
		if len(out) == n {
			break
		}
		why := s.why
		if m := matches[s.loc]; m > 1 {
			why += fmt.Sprintf(" (%d matches)", m)
		}
		out = append(out, domain.Candidate{Locator: s.loc, Why: why})
	}
	return out
}

// maxLabel is the most characters of an element's text that a line for a person shows.
const maxLabel = 60

// clipLabel cuts a label to maxLabel characters, so that a long text of the screen is never a long line of a
// report or of the output of a run.
func clipLabel(s string) string {
	if r := []rune(s); len(r) > maxLabel {
		return string(r[:maxLabel]) + "..."
	}
	return s
}

// nodeLocator is the locator that names a node, and the label that a person reads.
func nodeLocator(n AXNode) (domain.Locator, string, bool) {
	name := strings.Join(strings.Fields(n.Name), " ")
	switch {
	case n.ID != "":
		return domain.Locator{ID: n.ID}, shortID(n.ID), true
	case n.Desc != "" && len([]rune(n.Desc)) <= 60:
		return domain.Locator{Desc: n.Desc}, n.Desc, true
	case Meaningful(n.Role) && name != "":
		return domain.Locator{Role: n.Role, Name: name}, name, true
	case name != "" && len([]rune(name)) <= 60:
		return domain.Locator{Text: name}, name, true
	}
	return domain.Locator{}, "", false
}

// likeness is how alike a node is to what the old locator names, from 0 to 1. Each field of the old
// locator is compared with the field of the node that holds the same kind of text.
func likeness(old domain.Locator, n AXNode) float64 {
	best := 0.0
	try := func(want, have string) {
		if want != "" && have != "" {
			best = max(best, Similarity(want, have))
		}
	}
	try(old.Name, n.Name)
	try(old.Text, n.Name)
	try(old.HasText, n.Name)
	try(old.Desc, n.Desc)
	try(old.Desc, n.Name)
	if oldID := firstOf(old.ID, old.TestID); oldID != "" && n.ID != "" {
		try(shortID(oldID), shortID(n.ID))
	}
	return best
}
