package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAProfileNeverSavesOrFillsInSignIns(t *testing.T) {
	dir := t.TempDir()
	prefs := filepath.Join(dir, "Default", "Preferences")
	if err := os.MkdirAll(filepath.Dir(prefs), 0o700); err != nil {
		t.Fatal(err)
	}
	kept := `{"profile":{"password_manager_enabled":true,"name":"Semar"},"credentials_enable_service":true,"other":1}`
	if err := os.WriteFile(prefs, []byte(kept), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureProfile(dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(prefs)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	profile := got["profile"].(map[string]any)
	autofill := got["autofill"].(map[string]any)
	if got["credentials_enable_service"] != false || profile["password_manager_enabled"] != false ||
		autofill["profile_enabled"] != false || autofill["credit_card_enabled"] != false {
		t.Errorf("saved sign-ins are still on: %s", raw)
	}
	if profile["name"] != "Semar" || got["other"] != float64(1) {
		t.Errorf("other settings were lost: %s", raw)
	}
}
