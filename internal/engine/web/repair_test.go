package web

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func tree() []AXNode {
	return []AXNode{
		{"button", "Save changes"},
		{"button", "Cancel"},
		{"link", "Save"},
		{"heading", "Board"},
		{"StaticText", "Welcome back"},
		{"button", "Delete card"},
		{"generic", "Save"},
	}
}

func TestCandidatesSameRoleClosestName(t *testing.T) {
	got := Candidates("save", domain.Locator{Role: "button", Name: "Save"}, tree())
	if len(got) == 0 || got[0].Locator != (domain.Locator{Role: "button", Name: "Save changes"}) {
		t.Fatalf("candidates = %+v", got)
	}
	if got[0].Element != "save" || !strings.Contains(got[0].Why, `"Save changes"`) || !strings.Contains(got[0].Why, `"Save"`) {
		t.Errorf("why = %q", got[0].Why)
	}
}

func TestCandidatesSameNameAnyRole(t *testing.T) {
	got := Candidates("save", domain.Locator{Role: "button", Name: "Save"}, tree())
	found := false
	for _, c := range got {
		if c.Locator == (domain.Locator{Role: "link", Name: "Save"}) {
			found = true
			if !strings.Contains(c.Why, "the same name") {
				t.Errorf("why = %q", c.Why)
			}
		}
		if c.Locator.Role == "generic" {
			t.Errorf("a box with no meaning was proposed: %+v", c)
		}
	}
	if !found {
		t.Errorf("the link with the same name was not proposed: %+v", got)
	}
}

func TestCandidatesSameText(t *testing.T) {
	got := Candidates("hello", domain.Locator{Text: "Welcome back!"}, tree())
	if len(got) != 1 || got[0].Locator != (domain.Locator{Text: "Welcome back"}) {
		t.Fatalf("candidates = %+v", got)
	}
}

func TestCandidatesAreAtMostThree(t *testing.T) {
	var nodes []AXNode
	for _, n := range []string{"Save a", "Save b", "Save c", "Save d", "Save e"} {
		nodes = append(nodes, AXNode{"button", n})
	}
	if got := Candidates("save", domain.Locator{Role: "button", Name: "Save"}, nodes); len(got) != 3 {
		t.Errorf("got %d candidates, want 3", len(got))
	}
}

func TestCandidatesLeaveOutTheOneThatIsNotUnique(t *testing.T) {
	nodes := []AXNode{{"button", "Save changes"}, {"button", "Save changes"}}
	if got := Candidates("save", domain.Locator{Role: "button", Name: "Save"}, nodes); len(got) != 0 {
		t.Errorf("a locator that matches two elements was proposed: %+v", got)
	}
}

func TestCandidatesNeedSomethingToCompare(t *testing.T) {
	if got := Candidates("x", domain.Locator{TestID: "gone"}, tree()); len(got) != 0 {
		t.Errorf("candidates = %+v", got)
	}
	if got := Candidates("x", domain.Locator{Role: "button", Name: "Zzzzzz"}, tree()); len(got) != 0 {
		t.Errorf("an unlike name was proposed: %+v", got)
	}
}

func TestCandidatesAreRepeatable(t *testing.T) {
	a := Candidates("save", domain.Locator{Role: "button", Name: "Save"}, tree())
	for range 5 {
		b := Candidates("save", domain.Locator{Role: "button", Name: "Save"}, tree())
		if len(a) != len(b) {
			t.Fatal("the number of candidates changed")
		}
		for i := range a {
			if a[i] != b[i] {
				t.Fatalf("candidate %d changed: %+v then %+v", i, a[i], b[i])
			}
		}
	}
}

func TestSimilarity(t *testing.T) {
	for _, c := range []struct {
		a, b string
		min  float64
		max  float64
	}{
		{"Save", "save", 1, 1},
		{"Save", " Save  ", 1, 1},
		{"Save", "Save changes", 0.7, 1},
		{"Save", "Cancel", 0, 0.4},
		{"", "x", 0, 0},
	} {
		if got := similarity(c.a, c.b); got < c.min || got > c.max {
			t.Errorf("similarity(%q, %q) = %.2f, want %.2f to %.2f", c.a, c.b, got, c.min, c.max)
		}
	}
}
