package cli

import (
	"testing"

	"github.com/jancuk/semar/internal/pagelist"
)

// A model that sees what an earlier flow left on the page uses it, and the test it writes then needs a
// record that is gone at the next run.
func TestTheModelDoesNotSeeTheRecordsOfAnotherRun(t *testing.T) {
	item := func(ref, name, value string) pagelist.Element {
		return pagelist.Element{Ref: ref, Role: "button", Name: name, Value: value}
	}
	l := pagelist.List{Elements: []pagelist.Element{
		item("e1", "New board", ""),
		item("e2", "Board r-20261118T004030Z-96e2", ""),
		item("e3", "Board r-20261118T004200Z-aaaa", ""),
		item("e4", "Name", "board r-20261118t004030z-96e2"),
	}}
	got := ownElements(l, "r-20261118T004200Z-aaaa")
	var refs []string
	for _, e := range got.Elements {
		refs = append(refs, e.Ref)
	}
	if len(refs) != 2 || refs[0] != "e1" || refs[1] != "e3" {
		t.Errorf("the model sees %v, want e1 and e3", refs)
	}
	if len(l.Elements) != 4 {
		t.Errorf("the list of the browser lost elements: %d", len(l.Elements))
	}
}
