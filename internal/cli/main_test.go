package cli_test

import (
	"fmt"
	"os"
	"testing"
)

// TestMain points the person's home at an empty directory for every test, including the ones
// that call Run with the defaults, so that no test reads or writes the real one.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "semar-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Setenv("SEMAR_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
