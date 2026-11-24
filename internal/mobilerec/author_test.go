package mobilerec_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/mobilerec"
)

var agentAuthor = domain.ProviderRef{Instance: "mcp", Model: "agent", Harness: "mcp"}

func TestAuthorMakesChecksBaselineByModel(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	s := r.session(mobilerec.KindScenario, "AGENT-1", func(o *mobilerec.Options) { o.Author = &agentAuthor })
	f := recordLogin(t, r, s)
	if _, err := s.Assert(ctx, mobilerec.Assert{Frame: f.ID, Target: pick(t, f, byID("welcome")), Kind: mobilerec.AssertEquals}); err != nil {
		t.Fatal(err)
	}
	sv, err := s.Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	feature := r.read(sv.ScenarioPath)
	if !strings.Contains(feature, "# baseline, by model") {
		t.Errorf("the feature does not say baseline, by model:\n%s", feature)
	}
	if strings.Contains(feature, "# person") {
		t.Errorf("a check of an agent is marked as a person's:\n%s", feature)
	}
	if got := sv.Draft.Script.AuthoredBy; got != agentAuthor {
		t.Errorf("AuthoredBy = %+v, want %+v", got, agentAuthor)
	}
	if script := r.read(sv.Files[len(sv.Files)-1]); !strings.Contains(script, "harness: mcp") {
		t.Errorf("the script does not name the agent:\n%s", script)
	}
}

func TestNoAuthorChangesNothing(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	s := r.session(mobilerec.KindScenario, "PERSON-1")
	f := recordLogin(t, r, s)
	if _, err := s.Assert(ctx, mobilerec.Assert{Frame: f.ID, Target: pick(t, f, byID("welcome")), Kind: mobilerec.AssertEquals}); err != nil {
		t.Fatal(err)
	}
	sv, err := s.Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if feature := r.read(sv.ScenarioPath); !strings.Contains(feature, "# person") || strings.Contains(feature, "by model") {
		t.Errorf("a person's check changed:\n%s", feature)
	}
	if sv.Draft.Script.AuthoredBy != (domain.ProviderRef{Instance: "person", Harness: "record"}) {
		t.Errorf("AuthoredBy = %+v", sv.Draft.Script.AuthoredBy)
	}
}

func TestAuthorGivesAFlowADescription(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindFlow, "login", func(o *mobilerec.Options) { o.Author = &agentAuthor })
	recordLogin(t, r, s)
	sv, err := s.Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sv.Draft.Flow.Description != mobilerec.AgentDescription {
		t.Errorf("description = %q", sv.Draft.Flow.Description)
	}
	r.signed = false
	r.d.Show(signInScreen())
	s2 := r.session(mobilerec.KindFlow, "login2", func(o *mobilerec.Options) { o.Author = &agentAuthor; o.Title = "Sign in" })
	recordLogin(t, r, s2)
	sv2, err := s2.Save(mobilerec.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sv2.Draft.Flow.Description != "Sign in" {
		t.Errorf("a given title was replaced: %q", sv2.Draft.Flow.Description)
	}
}
