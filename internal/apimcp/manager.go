package apimcp

import (
	"encoding/json"
	"maps"
	"sync"

	"github.com/jancuk/semar/internal/mcp"
)

// entry is a step with what undoing it must restore.
type entry struct {
	step APIStep
	// before holds, for each variable that the step set, the value it had before (ok false: none).
	before map[string]prior
}

type prior struct {
	value string
	ok    bool
}

// Manager runs the API tools. One Manager serves one process: its variables and its log live as long as it
// does. It is safe for concurrent use, and it sends one request at a time.
type Manager struct {
	o Options

	callMu sync.Mutex // one request at a time
	mu     sync.Mutex
	vars   map[string]string
	steps  []entry
	calls  int // requests that were sent, for the step names r-<n>
}

// New makes a Manager.
func New(o Options) *Manager {
	return &Manager{o: o, vars: map[string]string{}}
}

// Tools gives the three API tools.
func (m *Manager) Tools() []mcp.Tool {
	return []mcp.Tool{m.listServicesTool(), m.listOperationsTool(), m.requestTool()}
}

// SetVar sets a session variable, such as a capture of another leg of a journey: <leg>.<name>.
func (m *Manager) SetVar(k, v string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vars[k] = v
}

// Var gives a session variable.
func (m *Manager) Var(k string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.vars[k]
	return v, ok
}

// Vars gives a copy of the session variables.
func (m *Manager) Vars() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return maps.Clone(m.vars)
}

// Seq is the number of steps in the log. A leg of a journey keeps it as its mark.
func (m *Manager) Seq() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.steps)
}

// Since gives the steps from index i on, as copies.
func (m *Manager) Since(i int) []APIStep {
	m.mu.Lock()
	defer m.mu.Unlock()
	if i < 0 {
		i = 0
	}
	if i >= len(m.steps) {
		return nil
	}
	out := make([]APIStep, 0, len(m.steps)-i)
	for _, e := range m.steps[i:] {
		out = append(out, cloneStep(e.step))
	}
	return out
}

// DropLast removes the last step, and puts back the variables that it captured. It reports whether
// there was a step.
func (m *Manager) DropLast() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(m.steps)
	if n == 0 {
		return false
	}
	last := m.steps[n-1]
	m.steps = m.steps[:n-1]
	for k, p := range last.before {
		if p.ok {
			m.vars[k] = p.value
		} else {
			delete(m.vars, k)
		}
	}
	return true
}

func cloneStep(s APIStep) APIStep {
	s.Captures = append(s.Captures[:0:0], s.Captures...)
	s.Expect = append(s.Expect[:0:0], s.Expect...)
	s.Request.Query = maps.Clone(s.Request.Query)
	s.Request.Headers = maps.Clone(s.Request.Headers)
	if s.Request.JSON != nil {
		// A deep copy through JSON keeps a caller from changing the log.
		if b, err := json.Marshal(s.Request.JSON); err == nil {
			var v any
			if json.Unmarshal(b, &v) == nil {
				s.Request.JSON = v
			}
		}
	}
	return s
}

func (m *Manager) scrub(text string) string {
	if m.o.Scrub == nil {
		return text
	}
	return m.o.Scrub.Text(text)
}

var _ Log = (*Manager)(nil)
