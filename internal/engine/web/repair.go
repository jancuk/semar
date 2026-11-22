package web

import (
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/uirepair"
)

// AXNode is one element of the accessibility tree, as the repair reads it.
type AXNode struct{ Role, Name string }

// Candidates proposes up to three locators for an element that was not found. It is the repair that
// the browser and the device share, in internal/uirepair.
func Candidates(element string, old domain.Locator, tree []AXNode) []domain.Candidate {
	nodes := make([]uirepair.AXNode, len(tree))
	for i, n := range tree {
		nodes[i] = uirepair.AXNode(n)
	}
	return uirepair.Candidates(element, old, nodes)
}

// meaningful is false for the roles that the tree gives to a box with no meaning of its own.
func meaningful(role string) bool { return uirepair.Meaningful(role) }

// similarity is how alike two names are, from 0 to 1.
func similarity(a, b string) float64 { return uirepair.Similarity(a, b) }
