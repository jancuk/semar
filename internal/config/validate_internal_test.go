package config

import (
	"fmt"
	"strings"
	"testing"
)

// YAML text cannot carry invalid UTF-8, so the rule is tested on the value directly.
func TestValidateDataRefusesInvalidUTF8(t *testing.T) {
	var got []string
	validateData("environments.uat.data", map[string]string{"a": "x\xffy", "b": "ok"}, func(format string, args ...any) {
		got = append(got, strings.TrimSpace(fmt.Sprintf(format, args...)))
	})
	if len(got) != 1 || got[0] != "environments.uat.data.a: the value is not valid UTF-8" {
		t.Fatalf("got %q", got)
	}
}
