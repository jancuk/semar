package keychain

import (
	"errors"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestMemoryStore(t *testing.T) {
	s := Memory()
	if _, err := s.Get("orders"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing = %v, want ErrNotFound", err)
	}
	if err := s.Set("orders", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("orders", "v2"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Get("orders"); err != nil || v != "v2" {
		t.Fatalf("Get = %q, %v", v, err)
	}
	if err := s.Delete("orders"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("orders"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Delete = %v, want ErrNotFound", err)
	}
}

func TestCheckName(t *testing.T) {
	for _, ok := range []string{"a", "acme.prod_1-x", strings.Repeat("a", 64)} {
		if err := CheckName(ok); err != nil {
			t.Errorf("CheckName(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", strings.Repeat("a", 65), "a b", "a/b", "a:b", "é", "a\n"} {
		if err := CheckName(bad); err == nil {
			t.Errorf("CheckName(%q) accepted", bad)
		}
	}
	s := Memory()
	if err := s.Set("bad name", "x"); err == nil {
		t.Error("Set accepted a bad name")
	}
}

func TestResolver(t *testing.T) {
	store := Memory()
	_ = store.Set("deepseek", "sk-stored-value")
	r := Resolver(env(map[string]string{"ACME_KEY": "sk-env-value", "EMPTY": ""}), store)

	if v, err := r("env:ACME_KEY"); err != nil || v != "sk-env-value" {
		t.Errorf("env = %q, %v", v, err)
	}
	if v, err := r("keychain:deepseek"); err != nil || v != "sk-stored-value" {
		t.Errorf("keychain = %q, %v", v, err)
	}

	for _, ref := range []string{"env:MISSING", "env:EMPTY", "keychain:orders"} {
		_, err := r(ref)
		if err == nil || !strings.Contains(err.Error(), ref) {
			t.Errorf("%s: error %v does not name the reference", ref, err)
		}
	}
	if _, err := Resolver(env(nil), nil)("keychain:x"); err == nil {
		t.Error("nil store accepted")
	}
}

func TestResolverErrorsHoldNoSecret(t *testing.T) {
	const pasted = "sk-live-0123456789abcdef"
	r := Resolver(env(map[string]string{"K": pasted}), Memory())
	for _, ref := range []string{pasted, "env:" + pasted + "/x", "keychain:" + pasted + " ", "https:" + pasted, ""} {
		_, err := r(ref)
		if err == nil {
			t.Errorf("%q accepted", ref)
			continue
		}
		if strings.Contains(err.Error(), pasted) {
			t.Errorf("error quotes the pasted value: %v", err)
		}
		if CheckRef(ref) == nil {
			t.Errorf("CheckRef accepted %q", ref)
		}
	}
	if err := CheckRef("keychain:deepseek"); err != nil {
		t.Error(err)
	}
}
