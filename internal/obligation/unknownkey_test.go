package obligation_test

import (
	"strings"
	"testing"

	"github.com/jancuk/semar/internal/obligation"
)

func TestParseNamesATypeMismatchWithoutAGoType(t *testing.T) {
	_, err := obligation.Parse([]byte("apiVersion: semar/v1\nkind: Obligations\nobligations: 3\n"), "o.yaml")
	if err == nil || !strings.Contains(err.Error(), "obligations must be a list") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "obligation.") {
		t.Fatalf("err names a Go type: %v", err)
	}
}
