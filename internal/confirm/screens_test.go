package confirm_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jancuk/semar/internal/confirm"
	"github.com/jancuk/semar/internal/domain"
)

func screensApproval(app string, h domain.Hash, user string, at time.Time) confirm.ScreensApproval {
	return confirm.ScreensApproval{
		App: app, Hash: h, User: user, Identity: "local, unverified identity", At: at,
		Locators: map[string]map[string]domain.Locator{"Cart": {"pay": {Role: "button", Name: "Pay"}}},
	}
}

func TestScreensApprovalIsKeyedByHash(t *testing.T) {
	store := confirm.NewLocal(t.TempDir())
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	if ok, err := store.ScreensApproved("shop", "h1"); err != nil || ok {
		t.Fatalf("before: %v, %v", ok, err)
	}
	if err := store.PutScreens(screensApproval("shop", "h1", "ana", now)); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.ScreensApproved("shop", "h1"); err != nil || !ok {
		t.Fatalf("approved hash: %v, %v", ok, err)
	}
	if ok, _ := store.ScreensApproved("shop", "h2"); ok {
		t.Error("another hash counted as approved")
	}
	if ok, _ := store.ScreensApproved("orders", "h1"); ok {
		t.Error("another app counted as approved")
	}
}

func TestLatestScreensIsTheNewest(t *testing.T) {
	store := confirm.NewLocal(t.TempDir())
	t0 := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	if _, ok, err := store.LatestScreens("shop"); err != nil || ok {
		t.Fatalf("empty: %v, %v", ok, err)
	}
	for i, h := range []domain.Hash{"h1", "h2", "h3"} {
		if err := store.PutScreens(screensApproval("shop", h, "ana", t0.Add(time.Duration(i)*time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := store.LatestScreens("shop")
	if err != nil || !ok || got.Hash != "h3" {
		t.Fatalf("latest = %+v, %v, %v", got, ok, err)
	}
	if loc, _ := got.Screens().Locator("Cart", "pay"); loc.Name != "Pay" {
		t.Errorf("the approved locators were not kept: %+v", loc)
	}
}

func TestScreensApprovalRefusesAForgedRecord(t *testing.T) {
	root := t.TempDir()
	store := confirm.NewLocal(root)
	if err := store.PutScreens(screensApproval("shop", "h1", "ana", time.Now())); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "store", "approvals", "screens", "shop", "h1")
	if err := os.WriteFile(filepath.Join(dir, "mallory.json"), []byte(`{"schema":1,"app":"shop","hash":"other"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ScreensApproved("shop", "h1"); err == nil || !strings.Contains(err.Error(), "want") {
		t.Fatalf("error = %v, want a refusal of the record for another hash", err)
	}
}
