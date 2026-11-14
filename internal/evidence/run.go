package evidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jancuk/semar/internal/atomicfile"
	"github.com/jancuk/semar/internal/domain"
	"github.com/jancuk/semar/internal/idcheck"
)

// Run is one run directory that is being written. It is safe for concurrent use.
type Run struct {
	mu       sync.Mutex
	id       domain.RunID
	dir      string
	activity *os.File
	closed   bool
}

func (s *Store) NewRun(id domain.RunID) (*Run, error) {
	dir, err := s.runDir(id)
	if err != nil {
		return nil, fmt.Errorf("create run: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dir), atomicfile.DirMode); err != nil {
		return nil, fmt.Errorf("create run %s: %w", id, err)
	}
	if err := os.Mkdir(dir, atomicfile.DirMode); err != nil {
		return nil, fmt.Errorf("create run %s: %w", id, err)
	}
	return &Run{id: id, dir: dir}, nil
}

var errClosed = errors.New("the run is closed")

// runScope is the exchanges directory of an action with no scenario. An ID cannot start
// with an underscore, so it never collides with a scenario.
const runScope = "_run"

// WriteAction stores the action under exchanges/<scenario>/<seq>-<step>.json and returns that path.
// Without a step the name is <seq>.json, and without a scenario the directory is _run.
func (r *Run) WriteAction(a domain.Action) (string, error) {
	if a.Use.Scenario != "" {
		if err := idcheck.Check("scenario ID", string(a.Use.Scenario)); err != nil {
			return "", fmt.Errorf("store action %s: %w", a.ID, err)
		}
	}
	if a.Use.Step != "" {
		if err := idcheck.Check("step ID", string(a.Use.Step)); err != nil {
			return "", fmt.Errorf("store action %s: %w", a.ID, err)
		}
	}
	if a.Seq < 1 {
		return "", fmt.Errorf("store action %s: seq %d is not positive", a.ID, a.Seq)
	}
	dir := string(a.Use.Scenario)
	if dir == "" {
		dir = runScope
	}
	name := fmt.Sprintf("%04d", a.Seq)
	if a.Use.Step != "" {
		name += "-" + string(a.Use.Step)
	}
	rel := "exchanges/" + dir + "/" + name + ".json"
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return "", fmt.Errorf("store action %s: %w", a.ID, errClosed)
	}
	full, err := join(r.dir, filepath.FromSlash(rel))
	if err != nil {
		return "", err
	}
	if err := atomicfile.WriteJSON(full, a); err != nil {
		return "", err
	}
	return rel, nil
}

// AppendActivity adds one compact JSON line to activity.jsonl.
func (r *Run) AppendActivity(e domain.Activity) error {
	line, err := compactLine(e)
	if err != nil {
		return fmt.Errorf("encode activity %d: %w", e.Seq, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return fmt.Errorf("append activity %d: %w", e.Seq, errClosed)
	}
	if r.activity == nil {
		p := filepath.Join(r.dir, activityFile)
		f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, atomicfile.FileMode)
		if err != nil {
			return fmt.Errorf("open %s: %w", p, err)
		}
		r.activity = f
	}
	if _, err := r.activity.Write(line); err != nil {
		return fmt.Errorf("append activity %d: %w", e.Seq, err)
	}
	return nil
}

// WriteFile stores a file at a relative, slash-separated path below the run directory.
func (r *Run) WriteFile(rel string, data []byte) error {
	if rel == runFile || rel == activityFile {
		return fmt.Errorf("write %s: the name is reserved", rel)
	}
	for _, seg := range strings.Split(rel, "/") {
		if err := idcheck.Check("path segment", seg); err != nil {
			return fmt.Errorf("write %q: %w", rel, err)
		}
	}
	full, err := join(r.dir, filepath.FromSlash(rel))
	if err != nil {
		return fmt.Errorf("write %q: %w", rel, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return fmt.Errorf("write %q: %w", rel, errClosed)
	}
	return atomicfile.Write(full, data)
}

// Close lists the SHA-256 of every file in run.json and writes it last, so a run that has a run.json is complete.
func (r *Run) Close(run domain.Run) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return fmt.Errorf("close run %s: %w", r.id, errClosed)
	}
	if run.ID != "" && run.ID != r.id {
		return fmt.Errorf("close run %s: the record is for run %s", r.id, run.ID)
	}
	if r.activity != nil {
		syncErr := r.activity.Sync()
		closeErr := r.activity.Close()
		r.activity = nil
		if err := errors.Join(syncErr, closeErr); err != nil {
			return fmt.Errorf("close %s of run %s: %w", activityFile, r.id, err)
		}
	}
	files, problems, err := hashTree(r.dir)
	if err != nil {
		return fmt.Errorf("close run %s: %w", r.id, err)
	}
	if len(problems) > 0 {
		return fmt.Errorf("close run %s: %s", r.id, strings.Join(problems, "; "))
	}
	run.Schema = schema
	run.ID = r.id
	run.Files = files
	if err := atomicfile.WriteJSON(filepath.Join(r.dir, runFile), run); err != nil {
		return err
	}
	// closed only once run.json is in place, so a failed Close can be retried
	r.closed = true
	return nil
}

// compactLine is one JSON object and a newline, with <, >, and & as they are.
func compactLine(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
