package mobilerec_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/mobilerec"
)

// A recording can check more than a text: a state, a count, an attribute, or that nothing is there.
func TestAssertKindsBecomeChecks(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindScenario, "SHOP-9")
	ctx := context.Background()
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	btn := pick(t, f, byDesc("Sign in"))
	for _, a := range []mobilerec.Assert{
		{Kind: mobilerec.AssertVisible},
		{Kind: mobilerec.AssertEnabled},
		{Kind: mobilerec.AssertNotExists},
		{Kind: mobilerec.AssertCount, Value: "1"},
		{Kind: mobilerec.AssertAttribute, Attribute: "content-desc"},
	} {
		a.Frame, a.Target = f.ID, btn
		if f, err = s.Assert(ctx, a); err != nil {
			t.Fatalf("%s: %v", a.Kind, err)
		}
	}
	d, err := s.Build()
	if err != nil {
		t.Fatal(err)
	}
	if d.Checks != 5 {
		t.Fatalf("checks = %d", d.Checks)
	}
	var states, counts, attrs, exists int
	for _, st := range d.Scenario.Steps {
		for _, e := range st.Expect {
			switch {
			case e.Check.State != "":
				states++
			case e.Check.Count != nil:
				counts++
			case e.Check.Attribute != "":
				attrs++
				if e.Check.Equals != "Sign in" {
					t.Errorf("the attribute check equals %v", e.Check.Equals)
				}
			case e.Check.Exists != nil && !*e.Check.Exists:
				exists++
			}
		}
	}
	if states != 2 || counts != 1 || attrs != 1 || exists != 1 {
		t.Errorf("states %d counts %d attrs %d not-exists %d", states, counts, attrs, exists)
	}

	for _, bad := range []mobilerec.Assert{
		{Kind: "sparkly"},
		{Kind: mobilerec.AssertCount, Value: "many"},
		{Kind: mobilerec.AssertAttribute},
	} {
		bad.Frame, bad.Target = f.ID, btn
		if _, err := s.Assert(ctx, bad); err == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
}

// The studio chooses a flow or a scenario at the end, so a recording can be saved as the other kind.
func TestRetargetChangesWhatTheRecordingIsSavedAs(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindScenario, "recording")
	ctx := context.Background()
	f, err := s.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Act(ctx, mobilerec.Act{Do: "tap", Frame: f.ID, Target: ptr(pick(t, f, byDesc("Sign in")))}); err != nil {
		t.Fatal(err)
	}
	if err := s.Retarget(mobilerec.Next{Kind: mobilerec.KindFlow, ID: "../bad"}); err == nil {
		t.Error("a flow ID that is a path was accepted")
	}
	if err := s.Retarget(mobilerec.Next{Kind: mobilerec.KindFlow, ID: "tap-in"}); err != nil {
		t.Fatal(err)
	}
	sv, err := s.Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sv.FlowPath != ".semar/flows/tap-in.yaml" || !strings.Contains(r.read(sv.FlowPath), "id: tap-in") {
		t.Errorf("saved %+v", sv.Files)
	}
}
