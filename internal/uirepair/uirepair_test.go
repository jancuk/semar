package uirepair_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/uirepair"
)

func TestCandidatesForADeviceTree(t *testing.T) {
	tree := []uirepair.AXNode{
		{Role: "button", Name: "Sign in"}, {Role: "button", Name: "Register"}, {Role: "text", Name: "Welcome back"}, {Role: "textbox", Name: "Email"},
	}
	got := uirepair.Candidates("submit", domain.Locator{Role: "button", Name: "Sign-in"}, tree)
	if len(got) != 1 || got[0].Locator != (domain.Locator{Role: "button", Name: "Sign in"}) || got[0].Element != "submit" {
		t.Errorf("same role, close name = %+v", got)
	}
	// A text node is found by its text, as StaticText is in a browser.
	got = uirepair.Candidates("hello", domain.Locator{Text: "Welcome back!"}, tree)
	if len(got) != 1 || got[0].Locator != (domain.Locator{Text: "Welcome back"}) {
		t.Errorf("text = %+v", got)
	}
	// A locator that names only an id gives nothing to compare.
	if got := uirepair.Candidates("x", domain.Locator{ID: "gone"}, tree); len(got) != 0 {
		t.Errorf("an id gave %+v", got)
	}
	// A candidate that matches two nodes is left out.
	dup := append([]uirepair.AXNode{{Role: "button", Name: "Sign in"}}, tree...)
	if got := uirepair.Candidates("submit", domain.Locator{Role: "button", Name: "Sign-in"}, dup); len(got) != 0 {
		t.Errorf("an ambiguous candidate = %+v", got)
	}
}

func TestSimilarity(t *testing.T) {
	if uirepair.Similarity("Sign In", "sign   in") != 1 || uirepair.Similarity("", "a") != 0 || uirepair.Similarity("Pay", "Pay now") < 0.7 {
		t.Error("similarity")
	}
	if !uirepair.Meaningful("button") || uirepair.Meaningful("text") || uirepair.Meaningful("") {
		t.Error("meaningful")
	}
}

// An element that a screens file finds by id or by desc gets candidates of the same kind from what the
// screen shows now.
func TestCandidatesForAnIDAndADesc(t *testing.T) {
	tree := []uirepair.AXNode{
		{Role: "button", Name: "Sign in", ID: "com.shop:id/sign_in_button", Desc: "Sign in"},
		{Role: "textbox", ID: "com.shop:id/email_field"},
		{Role: "button", Name: "Register", ID: "com.shop:id/register", Desc: "Register"},
	}
	got := uirepair.Candidates("submit", domain.Locator{ID: "sign_in"}, tree)
	if len(got) != 1 || got[0].Locator != (domain.Locator{ID: "com.shop:id/sign_in_button"}) || got[0].Element != "submit" {
		t.Errorf("id = %+v", got)
	}
	got = uirepair.Candidates("email", domain.Locator{ID: "com.shop:id/email"}, tree)
	if len(got) != 1 || got[0].Locator.ID != "com.shop:id/email_field" {
		t.Errorf("id of a node with no name = %+v", got)
	}
	got = uirepair.Candidates("submit", domain.Locator{Desc: "Sign-in"}, tree)
	if len(got) != 1 || got[0].Locator != (domain.Locator{Desc: "Sign in"}) {
		t.Errorf("desc = %+v", got)
	}
	// Two nodes with the same desc make a candidate that is not alone, and it is left out.
	dup := append([]uirepair.AXNode{{Role: "button", Name: "Sign in", Desc: "Sign in"}}, tree...)
	if got := uirepair.Candidates("submit", domain.Locator{Desc: "Sign-in"}, dup); len(got) != 0 {
		t.Errorf("an ambiguous desc = %+v", got)
	}
	// The id that the old locator has already is no candidate.
	if got := uirepair.Candidates("x", domain.Locator{ID: "register"}, tree); len(got) != 0 {
		t.Errorf("the same id = %+v", got)
	}
}

func TestClosestAlwaysGivesUpToNInTheOrderOfLikenessThenTheTree(t *testing.T) {
	tree := []uirepair.AXNode{
		{Role: "text", Name: "Orders"}, {Role: "button", Name: "Hapus Semua"}, {Role: "button", Name: "Batal"}, {Role: "button", Name: "Hapus"},
		{Role: "textbox", Name: "Email"}, {Role: "button", Name: "Simpan"}, {Role: "button", Name: "Kirim"},
	}
	old := domain.Locator{Role: "button", Name: "Hapuss"}
	got := uirepair.Closest(old, tree, 5)
	if len(got) != 5 {
		t.Fatalf("got %d, want 5: %+v", len(got), got)
	}
	if got[0].Locator != (domain.Locator{Role: "button", Name: "Hapus"}) || got[1].Locator != (domain.Locator{Role: "button", Name: "Hapus Semua"}) {
		t.Errorf("the order of likeness = %+v", got)
	}
	// Nothing is alike, and still five come back, in the order of the tree.
	none := uirepair.Closest(domain.Locator{Role: "button", Name: "zzzzzzzz"}, tree, 5)
	if len(none) != 5 || none[0].Locator.Name != "Orders" && none[0].Locator.Text != "Orders" {
		t.Errorf("no likeness gave %+v", none)
	}
	// The same input gives the same answer.
	again := uirepair.Closest(old, tree, 5)
	for i := range got {
		if got[i] != again[i] {
			t.Fatalf("the order changed at %d: %+v and %+v", i, got[i], again[i])
		}
	}
	if len(uirepair.Closest(old, tree[:2], 5)) != 2 || uirepair.Closest(old, tree, 0) != nil {
		t.Error("a short tree or a zero limit")
	}
}

func TestClosestNamesTheMatchesOfAnElementThatTwoNodesShare(t *testing.T) {
	tree := []uirepair.AXNode{{Role: "button", Name: "Hapus"}, {Role: "button", Name: "Hapus"}, {Role: "button", Name: "Batal"}}
	got := uirepair.Closest(domain.Locator{Role: "button", Name: "Hapus!"}, tree, 5)
	if len(got) != 2 || got[0].Why != `button "Hapus" (2 matches)` {
		t.Errorf("got %+v", got)
	}
	// A node with an id is named by the id, and a node with nothing to name it is left out.
	byID := uirepair.Closest(domain.Locator{ID: "delete_btn"}, []uirepair.AXNode{{Role: "generic", ID: "com.shop:id/delete_button"}, {Role: "generic"}}, 5)
	if len(byID) != 1 || byID[0].Locator.ID != "com.shop:id/delete_button" {
		t.Errorf("by id = %+v", byID)
	}
}

func TestClosestCapsEveryLabelItShows(t *testing.T) {
	long := strings.Repeat("Reset code 884213 ", 20)
	tree := []uirepair.AXNode{
		{Role: "button", Name: long}, {Role: "", ID: "com.x:id/" + strings.Repeat("a", 90)}, {Role: "text", Desc: strings.Repeat("d", 80)},
	}
	got := uirepair.Closest(domain.Locator{Role: "button", Name: "x"}, tree, 5)
	if len(got) == 0 {
		t.Fatal("no candidates")
	}
	for _, c := range got {
		if n := len([]rune(c.Why)); n > 80 {
			t.Errorf("a label of %d characters: %q", n, c.Why)
		}
	}
	// The locator stays exact: only what a person reads is cut.
	if got[0].Locator.Name != strings.Join(strings.Fields(long), " ") {
		t.Errorf("the locator was cut: %+v", got[0].Locator)
	}
}
