// Package takeover is the baton between the agent and a person who both want to drive one device or
// browser. It holds who owns the baton, counts the agent's writes in flight, and makes sure no agent
// write starts or keeps running once a person holds control.
//
// Locking: Baton.mu guards state only. It is never held across I/O, a callback, or a wait. The lock
// order for callers is Baton.mu, then Manager.mu, then Session.mu.
package takeover

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Owner says who holds the baton.
type Owner uint8

const (
	// Agent drives. It is the zero value.
	Agent Owner = iota
	// Human drives.
	Human
)

func (o Owner) String() string {
	switch o {
	case Agent:
		return "agent"
	case Human:
		return "human"
	}
	return fmt.Sprintf("owner(%d)", uint8(o))
}

// Defaults and bounds.
const (
	DefaultGrace            = 30 * time.Second
	DefaultHeartbeatTimeout = 120 * time.Second
	MaxWatch                = 25 * time.Second
	MaxEvents               = 200
	defaultCancelWait       = 2 * time.Second
	// defaultReadGrace is how long an agent read that is already running when a person takes control may go
	// on: a read of a minute (the device log) must not keep the person out of the device.
	defaultReadGrace = 2 * time.Second
	// defaultYieldGrace is how long writes in flight get to finish when the agent hands over control.
	defaultYieldGrace = 2 * time.Second
	pollTick          = time.Second
)

// ErrAgentInControl is returned to a person's write while the agent holds the baton.
var ErrAgentInControl = errors.New("the agent owns the device: press Pause first")

// ErrPauseAborted is returned by Pause when a Resume ended the pause before it finished.
var ErrPauseAborted = errors.New("the pause was cancelled by a hand-back")

// ErrHumanInControl is returned to an agent write while a person holds, or is about to hold, control.
type ErrHumanInControl struct {
	Reason string
	Since  time.Time
}

func (e *ErrHumanInControl) Error() string {
	if e.Reason == "" {
		return "a person is in control"
	}
	return "a person is in control: " + e.Reason
}

// State is a snapshot of the baton. Version is a revision counter: it grows on every ownership change,
// every setting change, and every recorded event, so a client can pass the Version it last saw to Watch.
type State struct {
	Version     uint64
	Owner       Owner
	Pending     bool // a Pause is in progress: new agent writes are already refused
	Asked       string
	Reason      string
	Note        string
	Since       time.Time
	Mode        string
	RecordHuman bool
	Gap         bool
}

// Event is one entry of the call feed. The caller scrubs Summary; the baton never sees arguments.
type Event struct {
	Seq     uint64
	At      time.Time
	Who     Owner
	Tool    string
	Surface string
	OK      bool
	MS      int64
	Summary string
}

type flight struct {
	cancel context.CancelFunc
}

// Baton is safe for concurrent use. There is one per process.
type Baton struct {
	clock func() time.Time

	mu           sync.Mutex
	st           State
	pendingSince time.Time
	rev          uint64
	events       []Event            // ring, oldest first, at most MaxEvents
	inflight     map[uint64]*flight // agent writes
	reads        map[uint64]*flight // agent reads: never refused, but cancelled when a Pause takes control
	nextFlight   uint64
	watchers     int
	lastBeat     time.Time
	hbTimeout    time.Duration
	cancelWait   time.Duration
	readGrace    time.Duration
	yieldGrace   time.Duration
	notice       string
	changed      chan struct{} // closed and replaced on every change
	onHandBack   []func()
}

// New returns a baton owned by the agent. A nil clock means time.Now.
func New(clock func() time.Time) *Baton {
	if clock == nil {
		clock = time.Now
	}
	b := &Baton{
		clock:      clock,
		inflight:   map[uint64]*flight{},
		reads:      map[uint64]*flight{},
		hbTimeout:  DefaultHeartbeatTimeout,
		cancelWait: defaultCancelWait,
		readGrace:  defaultReadGrace,
		yieldGrace: defaultYieldGrace,
		changed:    make(chan struct{}),
		st:         State{RecordHuman: true},
	}
	b.st.Since = clock()
	return b
}

// bump records a change and wakes every waiter. Caller holds b.mu.
func (b *Baton) bump() {
	b.rev++
	b.st.Version = b.rev
	close(b.changed)
	b.changed = make(chan struct{})
}

// SetHeartbeatTimeout changes how long a person may hold control without a studio poll.
func (b *Baton) SetHeartbeatTimeout(d time.Duration) {
	b.mu.Lock()
	if d <= 0 {
		d = DefaultHeartbeatTimeout
	}
	b.hbTimeout = d
	b.mu.Unlock()
}

// OnHandBack registers fn to run, outside the lock, each time control returns to the agent.
func (b *Baton) OnHandBack(fn func()) {
	if fn == nil {
		return
	}
	b.mu.Lock()
	b.onHandBack = append(b.onHandBack, fn)
	b.mu.Unlock()
}

// State returns a snapshot. It also runs the heartbeat check.
func (b *Baton) State() State {
	b.CheckHeartbeat()
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.st
}

// Pending reports whether a Pause is in progress or a person holds control. flow.Options.Before uses it
// to stop a running flow at the next step boundary.
func (b *Baton) Pending() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.st.Pending || b.st.Owner == Human
}

// SetMode records the studio mode ("live" or "record").
func (b *Baton) SetMode(mode string) {
	b.mu.Lock()
	if b.st.Mode != mode {
		b.st.Mode = mode
		b.bump()
	}
	b.mu.Unlock()
}

// SetRecordHuman sets whether a person's actions are recorded as steps.
func (b *Baton) SetRecordHuman(on bool) {
	b.mu.Lock()
	if b.st.RecordHuman != on {
		b.st.RecordHuman = on
		b.bump()
	}
	b.mu.Unlock()
}

// SetGap sets whether the person did steps that were not recorded.
func (b *Baton) SetGap(gap bool) {
	b.mu.Lock()
	if b.st.Gap != gap {
		b.st.Gap = gap
		b.bump()
	}
	b.mu.Unlock()
}

// RecordEvent appends e to the feed, assigning Seq (and At when zero). The feed keeps the last
// MaxEvents.
func (b *Baton) RecordEvent(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if e.At.IsZero() {
		e.At = b.clock()
	}
	b.bump()
	e.Seq = b.rev
	if len(b.events) >= MaxEvents {
		copy(b.events, b.events[1:])
		b.events = b.events[:MaxEvents-1]
	}
	b.events = append(b.events, e)
}

// Enter admits a call. Reads are always admitted. An agent write is refused with *ErrHumanInControl
// while a person owns the baton or a Pause is pending; a human write is refused with ErrAgentInControl
// unless the person owns the baton. The returned context is cancelled when Pause force-stops the write
// or when leave runs. leave must be called exactly once (extra calls are harmless).
func (b *Baton) Enter(ctx context.Context, who Owner, write bool) (context.Context, func(ok bool, summary string), error) {
	b.CheckHeartbeat()
	b.mu.Lock()
	if write {
		switch who {
		case Agent:
			if b.st.Owner == Human || b.st.Pending {
				err := &ErrHumanInControl{Reason: b.st.Reason, Since: b.sinceLocked()}
				b.mu.Unlock()
				return ctx, nil, err
			}
		case Human:
			if b.st.Owner != Human {
				b.mu.Unlock()
				return ctx, nil, ErrAgentInControl
			}
		}
	}
	ctx2, cancel := context.WithCancel(ctx)
	if who != Agent {
		b.mu.Unlock()
		return ctx2, func(bool, string) { cancel() }, nil
	}
	b.nextFlight++
	id := b.nextFlight
	flights := b.inflight
	if !write {
		flights = b.reads
	}
	flights[id] = &flight{cancel: cancel}
	b.mu.Unlock()

	var once sync.Once
	leave := func(bool, string) {
		once.Do(func() {
			cancel()
			b.mu.Lock()
			delete(flights, id)
			close(b.changed)
			b.changed = make(chan struct{})
			b.mu.Unlock()
		})
	}
	return ctx2, leave, nil
}

func (b *Baton) sinceLocked() time.Time {
	if b.st.Owner == Human {
		return b.st.Since
	}
	return b.pendingSince
}

// Pause takes control for a person. New agent writes are refused at once. In-flight agent writes get up
// to grace (30 s when grace <= 0) to finish, then their contexts are cancelled. It is idempotent when a
// person already holds control.
func (b *Baton) Pause(ctx context.Context, reason string, grace time.Duration) error {
	if grace <= 0 {
		grace = DefaultGrace
	}
	b.mu.Lock()
	if b.st.Owner == Human {
		b.mu.Unlock()
		return nil
	}
	if !b.st.Pending {
		b.st.Pending = true
		b.st.Reason = reason
		b.pendingSince = b.clock()
		b.bump()
	}
	stopReads := b.cancelReadsAfter(minDuration(grace, b.readGrace))
	b.mu.Unlock()

	// The reads that run now are cancelled after their grace even when the pause finishes first: the
	// person has the device then. A pause that fails or is ended leaves them alone.
	if err := b.drain(ctx, grace); err != nil {
		stopReads()
		b.mu.Lock()
		if b.st.Pending && b.st.Owner == Agent {
			b.st.Pending = false
			b.st.Reason = ""
			b.bump()
		}
		b.mu.Unlock()
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.st.Owner == Human {
		return nil // a concurrent Pause won
	}
	if !b.st.Pending {
		stopReads()
		return ErrPauseAborted
	}
	now := b.clock()
	b.st.Owner = Human
	b.st.Pending = false
	b.st.Reason = reason
	b.st.Since = now
	b.lastBeat = now
	b.bump()
	return nil
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// cancelReadsAfter cancels the agent reads that are in flight now once d has passed. Reads that start
// later are not touched: reads work while a person drives. The returned function stops the timer. Caller
// holds b.mu.
func (b *Baton) cancelReadsAfter(d time.Duration) (stop func()) {
	if len(b.reads) == 0 {
		return func() {}
	}
	cancels := make([]context.CancelFunc, 0, len(b.reads))
	for _, f := range b.reads {
		cancels = append(cancels, f.cancel)
	}
	t := time.AfterFunc(d, func() {
		for _, c := range cancels {
			c()
		}
	})
	return func() { t.Stop() }
}

// drain waits for in-flight agent writes: up to grace to finish on their own, then it cancels them and
// waits briefly for them to leave.
func (b *Baton) drain(ctx context.Context, grace time.Duration) error {
	if b.waitIdle(ctx, grace) {
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	for _, f := range b.inflight {
		f.cancel()
	}
	wait := b.cancelWait
	b.mu.Unlock()
	b.waitIdle(ctx, wait)
	return ctx.Err()
}

// waitIdle blocks until nothing is in flight, the timeout passes, or ctx ends. It reports whether it
// ended because ctx was done.
func (b *Baton) waitIdle(ctx context.Context, timeout time.Duration) (ctxDone bool) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		b.mu.Lock()
		n := len(b.inflight)
		ch := b.changed
		b.mu.Unlock()
		if n == 0 {
			return false
		}
		select {
		case <-ch:
		case <-timer.C:
			return false
		case <-ctx.Done():
			return true
		}
	}
}

// Yield is the agent handing control to a person (semar_request_human). Like Pause it refuses new agent
// writes at once, gives the writes in flight a moment (yieldGrace) to finish, cancels what is left, and
// then hands over. The semar_request_human call itself is on the control surface, which never enters the
// baton, so it is not one of the writes it waits for. A hand-back that comes in meanwhile ends the
// yield: the agent keeps control.
func (b *Baton) Yield(reason string) {
	b.mu.Lock()
	if b.st.Owner == Human {
		b.handOverLocked(reason)
		b.mu.Unlock()
		return
	}
	if !b.st.Pending {
		b.st.Pending = true
		b.st.Reason = reason
		b.pendingSince = b.clock()
		b.bump()
	}
	grace := b.yieldGrace
	stopReads := b.cancelReadsAfter(minDuration(grace, b.readGrace))
	b.mu.Unlock()

	ctx := context.Background()
	if !b.waitIdle(ctx, grace) {
		b.mu.Lock()
		for _, f := range b.inflight {
			f.cancel()
		}
		wait := b.cancelWait
		b.mu.Unlock()
		b.waitIdle(ctx, wait)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.st.Owner != Human && !b.st.Pending {
		stopReads()
		return // a Resume ended the pause that the yield began
	}
	b.handOverLocked(reason)
}

// handOverLocked makes the person the owner. Caller holds b.mu.
func (b *Baton) handOverLocked(reason string) {
	now := b.clock()
	b.st.Owner = Human
	b.st.Pending = false
	b.st.Asked = reason
	b.st.Reason = reason
	b.st.Since = now
	b.lastBeat = now
	b.bump()
}

// Resume gives control back to the agent with an optional note. It runs the OnHandBack callbacks outside
// the lock and queues a notice for the agent's next call. It does nothing when the agent already drives
// and no pause is pending.
func (b *Baton) Resume(note string) {
	b.mu.Lock()
	if b.st.Owner == Agent && !b.st.Pending {
		b.mu.Unlock()
		return
	}
	wasHuman := b.st.Owner == Human
	since := b.st.Since
	now := b.clock()
	b.st.Owner = Agent
	b.st.Pending = false
	b.st.Asked = ""
	b.st.Note = note
	if wasHuman {
		msg := fmt.Sprintf("a person had control %s to %s", since.Format("15:04"), now.Format("15:04"))
		if note != "" {
			msg += ": " + note
		}
		b.notice = msg + "; the screen may have changed"
	}
	b.bump()
	cbs := append([]func(){}, b.onHandBack...)
	b.mu.Unlock()
	for _, fn := range cbs {
		fn()
	}
}

// TakeNotice returns the hand-back notice for the agent's next call, once. It is empty when there is
// none.
func (b *Baton) TakeNotice() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := b.notice
	b.notice = ""
	return n
}

// CheckHeartbeat hands control back with "the studio went away" when a person holds control, no studio
// poll is in flight, and none arrived within the heartbeat timeout. It uses the injected clock.
func (b *Baton) CheckHeartbeat() {
	b.mu.Lock()
	expired := b.st.Owner == Human && b.watchers == 0 && b.clock().Sub(b.lastBeat) > b.hbTimeout
	b.mu.Unlock()
	if expired {
		b.Resume("the studio went away")
	}
}

// WaitAgent blocks until the agent owns the baton or ctx ends.
func (b *Baton) WaitAgent(ctx context.Context) (State, error) {
	tick := time.NewTicker(pollTick)
	defer tick.Stop()
	for {
		b.CheckHeartbeat()
		b.mu.Lock()
		st, ch := b.st, b.changed
		b.mu.Unlock()
		if st.Owner == Agent {
			return st, nil
		}
		select {
		case <-ch:
		case <-tick.C:
		case <-ctx.Done():
			return st, ctx.Err()
		}
	}
}

// Watch is the studio long-poll and its heartbeat. since is the Version the caller last saw. It returns
// as soon as the Version passes since, with the current state and the events newer than since, or after
// max (capped at 25 s) with whatever it has.
func (b *Baton) Watch(ctx context.Context, since uint64, max time.Duration) (State, []Event) {
	if max > MaxWatch {
		max = MaxWatch
	}
	b.mu.Lock()
	b.watchers++
	b.lastBeat = b.clock()
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.watchers--
		b.lastBeat = b.clock()
		b.mu.Unlock()
	}()

	timer := time.NewTimer(max)
	defer timer.Stop()
	expired := false
	for {
		b.mu.Lock()
		if b.rev > since || max <= 0 || expired || ctx.Err() != nil {
			st, evs := b.st, b.eventsAfter(since)
			b.mu.Unlock()
			return st, evs
		}
		ch := b.changed
		b.mu.Unlock()
		select {
		case <-ch:
		case <-timer.C:
			expired = true
		case <-ctx.Done():
		}
	}
}

// eventsAfter copies the events with Seq > since. Caller holds b.mu.
func (b *Baton) eventsAfter(since uint64) []Event {
	var out []Event
	for _, e := range b.events {
		if e.Seq > since {
			out = append(out, e)
		}
	}
	return out
}
