package uirepair_test

import (
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
