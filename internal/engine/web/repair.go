package web

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

// AXNode is one element of the accessibility tree, as the repair reads it.
type AXNode struct{ Role, Name string }

// Candidates proposes up to three locators for an element that was not found. It uses no model. It
// reads the accessibility tree that the engine already has, and it looks for, in this order: the same
// role with the closest name, the same name with another role, and the same text with other spelling.
// A candidate that would match more than one element is left out. An element that the old locator
// names only by testId or css gives none, because there is nothing to compare.
func Candidates(element string, old domain.Locator, tree []AXNode) []domain.Candidate {
	var nodes []AXNode
	for _, n := range tree {
		if n.Name = strings.TrimSpace(n.Name); n.Name != "" {
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
			if n.Name == old.Name && !strings.EqualFold(n.Role, role) && meaningful(n.Role) {
				add(domain.Locator{Role: n.Role, Name: n.Name},
					fmt.Sprintf("the same name %q with the role %s, and not %s", n.Name, n.Role, orNone(old.Role)))
			}
		}
	}
	if old.Text != "" {
		for _, n := range rank(old.Text, nodes, func(n AXNode) bool { return n.Name != old.Text }) {
			loc := domain.Locator{Role: n.Role, Name: n.Name}
			if n.Role == "StaticText" {
				loc = domain.Locator{Text: n.Name}
			}
			add(loc, fmt.Sprintf("the text %q, which is close to %q", n.Name, old.Text))
		}
	}
	return out
}

func orNone(role string) string {
	if role == "" {
		return "any role"
	}
	return role
}

// meaningful is false for the roles that the tree gives to a box with no meaning of its own.
func meaningful(role string) bool {
	switch role {
	case "", "generic", "none", "presentation", "StaticText", "InlineTextBox", "LineBreak", "RootWebArea", "paragraph", "group":
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
		if s := similarity(want, n.Name); s >= minCloseName {
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

// similarity is 1 for names that are equal once case and spacing are ignored, and falls toward 0 as
// they differ. A name that holds the other counts as at least 0.7.
func similarity(a, b string) float64 {
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
