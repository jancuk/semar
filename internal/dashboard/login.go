package dashboard

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jancuk/semar/internal/provider"
	"github.com/jancuk/semar/internal/userconfig"
)

const (
	keptLines   = 200
	maxLineRune = 2000
	keptJobs    = 32
)

const (
	stateRunning   = "running"
	stateDone      = "done"
	stateFailed    = "failed"
	stateCancelled = "cancelled"
)

// logins holds the sign-in jobs. ctx ends when the server stops, which ends every job.
type logins struct {
	mu     sync.Mutex
	jobs   map[string]*job
	order  []string
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (l *logins) init() {
	l.jobs = map[string]*job{}
	l.ctx, l.cancel = context.WithCancel(context.Background())
}

// stop cancels under the lock, so that startLogin either sees the end or has added its job to
// the wait group already.
func (l *logins) stop() {
	l.mu.Lock()
	l.cancel()
	l.mu.Unlock()
	l.wg.Wait()
}

type job struct {
	id, provider string

	mu        sync.Mutex
	lines     []string
	partial   []byte
	state     string
	detail    string
	cancelled bool
	cancel    context.CancelFunc
	done      chan struct{}
}

type jobBody struct {
	ID       string   `json:"id"`
	Provider string   `json:"provider"`
	State    string   `json:"state"`
	Detail   string   `json:"detail,omitempty"`
	Lines    []string `json:"lines"`
}

func (j *job) body() jobBody {
	j.mu.Lock()
	defer j.mu.Unlock()
	lines := append([]string{}, j.lines...)
	return jobBody{ID: j.id, Provider: j.provider, State: j.state, Detail: j.detail, Lines: lines}
}

func (s *Server) postLogin(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok || !decode(w, r, &struct{}{}, true) {
		return
	}
	f, _, err := userconfig.Load(s.d.Home)
	if err != nil {
		fail(w, http.StatusConflict, "The providers file has an error, so the dashboard starts nothing until it is fixed by hand: "+scrub(err.Error()))
		return
	}
	cfg, found := f.Find(id)
	if !found {
		fail(w, http.StatusNotFound, fmt.Sprintf("There is no provider with the ID %s.", id))
		return
	}
	inst, err := s.d.NewInstance(cfg)
	if err != nil {
		fail(w, http.StatusBadRequest, "This provider cannot be started: "+scrub(err.Error()))
		return
	}
	acc, ok := inst.(provider.Account)
	var argv []string
	if ok {
		argv = acc.LoginCommand()
	}
	if len(argv) == 0 {
		fail(w, http.StatusBadRequest, fmt.Sprintf("%s has no sign-in command. An API provider uses its key instead.", id))
		return
	}
	j, err := s.startLogin(id, argv)
	if err != nil {
		fail(w, statusOf(err), scrub(err.Error()))
		return
	}
	reply(w, http.StatusOK, j.body())
}

// startLogin returns the running job of the provider, or starts a new one.
func (s *Server) startLogin(providerID string, argv []string) (*job, error) {
	l := &s.logins
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ctx.Err() != nil {
		return nil, refuse(http.StatusServiceUnavailable, "The dashboard is stopping.")
	}
	for _, j := range l.jobs {
		if j.provider == providerID && j.body().State == stateRunning {
			return j, nil
		}
	}
	dir, err := os.MkdirTemp("", "semar-login-")
	if err != nil {
		return nil, refuse(http.StatusInternalServerError, "There is no empty directory for the sign-in: %v", err)
	}
	ctx, cancel := context.WithTimeout(l.ctx, s.loginLimit)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = provider.SetupEnv(s.d.Environ(), nil)
	cmd.Stdin = nil
	j := &job{id: newJobID(), provider: providerID, state: stateRunning, cancel: cancel, done: make(chan struct{})}
	cmd.Stdout = j
	cmd.Stderr = j
	cmd.WaitDelay = 2 * time.Second
	ownGroup(cmd)
	if err := cmd.Start(); err != nil {
		cancel()
		_ = os.RemoveAll(dir)
		return nil, refuse(http.StatusInternalServerError, "The sign-in command could not be started: %v", err)
	}
	l.jobs[j.id] = j
	l.order = append(l.order, j.id)
	l.forgetOld()
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		defer os.RemoveAll(dir)
		defer cancel()
		waitErr := cmd.Wait()
		killGroup(cmd)
		j.finish(ctx, waitErr, cmd, s.loginLimit)
	}()
	return j, nil
}

// forgetOld drops the oldest finished jobs beyond keptJobs.
func (l *logins) forgetOld() {
	for len(l.order) > keptJobs {
		dropped := false
		for i, id := range l.order {
			if l.jobs[id].body().State != stateRunning {
				delete(l.jobs, id)
				l.order = append(l.order[:i], l.order[i+1:]...)
				dropped = true
				break
			}
		}
		if !dropped {
			return
		}
	}
}

func (j *job) finish(ctx context.Context, waitErr error, cmd *exec.Cmd, limit time.Duration) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.flush()
	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	switch {
	case j.cancelled:
		j.state, j.detail = stateCancelled, "The sign-in was cancelled."
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		j.state, j.detail = stateFailed, "The sign-in did not finish within "+plainDuration(limit)+" and was stopped."
	case ctx.Err() != nil:
		j.state, j.detail = stateCancelled, "The dashboard stopped, and the sign-in with it."
	case code == 0 && (waitErr == nil || errors.Is(waitErr, exec.ErrWaitDelay)):
		j.state, j.detail = stateDone, "The sign-in command finished."
	default:
		j.state, j.detail = stateFailed, fmt.Sprintf("The sign-in command ended with exit code %d.", code)
	}
	close(j.done)
}

func (s *Server) lookupJob(w http.ResponseWriter, r *http.Request) (*job, bool) {
	s.logins.mu.Lock()
	j := s.logins.jobs[r.PathValue("job")]
	s.logins.mu.Unlock()
	if j == nil {
		fail(w, http.StatusNotFound, "There is no sign-in with that ID.")
		return nil, false
	}
	return j, true
}

func (s *Server) getLogin(w http.ResponseWriter, r *http.Request) {
	if j, ok := s.lookupJob(w, r); ok {
		reply(w, http.StatusOK, j.body())
	}
}

// deleteLogin cancels the job and waits until its process group is gone.
func (s *Server) deleteLogin(w http.ResponseWriter, r *http.Request) {
	if !decode(w, r, &struct{}{}, true) {
		return
	}
	j, ok := s.lookupJob(w, r)
	if !ok {
		return
	}
	j.mu.Lock()
	if j.state == stateRunning {
		j.cancelled = true
	}
	j.mu.Unlock()
	j.cancel()
	select {
	case <-j.done:
	case <-r.Context().Done():
		return
	}
	reply(w, http.StatusOK, j.body())
}

func newJobID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Write takes the output of the child and keeps the last lines.
func (j *job) Write(p []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, b := range p {
		if b == '\n' || b == '\r' {
			j.flush()
			continue
		}
		// A line without an end is cut here, and the rest of it is dropped.
		if len(j.partial) < maxLineRune*utf8.UTFMax {
			j.partial = append(j.partial, b)
		}
	}
	return len(p), nil
}

func (j *job) flush() {
	if len(j.partial) == 0 {
		return
	}
	line := cleanLine(string(j.partial))
	j.partial = j.partial[:0]
	if line == "" {
		return
	}
	j.lines = append(j.lines, line)
	if extra := len(j.lines) - keptLines; extra > 0 {
		j.lines = append(j.lines[:0], j.lines[extra:]...)
	}
}

var terminalCodes = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-_]`)

// cleanLine removes terminal codes and control characters, cuts the line, and removes an
// e-mail address or what looks like a credential.
func cleanLine(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = terminalCodes.ReplaceAllString(s, "")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case unicode.IsControl(r), r == ' ', r == ' ':
			return -1
		}
		return r
	}, s)
	s = strings.TrimRight(s, " ")
	if r := []rune(s); len(r) > maxLineRune {
		s = string(r[:maxLineRune])
	}
	return scrub(s)
}
