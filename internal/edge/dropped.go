package edge

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/yamlfile"
)

// DroppedFile is the file below the repository that remembers the edge cases that a person dropped.
const DroppedFile = ".semar/edge-dropped.yaml"

const (
	droppedAPIVersion = "semar/v1"
	droppedKind       = "EdgeDropped"
	maxDropped        = 2000
	maxDroppedBytes   = 1 << 20
	droppedMode       = 0o644
)

// Dropped is one edge case that a person dropped: the key of what it changed, and who dropped which
// scenario, when, and why. Plans after it do not draft that case again.
type Dropped struct {
	Key      string            `yaml:"key"`
	Scenario domain.ScenarioID `yaml:"scenario"`
	Reason   string            `yaml:"reason"`
	User     string            `yaml:"user"`
	Day      string            `yaml:"day"`
}

type droppedFile struct {
	APIVersion string    `yaml:"apiVersion"`
	Kind       string    `yaml:"kind"`
	Dropped    []Dropped `yaml:"dropped"`
}

// ReadDropped reads the dropped cases of the repository at dir. A missing file is no case. A file that
// cannot be read is an error, so that a plan never drafts again what a person dropped by mistake of a
// broken file.
func ReadDropped(dir string) ([]Dropped, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return readDropped(root)
}

func readDropped(root *os.Root) ([]Dropped, error) {
	f, err := root.Open(DroppedFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxDroppedBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDroppedBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", DroppedFile, maxDroppedBytes)
	}
	var file droppedFile
	if err := yamlfile.Decode(data, DroppedFile, &file); err != nil {
		return nil, err
	}
	if file.APIVersion != droppedAPIVersion || file.Kind != droppedKind {
		return nil, fmt.Errorf("%s: apiVersion must be %s and kind must be %s", DroppedFile, droppedAPIVersion, droppedKind)
	}
	return file.Dropped, nil
}

// Keys is the set of keys of the dropped cases.
func Keys(list []Dropped) map[string]bool {
	out := make(map[string]bool, len(list))
	for _, d := range list {
		out[d.Key] = true
	}
	return out
}

// AddDropped writes one more dropped case, unless its key is there already.
func AddDropped(dir string, d Dropped) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	list, err := readDropped(root)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(list, func(x Dropped) bool { return x.Key == d.Key }) {
		return nil
	}
	if len(list) >= maxDropped {
		return fmt.Errorf("%s holds %d cases already: remove the ones that no longer apply, in a reviewed commit", DroppedFile, len(list))
	}
	data, err := yaml.Marshal(droppedFile{APIVersion: droppedAPIVersion, Kind: droppedKind, Dropped: append(list, d)})
	if err != nil {
		return err
	}
	return atomicfile.WriteIn(root, DroppedFile, data, droppedMode)
}
