package journey

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sync"

	yaml "go.yaml.in/yaml/v4"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/flow"
	"github.com/jancuk/semar/internal/idcheck"
)

const fileMode = 0o644

// Path is the file of a journey, relative to the repository, with slashes.
func Path(id domain.JourneyID) string { return relDir + "/" + string(id) + ".yaml" }

// Marshal writes the journey as block YAML. The keys come in the order of the fields of the types, and
// the keys of a request body are sorted, so the same journey always gives the same bytes. An empty
// apiVersion or kind is written as the value that Parse requires. It refuses a journey that Parse would
// read back as another journey, or not at all.
func Marshal(j domain.Journey) ([]byte, error) {
	if j.APIVersion == "" {
		j.APIVersion = "semar/v1"
	}
	if j.Kind == "" {
		j.Kind = "Journey"
	}
	buf, err := encode(j)
	if err != nil {
		return nil, fmt.Errorf("write journey %s: %w", j.ID, err)
	}
	got, err := Parse(buf, "journey "+string(j.ID))
	if err != nil {
		return nil, fmt.Errorf("journey %s cannot be written as YAML that Parse accepts: %w", j.ID, err)
	}
	if !reflect.DeepEqual(normal(got), normal(j)) {
		// The numbers of a request body can come back as another Go type. The bytes tell whether the
		// journey is the same.
		again, err := encode(got)
		if err != nil || !bytes.Equal(again, buf) {
			return nil, fmt.Errorf("journey %s cannot be written: Parse would read it back as a different journey", j.ID)
		}
	}
	return buf, nil
}

// encode is the YAML of a journey. A request has no service of its own in a journey, because its leg has
// one, so an empty service is left out, as the files that people write leave it out.
func encode(j domain.Journey) ([]byte, error) {
	var doc yaml.Node
	if err := doc.Encode(j); err != nil {
		return nil, err
	}
	dropEmptyServices(&doc)
	var buf bytes.Buffer
	w := yaml.NewEncoder(&buf)
	w.SetIndent(2)
	if err := w.Encode(&doc); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// dropEmptyServices removes service: "" from each request of the document.
func dropEmptyServices(n *yaml.Node) {
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == "request" && n.Content[i+1].Kind == yaml.MappingNode {
				req := n.Content[i+1]
				for k := 0; k+1 < len(req.Content); k += 2 {
					if req.Content[k].Value == "service" && req.Content[k+1].Kind == yaml.ScalarNode && req.Content[k+1].Value == "" {
						req.Content = append(req.Content[:k], req.Content[k+2:]...)
						break
					}
				}
			}
		}
	}
	for _, c := range n.Content {
		dropEmptyServices(c)
	}
}

// normal makes the empty collections of a journey nil, as Parse reads them.
func normal(j domain.Journey) domain.Journey {
	if len(j.Given) == 0 {
		j.Given = nil
	}
	if len(j.Invariants) == 0 {
		j.Invariants = nil
	}
	legs := make([]domain.Leg, len(j.Legs))
	for i, l := range j.Legs {
		if len(l.Needs) == 0 {
			l.Needs = nil
		}
		if len(l.Outputs) == 0 {
			l.Outputs = nil
		}
		steps := make([]domain.LegStep, len(l.Steps))
		for k, st := range l.Steps {
			if len(st.Expect) == 0 {
				st.Expect = nil
			}
			if len(st.Captures) == 0 {
				st.Captures = nil
			}
			if len(st.With) == 0 {
				st.With = nil
			}
			if len(st.Request.Query) == 0 {
				st.Request.Query = nil
			}
			if len(st.Request.Headers) == 0 {
				st.Request.Headers = nil
			}
			steps[k] = st
		}
		l.Steps = steps
		legs[i] = l
	}
	j.Legs = legs
	return j
}

// Saved is a journey that Write stored. It can undo that one write.
type Saved struct {
	Path   string
	Digest string

	repo string
	prev []byte
	had  bool
	done bool
	mu   sync.Mutex
}

// Write stores the journey in .semar/journeys/<id>.yaml, atomically, and never through a symbolic link
// or outside the repository. A journey of that ID that is there is an error that wraps fs.ErrExist,
// unless replace is true: Saved can then put the old file back. A file <id>.yml is a second file of the
// same journey, and is refused too.
func Write(repo string, j domain.Journey, replace bool) (*Saved, error) {
	if err := idcheck.Check("journey ID", string(j.ID)); err != nil {
		return nil, err
	}
	data, err := Marshal(j)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(repo)
	if err != nil {
		return nil, fmt.Errorf("write journey %s: %w", j.ID, err)
	}
	defer root.Close()
	if _, err := atomicfile.MkdirIn(root, relDir); err != nil {
		return nil, err
	}
	rel := Path(j.ID)
	other := relDir + "/" + string(j.ID) + ".yml"
	if _, err := root.Lstat(other); err == nil {
		return nil, fmt.Errorf("%s exists: a journey has one file, so remove it first", other)
	}
	s := &Saved{Path: rel, Digest: flow.Digest(data), repo: repo}
	switch prev, err := readIn(root, rel); {
	case err == nil:
		if !replace {
			return nil, fmt.Errorf("%s exists already: %w", rel, fs.ErrExist)
		}
		s.prev, s.had = prev, true
	case errors.Is(err, fs.ErrNotExist):
	default:
		return nil, err
	}
	if err := atomicfile.WriteIn(root, rel, data, fileMode); err != nil {
		return nil, err
	}
	return s, nil
}

// Undo puts back what Write replaced, or removes the file when there was none. It runs once, and it
// refuses when the file is no longer the one that Write stored, so a later edit is never lost.
func (s *Saved) Undo() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return fmt.Errorf("%s was already undone", s.Path)
	}
	s.done = true
	root, err := os.OpenRoot(s.repo)
	if err != nil {
		return fmt.Errorf("undo %s: %w", s.Path, err)
	}
	defer root.Close()
	cur, err := readIn(root, s.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("undo %s: the file is gone", s.Path)
	case err != nil:
		return err
	case flow.Digest(cur) != s.Digest:
		return fmt.Errorf("undo %s: the file changed since it was saved, so it is left as it is", s.Path)
	}
	if !s.had {
		if err := root.Remove(s.Path); err != nil {
			return fmt.Errorf("undo %s: %w", s.Path, err)
		}
		return nil
	}
	return atomicfile.WriteIn(root, s.Path, s.prev, fileMode)
}

// readIn reads a regular file of limited size below root, and refuses a link.
func readIn(root *os.Root, rel string) ([]byte, error) {
	info, err := root.Lstat(rel)
	switch {
	case err != nil:
		return nil, err
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("%s is not a regular file, and Semar replaces only real files", filepath.Join(root.Name(), rel))
	case info.Size() > MaxFileSize:
		return nil, fmt.Errorf("%s is larger than %d KiB", filepath.Join(root.Name(), rel), MaxFileSize>>10)
	}
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, MaxFileSize+1))
}
