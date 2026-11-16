package fixture_test

import (
	"testing"

	"github.com/jancuk/semar/internal/fixture"
)

func TestParseNamesAnUnknownKeyInPlainWords(t *testing.T) {
	_, err := fixture.Parse([]byte("apiVersion: semar/v1\nkind: Fixture\nid: order.paid\nowners: x\n"), "f.yaml")
	if err == nil || err.Error() != "f.yaml:4: unknown key owners" {
		t.Fatalf("err = %v", err)
	}
}
