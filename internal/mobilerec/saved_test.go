package mobilerec_test

import (
	"testing"

	"github.com/jancuk/semar/internal/mobilerec"
)

func TestSavedCurrentFollowsTheStepsSinceTheLastSave(t *testing.T) {
	r := newRig(t)
	s := r.session(mobilerec.KindFlow, "login")
	recordEmailSignIn(t, s)
	if s.SavedCurrent() {
		t.Error("a recording that was never saved is current")
	}
	if _, err := s.Save(mobilerec.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	if !s.SavedCurrent() {
		t.Error("the save is not current right after it was written")
	}
	if err := s.EditStep(2, mobilerec.Edit{Secret: "SHOP_EMAIL"}); err != nil {
		t.Fatal(err)
	}
	if s.SavedCurrent() {
		t.Error("a step changed, and the save is still current")
	}
	if _, err := s.Save(mobilerec.SaveOptions{Replace: true}); err != nil {
		t.Fatal(err)
	}
	if !s.SavedCurrent() {
		t.Error("the save is not current after saving again")
	}
	if err := s.Undo(); err != nil {
		t.Fatal(err)
	}
	if s.SavedCurrent() {
		t.Error("an undone save is current")
	}
}
