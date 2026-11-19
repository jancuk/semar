package fixture

import (
	"bytes"
	"fmt"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/domain"
)

// Marshal writes a fixture as the file that Parse reads back to the same fixture. A key with no value
// is left out. The header says who drafted the file.
func Marshal(f domain.Fixture) ([]byte, error) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# Drafted by semar plan. Read it, edit it if needed, and plan again.\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(f); err != nil {
		return nil, fmt.Errorf("write fixture %s: %w", f.ID, err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("write fixture %s: %w", f.ID, err)
	}
	return buf.Bytes(), nil
}
