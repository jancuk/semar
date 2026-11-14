package domain_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/domain"
)

func TestJSONKeysAreLowerCamel(t *testing.T) {
	use := domain.UseCase{Pass: "p", Scenario: "s", Step: "t"}
	a, err := json.Marshal(domain.Action{Use: use})
	if err != nil {
		t.Fatal(err)
	}
	o, err := json.Marshal(domain.Activity{Use: use, Object: &domain.ObjectRef{Type: "order", ID: "1"}})
	if err != nil {
		t.Fatal(err)
	}
	got := string(a) + string(o)
	for _, k := range []string{`"pass"`, `"scenario"`, `"step"`, `"type"`, `"id"`} {
		if !strings.Contains(got, k) {
			t.Errorf("missing key %s in %s", k, got)
		}
	}
	for _, k := range []string{`"Pass"`, `"Scenario"`, `"Step"`, `"Type"`, `"ID"`} {
		if strings.Contains(got, k) {
			t.Errorf("unexpected key %s in %s", k, got)
		}
	}
}
