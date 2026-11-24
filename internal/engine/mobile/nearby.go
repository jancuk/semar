package mobile

import (
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/uirepair"
)

// maxNearby is how many of the elements that look like a missing one are kept.
const maxNearby = 5

// nearby gives the visible elements of the tree that look most like the locator of an element that was
// not found, named for that element. A password field is left out. What it holds is what the screen
// shows, and the recorder removes a secret from it like any other text.
func nearby(element string, old domain.Locator, t *Tree) []domain.Candidate {
	var nodes []uirepair.AXNode
	t.Walk(func(n *Node) bool {
		if !n.Visible || n.Password {
			return true
		}
		role := n.Role
		if role == "" {
			role = "generic"
		}
		nodes = append(nodes, uirepair.AXNode{Role: role, Name: n.AccName(), ID: n.ID, Desc: collapse(n.Desc)})
		return true
	})
	out := uirepair.Closest(old, nodes, maxNearby)
	for i := range out {
		out[i].Element = element
	}
	return out
}
